package hostpath

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

const (
	DriverName    = "hostpath.nimbuscore.local"
	DriverVersion = "0.1.0"
)

type volumeMeta struct {
	Name          string `json:"name"`
	CapacityBytes int64  `json:"capacity_bytes"`
}

type Driver struct {
	csi.UnimplementedIdentityServer
	csi.UnimplementedControllerServer
	csi.UnimplementedNodeServer

	nodeID  string
	baseDir string
	mu      sync.Mutex
}

func New(nodeID, baseDir string) (*Driver, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("hostpath: create base dir: %w", err)
	}
	return &Driver{nodeID: nodeID, baseDir: baseDir}, nil
}

func (d *Driver) volumeDir(id string) string { return filepath.Join(d.baseDir, id) }
func (d *Driver) metaPath(id string) string  { return filepath.Join(d.volumeDir(id), ".volume.json") }

func (d *Driver) validVolumeID(id string) bool {
	if id == "" {
		return false
	}
	dir := filepath.Clean(d.volumeDir(id))
	base := filepath.Clean(d.baseDir)
	rel, err := filepath.Rel(base, dir)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (d *Driver) GetPluginInfo(context.Context, *csi.GetPluginInfoRequest) (*csi.GetPluginInfoResponse, error) {
	return &csi.GetPluginInfoResponse{Name: DriverName, VendorVersion: DriverVersion}, nil
}

func (d *Driver) GetPluginCapabilities(context.Context, *csi.GetPluginCapabilitiesRequest) (*csi.GetPluginCapabilitiesResponse, error) {
	return &csi.GetPluginCapabilitiesResponse{
		Capabilities: []*csi.PluginCapability{
			{Type: &csi.PluginCapability_Service_{Service: &csi.PluginCapability_Service{
				Type: csi.PluginCapability_Service_CONTROLLER_SERVICE,
			}}},
		},
	}, nil
}

func (d *Driver) Probe(context.Context, *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	return &csi.ProbeResponse{Ready: wrapperspb.Bool(true)}, nil
}

func (d *Driver) CreateVolume(_ context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	if req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	if !d.validVolumeID(req.GetName()) {
		return nil, status.Errorf(codes.InvalidArgument, "invalid volume name %q", req.GetName())
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	volumeID := req.GetName()
	capacity := req.GetCapacityRange().GetRequiredBytes()

	if err := os.MkdirAll(d.volumeDir(volumeID), 0o755); err != nil {
		return nil, status.Errorf(codes.Internal, "create volume directory: %v", err)
	}

	meta := volumeMeta{Name: volumeID, CapacityBytes: capacity}
	b, err := json.Marshal(meta)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal volume metadata: %v", err)
	}
	if err := os.WriteFile(d.metaPath(volumeID), b, 0o644); err != nil {
		return nil, status.Errorf(codes.Internal, "write volume metadata: %v", err)
	}

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      volumeID,
			CapacityBytes: capacity,
		},
	}, nil
}

func (d *Driver) DeleteVolume(_ context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume_id is required")
	}
	if !d.validVolumeID(req.GetVolumeId()) {
		return nil, status.Errorf(codes.InvalidArgument, "invalid volume_id %q", req.GetVolumeId())
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if err := os.RemoveAll(d.volumeDir(req.GetVolumeId())); err != nil {
		return nil, status.Errorf(codes.Internal, "delete volume directory: %v", err)
	}
	return &csi.DeleteVolumeResponse{}, nil
}

func (d *Driver) ControllerGetCapabilities(context.Context, *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	rpc := func(t csi.ControllerServiceCapability_RPC_Type) *csi.ControllerServiceCapability {
		return &csi.ControllerServiceCapability{Type: &csi.ControllerServiceCapability_Rpc{
			Rpc: &csi.ControllerServiceCapability_RPC{Type: t},
		}}
	}
	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: []*csi.ControllerServiceCapability{
			rpc(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME),
			rpc(csi.ControllerServiceCapability_RPC_LIST_VOLUMES),
		},
	}, nil
}

func (d *Driver) ListVolumes(_ context.Context, req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	entries, err := os.ReadDir(d.baseDir)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list volumes: %v", err)
	}

	var out []*csi.ListVolumesResponse_Entry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		meta, err := d.readMeta(e.Name())
		if err != nil {
			continue
		}
		out = append(out, &csi.ListVolumesResponse_Entry{
			Volume: &csi.Volume{VolumeId: meta.Name, CapacityBytes: meta.CapacityBytes},
		})
	}
	return &csi.ListVolumesResponse{Entries: out}, nil
}

func (d *Driver) readMeta(volumeID string) (volumeMeta, error) {
	b, err := os.ReadFile(d.metaPath(volumeID))
	if err != nil {
		return volumeMeta{}, err
	}
	var meta volumeMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		return volumeMeta{}, err
	}
	return meta, nil
}

func (d *Driver) NodeGetInfo(context.Context, *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{NodeId: d.nodeID}, nil
}

func (d *Driver) NodeGetCapabilities(context.Context, *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{}, nil
}

func (d *Driver) NodeStageVolume(context.Context, *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	return &csi.NodeStageVolumeResponse{}, nil
}

func (d *Driver) NodeUnstageVolume(context.Context, *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (d *Driver) NodePublishVolume(_ context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	if req.GetVolumeId() == "" || req.GetTargetPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume_id and target_path are required")
	}
	if !d.validVolumeID(req.GetVolumeId()) {
		return nil, status.Errorf(codes.InvalidArgument, "invalid volume_id %q", req.GetVolumeId())
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	source := d.volumeDir(req.GetVolumeId())
	if _, err := os.Stat(source); err != nil {
		return nil, status.Errorf(codes.NotFound, "volume %q: %v", req.GetVolumeId(), err)
	}

	if err := os.MkdirAll(filepath.Dir(req.GetTargetPath()), 0o755); err != nil {
		return nil, status.Errorf(codes.Internal, "prepare target path: %v", err)
	}
	if _, err := os.Lstat(req.GetTargetPath()); err == nil {
		return &csi.NodePublishVolumeResponse{}, nil
	}
	if err := os.Symlink(source, req.GetTargetPath()); err != nil {
		return nil, status.Errorf(codes.Internal, "publish volume %q at %q: %v", req.GetVolumeId(), req.GetTargetPath(), err)
	}
	return &csi.NodePublishVolumeResponse{}, nil
}

func (d *Driver) NodeUnpublishVolume(_ context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	if req.GetTargetPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "target_path is required")
	}
	if err := os.Remove(req.GetTargetPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, status.Errorf(codes.Internal, "unpublish %q: %v", req.GetTargetPath(), err)
	}
	return &csi.NodeUnpublishVolumeResponse{}, nil
}
