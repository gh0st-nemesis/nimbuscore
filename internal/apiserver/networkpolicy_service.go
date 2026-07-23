package apiserver

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

type networkPolicyService struct {
	v1.UnimplementedNetworkPolicyServiceServer
	policies *registry.Registry[*v1.NetworkPolicy]
}

func NewNetworkPolicyService(s store.Store) v1.NetworkPolicyServiceServer {
	return &networkPolicyService{policies: registry.New(s, "networkpolicies", func() *v1.NetworkPolicy { return &v1.NetworkPolicy{} })}
}

func (svc *networkPolicyService) CreateNetworkPolicy(ctx context.Context, req *v1.CreateNetworkPolicyRequest) (*v1.NetworkPolicy, error) {
	np := req.GetNetworkPolicy()
	meta := np.GetMetadata()
	if meta.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "network_policy.metadata.name is required")
	}
	if err := svc.policies.Put(ctx, meta.GetNamespace(), meta.GetName(), np); err != nil {
		return nil, status.Errorf(codes.Internal, "create network policy: %v", err)
	}
	return np, nil
}

func (svc *networkPolicyService) GetNetworkPolicy(ctx context.Context, req *v1.GetNetworkPolicyRequest) (*v1.NetworkPolicy, error) {
	np, err := svc.policies.Get(ctx, req.GetNamespace(), req.GetName())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "network policy %s/%s: %v", req.GetNamespace(), req.GetName(), err)
	}
	return np, nil
}

func (svc *networkPolicyService) ListNetworkPolicies(ctx context.Context, req *v1.ListNetworkPoliciesRequest) (*v1.ListNetworkPoliciesResponse, error) {
	items, err := svc.policies.List(ctx, req.GetNamespace())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list network policies: %v", err)
	}
	return &v1.ListNetworkPoliciesResponse{Items: items}, nil
}

func (svc *networkPolicyService) DeleteNetworkPolicy(ctx context.Context, req *v1.DeleteNetworkPolicyRequest) (*v1.DeleteNetworkPolicyResponse, error) {
	if err := svc.policies.Delete(ctx, req.GetNamespace(), req.GetName()); err != nil {
		return nil, status.Errorf(codes.Internal, "delete network policy: %v", err)
	}
	return &v1.DeleteNetworkPolicyResponse{}, nil
}
