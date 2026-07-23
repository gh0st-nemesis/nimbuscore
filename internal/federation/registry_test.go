package federation_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/admission"
	"github.com/gh0st-nemesis/nimbuscore/internal/apiserver"
	"github.com/gh0st-nemesis/nimbuscore/internal/federation"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

func newTestCluster(t *testing.T, pods ...*v1.Pod) grpc.ClientConnInterface {
	t.Helper()

	s := store.NewMemStore()
	grpcServer := grpc.NewServer()
	v1.RegisterPodServiceServer(grpcServer, apiserver.NewPodService(s, admission.NewChain()))

	lis := bufconn.Listen(1 << 20)
	go grpcServer.Serve(lis) //nolint:errcheck
	t.Cleanup(grpcServer.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	client := v1.NewPodServiceClient(conn)
	for _, p := range pods {
		if _, err := client.CreatePod(context.Background(), &v1.CreatePodRequest{Pod: p}); err != nil {
			t.Fatalf("seed pod %s: %v", p.GetMetadata().GetName(), err)
		}
	}
	return conn
}

func testPod(name string) *v1.Pod {
	return &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:     &v1.PodSpec{Containers: []*v1.Container{{Name: "app", Image: "nginx"}}},
	}
}

func TestListPodsAllMergesResultsFromEveryCluster(t *testing.T) {
	east := newTestCluster(t, testPod("web-0"), testPod("web-1"))
	west := newTestCluster(t, testPod("web-2"))

	reg := federation.NewRegistry()
	reg.Register("east", east)
	reg.Register("west", west)

	results := reg.ListPodsAll(context.Background(), "default")
	if len(results) != 2 {
		t.Fatalf("got %d cluster results, want 2", len(results))
	}

	total := 0
	byCluster := make(map[string]int)
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("cluster %s returned error: %v", r.Cluster, r.Err)
		}
		total += len(r.Items)
		byCluster[r.Cluster] = len(r.Items)
	}
	if total != 3 {
		t.Errorf("total pods across clusters = %d, want 3", total)
	}
	if byCluster["east"] != 2 || byCluster["west"] != 1 {
		t.Errorf("per-cluster counts = %v, want east=2 west=1", byCluster)
	}
}

func TestListPodsAllTakesPartialResultsWhenOneClusterIsDown(t *testing.T) {
	up := newTestCluster(t, testPod("web-0"))

	down := grpc.NewServer()
	downLis := bufconn.Listen(1 << 20)
	go down.Serve(downLis) //nolint:errcheck
	downConn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return downLis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	down.Stop()
	downConn.Close()

	reg := federation.NewRegistry()
	reg.Register("up", up)
	reg.Register("down", downConn)

	results := reg.ListPodsAll(context.Background(), "default")
	if len(results) != 2 {
		t.Fatalf("got %d cluster results, want 2", len(results))
	}

	var upResult, downResult *federation.ClusterPods
	for i := range results {
		switch results[i].Cluster {
		case "up":
			upResult = &results[i]
		case "down":
			downResult = &results[i]
		}
	}
	if upResult == nil || upResult.Err != nil || len(upResult.Items) != 1 {
		t.Errorf("expected the healthy cluster to return its pod despite the other being down: %+v", upResult)
	}
	if downResult == nil || downResult.Err == nil {
		t.Errorf("expected the down cluster to report an error, got %+v", downResult)
	}
}

func TestUnregisterRemovesClusterFromFanOut(t *testing.T) {
	east := newTestCluster(t, testPod("web-0"))
	reg := federation.NewRegistry()
	reg.Register("east", east)
	reg.Unregister("east")

	if got := reg.Clusters(); len(got) != 0 {
		t.Errorf("Clusters() = %v, want empty after Unregister", got)
	}
	if results := reg.ListPodsAll(context.Background(), "default"); len(results) != 0 {
		t.Errorf("ListPodsAll returned %d results after Unregister, want 0", len(results))
	}
}
