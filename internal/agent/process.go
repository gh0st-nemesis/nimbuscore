package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/shirou/gopsutil/v3/process"
	"github.com/tetratelabs/wazero"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/wasmrt"
)

type exitEvent struct {
	key      string
	exitCode int
}

type trackedProcess struct {
	pod         *v1.Pod
	cmd         *exec.Cmd
	wasmRuntime *wasmrt.Runtime
	restarts    int32
}

type processRuntime struct {
	mu     sync.Mutex
	procs  map[string]*trackedProcess
	exited chan exitEvent
}

func newProcessRuntime() *processRuntime {
	return &processRuntime{
		procs:  make(map[string]*trackedProcess),
		exited: make(chan exitEvent, 32),
	}
}

func podKey(pod *v1.Pod) string {
	return pod.GetMetadata().GetNamespace() + "/" + pod.GetMetadata().GetName()
}

func (r *processRuntime) has(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.procs[key]
	return ok
}

func (r *processRuntime) keys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.procs))
	for k := range r.procs {
		out = append(out, k)
	}
	return out
}

func (r *processRuntime) restartCount(key string) int32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if tp, ok := r.procs[key]; ok {
		return tp.restarts
	}
	return 0
}

func (r *processRuntime) memoryUsageBytes(key string) int64 {
	r.mu.Lock()
	tp, ok := r.procs[key]
	r.mu.Unlock()
	if !ok || tp.cmd == nil || tp.cmd.Process == nil {
		return 0
	}
	proc, err := process.NewProcess(int32(tp.cmd.Process.Pid))
	if err != nil {
		return 0
	}
	info, err := proc.MemoryInfo()
	if err != nil || info == nil {
		return 0
	}
	return int64(info.RSS)
}

func firstContainer(pod *v1.Pod) *v1.Container {
	containers := pod.GetSpec().GetContainers()
	if len(containers) == 0 {
		return nil
	}
	return containers[0]
}

func (r *processRuntime) start(pod *v1.Pod) error {
	if c := firstContainer(pod); c.GetWasmModulePath() != "" {
		return r.startWASM(pod, c.GetWasmModulePath())
	}
	return r.startProcess(pod)
}

func (r *processRuntime) startProcess(pod *v1.Pod) error {
	key := podKey(pod)
	args := firstContainer(pod).GetCommand()
	if len(args) == 0 {
		return fmt.Errorf("agent: pod %s has neither a command nor a wasm_module_path to run", key)
	}

	cmd := exec.Command(args[0], args[1:]...)
	if err := cmd.Start(); err != nil {
		return err
	}

	r.mu.Lock()
	restarts := int32(0)
	if existing, ok := r.procs[key]; ok {
		restarts = existing.restarts
	}
	r.procs[key] = &trackedProcess{pod: pod, cmd: cmd, restarts: restarts}
	r.mu.Unlock()

	go func() {
		err := cmd.Wait()
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = -1
			}
		}
		r.exited <- exitEvent{key: key, exitCode: exitCode}
	}()
	return nil
}

func (r *processRuntime) startWASM(pod *v1.Pod, modulePath string) error {
	key := podKey(pod)

	wasmBytes, err := os.ReadFile(modulePath)
	if err != nil {
		return fmt.Errorf("agent: read wasm module %s: %w", modulePath, err)
	}

	ctx := context.Background()
	rt, err := wasmrt.New(ctx)
	if err != nil {
		return fmt.Errorf("agent: init wasm runtime for %s: %w", modulePath, err)
	}

	r.mu.Lock()
	restarts := int32(0)
	if existing, ok := r.procs[key]; ok {
		restarts = existing.restarts
	}
	r.procs[key] = &trackedProcess{pod: pod, wasmRuntime: rt, restarts: restarts}
	r.mu.Unlock()

	go func() {
		config := wazero.NewModuleConfig().WithStartFunctions("_start").WithStdout(os.Stdout).WithStderr(os.Stderr)
		exitCode, runErr := rt.Run(ctx, wasmBytes, config)
		rt.Close(ctx)

		code := int(exitCode)
		if runErr != nil {
			code = -1
		}
		r.exited <- exitEvent{key: key, exitCode: code}
	}()
	return nil
}

func (r *processRuntime) restart(pod *v1.Pod) error {
	key := podKey(pod)
	r.mu.Lock()
	if tp, ok := r.procs[key]; ok {
		tp.restarts++
	}
	r.mu.Unlock()
	return r.start(pod)
}

func (r *processRuntime) stop(key string) {
	r.mu.Lock()
	tp, ok := r.procs[key]
	delete(r.procs, key)
	r.mu.Unlock()

	if !ok {
		return
	}
	if tp.cmd != nil && tp.cmd.Process != nil {
		_ = tp.cmd.Process.Kill()
	}
	if tp.wasmRuntime != nil {
		_ = tp.wasmRuntime.Close(context.Background())
	}
}

func (r *processRuntime) stopAll() {
	for _, key := range r.keys() {
		r.stop(key)
	}
}

func (r *processRuntime) podOf(key string) *v1.Pod {
	r.mu.Lock()
	defer r.mu.Unlock()
	if tp, ok := r.procs[key]; ok {
		return tp.pod
	}
	return nil
}
