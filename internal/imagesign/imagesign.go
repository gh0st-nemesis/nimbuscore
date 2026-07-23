package imagesign

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"

	"github.com/gh0st-nemesis/nimbuscore/internal/identity"
)

func GenerateKey() (*ecdsa.PrivateKey, error) {
	return identity.GenerateKey()
}

func Sign(key *ecdsa.PrivateKey, imageRef string) ([]byte, error) {
	digest := sha256.Sum256([]byte(imageRef))
	return ecdsa.SignASN1(rand.Reader, key, digest[:])
}

func Verify(pub *ecdsa.PublicKey, imageRef string, sig []byte) error {
	digest := sha256.Sum256([]byte(imageRef))
	if !ecdsa.VerifyASN1(pub, digest[:], sig) {
		return fmt.Errorf("imagesign: signature verification failed for %q", imageRef)
	}
	return nil
}

func MarshalPublicKey(pub *ecdsa.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("imagesign: marshal public key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}

func MarshalPrivateKey(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("imagesign: marshal private key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

func ParsePublicKey(pemBytes []byte) (*ecdsa.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("imagesign: no PEM block found")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("imagesign: parse public key: %w", err)
	}
	ecdsaPub, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("imagesign: key is not ECDSA")
	}
	return ecdsaPub, nil
}

func ParsePrivateKey(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("imagesign: no PEM block found")
	}
	return x509.ParseECPrivateKey(block.Bytes)
}

type TrustFile struct {
	Signatures map[string]string `json:"signatures"`
}

func LoadTrustFile(path string) (*TrustFile, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &TrustFile{Signatures: map[string]string{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("imagesign: read trust file: %w", err)
	}
	var tf TrustFile
	if err := json.Unmarshal(b, &tf); err != nil {
		return nil, fmt.Errorf("imagesign: parse trust file: %w", err)
	}
	if tf.Signatures == nil {
		tf.Signatures = map[string]string{}
	}
	return &tf, nil
}

func (tf *TrustFile) Save(path string) error {
	b, err := json.MarshalIndent(tf, "", "  ")
	if err != nil {
		return fmt.Errorf("imagesign: marshal trust file: %w", err)
	}
	return os.WriteFile(path, b, 0o600)
}

func (tf *TrustFile) Add(imageRef string, sig []byte) {
	tf.Signatures[imageRef] = base64.StdEncoding.EncodeToString(sig)
}

type Verifier interface {
	Verify(imageRef string) error
}

type KeyVerifier struct {
	pub *ecdsa.PublicKey
	tf  *TrustFile
}

func NewKeyVerifier(pub *ecdsa.PublicKey, tf *TrustFile) *KeyVerifier {
	return &KeyVerifier{pub: pub, tf: tf}
}

func (v *KeyVerifier) Verify(imageRef string) error {
	encoded, ok := v.tf.Signatures[imageRef]
	if !ok {
		return fmt.Errorf("imagesign: no signature on file for image %q", imageRef)
	}
	sig, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("imagesign: decode signature for %q: %w", imageRef, err)
	}
	return Verify(v.pub, imageRef, sig)
}
