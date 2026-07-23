package apiserver

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/policy"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

type policyService struct {
	v1.UnimplementedPolicyServiceServer
	policies *registry.Registry[*v1.Policy]
	engine   *policy.Engine
}

func NewPolicyService(s store.Store, engine *policy.Engine) v1.PolicyServiceServer {
	return &policyService{
		policies: registry.New(s, "policies", func() *v1.Policy { return &v1.Policy{} }),
		engine:   engine,
	}
}

func (svc *policyService) CreatePolicy(ctx context.Context, req *v1.CreatePolicyRequest) (*v1.Policy, error) {
	pol := req.GetPolicy()
	meta := pol.GetMetadata()
	if meta.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "policy.metadata.name is required")
	}
	if pol.GetSpec().GetExpression() == "" {
		return nil, status.Error(codes.InvalidArgument, "policy.spec.expression is required")
	}
	if _, err := svc.engine.Compile(pol.GetSpec().GetExpression()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid CEL expression: %v", err)
	}

	if err := svc.policies.Put(ctx, "", meta.GetName(), pol); err != nil {
		return nil, status.Errorf(codes.Internal, "create policy: %v", err)
	}
	return pol, nil
}

func (svc *policyService) GetPolicy(ctx context.Context, req *v1.GetPolicyRequest) (*v1.Policy, error) {
	pol, err := svc.policies.Get(ctx, "", req.GetName())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "policy %s: %v", req.GetName(), err)
	}
	return pol, nil
}

func (svc *policyService) ListPolicies(ctx context.Context, _ *v1.ListPoliciesRequest) (*v1.ListPoliciesResponse, error) {
	items, err := svc.policies.List(ctx, "")
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list policies: %v", err)
	}
	return &v1.ListPoliciesResponse{Items: items}, nil
}

func (svc *policyService) DeletePolicy(ctx context.Context, req *v1.DeletePolicyRequest) (*v1.DeletePolicyResponse, error) {
	if err := svc.policies.Delete(ctx, "", req.GetName()); err != nil {
		return nil, status.Errorf(codes.Internal, "delete policy: %v", err)
	}
	return &v1.DeletePolicyResponse{}, nil
}
