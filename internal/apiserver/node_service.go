package apiserver

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

type nodeService struct {
	v1.UnimplementedNodeServiceServer
	nodes *registry.Registry[*v1.Node]
}

func NewNodeService(s store.Store) v1.NodeServiceServer {
	return &nodeService{nodes: registry.New(s, "nodes", func() *v1.Node { return &v1.Node{} })}
}

func (svc *nodeService) CreateNode(ctx context.Context, req *v1.CreateNodeRequest) (*v1.Node, error) {
	node := req.GetNode()
	if node.GetMetadata().GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "node.metadata.name is required")
	}
	if err := svc.nodes.Put(ctx, "", node.GetMetadata().GetName(), node); err != nil {
		return nil, status.Errorf(codes.Internal, "create node: %v", err)
	}
	return node, nil
}

func (svc *nodeService) GetNode(ctx context.Context, req *v1.GetNodeRequest) (*v1.Node, error) {
	node, err := svc.nodes.Get(ctx, "", req.GetName())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "node %s: %v", req.GetName(), err)
	}
	return node, nil
}

func (svc *nodeService) ListNodes(ctx context.Context, _ *v1.ListNodesRequest) (*v1.ListNodesResponse, error) {
	nodes, err := svc.nodes.List(ctx, "")
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list nodes: %v", err)
	}
	return &v1.ListNodesResponse{Items: nodes}, nil
}

func (svc *nodeService) DeleteNode(ctx context.Context, req *v1.DeleteNodeRequest) (*v1.DeleteNodeResponse, error) {
	if err := svc.nodes.Delete(ctx, "", req.GetName()); err != nil {
		return nil, status.Errorf(codes.Internal, "delete node: %v", err)
	}
	return &v1.DeleteNodeResponse{}, nil
}

func (svc *nodeService) Heartbeat(ctx context.Context, req *v1.HeartbeatRequest) (*v1.HeartbeatResponse, error) {
	node, err := svc.nodes.Get(ctx, "", req.GetName())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "node %s: %v", req.GetName(), err)
	}

	if node.Status == nil {
		node.Status = &v1.NodeStatus{}
	}
	node.Status.Ready = true
	node.Status.LastHeartbeatUnix = time.Now().Unix()
	if req.GetCapacity() != nil {
		node.Status.Capacity = req.GetCapacity()
	}
	if req.GetAllocatable() != nil {
		node.Status.Allocatable = req.GetAllocatable()
	}

	if err := svc.nodes.Put(ctx, "", req.GetName(), node); err != nil {
		return nil, status.Errorf(codes.Internal, "update node: %v", err)
	}
	return &v1.HeartbeatResponse{Accepted: true}, nil
}
