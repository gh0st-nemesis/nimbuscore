package federation

import (
	"context"
	"maps"
	"sync"

	"google.golang.org/grpc"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

type Registry struct {
	mu    sync.RWMutex
	conns map[string]grpc.ClientConnInterface
}

func NewRegistry() *Registry {
	return &Registry{conns: make(map[string]grpc.ClientConnInterface)}
}

func (r *Registry) Register(name string, conn grpc.ClientConnInterface) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.conns[name] = conn
}

func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.conns, name)
}

func (r *Registry) Clusters() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.conns))
	for name := range r.conns {
		out = append(out, name)
	}
	return out
}

func (r *Registry) snapshot() map[string]grpc.ClientConnInterface {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]grpc.ClientConnInterface, len(r.conns))
	maps.Copy(out, r.conns)
	return out
}

type ClusterPods struct {
	Cluster string
	Items   []*v1.Pod
	Err     error
}

func (r *Registry) ListPodsAll(ctx context.Context, namespace string) []ClusterPods {
	conns := r.snapshot()

	results := make([]ClusterPods, len(conns))
	var wg sync.WaitGroup
	i := 0
	for name, conn := range conns {
		wg.Add(1)
		go func(i int, name string, conn grpc.ClientConnInterface) {
			defer wg.Done()
			resp, err := v1.NewPodServiceClient(conn).ListPods(ctx, &v1.ListPodsRequest{Namespace: namespace})
			if err != nil {
				results[i] = ClusterPods{Cluster: name, Err: err}
				return
			}
			results[i] = ClusterPods{Cluster: name, Items: resp.GetItems()}
		}(i, name, conn)
		i++
	}
	wg.Wait()
	return results
}
