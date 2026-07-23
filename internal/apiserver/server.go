// Package apiserver implements the control-plane API server (design doc
// section 03): gRPC/Protobuf internally, with a REST gateway for
// external clients planned for a later pass.
package apiserver

import (
	"context"
	"crypto/tls"
	"log"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Config holds the API server startup parameters.
type Config struct {
	Addr string
	// TLSConfig, when set, is used both to present this server's SVID
	// and to accept (not require — see identity.SVID.ServerTLSConfig)
	// client certificates, so unenrolled callers can still reach
	// IdentityService.RequestSVID. Nil means plaintext, dev-only.
	TLSConfig *tls.Config
	// AuthInterceptor rejects RPCs from callers that didn't present a
	// valid SPIFFE client certificate (design doc section 05:
	// deny-by-default). Nil means no enforcement — dev-only.
	AuthInterceptor grpc.UnaryServerInterceptor
}

// Server is the NimbusCore control-plane API server. Phase 1 exposed it
// as a plaintext gRPC listener; Phase 2 adds mTLS (SPIFFE identities)
// and an authentication interceptor. The REST gateway (grpc-gateway) and
// the non-disableable admission pipeline (signature verification, RBAC,
// quotas) remain later-phase work.
type Server struct {
	addr string
	grpc *grpc.Server
}

// New returns a Server ready to have resource services registered on
// its GRPCServer() before Serve is called.
func New(cfg Config) *Server {
	var opts []grpc.ServerOption
	if cfg.TLSConfig != nil {
		opts = append(opts, grpc.Creds(credentials.NewTLS(cfg.TLSConfig)))
	}
	if cfg.AuthInterceptor != nil {
		opts = append(opts, grpc.UnaryInterceptor(cfg.AuthInterceptor))
	}

	return &Server{
		addr: cfg.Addr,
		grpc: grpc.NewServer(opts...),
	}
}

// GRPCServer exposes the underlying *grpc.Server so resource services
// (Pod, Node, Deployment, Identity, Admin) can register themselves.
func (s *Server) GRPCServer() *grpc.Server {
	return s.grpc
}

// Serve blocks, accepting connections until ctx is cancelled or the
// listener fails.
func (s *Server) Serve(ctx context.Context) error {
	lis, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}

	go func() {
		<-ctx.Done()
		s.grpc.GracefulStop()
	}()

	log.Printf("apiserver: listening on %s", s.addr)
	return s.grpc.Serve(lis)
}
