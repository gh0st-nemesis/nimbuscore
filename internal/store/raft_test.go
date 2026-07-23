package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func freeAddr(t testing.TB) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	addr := lis.Addr().String()
	lis.Close()
	return addr
}

func TestRaftStoreSingleNodePutGet(t *testing.T) {
	cfg := RaftConfig{
		NodeID:    "node-1",
		BindAddr:  "127.0.0.1:0",
		DataDir:   t.TempDir(),
		Bootstrap: true,
	}

	cfg.BindAddr = freeAddr(t)

	s, err := NewRaftStore(cfg)
	if err != nil {
		t.Fatalf("NewRaftStore: %v", err)
	}
	defer s.Shutdown()

	waitForLeader(t, s)

	ctx := context.Background()
	if err := s.Put(ctx, "/registry/pods/default/web-0", []byte("hello")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := s.Get(ctx, "/registry/pods/default/web-0")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("Get = %q, want %q", got, "hello")
	}

	list, err := s.List(ctx, "/registry/pods/default/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List returned %d entries, want 1", len(list))
	}

	if err := s.Delete(ctx, "/registry/pods/default/web-0"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, "/registry/pods/default/web-0"); err != ErrNotFound {
		t.Fatalf("Get after delete = %v, want ErrNotFound", err)
	}
}

func TestRaftStoreEncryptsAtRest(t *testing.T) {
	const secret = "super-secret-payload-nobody-should-see-on-disk"

	key := make([]byte, EncryptionKeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}

	dataDir := t.TempDir()
	cfg := RaftConfig{
		NodeID:        "node-1",
		BindAddr:      freeAddr(t),
		DataDir:       dataDir,
		Bootstrap:     true,
		EncryptionKey: key,
	}

	s, err := NewRaftStore(cfg)
	if err != nil {
		t.Fatalf("NewRaftStore: %v", err)
	}
	waitForLeader(t, s)

	ctx := context.Background()
	if err := s.Put(ctx, "/registry/pods/default/web-0", []byte(secret)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := s.Get(ctx, "/registry/pods/default/web-0")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != secret {
		t.Fatalf("Get = %q, want %q", got, secret)
	}

	if err := s.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dataDir, "raft-log.bolt"))
	if err != nil {
		t.Fatalf("read log store file: %v", err)
	}
	if bytes.Contains(raw, []byte(secret)) {
		t.Fatal("plaintext secret found in the on-disk Raft log — encryption at rest is not working")
	}
}

func waitForLeader(t testing.TB, s *RaftStore) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.IsLeader() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for single-node raft to elect itself leader")
}
