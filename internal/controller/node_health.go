package controller

import (
	"context"
	"log"
	"time"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
)

type NodeHealthReconciler struct {
	nodes   *registry.Registry[*v1.Node]
	timeout time.Duration
	resync  time.Duration
}

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
