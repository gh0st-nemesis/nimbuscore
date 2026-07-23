package apiserver

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/spiffe/go-spiffe/v2/spiffeid"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/federation"
	"github.com/gh0st-nemesis/nimbuscore/internal/identity"
)

type federationService struct {
	v1.UnimplementedFederationServiceServer
	registry *federation.Registry

	mu       sync.Mutex
	clusters map[string]*v1.RemoteCluster
	conns    map[string]*grpc.ClientConn
}

func NewFederationService(registry *federation.Registry) v1.FederationServiceServer {
	return &federationService{
		registry: registry,
		clusters: make(map[string]*v1.RemoteCluster),
		conns:    make(map[string]*grpc.ClientConn),
	}
}

func (svc *federationService) RegisterCluster(ctx context.Context, req *v1.RegisterClusterRequest) (*v1.RemoteCluster, error) {
	if req.GetName() == "" || req.GetApiAddr() == "" || req.GetJoinToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "name, api_addr, and join_token are required")
	}

	svid, _, err := identity.Enroll(ctx, identity.EnrollConfig{
		ControlPlaneAddr: req.GetApiAddr(),
		JoinToken:        req.GetJoinToken(),
		Name:             fmt.Sprintf("federation-%s", req.GetName()),
		Role:             v1.SVIDRole_SVID_ROLE_CLIENT,
	})
	if err != nil {
		return nil, status.Errorf(codes.PermissionDenied, "enroll with remote cluster %q: %v", req.GetName(), err)
	}

	selfID, err := svid.ID()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "read enrolled identity: %v", err)
	}

	conn, err := grpc.NewClient(req.GetApiAddr(),
		grpc.WithTransportCredentials(credentials.NewTLS(svid.ClientTLSConfig(spiffeid.MatchMemberOf(selfID.TrustDomain())))),
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "dial remote cluster %q: %v", req.GetName(), err)
	}

	rec := &v1.RemoteCluster{Name: req.GetName(), ApiAddr: req.GetApiAddr()}

	svc.mu.Lock()
	if old, ok := svc.conns[req.GetName()]; ok {
		old.Close()
	}
	svc.clusters[req.GetName()] = rec
	svc.conns[req.GetName()] = conn
	svc.mu.Unlock()

	svc.registry.Register(req.GetName(), conn)
	return rec, nil
}

func (svc *federationService) UnregisterCluster(_ context.Context, req *v1.UnregisterClusterRequest) (*v1.UnregisterClusterResponse, error) {
	svc.registry.Unregister(req.GetName())

	svc.mu.Lock()
	if conn, ok := svc.conns[req.GetName()]; ok {
		conn.Close()
	}
	delete(svc.clusters, req.GetName())
	delete(svc.conns, req.GetName())
	svc.mu.Unlock()

	return &v1.UnregisterClusterResponse{}, nil
}

func (svc *federationService) ListClusters(context.Context, *v1.ListClustersRequest) (*v1.ListClustersResponse, error) {
	svc.mu.Lock()
	defer svc.mu.Unlock()

	items := make([]*v1.RemoteCluster, 0, len(svc.clusters))
	for _, c := range svc.clusters {
		items = append(items, c)
	}
	return &v1.ListClustersResponse{Items: items}, nil
}

func (svc *federationService) ListFederatedPods(ctx context.Context, req *v1.ListFederatedPodsRequest) (*v1.ListFederatedPodsResponse, error) {
	results := svc.registry.ListPodsAll(ctx, req.GetNamespace())

	resp := &v1.ListFederatedPodsResponse{Results: make([]*v1.FederatedPods, 0, len(results))}
	for _, r := range results {
		fp := &v1.FederatedPods{Cluster: r.Cluster, Items: r.Items}
		if r.Err != nil {
			fp.Error = r.Err.Error()
		}
		resp.Results = append(resp.Results, fp)
	}
	return resp, nil
}
