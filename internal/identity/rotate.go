package identity

import (
	"context"
	"log"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
)

func StartSelfIssueRotateLoop(ctx context.Context, svid *SVID, ca *CA, path string, ttl time.Duration) {
	if ttl <= 0 {
		ttl = DefaultSVIDTTL
	}
	ticker := time.NewTicker(ttl / 2)

	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				key, err := GenerateKey()
				if err != nil {
					log.Printf("identity: rotate SVID: generate key: %v", err)
					continue
				}
				id, err := spiffeid.FromPath(ca.TrustDomain(), path)
				if err != nil {
					log.Printf("identity: rotate SVID: %v", err)
					continue
				}
				cert, err := ca.IssueSVID(&key.PublicKey, id, ttl)
				if err != nil {
					log.Printf("identity: rotate SVID: issue: %v", err)
					continue
				}
				svid.Rotate(key, cert)
				log.Printf("identity: rotated self-issued SVID for %s", id)
			}
		}
	}()
}

func StartReenrollRotateLoop(ctx context.Context, svid *SVID, cfg EnrollConfig, ttl time.Duration) {
	if ttl <= 0 {
		ttl = DefaultSVIDTTL
	}
	ticker := time.NewTicker(ttl / 2)

	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				renewed, _, err := Enroll(ctx, cfg)
				if err != nil {
					log.Printf("identity: rotate SVID: re-enroll: %v", err)
					continue
				}
				key, cert := renewed.Materials()
				svid.Rotate(key, cert)
				log.Printf("identity: rotated SVID for %s via re-enrollment", cfg.Name)
			}
		}
	}()
}
