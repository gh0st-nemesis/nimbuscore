package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/spiffe/go-spiffe/v2/spiffeid"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/agent"
	"github.com/gh0st-nemesis/nimbuscore/internal/identity"
	"github.com/gh0st-nemesis/nimbuscore/internal/telemetry"
)

func main() {
	fs := flag.NewFlagSet("nimbus-agent", flag.ExitOnError)
	nodeName := fs.String("node-name", defaultNodeName(), "unique node name")
	controlPlaneAddr := fs.String("control-plane-addr", "", "gRPC API address of the control-plane leader (required)")
	joinToken := fs.String("join-token", "", "shared bootstrap token (required)")
	cpuMillis := fs.Int64("cpu-millis", 2000, "advertised allocatable CPU, in millicores")
	memoryBytes := fs.Int64("memory-bytes", 2<<30, "advertised allocatable memory, in bytes")
	heartbeatInterval := fs.Duration("heartbeat-interval", 5*time.Second, "interval between heartbeats")
	otelExporter := fs.String("otel-exporter", "none", "OpenTelemetry exporter: none, stdout, or otlp")
	otlpEndpoint := fs.String("otlp-endpoint", "127.0.0.1:4317", "OTLP gRPC collector endpoint (when -otel-exporter=otlp)")
	fs.Parse(os.Args[1:])

	if *controlPlaneAddr == "" || *joinToken == "" {
		log.Fatal("agent: -control-plane-addr and -join-token are required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	otelProvider, err := telemetry.Setup(ctx, telemetry.Config{
		ServiceName:  "nimbus-agent",
		Exporter:     *otelExporter,
		OTLPEndpoint: *otlpEndpoint,
	})
	if err != nil {
		log.Fatalf("agent: telemetry: %v", err)
	}
	defer otelProvider.Shutdown(context.Background())

	svid, _, err := identity.Enroll(ctx, identity.EnrollConfig{
		ControlPlaneAddr: *controlPlaneAddr,
		JoinToken:        *joinToken,
		Name:             *nodeName,
		Role:             v1.SVIDRole_SVID_ROLE_NODE,
	})
	if err != nil {
		log.Fatalf("agent: enroll: %v", err)
	}

	selfID, err := svid.ID()
	if err != nil {
		log.Fatalf("agent: %v", err)
	}
	log.Printf("agent: identity %s", selfID)
	expectControlPlane := spiffeid.MatchMemberOf(selfID.TrustDomain())

	conn, err := grpc.NewClient(*controlPlaneAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(svid.ClientTLSConfig(expectControlPlane))),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		log.Fatalf("agent: dial control plane: %v", err)
	}
	defer conn.Close()

	nodeClient := v1.NewNodeServiceClient(conn)
	capacity := &v1.ResourceList{CpuMillis: *cpuMillis, MemoryBytes: *memoryBytes}

	if _, err := nodeClient.CreateNode(ctx, &v1.CreateNodeRequest{
		Node: &v1.Node{
			Metadata: &v1.ObjectMeta{Name: *nodeName},
			Status: &v1.NodeStatus{
				Ready:             true,
				Capacity:          capacity,
				Allocatable:       capacity,
				LastHeartbeatUnix: time.Now().Unix(),
			},
		},
	}); err != nil {
		log.Fatalf("agent: register node: %v", err)
	}
	log.Printf("agent: node %q registered with the control plane", *nodeName)

	go heartbeatLoop(ctx, nodeClient, *nodeName, capacity, *heartbeatInterval)

	a := agent.New(agent.Config{NodeName: *nodeName}, v1.NewPodServiceClient(conn))
	if err := a.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("agent: %v", err)
	}
}

func heartbeatLoop(ctx context.Context, client v1.NodeServiceClient, name string, capacity *v1.ResourceList, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := client.Heartbeat(ctx, &v1.HeartbeatRequest{
				Name:        name,
				Capacity:    capacity,
				Allocatable: capacity,
			}); err != nil {
				log.Printf("agent: heartbeat: %v", err)
			}
		}
	}
}

func defaultNodeName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "node"
	}
	return host
}
