package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

var noopStartWASM = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
	0x03, 0x02, 0x01, 0x00,
	0x07, 0x0A, 0x01, 0x06, 0x5F, 0x73, 0x74, 0x61, 0x72, 0x74, 0x00, 0x00,
	0x0A, 0x04, 0x01, 0x02, 0x00, 0x0B,
}

func writeTestWASM(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "module.wasm")
	if err := os.WriteFile(path, noopStartWASM, 0o644); err != nil {
		t.Fatalf("write wasm module: %v", err)
	}
	return path
}

func TestProcessRuntimeRunsWASMWorkload(t *testing.T) {
	r := newProcessRuntime()
	pod := &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: "wasm-pod", Namespace: "default"},
		Spec: &v1.PodSpec{
			Containers: []*v1.Container{{Name: "app", WasmModulePath: writeTestWASM(t)}},
		},
	}

	if err := r.start(pod); err != nil {
		t.Fatalf("start: %v", err)
	}

	select {
	case ev := <-r.exited:
		if ev.key != podKey(pod) {
			t.Errorf("exit event key = %q, want %q", ev.key, podKey(pod))
		}
		if ev.exitCode != 0 {
			t.Errorf("exit code = %d, want 0", ev.exitCode)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the wasm module to complete")
	}
}

func TestAgentRunsWASMPodToCompletion(t *testing.T) {
	client := newFakePodServiceClient()
	pod := &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: "wasm-pod", Namespace: "default"},
		Spec: &v1.PodSpec{
			NodeName:      "test-node",
			RestartPolicy: v1.RestartPolicy_RESTART_POLICY_NEVER,
			Containers:    []*v1.Container{{Name: "app", WasmModulePath: writeTestWASM(t)}},
		},
		Status: &v1.PodStatus{Phase: v1.PodPhase_POD_PHASE_PENDING},
	}
	client.seed(pod)

	a := New(Config{NodeName: "test-node", Interval: 30 * time.Millisecond}, client, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)

	waitForPhase(t, client, podKey(pod), v1.PodPhase_POD_PHASE_SUCCEEDED)
}
