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

// ErrNotLeader is returned by write operations issued against a
// RaftStore replica that is not currently the cluster leader. Unlike a
// generic key/value store, a Raft-replicated log only accepts writes on
// the leader — followers must redirect callers there. Phase 2 does not
// implement automatic proxying; callers (the API server) surface this to
// the client instead, the same contract etcd/Consul expose.
var ErrNotLeader = errors.New("store: not the raft leader")

// command is the payload appended to the Raft log. JSON keeps this
// internal-only encoding simple; it never crosses the wire outside the
// replication protocol itself, so there's no reason to reuse the
// Protobuf resource schema here.
type command struct {
	Op    string `json:"op"` // "put" or "delete"
	Key   string `json:"key"`
	Value []byte `json:"value,omitempty"`
}

// RaftConfig configures a multi-node RaftStore.
type RaftConfig struct {
	// NodeID must be unique across the cluster.
	NodeID string
	// BindAddr is the TCP address (host:port) the Raft transport
	// listens on for inter-node replication traffic — separate from the
	// gRPC API port.
	BindAddr string
	// DataDir stores the Raft log, stable store, and snapshots.
	DataDir string
	// Bootstrap starts a brand-new single-node cluster with this node as
	// its sole voter. Set only on the very first node; every other node
	// joins an existing leader instead (design doc section 08, phase 2:
	// "cluster réel multi-machines, tolérant à la panne d'un nœud").
	Bootstrap bool
}

// RaftStore is a Store replicated via the Raft consensus protocol
// (hashicorp/raft + BoltDB-backed log/stable store — design doc section
// 03), replacing the single-node in-memory Store from Phase 1.
type RaftStore struct {
	raft      *raft.Raft
	transport *raft.NetworkTransport
	fsm       *fsm
	logStore  *raftboltdb.BoltStore
}

// NewRaftStore starts (or rejoins) a Raft node according to cfg. Callers
// that are not the first node in the cluster must still call this — it
// starts the local Raft instance and its transport listener — and then
// have an existing leader call AddVoter with this node's NodeID and
// BindAddr (see AdminService.JoinRaft) before it becomes a full member.
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

// IsLeader reports whether this node currently holds Raft leadership.
func (s *RaftStore) IsLeader() bool {
	return s.raft.State() == raft.Leader
}

// LeaderAddr returns the Raft transport address of the current leader,
// if known.
func (s *RaftStore) LeaderAddr() string {
	addr, _ := s.raft.LeaderWithID()
	return string(addr)
}

// AddVoter adds a new node to the cluster's voter set. Only valid on the
// leader.
func (s *RaftStore) AddVoter(nodeID, addr string) error {
	if !s.IsLeader() {
		return ErrNotLeader
	}
	return s.raft.AddVoter(raft.ServerID(nodeID), raft.ServerAddress(addr), 0, 10*time.Second).Error()
}

// Shutdown stops the Raft node and closes its log store.
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

// fsm applies committed log entries to an in-memory map. Every node in
// the cluster runs its own fsm and reaches the same state by replaying
// the same log in the same order — the core Raft guarantee that makes
// reads safe on followers without contacting the leader.
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
