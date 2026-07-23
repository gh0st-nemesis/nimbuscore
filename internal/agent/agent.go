// Package agent implements the per-node component (kubelet equivalent,
// design doc section 04): it watches assigned pods and drives the local
// container runtime.
package agent

import (
	"context"
	"log"
)

// Runtime abstracts the container runtime the agent drives. Phase 1
// wires this to containerd via CRI; this interface lets the agent
// compile and be tested before that client exists.
type Runtime interface {
	RunPod(ctx context.Context, podName string) error
	StopPod(ctx context.Context, podName string) error
}

// Config holds agent startup parameters.
type Config struct {
	NodeName  string
	APIServer string
}

// Agent is the per-node control loop.
type Agent struct {
	cfg     Config
	runtime Runtime
}

// New returns an Agent bound to the given Runtime.
func New(cfg Config, runtime Runtime) *Agent {
	return &Agent{cfg: cfg, runtime: runtime}
}

// Run blocks until ctx is cancelled. Phase 1 only establishes the
// process lifecycle; watching the API server for pod assignments lands
// alongside the gRPC client work.
func (a *Agent) Run(ctx context.Context) error {
	log.Printf("agent: node %q connecting to apiserver %q", a.cfg.NodeName, a.cfg.APIServer)
	<-ctx.Done()
	return ctx.Err()
}
