package controller

import (
	"context"
	"testing"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

func TestScheduleUnassignedRespectsAcceleratorAvailability(t *testing.T) {
	ctx := context.Background()
	r, deployments, pods, nodes := newTestReconciler()

	cpuOnly := &v1.Node{
		Metadata: &v1.ObjectMeta{Name: "cpu-only"},
		Status: &v1.NodeStatus{
			Ready:       true,
			Allocatable: &v1.ResourceList{CpuMillis: 4000, MemoryBytes: 4 << 30},
		},
	}
	gpuNode := &v1.Node{
		Metadata: &v1.ObjectMeta{Name: "gpu-node"},
		Status: &v1.NodeStatus{
			Ready: true,
			Allocatable: &v1.ResourceList{
				CpuMillis:    4000,
				MemoryBytes:  4 << 30,
				Accelerators: map[string]int64{"nvidia.com/gpu": 2},
			},
		},
	}
	if err := nodes.Put(ctx, "", "cpu-only", cpuOnly); err != nil {
		t.Fatalf("seed cpu-only node: %v", err)
	}
	if err := nodes.Put(ctx, "", "gpu-node", gpuNode); err != nil {
		t.Fatalf("seed gpu node: %v", err)
	}

	d := &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "training", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas: 1,
			Selector: map[string]string{"app": "training"},
			Template: &v1.PodSpec{Containers: []*v1.Container{{
				Name: "trainer",
				Resources: &v1.ResourceRequirements{
					Requests: &v1.ResourceList{Accelerators: map[string]int64{"nvidia.com/gpu": 1}},
				},
			}}},
		},
	}
	if err := deployments.Put(ctx, "default", "training", d); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}

	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne: %v", err)
	}

	got, err := pods.List(ctx, "default")
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 pod, got %d", len(got))
	}
	if got[0].GetSpec().GetNodeName() != "gpu-node" {
		t.Errorf("scheduled onto %q, want gpu-node (only node with a GPU)", got[0].GetSpec().GetNodeName())
	}
}

func TestScheduleUnassignedRespectsAcceleratorCapacityLimit(t *testing.T) {
	ctx := context.Background()
	r, deployments, pods, nodes := newTestReconciler()

	gpuNode := &v1.Node{
		Metadata: &v1.ObjectMeta{Name: "gpu-node"},
		Status: &v1.NodeStatus{
			Ready: true,
			Allocatable: &v1.ResourceList{
				CpuMillis:    4000,
				MemoryBytes:  4 << 30,
				Accelerators: map[string]int64{"nvidia.com/gpu": 1},
			},
		},
	}
	if err := nodes.Put(ctx, "", "gpu-node", gpuNode); err != nil {
		t.Fatalf("seed gpu node: %v", err)
	}

	existing := &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: "already-using-gpu", Namespace: "default"},
		Spec: &v1.PodSpec{
			NodeName: "gpu-node",
			Containers: []*v1.Container{{
				Name:      "app",
				Resources: &v1.ResourceRequirements{Requests: &v1.ResourceList{Accelerators: map[string]int64{"nvidia.com/gpu": 1}}},
			}},
		},
	}
	if err := pods.Put(ctx, "default", "already-using-gpu", existing); err != nil {
		t.Fatalf("seed existing pod: %v", err)
	}

	d := &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "training", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas: 1,
			Selector: map[string]string{"app": "training"},
			Template: &v1.PodSpec{Containers: []*v1.Container{{
				Name:      "trainer",
				Resources: &v1.ResourceRequirements{Requests: &v1.ResourceList{Accelerators: map[string]int64{"nvidia.com/gpu": 1}}},
			}}},
		},
	}
	if err := deployments.Put(ctx, "default", "training", d); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}

	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne: %v", err)
	}

	all, err := pods.List(ctx, "default")
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	var newPod *v1.Pod
	for _, p := range all {
		if p.GetMetadata().GetName() != "already-using-gpu" {
			newPod = p
		}
	}
	if newPod == nil {
		t.Fatal("new pod not found")
	}
	if newPod.GetSpec().GetNodeName() != "" {
		t.Errorf("pod scheduled onto %q despite the node's single GPU being taken, want it to stay Pending", newPod.GetSpec().GetNodeName())
	}
}
