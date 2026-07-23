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

type EnrollConfig struct {
	ControlPlaneAddr string
	JoinToken        string
	Name             string
	Role             v1.SVIDRole
}

func Enroll(ctx context.Context, cfg EnrollConfig) (*SVID, []byte, error) {
	csrDER, key, err := GenerateCSR(cfg.Name)
	if err != nil {
		return nil, nil, err
	}

	bootstrapTLS := &tls.Config{
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true,
	}

	conn, err := grpc.NewClient(cfg.ControlPlaneAddr, grpc.WithTransportCredentials(credentials.NewTLS(bootstrapTLS)))
	if err != nil {
		return nil, nil, fmt.Errorf("identity: dial %s: %w", cfg.ControlPlaneAddr, err)
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
		return nil, nil, fmt.Errorf("identity: RequestSVID: %w", err)
	}

	cert, err := x509.ParseCertificate(resp.GetCertDer())
	if err != nil {
		return nil, nil, fmt.Errorf("identity: parse issued certificate: %w", err)
	}
	caCert, err := x509.ParseCertificate(resp.GetTrustBundleDer())
	if err != nil {
		return nil, nil, fmt.Errorf("identity: parse trust bundle: %w", err)
	}

	bundle := x509.NewCertPool()
	bundle.AddCert(caCert)

	return NewSVID(key, cert, bundle), resp.GetDataEncryptionKey(), nil
}
