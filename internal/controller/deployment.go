package controller

import (
	"context"
	"fmt"
	"log"
	"maps"
	"time"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
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
// Store gains a watch/notify API (Phase 2+), this can move to an
// edge-triggered work queue without changing the reconcile logic itself.
type DeploymentReconciler struct {
	deployments *registry.Registry[*v1.Deployment]
	pods        *registry.Registry[*v1.Pod]
	resync      time.Duration
}

// NewDeploymentReconciler wires a DeploymentReconciler to the Deployment
// and Pod registries it reconciles against. resync <= 0 defaults to 5s.
func NewDeploymentReconciler(deployments *registry.Registry[*v1.Deployment], pods *registry.Registry[*v1.Pod], resync time.Duration) *DeploymentReconciler {
	if resync <= 0 {
		resync = 5 * time.Second
	}
	return &DeploymentReconciler{deployments: deployments, pods: pods, resync: resync}
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
// spec.replicas, then writes back observed status — the same
// read-diff-act-report cycle every controller in the design follows.
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

func (r *DeploymentReconciler) ownedPods(ctx context.Context, d *v1.Deployment) ([]*v1.Pod, error) {
	all, err := r.pods.List(ctx, d.GetMetadata().GetNamespace())
	if err != nil {
		return nil, err
	}

	owned := all[:0]
	for _, p := range all {
		if p.GetMetadata().GetLabels()[ownerLabel] == d.GetMetadata().GetName() {
			owned = append(owned, p)
		}
	}
	return owned, nil
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
