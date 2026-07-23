package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

func newRunCmd() *cobra.Command {
	var (
		image     string
		namespace string
		replicas  int32
	)
	cmd := &cobra.Command{
		Use:   "run <name> --image=<image> [-- command args...]",
		Short: "Create a Pod (or a Deployment with --replicas > 1) running a container image",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dash := cmd.ArgsLenAtDash()
			nameArgs := args
			var command []string
			if dash >= 0 {
				nameArgs = args[:dash]
				command = args[dash:]
			}
			if len(nameArgs) != 1 {
				return fmt.Errorf("expected exactly one resource name, got %d (did you forget -- before the container command?)", len(nameArgs))
			}
			name := nameArgs[0]
			if len(command) == 0 {
				fmt.Fprintln(os.Stderr, "warning: no command given (-- <cmd> <args>...) — the agent runs containers as real OS processes (no CRI/OCI runtime yet), so without a command there is nothing for it to execute; see README known limitations")
			}

			container := &v1.Container{Name: name, Image: image, Command: command}

			ctx := context.Background()
			conn, err := dial(ctx, clientConfig{controlPlaneAddr: controlPlaneAddr, joinToken: joinToken, clientName: clientName})
			if err != nil {
				return err
			}
			defer conn.Close()

			if replicas <= 1 {
				pod := &v1.Pod{
					Metadata: &v1.ObjectMeta{Name: name, Namespace: namespace},
					Spec:     &v1.PodSpec{Containers: []*v1.Container{container}},
				}
				applied, err := v1.NewPodServiceClient(conn).CreatePod(ctx, &v1.CreatePodRequest{Pod: pod})
				if err != nil {
					return err
				}
				fmt.Printf("pod/%s created\n", applied.GetMetadata().GetName())
				return nil
			}

			d := &v1.Deployment{
				Metadata: &v1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: &v1.DeploymentSpec{
					Replicas: replicas,
					Selector: map[string]string{"app": name},
					Template: &v1.PodSpec{Containers: []*v1.Container{container}},
				},
			}
			applied, err := v1.NewDeploymentServiceClient(conn).CreateDeployment(ctx, &v1.CreateDeploymentRequest{Deployment: d})
			if err != nil {
				return err
			}
			fmt.Printf("deployment/%s created\n", applied.GetMetadata().GetName())
			return nil
		},
	}
	cmd.Flags().StringVar(&image, "image", "", "container image reference (required)")
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "namespace to create the resource in")
	cmd.Flags().Int32Var(&replicas, "replicas", 1, "number of replicas — creates a Deployment instead of a single Pod when > 1")
	cmd.MarkFlagRequired("image")
	return cmd
}
