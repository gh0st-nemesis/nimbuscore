package scheduler

import (
	"context"
	"fmt"
	"testing"
)

func BenchmarkScheduleAcross100Nodes(b *testing.B) {
	s := New()
	ctx := context.Background()

	nodes := make([]NodeCandidate, 100)
	for i := range nodes {
		nodes[i] = NodeCandidate{
			Name:        fmt.Sprintf("node-%d", i),
			CPUCapacity: 4000,
			MemCapacity: 8 << 30,
			CPUUsed:     int64(i * 10),
			MemUsed:     int64(i) << 20,
		}
	}
	pod := PodRequest{Name: "web", CPURequest: 500, MemRequest: 256 << 20}

	b.ResetTimer()
	for b.Loop() {
		if _, err := s.Schedule(ctx, pod, nodes); err != nil {
			b.Fatalf("Schedule: %v", err)
		}
	}
}
