package admission

import (
	"context"
	"testing"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/policy"
)

type fakePolicyLister struct {
	policies []*v1.Policy
}

func (f *fakePolicyLister) List(context.Context, string) ([]*v1.Policy, error) {
	return f.policies, nil
}

func TestPolicyValidatorRejectsMissingRequiredLabel(t *testing.T) {
	engine, err := policy.NewEngine()
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	lister := &fakePolicyLister{policies: []*v1.Policy{{
		Metadata: &v1.ObjectMeta{Name: "require-team-label"},
		Spec: &v1.PolicySpec{
			Expression: `"team" in pod.labels`,
			Message:    "every pod must carry a team label",
		},
	}}}
	v := NewPolicyValidator(engine, lister)

	withoutLabel := &Request{Namespace: "default", Spec: &v1.PodSpec{Containers: []*v1.Container{{Name: "app", Image: "nginx"}}}}
	if err := v.Admit(context.Background(), withoutLabel); err == nil {
		t.Fatal("Admit succeeded without the required label, want rejection")
	}

	withLabel := &Request{
		Namespace: "default",
		Labels:    map[string]string{"team": "payments"},
		Spec:      &v1.PodSpec{Containers: []*v1.Container{{Name: "app", Image: "nginx"}}},
	}
	if err := v.Admit(context.Background(), withLabel); err != nil {
		t.Fatalf("Admit rejected a compliant pod: %v", err)
	}
}

func TestPolicyValidatorNoPoliciesAlwaysAdmits(t *testing.T) {
	engine, err := policy.NewEngine()
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	v := NewPolicyValidator(engine, &fakePolicyLister{})

	req := &Request{Namespace: "default", Spec: &v1.PodSpec{Containers: []*v1.Container{{Name: "app", Image: "nginx"}}}}
	if err := v.Admit(context.Background(), req); err != nil {
		t.Fatalf("Admit rejected with zero policies configured: %v", err)
	}
}

func TestPolicyValidatorRejectsDisallowedRegistry(t *testing.T) {
	engine, err := policy.NewEngine()
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	lister := &fakePolicyLister{policies: []*v1.Policy{{
		Metadata: &v1.ObjectMeta{Name: "internal-registry-only"},
		Spec: &v1.PolicySpec{
			Expression: `pod.containers.all(c, c.image.startsWith("registry.internal/"))`,
		},
	}}}
	v := NewPolicyValidator(engine, lister)

	req := &Request{Namespace: "default", Spec: &v1.PodSpec{Containers: []*v1.Container{{Name: "app", Image: "docker.io/library/nginx"}}}}
	if err := v.Admit(context.Background(), req); err == nil {
		t.Fatal("Admit succeeded for a non-internal image, want rejection")
	}
}

func TestPolicyValidatorSurfacesInvalidExpressionAsError(t *testing.T) {
	engine, err := policy.NewEngine()
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	lister := &fakePolicyLister{policies: []*v1.Policy{{
		Metadata: &v1.ObjectMeta{Name: "broken"},
		Spec:     &v1.PolicySpec{Expression: "not valid cel {{{"},
	}}}
	v := NewPolicyValidator(engine, lister)

	req := &Request{Namespace: "default", Spec: &v1.PodSpec{Containers: []*v1.Container{{Name: "app", Image: "nginx"}}}}
	if err := v.Admit(context.Background(), req); err == nil {
		t.Fatal("Admit succeeded despite a broken policy expression, want error")
	}
}
