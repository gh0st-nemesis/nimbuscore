package gitops

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

func newTestGitRepo(t *testing.T, manifests map[string]string) string {
	t.Helper()
	repoDir := t.TempDir()

	repo, err := git.PlainInit(repoDir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}

	for name, content := range manifests {
		path := filepath.Join(repoDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
		if _, err := wt.Add(name); err != nil {
			t.Fatalf("git add %s: %v", name, err)
		}
	}

	if _, err := wt.Commit("initial manifests", &git.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@example.com", When: time.Now()},
	}); err != nil {
		t.Fatalf("commit: %v", err)
	}

	head, err := repo.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	branchName := head.Name().Short()
	if branchName != "master" && branchName != "main" {
		t.Fatalf("unexpected default branch %q", branchName)
	}
	t.Setenv("_gitops_test_branch", branchName)

	return repoDir
}

const deploymentManifest = `{
  "metadata": {"name": "web", "namespace": "default"},
  "spec": {
    "replicas": 2,
    "selector": {"app": "web"},
    "template": {"containers": [{"name": "web", "image": "nginx:v1"}]}
  }
}`

func TestSyncAppliesManifestsFromGit(t *testing.T) {
	repoDir := newTestGitRepo(t, map[string]string{
		"manifests/web.json": deploymentManifest,
	})
	branch := os.Getenv("_gitops_test_branch")

	s := store.NewMemStore()
	deployments := registry.New(s, "deployments", func() *v1.Deployment { return &v1.Deployment{} })

	r := NewReconciler(Config{
		RepoURL: "file://" + filepath.ToSlash(repoDir),
		Branch:  branch,
		Path:    "manifests",
		WorkDir: t.TempDir(),
	}, deployments, 0)

	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	got, err := deployments.Get(context.Background(), "default", "web")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.GetSpec().GetReplicas() != 2 {
		t.Errorf("replicas = %d, want 2", got.GetSpec().GetReplicas())
	}
	if got.GetSpec().GetTemplate().GetContainers()[0].GetImage() != "nginx:v1" {
		t.Errorf("image = %q, want nginx:v1", got.GetSpec().GetTemplate().GetContainers()[0].GetImage())
	}
}

func TestSyncPicksUpSubsequentCommits(t *testing.T) {
	repoDir := newTestGitRepo(t, map[string]string{
		"manifests/web.json": deploymentManifest,
	})
	branch := os.Getenv("_gitops_test_branch")

	s := store.NewMemStore()
	deployments := registry.New(s, "deployments", func() *v1.Deployment { return &v1.Deployment{} })

	r := NewReconciler(Config{
		RepoURL: "file://" + filepath.ToSlash(repoDir),
		Branch:  branch,
		Path:    "manifests",
		WorkDir: t.TempDir(),
	}, deployments, 0)

	ctx := context.Background()
	if err := r.Sync(ctx); err != nil {
		t.Fatalf("Sync (initial): %v", err)
	}

	repo, err := git.PlainOpen(repoDir)
	if err != nil {
		t.Fatalf("PlainOpen: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	updated := `{
  "metadata": {"name": "web", "namespace": "default"},
  "spec": {
    "replicas": 5,
    "selector": {"app": "web"},
    "template": {"containers": [{"name": "web", "image": "nginx:v2"}]}
  }
}`
	if err := os.WriteFile(filepath.Join(repoDir, "manifests", "web.json"), []byte(updated), 0o644); err != nil {
		t.Fatalf("update manifest: %v", err)
	}
	if _, err := wt.Add("manifests/web.json"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := wt.Commit("scale up", &git.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@example.com", When: time.Now()},
	}); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if err := r.Sync(ctx); err != nil {
		t.Fatalf("Sync (after update): %v", err)
	}

	got, err := deployments.Get(ctx, "default", "web")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.GetSpec().GetReplicas() != 5 {
		t.Errorf("replicas = %d, want 5 after pulling the new commit", got.GetSpec().GetReplicas())
	}
}

func TestSyncRecordsStatusAndLabelsAppliedDeployments(t *testing.T) {
	repoDir := newTestGitRepo(t, map[string]string{
		"manifests/web.json": deploymentManifest,
	})
	branch := os.Getenv("_gitops_test_branch")

	s := store.NewMemStore()
	deployments := registry.New(s, "deployments", func() *v1.Deployment { return &v1.Deployment{} })

	r := NewReconciler(Config{
		RepoURL: "file://" + filepath.ToSlash(repoDir),
		Branch:  branch,
		Path:    "manifests",
		WorkDir: t.TempDir(),
	}, deployments, 0)

	before := r.Status()
	if before.LastSyncUnix != 0 {
		t.Fatalf("status before any Sync: %+v, want zero-value", before)
	}

	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	status := r.Status()
	if status.LastError != "" {
		t.Errorf("status.LastError = %q, want empty after a successful sync", status.LastError)
	}
	if status.LastSyncUnix == 0 {
		t.Error("status.LastSyncUnix is still 0 after a successful sync")
	}
	if status.LastCommit == "" {
		t.Error("status.LastCommit is empty after a successful sync")
	}
	if status.RepoURL != r.cfg.RepoURL || status.Branch != branch || status.Path != "manifests" {
		t.Errorf("status = %+v, repo/branch/path do not match config", status)
	}

	got, err := deployments.Get(context.Background(), "default", "web")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.GetMetadata().GetLabels()[ManagedByLabel] != ManagedByValue {
		t.Errorf("labels = %v, want %s=%s", got.GetMetadata().GetLabels(), ManagedByLabel, ManagedByValue)
	}
}

func TestSyncSkipsManifestsMissingName(t *testing.T) {
	repoDir := newTestGitRepo(t, map[string]string{
		"manifests/broken.json": `{"spec": {"replicas": 1}}`,
	})
	branch := os.Getenv("_gitops_test_branch")

	s := store.NewMemStore()
	deployments := registry.New(s, "deployments", func() *v1.Deployment { return &v1.Deployment{} })

	r := NewReconciler(Config{
		RepoURL: "file://" + filepath.ToSlash(repoDir),
		Branch:  branch,
		Path:    "manifests",
		WorkDir: t.TempDir(),
	}, deployments, 0)

	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	all, err := deployments.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("expected the nameless manifest to be skipped, got %d deployments", len(all))
	}
}
