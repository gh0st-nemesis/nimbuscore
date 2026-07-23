package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "0.1.0-dev"

func main() {
	root := &cobra.Command{
		Use:   "nimbusctl",
		Short: "nimbusctl controls a NimbusCore cluster",
	}

	root.AddCommand(newVersionCmd())
	root.AddCommand(newGetCmd())
	root.AddCommand(newApplyCmd())
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

func newGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <resource>",
		Short: "Display one or many resources",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("get: not yet implemented — API server client lands with the resource schema (step B)")
		},
	}
}

func newApplyCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply a manifest to the cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("apply -f %s: not yet implemented — API server client lands with the resource schema (step B)", file)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "path to the manifest file")
	return cmd
}
