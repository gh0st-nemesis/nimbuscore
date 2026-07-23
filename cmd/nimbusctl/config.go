package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

type nimbusConfig struct {
	ControlPlaneAddr string `json:"control_plane_addr,omitempty"`
	JoinToken        string `json:"join_token,omitempty"`
	ClientName       string `json:"client_name,omitempty"`
}

func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".nimbus", "config.json"), nil
}

func loadConfig() nimbusConfig {
	path, err := configPath()
	if err != nil {
		return nimbusConfig{}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nimbusConfig{}
	}
	var cfg nimbusConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nimbusConfig{}
	}
	return cfg
}

func saveConfig(cfg nimbusConfig) (string, error) {
	path, err := configPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func newConfigCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "config",
		Short: "Manage nimbusctl's persistent client configuration (~/.nimbus/config.json)",
	}
	root.AddCommand(newConfigSetContextCmd())
	root.AddCommand(newConfigViewCmd())
	return root
}

func newConfigSetContextCmd() *cobra.Command {
	var addr, token, name string
	cmd := &cobra.Command{
		Use:   "set-context",
		Short: "Save the control plane address and/or join token so future commands don't need them",
		RunE: func(cmd *cobra.Command, args []string) error {
			if addr == "" && token == "" && name == "" {
				return fmt.Errorf("nothing to save — pass at least one of --control-plane-addr, --join-token, --client-name")
			}

			cfg := loadConfig()
			if addr != "" {
				cfg.ControlPlaneAddr = addr
			}
			if token != "" {
				cfg.JoinToken = token
			}
			if name != "" {
				cfg.ClientName = name
			}

			path, err := saveConfig(cfg)
			if err != nil {
				return err
			}
			fmt.Printf("saved %s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&addr, "control-plane-addr", "", "gRPC API address of the control plane")
	cmd.Flags().StringVar(&token, "join-token", "", "shared bootstrap token")
	cmd.Flags().StringVar(&name, "client-name", "", "identity name to enroll as (SVID role CLIENT)")
	return cmd
}

func newConfigViewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "view",
		Short: "Print the current persistent configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			cfg := loadConfig()
			fmt.Printf("config file:        %s\n", path)
			fmt.Printf("control-plane-addr: %s\n", cfg.ControlPlaneAddr)
			fmt.Printf("join-token:         %s\n", cfg.JoinToken)
			fmt.Printf("client-name:        %s\n", cfg.ClientName)
			return nil
		},
	}
}
