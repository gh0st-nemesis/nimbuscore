package identity

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

// EnrollConfig describes a one-time enrollment call against a running
// control plane's IdentityService.
type EnrollConfig struct {
	// ControlPlaneAddr is host:port of a control-plane replica's gRPC
	// API that has IdentityService registered.
	ControlPlaneAddr string
	JoinToken        string
	Name             string
	Role             v1.SVIDRole
}

// Enroll performs the trust-on-first-use bootstrap handshake: it dials
// ControlPlaneAddr without verifying the peer — there is no trust bundle
// to verify it against yet, the same problem kubeadm solves with a
// discovery-token CA hash pinned out of band — presents JoinToken and a
// freshly generated CSR over that connection, and returns an SVID built
// from the response. Every connection after this one should go through
// (*SVID).ClientTLSConfig, which does verify the peer.
func Enroll(ctx context.Context, cfg EnrollConfig) (*SVID, error) {
	csrDER, key, err := GenerateCSR(cfg.Name)
	if err != nil {
		return nil, err
	}

	bootstrapTLS := &tls.Config{
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, //nolint:gosec // TOFU by design — see doc comment above
	}

	conn, err := grpc.NewClient(cfg.ControlPlaneAddr, grpc.WithTransportCredentials(credentials.NewTLS(bootstrapTLS)))
	if err != nil {
		return nil, fmt.Errorf("identity: dial %s: %w", cfg.ControlPlaneAddr, err)
	}
	defer conn.Close()

	enrollCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resp, err := v1.NewIdentityServiceClient(conn).RequestSVID(enrollCtx, &v1.RequestSVIDRequest{
		JoinToken: cfg.JoinToken,
		CsrDer:    csrDER,
		NodeName:  cfg.Name,
		Role:      cfg.Role,
	})
	if err != nil {
		return nil, fmt.Errorf("identity: RequestSVID: %w", err)
	}

	cert, err := x509.ParseCertificate(resp.GetCertDer())
	if err != nil {
		return nil, fmt.Errorf("identity: parse issued certificate: %w", err)
	}
	caCert, err := x509.ParseCertificate(resp.GetTrustBundleDer())
	if err != nil {
		return nil, fmt.Errorf("identity: parse trust bundle: %w", err)
	}

	bundle := x509.NewCertPool()
	bundle.AddCert(caCert)

	return NewSVID(key, cert, bundle), nil
}
