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
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"

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

func newTestRepo(t *testing.T, files map[string]string) string {
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
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		if _, err := wt.Add(name); err != nil {
			t.Fatalf("git add %s: %v", name, err)
		}
	}
	if _, err := wt.Commit("initial", &git.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@example.com", When: time.Now()},
	}); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return dir
}

func newTestDockerfileRepo(t *testing.T, dockerfile string) string {
	t.Helper()
	return newTestRepo(t, map[string]string{"Dockerfile": dockerfile})
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
	if !statuses[id] {
		t.Errorf("task %s status = %v, want running", id, statuses[id])
	}
}

func TestCloneAuthEmptyTokenIsAnonymous(t *testing.T) {
	if auth := cloneAuth(""); auth != nil {
		t.Errorf("cloneAuth(\"\") = %v, want nil (anonymous clone)", auth)
	}
}

func TestCloneAuthUsesTokenAsBasicAuthPassword(t *testing.T) {
	auth := cloneAuth("gho_testtoken123")
	basic, ok := auth.(*githttp.BasicAuth)
	if !ok {
		t.Fatalf("cloneAuth returned %T, want *http.BasicAuth", auth)
	}
	if basic.Username != "x-access-token" {
		t.Errorf("username = %q, want x-access-token", basic.Username)
	}
	if basic.Password != "gho_testtoken123" {
		t.Errorf("password = %q, want the token", basic.Password)
	}
}

func writeContextFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestDetectDockerfileNodeWithStartScript(t *testing.T) {
	dir := t.TempDir()
	writeContextFiles(t, dir, map[string]string{"package.json": `{"scripts":{"start":"node server.js"}}`})

	got, err := detectDockerfile(dir)
	if err != nil {
		t.Fatalf("detectDockerfile: %v", err)
	}
	if !strings.Contains(string(got), "FROM node:21-alpine") {
		t.Errorf("expected node base image, got:\n%s", got)
	}
	if !strings.Contains(string(got), `CMD ["npm", "start"]`) {
		t.Errorf("expected npm start CMD, got:\n%s", got)
	}
}

func TestDetectDockerfileNodeWithMainFieldNoStartScript(t *testing.T) {
	dir := t.TempDir()
	writeContextFiles(t, dir, map[string]string{"package.json": `{"main":"server.js"}`})

	got, err := detectDockerfile(dir)
	if err != nil {
		t.Fatalf("detectDockerfile: %v", err)
	}
	if !strings.Contains(string(got), `CMD ["node", "server.js"]`) {
		t.Errorf("expected node server.js CMD, got:\n%s", got)
	}
}

func TestDetectDockerfileNodeDefaultsToIndexJS(t *testing.T) {
	dir := t.TempDir()
	writeContextFiles(t, dir, map[string]string{"package.json": `{}`})

	got, err := detectDockerfile(dir)
	if err != nil {
		t.Fatalf("detectDockerfile: %v", err)
	}
	if !strings.Contains(string(got), `CMD ["node", "index.js"]`) {
		t.Errorf("expected node index.js CMD, got:\n%s", got)
	}
}

func TestDetectDockerfileNodeWithBuildScriptRunsBuildBeforeStart(t *testing.T) {
	dir := t.TempDir()
	writeContextFiles(t, dir, map[string]string{
		"package.json": `{"scripts":{"build":"next build","start":"next start"}}`,
	})

	got, err := detectDockerfile(dir)
	if err != nil {
		t.Fatalf("detectDockerfile: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, "RUN npm install\n") {
		t.Errorf("expected a full (non --omit=dev) install ahead of the build step, got:\n%s", s)
	}
	if !strings.Contains(s, "RUN npm run build") {
		t.Errorf("expected the build script to run before start, got:\n%s", s)
	}
	if buildIdx, startIdx := strings.Index(s, "RUN npm run build"), strings.Index(s, `CMD ["npm", "start"]`); buildIdx == -1 || startIdx == -1 || buildIdx > startIdx {
		t.Errorf("expected npm run build before the start CMD, got:\n%s", s)
	}
}

func TestDetectDockerfileStaticSite(t *testing.T) {
	dir := t.TempDir()
	writeContextFiles(t, dir, map[string]string{"index.html": "<html></html>"})

	got, err := detectDockerfile(dir)
	if err != nil {
		t.Fatalf("detectDockerfile: %v", err)
	}
	if !strings.Contains(string(got), "FROM nginx:alpine") || !strings.Contains(string(got), "/usr/share/nginx/html") {
		t.Errorf("expected nginx static Dockerfile, got:\n%s", got)
	}
}

func TestDetectDockerfileNoSupportedStack(t *testing.T) {
	dir := t.TempDir()
	writeContextFiles(t, dir, map[string]string{"README.md": "just docs"})

	if _, err := detectDockerfile(dir); err == nil {
		t.Error("expected error for unrecognized stack, got nil")
	}
}

func TestBuildImageFromSourceAutoDetectsStaticSite(t *testing.T) {
	containerdAvailable(t)
	buildkitAvailable(t)

	repoDir := newTestRepo(t, map[string]string{"index.html": "built-by-test-static"})

	containerID := "nimbus-test-autodetect-static"
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

	out, err := exec.Command("ctr", "run", "--rm", tag, "autodetect-static-probe", "cat", "/usr/share/nginx/html/index.html").CombinedOutput()
	if err != nil {
		t.Fatalf("ctr run %s: %v: %s", tag, err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "built-by-test-static" {
		t.Errorf("container output = %q, want %q", got, "built-by-test-static")
	}
}

func TestBuildImageFromSourceAutoDetectsNodeApp(t *testing.T) {
	containerdAvailable(t)
	buildkitAvailable(t)

	repoDir := newTestRepo(t, map[string]string{
		"package.json": `{"name":"test-app","scripts":{"start":"node server.js"}}`,
		"server.js":    `console.log("built-by-test-node");`,
	})

	containerID := "nimbus-test-autodetect-node"
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

	out, err := exec.Command("ctr", "run", "--rm", tag, "autodetect-node-probe").CombinedOutput()
	if err != nil {
		t.Fatalf("ctr run %s: %v: %s", tag, err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "built-by-test-node" {
		t.Errorf("container output = %q, want %q", got, "built-by-test-node")
	}
}

func TestBuildImageFromSourceAutoDetectsNodeAppWithBuildStep(t *testing.T) {
	containerdAvailable(t)
	buildkitAvailable(t)

	// Mirrors frameworks like Next.js: "start" only works after "build" has
	// run, and here "build" needs a devDependency to prove --omit=dev alone
	// (the no-build-step path) would have broken it.
	repoDir := newTestRepo(t, map[string]string{
		"package.json": `{
			"name": "test-app",
			"scripts": {
				"build": "node build.js",
				"start": "node start.js"
			},
			"devDependencies": {
				"leftpad-marker": "file:./vendor/leftpad-marker"
			}
		}`,
		"build.js": `
			require("leftpad-marker");
			require("fs").writeFileSync("built.marker", "built-with-devdep");
		`,
		"start.js": `console.log(require("fs").readFileSync("built.marker", "utf8"));`,
		"vendor/leftpad-marker/package.json": `{"name":"leftpad-marker","version":"1.0.0","main":"index.js"}`,
		"vendor/leftpad-marker/index.js":     `module.exports = true;`,
	})

	containerID := "nimbus-test-autodetect-node-build"
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

	out, err := exec.Command("ctr", "run", "--rm", tag, "autodetect-node-build-probe").CombinedOutput()
	if err != nil {
		t.Fatalf("ctr run %s: %v: %s", tag, err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "built-with-devdep" {
		t.Errorf("container output = %q, want %q (build step must run, with devDependencies installed, before start)", got, "built-with-devdep")
	}
}

func TestBuildImageFromSourceExplicitDockerfilePathMissingStillFails(t *testing.T) {
	containerdAvailable(t)
	buildkitAvailable(t)

	repoDir := newTestRepo(t, map[string]string{"package.json": `{"scripts":{"start":"node server.js"}}`})

	containerID := "nimbus-test-explicit-path-missing"
	defer removeContainerdContainer(containerID)

	src := &v1.BuildSource{
		RepoUrl:        "file://" + filepath.ToSlash(repoDir),
		Branch:         "master",
		DockerfilePath: "nonexistent/Dockerfile",
	}
	logDir := t.TempDir()
	workDir := filepath.Join(t.TempDir(), "build")

	if _, err := buildImageFromSource(context.Background(), testBuildkitAddr, workDir, logDir, containerID, src); err == nil {
		t.Error("expected build to fail when explicit dockerfilePath is missing, got nil error")
	}
}
