package wasmrt

import (
	"context"
	"errors"
	"fmt"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

type Runtime struct {
	rt wazero.Runtime
}

func New(ctx context.Context) (*Runtime, error) {
	rt := wazero.NewRuntime(ctx)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("wasmrt: instantiate WASI: %w", err)
	}
	return &Runtime{rt: rt}, nil
}

func (r *Runtime) Close(ctx context.Context) error {
	return r.rt.Close(ctx)
}

func (r *Runtime) CallFunc(ctx context.Context, wasmBytes []byte, funcName string, args ...uint64) ([]uint64, error) {
	mod, err := r.rt.Instantiate(ctx, wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("wasmrt: instantiate module: %w", err)
	}
	defer mod.Close(ctx)

	fn := mod.ExportedFunction(funcName)
	if fn == nil {
		return nil, fmt.Errorf("wasmrt: function %q not exported", funcName)
	}
	results, err := fn.Call(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("wasmrt: call %q: %w", funcName, err)
	}
	return results, nil
}

func (r *Runtime) Run(ctx context.Context, wasmBytes []byte, config wazero.ModuleConfig) (exitCode uint32, err error) {
	mod, err := r.rt.InstantiateWithConfig(ctx, wasmBytes, config)
	if mod != nil {
		defer mod.Close(ctx)
	}
	if err == nil {
		return 0, nil
	}

	var exitErr *sys.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 0, fmt.Errorf("wasmrt: run: %w", err)
}
