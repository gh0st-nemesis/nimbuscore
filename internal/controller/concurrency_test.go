package controller

import (
	"context"
	"testing"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/scheduler"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

func TestDeploymentReconcilerDoesNotClobberConcurrentReplicaChange(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemStore()
	deployments := registry.New(s, "deployments", func() *v1.Deployment { return &v1.Deployment{} })
	pods := registry.New(s, "pods", func() *v1.Pod { return &v1.Pod{} })
	nodes := registry.New(s, "nodes", func() *v1.Node { return &v1.Node{} })
	seedReadyNode(t, ctx, nodes, "node-1")

	reconciler := NewDeploymentReconciler(deployments, pods, nodes, scheduler.New(), 0)
	hpa := NewHorizontalAutoscaler(deployments, pods, nodes, 0)

	d := &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas:    1,
			MinReplicas: 1,
			MaxReplicas: 5,
			Selector:    map[string]string{"app": "web"},
			Template: &v1.PodSpec{Containers: []*v1.Container{{
				Name:      "web",
				Resources: &v1.ResourceRequirements{Requests: &v1.ResourceList{MemoryBytes: 100 << 20}},
			}}},
		},
	}
	if err := deployments.Put(ctx, "default", "web", d); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}

	staleCopy, err := deployments.Get(ctx, "default", "web")
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}

	existingPod := &v1.Pod{
		Metadata: &v1.ObjectMeta{
			Name:      "web-0",
			Namespace: "default",
			Labels:    map[string]string{ownerLabel: "web"},
		},
		Spec: &v1.PodSpec{
			NodeName:   "node-1",
			Containers: []*v1.Container{{Name: "web"}},
		},
		Status: &v1.PodStatus{Phase: v1.PodPhase_POD_PHASE_RUNNING, MemoryUsageBytes: 300 << 20},
	}
	if err := pods.Put(ctx, "default", "web-0", existingPod); err != nil {
		t.Fatalf("seed pod: %v", err)
	}
	if err := hpa.reconcileOne(ctx, staleCopy); err != nil {
		t.Fatalf("hpa reconcileOne: %v", err)
	}

	afterHPA, err := deployments.Get(ctx, "default", "web")
	if err != nil {
		t.Fatalf("get deployment after hpa: %v", err)
	}
	if afterHPA.GetSpec().GetReplicas() != 3 {
		t.Fatalf("hpa did not scale up: replicas = %d, want 3", afterHPA.GetSpec().GetReplicas())
	}

	if err := reconciler.reconcileOne(ctx, staleCopy); err != nil {
		t.Fatalf("deployment reconciler reconcileOne: %v", err)
	}

	final, err := deployments.Get(ctx, "default", "web")
	if err != nil {
		t.Fatalf("get final deployment: %v", err)
	}
	if final.GetSpec().GetReplicas() != 3 {
		t.Errorf("deployment-controller clobbered the HPA's replica change: replicas = %d, want 3", final.GetSpec().GetReplicas())
	}
}
