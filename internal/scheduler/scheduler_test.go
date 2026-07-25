package scheduler

import (
	"context"
	"errors"
	"testing"
)

func TestScheduleAvoidsNodeWithConflictingPort(t *testing.T) {
	s := New()
	ctx := context.Background()

	nodes := []NodeCandidate{
		{Name: "busy", CPUCapacity: 4000, MemCapacity: 8 << 30, UsedPorts: map[int32]bool{80: true}},
		{Name: "free", CPUCapacity: 4000, MemCapacity: 8 << 30, UsedPorts: map[int32]bool{}},
	}
	pod := PodRequest{Name: "web", CPURequest: 500, MemRequest: 256 << 20, ContainerPorts: []int32{80}}

	got, err := s.Schedule(ctx, pod, nodes)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if got != "free" {
		t.Errorf("scheduled onto %q, want \"free\" (the only node without a port-80 conflict)", got)
	}
}

func TestScheduleFailsWhenAllNodesHaveConflictingPort(t *testing.T) {
	s := New()
	ctx := context.Background()

	nodes := []NodeCandidate{
		{Name: "node-a", CPUCapacity: 4000, MemCapacity: 8 << 30, UsedPorts: map[int32]bool{80: true}},
		{Name: "node-b", CPUCapacity: 4000, MemCapacity: 8 << 30, UsedPorts: map[int32]bool{80: true}},
	}
	pod := PodRequest{Name: "web", CPURequest: 500, MemRequest: 256 << 20, ContainerPorts: []int32{80}}

	_, err := s.Schedule(ctx, pod, nodes)
	if !errors.Is(err, ErrNoNodeAvailable) {
		t.Fatalf("Schedule error = %v, want ErrNoNodeAvailable", err)
	}
}

func TestScheduleIgnoresUnrelatedPorts(t *testing.T) {
	s := New()
	ctx := context.Background()

	nodes := []NodeCandidate{
		{Name: "only-node", CPUCapacity: 4000, MemCapacity: 8 << 30, UsedPorts: map[int32]bool{5432: true}},
	}
	pod := PodRequest{Name: "web", CPURequest: 500, MemRequest: 256 << 20, ContainerPorts: []int32{80}}

	got, err := s.Schedule(ctx, pod, nodes)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if got != "only-node" {
		t.Errorf("scheduled onto %q, want \"only-node\" (port 80 requested, only 5432 is taken)", got)
	}
}

func TestScheduleWithNoPortsRequestedIgnoresUsedPorts(t *testing.T) {
	s := New()
	ctx := context.Background()

	nodes := []NodeCandidate{
		{Name: "only-node", CPUCapacity: 4000, MemCapacity: 8 << 30, UsedPorts: map[int32]bool{80: true, 443: true}},
	}
	pod := PodRequest{Name: "worker", CPURequest: 500, MemRequest: 256 << 20}

	got, err := s.Schedule(ctx, pod, nodes)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if got != "only-node" {
		t.Errorf("scheduled onto %q, want \"only-node\" (pod requests no ports, so used ports elsewhere are irrelevant)", got)
	}
}
