package controller

import (
	"context"
	"crypto/rand"
	"log"
	"time"

	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

type KeyRotator interface {
	RotateEncryptionKey(newKey []byte) error
	ReencryptAll(ctx context.Context) (int, error)
}

type KeyRotationReconciler struct {
	store    KeyRotator
	interval time.Duration
}

func NewKeyRotationReconciler(s KeyRotator, interval time.Duration) *KeyRotationReconciler {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	return &KeyRotationReconciler{store: s, interval: interval}
}

func (r *KeyRotationReconciler) Name() string { return "secret-key-rotator" }

func (r *KeyRotationReconciler) Reconcile(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			r.rotate(ctx)
		}
	}
}

func (r *KeyRotationReconciler) rotate(ctx context.Context) {
	newKey := make([]byte, store.EncryptionKeySize)
	if _, err := rand.Read(newKey); err != nil {
		log.Printf("secret-key-rotator: generate key: %v", err)
		return
	}

	if err := r.store.RotateEncryptionKey(newKey); err != nil {
		log.Printf("secret-key-rotator: rotate: %v", err)
		return
	}

	n, err := r.store.ReencryptAll(ctx)
	if err != nil {
		log.Printf("secret-key-rotator: re-encrypt (completed %d entries before error): %v", n, err)
		return
	}
	log.Printf("secret-key-rotator: rotated encryption key, re-encrypted %d entries", n)
}
