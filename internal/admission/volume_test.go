package admission

import (
	"context"
	"testing"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/controller"
)

type fakeVolumeGetter struct {
	volumes map[string]*v1.Volume
}

func (f *fakeVolumeGetter) Get(ctx context.Context, namespace, name string) (*v1.Volume, error) {
	vol, ok := f.volumes[namespace+"/"+name]
	if !ok {
		return nil, context.DeadlineExceeded // any non-nil error signals "not found" to the policy
	}
	return vol, nil
}

func TestVolumeReplicasPolicyRejectsMultipleReplicas(t *testing.T) {
	v := NewVolumeReplicasPolicy()
	req := &Request{
		Namespace: "default",
		Replicas:  2,
		Spec: &v1.PodSpec{
			Volumes: []*v1.VolumeMount{{VolumeName: "data", MountPath: "/data"}},
		},
	}
	if err := v.Admit(context.Background(), req); err == nil {
		t.Fatal("Admit succeeded for volume-backed deployment with replicas=2, want rejection")
	}
}

func TestVolumeReplicasPolicyAllowsSingleReplica(t *testing.T) {
	v := NewVolumeReplicasPolicy()
	req := &Request{
		Namespace: "default",
		Replicas:  1,
		Spec: &v1.PodSpec{
			Volumes: []*v1.VolumeMount{{VolumeName: "data", MountPath: "/data"}},
		},
	}
	if err := v.Admit(context.Background(), req); err != nil {
		t.Fatalf("Admit rejected a single-replica volume-backed deployment: %v", err)
	}
}

func TestVolumeReplicasPolicyIgnoresDeploymentsWithoutVolumes(t *testing.T) {
	v := NewVolumeReplicasPolicy()
	req := &Request{Namespace: "default", Replicas: 5, Spec: &v1.PodSpec{}}
	if err := v.Admit(context.Background(), req); err != nil {
		t.Fatalf("Admit rejected a volume-less deployment: %v", err)
	}
}

func TestVolumeOwnershipPolicyRejectsDifferentOwner(t *testing.T) {
	getter := &fakeVolumeGetter{volumes: map[string]*v1.Volume{
		"default/data": {
			Metadata: &v1.ObjectMeta{Name: "data", Namespace: "default", Labels: map[string]string{
				controller.OwnerDeploymentLabel: "other-app",
			}},
		},
	}}
	v := NewVolumeOwnershipPolicy(getter)
	req := &Request{
		Namespace: "default",
		Name:      "my-app",
		Spec:      &v1.PodSpec{Volumes: []*v1.VolumeMount{{VolumeName: "data", MountPath: "/data"}}},
	}
	if err := v.Admit(context.Background(), req); err == nil {
		t.Fatal("Admit succeeded referencing a volume owned by a different deployment, want rejection")
	}
}

func TestVolumeOwnershipPolicyAllowsSameOwner(t *testing.T) {
	getter := &fakeVolumeGetter{volumes: map[string]*v1.Volume{
		"default/data": {
			Metadata: &v1.ObjectMeta{Name: "data", Namespace: "default", Labels: map[string]string{
				controller.OwnerDeploymentLabel: "my-app",
			}},
		},
	}}
	v := NewVolumeOwnershipPolicy(getter)
	req := &Request{
		Namespace: "default",
		Name:      "my-app",
		Spec:      &v1.PodSpec{Volumes: []*v1.VolumeMount{{VolumeName: "data", MountPath: "/data"}}},
	}
	if err := v.Admit(context.Background(), req); err != nil {
		t.Fatalf("Admit rejected the volume's own owning deployment: %v", err)
	}
}

func TestVolumeOwnershipPolicyAllowsUnclaimedVolume(t *testing.T) {
	v := NewVolumeOwnershipPolicy(&fakeVolumeGetter{volumes: map[string]*v1.Volume{}})
	req := &Request{
		Namespace: "default",
		Name:      "my-app",
		Spec:      &v1.PodSpec{Volumes: []*v1.VolumeMount{{VolumeName: "data", MountPath: "/data"}}},
	}
	if err := v.Admit(context.Background(), req); err != nil {
		t.Fatalf("Admit rejected a reference to a not-yet-created volume: %v", err)
	}
}
