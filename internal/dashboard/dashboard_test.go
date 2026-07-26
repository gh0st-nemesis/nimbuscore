package dashboard_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/admission"
	"github.com/gh0st-nemesis/nimbuscore/internal/agent"
	"github.com/gh0st-nemesis/nimbuscore/internal/apiserver"
	"github.com/gh0st-nemesis/nimbuscore/internal/controller"
	"github.com/gh0st-nemesis/nimbuscore/internal/csi/hostpath"
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

	csiDriver, err := hostpath.New("test-node", t.TempDir())
	if err != nil {
		t.Fatalf("hostpath.New: %v", err)
	}

	handler, err := dashboard.NewHandler(dashboard.Config{
		Nodes:         nodes,
		Pods:          pods,
		DeploymentSvc: apiserver.NewDeploymentService(s, admission.NewChain()),
		Services:      apiserver.NewServiceService(s),
		Namespaces:    apiserver.NewNamespaceService(s),
		Volumes:       apiserver.NewVolumeService(s, csiDriver),
		CostModel:     finops.DefaultCostModel(),
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

func TestDashboardCreateDeploymentViaAPI(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	body := strings.NewReader(`{"name":"web","namespace":"default","image":"nginx:alpine","replicas":2,"port":80}`)
	resp, err := http.Post(srv.URL+"/api/deployments", "application/json", body)
	if err != nil {
		t.Fatalf("POST /api/deployments: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	listResp, err := http.Get(srv.URL + "/api/deployments")
	if err != nil {
		t.Fatalf("GET /api/deployments: %v", err)
	}
	defer listResp.Body.Close()

	var decoded struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Replicas int32 `json:"replicas"`
				Template struct {
					Containers []struct {
						Image string `json:"image"`
					} `json:"containers"`
				} `json:"template"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Items) != 1 {
		t.Fatalf("got %d deployments, want 1", len(decoded.Items))
	}
	if decoded.Items[0].Metadata.Name != "web" {
		t.Errorf("name = %q, want web", decoded.Items[0].Metadata.Name)
	}
	if decoded.Items[0].Spec.Replicas != 2 {
		t.Errorf("replicas = %d, want 2", decoded.Items[0].Spec.Replicas)
	}
	if got := decoded.Items[0].Spec.Template.Containers[0].Image; got != "nginx:alpine" {
		t.Errorf("image = %q, want nginx:alpine", got)
	}
}

func TestDashboardCreateDeploymentWithPortAutoCreatesService(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	body := strings.NewReader(`{"name":"web","namespace":"default","image":"nginx:alpine","replicas":1,"port":80}`)
	resp, err := http.Post(srv.URL+"/api/deployments", "application/json", body)
	if err != nil {
		t.Fatalf("POST /api/deployments: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var envelope struct {
		Service struct {
			Spec struct {
				NodePort int32 `json:"nodePort"`
			} `json:"spec"`
		} `json:"service"`
		ServiceError string `json:"serviceError"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.ServiceError != "" {
		t.Fatalf("serviceError = %q, want none", envelope.ServiceError)
	}
	if envelope.Service.Spec.NodePort < 30000 || envelope.Service.Spec.NodePort > 32767 {
		t.Errorf("service.spec.nodePort = %d, want a value in the NodePort range", envelope.Service.Spec.NodePort)
	}

	svcResp, err := http.Get(srv.URL + "/api/services")
	if err != nil {
		t.Fatalf("GET /api/services: %v", err)
	}
	defer svcResp.Body.Close()
	var svcList struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.NewDecoder(svcResp.Body).Decode(&svcList); err != nil {
		t.Fatalf("decode services: %v", err)
	}
	if len(svcList.Items) != 1 || svcList.Items[0].Metadata.Name != "web" {
		t.Errorf("services = %+v, want one service named web", svcList.Items)
	}
}

func TestDashboardCreateDeploymentWithLinksSetsLinksToLabel(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	body := strings.NewReader(`{"name":"app","namespace":"default","image":"node:21-alpine","replicas":1,"links":["db","cache"]}`)
	resp, err := http.Post(srv.URL+"/api/deployments", "application/json", body)
	if err != nil {
		t.Fatalf("POST /api/deployments: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var envelope struct {
		Deployment struct {
			Metadata struct {
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
		} `json:"deployment"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := envelope.Deployment.Metadata.Labels["nimbuscore.io/links-to"]; got != "db,cache" {
		t.Errorf("nimbuscore.io/links-to label = %q, want %q", got, "db,cache")
	}
}

func TestDashboardCreateDeploymentWithoutLinksOmitsLinksToLabel(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	body := strings.NewReader(`{"name":"solo","namespace":"default","image":"node:21-alpine","replicas":1}`)
	resp, err := http.Post(srv.URL+"/api/deployments", "application/json", body)
	if err != nil {
		t.Fatalf("POST /api/deployments: %v", err)
	}
	defer resp.Body.Close()

	var envelope struct {
		Deployment struct {
			Metadata struct {
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
		} `json:"deployment"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := envelope.Deployment.Metadata.Labels["nimbuscore.io/links-to"]; ok {
		t.Errorf("nimbuscore.io/links-to label present without any links requested: %+v", envelope.Deployment.Metadata.Labels)
	}
}

func TestDashboardCreateDeploymentWithPersistentStorageCreatesVolume(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	body := strings.NewReader(`{"name":"site","namespace":"default","image":"nginx:alpine","replicas":1,"addPersistentStorage":true,"mountPath":"/usr/share/nginx/html"}`)
	resp, err := http.Post(srv.URL+"/api/deployments", "application/json", body)
	if err != nil {
		t.Fatalf("POST /api/deployments: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var envelope struct {
		Deployment struct {
			Spec struct {
				Template struct {
					Volumes []struct {
						VolumeName string `json:"volumeName"`
						MountPath  string `json:"mountPath"`
					} `json:"volumes"`
				} `json:"template"`
			} `json:"spec"`
		} `json:"deployment"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	vols := envelope.Deployment.Spec.Template.Volumes
	if len(vols) != 1 || vols[0].VolumeName != "site" || vols[0].MountPath != "/usr/share/nginx/html" {
		t.Fatalf("deployment.spec.template.volumes = %+v, want one mount for site at /usr/share/nginx/html", vols)
	}
}

func TestDashboardEditDeploymentPreservesExistingPersistentStorage(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	createBody := strings.NewReader(`{"name":"site","namespace":"default","image":"nginx:alpine","replicas":1,"addPersistentStorage":true,"mountPath":"/usr/share/nginx/html"}`)
	createResp, err := http.Post(srv.URL+"/api/deployments", "application/json", createBody)
	if err != nil {
		t.Fatalf("POST /api/deployments (create): %v", err)
	}
	createResp.Body.Close()
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d, want 200", createResp.StatusCode)
	}

	// The edit panel has no persistent-storage field, so a real edit request
	// never sends addPersistentStorage/mountPath — it only edits things like
	// image or env vars, exactly like this.
	editBody := strings.NewReader(`{"name":"site","namespace":"default","image":"nginx:alpine","replicas":1,"env":{"FOO":"bar"}}`)
	editResp, err := http.Post(srv.URL+"/api/deployments", "application/json", editBody)
	if err != nil {
		t.Fatalf("POST /api/deployments (edit): %v", err)
	}
	defer editResp.Body.Close()
	if editResp.StatusCode != http.StatusOK {
		t.Fatalf("edit status = %d, want 200", editResp.StatusCode)
	}

	var envelope struct {
		Deployment struct {
			Spec struct {
				Template struct {
					Volumes []struct {
						VolumeName string `json:"volumeName"`
						MountPath  string `json:"mountPath"`
					} `json:"volumes"`
				} `json:"template"`
			} `json:"spec"`
		} `json:"deployment"`
	}
	if err := json.NewDecoder(editResp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	vols := envelope.Deployment.Spec.Template.Volumes
	if len(vols) != 1 || vols[0].VolumeName != "site" || vols[0].MountPath != "/usr/share/nginx/html" {
		t.Fatalf("after edit, deployment.spec.template.volumes = %+v, want the pre-existing mount for site at /usr/share/nginx/html preserved", vols)
	}
}

func TestDashboardCreateDeploymentRejectsPersistentStorageWithMultipleReplicas(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	body := strings.NewReader(`{"name":"site","namespace":"default","image":"nginx:alpine","replicas":3,"addPersistentStorage":true,"mountPath":"/usr/share/nginx/html"}`)
	resp, err := http.Post(srv.URL+"/api/deployments", "application/json", body)
	if err != nil {
		t.Fatalf("POST /api/deployments: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (volume-backed deployment with replicas=3)", resp.StatusCode)
	}
}

func TestDashboardScaleRejectsVolumeBackedDeploymentAboveOneReplica(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	createBody := strings.NewReader(`{"name":"site","namespace":"default","image":"nginx:alpine","replicas":1,"addPersistentStorage":true,"mountPath":"/usr/share/nginx/html"}`)
	createResp, err := http.Post(srv.URL+"/api/deployments", "application/json", createBody)
	if err != nil {
		t.Fatalf("POST /api/deployments: %v", err)
	}
	createResp.Body.Close()
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d, want 200", createResp.StatusCode)
	}

	scaleBody := strings.NewReader(`{"namespace":"default","name":"site","replicas":2}`)
	req, err := http.NewRequest(http.MethodPatch, srv.URL+"/api/deployments", scaleBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	scaleResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH /api/deployments: %v", err)
	}
	defer scaleResp.Body.Close()
	if scaleResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("scale status = %d, want 400 (volume-backed deployment cannot scale above 1)", scaleResp.StatusCode)
	}
}

func TestDashboardHandleFilesProxiesToClaimedNode(t *testing.T) {
	s := store.NewMemStore()
	nodesReg := registry.New(s, "nodes", func() *v1.Node { return &v1.Node{} })
	podsReg := registry.New(s, "pods", func() *v1.Pod { return &v1.Pod{} })
	volumesReg := registry.New(s, "volumes", func() *v1.Volume { return &v1.Volume{} })

	csiDriver, err := hostpath.New("test-node", t.TempDir())
	if err != nil {
		t.Fatalf("hostpath.New: %v", err)
	}
	volumeSvc := apiserver.NewVolumeService(s, csiDriver)

	// Fake per-node agent files server, standing in for a real nimbus-agent.
	agentSrv := httptest.NewServer(agent.NewFilesHandler(t.TempDir()))
	defer agentSrv.Close()
	agentURL, err := url.Parse(agentSrv.URL)
	if err != nil {
		t.Fatalf("parse agent url: %v", err)
	}
	agentPort, err := strconv.Atoi(agentURL.Port())
	if err != nil {
		t.Fatalf("agent port: %v", err)
	}

	if err := nodesReg.Put(t.Context(), "", "node-1", &v1.Node{
		Metadata: &v1.ObjectMeta{Name: "node-1"},
		Status:   &v1.NodeStatus{Ready: true, InternalIp: "127.0.0.1"},
	}); err != nil {
		t.Fatalf("seed node: %v", err)
	}

	if _, err := volumeSvc.CreateVolume(t.Context(), &v1.CreateVolumeRequest{
		Volume: &v1.Volume{Metadata: &v1.ObjectMeta{Name: "data", Namespace: "default"}, Spec: &v1.VolumeSpec{RequestedBytes: 1 << 20}},
	}); err != nil {
		t.Fatalf("create volume: %v", err)
	}
	vol, err := volumesReg.Get(t.Context(), "default", "data")
	if err != nil {
		t.Fatalf("get volume: %v", err)
	}
	vol.Status.NodeName = "node-1"
	if err := volumesReg.Put(t.Context(), "default", "data", vol); err != nil {
		t.Fatalf("claim volume: %v", err)
	}

	handler, err := dashboard.NewHandler(dashboard.Config{
		Nodes:          nodesReg,
		Pods:           podsReg,
		DeploymentSvc:  apiserver.NewDeploymentService(s, admission.NewChain()),
		Services:       apiserver.NewServiceService(s),
		Volumes:        volumeSvc,
		CostModel:      finops.DefaultCostModel(),
		AgentFilesPort: agentPort,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	writeReq, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/files?namespace=default&name=data&path=index.html", strings.NewReader("hello from files tab"))
	writeResp, err := http.DefaultClient.Do(writeReq)
	if err != nil {
		t.Fatalf("write via dashboard proxy: %v", err)
	}
	if writeResp.StatusCode != http.StatusNoContent {
		t.Fatalf("write status = %d, want 204", writeResp.StatusCode)
	}

	readResp, err := http.Get(srv.URL + "/api/files?op=read&namespace=default&name=data&path=index.html")
	if err != nil {
		t.Fatalf("read via dashboard proxy: %v", err)
	}
	defer readResp.Body.Close()
	content, err := io.ReadAll(readResp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if string(content) != "hello from files tab" {
		t.Errorf("read content = %q, want %q", string(content), "hello from files tab")
	}
}

func TestDashboardHandleFilesRejectsUnclaimedVolume(t *testing.T) {
	s := store.NewMemStore()
	nodesReg := registry.New(s, "nodes", func() *v1.Node { return &v1.Node{} })
	podsReg := registry.New(s, "pods", func() *v1.Pod { return &v1.Pod{} })

	csiDriver, err := hostpath.New("test-node", t.TempDir())
	if err != nil {
		t.Fatalf("hostpath.New: %v", err)
	}
	volumeSvc := apiserver.NewVolumeService(s, csiDriver)

	if _, err := volumeSvc.CreateVolume(t.Context(), &v1.CreateVolumeRequest{
		Volume: &v1.Volume{Metadata: &v1.ObjectMeta{Name: "data", Namespace: "default"}, Spec: &v1.VolumeSpec{RequestedBytes: 1 << 20}},
	}); err != nil {
		t.Fatalf("create volume: %v", err)
	}

	handler, err := dashboard.NewHandler(dashboard.Config{
		Nodes:         nodesReg,
		Pods:          podsReg,
		DeploymentSvc: apiserver.NewDeploymentService(s, admission.NewChain()),
		Services:      apiserver.NewServiceService(s),
		Volumes:       volumeSvc,
		CostModel:     finops.DefaultCostModel(),
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/files?namespace=default&name=data")
	if err != nil {
		t.Fatalf("GET /api/files: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (volume not yet claimed by any node)", resp.StatusCode)
	}
}

func TestDashboardCreateAndListNamespaces(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	body := strings.NewReader(`{"name":"prod"}`)
	resp, err := http.Post(srv.URL+"/api/namespaces", "application/json", body)
	if err != nil {
		t.Fatalf("POST /api/namespaces: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	listResp, err := http.Get(srv.URL + "/api/namespaces")
	if err != nil {
		t.Fatalf("GET /api/namespaces: %v", err)
	}
	defer listResp.Body.Close()
	var decoded struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Items) != 1 || decoded.Items[0].Metadata.Name != "prod" {
		t.Fatalf("items = %+v, want one namespace named prod", decoded.Items)
	}
}

func TestDashboardDeleteNamespace(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	create := strings.NewReader(`{"name":"staging"}`)
	if _, err := http.Post(srv.URL+"/api/namespaces", "application/json", create); err != nil {
		t.Fatalf("POST /api/namespaces: %v", err)
	}

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/namespaces?name=staging", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/namespaces: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	listResp, err := http.Get(srv.URL + "/api/namespaces")
	if err != nil {
		t.Fatalf("GET /api/namespaces: %v", err)
	}
	defer listResp.Body.Close()
	var decoded struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Items) != 0 {
		t.Errorf("got %d namespaces after delete, want 0", len(decoded.Items))
	}
}

func TestDashboardCreateDeploymentFromGitRepo(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	body := strings.NewReader(`{"name":"web","gitRepoUrl":"https://example.com/user/repo.git","gitBranch":"dev","gitDockerfilePath":"docker/Dockerfile","gitContextPath":"app"}`)
	resp, err := http.Post(srv.URL+"/api/deployments", "application/json", body)
	if err != nil {
		t.Fatalf("POST /api/deployments: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	listResp, err := http.Get(srv.URL + "/api/deployments")
	if err != nil {
		t.Fatalf("GET /api/deployments: %v", err)
	}
	defer listResp.Body.Close()
	var decoded struct {
		Items []struct {
			Spec struct {
				Template struct {
					Containers []struct {
						Image       string `json:"image"`
						BuildSource struct {
							RepoUrl        string `json:"repoUrl"`
							Branch         string `json:"branch"`
							DockerfilePath string `json:"dockerfilePath"`
							ContextPath    string `json:"contextPath"`
						} `json:"buildSource"`
					} `json:"containers"`
				} `json:"template"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Items) != 1 {
		t.Fatalf("got %d deployments, want 1", len(decoded.Items))
	}
	c := decoded.Items[0].Spec.Template.Containers[0]
	if c.Image != "" {
		t.Errorf("image = %q, want empty for a Git-sourced deployment", c.Image)
	}
	if c.BuildSource.RepoUrl != "https://example.com/user/repo.git" {
		t.Errorf("buildSource.repoUrl = %q", c.BuildSource.RepoUrl)
	}
	if c.BuildSource.Branch != "dev" {
		t.Errorf("buildSource.branch = %q, want dev", c.BuildSource.Branch)
	}
	if c.BuildSource.DockerfilePath != "docker/Dockerfile" {
		t.Errorf("buildSource.dockerfilePath = %q", c.BuildSource.DockerfilePath)
	}
	if c.BuildSource.ContextPath != "app" {
		t.Errorf("buildSource.contextPath = %q", c.BuildSource.ContextPath)
	}
}

func TestDashboardCreateDeploymentRequiresImageOrGitRepo(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	body := strings.NewReader(`{"name":"web"}`)
	resp, err := http.Post(srv.URL+"/api/deployments", "application/json", body)
	if err != nil {
		t.Fatalf("POST /api/deployments: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestDashboardCreateDeploymentWithEnvVars(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	body := strings.NewReader(`{"name":"web","image":"nginx:alpine","env":{"FOO":"bar","API_KEY":"secret"}}`)
	resp, err := http.Post(srv.URL+"/api/deployments", "application/json", body)
	if err != nil {
		t.Fatalf("POST /api/deployments: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	listResp, err := http.Get(srv.URL + "/api/deployments")
	if err != nil {
		t.Fatalf("GET /api/deployments: %v", err)
	}
	defer listResp.Body.Close()
	var decoded struct {
		Items []struct {
			Spec struct {
				Template struct {
					Containers []struct {
						Env map[string]string `json:"env"`
					} `json:"containers"`
				} `json:"template"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Items) != 1 {
		t.Fatalf("got %d deployments, want 1", len(decoded.Items))
	}
	env := decoded.Items[0].Spec.Template.Containers[0].Env
	if env["FOO"] != "bar" || env["API_KEY"] != "secret" {
		t.Errorf("env = %+v, want FOO=bar, API_KEY=secret", env)
	}
}

func TestDashboardEditDeploymentUpdatesEnvVars(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	create := strings.NewReader(`{"name":"web","image":"nginx:alpine","env":{"FOO":"old"}}`)
	if _, err := http.Post(srv.URL+"/api/deployments", "application/json", create); err != nil {
		t.Fatalf("POST create: %v", err)
	}

	update := strings.NewReader(`{"name":"web","namespace":"default","image":"nginx:alpine","env":{"FOO":"new","BAR":"added"}}`)
	resp, err := http.Post(srv.URL+"/api/deployments", "application/json", update)
	if err != nil {
		t.Fatalf("POST update: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	listResp, err := http.Get(srv.URL + "/api/deployments")
	if err != nil {
		t.Fatalf("GET /api/deployments: %v", err)
	}
	defer listResp.Body.Close()
	var decoded struct {
		Items []struct {
			Spec struct {
				Template struct {
					Containers []struct {
						Env map[string]string `json:"env"`
					} `json:"containers"`
				} `json:"template"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Items) != 1 {
		t.Fatalf("got %d deployments after update, want 1 (upsert, not a duplicate)", len(decoded.Items))
	}
	env := decoded.Items[0].Spec.Template.Containers[0].Env
	if env["FOO"] != "new" || env["BAR"] != "added" {
		t.Errorf("env after update = %+v, want FOO=new, BAR=added", env)
	}
}

func TestDashboardEditDeploymentRestartsOwnedPods(t *testing.T) {
	handler, pods, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	create := strings.NewReader(`{"name":"web","image":"nginx:alpine"}`)
	if _, err := http.Post(srv.URL+"/api/deployments", "application/json", create); err != nil {
		t.Fatalf("POST create: %v", err)
	}

	if err := pods.Put(t.Context(), "default", "web-0", &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: "web-0", Namespace: "default", Labels: map[string]string{controller.OwnerDeploymentLabel: "web"}},
	}); err != nil {
		t.Fatalf("seed pod: %v", err)
	}

	update := strings.NewReader(`{"name":"web","namespace":"default","image":"nginx:alpine","env":{"FOO":"bar"}}`)
	if _, err := http.Post(srv.URL+"/api/deployments", "application/json", update); err != nil {
		t.Fatalf("POST update: %v", err)
	}

	if _, err := pods.Get(t.Context(), "default", "web-0"); err == nil {
		t.Error("owned pod still exists after editing the deployment, want it deleted so the reconciler recreates it with the updated spec")
	}
}

func TestDashboardCreateDeploymentDoesNotRestartUnrelatedPods(t *testing.T) {
	handler, pods, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	if err := pods.Put(t.Context(), "default", "standalone", &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: "standalone", Namespace: "default"},
	}); err != nil {
		t.Fatalf("seed pod: %v", err)
	}

	create := strings.NewReader(`{"name":"web","image":"nginx:alpine"}`)
	if _, err := http.Post(srv.URL+"/api/deployments", "application/json", create); err != nil {
		t.Fatalf("POST create: %v", err)
	}

	if _, err := pods.Get(t.Context(), "default", "standalone"); err != nil {
		t.Errorf("unrelated pod was removed by an unrelated deployment create: %v", err)
	}
}

func TestDashboardDeletePodRemovesIt(t *testing.T) {
	handler, pods, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	if err := pods.Put(t.Context(), "default", "standalone", &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: "standalone", Namespace: "default"},
		Spec:     &v1.PodSpec{Containers: []*v1.Container{{Name: "standalone", Image: "alpine"}}},
	}); err != nil {
		t.Fatalf("seed pod: %v", err)
	}

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/pods?namespace=default&name=standalone", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/pods: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	remaining, err := pods.List(t.Context(), "default")
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("pods = %+v, want none remaining", remaining)
	}
}

func TestDashboardDeletePodRequiresName(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/pods?namespace=default", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/pods: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestDashboardDeleteDeploymentRemovesIt(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	body := strings.NewReader(`{"name":"web","image":"nginx:alpine"}`)
	if _, err := http.Post(srv.URL+"/api/deployments", "application/json", body); err != nil {
		t.Fatalf("POST /api/deployments: %v", err)
	}

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/deployments?namespace=default&name=web", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/deployments: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	listResp, err := http.Get(srv.URL + "/api/deployments")
	if err != nil {
		t.Fatalf("GET /api/deployments: %v", err)
	}
	defer listResp.Body.Close()
	var decoded struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Items) != 0 {
		t.Errorf("got %d deployments after delete, want 0", len(decoded.Items))
	}
}

func TestDashboardScaleDeploymentUpdatesReplicas(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	body := strings.NewReader(`{"name":"web","image":"nginx:alpine","replicas":3}`)
	if _, err := http.Post(srv.URL+"/api/deployments", "application/json", body); err != nil {
		t.Fatalf("POST /api/deployments: %v", err)
	}

	patchBody := strings.NewReader(`{"namespace":"default","name":"web","replicas":0}`)
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/deployments", patchBody)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH /api/deployments: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	listResp, err := http.Get(srv.URL + "/api/deployments")
	if err != nil {
		t.Fatalf("GET /api/deployments: %v", err)
	}
	defer listResp.Body.Close()
	var decoded struct {
		Items []struct {
			Spec struct {
				Replicas int32 `json:"replicas"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Items) != 1 || decoded.Items[0].Spec.Replicas != 0 {
		t.Errorf("deployments = %+v, want one deployment with replicas=0", decoded.Items)
	}
}

func TestDashboardCreateDeploymentRejectsMissingImage(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	body := strings.NewReader(`{"name":"web"}`)
	resp, err := http.Post(srv.URL+"/api/deployments", "application/json", body)
	if err != nil {
		t.Fatalf("POST /api/deployments: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestDashboardCICDReportsManualSourceWithoutGitOps(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	body := strings.NewReader(`{"name":"web","image":"nginx:alpine"}`)
	if _, err := http.Post(srv.URL+"/api/deployments", "application/json", body); err != nil {
		t.Fatalf("POST /api/deployments: %v", err)
	}

	resp, err := http.Get(srv.URL + "/api/cicd")
	if err != nil {
		t.Fatalf("GET /api/cicd: %v", err)
	}
	defer resp.Body.Close()

	var decoded struct {
		GitOps struct {
			Configured bool `json:"configured"`
		} `json:"gitops"`
		Deployments []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"deployments"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.GitOps.Configured {
		t.Error("gitops.configured = true, want false (no GitOps reconciler wired in this test)")
	}
	if len(decoded.Deployments) != 1 || decoded.Deployments[0].Source != "manual" {
		t.Errorf("deployments = %+v, want one entry with source=manual", decoded.Deployments)
	}
}

func TestDashboardMetricsAggregatesNodeAndPodUsage(t *testing.T) {
	handler, pods, nodes := newTestHandler(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	ctx := t.Context()
	if err := nodes.Put(ctx, "", "worker-1", &v1.Node{
		Metadata: &v1.ObjectMeta{Name: "worker-1"},
		Status: &v1.NodeStatus{
			Ready:       true,
			Allocatable: &v1.ResourceList{CpuMillis: 2000, MemoryBytes: 4 << 30},
		},
	}); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if err := pods.Put(ctx, "default", "web-0", &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: "web-0", Namespace: "default"},
		Spec: &v1.PodSpec{
			NodeName:   "worker-1",
			Containers: []*v1.Container{{Name: "web", Resources: &v1.ResourceRequirements{Requests: &v1.ResourceList{CpuMillis: 500, MemoryBytes: 1 << 30}}}},
		},
		Status: &v1.PodStatus{MemoryUsageBytes: 100 << 20},
	}); err != nil {
		t.Fatalf("seed pod: %v", err)
	}

	resp, err := http.Get(srv.URL + "/api/metrics")
	if err != nil {
		t.Fatalf("GET /api/metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var decoded struct {
		CPUAllocatableMillis   int64 `json:"cpuAllocatableMillis"`
		CPURequestedMillis     int64 `json:"cpuRequestedMillis"`
		MemoryAllocatableBytes int64 `json:"memoryAllocatableBytes"`
		MemoryRequestedBytes   int64 `json:"memoryRequestedBytes"`
		MemoryUsedBytes        int64 `json:"memoryUsedBytes"`
		Nodes                  []struct {
			Name               string `json:"name"`
			CPURequestedMillis int64  `json:"cpuRequestedMillis"`
			MemoryUsedBytes    int64  `json:"memoryUsedBytes"`
		} `json:"nodes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if decoded.CPUAllocatableMillis != 2000 {
		t.Errorf("cpuAllocatableMillis = %d, want 2000", decoded.CPUAllocatableMillis)
	}
	if decoded.CPURequestedMillis != 500 {
		t.Errorf("cpuRequestedMillis = %d, want 500", decoded.CPURequestedMillis)
	}
	if decoded.MemoryAllocatableBytes != int64(4)<<30 {
		t.Errorf("memoryAllocatableBytes = %d, want %d", decoded.MemoryAllocatableBytes, int64(4)<<30)
	}
	if decoded.MemoryRequestedBytes != int64(1)<<30 {
		t.Errorf("memoryRequestedBytes = %d, want %d", decoded.MemoryRequestedBytes, int64(1)<<30)
	}
	if decoded.MemoryUsedBytes != int64(100)<<20 {
		t.Errorf("memoryUsedBytes = %d, want %d", decoded.MemoryUsedBytes, int64(100)<<20)
	}
	if len(decoded.Nodes) != 1 || decoded.Nodes[0].Name != "worker-1" {
		t.Fatalf("nodes = %+v, want one node named worker-1", decoded.Nodes)
	}
	if decoded.Nodes[0].CPURequestedMillis != 500 {
		t.Errorf("node cpuRequestedMillis = %d, want 500", decoded.Nodes[0].CPURequestedMillis)
	}
	if decoded.Nodes[0].MemoryUsedBytes != 100<<20 {
		t.Errorf("node memoryUsedBytes = %d, want %d", decoded.Nodes[0].MemoryUsedBytes, 100<<20)
	}
}

func newTestHandlerWithAuth(t *testing.T, username, password string) *httptest.Server {
	t.Helper()
	s := store.NewMemStore()
	nodes := registry.New(s, "nodes", func() *v1.Node { return &v1.Node{} })
	pods := registry.New(s, "pods", func() *v1.Pod { return &v1.Pod{} })

	handler, err := dashboard.NewHandler(dashboard.Config{
		Nodes:         nodes,
		Pods:          pods,
		DeploymentSvc: apiserver.NewDeploymentService(s, admission.NewChain()),
		Services:      apiserver.NewServiceService(s),
		CostModel:     finops.DefaultCostModel(),
		Username:      username,
		Password:      password,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return httptest.NewServer(handler)
}

func TestDashboardRequiresSessionWhenPasswordSet(t *testing.T) {
	srv := newTestHandlerWithAuth(t, "admin", "s3cret")
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/nodes")
	if err != nil {
		t.Fatalf("GET without session: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status without session = %d, want 401", resp.StatusCode)
	}
}

func TestDashboardRootRedirectsToLoginWithoutSession(t *testing.T) {
	srv := newTestHandlerWithAuth(t, "admin", "s3cret")
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302 redirect to /login", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login" {
		t.Errorf("redirect location = %q, want /login", loc)
	}
}

func TestDashboardLoginRejectsWrongCredentials(t *testing.T) {
	srv := newTestHandlerWithAuth(t, "admin", "s3cret")
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"wrong"}`))
	if err != nil {
		t.Fatalf("POST /api/login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status with wrong password = %d, want 401", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "nimbus_session" {
			t.Fatal("a session cookie was set despite wrong credentials")
		}
	}
}

func TestDashboardLoginGrantsSessionAndLogoutRevokesIt(t *testing.T) {
	srv := newTestHandlerWithAuth(t, "admin", "s3cret")
	defer srv.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &http.Client{Jar: jar}

	loginResp, err := client.Post(srv.URL+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"s3cret"}`))
	if err != nil {
		t.Fatalf("POST /api/login: %v", err)
	}
	loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusNoContent {
		t.Fatalf("login status = %d, want 204", loginResp.StatusCode)
	}

	apiResp, err := client.Get(srv.URL + "/api/nodes")
	if err != nil {
		t.Fatalf("GET /api/nodes with session: %v", err)
	}
	apiResp.Body.Close()
	if apiResp.StatusCode != http.StatusOK {
		t.Fatalf("status with session cookie = %d, want 200", apiResp.StatusCode)
	}

	logoutResp, err := client.Post(srv.URL+"/api/logout", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/logout: %v", err)
	}
	logoutResp.Body.Close()
	if logoutResp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", logoutResp.StatusCode)
	}

	afterLogout, err := client.Get(srv.URL + "/api/nodes")
	if err != nil {
		t.Fatalf("GET /api/nodes after logout: %v", err)
	}
	afterLogout.Body.Close()
	if afterLogout.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status after logout = %d, want 401", afterLogout.StatusCode)
	}
}

func TestDashboardLoginPageAndAssetsAreReachableWithoutSession(t *testing.T) {
	srv := newTestHandlerWithAuth(t, "admin", "s3cret")
	defer srv.Close()

	for _, path := range []string{
		"/login", "/style.css", "/login.js", "/fonts/InterVariable.woff2", "/particles.js",
		"/vendor/three.module.min.js", "/vendor/three.core.min.js",
	} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200 (must be reachable without a session)", path, resp.StatusCode)
		}
	}
}
