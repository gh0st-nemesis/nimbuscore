package apiserver_test

import (
	"context"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/admission"
	"github.com/gh0st-nemesis/nimbuscore/internal/apiserver"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

func TestGRPCCallsProduceSpans(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background())

	st := store.NewMemStore()
	grpcServer := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler(otelgrpc.WithTracerProvider(tp))))
	v1.RegisterPodServiceServer(grpcServer, apiserver.NewPodService(st, admission.NewChain()))

	lis := bufconn.Listen(1 << 20)
	go grpcServer.Serve(lis)
	defer grpcServer.Stop()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler(otelgrpc.WithTracerProvider(tp))),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	podClient := v1.NewPodServiceClient(conn)
	_, err = podClient.CreatePod(context.Background(), &v1.CreatePodRequest{
		Pod: &v1.Pod{
			Metadata: &v1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec:     &v1.PodSpec{Containers: []*v1.Container{{Name: "app", Image: "nginx"}}},
		},
	})
	if err != nil {
		t.Fatalf("CreatePod: %v", err)
	}

	if err := tp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans were recorded for the CreatePod call")
	}

	found := false
	var names []string
	for _, s := range spans {
		names = append(names, s.Name)
		if strings.Contains(s.Name, "CreatePod") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a span mentioning CreatePod, got spans: %v", names)
	}
}
