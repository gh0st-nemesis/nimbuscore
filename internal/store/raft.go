package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
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

	EncryptionKey []byte
}

type RaftStore struct {
	raft      *raft.Raft
	transport *raft.NetworkTransport
	fsm       *fsm
	logStore  *raftboltdb.BoltStore

	keyMu      sync.RWMutex
	encKey     []byte
	prevEncKey []byte
}

func NewRaftStore(cfg RaftConfig) (*RaftStore, error) {
	if cfg.NodeID == "" {
		return nil, errors.New("store: NodeID is required")
	}
	if len(cfg.EncryptionKey) == 0 {
		log.Printf("store: WARNING no encryption key configured — values will be replicated and persisted in plaintext (dev only)")
	} else if len(cfg.EncryptionKey) != EncryptionKeySize {
		return nil, fmt.Errorf("store: encryption key must be %d bytes, got %d", EncryptionKeySize, len(cfg.EncryptionKey))
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

	return &RaftStore{raft: r, transport: transport, fsm: f, logStore: logStore, encKey: cfg.EncryptionKey}, nil
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
	stored, err := s.fsm.get(key)
	if err != nil {
		return nil, err
	}
	return s.decrypt(stored)
}

func (s *RaftStore) Put(_ context.Context, key string, value []byte) error {
	stored, err := s.encrypt(value)
	if err != nil {
		return err
	}
	return s.apply(command{Op: "put", Key: key, Value: stored})
}

func (s *RaftStore) Delete(_ context.Context, key string) error {
	return s.apply(command{Op: "delete", Key: key})
}

func (s *RaftStore) List(_ context.Context, prefix string) (map[string][]byte, error) {
	stored := s.fsm.list(prefix)
	out := make(map[string][]byte, len(stored))
	for k, v := range stored {
		plain, err := s.decrypt(v)
		if err != nil {
			return nil, fmt.Errorf("store: decrypt %s: %w", k, err)
		}
		out[k] = plain
	}
	return out, nil
}

func (s *RaftStore) encrypt(plaintext []byte) ([]byte, error) {
	s.keyMu.RLock()
	key := s.encKey
	s.keyMu.RUnlock()

	if len(key) == 0 {
		return plaintext, nil
	}
	return encryptValue(key, plaintext)
}

func (s *RaftStore) decrypt(stored []byte) ([]byte, error) {
	s.keyMu.RLock()
	key, prevKey := s.encKey, s.prevEncKey
	s.keyMu.RUnlock()

	if len(key) == 0 {
		return stored, nil
	}
	plain, err := decryptValue(key, stored)
	if err == nil {
		return plain, nil
	}
	if len(prevKey) > 0 {
		if plain, prevErr := decryptValue(prevKey, stored); prevErr == nil {
			return plain, nil
		}
	}
	return nil, err
}

func (s *RaftStore) RotateEncryptionKey(newKey []byte) error {
	if len(newKey) != EncryptionKeySize {
		return fmt.Errorf("store: encryption key must be %d bytes, got %d", EncryptionKeySize, len(newKey))
	}
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	s.prevEncKey = s.encKey
	s.encKey = newKey
	return nil
}

func (s *RaftStore) ReencryptAll(ctx context.Context) (int, error) {
	all, err := s.List(ctx, "")
	if err != nil {
		return 0, fmt.Errorf("store: list values to re-encrypt: %w", err)
	}

	count := 0
	for key, value := range all {
		if err := s.Put(ctx, key, value); err != nil {
			return count, fmt.Errorf("store: re-encrypt %s: %w", key, err)
		}
		count++
	}

	s.keyMu.Lock()
	s.prevEncKey = nil
	s.keyMu.Unlock()

	return count, nil
}

type fsm struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func (f *fsm) Apply(raftLog *raft.Log) any {
	var cmd command
	if err := json.Unmarshal(raftLog.Data, &cmd); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	switch cmd.Op {
	case "put":
		f.data[cmd.Key] = cmd.Value
	case "delete":
		_, existed := f.data[cmd.Key]
		delete(f.data, cmd.Key)
		if existed {
			log.Printf("store: applied delete for %q (raft index=%d term=%d)", cmd.Key, raftLog.Index, raftLog.Term)
		}
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
