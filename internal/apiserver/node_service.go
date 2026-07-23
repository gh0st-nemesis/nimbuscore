package apiserver

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

// nodeService implements v1.NodeServiceServer. Nodes are cluster-scoped
// (no namespace), so it stores them under the empty namespace.
type nodeService struct {
	v1.UnimplementedNodeServiceServer
	nodes *registry.Registry[*v1.Node]
}

// NewNodeService returns a v1.NodeServiceServer backed by s.
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
