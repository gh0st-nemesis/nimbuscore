package apiserver

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

type secretService struct {
	v1.UnimplementedSecretServiceServer
	secrets *registry.Registry[*v1.Secret]
}

func NewSecretService(s store.Store) v1.SecretServiceServer {
	return &secretService{secrets: registry.New(s, "secrets", func() *v1.Secret { return &v1.Secret{} })}
}

func (svc *secretService) CreateSecret(ctx context.Context, req *v1.CreateSecretRequest) (*v1.Secret, error) {
	secret := req.GetSecret()
	meta := secret.GetMetadata()
	if meta.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "secret.metadata.name is required")
	}
	if err := svc.secrets.Put(ctx, meta.GetNamespace(), meta.GetName(), secret); err != nil {
		return nil, status.Errorf(codes.Internal, "create secret: %v", err)
	}
	return secret, nil
}

func (svc *secretService) GetSecret(ctx context.Context, req *v1.GetSecretRequest) (*v1.Secret, error) {
	secret, err := svc.secrets.Get(ctx, req.GetNamespace(), req.GetName())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "secret %s/%s: %v", req.GetNamespace(), req.GetName(), err)
	}
	return secret, nil
}

func (svc *secretService) ListSecrets(ctx context.Context, req *v1.ListSecretsRequest) (*v1.ListSecretsResponse, error) {
	items, err := svc.secrets.List(ctx, req.GetNamespace())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list secrets: %v", err)
	}
	return &v1.ListSecretsResponse{Items: items}, nil
}

func (svc *secretService) DeleteSecret(ctx context.Context, req *v1.DeleteSecretRequest) (*v1.DeleteSecretResponse, error) {
	if err := svc.secrets.Delete(ctx, req.GetNamespace(), req.GetName()); err != nil {
		return nil, status.Errorf(codes.Internal, "delete secret: %v", err)
	}
	return &v1.DeleteSecretResponse{}, nil
}
