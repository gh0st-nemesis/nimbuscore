package identity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
)

type svidMaterial struct {
	key  *ecdsa.PrivateKey
	cert *x509.Certificate
}

type SVID struct {
	material atomic.Pointer[svidMaterial]
	bundle   *x509.CertPool
}

func NewSVID(key *ecdsa.PrivateKey, cert *x509.Certificate, bundle *x509.CertPool) *SVID {
	s := &SVID{bundle: bundle}
	s.material.Store(&svidMaterial{key: key, cert: cert})
	return s
}

func GenerateKey() (*ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("identity: generate key: %w", err)
	}
	return key, nil
}

func GenerateCSR(commonName string) (csrDER []byte, key *ecdsa.PrivateKey, err error) {
	key, err = GenerateKey()
	if err != nil {
		return nil, nil, err
	}

	tmpl := &x509.CertificateRequest{
		Subject:            pkix.Name{CommonName: commonName},
		SignatureAlgorithm: x509.ECDSAWithSHA256,
	}
	csrDER, err = x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		return nil, nil, fmt.Errorf("identity: create CSR: %w", err)
	}
	return csrDER, key, nil
}

func (s *SVID) Rotate(key *ecdsa.PrivateKey, cert *x509.Certificate) {
	s.material.Store(&svidMaterial{key: key, cert: cert})
}

func (s *SVID) Materials() (*ecdsa.PrivateKey, *x509.Certificate) {
	m := s.material.Load()
	return m.key, m.cert
}

func (s *SVID) ID() (spiffeid.ID, error) {
	_, cert := s.Materials()
	if len(cert.URIs) != 1 {
		return spiffeid.ID{}, errors.New("identity: SVID certificate must carry exactly one URI SAN")
	}
	return spiffeid.FromURI(cert.URIs[0])
}

func (s *SVID) tlsCertificate() (tls.Certificate, error) {
	key, cert := s.Materials()
	if cert == nil {
		return tls.Certificate{}, errors.New("identity: SVID has no certificate yet")
	}
	return tls.Certificate{
		Certificate: [][]byte{cert.Raw},
		PrivateKey:  key,
		Leaf:        cert,
	}, nil
}

func (s *SVID) ServerTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			cert, err := s.tlsCertificate()
			if err != nil {
				return nil, err
			}
			return &cert, nil
		},
		ClientAuth: tls.VerifyClientCertIfGiven,
		ClientCAs:  s.bundle,
	}
}

func (s *SVID) ClientTLSConfig(expect spiffeid.Matcher) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			cert, err := s.tlsCertificate()
			if err != nil {
				return nil, err
			}
			return &cert, nil
		},
		RootCAs: s.bundle,

		InsecureSkipVerify:    true,
		VerifyPeerCertificate: verifyPeerSPIFFE(s.bundle, expect),
	}
}

func verifyPeerSPIFFE(bundle *x509.CertPool, expect spiffeid.Matcher) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return errors.New("identity: peer presented no certificate")
		}
		leaf, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return fmt.Errorf("identity: parse peer certificate: %w", err)
		}

		intermediates := x509.NewCertPool()
		for _, raw := range rawCerts[1:] {
			cert, err := x509.ParseCertificate(raw)
			if err != nil {
				return fmt.Errorf("identity: parse peer certificate: %w", err)
			}
			intermediates.AddCert(cert)
		}

		if _, err := leaf.Verify(x509.VerifyOptions{
			Roots:         bundle,
			Intermediates: intermediates,
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
		}); err != nil {
			return fmt.Errorf("identity: verify peer chain: %w", err)
		}

		if len(leaf.URIs) != 1 {
			return errors.New("identity: peer certificate must carry exactly one URI SAN")
		}
		id, err := spiffeid.FromURI(leaf.URIs[0])
		if err != nil {
			return fmt.Errorf("identity: peer certificate URI is not a SPIFFE ID: %w", err)
		}
		if err := expect(id); err != nil {
			return fmt.Errorf("identity: peer %s rejected: %w", id, err)
		}
		return nil
	}
}
