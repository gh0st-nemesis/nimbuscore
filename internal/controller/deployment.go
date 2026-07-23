package controller

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"time"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/scheduler"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

// ownerLabel marks a Pod as owned by a Deployment, mirroring how
// Kubernetes uses OwnerReferences — a plain label is enough for Phase 1
// since we have no garbage-collector component yet.
const ownerLabel = "nimbuscore.io/owner-deployment"

// DeploymentReconciler is the concrete reconciliation loop referenced in
// the design doc (section 03: "boucles de réconciliation — état désiré
// vs. observé"). It is level-triggered: every tick it recomputes the
// full desired-vs-observed diff for every Deployment, rather than
// reacting to individual events. That's deliberate — level-triggered
// reconcilers self-heal from any missed or out-of-order update, at the
// cost of a fixed polling interval instead of instant reaction. Once the
// Store gains a watch/notify API, this can move to an edge-triggered
// work queue without changing the reconcile logic itself.
//
// Phase 2 adds two things on top of the Phase 1 scale up/down loop: it
// asks the Scheduler for a Node when a Pod has none yet, and it treats a
// Pod sitting on a not-ready Node as needing replacement — the
// self-healing half of "cluster réel multi-machines, tolérant à la
// panne d'un nœud" (design doc section 08, phase 2).
type DeploymentReconciler struct {
	deployments *registry.Registry[*v1.Deployment]
	pods        *registry.Registry[*v1.Pod]
	nodes       *registry.Registry[*v1.Node]
	scheduler   scheduler.Scheduler
	resync      time.Duration
}

// NewDeploymentReconciler wires a DeploymentReconciler to the registries
// and Scheduler it reconciles against. resync <= 0 defaults to 5s.
func NewDeploymentReconciler(
	deployments *registry.Registry[*v1.Deployment],
	pods *registry.Registry[*v1.Pod],
	nodes *registry.Registry[*v1.Node],
	sched scheduler.Scheduler,
	resync time.Duration,
) *DeploymentReconciler {
	if resync <= 0 {
		resync = 5 * time.Second
	}
	return &DeploymentReconciler{deployments: deployments, pods: pods, nodes: nodes, scheduler: sched, resync: resync}
}

func (r *DeploymentReconciler) Name() string { return "deployment-controller" }

// Reconcile blocks, running one full pass every resync interval until
// ctx is cancelled.
func (r *DeploymentReconciler) Reconcile(ctx context.Context) error {
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

func (r *DeploymentReconciler) reconcileAll(ctx context.Context) {
	deployments, err := r.deployments.List(ctx, "")
	if err != nil {
		log.Printf("deployment-controller: list deployments: %v", err)
		return
	}
	for _, d := range deployments {
		if err := r.reconcileOne(ctx, d); err != nil {
			log.Printf("deployment-controller: %s/%s: %v", d.GetMetadata().GetNamespace(), d.GetMetadata().GetName(), err)
		}
	}
}

// reconcileOne drives a single Deployment's owned Pods toward
// spec.replicas, schedules anything still unassigned, then writes back
// observed status — the same read-diff-act-report cycle every
// controller in the design follows.
func (r *DeploymentReconciler) reconcileOne(ctx context.Context, d *v1.Deployment) error {
	owned, err := r.ownedPods(ctx, d)
	if err != nil {
		return fmt.Errorf("list owned pods: %w", err)
	}

	want := int(d.GetSpec().GetReplicas())
	have := len(owned)

	switch {
	case have < want:
		for i := have; i < want; i++ {
			pod := r.newPod(d, i)
			if err := r.pods.Put(ctx, pod.GetMetadata().GetNamespace(), pod.GetMetadata().GetName(), pod); err != nil {
				return fmt.Errorf("create pod: %w", err)
			}
			log.Printf("deployment-controller: %s/%s: created %s", d.GetMetadata().GetNamespace(), d.GetMetadata().GetName(), pod.GetMetadata().GetName())
		}
	case have > want:
		for i := want; i < have; i++ {
			p := owned[i]
			if err := r.pods.Delete(ctx, p.GetMetadata().GetNamespace(), p.GetMetadata().GetName()); err != nil {
				return fmt.Errorf("delete pod: %w", err)
			}
			log.Printf("deployment-controller: %s/%s: deleted %s", d.GetMetadata().GetNamespace(), d.GetMetadata().GetName(), p.GetMetadata().GetName())
		}
	}

	owned, err = r.ownedPods(ctx, d) // re-read after create/delete to report accurate status
	if err != nil {
		return fmt.Errorf("re-list owned pods: %w", err)
	}

	if err := r.scheduleUnassigned(ctx, owned); err != nil {
		log.Printf("deployment-controller: %s/%s: schedule: %v", d.GetMetadata().GetNamespace(), d.GetMetadata().GetName(), err)
	}

	var ready int32
	for _, p := range owned {
		if p.GetStatus().GetPhase() == v1.PodPhase_POD_PHASE_RUNNING {
			ready++
		}
	}

	d.Status = &v1.DeploymentStatus{
		Replicas:      int32(len(owned)),
		ReadyReplicas: ready,
	}
	return r.deployments.Put(ctx, d.GetMetadata().GetNamespace(), d.GetMetadata().GetName(), d)
}

// ownedPods lists the Pods labeled as belonging to d, evicting (deleting)
// any that sit on a Node no longer ready. An evicted Pod isn't counted —
// the scale-up branch above recreates it, and scheduleUnassigned places
// the replacement on a healthy Node.
func (r *DeploymentReconciler) ownedPods(ctx context.Context, d *v1.Deployment) ([]*v1.Pod, error) {
	all, err := r.pods.List(ctx, d.GetMetadata().GetNamespace())
	if err != nil {
		return nil, err
	}

	owned := all[:0]
	for _, p := range all {
		if p.GetMetadata().GetLabels()[ownerLabel] != d.GetMetadata().GetName() {
			continue
		}

		if nodeName := p.GetSpec().GetNodeName(); nodeName != "" {
			ready, err := r.nodeReady(ctx, nodeName)
			if err != nil {
				return nil, err
			}
			if !ready {
				log.Printf("deployment-controller: %s/%s: evicting %s from dead node %s",
					d.GetMetadata().GetNamespace(), d.GetMetadata().GetName(), p.GetMetadata().GetName(), nodeName)
				if err := r.pods.Delete(ctx, p.GetMetadata().GetNamespace(), p.GetMetadata().GetName()); err != nil {
					return nil, err
				}
				continue
			}
		}

		owned = append(owned, p)
	}
	return owned, nil
}

func (r *DeploymentReconciler) nodeReady(ctx context.Context, name string) (bool, error) {
	node, err := r.nodes.Get(ctx, "", name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, nil // node deregistered entirely — treat like dead
		}
		return false, err
	}
	return node.GetStatus().GetReady(), nil
}

// scheduleUnassigned asks the Scheduler for a Node for every Pod that
// doesn't have one yet. A Pod that fails to schedule (no capacity, no
// ready Node) simply stays unassigned — Pending — and is retried on the
// next tick.
func (r *DeploymentReconciler) scheduleUnassigned(ctx context.Context, pods []*v1.Pod) error {
	var unassigned []*v1.Pod
	for _, p := range pods {
		if p.GetSpec().GetNodeName() == "" {
			unassigned = append(unassigned, p)
		}
	}
	if len(unassigned) == 0 {
		return nil
	}

	nodes, err := r.nodes.List(ctx, "")
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}

	candidates := make([]scheduler.NodeCandidate, 0, len(nodes))
	for _, n := range nodes {
		if !n.GetStatus().GetReady() || n.GetSpec().GetUnschedulable() {
			continue
		}
		candidates = append(candidates, scheduler.NodeCandidate{
			Name:        n.GetMetadata().GetName(),
			CPUCapacity: n.GetStatus().GetAllocatable().GetCpuMillis(),
			MemCapacity: n.GetStatus().GetAllocatable().GetMemoryBytes(),
		})
	}

	for _, p := range unassigned {
		var cpuReq, memReq int64
		for _, c := range p.GetSpec().GetContainers() {
			cpuReq += c.GetResources().GetRequests().GetCpuMillis()
			memReq += c.GetResources().GetRequests().GetMemoryBytes()
		}

		nodeName, err := r.scheduler.Schedule(ctx, scheduler.PodRequest{
			Name:       p.GetMetadata().GetName(),
			CPURequest: cpuReq,
			MemRequest: memReq,
		}, candidates)
		if err != nil {
			continue // no fit yet — stays Pending, retried next tick
		}

		p.Spec.NodeName = nodeName
		if err := r.pods.Put(ctx, p.GetMetadata().GetNamespace(), p.GetMetadata().GetName(), p); err != nil {
			return fmt.Errorf("assign pod %s to node %s: %w", p.GetMetadata().GetName(), nodeName, err)
		}
		log.Printf("deployment-controller: scheduled %s/%s onto %s", p.GetMetadata().GetNamespace(), p.GetMetadata().GetName(), nodeName)
	}
	return nil
}

func (r *DeploymentReconciler) newPod(d *v1.Deployment, index int) *v1.Pod {
	labels := make(map[string]string, len(d.GetSpec().GetSelector())+1)
	maps.Copy(labels, d.GetSpec().GetSelector())
	labels[ownerLabel] = d.GetMetadata().GetName()

	return &v1.Pod{
		Metadata: &v1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%d", d.GetMetadata().GetName(), index),
			Namespace: d.GetMetadata().GetNamespace(),
			Labels:    labels,
		},
		Spec: d.GetSpec().GetTemplate(),
		Status: &v1.PodStatus{
			Phase: v1.PodPhase_POD_PHASE_PENDING,
		},
	}
}
