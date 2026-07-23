package controller

import (
	"context"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

func freeAddrForKeyRotationTest(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	addr := lis.Addr().String()
	lis.Close()
	return addr
}

func TestKeyRotationReconcilerRotatesOnSchedule(t *testing.T) {
	key := make([]byte, store.EncryptionKeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}

	s, err := store.NewRaftStore(store.RaftConfig{
		NodeID:        "node-1",
		BindAddr:      freeAddrForKeyRotationTest(t),
		DataDir:       t.TempDir(),
		Bootstrap:     true,
		EncryptionKey: key,
	})
	if err != nil {
		t.Fatalf("NewRaftStore: %v", err)
	}
	defer s.Shutdown()

	deadline := time.Now().Add(5 * time.Second)
	for !s.IsLeader() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !s.IsLeader() {
		t.Fatal("store never became leader")
	}

	ctx := context.Background()
	if err := s.Put(ctx, "/registry/secrets/default/foo", []byte("bar")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	r := NewKeyRotationReconciler(s, 30*time.Millisecond)
	runCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_ = r.Reconcile(runCtx)

	got, err := s.Get(ctx, "/registry/secrets/default/foo")
	if err != nil {
		t.Fatalf("Get after rotation cycles: %v", err)
	}
	if string(got) != "bar" {
		t.Errorf("Get after rotation cycles = %q, want %q", got, "bar")
	}
}
