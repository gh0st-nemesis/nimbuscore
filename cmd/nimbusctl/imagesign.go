package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/gh0st-nemesis/nimbuscore/internal/imagesign"
)

func newGenerateKeyCmd() *cobra.Command {
	var outDir string
	cmd := &cobra.Command{
		Use:   "generate-key",
		Short: "Generate an image-signing keypair",
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := imagesign.GenerateKey()
			if err != nil {
				return err
			}

			privPEM, err := imagesign.MarshalPrivateKey(key)
			if err != nil {
				return err
			}
			pubPEM, err := imagesign.MarshalPublicKey(&key.PublicKey)
			if err != nil {
				return err
			}

			if err := os.MkdirAll(outDir, 0o755); err != nil {
				return err
			}
			privPath := filepath.Join(outDir, "image-signing-key.pem")
			pubPath := filepath.Join(outDir, "image-signing-key.pub.pem")

			if err := os.WriteFile(privPath, privPEM, 0o600); err != nil {
				return err
			}
			if err := os.WriteFile(pubPath, pubPEM, 0o644); err != nil {
				return err
			}

			fmt.Printf("private key: %s\npublic key:  %s\n", privPath, pubPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&outDir, "out-dir", ".", "directory to write the keypair into")
	return cmd
}

func newSignImageCmd() *cobra.Command {
	var keyFile, trustFile string
	cmd := &cobra.Command{
		Use:   "sign-image <image-ref>",
		Short: "Sign an image reference and add it to a trust file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			imageRef := args[0]

			keyPEM, err := os.ReadFile(keyFile)
			if err != nil {
				return fmt.Errorf("read key file: %w", err)
			}
			key, err := imagesign.ParsePrivateKey(keyPEM)
			if err != nil {
				return fmt.Errorf("parse key file: %w", err)
			}

			sig, err := imagesign.Sign(key, imageRef)
			if err != nil {
				return fmt.Errorf("sign image: %w", err)
			}

			tf, err := imagesign.LoadTrustFile(trustFile)
			if err != nil {
				return fmt.Errorf("load trust file: %w", err)
			}
			tf.Add(imageRef, sig)
			if err := tf.Save(trustFile); err != nil {
				return fmt.Errorf("save trust file: %w", err)
			}

			fmt.Printf("signed %q, updated %s\n", imageRef, trustFile)
			return nil
		},
	}
	cmd.Flags().StringVar(&keyFile, "key", "", "path to the image-signing private key (required)")
	cmd.Flags().StringVar(&trustFile, "trust-file", "trust.json", "path to the trust file to update")
	cmd.MarkFlagRequired("key")
	return cmd
}
