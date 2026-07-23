package apiserver

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/container-storage-interface/spec/lib/go/csi"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

type CSIController interface {
	CreateVolume(context.Context, *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error)
	DeleteVolume(context.Context, *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error)
}

type volumeService struct {
	v1.UnimplementedVolumeServiceServer
	volumes *registry.Registry[*v1.Volume]
	csi     CSIController
}

func NewVolumeService(s store.Store, csiController CSIController) v1.VolumeServiceServer {
	return &volumeService{
		volumes: registry.New(s, "volumes", func() *v1.Volume { return &v1.Volume{} }),
		csi:     csiController,
	}
}

func (svc *volumeService) CreateVolume(ctx context.Context, req *v1.CreateVolumeRequest) (*v1.Volume, error) {
	vol := req.GetVolume()
	meta := vol.GetMetadata()
	if meta.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume.metadata.name is required")
	}

	resp, err := svc.csi.CreateVolume(ctx, &csi.CreateVolumeRequest{
		Name:          meta.GetNamespace() + "/" + meta.GetName(),
		CapacityRange: &csi.CapacityRange{RequiredBytes: vol.GetSpec().GetRequestedBytes()},
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "provision volume: %v", err)
	}

	vol.Status = &v1.VolumeStatus{
		Phase:         v1.VolumePhase_VOLUME_PHASE_BOUND,
		CapacityBytes: resp.GetVolume().GetCapacityBytes(),
	}

	if err := svc.volumes.Put(ctx, meta.GetNamespace(), meta.GetName(), vol); err != nil {
		return nil, status.Errorf(codes.Internal, "create volume: %v", err)
	}
	return vol, nil
}

func (svc *volumeService) GetVolume(ctx context.Context, req *v1.GetVolumeRequest) (*v1.Volume, error) {
	vol, err := svc.volumes.Get(ctx, req.GetNamespace(), req.GetName())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "volume %s/%s: %v", req.GetNamespace(), req.GetName(), err)
	}
	return vol, nil
}

func (svc *volumeService) ListVolumes(ctx context.Context, req *v1.ListVolumesRequest) (*v1.ListVolumesResponse, error) {
	vols, err := svc.volumes.List(ctx, req.GetNamespace())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list volumes: %v", err)
	}
	return &v1.ListVolumesResponse{Items: vols}, nil
}

func (svc *volumeService) DeleteVolume(ctx context.Context, req *v1.DeleteVolumeRequest) (*v1.DeleteVolumeResponse, error) {
	if req.GetNamespace() == "" || req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "namespace and name are required")
	}

	if _, err := svc.csi.DeleteVolume(ctx, &csi.DeleteVolumeRequest{
		VolumeId: req.GetNamespace() + "/" + req.GetName(),
	}); err != nil {
		return nil, status.Errorf(codes.Internal, "deprovision volume: %v", err)
	}

	if err := svc.volumes.Delete(ctx, req.GetNamespace(), req.GetName()); err != nil {
		return nil, status.Errorf(codes.Internal, "delete volume: %v", err)
	}
	return &v1.DeleteVolumeResponse{}, nil
}
