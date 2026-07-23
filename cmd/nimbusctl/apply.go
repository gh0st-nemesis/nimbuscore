package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

func newApplyCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply a manifest to the cluster (create, or replace if it already exists)",
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("read %s: %w", file, err)
			}

			var envelope struct {
				Kind string `json:"kind"`
			}
			if err := json.Unmarshal(raw, &envelope); err != nil {
				return fmt.Errorf("parse %s: %w", file, err)
			}
			if envelope.Kind == "" {
				return fmt.Errorf(`%s: missing required "kind" field (Pod, Deployment, Node, Service)`, file)
			}

			ctx := context.Background()
			conn, err := dial(ctx, clientConfig{controlPlaneAddr: controlPlaneAddr, joinToken: joinToken, clientName: clientName})
			if err != nil {
				return err
			}
			defer conn.Close()

			unmarshal := protojson.UnmarshalOptions{DiscardUnknown: true}

			switch envelope.Kind {
			case "Pod":
				pod := &v1.Pod{}
				if err := unmarshal.Unmarshal(raw, pod); err != nil {
					return fmt.Errorf("parse Pod manifest: %w", err)
				}
				applied, err := v1.NewPodServiceClient(conn).CreatePod(ctx, &v1.CreatePodRequest{Pod: pod})
				if err != nil {
					return err
				}
				fmt.Printf("pod/%s applied\n", applied.GetMetadata().GetName())

			case "Deployment":
				d := &v1.Deployment{}
				if err := unmarshal.Unmarshal(raw, d); err != nil {
					return fmt.Errorf("parse Deployment manifest: %w", err)
				}
				applied, err := v1.NewDeploymentServiceClient(conn).CreateDeployment(ctx, &v1.CreateDeploymentRequest{Deployment: d})
				if err != nil {
					return err
				}
				fmt.Printf("deployment/%s applied\n", applied.GetMetadata().GetName())

			case "Node":
				n := &v1.Node{}
				if err := unmarshal.Unmarshal(raw, n); err != nil {
					return fmt.Errorf("parse Node manifest: %w", err)
				}
				applied, err := v1.NewNodeServiceClient(conn).CreateNode(ctx, &v1.CreateNodeRequest{Node: n})
				if err != nil {
					return err
				}
				fmt.Printf("node/%s applied\n", applied.GetMetadata().GetName())

			case "Service":
				s := &v1.Service{}
				if err := unmarshal.Unmarshal(raw, s); err != nil {
					return fmt.Errorf("parse Service manifest: %w", err)
				}
				applied, err := v1.NewServiceServiceClient(conn).CreateService(ctx, &v1.CreateServiceRequest{Service: s})
				if err != nil {
					return err
				}
				fmt.Printf("service/%s applied\n", applied.GetMetadata().GetName())

			default:
				return fmt.Errorf("unsupported kind %q (supported: Pod, Deployment, Node, Service)", envelope.Kind)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "path to the manifest file (JSON)")
	cmd.MarkFlagRequired("file")
	return cmd
}
