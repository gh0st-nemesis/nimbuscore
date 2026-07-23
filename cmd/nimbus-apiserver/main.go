// Command nimbus-apiserver runs the control-plane process: the gRPC API
// server, the (in-memory, Phase 1) store, and the controller manager's
// reconciliation loops, all in a single process for now.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/apiserver"
	"github.com/gh0st-nemesis/nimbuscore/internal/controller"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	st := store.NewMemStore()

	srv := apiserver.New(apiserver.Config{Addr: ":7443"})
	v1.RegisterPodServiceServer(srv.GRPCServer(), apiserver.NewPodService(st))
	v1.RegisterNodeServiceServer(srv.GRPCServer(), apiserver.NewNodeService(st))
	v1.RegisterDeploymentServiceServer(srv.GRPCServer(), apiserver.NewDeploymentService(st))

	deployments := registry.New(st, "deployments", func() *v1.Deployment { return &v1.Deployment{} })
	pods := registry.New(st, "pods", func() *v1.Pod { return &v1.Pod{} })

	mgr := controller.NewManager()
	mgr.Register(controller.NewDeploymentReconciler(deployments, pods, 0))
	go mgr.Run(ctx)

	if err := srv.Serve(ctx); err != nil {
		log.Fatalf("apiserver: %v", err)
	}
}
