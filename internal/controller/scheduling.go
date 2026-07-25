package controller

import (
	"context"
	"fmt"
	"log"

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
			UsedPorts:           usage[name].ports,
		})
	}
	return candidates, nil
}

func podResourceRequest(p *v1.Pod) (cpuMillis, memoryBytes int64, accelerators map[string]int64, ports []int32) {
	accelerators = make(map[string]int64)
	for _, c := range p.GetSpec().GetContainers() {
		req := c.GetResources().GetRequests()
		cpuMillis += req.GetCpuMillis()
		memoryBytes += req.GetMemoryBytes()
		for name, count := range req.GetAccelerators() {
			accelerators[name] += count
		}
		ports = append(ports, c.GetContainerPorts()...)
	}
	return cpuMillis, memoryBytes, accelerators, ports
}

// schedulePod schedules a single unassigned pod against the given candidates,
// shared by both DeploymentReconciler and PodReconciler so port-aware
// placement and the pending-reason write-back only need to live in one
// place. On success it assigns NodeName and clears any previously recorded
// pending message; on failure it records why in Status.Message (only writing
// when the message actually changed, to avoid a raft write every tick for a
// pod that's been pending for a while).
func schedulePod(ctx context.Context, pods *registry.Registry[*v1.Pod], sched scheduler.Scheduler, candidates []scheduler.NodeCandidate, p *v1.Pod) error {
	cpuReq, memReq, accelReq, ports := podResourceRequest(p)

	nodeName, err := sched.Schedule(ctx, scheduler.PodRequest{
		Name:                  p.GetMetadata().GetName(),
		CPURequest:            cpuReq,
		MemRequest:            memReq,
		AcceleratorsRequested: accelReq,
		ContainerPorts:        ports,
	}, candidates)
	if err != nil {
		msg := "waiting for a node with enough free CPU/memory"
		if len(ports) > 0 {
			msg = fmt.Sprintf("waiting for a node with enough free CPU/memory and an available port for containerPort(s) %v", ports)
		}
		if p.GetStatus().GetMessage() == msg {
			return nil
		}
		if p.Status == nil {
			p.Status = &v1.PodStatus{}
		}
		p.Status.Message = msg
		if putErr := pods.Put(ctx, p.GetMetadata().GetNamespace(), p.GetMetadata().GetName(), p); putErr != nil {
			return fmt.Errorf("record pending reason for pod %s: %w", p.GetMetadata().GetName(), putErr)
		}
		return nil
	}

	if p.Spec == nil {
		p.Spec = &v1.PodSpec{}
	}
	p.Spec.NodeName = nodeName
	if p.GetStatus().GetMessage() != "" {
		if p.Status == nil {
			p.Status = &v1.PodStatus{}
		}
		p.Status.Message = ""
	}
	if err := pods.Put(ctx, p.GetMetadata().GetNamespace(), p.GetMetadata().GetName(), p); err != nil {
		return fmt.Errorf("assign pod %s to node %s: %w", p.GetMetadata().GetName(), nodeName, err)
	}
	log.Printf("controller: scheduled %s/%s onto %s", p.GetMetadata().GetNamespace(), p.GetMetadata().GetName(), nodeName)
	return nil
}
