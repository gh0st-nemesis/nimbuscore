package identity

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoadBootstrapStateRoundTrips(t *testing.T) {
	ca, err := NewCA("nimbuscore.local")
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}
	dek := []byte("0123456789abcdef0123456789abcdef")

	dir := t.TempDir()
	path := BootstrapStatePath(dir)

	if err := SaveBootstrapState(path, ca, dek); err != nil {
		t.Fatalf("SaveBootstrapState: %v", err)
	}

	loadedCA, loadedDEK, err := LoadBootstrapState(path)
	if err != nil {
		t.Fatalf("LoadBootstrapState: %v", err)
	}

	if loadedCA.TrustDomain().String() != ca.TrustDomain().String() {
		t.Errorf("trust domain = %q, want %q", loadedCA.TrustDomain(), ca.TrustDomain())
	}
	if !loadedCA.Cert().Equal(ca.Cert()) {
		t.Error("loaded CA certificate does not match the original")
	}
	if !bytes.Equal(loadedDEK, dek) {
		t.Errorf("dek = %x, want %x", loadedDEK, dek)
	}

	svid := issueSVID(t, loadedCA, "/node/worker-1")
	if _, err := svid.ID(); err != nil {
		t.Errorf("issuing an SVID from the reloaded CA failed: %v", err)
	}
}

func TestLoadBootstrapStateReportsNotExist(t *testing.T) {
	dir := t.TempDir()
	_, _, err := LoadBootstrapState(BootstrapStatePath(dir))
	if !os.IsNotExist(err) {
		t.Errorf("err = %v, want a not-exist error", err)
	}
}

func TestSaveBootstrapStateFilePermissions(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("permission bits are not meaningful on this platform")
	}

	ca, err := NewCA("nimbuscore.local")
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "bootstrap-identity.json")
	if err := SaveBootstrapState(path, ca, []byte("key")); err != nil {
		t.Fatalf("SaveBootstrapState: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %o, want 0600", perm)
	}
}
