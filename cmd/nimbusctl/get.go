package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

func newGetCmd() *cobra.Command {
	var namespace string
	var allNamespaces bool
	cmd := &cobra.Command{
		Use:   "get <pods|nodes|deployments|services>",
		Short: "Display one or many resources",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			conn, err := dial(ctx, clientConfig{controlPlaneAddr: controlPlaneAddr, joinToken: joinToken, clientName: clientName})
			if err != nil {
				return err
			}
			defer conn.Close()

			ns := namespace
			if allNamespaces {
				ns = ""
			}

			switch args[0] {
			case "pods", "pod":
				return getPods(ctx, conn, ns)
			case "nodes", "node":
				return getNodes(ctx, conn)
			case "deployments", "deployment", "deploy":
				return getDeployments(ctx, conn, ns)
			case "services", "service", "svc":
				return getServices(ctx, conn, ns)
			default:
				return fmt.Errorf("unknown resource %q (supported: pods, nodes, deployments, services)", args[0])
			}
		},
	}
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "namespace to list resources from")
	cmd.Flags().BoolVarP(&allNamespaces, "all-namespaces", "A", false, "list resources across all namespaces")
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
	fmt.Fprintln(w, "NAME\tREADY\tINTERNAL-IP\tCPU-ALLOCATABLE\tMEMORY-ALLOCATABLE")
	for _, n := range resp.GetItems() {
		fmt.Fprintf(w, "%s\t%v\t%s\t%dm\t%d\n",
			n.GetMetadata().GetName(), n.GetStatus().GetReady(), n.GetStatus().GetInternalIp(),
			n.GetStatus().GetAllocatable().GetCpuMillis(), n.GetStatus().GetAllocatable().GetMemoryBytes())
	}
	return w.Flush()
}

func getServices(ctx context.Context, conn *grpc.ClientConn, namespace string) error {
	resp, err := v1.NewServiceServiceClient(conn).ListServices(ctx, &v1.ListServicesRequest{Namespace: namespace})
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tNAMESPACE\tPORT\tENDPOINTS")
	for _, s := range resp.GetItems() {
		var endpoints []string
		for _, e := range s.GetStatus().GetEndpoints() {
			endpoints = append(endpoints, fmt.Sprintf("%s:%d", e.GetNodeIp(), e.GetNodePort()))
		}
		endpointsStr := strings.Join(endpoints, ",")
		if endpointsStr == "" {
			endpointsStr = "<none>"
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\n",
			s.GetMetadata().GetName(), s.GetMetadata().GetNamespace(), s.GetSpec().GetPort(), endpointsStr)
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
