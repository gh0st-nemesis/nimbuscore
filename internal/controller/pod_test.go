package controller

import (
	"context"
	"testing"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/scheduler"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

func newTestPodReconciler() (*PodReconciler, *registry.Registry[*v1.Pod], *registry.Registry[*v1.Node]) {
	s := store.NewMemStore()
	pods := registry.New(s, "pods", func() *v1.Pod { return &v1.Pod{} })
	nodes := registry.New(s, "nodes", func() *v1.Node { return &v1.Node{} })
	return NewPodReconciler(pods, nodes, scheduler.New(), 0), pods, nodes
}

func TestPodReconcilerSchedulesStandaloneUnassignedPod(t *testing.T) {
	ctx := context.Background()
	r, pods, nodes := newTestPodReconciler()
	seedReadyNode(t, ctx, nodes, "node-1")

	pod := &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec:     &v1.PodSpec{Containers: []*v1.Container{{Name: "app", Image: "nginx"}}},
	}
	if err := pods.Put(ctx, "default", "web", pod); err != nil {
		t.Fatalf("seed pod: %v", err)
	}

	r.reconcileAll(ctx)

	got, err := pods.Get(ctx, "default", "web")
	if err != nil {
		t.Fatalf("get pod: %v", err)
	}
	if got.GetSpec().GetNodeName() != "node-1" {
		t.Errorf("node_name = %q, want %q", got.GetSpec().GetNodeName(), "node-1")
	}
}

func TestPodReconcilerIgnoresDeploymentOwnedPods(t *testing.T) {
	ctx := context.Background()
	r, pods, nodes := newTestPodReconciler()
	seedReadyNode(t, ctx, nodes, "node-1")

	pod := &v1.Pod{
		Metadata: &v1.ObjectMeta{
			Name:      "web-0",
			Namespace: "default",
			Labels:    map[string]string{ownerLabel: "web"},
		},
		Spec: &v1.PodSpec{Containers: []*v1.Container{{Name: "app", Image: "nginx"}}},
	}
	if err := pods.Put(ctx, "default", "web-0", pod); err != nil {
		t.Fatalf("seed pod: %v", err)
	}

	r.reconcileAll(ctx)

	got, err := pods.Get(ctx, "default", "web-0")
	if err != nil {
		t.Fatalf("get pod: %v", err)
	}
	if got.GetSpec().GetNodeName() != "" {
		t.Errorf("node_name = %q, want unscheduled — deployment-owned pods are the DeploymentReconciler's job", got.GetSpec().GetNodeName())
	}
}

func TestPodReconcilerSkipsAlreadyScheduledAndTerminalPods(t *testing.T) {
	ctx := context.Background()
	r, pods, nodes := newTestPodReconciler()
	seedReadyNode(t, ctx, nodes, "node-1")

	scheduled := &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: "scheduled", Namespace: "default"},
		Spec:     &v1.PodSpec{NodeName: "node-1", Containers: []*v1.Container{{Name: "app"}}},
	}
	terminal := &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: "done", Namespace: "default"},
		Spec:     &v1.PodSpec{Containers: []*v1.Container{{Name: "app"}}},
		Status:   &v1.PodStatus{Phase: v1.PodPhase_POD_PHASE_SUCCEEDED},
	}
	if err := pods.Put(ctx, "default", "scheduled", scheduled); err != nil {
		t.Fatalf("seed scheduled pod: %v", err)
	}
	if err := pods.Put(ctx, "default", "done", terminal); err != nil {
		t.Fatalf("seed terminal pod: %v", err)
	}

	r.reconcileAll(ctx)

	got, err := pods.Get(ctx, "default", "done")
	if err != nil {
		t.Fatalf("get pod: %v", err)
	}
	if got.GetSpec().GetNodeName() != "" {
		t.Errorf("terminal pod should not be scheduled, got node %q", got.GetSpec().GetNodeName())
	}
}
