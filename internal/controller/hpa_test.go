package controller

import (
	"context"
	"fmt"
	"testing"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

func newTestHPA(t *testing.T) (*HorizontalAutoscaler, *registry.Registry[*v1.Deployment], *registry.Registry[*v1.Pod]) {
	s := store.NewMemStore()
	deployments := registry.New(s, "deployments", func() *v1.Deployment { return &v1.Deployment{} })
	pods := registry.New(s, "pods", func() *v1.Pod { return &v1.Pod{} })
	nodes := registry.New(s, "nodes", func() *v1.Node { return &v1.Node{} })
	seedReadyNode(t, context.Background(), nodes, "node-1")
	seedReadyNode(t, context.Background(), nodes, "node-2")
	return NewHorizontalAutoscaler(deployments, pods, nodes, 0), deployments, pods
}

func seedRunningPod(t *testing.T, ctx context.Context, pods *registry.Registry[*v1.Pod], deploymentName, podName string, memoryUsageBytes int64) {
	t.Helper()
	p := &v1.Pod{
		Metadata: &v1.ObjectMeta{
			Name:      podName,
			Namespace: "default",
			Labels:    map[string]string{OwnerDeploymentLabel: deploymentName},
		},
		Status: &v1.PodStatus{
			Phase:            v1.PodPhase_POD_PHASE_RUNNING,
			MemoryUsageBytes: memoryUsageBytes,
		},
	}
	if err := pods.Put(ctx, "default", podName, p); err != nil {
		t.Fatalf("seed pod %s: %v", podName, err)
	}
}

func TestHPAScalesUpUnderPressure(t *testing.T) {
	ctx := context.Background()
	r, deployments, pods := newTestHPA(t)

	d := &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas:    1,
			MinReplicas: 1,
			MaxReplicas: 5,
			Template: &v1.PodSpec{Containers: []*v1.Container{{
				Name:      "web",
				Resources: &v1.ResourceRequirements{Requests: &v1.ResourceList{MemoryBytes: 100 << 20}},
			}}},
		},
	}
	if err := deployments.Put(ctx, "default", "web", d); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	seedRunningPod(t, ctx, pods, "web", "web-0", 300<<20)

	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne: %v", err)
	}

	updated, err := deployments.Get(ctx, "default", "web")
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if updated.GetSpec().GetReplicas() != 3 {
		t.Errorf("replicas = %d, want 3 (300%% utilization x 1 replica)", updated.GetSpec().GetReplicas())
	}
}

func TestHPAScalesDownWhenIdle(t *testing.T) {
	ctx := context.Background()
	r, deployments, pods := newTestHPA(t)

	d := &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas:    4,
			MinReplicas: 1,
			MaxReplicas: 5,
			Template: &v1.PodSpec{Containers: []*v1.Container{{
				Name:      "web",
				Resources: &v1.ResourceRequirements{Requests: &v1.ResourceList{MemoryBytes: 100 << 20}},
			}}},
		},
	}
	if err := deployments.Put(ctx, "default", "web", d); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	for i := range 4 {
		seedRunningPod(t, ctx, pods, "web", fmt.Sprintf("web-%d", i), 10<<20)
	}

	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne: %v", err)
	}

	updated, err := deployments.Get(ctx, "default", "web")
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if updated.GetSpec().GetReplicas() != 1 {
		t.Errorf("replicas = %d, want 1 (10%% utilization x 4 replicas, floored to min)", updated.GetSpec().GetReplicas())
	}
}

func TestHPARespectsMaxReplicas(t *testing.T) {
	ctx := context.Background()
	r, deployments, pods := newTestHPA(t)

	d := &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas:    1,
			MinReplicas: 1,
			MaxReplicas: 2,
			Template: &v1.PodSpec{Containers: []*v1.Container{{
				Name:      "web",
				Resources: &v1.ResourceRequirements{Requests: &v1.ResourceList{MemoryBytes: 100 << 20}},
			}}},
		},
	}
	if err := deployments.Put(ctx, "default", "web", d); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	seedRunningPod(t, ctx, pods, "web", "web-0", 1000<<20)

	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne: %v", err)
	}

	updated, err := deployments.Get(ctx, "default", "web")
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if updated.GetSpec().GetReplicas() != 2 {
		t.Errorf("replicas = %d, want 2 (capped at max_replicas)", updated.GetSpec().GetReplicas())
	}
}

func TestHPACapsScaleUpToClusterCapacity(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemStore()
	deployments := registry.New(s, "deployments", func() *v1.Deployment { return &v1.Deployment{} })
	pods := registry.New(s, "pods", func() *v1.Pod { return &v1.Pod{} })
	nodes := registry.New(s, "nodes", func() *v1.Node { return &v1.Node{} })

	n := &v1.Node{
		Metadata: &v1.ObjectMeta{Name: "tiny"},
		Status:   &v1.NodeStatus{Ready: true, Allocatable: &v1.ResourceList{MemoryBytes: 250 << 20}},
	}
	if err := nodes.Put(ctx, "", "tiny", n); err != nil {
		t.Fatalf("seed node: %v", err)
	}

	r := NewHorizontalAutoscaler(deployments, pods, nodes, 0)

	d := &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas:    1,
			MinReplicas: 1,
			MaxReplicas: 10,
			Template: &v1.PodSpec{Containers: []*v1.Container{{
				Name:      "web",
				Resources: &v1.ResourceRequirements{Requests: &v1.ResourceList{MemoryBytes: 100 << 20}},
			}}},
		},
	}
	if err := deployments.Put(ctx, "default", "web", d); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	seedRunningPod(t, ctx, pods, "web", "web-0", 500<<20)

	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne: %v", err)
	}

	updated, err := deployments.Get(ctx, "default", "web")
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if updated.GetSpec().GetReplicas() != 3 {
		t.Errorf("replicas = %d, want 3 (500%% utilization wants 5, but 250MiB cluster capacity / 100MiB per replica only fits 2 more than the current 1)", updated.GetSpec().GetReplicas())
	}
}

func TestHPADisabledWithoutMaxReplicas(t *testing.T) {
	ctx := context.Background()
	r, deployments, pods := newTestHPA(t)

	d := &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas: 2,
			Template: &v1.PodSpec{Containers: []*v1.Container{{
				Name:      "web",
				Resources: &v1.ResourceRequirements{Requests: &v1.ResourceList{MemoryBytes: 100 << 20}},
			}}},
		},
	}
	if err := deployments.Put(ctx, "default", "web", d); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	seedRunningPod(t, ctx, pods, "web", "web-0", 1000<<20)

	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne: %v", err)
	}

	updated, err := deployments.Get(ctx, "default", "web")
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if updated.GetSpec().GetReplicas() != 2 {
		t.Errorf("replicas changed to %d, want unchanged 2 (autoscaling disabled without max_replicas)", updated.GetSpec().GetReplicas())
	}
}
