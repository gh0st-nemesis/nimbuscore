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

	cfg := loadConfig()

	root.PersistentFlags().StringVar(&controlPlaneAddr, "control-plane-addr",
		firstNonEmpty(os.Getenv("NIMBUS_API_ADDR"), cfg.ControlPlaneAddr),
		"gRPC API address of the control plane (env NIMBUS_API_ADDR, or ~/.nimbus/config.json — see nimbusctl config set-context)")
	root.PersistentFlags().StringVar(&joinToken, "join-token",
		firstNonEmpty(os.Getenv("NIMBUS_JOIN_TOKEN"), cfg.JoinToken),
		"shared bootstrap token used to enroll a short-lived client identity (env NIMBUS_JOIN_TOKEN, or ~/.nimbus/config.json)")
	root.PersistentFlags().StringVar(&clientName, "client-name",
		firstNonEmpty(cfg.ClientName, defaultClientName()),
		"identity name to enroll as (SVID role CLIENT)")

	root.AddCommand(newVersionCmd())
	root.AddCommand(newGetCmd())
	root.AddCommand(newApplyCmd())
	root.AddCommand(newRunCmd())
	root.AddCommand(newConfigCmd())
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
