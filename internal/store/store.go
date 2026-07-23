// Package store defines the control plane's replicated key/value
// abstraction (design doc section 03).
package store

import (
	"context"
	"errors"
	"strings"
	"sync"
)

// ErrNotFound is returned when a lookup key has no matching entry.
var ErrNotFound = errors.New("store: key not found")

// Store is the interface every control-plane component depends on to
// read and write cluster state. Phase 1 ships an in-memory
// implementation to unblock the API server and scheduler; Phase 2
// replaces it with a hashicorp/raft + raft-boltdb backend, multi-node
// and chiffré au repos, per the design doc.
type Store interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) (map[string][]byte, error)
}

// memStore is a single-node, non-durable Store used until the Raft-backed
// implementation lands.
type memStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewMemStore returns an in-memory Store. Not durable, not replicated —
// scaffolding only.
func NewMemStore() Store {
	return &memStore{data: make(map[string][]byte)}
}

func (s *memStore) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	if !ok {
		return nil, ErrNotFound
	}
	return v, nil
}

func (s *memStore) Put(_ context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
	return nil
}

func (s *memStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

func (s *memStore) List(_ context.Context, prefix string) (map[string][]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string][]byte)
	for k, v := range s.data {
		if strings.HasPrefix(k, prefix) {
			out[k] = v
		}
	}
	return out, nil
}
