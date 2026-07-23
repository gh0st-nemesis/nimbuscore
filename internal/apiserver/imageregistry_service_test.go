package apiserver_test

import (
	"context"
	"testing"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/apiserver"
	"github.com/gh0st-nemesis/nimbuscore/internal/imagesign"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

func TestImageRegistryRejectsUnsignedPush(t *testing.T) {
	key, err := imagesign.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	svc := apiserver.NewImageRegistryService(store.NewMemStore(), &key.PublicKey)

	_, err = svc.PushImage(context.Background(), &v1.PushImageRequest{
		Reference: "registry.internal/app:v1",
		Signature: []byte("not a real signature"),
	})
	if err == nil {
		t.Fatal("PushImage succeeded with a bogus signature, want rejection")
	}
}

func TestImageRegistryAcceptsValidPushAndRoundTrips(t *testing.T) {
	key, err := imagesign.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	svc := apiserver.NewImageRegistryService(store.NewMemStore(), &key.PublicKey)

	ref := "registry.internal/app:v1"
	sig, err := imagesign.Sign(key, ref)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	ctx := context.Background()
	if _, err := svc.PushImage(ctx, &v1.PushImageRequest{Reference: ref, Signature: sig}); err != nil {
		t.Fatalf("PushImage: %v", err)
	}

	got, err := svc.GetImage(ctx, &v1.GetImageRequest{Reference: ref})
	if err != nil {
		t.Fatalf("GetImage: %v", err)
	}
	if got.GetReference() != ref {
		t.Errorf("Reference = %q, want %q", got.GetReference(), ref)
	}

	list, err := svc.ListImages(ctx, &v1.ListImagesRequest{})
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if len(list.GetItems()) != 1 {
		t.Fatalf("ListImages returned %d items, want 1", len(list.GetItems()))
	}

	if _, err := svc.DeleteImage(ctx, &v1.DeleteImageRequest{Reference: ref}); err != nil {
		t.Fatalf("DeleteImage: %v", err)
	}
	if _, err := svc.GetImage(ctx, &v1.GetImageRequest{Reference: ref}); err == nil {
		t.Fatal("GetImage succeeded after delete, want error")
	}
}

func TestImageRegistryRejectsPushWithWrongKey(t *testing.T) {
	key1, err := imagesign.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	key2, err := imagesign.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	svc := apiserver.NewImageRegistryService(store.NewMemStore(), &key1.PublicKey)

	ref := "registry.internal/app:v1"
	sig, err := imagesign.Sign(key2, ref)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if _, err := svc.PushImage(context.Background(), &v1.PushImageRequest{Reference: ref, Signature: sig}); err == nil {
		t.Fatal("PushImage succeeded with a signature from the wrong key, want rejection")
	}
}
