package apiserver

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

type namespaceService struct {
	v1.UnimplementedNamespaceServiceServer
	namespaces *registry.Registry[*v1.Namespace]
}

func NewNamespaceService(s store.Store) v1.NamespaceServiceServer {
	return &namespaceService{namespaces: registry.New(s, "namespaces", func() *v1.Namespace { return &v1.Namespace{} })}
}

func (svc *namespaceService) CreateNamespace(ctx context.Context, req *v1.CreateNamespaceRequest) (*v1.Namespace, error) {
	ns := req.GetNamespace()
	name := ns.GetMetadata().GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "namespace.metadata.name is required")
	}
	if _, err := svc.namespaces.Get(ctx, "", name); err == nil {
		return nil, status.Errorf(codes.AlreadyExists, "namespace %q already exists", name)
	}
	if err := svc.namespaces.Put(ctx, "", name, ns); err != nil {
		return nil, status.Errorf(codes.Internal, "create namespace: %v", err)
	}
	return ns, nil
}

func (svc *namespaceService) GetNamespace(ctx context.Context, req *v1.GetNamespaceRequest) (*v1.Namespace, error) {
	ns, err := svc.namespaces.Get(ctx, "", req.GetName())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "namespace %s: %v", req.GetName(), err)
	}
	return ns, nil
}

func (svc *namespaceService) ListNamespaces(ctx context.Context, _ *v1.ListNamespacesRequest) (*v1.ListNamespacesResponse, error) {
	items, err := svc.namespaces.List(ctx, "")
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list namespaces: %v", err)
	}
	return &v1.ListNamespacesResponse{Items: items}, nil
}

func (svc *namespaceService) DeleteNamespace(ctx context.Context, req *v1.DeleteNamespaceRequest) (*v1.DeleteNamespaceResponse, error) {
	if err := svc.namespaces.Delete(ctx, "", req.GetName()); err != nil {
		return nil, status.Errorf(codes.Internal, "delete namespace: %v", err)
	}
	return &v1.DeleteNamespaceResponse{}, nil
}
