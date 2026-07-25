package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

const testBuildkitAddr = "unix:///run/buildkit/buildkitd.sock"

func buildkitAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("buildctl"); err != nil {
		t.Skip("buildctl not available on PATH")
	}
	if err := exec.Command("buildctl", "--addr", testBuildkitAddr, "debug", "workers").Run(); err != nil {
		t.Skip("buildkitd not reachable at " + testBuildkitAddr)
	}
}

func newTestDockerfileRepo(t *testing.T, dockerfile string) string {
	t.Helper()
	dir := t.TempDir()

	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if _, err := wt.Add("Dockerfile"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := wt.Commit("initial", &git.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@example.com", When: time.Now()},
	}); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return dir
}

func TestBuildImageFromSourceProducesRunnableImage(t *testing.T) {
	containerdAvailable(t)
	buildkitAvailable(t)

	repoDir := newTestDockerfileRepo(t, "FROM alpine:latest\nRUN echo built-by-test > /marker.txt\nCMD [\"cat\", \"/marker.txt\"]\n")

	containerID := "nimbus-test-buildsource"
	defer removeContainerdContainer(containerID)
	defer func() {
		exec.Command("ctr", "images", "rm", "docker.io/library/"+containerID+":latest").Run() //nolint:errcheck
	}()

	src := &v1.BuildSource{RepoUrl: "file://" + filepath.ToSlash(repoDir), Branch: "master"}
	logDir := t.TempDir()
	workDir := filepath.Join(t.TempDir(), "build")

	tag, err := buildImageFromSource(context.Background(), testBuildkitAddr, workDir, logDir, containerID, src)
	if err != nil {
		buildLog, _ := os.ReadFile(buildLogFilePath(logDir, containerID))
		t.Fatalf("buildImageFromSource: %v\nbuild log:\n%s", err, buildLog)
	}
	wantTag := "docker.io/library/" + containerID + ":latest"
	if tag != wantTag {
		t.Errorf("tag = %q, want %q", tag, wantTag)
	}

	out, err := exec.Command("ctr", "run", "--rm", tag, "buildsource-probe").CombinedOutput()
	if err != nil {
		t.Fatalf("ctr run %s: %v: %s", tag, err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "built-by-test" {
		t.Errorf("container output = %q, want %q", got, "built-by-test")
	}

	buildLog, err := os.ReadFile(buildLogFilePath(logDir, containerID))
	if err != nil {
		t.Fatalf("read build log: %v", err)
	}
	if len(buildLog) == 0 {
		t.Error("build log is empty, want buildctl output")
	}
}

func TestContainerdRuntimeStartsPodFromGitBuildSource(t *testing.T) {
	containerdAvailable(t)
	buildkitAvailable(t)

	repoDir := newTestDockerfileRepo(t, "FROM alpine:latest\nCMD [\"sleep\", \"60\"]\n")

	pod := &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: "ctr-build-pod", Namespace: "default"},
		Spec: &v1.PodSpec{
			NodeName:      "test-node",
			RestartPolicy: v1.RestartPolicy_RESTART_POLICY_NEVER,
			Containers: []*v1.Container{{
				Name:        "app",
				BuildSource: &v1.BuildSource{RepoUrl: "file://" + filepath.ToSlash(repoDir), Branch: "master"},
			}},
		},
		Status: &v1.PodStatus{Phase: v1.PodPhase_POD_PHASE_PENDING},
	}
	id := containerName(pod)
	defer removeContainerdContainer(id)
	defer func() {
		exec.Command("ctr", "images", "rm", "docker.io/library/"+id+":latest").Run() //nolint:errcheck
	}()

	r := newProcessRuntime()
	r.buildkitAddr = testBuildkitAddr
	r.buildDir = filepath.Join(t.TempDir(), "builds")
	r.logDir = t.TempDir()

	if err := r.start(pod); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer r.stop(podKey(pod))

	statuses, err := listContainerdTaskStatuses()
	if err != nil {
		t.Fatalf("listContainerdTaskStatuses: %v", err)
	}
	if statuses[id] != "RUNNING" {
		t.Errorf("task %s status = %q, want RUNNING", id, statuses[id])
	}
}
