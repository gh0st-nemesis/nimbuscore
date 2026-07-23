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
	resync      time.Duration
}

func NewHorizontalAutoscaler(deployments *registry.Registry[*v1.Deployment], pods *registry.Registry[*v1.Pod], resync time.Duration) *HorizontalAutoscaler {
	if resync <= 0 {
		resync = 15 * time.Second
	}
	return &HorizontalAutoscaler{deployments: deployments, pods: pods, resync: resync}
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
