package controller

import (
	"context"
	"fmt"
	"log"
	"math"
	"time"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
)

type HorizontalAutoscaler struct {
	deployments *registry.Registry[*v1.Deployment]
	pods        *registry.Registry[*v1.Pod]
	nodes       *registry.Registry[*v1.Node]
	resync      time.Duration
}

func NewHorizontalAutoscaler(deployments *registry.Registry[*v1.Deployment], pods *registry.Registry[*v1.Pod], nodes *registry.Registry[*v1.Node], resync time.Duration) *HorizontalAutoscaler {
	if resync <= 0 {
		resync = 15 * time.Second
	}
	return &HorizontalAutoscaler{deployments: deployments, pods: pods, nodes: nodes, resync: resync}
}

func (r *HorizontalAutoscaler) Name() string { return "horizontal-autoscaler" }

func (r *HorizontalAutoscaler) Reconcile(ctx context.Context) error {
	ticker := time.NewTicker(r.resync)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			r.reconcileAll(ctx)
		}
	}
}

func (r *HorizontalAutoscaler) reconcileAll(ctx context.Context) {
	deployments, err := r.deployments.List(ctx, "")
	if err != nil {
		log.Printf("horizontal-autoscaler: list deployments: %v", err)
		return
	}
	for _, d := range deployments {
		if err := r.reconcileOne(ctx, d); err != nil {
			log.Printf("horizontal-autoscaler: %s/%s: %v", d.GetMetadata().GetNamespace(), d.GetMetadata().GetName(), err)
		}
	}
}

func (r *HorizontalAutoscaler) reconcileOne(ctx context.Context, d *v1.Deployment) error {
	maxReplicas := d.GetSpec().GetMaxReplicas()
	if maxReplicas <= 0 {
		return nil
	}
	minReplicas := d.GetSpec().GetMinReplicas()
	if minReplicas <= 0 {
		minReplicas = 1
	}

	containers := d.GetSpec().GetTemplate().GetContainers()
	if len(containers) == 0 {
		return nil
	}
	targetMemory := containers[0].GetResources().GetRequests().GetMemoryBytes()
	if targetMemory <= 0 {
		return nil
	}

	all, err := r.pods.List(ctx, d.GetMetadata().GetNamespace())
	if err != nil {
		return err
	}

	var totalUtilization float64
	var running int
	for _, p := range all {
		if p.GetMetadata().GetLabels()[ownerLabel] != d.GetMetadata().GetName() {
			continue
		}
		if p.GetStatus().GetPhase() != v1.PodPhase_POD_PHASE_RUNNING {
			continue
		}
		totalUtilization += float64(p.GetStatus().GetMemoryUsageBytes()) / float64(targetMemory)
		running++
	}
	if running == 0 {
		return nil
	}

	avgUtilization := totalUtilization / float64(running)
	currentReplicas := d.GetSpec().GetReplicas()
	if currentReplicas <= 0 {
		currentReplicas = int32(running)
	}

	desired := int32(math.Ceil(float64(currentReplicas) * avgUtilization))
	if desired < minReplicas {
		desired = minReplicas
	}
	if desired > maxReplicas {
		desired = maxReplicas
	}

	if desired > currentReplicas {
		capped, shortfall, err := r.capToClusterCapacity(ctx, d, currentReplicas, desired)
		if err != nil {
			return fmt.Errorf("check cluster capacity: %w", err)
		}
		if shortfall > 0 {
			log.Printf("horizontal-autoscaler: %s/%s: wanted %d replicas but cluster capacity only fits %d (shortfall %d)",
				d.GetMetadata().GetNamespace(), d.GetMetadata().GetName(), desired, capped, shortfall)
		}
		desired = capped
	}

	if desired == d.GetSpec().GetReplicas() {
		return nil
	}

	log.Printf("horizontal-autoscaler: %s/%s: scaling %d -> %d (avg memory utilization %.0f%%)",
		d.GetMetadata().GetNamespace(), d.GetMetadata().GetName(), d.GetSpec().GetReplicas(), desired, avgUtilization*100)

	current, err := r.deployments.Get(ctx, d.GetMetadata().GetNamespace(), d.GetMetadata().GetName())
	if err != nil {
		return fmt.Errorf("re-fetch deployment before scaling: %w", err)
	}
	current.Spec.Replicas = desired
	return r.deployments.Put(ctx, current.GetMetadata().GetNamespace(), current.GetMetadata().GetName(), current)
}

func (r *HorizontalAutoscaler) capToClusterCapacity(ctx context.Context, d *v1.Deployment, currentReplicas, desired int32) (capped int32, shortfall int32, err error) {
	usage, err := computeNodeUsage(ctx, r.pods)
	if err != nil {
		return desired, 0, err
	}
	capacity, err := computeClusterCapacity(ctx, r.nodes, usage)
	if err != nil {
		return desired, 0, err
	}

	var perReplicaCPU, perReplicaMem int64
	perReplicaAccel := make(map[string]int64)
	for _, c := range d.GetSpec().GetTemplate().GetContainers() {
		req := c.GetResources().GetRequests()
		perReplicaCPU += req.GetCpuMillis()
		perReplicaMem += req.GetMemoryBytes()
		for name, count := range req.GetAccelerators() {
			perReplicaAccel[name] += count
		}
	}

	maxAdditional := capacity.maxAdditionalReplicas(perReplicaCPU, perReplicaMem, perReplicaAccel)
	wantAdditional := desired - currentReplicas
	if wantAdditional <= maxAdditional {
		return desired, 0, nil
	}

	capped = currentReplicas + maxAdditional
	if capped < currentReplicas {
		capped = currentReplicas
	}
	return capped, desired - capped, nil
}
