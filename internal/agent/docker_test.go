package agent

import (
	"os/exec"
	"runtime"
	"testing"
	"time"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

func dockerAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available on PATH")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon not reachable")
	}
}

func dockerPod(name, image string, command []string) *v1.Pod {
	return &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: &v1.PodSpec{
			NodeName:      "test-node",
			RestartPolicy: v1.RestartPolicy_RESTART_POLICY_NEVER,
			Containers:    []*v1.Container{{Name: "app", Image: image, Command: command}},
		},
		Status: &v1.PodStatus{Phase: v1.PodPhase_POD_PHASE_PENDING},
	}
}

func TestDockerRuntimeStartAndExit(t *testing.T) {
	dockerAvailable(t)

	r := newProcessRuntime()
	pod := dockerPod("docker-exit0", "alpine:latest", []string{"true"})
	defer exec.Command("docker", "rm", "-f", dockerContainerName(pod)).Run()

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
	case <-time.After(60 * time.Second):
		t.Fatal("timed out waiting for docker container exit")
	}
}

func TestDockerRuntimeNonZeroExit(t *testing.T) {
	dockerAvailable(t)

	r := newProcessRuntime()
	pod := dockerPod("docker-exit1", "alpine:latest", []string{"false"})
	defer exec.Command("docker", "rm", "-f", dockerContainerName(pod)).Run()

	if err := r.start(pod); err != nil {
		t.Fatalf("start: %v", err)
	}

	select {
	case ev := <-r.exited:
		if ev.exitCode != 1 {
			t.Errorf("exit code = %d, want 1", ev.exitCode)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("timed out waiting for docker container exit")
	}
}

func TestDockerRuntimeStopRemovesContainer(t *testing.T) {
	dockerAvailable(t)

	r := newProcessRuntime()
	pod := dockerPod("docker-sleeper", "alpine:latest", []string{"sleep", "60"})
	name := dockerContainerName(pod)
	defer exec.Command("docker", "rm", "-f", name).Run()

	if err := r.start(pod); err != nil {
		t.Fatalf("start: %v", err)
	}
	key := podKey(pod)
	if !r.has(key) {
		t.Fatal("runtime does not report the container as tracked")
	}

	r.stop(key)
	if r.has(key) {
		t.Error("container still tracked after stop")
	}

	out, err := exec.Command("docker", "ps", "-a", "--filter", "name=^"+name+"$", "--format", "{{.Names}}").Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("container %q still exists after stop: %q", name, out)
	}
}

func TestDockerRuntimeReportsMemoryUsage(t *testing.T) {
	dockerAvailable(t)
	if runtime.GOOS != "linux" {
		t.Skipf("docker inspect .State.Pid is only host-visible to gopsutil when the daemon runs natively on Linux — on %s (e.g. Docker Desktop's WSL2/Hyper-V VM) the reported PID belongs to a different process table", runtime.GOOS)
	}

	r := newProcessRuntime()
	pod := dockerPod("docker-mem", "alpine:latest", []string{"sleep", "60"})
	defer exec.Command("docker", "rm", "-f", dockerContainerName(pod)).Run()

	if err := r.start(pod); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer r.stop(podKey(pod))

	deadline := time.Now().Add(15 * time.Second)
	var usage int64
	for time.Now().Before(deadline) {
		usage = r.memoryUsageBytes(podKey(pod))
		if usage > 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if usage <= 0 {
		t.Fatal("memoryUsageBytes returned 0 for a live docker container")
	}
}
