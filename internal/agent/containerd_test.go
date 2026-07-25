package agent

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

func containerdAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ctr"); err != nil {
		t.Skip("ctr not available on PATH")
	}
	if err := exec.Command("ctr", "version").Run(); err != nil {
		t.Skip("containerd daemon not reachable")
	}
}

func containerdPod(name, image string, command []string) *v1.Pod {
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

func TestResolveImageRef(t *testing.T) {
	cases := map[string]string{
		"nginx:alpine":                        "docker.io/library/nginx:alpine",
		"nginx":                               "docker.io/library/nginx:latest",
		"alpine":                              "docker.io/library/alpine:latest",
		"myuser/myimage:tag":                  "docker.io/myuser/myimage:tag",
		"myuser/myimage":                      "docker.io/myuser/myimage:latest",
		"gcr.io/project/image:tag":            "gcr.io/project/image:tag",
		"gcr.io/project/image":                "gcr.io/project/image:latest",
		"localhost:5000/myimage":              "localhost:5000/myimage:latest",
		"localhost:5000/myimage:tag":          "localhost:5000/myimage:tag",
		"docker.io/library/nginx:alpine":      "docker.io/library/nginx:alpine",
		"docker.io/library/nginx@sha256:abcd": "docker.io/library/nginx@sha256:abcd",
	}
	for in, want := range cases {
		if got := resolveImageRef(in); got != want {
			t.Errorf("resolveImageRef(%q) = %q, want %q", in, got, want)
		}
	}
}

func pollUntilExit(t *testing.T, r *processRuntime, timeout time.Duration) exitEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r.checkContainerdExits()
		select {
		case ev := <-r.exited:
			return ev
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Fatal("timed out waiting for containerd task exit")
	return exitEvent{}
}

func TestContainerdRuntimeStartAndExit(t *testing.T) {
	containerdAvailable(t)

	r := newProcessRuntime()
	pod := containerdPod("ctr-exit0", "alpine:latest", []string{"true"})
	defer removeContainerdContainer(containerName(pod))

	if err := r.start(pod); err != nil {
		t.Fatalf("start: %v", err)
	}

	ev := pollUntilExit(t, r, 30*time.Second)
	if ev.key != podKey(pod) {
		t.Errorf("exit event key = %q, want %q", ev.key, podKey(pod))
	}
}

func TestContainerdRuntimeNonZeroExit(t *testing.T) {
	containerdAvailable(t)

	r := newProcessRuntime()
	pod := containerdPod("ctr-exit1", "docker.io/library/alpine:latest", []string{"false"})
	defer removeContainerdContainer(containerName(pod))

	if err := r.start(pod); err != nil {
		t.Fatalf("start: %v", err)
	}

	pollUntilExit(t, r, 30*time.Second)
}

func TestContainerdRuntimeSurvivesAgentRestartViaAdoption(t *testing.T) {
	containerdAvailable(t)

	pod := containerdPod("ctr-adopt", "docker.io/library/alpine:latest", []string{"sleep", "60"})
	id := containerName(pod)
	defer removeContainerdContainer(id)

	original := newProcessRuntime()
	if err := original.start(pod); err != nil {
		t.Fatalf("start: %v", err)
	}
	originalPID, err := containerdTaskPID(id)
	if err != nil {
		t.Fatalf("containerdTaskPID: %v", err)
	}

	restarted := newProcessRuntime()
	if !restarted.adoptIfRunning(pod) {
		t.Fatal("adoptIfRunning = false, want true for an already-running containerd task")
	}
	if restarted.restartCount(podKey(pod)) != 0 {
		t.Errorf("restartCount = %d, want 0 (adoption should not count as a restart)", restarted.restartCount(podKey(pod)))
	}

	adoptedPID, err := containerdTaskPID(id)
	if err != nil {
		t.Fatalf("containerdTaskPID after adoption: %v", err)
	}
	if adoptedPID != originalPID {
		t.Errorf("task PID changed after adoption (%d -> %d), the container was restarted instead of adopted", originalPID, adoptedPID)
	}
}

func TestContainerdRuntimeDoesNotAdoptStoppedTask(t *testing.T) {
	containerdAvailable(t)

	r := newProcessRuntime()
	pod := containerdPod("ctr-adopt-stopped", "docker.io/library/alpine:latest", []string{"true"})
	defer removeContainerdContainer(containerName(pod))

	if err := r.start(pod); err != nil {
		t.Fatalf("start: %v", err)
	}
	pollUntilExit(t, r, 30*time.Second)

	fresh := newProcessRuntime()
	if fresh.adoptIfRunning(pod) {
		t.Error("adoptIfRunning = true for a STOPPED task, want false")
	}
}

func TestContainerdRuntimePassesEnvVars(t *testing.T) {
	containerdAvailable(t)

	pod := containerdPod("ctr-env", "docker.io/library/alpine:latest", []string{"sleep", "60"})
	pod.Spec.Containers[0].Env = map[string]string{"FOO": "bar-value"}
	id := containerName(pod)
	defer removeContainerdContainer(id)

	r := newProcessRuntime()
	if err := r.start(pod); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer r.stop(podKey(pod))

	out, err := exec.Command("ctr", "task", "exec", "--exec-id", "envcheck", id, "sh", "-c", "echo $FOO").CombinedOutput()
	if err != nil {
		t.Fatalf("ctr task exec: %v: %s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "bar-value" {
		t.Errorf("env var FOO in container = %q, want %q", got, "bar-value")
	}
}

func TestContainerdRuntimeWritesLogsToLogDir(t *testing.T) {
	containerdAvailable(t)

	dir := t.TempDir()
	r := newProcessRuntime()
	r.logDir = dir

	pod := containerdPod("ctr-logs", "docker.io/library/alpine:latest", []string{"sh", "-c", "echo hello-from-log-test; sleep 30"})
	id := containerName(pod)
	defer removeContainerdContainer(id)

	if err := r.start(pod); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer r.stop(podKey(pod))

	deadline := time.Now().Add(15 * time.Second)
	var content []byte
	var err error
	for time.Now().Before(deadline) {
		content, err = tailFile(logFilePath(dir, id), 100)
		if err == nil && strings.Contains(string(content), "hello-from-log-test") {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("log file never contained expected output (last read err=%v content=%q)", err, content)
}

func TestContainerdRuntimeStopRemovesTask(t *testing.T) {
	containerdAvailable(t)

	r := newProcessRuntime()
	pod := containerdPod("ctr-sleeper", "docker.io/library/alpine:latest", []string{"sleep", "60"})
	id := containerName(pod)
	defer removeContainerdContainer(id)

	if err := r.start(pod); err != nil {
		t.Fatalf("start: %v", err)
	}
	key := podKey(pod)
	if !r.has(key) {
		t.Fatal("runtime does not report the task as tracked")
	}

	r.stop(key)
	if r.has(key) {
		t.Error("task still tracked after stop")
	}

	out, err := exec.Command("ctr", "task", "ls").Output()
	if err != nil {
		t.Fatalf("ctr task ls: %v", err)
	}
	if strings.Contains(string(out), id) {
		t.Errorf("task %q still listed after stop: %q", id, out)
	}
}

func TestContainerdRuntimeReportsMemoryUsage(t *testing.T) {
	containerdAvailable(t)

	r := newProcessRuntime()
	pod := containerdPod("ctr-mem", "docker.io/library/alpine:latest", []string{"sleep", "60"})
	defer removeContainerdContainer(containerName(pod))

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
		t.Fatal("memoryUsageBytes returned 0 for a live containerd task")
	}
}
