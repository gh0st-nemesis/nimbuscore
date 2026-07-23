package apiserver

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

// podService implements v1.PodServiceServer directly against a Pod
// registry. Admission control (signature verification, RBAC, quotas —
// design doc section 03) is not wired in yet; every write is accepted
// as-is until Phase 3.
type podService struct {
	v1.UnimplementedPodServiceServer
	pods *registry.Registry[*v1.Pod]
}

// NewPodService returns a v1.PodServiceServer backed by s.
func NewPodService(s store.Store) v1.PodServiceServer {
	return &podService{pods: registry.New(s, "pods", func() *v1.Pod { return &v1.Pod{} })}
}

func (svc *podService) CreatePod(ctx context.Context, req *v1.CreatePodRequest) (*v1.Pod, error) {
	pod := req.GetPod()
	meta := pod.GetMetadata()
	if meta.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "pod.metadata.name is required")
	}
	if err := svc.pods.Put(ctx, meta.GetNamespace(), meta.GetName(), pod); err != nil {
		return nil, status.Errorf(codes.Internal, "create pod: %v", err)
	}
	return pod, nil
}

func (svc *podService) GetPod(ctx context.Context, req *v1.GetPodRequest) (*v1.Pod, error) {
	pod, err := svc.pods.Get(ctx, req.GetNamespace(), req.GetName())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "pod %s/%s: %v", req.GetNamespace(), req.GetName(), err)
	}
	return pod, nil
}

func (svc *podService) ListPods(ctx context.Context, req *v1.ListPodsRequest) (*v1.ListPodsResponse, error) {
	pods, err := svc.pods.List(ctx, req.GetNamespace())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list pods: %v", err)
	}
	return &v1.ListPodsResponse{Items: pods}, nil
}

func (svc *podService) DeletePod(ctx context.Context, req *v1.DeletePodRequest) (*v1.DeletePodResponse, error) {
	if err := svc.pods.Delete(ctx, req.GetNamespace(), req.GetName()); err != nil {
		return nil, status.Errorf(codes.Internal, "delete pod: %v", err)
	}
	return &v1.DeletePodResponse{}, nil
}
