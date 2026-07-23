package controller

import (
	"context"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/scheduler"
)

func buildNodeCandidates(ctx context.Context, nodes *registry.Registry[*v1.Node], usage map[string]nodeResourceUsage) ([]scheduler.NodeCandidate, error) {
	all, err := nodes.List(ctx, "")
	if err != nil {
		return nil, err
	}

	candidates := make([]scheduler.NodeCandidate, 0, len(all))
	for _, n := range all {
		if !n.GetStatus().GetReady() || n.GetSpec().GetUnschedulable() {
			continue
		}
		name := n.GetMetadata().GetName()
		candidates = append(candidates, scheduler.NodeCandidate{
			Name:                name,
			CPUCapacity:         n.GetStatus().GetAllocatable().GetCpuMillis(),
			MemCapacity:         n.GetStatus().GetAllocatable().GetMemoryBytes(),
			CPUUsed:             usage[name].cpuMillis,
			MemUsed:             usage[name].memoryBytes,
			AcceleratorCapacity: n.GetStatus().GetAllocatable().GetAccelerators(),
			AcceleratorUsed:     usage[name].accelerators,
		})
	}
	return candidates, nil
}

func podResourceRequest(p *v1.Pod) (cpuMillis, memoryBytes int64, accelerators map[string]int64) {
	accelerators = make(map[string]int64)
	for _, c := range p.GetSpec().GetContainers() {
		req := c.GetResources().GetRequests()
		cpuMillis += req.GetCpuMillis()
		memoryBytes += req.GetMemoryBytes()
		for name, count := range req.GetAccelerators() {
			accelerators[name] += count
		}
	}
	return cpuMillis, memoryBytes, accelerators
}
