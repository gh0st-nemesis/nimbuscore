package dashboard

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/controller"
	"github.com/gh0st-nemesis/nimbuscore/internal/finops"
	"github.com/gh0st-nemesis/nimbuscore/internal/gitops"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
)

//go:embed static
var staticFiles embed.FS

type Config struct {
	Nodes          *registry.Registry[*v1.Node]
	Pods           *registry.Registry[*v1.Pod]
	DeploymentSvc  v1.DeploymentServiceServer
	Services       v1.ServiceServiceServer
	Namespaces     v1.NamespaceServiceServer
	Volumes        v1.VolumeServiceServer
	GitOps         *gitops.Reconciler
	CostModel      finops.CostModel
	Username       string
	Password       string
	AgentLogPort   int
	AgentFilesPort int
}

func NewHandler(cfg Config) (http.Handler, error) {
	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, err
	}

	appMux := http.NewServeMux()
	appMux.Handle("/", http.FileServer(http.FS(static)))
	appMux.HandleFunc("/api/nodes", cfg.handleNodes)
	appMux.HandleFunc("/api/pods", cfg.handlePods)
	appMux.HandleFunc("/api/deployments", cfg.handleDeployments)
	appMux.HandleFunc("/api/services", cfg.handleServices)
	appMux.HandleFunc("/api/finops", cfg.handleFinops)
	appMux.HandleFunc("/api/cicd", cfg.handleCICD)
	appMux.HandleFunc("/api/metrics", cfg.handleMetrics)
	appMux.HandleFunc("/api/logs", cfg.handleLogs)
	appMux.HandleFunc("/api/namespaces", cfg.handleNamespaces)
	appMux.HandleFunc("/api/files", cfg.handleFiles)

	mux := http.NewServeMux()
	if cfg.Password == "" {
		mux.Handle("/", appMux)
		return mux, nil
	}

	sess := newSessionAuth(cfg.Username, cfg.Password, static)
	mux.HandleFunc("/login", sess.handleLoginPage)
	mux.HandleFunc("/api/login", sess.handleLogin)
	mux.HandleFunc("/api/logout", sess.handleLogout)
	fileServer := http.FileServer(http.FS(static))
	mux.Handle("/fonts/", fileServer)  // public: self-hosted webfont, needed to render the login page, not sensitive
	mux.Handle("/vendor/", fileServer) // public: self-hosted three.js, needed by the login page's particle background
	mux.Handle("/style.css", fileServer)
	mux.Handle("/logo.png", fileServer)
	mux.Handle("/login.js", fileServer)
	mux.Handle("/particles.js", fileServer)
	mux.Handle("/", sess.requireSession(appMux))
	return mux, nil
}

func (cfg Config) handleNodes(w http.ResponseWriter, r *http.Request) {
	items, err := cfg.Nodes.List(r.Context(), "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeProto(w, &v1.ListNodesResponse{Items: items})
}

func (cfg Config) handlePods(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := cfg.Pods.List(r.Context(), "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeProto(w, &v1.ListPodsResponse{Items: items})

	case http.MethodDelete:
		namespace := r.URL.Query().Get("namespace")
		if namespace == "" {
			namespace = "default"
		}
		name := r.URL.Query().Get("name")
		if name == "" {
			http.Error(w, "name query parameter is required", http.StatusBadRequest)
			return
		}
		// Deleting a pod that belongs to a Deployment is expected to be a
		// transient action — the deployment reconciler notices the replica
		// shortfall on its next tick and recreates it to restore the desired
		// replica count. Deleting a standalone pod just deletes it.
		if err := cfg.Pods.Delete(r.Context(), namespace, name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (cfg Config) handleNamespaces(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		resp, err := cfg.Namespaces.ListNamespaces(r.Context(), &v1.ListNamespacesRequest{})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeProto(w, resp)

	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.Name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}
		created, err := cfg.Namespaces.CreateNamespace(r.Context(), &v1.CreateNamespaceRequest{
			Namespace: &v1.Namespace{Metadata: &v1.ObjectMeta{Name: req.Name}},
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeProto(w, created)

	case http.MethodDelete:
		name := r.URL.Query().Get("name")
		if name == "" {
			http.Error(w, "name query parameter is required", http.StatusBadRequest)
			return
		}
		if _, err := cfg.Namespaces.DeleteNamespace(r.Context(), &v1.DeleteNamespaceRequest{Name: name}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// resolveNodeIP looks up a node's internal IP, the address the dashboard uses
// to reach that node's per-node agent HTTP endpoints (logs, files).
func (cfg Config) resolveNodeIP(ctx context.Context, nodeName string) (string, error) {
	node, err := cfg.Nodes.Get(ctx, "", nodeName)
	if err != nil {
		return "", fmt.Errorf("node not found: %w", err)
	}
	ip := node.GetStatus().GetInternalIp()
	if ip == "" {
		return "", fmt.Errorf("node %q has no internal IP recorded", nodeName)
	}
	return ip, nil
}

// proxyToAgent forwards the incoming request (method, query string, and body)
// to a node-local agent HTTP endpoint and streams the response back verbatim.
func proxyToAgent(w http.ResponseWriter, r *http.Request, internalIP string, port int, path string) {
	upstream := fmt.Sprintf("http://%s:%d%s?%s", internalIP, port, path, r.URL.RawQuery)
	upReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstream, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp, err := http.DefaultClient.Do(upReq)
	if err != nil {
		http.Error(w, "fetch from node: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body) //nolint:errcheck
}

func (cfg Config) handleLogs(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = "default"
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "name query parameter is required", http.StatusBadRequest)
		return
	}

	pod, err := cfg.Pods.Get(r.Context(), namespace, name)
	if err != nil {
		http.Error(w, "pod not found: "+err.Error(), http.StatusNotFound)
		return
	}
	nodeName := pod.GetSpec().GetNodeName()
	if nodeName == "" {
		http.Error(w, "pod is not scheduled to a node yet", http.StatusNotFound)
		return
	}

	internalIP, err := cfg.resolveNodeIP(r.Context(), nodeName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	port := cfg.AgentLogPort
	if port <= 0 {
		port = 10250
	}
	proxyToAgent(w, r, internalIP, port, "/logs")
}

// handleFiles proxies file-browser operations for a persistent volume to
// whichever node it's claimed on. It resolves via the Volume's identity
// rather than a pod's, so browsing/editing keeps working even while the
// owning deployment's pod is stopped, scaling, or mid-redeploy.
func (cfg Config) handleFiles(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = "default"
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "name query parameter is required", http.StatusBadRequest)
		return
	}

	vol, err := cfg.Volumes.GetVolume(r.Context(), &v1.GetVolumeRequest{Namespace: namespace, Name: name})
	if err != nil {
		http.Error(w, "volume not found: "+err.Error(), http.StatusNotFound)
		return
	}
	nodeName := vol.GetStatus().GetNodeName()
	if nodeName == "" {
		http.Error(w, "volume is not yet claimed by any node", http.StatusNotFound)
		return
	}

	internalIP, err := cfg.resolveNodeIP(r.Context(), nodeName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	port := cfg.AgentFilesPort
	if port <= 0 {
		port = 10251
	}

	var op string
	switch r.Method {
	case http.MethodGet:
		op = r.URL.Query().Get("op")
		if op != "read" {
			op = "list"
		}
	case http.MethodPut:
		op = "write"
	case http.MethodDelete:
		op = "delete"
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	proxyToAgent(w, r, internalIP, port, "/files/"+op)
}

func (cfg Config) handleDeployments(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		resp, err := cfg.DeploymentSvc.ListDeployments(r.Context(), &v1.ListDeploymentsRequest{})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeProto(w, resp)

	case http.MethodPost:
		cfg.handleCreateDeployment(w, r)

	case http.MethodPatch:
		cfg.handleScaleDeployment(w, r)

	case http.MethodDelete:
		cfg.handleDeleteDeployment(w, r)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type createDeploymentRequest struct {
	Name                 string            `json:"name"`
	Namespace            string            `json:"namespace"`
	Image                string            `json:"image"`
	GitRepoURL           string            `json:"gitRepoUrl"`
	GitBranch            string            `json:"gitBranch"`
	GitDockerfile        string            `json:"gitDockerfilePath"`
	GitContext           string            `json:"gitContextPath"`
	Replicas             int32             `json:"replicas"`
	Port                 int32             `json:"port"`
	Command              []string          `json:"command"`
	Env                  map[string]string `json:"env"`
	CPUMillis            int64             `json:"cpuMillis"`
	MemoryBytes          int64             `json:"memoryBytes"`
	AddPersistentStorage bool              `json:"addPersistentStorage"`
	MountPath            string            `json:"mountPath"`
	Links                []string          `json:"links"`
}

// linksToLabel is a well-known deployment label recording the names of other
// deployments (in the same namespace) this one was linked to via the
// dashboard's "Link a service" action — the canvas reads it to draw a
// connecting line between the two. Comma-separated since ObjectMeta.Labels
// only holds plain strings, not lists.
const linksToLabel = "nimbuscore.io/links-to"

func (cfg Config) handleCreateDeployment(w http.ResponseWriter, r *http.Request) {
	var req createDeploymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if req.Image == "" && req.GitRepoURL == "" {
		http.Error(w, "image or gitRepoUrl is required", http.StatusBadRequest)
		return
	}
	if req.Namespace == "" {
		req.Namespace = "default"
	}
	if req.Replicas <= 0 {
		req.Replicas = 1
	}
	if req.AddPersistentStorage && req.Replicas > 1 {
		http.Error(w, "deployments with persistent storage must have replicas = 1", http.StatusBadRequest)
		return
	}
	if req.AddPersistentStorage && req.MountPath == "" {
		http.Error(w, "mountPath is required when addPersistentStorage is set", http.StatusBadRequest)
		return
	}

	_, getErr := cfg.DeploymentSvc.GetDeployment(r.Context(), &v1.GetDeploymentRequest{Namespace: req.Namespace, Name: req.Name})
	isEdit := getErr == nil

	var ports []int32
	if req.Port > 0 {
		ports = []int32{req.Port}
	}

	container := &v1.Container{
		Name:           req.Name,
		Image:          req.Image,
		Command:        req.Command,
		ContainerPorts: ports,
		Env:            req.Env,
	}
	if req.GitRepoURL != "" {
		container.Image = ""
		container.BuildSource = &v1.BuildSource{
			RepoUrl:        req.GitRepoURL,
			Branch:         req.GitBranch,
			DockerfilePath: req.GitDockerfile,
			ContextPath:    req.GitContext,
		}
	}
	if req.CPUMillis > 0 || req.MemoryBytes > 0 {
		container.Resources = &v1.ResourceRequirements{
			Requests: &v1.ResourceList{CpuMillis: req.CPUMillis, MemoryBytes: req.MemoryBytes},
		}
	}

	labels := map[string]string{"app": req.Name}
	if len(req.Links) > 0 {
		labels[linksToLabel] = strings.Join(req.Links, ",")
	}

	deployment := &v1.Deployment{
		Metadata: &v1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
			Labels:    labels,
		},
		Spec: &v1.DeploymentSpec{
			Replicas: req.Replicas,
			Selector: map[string]string{"app": req.Name},
			Template: &v1.PodSpec{Containers: []*v1.Container{container}},
		},
	}

	if req.AddPersistentStorage {
		// The volume must exist (and be part of the template) before the
		// Deployment is created — unlike the Service below, which is a
		// supplementary side effect created after the fact.
		if _, err := cfg.Volumes.GetVolume(r.Context(), &v1.GetVolumeRequest{Namespace: req.Namespace, Name: req.Name}); err != nil {
			if _, err := cfg.Volumes.CreateVolume(r.Context(), &v1.CreateVolumeRequest{
				Volume: &v1.Volume{
					Metadata: &v1.ObjectMeta{Name: req.Name, Namespace: req.Namespace},
					Spec:     &v1.VolumeSpec{RequestedBytes: 1 << 30},
				},
			}); err != nil {
				http.Error(w, "create volume: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
		deployment.Spec.Template.Volumes = []*v1.VolumeMount{{VolumeName: req.Name, MountPath: req.MountPath}}
	}

	created, err := cfg.DeploymentSvc.CreateDeployment(r.Context(), &v1.CreateDeploymentRequest{Deployment: deployment})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if isEdit {
		cfg.restartOwnedPods(r.Context(), req.Namespace, req.Name)
	}

	depJSON, err := protojson.Marshal(created)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	envelope := map[string]json.RawMessage{"deployment": depJSON}

	if req.Port > 0 {
		svcObj := &v1.Service{
			Metadata: &v1.ObjectMeta{Name: req.Name, Namespace: req.Namespace, Labels: map[string]string{"app": req.Name}},
			Spec:     &v1.ServiceSpec{Selector: map[string]string{"app": req.Name}, Port: req.Port, TargetPort: req.Port},
		}
		createdSvc, svcErr := cfg.Services.CreateService(r.Context(), &v1.CreateServiceRequest{Service: svcObj})
		if svcErr != nil {
			if b, err := json.Marshal(svcErr.Error()); err == nil {
				envelope["serviceError"] = b
			}
		} else if svcJSON, err := protojson.Marshal(createdSvc); err == nil {
			envelope["service"] = svcJSON
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(envelope) //nolint:errcheck
}

func (cfg Config) restartOwnedPods(ctx context.Context, namespace, name string) {
	pods, err := cfg.Pods.List(ctx, namespace)
	if err != nil {
		return
	}
	for _, p := range pods {
		if p.GetMetadata().GetLabels()[controller.OwnerDeploymentLabel] != name {
			continue
		}
		_ = cfg.Pods.Delete(ctx, p.GetMetadata().GetNamespace(), p.GetMetadata().GetName())
	}
}

func (cfg Config) handleDeleteDeployment(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = "default"
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "name query parameter is required", http.StatusBadRequest)
		return
	}

	if _, err := cfg.DeploymentSvc.DeleteDeployment(r.Context(), &v1.DeleteDeploymentRequest{Namespace: namespace, Name: name}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type scaleDeploymentRequest struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Replicas  int32  `json:"replicas"`
}

func (cfg Config) handleScaleDeployment(w http.ResponseWriter, r *http.Request) {
	var req scaleDeploymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if req.Namespace == "" {
		req.Namespace = "default"
	}
	if req.Replicas < 0 {
		http.Error(w, "replicas must be >= 0", http.StatusBadRequest)
		return
	}

	current, err := cfg.DeploymentSvc.GetDeployment(r.Context(), &v1.GetDeploymentRequest{Namespace: req.Namespace, Name: req.Name})
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if len(current.GetSpec().GetTemplate().GetVolumes()) > 0 && req.Replicas > 1 {
		http.Error(w, "deployments with persistent storage must have replicas <= 1", http.StatusBadRequest)
		return
	}
	current.Spec.Replicas = req.Replicas

	updated, err := cfg.DeploymentSvc.CreateDeployment(r.Context(), &v1.CreateDeploymentRequest{Deployment: current})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeProto(w, updated)
}

func (cfg Config) handleServices(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		resp, err := cfg.Services.ListServices(r.Context(), &v1.ListServicesRequest{})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeProto(w, resp)

	case http.MethodDelete:
		namespace := r.URL.Query().Get("namespace")
		if namespace == "" {
			namespace = "default"
		}
		name := r.URL.Query().Get("name")
		if name == "" {
			http.Error(w, "name query parameter is required", http.StatusBadRequest)
			return
		}
		if _, err := cfg.Services.DeleteService(r.Context(), &v1.DeleteServiceRequest{Namespace: namespace, Name: name}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type nodeMetrics struct {
	Name                   string `json:"name"`
	Ready                  bool   `json:"ready"`
	CPUAllocatableMillis   int64  `json:"cpuAllocatableMillis"`
	CPURequestedMillis     int64  `json:"cpuRequestedMillis"`
	MemoryAllocatableBytes int64  `json:"memoryAllocatableBytes"`
	MemoryRequestedBytes   int64  `json:"memoryRequestedBytes"`
	MemoryUsedBytes        int64  `json:"memoryUsedBytes"`
}

type clusterMetrics struct {
	Nodes                  []nodeMetrics `json:"nodes"`
	CPUAllocatableMillis   int64         `json:"cpuAllocatableMillis"`
	CPURequestedMillis     int64         `json:"cpuRequestedMillis"`
	MemoryAllocatableBytes int64         `json:"memoryAllocatableBytes"`
	MemoryRequestedBytes   int64         `json:"memoryRequestedBytes"`
	MemoryUsedBytes        int64         `json:"memoryUsedBytes"`
}

func (cfg Config) handleMetrics(w http.ResponseWriter, r *http.Request) {
	nodes, err := cfg.Nodes.List(r.Context(), "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pods, err := cfg.Pods.List(r.Context(), "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type usage struct {
		cpuRequested, memRequested, memUsed int64
	}
	byNode := make(map[string]usage, len(nodes))
	for _, p := range pods {
		name := p.GetSpec().GetNodeName()
		if name == "" {
			continue
		}
		u := byNode[name]
		for _, c := range p.GetSpec().GetContainers() {
			u.cpuRequested += c.GetResources().GetRequests().GetCpuMillis()
			u.memRequested += c.GetResources().GetRequests().GetMemoryBytes()
		}
		u.memUsed += p.GetStatus().GetMemoryUsageBytes()
		byNode[name] = u
	}

	out := clusterMetrics{Nodes: make([]nodeMetrics, 0, len(nodes))}
	for _, n := range nodes {
		name := n.GetMetadata().GetName()
		u := byNode[name]
		alloc := n.GetStatus().GetAllocatable()
		nm := nodeMetrics{
			Name:                   name,
			Ready:                  n.GetStatus().GetReady(),
			CPUAllocatableMillis:   alloc.GetCpuMillis(),
			CPURequestedMillis:     u.cpuRequested,
			MemoryAllocatableBytes: alloc.GetMemoryBytes(),
			MemoryRequestedBytes:   u.memRequested,
			MemoryUsedBytes:        u.memUsed,
		}
		out.Nodes = append(out.Nodes, nm)
		out.CPUAllocatableMillis += nm.CPUAllocatableMillis
		out.CPURequestedMillis += nm.CPURequestedMillis
		out.MemoryAllocatableBytes += nm.MemoryAllocatableBytes
		out.MemoryRequestedBytes += nm.MemoryRequestedBytes
		out.MemoryUsedBytes += nm.MemoryUsedBytes
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out) //nolint:errcheck
}

func (cfg Config) handleFinops(w http.ResponseWriter, r *http.Request) {
	pods, err := cfg.Pods.List(r.Context(), "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	report := finops.Estimate(pods, cfg.CostModel, "", time.Now())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(report) //nolint:errcheck
}

type cicdDeployment struct {
	Namespace     string `json:"namespace"`
	Name          string `json:"name"`
	Image         string `json:"image"`
	Replicas      int32  `json:"replicas"`
	ReadyReplicas int32  `json:"readyReplicas"`
	Source        string `json:"source"`
}

type cicdGitOps struct {
	Configured   bool   `json:"configured"`
	RepoURL      string `json:"repoUrl"`
	Branch       string `json:"branch"`
	Path         string `json:"path"`
	LastSyncUnix int64  `json:"lastSyncUnix"`
	LastCommit   string `json:"lastCommit"`
	LastError    string `json:"lastError"`
}

type cicdResponse struct {
	GitOps      cicdGitOps       `json:"gitops"`
	Deployments []cicdDeployment `json:"deployments"`
}

func (cfg Config) handleCICD(w http.ResponseWriter, r *http.Request) {
	resp, err := cfg.DeploymentSvc.ListDeployments(r.Context(), &v1.ListDeploymentsRequest{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	out := cicdResponse{Deployments: make([]cicdDeployment, 0, len(resp.GetItems()))}
	if cfg.GitOps != nil {
		st := cfg.GitOps.Status()
		out.GitOps = cicdGitOps{
			Configured:   true,
			RepoURL:      st.RepoURL,
			Branch:       st.Branch,
			Path:         st.Path,
			LastSyncUnix: st.LastSyncUnix,
			LastCommit:   st.LastCommit,
			LastError:    st.LastError,
		}
	}

	for _, d := range resp.GetItems() {
		source := "manual"
		if d.GetMetadata().GetLabels()[gitops.ManagedByLabel] == gitops.ManagedByValue {
			source = "gitops"
		}
		image := ""
		if containers := d.GetSpec().GetTemplate().GetContainers(); len(containers) > 0 {
			image = containers[0].GetImage()
			if build := containers[0].GetBuildSource(); build != nil {
				image = "git: " + build.GetRepoUrl()
			}
		}
		out.Deployments = append(out.Deployments, cicdDeployment{
			Namespace:     d.GetMetadata().GetNamespace(),
			Name:          d.GetMetadata().GetName(),
			Image:         image,
			Replicas:      d.GetSpec().GetReplicas(),
			ReadyReplicas: d.GetStatus().GetReadyReplicas(),
			Source:        source,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out) //nolint:errcheck
}

func writeProto(w http.ResponseWriter, msg proto.Message) {
	raw, err := protojson.Marshal(msg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw) //nolint:errcheck
}
