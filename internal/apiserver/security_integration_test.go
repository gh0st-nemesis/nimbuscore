package apiserver_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/test/bufconn"

	"github.com/spiffe/go-spiffe/v2/spiffeid"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/admission"
	"github.com/gh0st-nemesis/nimbuscore/internal/apiserver"
	"github.com/gh0st-nemesis/nimbuscore/internal/identity"
	"github.com/gh0st-nemesis/nimbuscore/internal/imagesign"
	"github.com/gh0st-nemesis/nimbuscore/internal/rbac"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

type fakeRaftJoiner struct{}

func (fakeRaftJoiner) AddVoter(nodeID, addr string) error { return nil }

func issueTestSVID(t *testing.T, ca *identity.CA, path string) *identity.SVID {
	t.Helper()
	key, err := identity.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	id, err := spiffeid.FromPath(ca.TrustDomain(), path)
	if err != nil {
		t.Fatalf("FromPath: %v", err)
	}
	cert, err := ca.IssueSVID(&key.PublicKey, id, identity.DefaultSVIDTTL)
	if err != nil {
		t.Fatalf("IssueSVID: %v", err)
	}
	return identity.NewSVID(key, cert, ca.TrustBundle())
}

func dialAs(t *testing.T, lis *bufconn.Listener, svid *identity.SVID, td spiffeid.TrustDomain) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(credentials.NewTLS(svid.ClientTLSConfig(spiffeid.MatchMemberOf(td)))),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func newSignedImageChain(t *testing.T, signedImage string) *admission.Chain {
	t.Helper()
	key, err := imagesign.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	sig, err := imagesign.Sign(key, signedImage)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	tf := &imagesign.TrustFile{Signatures: map[string]string{}}
	tf.Add(signedImage, sig)

	return admission.NewChain(
		admission.NewSecurityContextPolicy(),
		admission.NewImageSignaturePolicy(imagesign.NewKeyVerifier(&key.PublicKey, tf)),
	)
}

func newSecurityTestServer(t *testing.T, chain *admission.Chain) (*bufconn.Listener, *identity.CA) {
	t.Helper()

	ca, err := identity.NewCA("nimbuscore.local")
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}
	serverSVID := issueTestSVID(t, ca, "/control-plane/test-server")

	st := store.NewMemStore()

	grpcServer := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(serverSVID.ServerTLSConfig())),
		grpc.UnaryInterceptor(apiserver.AuthInterceptor(
			spiffeid.MatchMemberOf(ca.TrustDomain()),
			rbac.NewAuthorizer(apiserver.DefaultRBACBindings()...),
		)),
	)
	v1.RegisterPodServiceServer(grpcServer, apiserver.NewPodService(st, chain))
	v1.RegisterDeploymentServiceServer(grpcServer, apiserver.NewDeploymentService(st, chain))
	v1.RegisterNodeServiceServer(grpcServer, apiserver.NewNodeService(st))
	v1.RegisterAdminServiceServer(grpcServer, apiserver.NewAdminService(fakeRaftJoiner{}))

	lis := bufconn.Listen(1 << 20)
	go grpcServer.Serve(lis)
	t.Cleanup(grpcServer.Stop)

	return lis, ca
}

func TestAdmissionRejectsUnsignedImage(t *testing.T) {
	chain := newSignedImageChain(t, "registry.example/app:signed")
	lis, ca := newSecurityTestServer(t, chain)

	clientSVID := issueTestSVID(t, ca, "/client/tester")
	conn := dialAs(t, lis, clientSVID, ca.TrustDomain())

	podClient := v1.NewPodServiceClient(conn)
	ctx := context.Background()

	_, err := podClient.CreatePod(ctx, &v1.CreatePodRequest{
		Pod: &v1.Pod{
			Metadata: &v1.ObjectMeta{Name: "unsigned", Namespace: "default"},
			Spec:     &v1.PodSpec{Containers: []*v1.Container{{Name: "app", Image: "registry.example/app:unsigned"}}},
		},
	})
	if err == nil {
		t.Fatal("CreatePod succeeded with an unsigned image, want rejection")
	}
}

func TestAdmissionAllowsSignedImage(t *testing.T) {
	chain := newSignedImageChain(t, "registry.example/app:signed")
	lis, ca := newSecurityTestServer(t, chain)

	clientSVID := issueTestSVID(t, ca, "/client/tester")
	conn := dialAs(t, lis, clientSVID, ca.TrustDomain())

	podClient := v1.NewPodServiceClient(conn)
	ctx := context.Background()

	_, err := podClient.CreatePod(ctx, &v1.CreatePodRequest{
		Pod: &v1.Pod{
			Metadata: &v1.ObjectMeta{Name: "signed", Namespace: "default"},
			Spec:     &v1.PodSpec{Containers: []*v1.Container{{Name: "app", Image: "registry.example/app:signed"}}},
		},
	})
	if err != nil {
		t.Fatalf("CreatePod rejected a signed image: %v", err)
	}
}

func TestAdmissionRejectsPrivilegedContainerEvenIfSigned(t *testing.T) {
	chain := newSignedImageChain(t, "registry.example/app:signed")
	lis, ca := newSecurityTestServer(t, chain)

	clientSVID := issueTestSVID(t, ca, "/client/tester")
	conn := dialAs(t, lis, clientSVID, ca.TrustDomain())

	podClient := v1.NewPodServiceClient(conn)
	ctx := context.Background()

	_, err := podClient.CreatePod(ctx, &v1.CreatePodRequest{
		Pod: &v1.Pod{
			Metadata: &v1.ObjectMeta{Name: "privileged", Namespace: "default"},
			Spec: &v1.PodSpec{Containers: []*v1.Container{{
				Name:            "app",
				Image:           "registry.example/app:signed",
				SecurityContext: &v1.SecurityContext{Privileged: true},
			}}},
		},
	})
	if err == nil {
		t.Fatal("CreatePod succeeded for a privileged container, want rejection")
	}
}

func TestRBACDeniesNodeIdentityFromCreatingDeployments(t *testing.T) {
	chain := admission.NewChain()
	lis, ca := newSecurityTestServer(t, chain)

	nodeSVID := issueTestSVID(t, ca, "/node/worker-1")
	conn := dialAs(t, lis, nodeSVID, ca.TrustDomain())

	deployClient := v1.NewDeploymentServiceClient(conn)
	ctx := context.Background()

	_, err := deployClient.CreateDeployment(ctx, &v1.CreateDeploymentRequest{
		Deployment: &v1.Deployment{
			Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
			Spec: &v1.DeploymentSpec{
				Replicas: 1,
				Template: &v1.PodSpec{Containers: []*v1.Container{{Name: "app", Image: "nginx"}}},
			},
		},
	})
	if err == nil {
		t.Fatal("a node identity was allowed to create a Deployment, want PermissionDenied")
	}
}

func TestRBACAllowsClientIdentityToCreateDeployments(t *testing.T) {
	chain := admission.NewChain()
	lis, ca := newSecurityTestServer(t, chain)

	clientSVID := issueTestSVID(t, ca, "/client/operator")
	conn := dialAs(t, lis, clientSVID, ca.TrustDomain())

	deployClient := v1.NewDeploymentServiceClient(conn)
	ctx := context.Background()

	_, err := deployClient.CreateDeployment(ctx, &v1.CreateDeploymentRequest{
		Deployment: &v1.Deployment{
			Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
			Spec: &v1.DeploymentSpec{
				Replicas: 1,
				Template: &v1.PodSpec{Containers: []*v1.Container{{Name: "app", Image: "nginx"}}},
			},
		},
	})
	if err != nil {
		t.Fatalf("a client identity was denied creating a Deployment: %v", err)
	}
}

func TestRBACDeniesClientFromJoiningRaft(t *testing.T) {
	chain := admission.NewChain()
	lis, ca := newSecurityTestServer(t, chain)

	clientSVID := issueTestSVID(t, ca, "/client/operator")
	conn := dialAs(t, lis, clientSVID, ca.TrustDomain())

	adminClient := v1.NewAdminServiceClient(conn)
	_, err := adminClient.JoinRaft(context.Background(), &v1.JoinRaftRequest{NodeId: "x", RaftAddr: "127.0.0.1:1"})
	if err == nil {
		t.Fatal("a client identity was allowed to call JoinRaft, want PermissionDenied")
	}
}
