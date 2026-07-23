package apiserver_test

import (
	"context"
	"testing"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/apiserver"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

func TestBackupAndRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	source := store.NewMemStore()

	secrets := registry.New(source, "secrets", func() *v1.Secret { return &v1.Secret{} })
	if err := secrets.Put(ctx, "default", "db-password", &v1.Secret{
		Metadata: &v1.ObjectMeta{Name: "db-password", Namespace: "default"},
		Data:     map[string][]byte{"password": []byte("hunter2")},
	}); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	pods := registry.New(source, "pods", func() *v1.Pod { return &v1.Pod{} })
	if err := pods.Put(ctx, "default", "web-0", &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: "web-0", Namespace: "default"},
	}); err != nil {
		t.Fatalf("seed pod: %v", err)
	}

	backupSvc := apiserver.NewBackupService(source)
	dump, err := backupSvc.CreateBackup(ctx, &v1.CreateBackupRequest{})
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	if len(dump.GetEntries()) != 2 {
		t.Fatalf("backup has %d entries, want 2", len(dump.GetEntries()))
	}

	dest := store.NewMemStore()
	restoreSvc := apiserver.NewBackupService(dest)
	resp, err := restoreSvc.RestoreBackup(ctx, &v1.RestoreBackupRequest{Backup: dump})
	if err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}
	if resp.GetRestoredCount() != 2 {
		t.Errorf("restored_count = %d, want 2", resp.GetRestoredCount())
	}

	restoredSecrets := registry.New(dest, "secrets", func() *v1.Secret { return &v1.Secret{} })
	got, err := restoredSecrets.Get(ctx, "default", "db-password")
	if err != nil {
		t.Fatalf("Get restored secret: %v", err)
	}
	if string(got.GetData()["password"]) != "hunter2" {
		t.Errorf("restored secret password = %q, want %q", got.GetData()["password"], "hunter2")
	}

	restoredPods := registry.New(dest, "pods", func() *v1.Pod { return &v1.Pod{} })
	if _, err := restoredPods.Get(ctx, "default", "web-0"); err != nil {
		t.Fatalf("Get restored pod: %v", err)
	}
}

func TestBackupOfEmptyStoreReturnsNoEntries(t *testing.T) {
	svc := apiserver.NewBackupService(store.NewMemStore())
	dump, err := svc.CreateBackup(context.Background(), &v1.CreateBackupRequest{})
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	if len(dump.GetEntries()) != 0 {
		t.Errorf("backup of empty store has %d entries, want 0", len(dump.GetEntries()))
	}
}
