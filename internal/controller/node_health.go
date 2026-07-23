package controller

import (
	"context"
	"log"
	"time"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
)

// NodeHealthReconciler marks Nodes not-ready once their heartbeat goes
// stale (design doc section 08, phase 2: "health checks et détection de
// nœud mort"). It does not evict Pods itself — DeploymentReconciler
// checks node readiness when counting owned Pods and replaces the ones
// sitting on a dead node, keeping eviction logic in one place.
type NodeHealthReconciler struct {
	nodes   *registry.Registry[*v1.Node]
	timeout time.Duration
	resync  time.Duration
}

// NewNodeHealthReconciler returns a reconciler that marks a Node
// not-ready once timeout elapses since its last heartbeat, checking
// every resync interval. timeout <= 0 defaults to 15s, resync <= 0 to
// 5s.
func NewNodeHealthReconciler(nodes *registry.Registry[*v1.Node], timeout, resync time.Duration) *NodeHealthReconciler {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if resync <= 0 {
		resync = 5 * time.Second
	}
	return &NodeHealthReconciler{nodes: nodes, timeout: timeout, resync: resync}
}

func (r *NodeHealthReconciler) Name() string { return "node-health-controller" }

func (r *NodeHealthReconciler) Reconcile(ctx context.Context) error {
	ticker := time.NewTicker(r.resync)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			r.checkAll(ctx)
		}
	}
}

func (r *NodeHealthReconciler) checkAll(ctx context.Context) {
	nodes, err := r.nodes.List(ctx, "")
	if err != nil {
		log.Printf("node-health-controller: list nodes: %v", err)
		return
	}

	cutoff := time.Now().Add(-r.timeout).Unix()
	for _, n := range nodes {
		if !n.GetStatus().GetReady() {
			continue
		}
		if n.GetStatus().GetLastHeartbeatUnix() >= cutoff {
			continue
		}

		n.Status.Ready = false
		if err := r.nodes.Put(ctx, "", n.GetMetadata().GetName(), n); err != nil {
			log.Printf("node-health-controller: mark %s not-ready: %v", n.GetMetadata().GetName(), err)
			continue
		}
		log.Printf("node-health-controller: %s marked not-ready (no heartbeat in over %s)", n.GetMetadata().GetName(), r.timeout)
	}
}
