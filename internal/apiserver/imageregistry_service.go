package apiserver

import (
	"context"
	"crypto/ecdsa"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/imagesign"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

type imageRegistryService struct {
	v1.UnimplementedImageRegistryServiceServer
	images *registry.Registry[*v1.ImageRecord]
	pub    *ecdsa.PublicKey
}

func NewImageRegistryService(s store.Store, pub *ecdsa.PublicKey) v1.ImageRegistryServiceServer {
	return &imageRegistryService{
		images: registry.New(s, "images", func() *v1.ImageRecord { return &v1.ImageRecord{} }),
		pub:    pub,
	}
}

func (svc *imageRegistryService) PushImage(ctx context.Context, req *v1.PushImageRequest) (*v1.ImageRecord, error) {
	if req.GetReference() == "" {
		return nil, status.Error(codes.InvalidArgument, "reference is required")
	}
	if err := imagesign.Verify(svc.pub, req.GetReference(), req.GetSignature()); err != nil {
		return nil, status.Errorf(codes.PermissionDenied, "signature verification failed: %v", err)
	}

	rec := &v1.ImageRecord{
		Reference:    req.GetReference(),
		Signature:    req.GetSignature(),
		PushedAtUnix: time.Now().Unix(),
	}
	if err := svc.images.Put(ctx, "", req.GetReference(), rec); err != nil {
		return nil, status.Errorf(codes.Internal, "push image: %v", err)
	}
	return rec, nil
}

func (svc *imageRegistryService) GetImage(ctx context.Context, req *v1.GetImageRequest) (*v1.ImageRecord, error) {
	rec, err := svc.images.Get(ctx, "", req.GetReference())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "image %s: %v", req.GetReference(), err)
	}
	return rec, nil
}

func (svc *imageRegistryService) ListImages(ctx context.Context, _ *v1.ListImagesRequest) (*v1.ListImagesResponse, error) {
	items, err := svc.images.List(ctx, "")
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list images: %v", err)
	}
	return &v1.ListImagesResponse{Items: items}, nil
}

func (svc *imageRegistryService) DeleteImage(ctx context.Context, req *v1.DeleteImageRequest) (*v1.DeleteImageResponse, error) {
	if err := svc.images.Delete(ctx, "", req.GetReference()); err != nil {
		return nil, status.Errorf(codes.Internal, "delete image: %v", err)
	}
	return &v1.DeleteImageResponse{}, nil
}
