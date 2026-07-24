package gitops

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"google.golang.org/protobuf/encoding/protojson"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
)

const ManagedByLabel = "nimbuscore.io/managed-by"
const ManagedByValue = "gitops"

type Config struct {
	RepoURL string
	Branch  string
	Path    string
	WorkDir string
}

type Status struct {
	RepoURL      string
	Branch       string
	Path         string
	LastSyncUnix int64
	LastCommit   string
	LastError    string
}

type Reconciler struct {
	cfg         Config
	deployments *registry.Registry[*v1.Deployment]
	resync      time.Duration

	mu     sync.RWMutex
	status Status
}

func NewReconciler(cfg Config, deployments *registry.Registry[*v1.Deployment], resync time.Duration) *Reconciler {
	if resync <= 0 {
		resync = 30 * time.Second
	}
	if cfg.Branch == "" {
		cfg.Branch = "main"
	}
	return &Reconciler{cfg: cfg, deployments: deployments, resync: resync}
}

func (r *Reconciler) Name() string { return "gitops-reconciler" }

func (r *Reconciler) Status() Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

func (r *Reconciler) Reconcile(ctx context.Context) error {
	ticker := time.NewTicker(r.resync)
	defer ticker.Stop()

	if err := r.Sync(ctx); err != nil {
		log.Printf("gitops-reconciler: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := r.Sync(ctx); err != nil {
				log.Printf("gitops-reconciler: %v", err)
			}
		}
	}
}

func (r *Reconciler) Sync(ctx context.Context) error {
	if err := r.fetchRepo(ctx); err != nil {
		r.recordResult("", err)
		return fmt.Errorf("gitops: fetch: %w", err)
	}
	commit, err := r.headCommit()
	if err != nil {
		log.Printf("gitops-reconciler: read HEAD commit: %v", err)
	}

	manifestDir := filepath.Join(r.cfg.WorkDir, r.cfg.Path)
	entries, err := os.ReadDir(manifestDir)
	if err != nil {
		r.recordResult(commit, err)
		return fmt.Errorf("gitops: read manifest dir: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		b, err := os.ReadFile(filepath.Join(manifestDir, e.Name()))
		if err != nil {
			log.Printf("gitops-reconciler: read %s: %v", e.Name(), err)
			continue
		}

		var d v1.Deployment
		if err := protojson.Unmarshal(b, &d); err != nil {
			log.Printf("gitops-reconciler: parse %s: %v", e.Name(), err)
			continue
		}

		meta := d.GetMetadata()
		if meta.GetName() == "" {
			log.Printf("gitops-reconciler: %s is missing metadata.name, skipping", e.Name())
			continue
		}
		if meta.Labels == nil {
			meta.Labels = make(map[string]string)
		}
		meta.Labels[ManagedByLabel] = ManagedByValue

		if err := r.deployments.Put(ctx, meta.GetNamespace(), meta.GetName(), &d); err != nil {
			log.Printf("gitops-reconciler: apply %s/%s: %v", meta.GetNamespace(), meta.GetName(), err)
			continue
		}
		log.Printf("gitops-reconciler: applied %s/%s from %s", meta.GetNamespace(), meta.GetName(), e.Name())
	}
	r.recordResult(commit, nil)
	return nil
}

func (r *Reconciler) recordResult(commit string, syncErr error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status.RepoURL = r.cfg.RepoURL
	r.status.Branch = r.cfg.Branch
	r.status.Path = r.cfg.Path
	r.status.LastSyncUnix = time.Now().Unix()
	if commit != "" {
		r.status.LastCommit = commit
	}
	if syncErr != nil {
		r.status.LastError = syncErr.Error()
	} else {
		r.status.LastError = ""
	}
}

func (r *Reconciler) headCommit() (string, error) {
	repo, err := git.PlainOpen(r.cfg.WorkDir)
	if err != nil {
		return "", err
	}
	head, err := repo.Head()
	if err != nil {
		return "", err
	}
	return head.Hash().String(), nil
}

func (r *Reconciler) fetchRepo(ctx context.Context) error {
	ref := plumbing.NewBranchReferenceName(r.cfg.Branch)

	if _, err := os.Stat(filepath.Join(r.cfg.WorkDir, ".git")); os.IsNotExist(err) {
		_, err := git.PlainCloneContext(ctx, r.cfg.WorkDir, false, &git.CloneOptions{
			URL:           r.cfg.RepoURL,
			ReferenceName: ref,
			SingleBranch:  true,
		})
		return err
	}

	repo, err := git.PlainOpen(r.cfg.WorkDir)
	if err != nil {
		return fmt.Errorf("open working copy: %w", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("open worktree: %w", err)
	}

	err = wt.PullContext(ctx, &git.PullOptions{ReferenceName: ref, SingleBranch: true})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return fmt.Errorf("pull: %w", err)
	}
	return nil
}
