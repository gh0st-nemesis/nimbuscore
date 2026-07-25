package admission

import (
	"context"
	"fmt"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/controller"
)

type VolumeGetter interface {
	Get(ctx context.Context, namespace, name string) (*v1.Volume, error)
}

// VolumeReplicasPolicy rejects deployments that mount a volume but request
// more than one replica — hostpath volumes are node-local, so only a single
// pod can ever use one at a time.
type VolumeReplicasPolicy struct{}

func NewVolumeReplicasPolicy() *VolumeReplicasPolicy { return &VolumeReplicasPolicy{} }

func (p *VolumeReplicasPolicy) Admit(ctx context.Context, req *Request) error {
	if len(req.Spec.GetVolumes()) == 0 {
		return nil
	}
	if req.Replicas > 1 {
		return fmt.Errorf("volume-backed deployments must have replicas <= 1, got %d", req.Replicas)
	}
	return nil
}

// VolumeOwnershipPolicy rejects a deployment if any volume it mounts is
// already claimed by a different deployment, so a node-local volume can never
// be silently shared/corrupted by two owners.
type VolumeOwnershipPolicy struct {
	volumes VolumeGetter
}

func NewVolumeOwnershipPolicy(volumes VolumeGetter) *VolumeOwnershipPolicy {
	return &VolumeOwnershipPolicy{volumes: volumes}
}

func (p *VolumeOwnershipPolicy) Admit(ctx context.Context, req *Request) error {
	for _, m := range req.Spec.GetVolumes() {
		vol, err := p.volumes.Get(ctx, req.Namespace, m.GetVolumeName())
		if err != nil {
			continue // volume not found yet is fine; nothing to own-check against
		}
		owner := vol.GetMetadata().GetLabels()[controller.OwnerDeploymentLabel]
		if owner != "" && req.Name != "" && owner != req.Name {
			return fmt.Errorf("volume %q is already owned by deployment %q", m.GetVolumeName(), owner)
		}
	}
	return nil
}
