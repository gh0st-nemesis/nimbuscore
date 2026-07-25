package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/spiffe/go-spiffe/v2/spiffeid"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/agent"
	"github.com/gh0st-nemesis/nimbuscore/internal/identity"
	"github.com/gh0st-nemesis/nimbuscore/internal/mesh"
	"github.com/gh0st-nemesis/nimbuscore/internal/telemetry"
)

func main() {
	fs := flag.NewFlagSet("nimbus-agent", flag.ExitOnError)
	nodeName := fs.String("node-name", defaultNodeName(), "unique node name")
	controlPlaneAddr := fs.String("control-plane-addr", "", "gRPC API address of the control-plane leader (required)")
	joinToken := fs.String("join-token", "", "shared bootstrap token (required)")
	cpuMillis := fs.Int64("cpu-millis", 2000, "advertised allocatable CPU, in millicores")
	memoryBytes := fs.Int64("memory-bytes", 2<<30, "advertised allocatable memory, in bytes")
	acceleratorsFlag := fs.String("accelerators", "", "advertised accelerators, e.g. \"nvidia.com/gpu=2\" (comma-separated for multiple kinds)")
	nodeIP := fs.String("node-ip", "", "IP address other nodes/clients can reach this node's containers on (auto-detected from the route to -control-plane-addr if empty)")
	heartbeatInterval := fs.Duration("heartbeat-interval", 5*time.Second, "interval between heartbeats")
	otelExporter := fs.String("otel-exporter", "none", "OpenTelemetry exporter: none, stdout, or otlp")
	otlpEndpoint := fs.String("otlp-endpoint", "127.0.0.1:4317", "OTLP gRPC collector endpoint (when -otel-exporter=otlp)")
	logDir := fs.String("log-dir", "./data/logs", "directory where container stdout/stderr logs are written")
	logAddr := fs.String("log-addr", ":10250", "HTTP listen address serving container logs to the control plane")
	buildDir := fs.String("build-dir", "./data/builds", "directory used to clone and build container images from Git repos")
	buildkitAddr := fs.String("buildkit-addr", "unix:///run/buildkit/buildkitd.sock", "buildkitd address used to build images from Git repos")
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

	identity.StartReenrollRotateLoop(ctx, svid, identity.EnrollConfig{
		ControlPlaneAddr: *controlPlaneAddr,
		JoinToken:        *joinToken,
		Name:             *nodeName,
		Role:             v1.SVIDRole_SVID_ROLE_NODE,
	}, identity.DefaultSVIDTTL)

	breaker := mesh.NewCircuitBreaker(5, 30*time.Second)
	conn, err := grpc.NewClient(*controlPlaneAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(svid.ClientTLSConfig(expectControlPlane))),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		grpc.WithUnaryInterceptor(mesh.NewClientInterceptor(mesh.DefaultRetryPolicy(), breaker)),
	)
	if err != nil {
		log.Fatalf("agent: dial control plane: %v", err)
	}
	defer conn.Close()

	internalIP := *nodeIP
	if internalIP == "" {
		internalIP, err = detectOutboundIP(*controlPlaneAddr)
		if err != nil {
			log.Fatalf("agent: detect node IP (pass -node-ip to set it explicitly): %v", err)
		}
	}

	nodeClient := v1.NewNodeServiceClient(conn)
	capacity := &v1.ResourceList{CpuMillis: *cpuMillis, MemoryBytes: *memoryBytes, Accelerators: parseAccelerators(*acceleratorsFlag)}

	if _, err := nodeClient.CreateNode(ctx, &v1.CreateNodeRequest{
		Node: &v1.Node{
			Metadata: &v1.ObjectMeta{Name: *nodeName},
			Status: &v1.NodeStatus{
				Ready:             true,
				Capacity:          capacity,
				Allocatable:       capacity,
				LastHeartbeatUnix: time.Now().Unix(),
				InternalIp:        internalIP,
			},
		},
	}); err != nil {
		log.Fatalf("agent: register node: %v", err)
	}
	log.Printf("agent: node %q registered with the control plane (internal_ip=%s)", *nodeName, internalIP)

	go heartbeatLoop(ctx, nodeClient, *nodeName, capacity, *heartbeatInterval)

	go func() {
		log.Printf("agent: log server listening on %s", *logAddr)
		if err := agent.StartLogServer(*logAddr, *logDir); err != nil {
			log.Printf("agent: %v", err)
		}
	}()

	a := agent.New(agent.Config{
		NodeName:     *nodeName,
		InternalIP:   internalIP,
		LogDir:       *logDir,
		BuildDir:     *buildDir,
		BuildkitAddr: *buildkitAddr,
	}, v1.NewPodServiceClient(conn), v1.NewServiceServiceClient(conn))
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

func parseAccelerators(s string) map[string]int64 {
	if s == "" {
		return nil
	}
	out := make(map[string]int64)
	for _, entry := range strings.Split(s, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, countStr, ok := strings.Cut(entry, "=")
		if !ok {
			log.Fatalf("agent: invalid -accelerators entry %q, want name=count", entry)
		}
		count, err := strconv.ParseInt(strings.TrimSpace(countStr), 10, 64)
		if err != nil {
			log.Fatalf("agent: invalid count in -accelerators entry %q: %v", entry, err)
		}
		out[strings.TrimSpace(name)] = count
	}
	return out
}

func detectOutboundIP(controlPlaneAddr string) (string, error) {
	conn, err := net.Dial("udp", controlPlaneAddr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String(), nil
}

func defaultNodeName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "node"
	}
	return host
}
