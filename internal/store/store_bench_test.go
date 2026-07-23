package store

import (
	"context"
	"fmt"
	"testing"
)

func BenchmarkMemStorePutGet(b *testing.B) {
	s := NewMemStore()
	ctx := context.Background()

	b.ResetTimer()
	i := 0
	for b.Loop() {
		key := fmt.Sprintf("/registry/pods/default/web-%d", i)
		if err := s.Put(ctx, key, []byte("payload")); err != nil {
			b.Fatalf("Put: %v", err)
		}
		if _, err := s.Get(ctx, key); err != nil {
			b.Fatalf("Get: %v", err)
		}
		i++
	}
}

func BenchmarkRaftStorePutGet(b *testing.B) {
	s, err := NewRaftStore(RaftConfig{
		NodeID:    "bench-node",
		BindAddr:  freeAddr(b),
		DataDir:   b.TempDir(),
		Bootstrap: true,
	})
	if err != nil {
		b.Fatalf("NewRaftStore: %v", err)
	}
	defer s.Shutdown()

	ctx := context.Background()
	waitForLeader(b, s)

	b.ResetTimer()
	i := 0
	for b.Loop() {
		key := fmt.Sprintf("/registry/pods/default/web-%d", i)
		if err := s.Put(ctx, key, []byte("payload")); err != nil {
			b.Fatalf("Put: %v", err)
		}
		if _, err := s.Get(ctx, key); err != nil {
			b.Fatalf("Get: %v", err)
		}
		i++
	}
}
