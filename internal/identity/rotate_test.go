package identity_test

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/spiffe/go-spiffe/v2/spiffeid"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/apiserver"
	"github.com/gh0st-nemesis/nimbuscore/internal/identity"
)

func TestStartSelfIssueRotateLoopRenewsBeforeExpiry(t *testing.T) {
	ca, err := identity.NewCA("nimbuscore.local")
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}

	const shortTTL = 150 * time.Millisecond
	key, err := identity.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	id, err := spiffeid.FromPath(ca.TrustDomain(), "/control-plane/test")
	if err != nil {
		t.Fatalf("path id: %v", err)
	}
	firstCert, err := ca.IssueSVID(&key.PublicKey, id, shortTTL)
	if err != nil {
		t.Fatalf("IssueSVID: %v", err)
	}
	svid := identity.NewSVID(key, firstCert, ca.TrustBundle())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	identity.StartSelfIssueRotateLoop(ctx, svid, ca, "/control-plane/test", shortTTL)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, cert := svid.Materials()
		if !cert.NotAfter.Equal(firstCert.NotAfter) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("SVID was never rotated before the original certificate's expiry")
}

func TestStartReenrollRotateLoopRenewsBeforeExpiry(t *testing.T) {
	const shortTTL = 150 * time.Millisecond

	ca, err := identity.NewCA("nimbuscore.local")
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}
	dek := make([]byte, 32)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	serverSVID := issueTestSVIDFor(t, ca, "/control-plane/server")
	grpcServer := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverSVID.ServerTLSConfig())))
	v1.RegisterIdentityServiceServer(grpcServer, apiserver.NewIdentityService(ca, "jointoken", shortTTL, dek))
	go grpcServer.Serve(lis) //nolint:errcheck
	t.Cleanup(grpcServer.Stop)

	cfg := identity.EnrollConfig{
		ControlPlaneAddr: lis.Addr().String(),
		JoinToken:        "jointoken",
		Name:             "agent-1",
		Role:             v1.SVIDRole_SVID_ROLE_NODE,
	}
	initial, _, err := identity.Enroll(context.Background(), cfg)
	if err != nil {
		t.Fatalf("initial Enroll: %v", err)
	}
	_, firstCert := initial.Materials()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	identity.StartReenrollRotateLoop(ctx, initial, cfg, shortTTL)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, cert := initial.Materials()
		if cert.SerialNumber.Cmp(firstCert.SerialNumber) != 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("SVID was never renewed via re-enrollment before the original certificate's expiry")
}

func issueTestSVIDFor(t *testing.T, ca *identity.CA, path string) *identity.SVID {
	t.Helper()
	key, err := identity.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	id, err := spiffeid.FromPath(ca.TrustDomain(), path)
	if err != nil {
		t.Fatalf("path id: %v", err)
	}
	cert, err := ca.IssueSVID(&key.PublicKey, id, identity.DefaultSVIDTTL)
	if err != nil {
		t.Fatalf("IssueSVID: %v", err)
	}
	return identity.NewSVID(key, cert, ca.TrustBundle())
}
