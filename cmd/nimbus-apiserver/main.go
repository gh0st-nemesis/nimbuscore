package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/spiffe/go-spiffe/v2/spiffeid"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/apiserver"
	"github.com/gh0st-nemesis/nimbuscore/internal/controller"
	"github.com/gh0st-nemesis/nimbuscore/internal/identity"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/scheduler"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

func main() {
	fs := flag.NewFlagSet("nimbus-apiserver", flag.ExitOnError)
	nodeID := fs.String("node-id", defaultNodeID(), "unique Raft node ID for this replica")
	apiAddr := fs.String("api-addr", ":7443", "gRPC API listen address")
	raftAddr := fs.String("raft-addr", "127.0.0.1:7946", "Raft transport bind address; must be reachable by other replicas")
	dataDir := fs.String("data-dir", "./data", "directory for the Raft log, stable store, and snapshots")
	trustDomain := fs.String("trust-domain", "nimbuscore.local", "SPIFFE trust domain (bootstrap only)")
	bootstrap := fs.Bool("bootstrap", false, "bootstrap a brand-new cluster — first replica only")
	joinAddr := fs.String("join-api-addr", "", "gRPC API address of the current Raft leader (required unless -bootstrap)")
	joinToken := fs.String("join-token", "", "shared bootstrap token (required)")
	fs.Parse(os.Args[1:])

	if *joinToken == "" {
		log.Fatal("apiserver: -join-token is required")
	}
	if !*bootstrap && *joinAddr == "" {
		log.Fatal("apiserver: -join-api-addr is required unless -bootstrap is set")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	raftStore, err := store.NewRaftStore(store.RaftConfig{
		NodeID:    *nodeID,
		BindAddr:  *raftAddr,
		DataDir:   *dataDir,
		Bootstrap: *bootstrap,
	})
	if err != nil {
		log.Fatalf("apiserver: raft: %v", err)
	}
	defer raftStore.Shutdown()

	svid, ca, err := loadOrBootstrapSVID(ctx, *bootstrap, *trustDomain, *nodeID, *joinAddr, *joinToken)
	if err != nil {
		log.Fatalf("apiserver: %v", err)
	}

	selfID, err := svid.ID()
	if err != nil {
		log.Fatalf("apiserver: %v", err)
	}
	expectMember := spiffeid.MatchMemberOf(selfID.TrustDomain())
	log.Printf("apiserver: identity %s", selfID)

	if !*bootstrap {
		if err := joinRaftCluster(ctx, svid, expectMember, *joinAddr, *nodeID, *raftAddr); err != nil {
			log.Fatalf("apiserver: join raft cluster: %v", err)
		}
	}

	srv := apiserver.New(apiserver.Config{
		Addr:            *apiAddr,
		TLSConfig:       svid.ServerTLSConfig(),
		AuthInterceptor: apiserver.AuthInterceptor(expectMember),
	})

	v1.RegisterPodServiceServer(srv.GRPCServer(), apiserver.NewPodService(raftStore))
	v1.RegisterNodeServiceServer(srv.GRPCServer(), apiserver.NewNodeService(raftStore))
	v1.RegisterDeploymentServiceServer(srv.GRPCServer(), apiserver.NewDeploymentService(raftStore))
	v1.RegisterAdminServiceServer(srv.GRPCServer(), apiserver.NewAdminService(raftStore))
	if ca != nil {

		v1.RegisterIdentityServiceServer(srv.GRPCServer(), apiserver.NewIdentityService(ca, *joinToken, identity.DefaultSVIDTTL))
	}

	nodes := registry.New(raftStore, "nodes", func() *v1.Node { return &v1.Node{} })
	deployments := registry.New(raftStore, "deployments", func() *v1.Deployment { return &v1.Deployment{} })
	pods := registry.New(raftStore, "pods", func() *v1.Pod { return &v1.Pod{} })

	mgr := controller.NewManager()
	mgr.Register(controller.NewDeploymentReconciler(deployments, pods, nodes, scheduler.New(), 0))
	mgr.Register(controller.NewNodeHealthReconciler(nodes, 0, 0))
	go controller.RunWhileLeader(ctx, raftStore, mgr, 0)

	if err := srv.Serve(ctx); err != nil {
		log.Fatalf("apiserver: %v", err)
	}
}

func loadOrBootstrapSVID(ctx context.Context, bootstrap bool, trustDomain, nodeID, joinAddr, joinToken string) (*identity.SVID, *identity.CA, error) {
	if !bootstrap {
		svid, err := identity.Enroll(ctx, identity.EnrollConfig{
			ControlPlaneAddr: joinAddr,
			JoinToken:        joinToken,
			Name:             nodeID,
			Role:             v1.SVIDRole_SVID_ROLE_CONTROL_PLANE,
		})
		return svid, nil, err
	}

	ca, err := identity.NewCA(trustDomain)
	if err != nil {
		return nil, nil, err
	}

	key, err := identity.GenerateKey()
	if err != nil {
		return nil, nil, err
	}
	id, err := spiffeid.FromPathf(ca.TrustDomain(), "/control-plane/%s", nodeID)
	if err != nil {
		return nil, nil, err
	}
	cert, err := ca.IssueSVID(&key.PublicKey, id, identity.DefaultSVIDTTL)
	if err != nil {
		return nil, nil, err
	}

	log.Printf("apiserver: bootstrapped cluster, trust domain %q, join token %q", trustDomain, joinToken)
	return identity.NewSVID(key, cert, ca.TrustBundle()), ca, nil
}

func joinRaftCluster(ctx context.Context, svid *identity.SVID, expect spiffeid.Matcher, joinAddr, nodeID, raftAddr string) error {
	conn, err := grpc.NewClient(joinAddr, grpc.WithTransportCredentials(credentials.NewTLS(svid.ClientTLSConfig(expect))))
	if err != nil {
		return err
	}
	defer conn.Close()

	joinCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, err = v1.NewAdminServiceClient(conn).JoinRaft(joinCtx, &v1.JoinRaftRequest{
		NodeId:   nodeID,
		RaftAddr: raftAddr,
	})
	return err
}

func defaultNodeID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "node"
	}
	return host
}
