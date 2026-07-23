package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
)

var ErrNotLeader = errors.New("store: not the raft leader")

type command struct {
	Op    string `json:"op"`
	Key   string `json:"key"`
	Value []byte `json:"value,omitempty"`
}

type RaftConfig struct {
	NodeID string

	BindAddr string

	DataDir string

	Bootstrap bool
}

type RaftStore struct {
	raft      *raft.Raft
	transport *raft.NetworkTransport
	fsm       *fsm
	logStore  *raftboltdb.BoltStore
}

func NewRaftStore(cfg RaftConfig) (*RaftStore, error) {
	if cfg.NodeID == "" {
		return nil, errors.New("store: NodeID is required")
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("store: create data dir: %w", err)
	}

	addr, err := net.ResolveTCPAddr("tcp", cfg.BindAddr)
	if err != nil {
		return nil, fmt.Errorf("store: resolve bind addr: %w", err)
	}
	transport, err := raft.NewTCPTransport(cfg.BindAddr, addr, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("store: create transport: %w", err)
	}

	snapshots, err := raft.NewFileSnapshotStore(cfg.DataDir, 2, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("store: create snapshot store: %w", err)
	}

	logStore, err := raftboltdb.New(raftboltdb.Options{Path: filepath.Join(cfg.DataDir, "raft-log.bolt")})
	if err != nil {
		return nil, fmt.Errorf("store: create log store: %w", err)
	}

	f := &fsm{data: make(map[string][]byte)}

	raftCfg := raft.DefaultConfig()
	raftCfg.LocalID = raft.ServerID(cfg.NodeID)

	r, err := raft.NewRaft(raftCfg, f, logStore, logStore, snapshots, transport)
	if err != nil {
		return nil, fmt.Errorf("store: start raft: %w", err)
	}

	if cfg.Bootstrap {
		hasState, err := raft.HasExistingState(logStore, logStore, snapshots)
		if err != nil {
			return nil, fmt.Errorf("store: check existing state: %w", err)
		}
		if !hasState {
			bootstrapCfg := raft.Configuration{
				Servers: []raft.Server{{ID: raftCfg.LocalID, Address: transport.LocalAddr()}},
			}
			if err := r.BootstrapCluster(bootstrapCfg).Error(); err != nil {
				return nil, fmt.Errorf("store: bootstrap cluster: %w", err)
			}
		}
	}

	return &RaftStore{raft: r, transport: transport, fsm: f, logStore: logStore}, nil
}

func (s *RaftStore) IsLeader() bool {
	return s.raft.State() == raft.Leader
}

func (s *RaftStore) LeaderAddr() string {
	addr, _ := s.raft.LeaderWithID()
	return string(addr)
}

func (s *RaftStore) AddVoter(nodeID, addr string) error {
	if !s.IsLeader() {
		return ErrNotLeader
	}
	return s.raft.AddVoter(raft.ServerID(nodeID), raft.ServerAddress(addr), 0, 10*time.Second).Error()
}

func (s *RaftStore) Shutdown() error {
	if err := s.raft.Shutdown().Error(); err != nil {
		return err
	}
	return s.logStore.Close()
}

func (s *RaftStore) apply(cmd command) error {
	if !s.IsLeader() {
		return ErrNotLeader
	}
	b, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	return s.raft.Apply(b, 10*time.Second).Error()
}

func (s *RaftStore) Get(_ context.Context, key string) ([]byte, error) {
	return s.fsm.get(key)
}

func (s *RaftStore) Put(_ context.Context, key string, value []byte) error {
	return s.apply(command{Op: "put", Key: key, Value: value})
}

func (s *RaftStore) Delete(_ context.Context, key string) error {
	return s.apply(command{Op: "delete", Key: key})
}

func (s *RaftStore) List(_ context.Context, prefix string) (map[string][]byte, error) {
	return s.fsm.list(prefix), nil
}

type fsm struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func (f *fsm) Apply(log *raft.Log) any {
	var cmd command
	if err := json.Unmarshal(log.Data, &cmd); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	switch cmd.Op {
	case "put":
		f.data[cmd.Key] = cmd.Value
	case "delete":
		delete(f.data, cmd.Key)
	}
	return nil
}

func (f *fsm) get(key string) ([]byte, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	v, ok := f.data[key]
	if !ok {
		return nil, ErrNotFound
	}
	return v, nil
}

func (f *fsm) list(prefix string) map[string][]byte {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make(map[string][]byte)
	for k, v := range f.data {
		if strings.HasPrefix(k, prefix) {
			out[k] = v
		}
	}
	return out
}

func (f *fsm) Snapshot() (raft.FSMSnapshot, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	clone := make(map[string][]byte, len(f.data))
	maps.Copy(clone, f.data)
	return &fsmSnapshot{data: clone}, nil
}

func (f *fsm) Restore(rc io.ReadCloser) error {
	defer rc.Close()
	data := make(map[string][]byte)
	if err := json.NewDecoder(rc).Decode(&data); err != nil {
		return err
	}
	f.mu.Lock()
	f.data = data
	f.mu.Unlock()
	return nil
}

type fsmSnapshot struct {
	data map[string][]byte
}

func (s *fsmSnapshot) Persist(sink raft.SnapshotSink) error {
	if err := json.NewEncoder(sink).Encode(s.data); err != nil {
		sink.Cancel()
		return err
	}
	return sink.Close()
}

func (s *fsmSnapshot) Release() {}
