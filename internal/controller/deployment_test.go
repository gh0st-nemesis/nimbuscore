package controller

import (
	"context"
	"testing"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

func newTestReconciler() (*DeploymentReconciler, *registry.Registry[*v1.Deployment], *registry.Registry[*v1.Pod]) {
	s := store.NewMemStore()
	deployments := registry.New(s, "deployments", func() *v1.Deployment { return &v1.Deployment{} })
	pods := registry.New(s, "pods", func() *v1.Pod { return &v1.Pod{} })
	return NewDeploymentReconciler(deployments, pods, 0), deployments, pods
}

func TestReconcileOneScalesUp(t *testing.T) {
	ctx := context.Background()
	r, deployments, pods := newTestReconciler()

	d := &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas: 3,
			Selector: map[string]string{"app": "web"},
			Template: &v1.PodSpec{Containers: []*v1.Container{{Name: "web", Image: "nginx"}}},
		},
	}
	if err := deployments.Put(ctx, "default", "web", d); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}

	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne: %v", err)
	}

	got, err := pods.List(ctx, "default")
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 pods, got %d", len(got))
	}
	for _, p := range got {
		if p.GetMetadata().GetLabels()[ownerLabel] != "web" {
			t.Errorf("pod %s missing owner label", p.GetMetadata().GetName())
		}
	}

	updated, err := deployments.Get(ctx, "default", "web")
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if updated.GetStatus().GetReplicas() != 3 {
		t.Errorf("status.replicas = %d, want 3", updated.GetStatus().GetReplicas())
	}
}

func TestReconcileOneScalesDown(t *testing.T) {
	ctx := context.Background()
	r, deployments, pods := newTestReconciler()

	d := &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas: 3,
			Selector: map[string]string{"app": "web"},
			Template: &v1.PodSpec{Containers: []*v1.Container{{Name: "web", Image: "nginx"}}},
		},
	}
	if err := deployments.Put(ctx, "default", "web", d); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne (scale up): %v", err)
	}

	d.Spec.Replicas = 1
	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne (scale down): %v", err)
	}

	got, err := pods.List(ctx, "default")
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 pod after scale down, got %d", len(got))
	}
}

func TestReconcileOneCountsReadyReplicas(t *testing.T) {
	ctx := context.Background()
	r, deployments, pods := newTestReconciler()

	d := &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas: 2,
			Selector: map[string]string{"app": "web"},
			Template: &v1.PodSpec{Containers: []*v1.Container{{Name: "web", Image: "nginx"}}},
		},
	}
	if err := deployments.Put(ctx, "default", "web", d); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne: %v", err)
	}

	all, err := pods.List(ctx, "default")
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	all[0].Status.Phase = v1.PodPhase_POD_PHASE_RUNNING
	if err := pods.Put(ctx, "default", all[0].GetMetadata().GetName(), all[0]); err != nil {
		t.Fatalf("update pod status: %v", err)
	}

	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne (recount): %v", err)
	}

	updated, err := deployments.Get(ctx, "default", "web")
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if updated.GetStatus().GetReadyReplicas() != 1 {
		t.Errorf("status.ready_replicas = %d, want 1", updated.GetStatus().GetReadyReplicas())
	}
}
