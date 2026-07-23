package admission

import (
	"context"
	"fmt"
)

type ImageVerifier interface {
	Verify(imageRef string) error
}

type ImageSignaturePolicy struct {
	verifier ImageVerifier
}

func NewImageSignaturePolicy(verifier ImageVerifier) *ImageSignaturePolicy {
	return &ImageSignaturePolicy{verifier: verifier}
}

func (p *ImageSignaturePolicy) Admit(_ context.Context, req *Request) error {
	for _, c := range req.Spec.GetContainers() {
		if err := p.verifier.Verify(c.GetImage()); err != nil {
			return fmt.Errorf("container %q: image %q: %w", c.GetName(), c.GetImage(), err)
		}
	}
	return nil
}
