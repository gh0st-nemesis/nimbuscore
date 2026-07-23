// Package registry provides typed, Protobuf-backed access to a
// store.Store, the same role client-go's typed clientset / informer
// cache plays on top of etcd in Kubernetes — except here it talks
// directly to our Store, no network hop, since Phase 1 runs everything
// in one process.
package registry

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

// Registry stores and retrieves T values, keyed by namespace/name, under
// a per-kind prefix in the underlying Store.
type Registry[T proto.Message] struct {
	store store.Store
	kind  string
	newFn func() T
}

// New returns a Registry for the given resource kind (e.g. "pods").
// newFn must return a fresh zero-value T for unmarshaling — proto.Message
// implementations are pointers to generated structs, so this is
// typically `func() *v1.Pod { return &v1.Pod{} }`.
func New[T proto.Message](s store.Store, kind string, newFn func() T) *Registry[T] {
	return &Registry[T]{store: s, kind: kind, newFn: newFn}
}

func (r *Registry[T]) key(namespace, name string) string {
	if namespace == "" {
		return fmt.Sprintf("/registry/%s/%s", r.kind, name)
	}
	return fmt.Sprintf("/registry/%s/%s/%s", r.kind, namespace, name)
}

// Put marshals obj and writes it under namespace/name.
func (r *Registry[T]) Put(ctx context.Context, namespace, name string, obj T) error {
	b, err := proto.Marshal(obj)
	if err != nil {
		return fmt.Errorf("registry: marshal %s %s/%s: %w", r.kind, namespace, name, err)
	}
	return r.store.Put(ctx, r.key(namespace, name), b)
}

// Get returns the object stored under namespace/name.
func (r *Registry[T]) Get(ctx context.Context, namespace, name string) (T, error) {
	var zero T
	b, err := r.store.Get(ctx, r.key(namespace, name))
	if err != nil {
		return zero, err
	}
	obj := r.newFn()
	if err := proto.Unmarshal(b, obj); err != nil {
		return zero, fmt.Errorf("registry: unmarshal %s %s/%s: %w", r.kind, namespace, name, err)
	}
	return obj, nil
}

// Delete removes the object stored under namespace/name.
func (r *Registry[T]) Delete(ctx context.Context, namespace, name string) error {
	return r.store.Delete(ctx, r.key(namespace, name))
}

// List returns every object of this kind in namespace. An empty
// namespace lists across all namespaces.
func (r *Registry[T]) List(ctx context.Context, namespace string) ([]T, error) {
	prefix := fmt.Sprintf("/registry/%s/", r.kind)
	if namespace != "" {
		prefix = fmt.Sprintf("/registry/%s/%s/", r.kind, namespace)
	}

	raw, err := r.store.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("registry: list %s: %w", r.kind, err)
	}

	out := make([]T, 0, len(raw))
	for _, b := range raw {
		obj := r.newFn()
		if err := proto.Unmarshal(b, obj); err != nil {
			return nil, fmt.Errorf("registry: unmarshal %s: %w", r.kind, err)
		}
		out = append(out, obj)
	}
	return out, nil
}
