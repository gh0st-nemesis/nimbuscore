package apiserver

import (
	"context"
	"crypto/tls"
	"log"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
)

type Config struct {
	Addr string

	TLSConfig *tls.Config

	AuthInterceptor grpc.UnaryServerInterceptor
}

type Server struct {
	addr string
	grpc *grpc.Server
}

func New(cfg Config) *Server {
	opts := []grpc.ServerOption{grpc.StatsHandler(otelgrpc.NewServerHandler())}
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

func (s *Server) GRPCServer() *grpc.Server {
	return s.grpc
}

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
