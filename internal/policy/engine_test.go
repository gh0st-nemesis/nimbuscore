package policy

import (
	"testing"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

func TestEngineEvaluatesLabelRequirement(t *testing.T) {
	e, err := NewEngine()
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	prg, err := e.Compile(`"team" in pod.labels`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	withTeam := PodView("default", map[string]string{"team": "payments"}, &v1.PodSpec{})
	ok, err := e.Evaluate(prg, withTeam)
	if err != nil {
		t.Fatalf("Evaluate(withTeam): %v", err)
	}
	if !ok {
		t.Error("expected a pod with a team label to satisfy the policy")
	}

	withoutTeam := PodView("default", map[string]string{}, &v1.PodSpec{})
	ok, err = e.Evaluate(prg, withoutTeam)
	if err != nil {
		t.Fatalf("Evaluate(withoutTeam): %v", err)
	}
	if ok {
		t.Error("expected a pod without a team label to fail the policy")
	}
}

func TestEngineEvaluatesContainerImageRule(t *testing.T) {
	e, err := NewEngine()
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	prg, err := e.Compile(`pod.containers.all(c, c.image.startsWith("registry.internal/"))`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	compliant := PodView("default", nil, &v1.PodSpec{
		Containers: []*v1.Container{{Name: "app", Image: "registry.internal/app:v1"}},
	})
	ok, err := e.Evaluate(prg, compliant)
	if err != nil {
		t.Fatalf("Evaluate(compliant): %v", err)
	}
	if !ok {
		t.Error("expected an internal-registry image to satisfy the policy")
	}

	noncompliant := PodView("default", nil, &v1.PodSpec{
		Containers: []*v1.Container{{Name: "app", Image: "docker.io/library/nginx"}},
	})
	ok, err = e.Evaluate(prg, noncompliant)
	if err != nil {
		t.Fatalf("Evaluate(noncompliant): %v", err)
	}
	if ok {
		t.Error("expected a public-registry image to fail the policy")
	}
}

func TestEngineRejectsInvalidExpression(t *testing.T) {
	e, err := NewEngine()
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if _, err := e.Compile("this is not valid CEL {{{"); err == nil {
		t.Fatal("Compile succeeded for invalid CEL, want error")
	}
}

func TestEngineRejectsNonBoolExpression(t *testing.T) {
	e, err := NewEngine()
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	prg, err := e.Compile(`"not a bool"`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, err := e.Evaluate(prg, PodView("default", nil, &v1.PodSpec{})); err == nil {
		t.Fatal("Evaluate succeeded for a non-bool expression, want error")
	}
}
