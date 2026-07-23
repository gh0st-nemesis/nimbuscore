package agent

import (
	"context"
	"log"
)

type Runtime interface {
	RunPod(ctx context.Context, podName string) error
	StopPod(ctx context.Context, podName string) error
}

type Config struct {
	NodeName  string
	APIServer string
}

type Agent struct {
	cfg     Config
	runtime Runtime
}

func New(cfg Config, runtime Runtime) *Agent {
	return &Agent{cfg: cfg, runtime: runtime}
}

func (a *Agent) Run(ctx context.Context) error {
	log.Printf("agent: node %q connecting to apiserver %q", a.cfg.NodeName, a.cfg.APIServer)
	<-ctx.Done()
	return ctx.Err()
}
