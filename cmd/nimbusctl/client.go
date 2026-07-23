package main

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/spiffe/go-spiffe/v2/spiffeid"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/identity"
)

type clientConfig struct {
	controlPlaneAddr string
	joinToken        string
	clientName       string
}

func dial(ctx context.Context, cfg clientConfig) (*grpc.ClientConn, error) {
	if cfg.controlPlaneAddr == "" {
		return nil, fmt.Errorf("--control-plane-addr (or $NIMBUS_API_ADDR) is required")
	}
	if cfg.joinToken == "" {
		return nil, fmt.Errorf("--join-token (or $NIMBUS_JOIN_TOKEN) is required")
	}

	svid, _, err := identity.Enroll(ctx, identity.EnrollConfig{
		ControlPlaneAddr: cfg.controlPlaneAddr,
		JoinToken:        cfg.joinToken,
		Name:             cfg.clientName,
		Role:             v1.SVIDRole_SVID_ROLE_CLIENT,
	})
	if err != nil {
		return nil, fmt.Errorf("enroll with control plane %s: %w", cfg.controlPlaneAddr, err)
	}

	selfID, err := svid.ID()
	if err != nil {
		return nil, err
	}

	conn, err := grpc.NewClient(cfg.controlPlaneAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(svid.ClientTLSConfig(spiffeid.MatchMemberOf(selfID.TrustDomain())))),
	)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", cfg.controlPlaneAddr, err)
	}
	return conn, nil
}
