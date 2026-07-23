package agent

import (
	"os"
	"testing"
	"time"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)

	args := os.Args
	for len(args) > 0 {
		if args[0] == "--" {
			args = args[1:]
			break
		}
		args = args[1:]
	}
	if len(args) == 0 {
		os.Exit(2)
	}

	switch args[0] {
	case "exit0":
		os.Exit(0)
	case "exit1":
		os.Exit(1)
	case "sleep":
		time.Sleep(10 * time.Second)
	}
}

func helperCommand(t *testing.T, verb string) []string {
	t.Helper()
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return []string{exe, "-test.run=TestHelperProcess", "--", verb}
}

func testPod(name string, cmd []string, restartPolicy v1.RestartPolicy) *v1.Pod {
	return &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: &v1.PodSpec{
			NodeName:      "test-node",
			RestartPolicy: restartPolicy,
			Containers:    []*v1.Container{{Name: "app", Command: cmd}},
		},
		Status: &v1.PodStatus{Phase: v1.PodPhase_POD_PHASE_PENDING},
	}
}

func TestProcessRuntimeStartAndExit(t *testing.T) {
	r := newProcessRuntime()
	pod := testPod("exits", helperCommand(t, "exit0"), v1.RestartPolicy_RESTART_POLICY_NEVER)

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
		t.Fatal("timed out waiting for process exit")
	}
}

func TestProcessRuntimeNonZeroExit(t *testing.T) {
	r := newProcessRuntime()
	pod := testPod("fails", helperCommand(t, "exit1"), v1.RestartPolicy_RESTART_POLICY_NEVER)

	if err := r.start(pod); err != nil {
		t.Fatalf("start: %v", err)
	}

	select {
	case ev := <-r.exited:
		if ev.exitCode != 1 {
			t.Errorf("exit code = %d, want 1", ev.exitCode)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for process exit")
	}
}

func TestProcessRuntimeStopKillsRunningProcess(t *testing.T) {
	r := newProcessRuntime()
	pod := testPod("sleeper", helperCommand(t, "sleep"), v1.RestartPolicy_RESTART_POLICY_NEVER)

	if err := r.start(pod); err != nil {
		t.Fatalf("start: %v", err)
	}
	key := podKey(pod)
	if !r.has(key) {
		t.Fatal("runtime does not report the process as tracked")
	}

	r.stop(key)
	if r.has(key) {
		t.Error("process still tracked after stop")
	}

	select {
	case <-r.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for killed process to report exit")
	}
}

func TestProcessRuntimeReportsMemoryUsage(t *testing.T) {
	r := newProcessRuntime()
	pod := testPod("sleeper", helperCommand(t, "sleep"), v1.RestartPolicy_RESTART_POLICY_NEVER)

	if err := r.start(pod); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer r.stop(podKey(pod))

	deadline := time.Now().Add(3 * time.Second)
	var usage int64
	for time.Now().Before(deadline) {
		usage = r.memoryUsageBytes(podKey(pod))
		if usage > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if usage <= 0 {
		t.Fatal("memoryUsageBytes returned 0 for a live process")
	}
}

func TestProcessRuntimeRestartIncrementsCount(t *testing.T) {
	r := newProcessRuntime()
	pod := testPod("restarter", helperCommand(t, "sleep"), v1.RestartPolicy_RESTART_POLICY_ALWAYS)

	if err := r.start(pod); err != nil {
		t.Fatalf("start: %v", err)
	}
	key := podKey(pod)
	if got := r.restartCount(key); got != 0 {
		t.Fatalf("restart count = %d, want 0 before any restart", got)
	}

	if err := r.restart(pod); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if got := r.restartCount(key); got != 1 {
		t.Errorf("restart count = %d, want 1", got)
	}
	r.stop(key)
}
