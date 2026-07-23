package registry

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

type Registry[T proto.Message] struct {
	store store.Store
	kind  string
	newFn func() T
}

func New[T proto.Message](s store.Store, kind string, newFn func() T) *Registry[T] {
	return &Registry[T]{store: s, kind: kind, newFn: newFn}
}

func (r *Registry[T]) key(namespace, name string) string {
	if namespace == "" {
		return fmt.Sprintf("/registry/%s/%s", r.kind, name)
	}
	return fmt.Sprintf("/registry/%s/%s/%s", r.kind, namespace, name)
}

func (r *Registry[T]) Put(ctx context.Context, namespace, name string, obj T) error {
	b, err := proto.Marshal(obj)
	if err != nil {
		return fmt.Errorf("registry: marshal %s %s/%s: %w", r.kind, namespace, name, err)
	}
	return r.store.Put(ctx, r.key(namespace, name), b)
}

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

func (r *Registry[T]) Delete(ctx context.Context, namespace, name string) error {
	return r.store.Delete(ctx, r.key(namespace, name))
}

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
