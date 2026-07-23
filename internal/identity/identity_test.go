package identity

import (
	"context"
	"crypto/tls"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
)

func issueSVID(t *testing.T, ca *CA, path string) *SVID {
	t.Helper()
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	id, err := spiffeid.FromPath(ca.TrustDomain(), path)
	if err != nil {
		t.Fatalf("FromPath: %v", err)
	}
	cert, err := ca.IssueSVID(&key.PublicKey, id, time.Hour)
	if err != nil {
		t.Fatalf("IssueSVID: %v", err)
	}
	return NewSVID(key, cert, ca.TrustBundle())
}

func TestIssueSVIDEmbedsSPIFFEID(t *testing.T) {
	ca, err := NewCA("nimbuscore.local")
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}

	svid := issueSVID(t, ca, "/node/worker-1")
	id, err := svid.ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}
	if want := "spiffe://nimbuscore.local/node/worker-1"; id.String() != want {
		t.Errorf("ID = %q, want %q", id.String(), want)
	}
}

func TestIssueSVIDRejectsOtherTrustDomain(t *testing.T) {
	ca, err := NewCA("nimbuscore.local")
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	foreignID := spiffeid.RequireFromString("spiffe://someone-elses-cluster.example/node/x")

	if _, err := ca.IssueSVID(&key.PublicKey, foreignID, time.Hour); err == nil {
		t.Fatal("IssueSVID succeeded for an ID outside the CA's trust domain, want error")
	}
}

func TestMTLSHandshakeEnforcesIdentity(t *testing.T) {
	ca, err := NewCA("nimbuscore.local")
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}

	serverSVID := issueSVID(t, ca, "/control-plane/node-1")
	clientSVID := issueSVID(t, ca, "/node/worker-1")

	lis, err := tls.Listen("tcp", "127.0.0.1:0", serverSVID.ServerTLSConfig())
	if err != nil {
		t.Fatalf("tls.Listen: %v", err)
	}
	defer lis.Close()

	accept := func() {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.(*tls.Conn).HandshakeContext(context.Background())
	}

	t.Run("expected identity connects", func(t *testing.T) {
		go accept()

		serverID, err := serverSVID.ID()
		if err != nil {
			t.Fatalf("ID: %v", err)
		}
		conn, err := tls.Dial("tcp", lis.Addr().String(), clientSVID.ClientTLSConfig(spiffeid.MatchID(serverID)))
		if err != nil {
			t.Fatalf("tls.Dial: %v", err)
		}
		defer conn.Close()
	})

	t.Run("unexpected identity is rejected", func(t *testing.T) {
		go accept()

		wrongExpectation := spiffeid.RequireFromString("spiffe://nimbuscore.local/control-plane/not-the-real-one")
		_, err := tls.Dial("tcp", lis.Addr().String(), clientSVID.ClientTLSConfig(spiffeid.MatchID(wrongExpectation)))
		if err == nil {
			t.Fatal("tls.Dial succeeded despite identity mismatch, want error")
		}
	})
}
