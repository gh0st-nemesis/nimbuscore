// Package apiserver implements the control-plane API server (design doc
// section 03): gRPC/Protobuf internally, with a REST gateway for
// external clients planned for a later pass.
package apiserver

import (
	"context"
	"log"
	"net"

	"google.golang.org/grpc"
)

// Config holds the API server startup parameters.
type Config struct {
	Addr string
}

// Server is the NimbusCore control-plane API server. Phase 1 exposes it
// purely as a gRPC listener; the REST gateway (grpc-gateway) and the
// non-disableable admission pipeline (signature verification, RBAC,
// quotas) land once the resource schema (step B) exists to validate
// against.
type Server struct {
	addr string
	grpc *grpc.Server
}

// New returns a Server ready to have resource services registered on
// its GRPCServer() before Serve is called.
func New(cfg Config) *Server {
	return &Server{
		addr: cfg.Addr,
		grpc: grpc.NewServer(),
	}
}

// GRPCServer exposes the underlying *grpc.Server so resource services
// (Pod, Node, Deployment) can register themselves once defined.
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
