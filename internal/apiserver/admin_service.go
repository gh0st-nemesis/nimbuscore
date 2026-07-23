package apiserver

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

// RaftJoiner is the subset of *store.RaftStore the AdminService needs —
// kept narrow so this package doesn't have to depend on Raft's full
// surface, only "can add a voter."
type RaftJoiner interface {
	AddVoter(nodeID, addr string) error
}

// adminService implements v1.AdminServiceServer. It is not gated by
// AuthInterceptor's blanket membership check alone: JoinRaft additionally
// requires the caller's SPIFFE ID path to start with "control-plane/",
// since only other control-plane replicas — never worker nodes — may
// add themselves as Raft voters.
type adminService struct {
	v1.UnimplementedAdminServiceServer
	raft RaftJoiner
}

// NewAdminService returns a v1.AdminServiceServer backed by raft.
func NewAdminService(raft RaftJoiner) v1.AdminServiceServer {
	return &adminService{raft: raft}
}

func (svc *adminService) JoinRaft(ctx context.Context, req *v1.JoinRaftRequest) (*v1.JoinRaftResponse, error) {
	id, err := PeerSPIFFEID(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "%v", err)
	}
	if !strings.HasPrefix(id.Path(), "/control-plane/") {
		return nil, status.Errorf(codes.PermissionDenied, "JoinRaft requires a control-plane identity, got %s", id)
	}

	if req.GetNodeId() == "" || req.GetRaftAddr() == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id and raft_addr are required")
	}

	if err := svc.raft.AddVoter(req.GetNodeId(), req.GetRaftAddr()); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "add voter: %v", err)
	}
	return &v1.JoinRaftResponse{}, nil
}
