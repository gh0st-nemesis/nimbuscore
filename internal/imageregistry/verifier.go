package imageregistry

import (
	"context"
	"fmt"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

type Lister interface {
	Get(ctx context.Context, namespace, name string) (*v1.ImageRecord, error)
}

type Verifier struct {
	records Lister
}

func NewVerifier(records Lister) *Verifier {
	return &Verifier{records: records}
}

func (v *Verifier) Verify(ctx context.Context, imageRef string) error {
	if _, err := v.records.Get(ctx, "", imageRef); err != nil {
		return fmt.Errorf("imageregistry: image %q was not pushed to the cluster registry: %w", imageRef, err)
	}
	return nil
}
