// Command nimbus-agent runs the per-node agent (kubelet equivalent).
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"

	"github.com/gh0st-nemesis/nimbuscore/internal/agent"
)

// noopRuntime is a placeholder Runtime until the containerd/CRI client
// lands.
type noopRuntime struct{}

func (noopRuntime) RunPod(_ context.Context, _ string) error  { return nil }
func (noopRuntime) StopPod(_ context.Context, _ string) error { return nil }

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	nodeName, _ := os.Hostname()
	a := agent.New(agent.Config{
		NodeName:  nodeName,
		APIServer: "localhost:7443",
	}, noopRuntime{})

	if err := a.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("agent: %v", err)
	}
}
