package apiserver_test

import (
	"context"
	"testing"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/apiserver"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

func TestServiceResolvesEndpointsFromMatchingRunningPods(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemStore()

	nodes := registry.New(s, "nodes", func() *v1.Node { return &v1.Node{} })
	if err := nodes.Put(ctx, "", "worker-1", &v1.Node{
		Metadata: &v1.ObjectMeta{Name: "worker-1"},
		Status:   &v1.NodeStatus{Ready: true, InternalIp: "10.0.0.5"},
	}); err != nil {
		t.Fatalf("seed node: %v", err)
	}

	pods := registry.New(s, "pods", func() *v1.Pod { return &v1.Pod{} })
	if err := pods.Put(ctx, "default", "web-0", &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: "web-0", Namespace: "default", Labels: map[string]string{"app": "web"}},
		Spec:     &v1.PodSpec{NodeName: "worker-1"},
		Status:   &v1.PodStatus{Phase: v1.PodPhase_POD_PHASE_RUNNING},
	}); err != nil {
		t.Fatalf("seed running pod: %v", err)
	}
	if err := pods.Put(ctx, "default", "web-1", &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: "web-1", Namespace: "default", Labels: map[string]string{"app": "web"}},
		Spec:     &v1.PodSpec{NodeName: "worker-1"},
		Status:   &v1.PodStatus{Phase: v1.PodPhase_POD_PHASE_PENDING},
	}); err != nil {
		t.Fatalf("seed pending pod: %v", err)
	}
	if err := pods.Put(ctx, "default", "other", &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: "other", Namespace: "default", Labels: map[string]string{"app": "not-web"}},
		Spec:     &v1.PodSpec{NodeName: "worker-1"},
		Status:   &v1.PodStatus{Phase: v1.PodPhase_POD_PHASE_RUNNING},
	}); err != nil {
		t.Fatalf("seed non-matching pod: %v", err)
	}

	svc := apiserver.NewServiceService(s)
	created, err := svc.CreateService(ctx, &v1.CreateServiceRequest{
		Service: &v1.Service{
			Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
			Spec:     &v1.ServiceSpec{Selector: map[string]string{"app": "web"}, Port: 80},
		},
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	endpoints := created.GetStatus().GetEndpoints()
	if len(endpoints) != 1 {
		t.Fatalf("got %d endpoints, want 1 (only the Running pod matching the selector)", len(endpoints))
	}
	if endpoints[0].GetPodName() != "web-0" {
		t.Errorf("endpoint pod = %q, want web-0", endpoints[0].GetPodName())
	}
	if endpoints[0].GetNodeIp() != "10.0.0.5" {
		t.Errorf("endpoint node_ip = %q, want 10.0.0.5", endpoints[0].GetNodeIp())
	}
	if endpoints[0].GetNodePort() != 80 {
		t.Errorf("endpoint node_port = %d, want 80 (falls back to spec.port when target_port is unset)", endpoints[0].GetNodePort())
	}
}

func TestServiceWithNoMatchingPodsHasNoEndpoints(t *testing.T) {
	ctx := context.Background()
	svc := apiserver.NewServiceService(store.NewMemStore())

	created, err := svc.CreateService(ctx, &v1.CreateServiceRequest{
		Service: &v1.Service{
			Metadata: &v1.ObjectMeta{Name: "empty", Namespace: "default"},
			Spec:     &v1.ServiceSpec{Selector: map[string]string{"app": "ghost"}, Port: 80},
		},
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	if len(created.GetStatus().GetEndpoints()) != 0 {
		t.Errorf("got %d endpoints, want 0", len(created.GetStatus().GetEndpoints()))
	}
}
