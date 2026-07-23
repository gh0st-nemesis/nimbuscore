// Package identity implements a lightweight, in-process certificate
// authority that issues short-lived X.509 SVIDs carrying a
// spiffe://<trust-domain>/... URI SAN (design doc section 05: "identités
// éphémères — SPIFFE/SPIRE partout, pas de credentials longue durée en
// circulation").
//
// It deliberately does not run a SPIRE server: spiffe/go-spiffe's
// central role is talking to a SPIRE Workload API over a Unix socket,
// and no SPIRE deployment exists in this environment. What's reused
// from that library is the part that doesn't require one — the
// spiffeid package, for parsing, formatting, and matching SPIFFE IDs —
// while this package plays the role SPIRE's server would: holding the
// trust domain's root key and signing leaf certificates. Every other
// package here (internal/apiserver, cmd/nimbus-agent) depends only on
// *SVID's TLS config methods, so swapping this CA for a real SPIRE
// Workload API client later is a localized change.
package identity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net/url"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
)

// DefaultSVIDTTL is how long an issued leaf certificate stays valid.
// Short-lived by design: rotation, not revocation, is what stops a
// compromised credential from working.
const DefaultSVIDTTL = 1 * time.Hour

// CA is a minimal certificate authority for one NimbusCore trust
// domain. The root private key lives only in the control-plane process
// that generates it.
type CA struct {
	trustDomain spiffeid.TrustDomain
	cert        *x509.Certificate
	key         *ecdsa.PrivateKey
}

// NewCA generates a fresh self-signed root for trustDomain (e.g.
// "nimbuscore.local").
func NewCA(trustDomain string) (*CA, error) {
	td, err := spiffeid.TrustDomainFromString(trustDomain)
	if err != nil {
		return nil, fmt.Errorf("identity: invalid trust domain %q: %w", trustDomain, err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("identity: generate CA key: %w", err)
	}

	serial, err := randSerial()
	if err != nil {
		return nil, err
	}

	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "NimbusCore CA - " + td.String()},
		NotBefore:             time.Now().Add(-5 * time.Minute),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("identity: self-sign CA cert: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("identity: parse CA cert: %w", err)
	}

	return &CA{trustDomain: td, cert: cert, key: key}, nil
}

// TrustDomain returns the CA's trust domain.
func (ca *CA) TrustDomain() spiffeid.TrustDomain { return ca.trustDomain }

// Cert returns the CA's own certificate, distributed to workloads so
// they can build their trust bundle (its .Raw field is the DER encoding
// sent over RequestSVIDResponse.trust_bundle_der).
func (ca *CA) Cert() *x509.Certificate { return ca.cert }

// TrustBundle returns a pool containing just this CA's certificate.
func (ca *CA) TrustBundle() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	return pool
}

// IssueSVID signs a leaf certificate for pub, embedding id as its sole
// spiffe:// URI SAN — the only identity information the rest of this
// codebase relies on (no CN/DNS-based trust, consistent with
// deny-by-default).
func (ca *CA) IssueSVID(pub *ecdsa.PublicKey, id spiffeid.ID, ttl time.Duration) (*x509.Certificate, error) {
	if !id.MemberOf(ca.trustDomain) {
		return nil, fmt.Errorf("identity: %s is not a member of trust domain %s", id, ca.trustDomain)
	}
	if ttl <= 0 {
		ttl = DefaultSVIDTTL
	}

	serial, err := randSerial()
	if err != nil {
		return nil, err
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: id.String()},
		URIs:         []*url.URL{id.URL()},
		NotBefore:    time.Now().Add(-1 * time.Minute),
		NotAfter:     time.Now().Add(ttl),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, pub, ca.key)
	if err != nil {
		return nil, fmt.Errorf("identity: issue SVID for %s: %w", id, err)
	}
	return x509.ParseCertificate(der)
}

func randSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("identity: generate serial number: %w", err)
	}
	return serial, nil
}
