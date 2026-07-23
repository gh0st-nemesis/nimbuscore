package apiserver

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

type backupService struct {
	v1.UnimplementedBackupServiceServer
	store store.Store
}

func NewBackupService(s store.Store) v1.BackupServiceServer {
	return &backupService{store: s}
}

func (svc *backupService) CreateBackup(ctx context.Context, _ *v1.CreateBackupRequest) (*v1.BackupData, error) {
	all, err := svc.store.List(ctx, "")
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list store contents: %v", err)
	}

	entries := make([]*v1.BackupEntry, 0, len(all))
	for k, v := range all {
		entries = append(entries, &v1.BackupEntry{Key: k, Value: v})
	}
	return &v1.BackupData{Entries: entries}, nil
}

func (svc *backupService) RestoreBackup(ctx context.Context, req *v1.RestoreBackupRequest) (*v1.RestoreBackupResponse, error) {
	entries := req.GetBackup().GetEntries()
	for _, e := range entries {
		if err := svc.store.Put(ctx, e.GetKey(), e.GetValue()); err != nil {
			return nil, status.Errorf(codes.Internal, "restore key %s: %v", e.GetKey(), err)
		}
	}
	return &v1.RestoreBackupResponse{RestoredCount: int32(len(entries))}, nil
}
