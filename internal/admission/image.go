package admission

import (
	"context"
	"fmt"
)

type ImageVerifier interface {
	Verify(ctx context.Context, imageRef string) error
}

type ImageSignaturePolicy struct {
	verifier ImageVerifier
}

func NewImageSignaturePolicy(verifier ImageVerifier) *ImageSignaturePolicy {
	return &ImageSignaturePolicy{verifier: verifier}
}

func (p *ImageSignaturePolicy) Admit(ctx context.Context, req *Request) error {
	for _, c := range req.Spec.GetContainers() {
		if err := p.verifier.Verify(ctx, c.GetImage()); err != nil {
			return fmt.Errorf("container %q: image %q: %w", c.GetName(), c.GetImage(), err)
		}
	}
	return nil
}
