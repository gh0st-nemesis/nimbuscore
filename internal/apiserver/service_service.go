package apiserver

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

type serviceService struct {
	v1.UnimplementedServiceServiceServer
	services *registry.Registry[*v1.Service]
	pods     *registry.Registry[*v1.Pod]
	nodes    *registry.Registry[*v1.Node]
}

func NewServiceService(s store.Store) v1.ServiceServiceServer {
	return &serviceService{
		services: registry.New(s, "services", func() *v1.Service { return &v1.Service{} }),
		pods:     registry.New(s, "pods", func() *v1.Pod { return &v1.Pod{} }),
		nodes:    registry.New(s, "nodes", func() *v1.Node { return &v1.Node{} }),
	}
}

func (svc *serviceService) CreateService(ctx context.Context, req *v1.CreateServiceRequest) (*v1.Service, error) {
	s := req.GetService()
	meta := s.GetMetadata()
	if meta.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "service.metadata.name is required")
	}
	if err := svc.services.Put(ctx, meta.GetNamespace(), meta.GetName(), s); err != nil {
		return nil, status.Errorf(codes.Internal, "create service: %v", err)
	}
	return svc.resolve(ctx, s)
}

func (svc *serviceService) GetService(ctx context.Context, req *v1.GetServiceRequest) (*v1.Service, error) {
	s, err := svc.services.Get(ctx, req.GetNamespace(), req.GetName())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "service %s/%s: %v", req.GetNamespace(), req.GetName(), err)
	}
	return svc.resolve(ctx, s)
}

func (svc *serviceService) ListServices(ctx context.Context, req *v1.ListServicesRequest) (*v1.ListServicesResponse, error) {
	items, err := svc.services.List(ctx, req.GetNamespace())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list services: %v", err)
	}
	resolved := make([]*v1.Service, 0, len(items))
	for _, s := range items {
		r, err := svc.resolve(ctx, s)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, r)
	}
	return &v1.ListServicesResponse{Items: resolved}, nil
}

func (svc *serviceService) DeleteService(ctx context.Context, req *v1.DeleteServiceRequest) (*v1.DeleteServiceResponse, error) {
	if err := svc.services.Delete(ctx, req.GetNamespace(), req.GetName()); err != nil {
		return nil, status.Errorf(codes.Internal, "delete service: %v", err)
	}
	return &v1.DeleteServiceResponse{}, nil
}

func (svc *serviceService) resolve(ctx context.Context, s *v1.Service) (*v1.Service, error) {
	selector := s.GetSpec().GetSelector()
	if len(selector) == 0 {
		s.Status = &v1.ServiceStatus{}
		return s, nil
	}

	targetPort := s.GetSpec().GetTargetPort()
	if targetPort == 0 {
		targetPort = s.GetSpec().GetPort()
	}

	allPods, err := svc.pods.List(ctx, s.GetMetadata().GetNamespace())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "resolve service endpoints: list pods: %v", err)
	}

	var endpoints []*v1.ServiceEndpoint
	for _, p := range allPods {
		if p.GetStatus().GetPhase() != v1.PodPhase_POD_PHASE_RUNNING {
			continue
		}
		if !labelsMatch(p.GetMetadata().GetLabels(), selector) {
			continue
		}
		nodeName := p.GetSpec().GetNodeName()
		if nodeName == "" {
			continue
		}
		node, err := svc.nodes.Get(ctx, "", nodeName)
		if err != nil {
			continue
		}
		endpoints = append(endpoints, &v1.ServiceEndpoint{
			PodName:  p.GetMetadata().GetName(),
			NodeIp:   node.GetStatus().GetInternalIp(),
			NodePort: targetPort,
		})
	}

	s.Status = &v1.ServiceStatus{Endpoints: endpoints}
	return s, nil
}

func labelsMatch(labels, selector map[string]string) bool {
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}
