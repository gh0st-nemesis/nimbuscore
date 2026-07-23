package store

import (
	"context"
	"crypto/rand"
	"testing"
)

func TestRotateEncryptionKeyKeepsDataReadableThenDropsOldKey(t *testing.T) {
	keyA := make([]byte, EncryptionKeySize)
	if _, err := rand.Read(keyA); err != nil {
		t.Fatalf("generate keyA: %v", err)
	}
	keyB := make([]byte, EncryptionKeySize)
	if _, err := rand.Read(keyB); err != nil {
		t.Fatalf("generate keyB: %v", err)
	}

	s, err := NewRaftStore(RaftConfig{
		NodeID:        "node-1",
		BindAddr:      freeAddr(t),
		DataDir:       t.TempDir(),
		Bootstrap:     true,
		EncryptionKey: keyA,
	})
	if err != nil {
		t.Fatalf("NewRaftStore: %v", err)
	}
	defer s.Shutdown()
	waitForLeader(t, s)

	ctx := context.Background()
	if err := s.Put(ctx, "/registry/secrets/default/foo", []byte("bar")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rawBefore, err := s.fsm.get("/registry/secrets/default/foo")
	if err != nil {
		t.Fatalf("fsm.get: %v", err)
	}
	if _, err := decryptValue(keyA, rawBefore); err != nil {
		t.Fatalf("expected keyA to decrypt the pre-rotation ciphertext: %v", err)
	}

	if err := s.RotateEncryptionKey(keyB); err != nil {
		t.Fatalf("RotateEncryptionKey: %v", err)
	}

	got, err := s.Get(ctx, "/registry/secrets/default/foo")
	if err != nil {
		t.Fatalf("Get after rotation (should fall back to previous key): %v", err)
	}
	if string(got) != "bar" {
		t.Fatalf("Get after rotation = %q, want %q", got, "bar")
	}

	if err := s.Put(ctx, "/registry/secrets/default/baz", []byte("qux")); err != nil {
		t.Fatalf("Put after rotation: %v", err)
	}

	n, err := s.ReencryptAll(ctx)
	if err != nil {
		t.Fatalf("ReencryptAll: %v", err)
	}
	if n != 2 {
		t.Errorf("ReencryptAll processed %d entries, want 2", n)
	}

	for key, want := range map[string]string{
		"/registry/secrets/default/foo": "bar",
		"/registry/secrets/default/baz": "qux",
	} {
		got, err := s.Get(ctx, key)
		if err != nil {
			t.Fatalf("Get %s after re-encryption: %v", key, err)
		}
		if string(got) != want {
			t.Errorf("Get %s after re-encryption = %q, want %q", key, got, want)
		}
	}

	rawAfter, err := s.fsm.get("/registry/secrets/default/foo")
	if err != nil {
		t.Fatalf("fsm.get after re-encryption: %v", err)
	}
	if _, err := decryptValue(keyA, rawAfter); err == nil {
		t.Fatal("the old key can still decrypt data after ReencryptAll — rotation did not complete")
	}
	if _, err := decryptValue(keyB, rawAfter); err != nil {
		t.Fatalf("expected keyB to decrypt the post-rotation ciphertext: %v", err)
	}
}
