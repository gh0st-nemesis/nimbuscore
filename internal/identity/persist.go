package identity

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
)

type persistedBootstrap struct {
	TrustDomain string `json:"trust_domain"`
	CAKeyPEM    string `json:"ca_key_pem"`
	CACertPEM   string `json:"ca_cert_pem"`
	DEK         []byte `json:"dek"`
}

func BootstrapStatePath(dataDir string) string {
	return filepath.Join(dataDir, "bootstrap-identity.json")
}

func SaveBootstrapState(path string, ca *CA, dek []byte) error {
	keyDER, err := x509.MarshalECPrivateKey(ca.key)
	if err != nil {
		return fmt.Errorf("identity: marshal CA key: %w", err)
	}

	state := persistedBootstrap{
		TrustDomain: ca.trustDomain.String(),
		CAKeyPEM:    string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})),
		CACertPEM:   string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw})),
		DEK:         dek,
	}

	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("identity: marshal bootstrap state: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("identity: write bootstrap state: %w", err)
	}
	return nil
}

func LoadBootstrapState(path string) (*CA, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}

	var state persistedBootstrap
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, nil, fmt.Errorf("identity: parse bootstrap state: %w", err)
	}

	td, err := spiffeid.TrustDomainFromString(state.TrustDomain)
	if err != nil {
		return nil, nil, fmt.Errorf("identity: parse persisted trust domain: %w", err)
	}

	keyBlock, _ := pem.Decode([]byte(state.CAKeyPEM))
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("identity: decode persisted CA key PEM")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("identity: parse persisted CA key: %w", err)
	}

	certBlock, _ := pem.Decode([]byte(state.CACertPEM))
	if certBlock == nil {
		return nil, nil, fmt.Errorf("identity: decode persisted CA cert PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("identity: parse persisted CA cert: %w", err)
	}

	return &CA{trustDomain: td, cert: cert, key: key}, state.DEK, nil
}
