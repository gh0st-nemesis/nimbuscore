package imageregistry

import (
	"context"
	"errors"
	"testing"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

type fakeLister struct {
	records map[string]*v1.ImageRecord
}

func (f *fakeLister) Get(_ context.Context, _, name string) (*v1.ImageRecord, error) {
	rec, ok := f.records[name]
	if !ok {
		return nil, errors.New("not found")
	}
	return rec, nil
}

func TestVerifierAcceptsPushedImage(t *testing.T) {
	v := NewVerifier(&fakeLister{records: map[string]*v1.ImageRecord{
		"registry.internal/app:v1": {Reference: "registry.internal/app:v1"},
	}})
	if err := v.Verify(context.Background(), "registry.internal/app:v1"); err != nil {
		t.Errorf("Verify(pushed image) = %v, want nil", err)
	}
}

func TestVerifierRejectsUnpushedImage(t *testing.T) {
	v := NewVerifier(&fakeLister{records: map[string]*v1.ImageRecord{}})
	if err := v.Verify(context.Background(), "registry.internal/app:v1"); err == nil {
		t.Error("Verify(unpushed image) succeeded, want error")
	}
}
