package store

import (
	"context"
	"errors"
	"strings"
	"sync"
)

var ErrNotFound = errors.New("store: key not found")

type Store interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) (map[string][]byte, error)
}

type memStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

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
