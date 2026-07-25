package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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
	containerID string
	restarts    int32
}

type processRuntime struct {
	mu           sync.Mutex
	procs        map[string]*trackedProcess
	exited       chan exitEvent
	logDir       string
	buildDir     string
	buildkitAddr string
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

func logFilePath(logDir, containerID string) string {
	return filepath.Join(logDir, containerID+".log")
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
	if !ok {
		return 0
	}

	var pid int32
	switch {
	case tp.containerID != "":
		found, err := containerdTaskPID(tp.containerID)
		if err != nil {
			return 0
		}
		pid = found
	case tp.cmd != nil && tp.cmd.Process != nil:
		pid = int32(tp.cmd.Process.Pid)
	default:
		return 0
	}

	proc, err := process.NewProcess(pid)
	if err != nil {
		return 0
	}
	info, err := proc.MemoryInfo()
	if err != nil || info == nil {
		return 0
	}
	return int64(info.RSS)
}

func containerdTaskPID(containerID string) (int32, error) {
	out, err := exec.Command("ctr", "task", "ls").Output()
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == containerID {
			pid, err := strconv.Atoi(fields[1])
			if err != nil {
				return 0, err
			}
			return int32(pid), nil
		}
	}
	return 0, fmt.Errorf("agent: task %s not found in ctr task ls", containerID)
}

func firstContainer(pod *v1.Pod) *v1.Container {
	containers := pod.GetSpec().GetContainers()
	if len(containers) == 0 {
		return nil
	}
	return containers[0]
}

func (r *processRuntime) start(pod *v1.Pod) error {
	c := firstContainer(pod)
	switch {
	case c.GetWasmModulePath() != "":
		return r.startWASM(pod, c.GetWasmModulePath())
	case c.GetImage() != "" || c.GetBuildSource() != nil:
		return r.startContainerd(pod)
	default:
		return r.startProcess(pod)
	}
}

func containerName(pod *v1.Pod) string {
	return containerNameRaw(pod.GetMetadata().GetNamespace(), pod.GetMetadata().GetName())
}

func containerNameRaw(namespace, name string) string {
	return fmt.Sprintf("nimbus-%s-%s", namespace, name)
}

func removeContainerdContainer(id string) {
	for i := 0; i < 30; i++ {
		if exec.Command("ctr", "task", "kill", "-s", "SIGKILL", id).Run() == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	for i := 0; i < 30; i++ {
		out, err := exec.Command("ctr", "task", "ls").Output()
		if err == nil && !strings.Contains(string(out), id) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	exec.Command("ctr", "task", "rm", id).Run()      //nolint:errcheck
	exec.Command("ctr", "container", "rm", id).Run() //nolint:errcheck
}

func resolveImageRef(ref string) string {
	return ensureTagOrDigest(expandRegistry(ref))
}

func expandRegistry(ref string) string {
	if strings.HasPrefix(ref, "docker.io/") || strings.HasPrefix(ref, "localhost/") {
		return ref
	}
	slash := strings.Index(ref, "/")
	if slash == -1 {
		return "docker.io/library/" + ref
	}
	firstSegment := ref[:slash]
	if strings.ContainsAny(firstSegment, ".:") || firstSegment == "localhost" {
		return ref
	}
	return "docker.io/" + ref
}

func ensureTagOrDigest(ref string) string {
	if strings.Contains(ref, "@") {
		return ref
	}
	lastSlash := strings.LastIndex(ref, "/")
	lastColon := strings.LastIndex(ref, ":")
	if lastColon > lastSlash {
		return ref
	}
	return ref + ":latest"
}

func (r *processRuntime) startContainerd(pod *v1.Pod) error {
	key := podKey(pod)
	c := firstContainer(pod)
	id := containerName(pod)

	removeContainerdContainer(id)

	var image string
	if src := c.GetBuildSource(); src != nil {
		buildDir := r.buildDir
		if buildDir == "" {
			buildDir = filepath.Join(os.TempDir(), "nimbus-builds")
		}
		buildkitAddr := r.buildkitAddr
		if buildkitAddr == "" {
			buildkitAddr = "unix:///run/buildkit/buildkitd.sock"
		}
		built, err := buildImageFromSource(context.Background(), buildkitAddr, filepath.Join(buildDir, id), r.logDir, id, src)
		if err != nil {
			return err
		}
		image = built
	} else {
		image = resolveImageRef(c.GetImage())
		if out, err := exec.Command("ctr", "images", "pull", image).CombinedOutput(); err != nil {
			return fmt.Errorf("agent: ctr images pull %s: %w: %s", image, err, strings.TrimSpace(string(out)))
		}
	}

	args := []string{"run", "-d", "--net-host"}
	if r.logDir != "" {
		if err := os.MkdirAll(r.logDir, 0o755); err != nil {
			return fmt.Errorf("agent: create log dir %s: %w", r.logDir, err)
		}
		logPath, err := filepath.Abs(logFilePath(r.logDir, id))
		if err != nil {
			return fmt.Errorf("agent: resolve log path: %w", err)
		}
		os.Remove(logPath) //nolint:errcheck
		args = append(args, "--log-uri", "file://"+logPath)
	}
	if limits := c.GetResources().GetLimits(); limits != nil {
		if millis := limits.GetCpuMillis(); millis > 0 {
			args = append(args, "--cpus", fmt.Sprintf("%.3f", float64(millis)/1000))
		}
		if mem := limits.GetMemoryBytes(); mem > 0 {
			args = append(args, "--memory-limit", strconv.FormatInt(mem, 10))
		}
	}
	envKeys := make([]string, 0, len(c.GetEnv()))
	for k := range c.GetEnv() {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	for _, k := range envKeys {
		args = append(args, "--env", k+"="+c.GetEnv()[k])
	}
	args = append(args, image, id)
	args = append(args, c.GetCommand()...)

	if out, err := exec.Command("ctr", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("agent: ctr run %s: %w: %s", image, err, strings.TrimSpace(string(out)))
	}

	r.mu.Lock()
	restarts := int32(0)
	if existing, ok := r.procs[key]; ok {
		restarts = existing.restarts
	}
	r.procs[key] = &trackedProcess{pod: pod, containerID: id, restarts: restarts}
	r.mu.Unlock()
	return nil
}

func listContainerdTaskStatuses() (map[string]string, error) {
	out, err := exec.Command("ctr", "task", "ls").Output()
	if err != nil {
		return nil, err
	}
	statuses := make(map[string]string)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] == "TASK" {
			continue
		}
		statuses[fields[0]] = fields[2]
	}
	return statuses, nil
}

func (r *processRuntime) adoptIfRunning(pod *v1.Pod) bool {
	c := firstContainer(pod)
	if c.GetImage() == "" && c.GetBuildSource() == nil {
		return false
	}
	id := containerName(pod)
	statuses, err := listContainerdTaskStatuses()
	if err != nil || statuses[id] != "RUNNING" {
		return false
	}

	key := podKey(pod)
	r.mu.Lock()
	r.procs[key] = &trackedProcess{pod: pod, containerID: id}
	r.mu.Unlock()
	return true
}

func (r *processRuntime) checkContainerdExits() {
	statuses, err := listContainerdTaskStatuses()
	if err != nil {
		return
	}

	r.mu.Lock()
	var exits []exitEvent
	for key, tp := range r.procs {
		if tp.containerID == "" {
			continue
		}
		if statuses[tp.containerID] == "RUNNING" {
			continue
		}
		exits = append(exits, exitEvent{key: key, exitCode: -1})
	}
	r.mu.Unlock()

	for _, ev := range exits {
		r.exited <- ev
	}
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
	if tp.containerID != "" {
		removeContainerdContainer(tp.containerID)
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
