package apiserver_test

import (
	"testing"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/apiserver"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

func TestNamespaceServiceCreateListDelete(t *testing.T) {
	st := store.NewMemStore()
	svc := apiserver.NewNamespaceService(st)
	ctx := t.Context()

	if _, err := svc.CreateNamespace(ctx, &v1.CreateNamespaceRequest{
		Namespace: &v1.Namespace{Metadata: &v1.ObjectMeta{Name: "prod"}},
	}); err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	listResp, err := svc.ListNamespaces(ctx, &v1.ListNamespacesRequest{})
	if err != nil {
		t.Fatalf("ListNamespaces: %v", err)
	}
	if len(listResp.GetItems()) != 1 || listResp.GetItems()[0].GetMetadata().GetName() != "prod" {
		t.Fatalf("items = %+v, want one namespace named prod", listResp.GetItems())
	}

	if _, err := svc.DeleteNamespace(ctx, &v1.DeleteNamespaceRequest{Name: "prod"}); err != nil {
		t.Fatalf("DeleteNamespace: %v", err)
	}
	listResp2, err := svc.ListNamespaces(ctx, &v1.ListNamespacesRequest{})
	if err != nil {
		t.Fatalf("ListNamespaces after delete: %v", err)
	}
	if len(listResp2.GetItems()) != 0 {
		t.Errorf("items after delete = %+v, want none", listResp2.GetItems())
	}
}

func TestNamespaceServiceRejectsDuplicateName(t *testing.T) {
	st := store.NewMemStore()
	svc := apiserver.NewNamespaceService(st)
	ctx := t.Context()

	create := func() error {
		_, err := svc.CreateNamespace(ctx, &v1.CreateNamespaceRequest{
			Namespace: &v1.Namespace{Metadata: &v1.ObjectMeta{Name: "prod"}},
		})
		return err
	}
	if err := create(); err != nil {
		t.Fatalf("first CreateNamespace: %v", err)
	}
	if err := create(); err == nil {
		t.Error("second CreateNamespace with the same name succeeded, want AlreadyExists error")
	}
}

func TestNamespaceServiceRequiresName(t *testing.T) {
	st := store.NewMemStore()
	svc := apiserver.NewNamespaceService(st)

	_, err := svc.CreateNamespace(t.Context(), &v1.CreateNamespaceRequest{
		Namespace: &v1.Namespace{Metadata: &v1.ObjectMeta{}},
	})
	if err == nil {
		t.Error("CreateNamespace with empty name succeeded, want an error")
	}
}
