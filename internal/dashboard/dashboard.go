package dashboard

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/finops"
	"github.com/gh0st-nemesis/nimbuscore/internal/gitops"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
)

//go:embed static
var staticFiles embed.FS

type Config struct {
	Nodes         *registry.Registry[*v1.Node]
	Pods          *registry.Registry[*v1.Pod]
	DeploymentSvc v1.DeploymentServiceServer
	Services      v1.ServiceServiceServer
	GitOps        *gitops.Reconciler
	CostModel     finops.CostModel
	Username      string
	Password      string
}

func NewHandler(cfg Config) (http.Handler, error) {
	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(static)))
	mux.HandleFunc("/api/nodes", cfg.handleNodes)
	mux.HandleFunc("/api/pods", cfg.handlePods)
	mux.HandleFunc("/api/deployments", cfg.handleDeployments)
	mux.HandleFunc("/api/services", cfg.handleServices)
	mux.HandleFunc("/api/finops", cfg.handleFinops)
	mux.HandleFunc("/api/cicd", cfg.handleCICD)
	mux.HandleFunc("/api/metrics", cfg.handleMetrics)

	if cfg.Password == "" {
		return mux, nil
	}
	return basicAuth(cfg.Username, cfg.Password, mux), nil
}

func basicAuth(username, password string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		userMatch := subtle.ConstantTimeCompare([]byte(user), []byte(username)) == 1
		passMatch := subtle.ConstantTimeCompare([]byte(pass), []byte(password)) == 1
		if !ok || !userMatch || !passMatch {
			w.Header().Set("WWW-Authenticate", `Basic realm="nimbuscore"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
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
	items, err := cfg.Pods.List(r.Context(), "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeProto(w, &v1.ListPodsResponse{Items: items})
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
	Name        string   `json:"name"`
	Namespace   string   `json:"namespace"`
	Image       string   `json:"image"`
	Replicas    int32    `json:"replicas"`
	Port        int32    `json:"port"`
	Command     []string `json:"command"`
	CPUMillis   int64    `json:"cpuMillis"`
	MemoryBytes int64    `json:"memoryBytes"`
}

func (cfg Config) handleCreateDeployment(w http.ResponseWriter, r *http.Request) {
	var req createDeploymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.Image == "" {
		http.Error(w, "name and image are required", http.StatusBadRequest)
		return
	}
	if req.Namespace == "" {
		req.Namespace = "default"
	}
	if req.Replicas <= 0 {
		req.Replicas = 1
	}

	var ports []int32
	if req.Port > 0 {
		ports = []int32{req.Port}
	}

	container := &v1.Container{
		Name:           req.Name,
		Image:          req.Image,
		Command:        req.Command,
		ContainerPorts: ports,
	}
	if req.CPUMillis > 0 || req.MemoryBytes > 0 {
		container.Resources = &v1.ResourceRequirements{
			Requests: &v1.ResourceList{CpuMillis: req.CPUMillis, MemoryBytes: req.MemoryBytes},
		}
	}

	deployment := &v1.Deployment{
		Metadata: &v1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
			Labels:    map[string]string{"app": req.Name},
		},
		Spec: &v1.DeploymentSpec{
			Replicas: req.Replicas,
			Selector: map[string]string{"app": req.Name},
			Template: &v1.PodSpec{Containers: []*v1.Container{container}},
		},
	}

	created, err := cfg.DeploymentSvc.CreateDeployment(r.Context(), &v1.CreateDeploymentRequest{Deployment: deployment})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
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
