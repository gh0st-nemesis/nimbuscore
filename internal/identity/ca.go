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

const DefaultSVIDTTL = 1 * time.Hour

type CA struct {
	trustDomain spiffeid.TrustDomain
	cert        *x509.Certificate
	key         *ecdsa.PrivateKey
}

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

func (ca *CA) TrustDomain() spiffeid.TrustDomain { return ca.trustDomain }

func (ca *CA) Cert() *x509.Certificate { return ca.cert }

func (ca *CA) TrustBundle() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	return pool
}

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
