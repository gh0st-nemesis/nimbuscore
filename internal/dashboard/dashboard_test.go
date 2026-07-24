package dashboard_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/apiserver"
	"github.com/gh0st-nemesis/nimbuscore/internal/dashboard"
	"github.com/gh0st-nemesis/nimbuscore/internal/finops"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

func newTestHandler(t *testing.T) (http.Handler, *registry.Registry[*v1.Pod], *registry.Registry[*v1.Node]) {
	t.Helper()
	s := store.NewMemStore()
	nodes := registry.New(s, "nodes", func() *v1.Node { return &v1.Node{} })
	pods := registry.New(s, "pods", func() *v1.Pod { return &v1.Pod{} })
	deployments := registry.New(s, "deployments", func() *v1.Deployment { return &v1.Deployment{} })

	handler, err := dashboard.NewHandler(dashboard.Config{
		Nodes:       nodes,
		Pods:        pods,
		Deployments: deployments,
		Services:    apiserver.NewServiceService(s),
		CostModel:   finops.DefaultCostModel(),
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return handler, pods, nodes
}

func TestDashboardServesIndexPage(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && ct != "text/html; charset=utf-8" {
		t.Errorf("content-type = %q, want text/html", ct)
	}
}

func TestDashboardAPINodesReflectsRegistry(t *testing.T) {
	handler, _, nodes := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	if err := nodes.Put(t.Context(), "", "worker-1", &v1.Node{
		Metadata: &v1.ObjectMeta{Name: "worker-1"},
		Status:   &v1.NodeStatus{Ready: true, InternalIp: "10.0.0.5"},
	}); err != nil {
		t.Fatalf("seed node: %v", err)
	}

	resp, err := http.Get(srv.URL + "/api/nodes")
	if err != nil {
		t.Fatalf("GET /api/nodes: %v", err)
	}
	defer resp.Body.Close()

	var decoded struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Ready      bool   `json:"ready"`
				InternalIP string `json:"internalIp"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Items) != 1 {
		t.Fatalf("got %d nodes, want 1", len(decoded.Items))
	}
	if decoded.Items[0].Metadata.Name != "worker-1" {
		t.Errorf("name = %q, want worker-1", decoded.Items[0].Metadata.Name)
	}
	if !decoded.Items[0].Status.Ready {
		t.Error("ready = false, want true")
	}
	if decoded.Items[0].Status.InternalIP != "10.0.0.5" {
		t.Errorf("internal_ip = %q, want 10.0.0.5", decoded.Items[0].Status.InternalIP)
	}
}

func TestDashboardAPIFinopsReflectsPodCost(t *testing.T) {
	handler, pods, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	if err := pods.Put(t.Context(), "default", "web-0", &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: "web-0", Namespace: "default"},
	}); err != nil {
		t.Fatalf("seed pod: %v", err)
	}

	resp, err := http.Get(srv.URL + "/api/finops")
	if err != nil {
		t.Fatalf("GET /api/finops: %v", err)
	}
	defer resp.Body.Close()

	var report finops.Report
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if report.Total < 0 {
		t.Errorf("total = %v, want >= 0", report.Total)
	}
}

func TestDashboardRequiresBasicAuthWhenPasswordSet(t *testing.T) {
	s := store.NewMemStore()
	nodes := registry.New(s, "nodes", func() *v1.Node { return &v1.Node{} })
	pods := registry.New(s, "pods", func() *v1.Pod { return &v1.Pod{} })
	deployments := registry.New(s, "deployments", func() *v1.Deployment { return &v1.Deployment{} })

	handler, err := dashboard.NewHandler(dashboard.Config{
		Nodes:       nodes,
		Pods:        pods,
		Deployments: deployments,
		Services:    apiserver.NewServiceService(s),
		CostModel:   finops.DefaultCostModel(),
		Username:    "admin",
		Password:    "s3cret",
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/nodes")
	if err != nil {
		t.Fatalf("GET without credentials: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status without credentials = %d, want 401", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/nodes", nil)
	req.SetBasicAuth("admin", "s3cret")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET with credentials: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status with correct credentials = %d, want 200", resp2.StatusCode)
	}

	req3, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/nodes", nil)
	req3.SetBasicAuth("admin", "wrong")
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatalf("GET with wrong password: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status with wrong password = %d, want 401", resp3.StatusCode)
	}
}
