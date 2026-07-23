package controller

import (
	"context"
	"fmt"
	"testing"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

func BenchmarkReconcileOneScaleUp(b *testing.B) {
	ctx := context.Background()

	for b.Loop() {
		b.StopTimer()
		r, deployments, _, nodes := newTestReconciler()
		for i := range 20 {
			seedReadyNode(b, ctx, nodes, fmt.Sprintf("node-%d", i))
		}
		d := &v1.Deployment{
			Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
			Spec: &v1.DeploymentSpec{
				Replicas: 50,
				Selector: map[string]string{"app": "web"},
				Template: &v1.PodSpec{Containers: []*v1.Container{{Name: "web", Image: "nginx"}}},
			},
		}
		if err := deployments.Put(ctx, "default", "web", d); err != nil {
			b.Fatalf("seed deployment: %v", err)
		}
		b.StartTimer()

		if err := r.reconcileOne(ctx, d); err != nil {
			b.Fatalf("reconcileOne: %v", err)
		}
	}
}

func BenchmarkReconcileOneSteadyState(b *testing.B) {
	ctx := context.Background()
	r, deployments, _, nodes := newTestReconciler()
	seedReadyNode(b, ctx, nodes, "node-1")

	d := &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas: 20,
			Selector: map[string]string{"app": "web"},
			Template: &v1.PodSpec{Containers: []*v1.Container{{Name: "web", Image: "nginx"}}},
		},
	}
	if err := deployments.Put(ctx, "default", "web", d); err != nil {
		b.Fatalf("seed deployment: %v", err)
	}
	if err := r.reconcileOne(ctx, d); err != nil {
		b.Fatalf("initial reconcile: %v", err)
	}

	b.ResetTimer()
	for b.Loop() {
		if err := r.reconcileOne(ctx, d); err != nil {
			b.Fatalf("reconcileOne: %v", err)
		}
	}
}
