package wasmrt

import (
	"context"
	"testing"

	"github.com/tetratelabs/wazero"
)

var addModule = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x07, 0x01, 0x60, 0x02, 0x7F, 0x7F, 0x01, 0x7F,
	0x03, 0x02, 0x01, 0x00,
	0x07, 0x07, 0x01, 0x03, 0x61, 0x64, 0x64, 0x00, 0x00,
	0x0A, 0x09, 0x01, 0x07, 0x00, 0x20, 0x00, 0x20, 0x01, 0x6A, 0x0B,
}

var noopStartModule = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
	0x03, 0x02, 0x01, 0x00,
	0x07, 0x0A, 0x01, 0x06, 0x5F, 0x73, 0x74, 0x61, 0x72, 0x74, 0x00, 0x00,
	0x0A, 0x04, 0x01, 0x02, 0x00, 0x0B,
}

func TestRuntimeCallFuncAddsTwoNumbers(t *testing.T) {
	ctx := context.Background()
	rt, err := New(ctx)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close(ctx)

	results, err := rt.CallFunc(ctx, addModule, "add", 2, 3)
	if err != nil {
		t.Fatalf("CallFunc: %v", err)
	}
	if len(results) != 1 || results[0] != 5 {
		t.Fatalf("CallFunc(add, 2, 3) = %v, want [5]", results)
	}
}

func TestRuntimeCallFuncRejectsUnknownFunction(t *testing.T) {
	ctx := context.Background()
	rt, err := New(ctx)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close(ctx)

	if _, err := rt.CallFunc(ctx, addModule, "does-not-exist"); err == nil {
		t.Fatal("CallFunc succeeded for an unexported function, want error")
	}
}

func TestRuntimeRunCompletesNormally(t *testing.T) {
	ctx := context.Background()
	rt, err := New(ctx)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close(ctx)

	exitCode, err := rt.Run(ctx, noopStartModule, wazero.NewModuleConfig().WithStartFunctions("_start"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0", exitCode)
	}
}

func TestRuntimeRejectsInvalidModule(t *testing.T) {
	ctx := context.Background()
	rt, err := New(ctx)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close(ctx)

	if _, err := rt.CallFunc(ctx, []byte("not a wasm module"), "add"); err == nil {
		t.Fatal("CallFunc succeeded on garbage bytes, want error")
	}
}
