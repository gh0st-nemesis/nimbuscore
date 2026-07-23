package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

func newDeleteCmd() *cobra.Command {
	var namespace string
	cmd := &cobra.Command{
		Use:   "delete <pods|nodes|deployments> <name>",
		Short: "Delete a resource",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			conn, err := dial(ctx, clientConfig{controlPlaneAddr: controlPlaneAddr, joinToken: joinToken, clientName: clientName})
			if err != nil {
				return err
			}
			defer conn.Close()

			name := args[1]
			switch args[0] {
			case "pods", "pod":
				if _, err := v1.NewPodServiceClient(conn).DeletePod(ctx, &v1.DeletePodRequest{Namespace: namespace, Name: name}); err != nil {
					return err
				}
				fmt.Printf("pod/%s deleted\n", name)
			case "nodes", "node":
				if _, err := v1.NewNodeServiceClient(conn).DeleteNode(ctx, &v1.DeleteNodeRequest{Name: name}); err != nil {
					return err
				}
				fmt.Printf("node/%s deleted\n", name)
			case "deployments", "deployment", "deploy":
				if _, err := v1.NewDeploymentServiceClient(conn).DeleteDeployment(ctx, &v1.DeleteDeploymentRequest{Namespace: namespace, Name: name}); err != nil {
					return err
				}
				fmt.Printf("deployment/%s deleted\n", name)
			default:
				return fmt.Errorf("unknown resource %q (supported: pods, nodes, deployments)", args[0])
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "namespace of the resource to delete")
	return cmd
}
