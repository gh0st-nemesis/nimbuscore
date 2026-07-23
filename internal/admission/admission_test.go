package admission

import (
	"context"
	"errors"
	"testing"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

var errNotSigned = errors.New("image is not signed")

type fakeVerifier struct {
	signed map[string]bool
}

func (f *fakeVerifier) Verify(_ context.Context, imageRef string) error {
	if !f.signed[imageRef] {
		return errNotSigned
	}
	return nil
}

func TestSecurityContextPolicyRejectsPrivileged(t *testing.T) {
	p := NewSecurityContextPolicy()
	req := &Request{
		Namespace: "default",
		Spec: &v1.PodSpec{
			Containers: []*v1.Container{{
				Name:            "app",
				Image:           "nginx",
				SecurityContext: &v1.SecurityContext{Privileged: true},
			}},
		},
	}
	if err := p.Admit(context.Background(), req); err == nil {
		t.Fatal("Admit succeeded for a privileged container, want error")
	}
}

func TestSecurityContextPolicyRejectsDisallowedCapability(t *testing.T) {
	p := NewSecurityContextPolicy("NET_BIND_SERVICE")
	req := &Request{
		Namespace: "default",
		Spec: &v1.PodSpec{
			Containers: []*v1.Container{{
				Name:            "app",
				Image:           "nginx",
				SecurityContext: &v1.SecurityContext{AddCapabilities: []string{"SYS_ADMIN"}},
			}},
		},
	}
	if err := p.Admit(context.Background(), req); err == nil {
		t.Fatal("Admit succeeded for a disallowed capability, want error")
	}
}

func TestSecurityContextPolicyAllowsAllowlistedCapability(t *testing.T) {
	p := NewSecurityContextPolicy("NET_BIND_SERVICE")
	req := &Request{
		Namespace: "default",
		Spec: &v1.PodSpec{
			Containers: []*v1.Container{{
				Name:            "app",
				Image:           "nginx",
				SecurityContext: &v1.SecurityContext{AddCapabilities: []string{"NET_BIND_SERVICE"}},
			}},
		},
	}
	if err := p.Admit(context.Background(), req); err != nil {
		t.Fatalf("Admit rejected an allowlisted capability: %v", err)
	}
}

func TestSecurityContextPolicyDefaultsSeccompProfile(t *testing.T) {
	p := NewSecurityContextPolicy()
	container := &v1.Container{Name: "app", Image: "nginx"}
	req := &Request{Namespace: "default", Spec: &v1.PodSpec{Containers: []*v1.Container{container}}}

	if err := p.Admit(context.Background(), req); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if container.GetSecurityContext().GetSeccompProfile() != DefaultSeccompProfile {
		t.Errorf("seccomp_profile = %q, want %q", container.GetSecurityContext().GetSeccompProfile(), DefaultSeccompProfile)
	}
}

func TestImageSignaturePolicyRejectsUnsignedImage(t *testing.T) {
	p := NewImageSignaturePolicy(&fakeVerifier{signed: map[string]bool{"nginx:signed": true}})

	signed := &Request{Namespace: "default", Spec: &v1.PodSpec{Containers: []*v1.Container{{Name: "app", Image: "nginx:signed"}}}}
	if err := p.Admit(context.Background(), signed); err != nil {
		t.Errorf("Admit(signed) = %v, want nil", err)
	}

	unsigned := &Request{Namespace: "default", Spec: &v1.PodSpec{Containers: []*v1.Container{{Name: "app", Image: "nginx:latest"}}}}
	if err := p.Admit(context.Background(), unsigned); err == nil {
		t.Error("Admit(unsigned) succeeded, want error")
	}
}

func TestQuotaPolicyRejectsOverQuota(t *testing.T) {
	s := store.NewMemStore()
	pods := registry.New(s, "pods", func() *v1.Pod { return &v1.Pod{} })
	ctx := context.Background()

	existing := &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: "existing", Namespace: "default"},
		Spec: &v1.PodSpec{Containers: []*v1.Container{{
			Name:      "app",
			Resources: &v1.ResourceRequirements{Requests: &v1.ResourceList{CpuMillis: 800, MemoryBytes: 800 << 20}},
		}}},
	}
	if err := pods.Put(ctx, "default", "existing", existing); err != nil {
		t.Fatalf("seed pod: %v", err)
	}

	quota := &v1.ResourceList{CpuMillis: 1000, MemoryBytes: 1 << 30}
	p := NewQuotaPolicy(pods, quota)

	req := &Request{
		Namespace: "default",
		Replicas:  1,
		Spec: &v1.PodSpec{Containers: []*v1.Container{{
			Name:      "app",
			Resources: &v1.ResourceRequirements{Requests: &v1.ResourceList{CpuMillis: 500}},
		}}},
	}
	if err := p.Admit(ctx, req); err == nil {
		t.Fatal("Admit succeeded despite exceeding the CPU quota, want error")
	}
}

func TestQuotaPolicyAllowsWithinQuota(t *testing.T) {
	s := store.NewMemStore()
	pods := registry.New(s, "pods", func() *v1.Pod { return &v1.Pod{} })
	ctx := context.Background()

	quota := &v1.ResourceList{CpuMillis: 1000, MemoryBytes: 1 << 30}
	p := NewQuotaPolicy(pods, quota)

	req := &Request{
		Namespace: "default",
		Replicas:  1,
		Spec: &v1.PodSpec{Containers: []*v1.Container{{
			Name:      "app",
			Resources: &v1.ResourceRequirements{Requests: &v1.ResourceList{CpuMillis: 500}},
		}}},
	}
	if err := p.Admit(ctx, req); err != nil {
		t.Fatalf("Admit rejected a request within quota: %v", err)
	}
}

func TestQuotaPolicyAccountsForReplicas(t *testing.T) {
	s := store.NewMemStore()
	pods := registry.New(s, "pods", func() *v1.Pod { return &v1.Pod{} })
	ctx := context.Background()

	quota := &v1.ResourceList{CpuMillis: 1000}
	p := NewQuotaPolicy(pods, quota)

	req := &Request{
		Namespace: "default",
		Replicas:  5,
		Spec: &v1.PodSpec{Containers: []*v1.Container{{
			Name:      "app",
			Resources: &v1.ResourceRequirements{Requests: &v1.ResourceList{CpuMillis: 300}},
		}}},
	}
	if err := p.Admit(ctx, req); err == nil {
		t.Fatal("Admit succeeded for 5 replicas x 300 millicores against a 1000 millicore quota, want error")
	}
}

func TestChainStopsAtFirstRejection(t *testing.T) {
	chain := NewChain(
		NewSecurityContextPolicy(),
		NewImageSignaturePolicy(&fakeVerifier{signed: map[string]bool{}}),
	)

	req := &Request{
		Namespace: "default",
		Spec: &v1.PodSpec{Containers: []*v1.Container{{
			Name:            "app",
			Image:           "nginx",
			SecurityContext: &v1.SecurityContext{Privileged: true},
		}}},
	}
	if err := chain.Admit(context.Background(), req); err == nil {
		t.Fatal("Admit succeeded, want rejection from the security-context policy")
	}
}
