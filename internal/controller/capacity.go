package controller

import (
	"context"
	"math"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
)

type clusterCapacity struct {
	cpuCapacity         int64
	cpuUsed             int64
	memCapacity         int64
	memUsed             int64
	acceleratorCapacity map[string]int64
	acceleratorUsed     map[string]int64
}

func computeClusterCapacity(ctx context.Context, nodes *registry.Registry[*v1.Node], usage map[string]nodeResourceUsage) (clusterCapacity, error) {
	all, err := nodes.List(ctx, "")
	if err != nil {
		return clusterCapacity{}, err
	}

	result := clusterCapacity{
		acceleratorCapacity: make(map[string]int64),
		acceleratorUsed:     make(map[string]int64),
	}
	for _, n := range all {
		if !n.GetStatus().GetReady() || n.GetSpec().GetUnschedulable() {
			continue
		}
		name := n.GetMetadata().GetName()
		alloc := n.GetStatus().GetAllocatable()
		result.cpuCapacity += alloc.GetCpuMillis()
		result.memCapacity += alloc.GetMemoryBytes()
		for accel, count := range alloc.GetAccelerators() {
			result.acceleratorCapacity[accel] += count
		}

		u := usage[name]
		result.cpuUsed += u.cpuMillis
		result.memUsed += u.memoryBytes
		for accel, count := range u.accelerators {
			result.acceleratorUsed[accel] += count
		}
	}
	return result, nil
}

func (c clusterCapacity) maxAdditionalReplicas(perReplicaCPU, perReplicaMem int64, perReplicaAccel map[string]int64) int32 {
	max := int32(math.MaxInt32)

	if fit := fitCount(c.cpuCapacity-c.cpuUsed, perReplicaCPU); fit < max {
		max = fit
	}
	if fit := fitCount(c.memCapacity-c.memUsed, perReplicaMem); fit < max {
		max = fit
	}
	for name, per := range perReplicaAccel {
		remaining := c.acceleratorCapacity[name] - c.acceleratorUsed[name]
		if fit := fitCount(remaining, per); fit < max {
			max = fit
		}
	}
	return max
}

func fitCount(remaining, perUnit int64) int32 {
	if perUnit <= 0 {
		return math.MaxInt32
	}
	if remaining <= 0 {
		return 0
	}
	fit := remaining / perUnit
	if fit > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(fit)
}
