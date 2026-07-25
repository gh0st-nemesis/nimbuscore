package controller

import (
	"context"
	"log"
	"time"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/scheduler"
)

type PodReconciler struct {
	pods      *registry.Registry[*v1.Pod]
	nodes     *registry.Registry[*v1.Node]
	scheduler scheduler.Scheduler
	resync    time.Duration
}

func NewPodReconciler(pods *registry.Registry[*v1.Pod], nodes *registry.Registry[*v1.Node], sched scheduler.Scheduler, resync time.Duration) *PodReconciler {
	if resync <= 0 {
		resync = 5 * time.Second
	}
	return &PodReconciler{pods: pods, nodes: nodes, scheduler: sched, resync: resync}
}

func (r *PodReconciler) Name() string { return "pod-controller" }

func (r *PodReconciler) Reconcile(ctx context.Context) error {
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

func (r *PodReconciler) reconcileAll(ctx context.Context) {
	all, err := r.pods.List(ctx, "")
	if err != nil {
		log.Printf("pod-controller: list pods: %v", err)
		return
	}

	var unassigned []*v1.Pod
	for _, p := range all {
		if p.GetMetadata().GetLabels()[OwnerDeploymentLabel] != "" {
			continue
		}
		if p.GetSpec().GetNodeName() != "" {
			continue
		}
		switch p.GetStatus().GetPhase() {
		case v1.PodPhase_POD_PHASE_SUCCEEDED, v1.PodPhase_POD_PHASE_FAILED:
			continue
		}
		unassigned = append(unassigned, p)
	}
	if len(unassigned) == 0 {
		return
	}

	usage, err := computeNodeUsage(ctx, r.pods)
	if err != nil {
		log.Printf("pod-controller: compute node usage: %v", err)
		return
	}
	candidates, err := buildNodeCandidates(ctx, r.nodes, usage)
	if err != nil {
		log.Printf("pod-controller: list nodes: %v", err)
		return
	}

	for _, p := range unassigned {
		if err := schedulePod(ctx, r.pods, r.scheduler, candidates, p); err != nil {
			log.Printf("pod-controller: %v", err)
		}
	}
}
