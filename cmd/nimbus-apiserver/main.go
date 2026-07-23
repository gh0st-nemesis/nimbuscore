package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/spiffe/go-spiffe/v2/spiffeid"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/admission"
	"github.com/gh0st-nemesis/nimbuscore/internal/apiserver"
	"github.com/gh0st-nemesis/nimbuscore/internal/controller"
	"github.com/gh0st-nemesis/nimbuscore/internal/csi/hostpath"
	"github.com/gh0st-nemesis/nimbuscore/internal/federation"
	"github.com/gh0st-nemesis/nimbuscore/internal/finops"
	"github.com/gh0st-nemesis/nimbuscore/internal/gitops"
	"github.com/gh0st-nemesis/nimbuscore/internal/identity"
	"github.com/gh0st-nemesis/nimbuscore/internal/imagesign"
	"github.com/gh0st-nemesis/nimbuscore/internal/policy"
	"github.com/gh0st-nemesis/nimbuscore/internal/rbac"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/scheduler"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
	"github.com/gh0st-nemesis/nimbuscore/internal/telemetry"
)

type allowAllVerifier struct{}

func (allowAllVerifier) Verify(context.Context, string) error { return nil }

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
	imagePubKeyFile := fs.String("image-signing-pubkey", "", "PEM file with the image-signing public key (see nimbusctl generate-key)")
	imageTrustFile := fs.String("image-trust-file", "", "JSON file mapping image references to signatures (see nimbusctl sign-image)")
	insecureSkipImageVerification := fs.Bool("insecure-skip-image-verification", false, "DEV ONLY: admit every image regardless of signature")
	allowedCapabilities := fs.String("allowed-capabilities", "", "comma-separated Linux capabilities containers may request (default: none)")
	cpuQuotaMillis := fs.Int64("namespace-cpu-quota-millis", 0, "per-namespace CPU quota in millicores (0 = unlimited)")
	memQuotaBytes := fs.Int64("namespace-memory-quota-bytes", 0, "per-namespace memory quota in bytes (0 = unlimited)")
	otelExporter := fs.String("otel-exporter", "none", "OpenTelemetry exporter: none, stdout, or otlp")
	otlpEndpoint := fs.String("otlp-endpoint", "127.0.0.1:4317", "OTLP gRPC collector endpoint (when -otel-exporter=otlp)")
	csiHostpathDir := fs.String("csi-hostpath-dir", "./data/volumes", "directory backing the built-in hostpath CSI driver")
	secretKeyRotationInterval := fs.Duration("secret-key-rotation-interval", 24*time.Hour, "how often the store's encryption key is rotated")
	gitOpsRepoURL := fs.String("gitops-repo-url", "", "Git repository URL to sync Deployment manifests from (disabled if empty)")
	gitOpsBranch := fs.String("gitops-branch", "main", "branch to sync when -gitops-repo-url is set")
	gitOpsPath := fs.String("gitops-path", "", "subdirectory within the repo containing *.json Deployment manifests")
	gitOpsWorkDir := fs.String("gitops-work-dir", "./data/gitops", "local working copy for the GitOps repository")
	gitOpsSyncInterval := fs.Duration("gitops-sync-interval", 30*time.Second, "how often the GitOps repository is re-synced")
	costCPUCoreHour := fs.Float64("cost-cpu-core-hour", finops.DefaultCostModel().CPUCoreHour, "estimated cost in $ per CPU core-hour, used by FinOpsService")
	costMemoryGBHour := fs.Float64("cost-memory-gb-hour", finops.DefaultCostModel().MemoryGBHour, "estimated cost in $ per memory GB-hour, used by FinOpsService")
	fs.Parse(os.Args[1:])

	if *joinToken == "" {
		log.Fatal("apiserver: -join-token is required")
	}
	if !*bootstrap && *joinAddr == "" {
		log.Fatal("apiserver: -join-api-addr is required unless -bootstrap is set")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	otelProvider, err := telemetry.Setup(ctx, telemetry.Config{
		ServiceName:  "nimbus-apiserver",
		Exporter:     *otelExporter,
		OTLPEndpoint: *otlpEndpoint,
	})
	if err != nil {
		log.Fatalf("apiserver: telemetry: %v", err)
	}
	defer otelProvider.Shutdown(context.Background())

	svid, ca, dek, err := loadOrBootstrapIdentity(ctx, *bootstrap, *trustDomain, *nodeID, *joinAddr, *joinToken)
	if err != nil {
		log.Fatalf("apiserver: %v", err)
	}

	raftStore, err := store.NewRaftStore(store.RaftConfig{
		NodeID:        *nodeID,
		BindAddr:      *raftAddr,
		DataDir:       *dataDir,
		Bootstrap:     *bootstrap,
		EncryptionKey: dek,
	})
	if err != nil {
		log.Fatalf("apiserver: raft: %v", err)
	}
	defer raftStore.Shutdown()

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

	authz := rbac.NewAuthorizer(apiserver.DefaultRBACBindings()...)
	srv := apiserver.New(apiserver.Config{
		Addr:            *apiAddr,
		TLSConfig:       svid.ServerTLSConfig(),
		AuthInterceptor: apiserver.AuthInterceptor(expectMember, authz),
	})

	nodes := registry.New(raftStore, "nodes", func() *v1.Node { return &v1.Node{} })
	deployments := registry.New(raftStore, "deployments", func() *v1.Deployment { return &v1.Deployment{} })
	pods := registry.New(raftStore, "pods", func() *v1.Pod { return &v1.Pod{} })

	policyEngine, err := policy.NewEngine()
	if err != nil {
		log.Fatalf("apiserver: policy engine: %v", err)
	}
	policies := registry.New(raftStore, "policies", func() *v1.Policy { return &v1.Policy{} })

	imageVerifier, imageSigningPub := loadImageVerifier(*imageTrustFile, *imagePubKeyFile, *insecureSkipImageVerification)
	admissionChain := admission.NewChain(
		admission.NewSecurityContextPolicy(splitCSV(*allowedCapabilities)...),
		admission.NewImageSignaturePolicy(imageVerifier),
		admission.NewQuotaPolicy(pods, &v1.ResourceList{CpuMillis: *cpuQuotaMillis, MemoryBytes: *memQuotaBytes}),
		admission.NewPolicyValidator(policyEngine, policies),
	)

	csiDriver, err := hostpath.New(*nodeID, *csiHostpathDir)
	if err != nil {
		log.Fatalf("apiserver: csi hostpath driver: %v", err)
	}

	v1.RegisterPodServiceServer(srv.GRPCServer(), apiserver.NewPodService(raftStore, admissionChain))
	v1.RegisterNodeServiceServer(srv.GRPCServer(), apiserver.NewNodeService(raftStore))
	v1.RegisterDeploymentServiceServer(srv.GRPCServer(), apiserver.NewDeploymentService(raftStore, admissionChain))
	v1.RegisterAdminServiceServer(srv.GRPCServer(), apiserver.NewAdminService(raftStore))
	v1.RegisterVolumeServiceServer(srv.GRPCServer(), apiserver.NewVolumeService(raftStore, csiDriver))
	v1.RegisterNetworkPolicyServiceServer(srv.GRPCServer(), apiserver.NewNetworkPolicyService(raftStore))
	v1.RegisterPolicyServiceServer(srv.GRPCServer(), apiserver.NewPolicyService(raftStore, policyEngine))
	v1.RegisterSecretServiceServer(srv.GRPCServer(), apiserver.NewSecretService(raftStore))
	v1.RegisterBackupServiceServer(srv.GRPCServer(), apiserver.NewBackupService(raftStore))
	v1.RegisterFederationServiceServer(srv.GRPCServer(), apiserver.NewFederationService(federation.NewRegistry()))
	v1.RegisterFinOpsServiceServer(srv.GRPCServer(), apiserver.NewFinOpsService(pods, finops.CostModel{CPUCoreHour: *costCPUCoreHour, MemoryGBHour: *costMemoryGBHour}))
	if imageSigningPub != nil {
		v1.RegisterImageRegistryServiceServer(srv.GRPCServer(), apiserver.NewImageRegistryService(raftStore, imageSigningPub))
	}
	if ca != nil {
		v1.RegisterIdentityServiceServer(srv.GRPCServer(), apiserver.NewIdentityService(ca, *joinToken, identity.DefaultSVIDTTL, dek))
	}

	mgr := controller.NewManager()
	mgr.Register(controller.NewDeploymentReconciler(deployments, pods, nodes, scheduler.New(), 0))
	mgr.Register(controller.NewNodeHealthReconciler(nodes, 0, 0))
	mgr.Register(controller.NewHorizontalAutoscaler(deployments, pods, nodes, 0))
	mgr.Register(controller.NewKeyRotationReconciler(raftStore, *secretKeyRotationInterval))
	if *gitOpsRepoURL != "" {
		mgr.Register(gitops.NewReconciler(gitops.Config{
			RepoURL: *gitOpsRepoURL,
			Branch:  *gitOpsBranch,
			Path:    *gitOpsPath,
			WorkDir: *gitOpsWorkDir,
		}, deployments, *gitOpsSyncInterval))
	}
	go controller.RunWhileLeader(ctx, raftStore, mgr, 0)

	if err := srv.Serve(ctx); err != nil {
		log.Fatalf("apiserver: %v", err)
	}
}

func loadOrBootstrapIdentity(ctx context.Context, bootstrap bool, trustDomain, nodeID, joinAddr, joinToken string) (*identity.SVID, *identity.CA, []byte, error) {
	if !bootstrap {
		svid, dek, err := identity.Enroll(ctx, identity.EnrollConfig{
			ControlPlaneAddr: joinAddr,
			JoinToken:        joinToken,
			Name:             nodeID,
			Role:             v1.SVIDRole_SVID_ROLE_CONTROL_PLANE,
		})
		return svid, nil, dek, err
	}

	ca, err := identity.NewCA(trustDomain)
	if err != nil {
		return nil, nil, nil, err
	}

	key, err := identity.GenerateKey()
	if err != nil {
		return nil, nil, nil, err
	}
	id, err := spiffeid.FromPathf(ca.TrustDomain(), "/control-plane/%s", nodeID)
	if err != nil {
		return nil, nil, nil, err
	}
	cert, err := ca.IssueSVID(&key.PublicKey, id, identity.DefaultSVIDTTL)
	if err != nil {
		return nil, nil, nil, err
	}

	dek := make([]byte, store.EncryptionKeySize)
	if _, err := rand.Read(dek); err != nil {
		return nil, nil, nil, err
	}

	log.Printf("apiserver: bootstrapped cluster, trust domain %q, join token %q", trustDomain, joinToken)
	return identity.NewSVID(key, cert, ca.TrustBundle()), ca, dek, nil
}

func joinRaftCluster(ctx context.Context, svid *identity.SVID, expect spiffeid.Matcher, joinAddr, nodeID, raftAddr string) error {
	conn, err := grpc.NewClient(joinAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(svid.ClientTLSConfig(expect))),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
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

func loadImageVerifier(trustFile, pubKeyFile string, insecureSkip bool) (admission.ImageVerifier, *ecdsa.PublicKey) {
	if insecureSkip {
		log.Printf("apiserver: WARNING image signature verification is DISABLED (-insecure-skip-image-verification) — do not run this in production")
		return allowAllVerifier{}, nil
	}
	if pubKeyFile == "" || trustFile == "" {
		log.Fatal("apiserver: -image-signing-pubkey and -image-trust-file are required unless -insecure-skip-image-verification is set")
	}

	pubPEM, err := os.ReadFile(pubKeyFile)
	if err != nil {
		log.Fatalf("apiserver: read image signing public key: %v", err)
	}
	pub, err := imagesign.ParsePublicKey(pubPEM)
	if err != nil {
		log.Fatalf("apiserver: parse image signing public key: %v", err)
	}

	tf, err := imagesign.LoadTrustFile(trustFile)
	if err != nil {
		log.Fatalf("apiserver: load image trust file: %v", err)
	}
	return imagesign.NewKeyVerifier(pub, tf), pub
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func defaultNodeID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "node"
	}
	return host
}
