package apiserver_test

import (
	"testing"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/admission"
	"github.com/gh0st-nemesis/nimbuscore/internal/apiserver"
	"github.com/gh0st-nemesis/nimbuscore/internal/controller"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

func TestDeleteDeploymentCascadeDeletesOwnedPods(t *testing.T) {
	st := store.NewMemStore()
	svc := apiserver.NewDeploymentService(st, admission.NewChain())
	pods := registry.New(st, "pods", func() *v1.Pod { return &v1.Pod{} })

	ctx := t.Context()
	if _, err := svc.CreateDeployment(ctx, &v1.CreateDeploymentRequest{Deployment: &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas: 2,
			Selector: map[string]string{"app": "web"},
			Template: &v1.PodSpec{Containers: []*v1.Container{{Name: "web", Image: "nginx:alpine"}}},
		},
	}}); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	owned := &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: "web-0", Namespace: "default", Labels: map[string]string{controller.OwnerDeploymentLabel: "web"}},
	}
	if err := pods.Put(ctx, "default", "web-0", owned); err != nil {
		t.Fatalf("seed owned pod: %v", err)
	}
	unrelated := &v1.Pod{Metadata: &v1.ObjectMeta{Name: "standalone", Namespace: "default"}}
	if err := pods.Put(ctx, "default", "standalone", unrelated); err != nil {
		t.Fatalf("seed unrelated pod: %v", err)
	}

	if _, err := svc.DeleteDeployment(ctx, &v1.DeleteDeploymentRequest{Namespace: "default", Name: "web"}); err != nil {
		t.Fatalf("DeleteDeployment: %v", err)
	}

	if _, err := pods.Get(ctx, "default", "web-0"); err == nil {
		t.Error("owned pod web-0 still exists after deleting its deployment, want cascade-deleted")
	}
	if _, err := pods.Get(ctx, "default", "standalone"); err != nil {
		t.Errorf("unrelated pod standalone was deleted, want kept: %v", err)
	}
	if _, err := svc.GetDeployment(ctx, &v1.GetDeploymentRequest{Namespace: "default", Name: "web"}); err == nil {
		t.Error("deployment web still exists after DeleteDeployment")
	}
}
