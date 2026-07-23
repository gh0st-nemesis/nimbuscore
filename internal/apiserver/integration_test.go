package apiserver_test

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/apiserver"
	"github.com/gh0st-nemesis/nimbuscore/internal/controller"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/scheduler"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

// TestDeploymentCreateReconcilesToPods exercises the full Phase 1 loop
// end to end: a gRPC client creates a Deployment through the API
// server, and the controller manager's DeploymentReconciler — running
// concurrently, on its own resync ticker — picks it up and materializes
// the matching Pods, exactly as it would with a real client over the
// network.
func TestDeploymentCreateReconcilesToPods(t *testing.T) {
	st := store.NewMemStore()

	grpcServer := grpc.NewServer()
	v1.RegisterDeploymentServiceServer(grpcServer, apiserver.NewDeploymentService(st))
	v1.RegisterPodServiceServer(grpcServer, apiserver.NewPodService(st))
	v1.RegisterNodeServiceServer(grpcServer, apiserver.NewNodeService(st))

	lis := bufconn.Listen(1 << 20)
	go grpcServer.Serve(lis) //nolint:errcheck // Stop() below always ends this with an expected error

	defer grpcServer.Stop()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	deployments := registry.New(st, "deployments", func() *v1.Deployment { return &v1.Deployment{} })
	pods := registry.New(st, "pods", func() *v1.Pod { return &v1.Pod{} })
	nodes := registry.New(st, "nodes", func() *v1.Node { return &v1.Node{} })

	mgr := controller.NewManager()
	mgr.Register(controller.NewDeploymentReconciler(deployments, pods, nodes, scheduler.New(), 30*time.Millisecond))
	go mgr.Run(t.Context())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	nodeClient := v1.NewNodeServiceClient(conn)
	deploymentClient := v1.NewDeploymentServiceClient(conn)
	podClient := v1.NewPodServiceClient(conn)

	if _, err := nodeClient.CreateNode(ctx, &v1.CreateNodeRequest{
		Node: &v1.Node{
			Metadata: &v1.ObjectMeta{Name: "node-1"},
			Status: &v1.NodeStatus{
				Ready:       true,
				Allocatable: &v1.ResourceList{CpuMillis: 4000, MemoryBytes: 4 << 30},
			},
		},
	}); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	_, err = deploymentClient.CreateDeployment(ctx, &v1.CreateDeploymentRequest{
		Deployment: &v1.Deployment{
			Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
			Spec: &v1.DeploymentSpec{
				Replicas: 2,
				Selector: map[string]string{"app": "web"},
				Template: &v1.PodSpec{Containers: []*v1.Container{{Name: "web", Image: "nginx"}}},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := podClient.ListPods(ctx, &v1.ListPodsRequest{Namespace: "default"})
		if err != nil {
			t.Fatalf("ListPods: %v", err)
		}
		if len(resp.GetItems()) == 2 && allScheduled(resp.GetItems()) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for 2 scheduled pods, got %d", len(resp.GetItems()))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func allScheduled(pods []*v1.Pod) bool {
	for _, p := range pods {
		if p.GetSpec().GetNodeName() == "" {
			return false
		}
	}
	return true
}
