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
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
)

//go:embed static
var staticFiles embed.FS

type Config struct {
	Nodes       *registry.Registry[*v1.Node]
	Pods        *registry.Registry[*v1.Pod]
	Deployments *registry.Registry[*v1.Deployment]
	Services    v1.ServiceServiceServer
	CostModel   finops.CostModel
	Username    string
	Password    string
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
	items, err := cfg.Deployments.List(r.Context(), "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeProto(w, &v1.ListDeploymentsResponse{Items: items})
}

func (cfg Config) handleServices(w http.ResponseWriter, r *http.Request) {
	resp, err := cfg.Services.ListServices(r.Context(), &v1.ListServicesRequest{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeProto(w, resp)
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

func writeProto(w http.ResponseWriter, msg proto.Message) {
	raw, err := protojson.Marshal(msg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw) //nolint:errcheck
}
