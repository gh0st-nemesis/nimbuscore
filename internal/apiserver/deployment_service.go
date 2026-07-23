package apiserver

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

type deploymentService struct {
	v1.UnimplementedDeploymentServiceServer
	deployments *registry.Registry[*v1.Deployment]
}

func NewDeploymentService(s store.Store) v1.DeploymentServiceServer {
	return &deploymentService{deployments: registry.New(s, "deployments", func() *v1.Deployment { return &v1.Deployment{} })}
}

func (svc *deploymentService) CreateDeployment(ctx context.Context, req *v1.CreateDeploymentRequest) (*v1.Deployment, error) {
	d := req.GetDeployment()
	meta := d.GetMetadata()
	if meta.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "deployment.metadata.name is required")
	}
	if err := svc.deployments.Put(ctx, meta.GetNamespace(), meta.GetName(), d); err != nil {
		return nil, status.Errorf(codes.Internal, "create deployment: %v", err)
	}
	return d, nil
}

func (svc *deploymentService) GetDeployment(ctx context.Context, req *v1.GetDeploymentRequest) (*v1.Deployment, error) {
	d, err := svc.deployments.Get(ctx, req.GetNamespace(), req.GetName())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "deployment %s/%s: %v", req.GetNamespace(), req.GetName(), err)
	}
	return d, nil
}

func (svc *deploymentService) ListDeployments(ctx context.Context, req *v1.ListDeploymentsRequest) (*v1.ListDeploymentsResponse, error) {
	deployments, err := svc.deployments.List(ctx, req.GetNamespace())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list deployments: %v", err)
	}
	return &v1.ListDeploymentsResponse{Items: deployments}, nil
}

func (svc *deploymentService) DeleteDeployment(ctx context.Context, req *v1.DeleteDeploymentRequest) (*v1.DeleteDeploymentResponse, error) {
	if err := svc.deployments.Delete(ctx, req.GetNamespace(), req.GetName()); err != nil {
		return nil, status.Errorf(codes.Internal, "delete deployment: %v", err)
	}
	return &v1.DeleteDeploymentResponse{}, nil
}
