package imagesign

import (
	"path/filepath"
	"testing"
)

func TestSignAndVerifyRoundTrip(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	sig, err := Sign(key, "registry.example/app:v1")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if err := Verify(&key.PublicKey, "registry.example/app:v1", sig); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerifyRejectsWrongImage(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	sig, err := Sign(key, "registry.example/app:v1")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if err := Verify(&key.PublicKey, "registry.example/app:v2", sig); err == nil {
		t.Fatal("Verify succeeded for a different image reference, want error")
	}
}

func TestVerifyRejectsWrongKey(t *testing.T) {
	key1, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	key2, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	sig, err := Sign(key1, "registry.example/app:v1")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if err := Verify(&key2.PublicKey, "registry.example/app:v1", sig); err == nil {
		t.Fatal("Verify succeeded with the wrong public key, want error")
	}
}

func TestKeyPEMRoundTrip(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	privPEM, err := MarshalPrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPrivateKey: %v", err)
	}
	pubPEM, err := MarshalPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPublicKey: %v", err)
	}

	parsedPriv, err := ParsePrivateKey(privPEM)
	if err != nil {
		t.Fatalf("ParsePrivateKey: %v", err)
	}
	parsedPub, err := ParsePublicKey(pubPEM)
	if err != nil {
		t.Fatalf("ParsePublicKey: %v", err)
	}

	sig, err := Sign(parsedPriv, "registry.example/app:v1")
	if err != nil {
		t.Fatalf("Sign with parsed key: %v", err)
	}
	if err := Verify(parsedPub, "registry.example/app:v1", sig); err != nil {
		t.Fatalf("Verify with parsed key: %v", err)
	}
}

func TestKeyVerifierRejectsUnsignedImage(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	tf := &TrustFile{Signatures: map[string]string{}}
	sig, err := Sign(key, "registry.example/signed:v1")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	tf.Add("registry.example/signed:v1", sig)

	v := NewKeyVerifier(&key.PublicKey, tf)

	if err := v.Verify("registry.example/signed:v1"); err != nil {
		t.Errorf("Verify(signed) = %v, want nil", err)
	}
	if err := v.Verify("registry.example/unsigned:v1"); err == nil {
		t.Error("Verify(unsigned) succeeded, want error")
	}
}

func TestTrustFileSaveLoadRoundTrip(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	sig, err := Sign(key, "registry.example/app:v1")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	tf := &TrustFile{Signatures: map[string]string{}}
	tf.Add("registry.example/app:v1", sig)

	path := filepath.Join(t.TempDir(), "trust.json")
	if err := tf.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadTrustFile(path)
	if err != nil {
		t.Fatalf("LoadTrustFile: %v", err)
	}
	v := NewKeyVerifier(&key.PublicKey, loaded)
	if err := v.Verify("registry.example/app:v1"); err != nil {
		t.Errorf("Verify after reload = %v, want nil", err)
	}
}

func TestLoadTrustFileMissingReturnsEmpty(t *testing.T) {
	tf, err := LoadTrustFile(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("LoadTrustFile: %v", err)
	}
	if len(tf.Signatures) != 0 {
		t.Errorf("expected empty trust file, got %d entries", len(tf.Signatures))
	}
}
