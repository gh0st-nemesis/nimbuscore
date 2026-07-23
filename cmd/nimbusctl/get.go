package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

func newGetCmd() *cobra.Command {
	var namespace string
	cmd := &cobra.Command{
		Use:   "get <pods|nodes|deployments>",
		Short: "Display one or many resources",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			conn, err := dial(ctx, clientConfig{controlPlaneAddr: controlPlaneAddr, joinToken: joinToken, clientName: clientName})
			if err != nil {
				return err
			}
			defer conn.Close()

			switch args[0] {
			case "pods", "pod":
				return getPods(ctx, conn, namespace)
			case "nodes", "node":
				return getNodes(ctx, conn)
			case "deployments", "deployment", "deploy":
				return getDeployments(ctx, conn, namespace)
			default:
				return fmt.Errorf("unknown resource %q (supported: pods, nodes, deployments)", args[0])
			}
		},
	}
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "namespace to list resources from")
	return cmd
}

func getPods(ctx context.Context, conn *grpc.ClientConn, namespace string) error {
	resp, err := v1.NewPodServiceClient(conn).ListPods(ctx, &v1.ListPodsRequest{Namespace: namespace})
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tNAMESPACE\tPHASE\tNODE\tRESTARTS")
	for _, p := range resp.GetItems() {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\n",
			p.GetMetadata().GetName(), p.GetMetadata().GetNamespace(), p.GetStatus().GetPhase(),
			p.GetSpec().GetNodeName(), p.GetStatus().GetRestartCount())
	}
	return w.Flush()
}

func getNodes(ctx context.Context, conn *grpc.ClientConn) error {
	resp, err := v1.NewNodeServiceClient(conn).ListNodes(ctx, &v1.ListNodesRequest{})
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tREADY\tCPU-ALLOCATABLE\tMEMORY-ALLOCATABLE")
	for _, n := range resp.GetItems() {
		fmt.Fprintf(w, "%s\t%v\t%dm\t%d\n",
			n.GetMetadata().GetName(), n.GetStatus().GetReady(),
			n.GetStatus().GetAllocatable().GetCpuMillis(), n.GetStatus().GetAllocatable().GetMemoryBytes())
	}
	return w.Flush()
}

func getDeployments(ctx context.Context, conn *grpc.ClientConn, namespace string) error {
	resp, err := v1.NewDeploymentServiceClient(conn).ListDeployments(ctx, &v1.ListDeploymentsRequest{Namespace: namespace})
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tNAMESPACE\tREPLICAS\tREADY")
	for _, d := range resp.GetItems() {
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\n",
			d.GetMetadata().GetName(), d.GetMetadata().GetNamespace(),
			d.GetSpec().GetReplicas(), d.GetStatus().GetReadyReplicas())
	}
	return w.Flush()
}
