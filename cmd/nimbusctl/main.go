package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "0.1.0-dev"

var (
	controlPlaneAddr string
	joinToken        string
	clientName       string
)

func main() {
	root := &cobra.Command{
		Use:   "nimbusctl",
		Short: "nimbusctl controls a NimbusCore cluster",
	}

	root.PersistentFlags().StringVar(&controlPlaneAddr, "control-plane-addr", os.Getenv("NIMBUS_API_ADDR"), "gRPC API address of the control plane (env NIMBUS_API_ADDR)")
	root.PersistentFlags().StringVar(&joinToken, "join-token", os.Getenv("NIMBUS_JOIN_TOKEN"), "shared bootstrap token used to enroll a short-lived client identity (env NIMBUS_JOIN_TOKEN)")
	root.PersistentFlags().StringVar(&clientName, "client-name", defaultClientName(), "identity name to enroll as (SVID role CLIENT)")

	root.AddCommand(newVersionCmd())
	root.AddCommand(newGetCmd())
	root.AddCommand(newApplyCmd())
	root.AddCommand(newRunCmd())
	root.AddCommand(newGenerateKeyCmd())
	root.AddCommand(newSignImageCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the nimbusctl version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println(version)
			return nil
		},
	}
}

func defaultClientName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "client"
	}
	return "nimbusctl-" + host
}
