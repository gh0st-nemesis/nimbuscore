package gitops

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"google.golang.org/protobuf/encoding/protojson"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
)

type Config struct {
	RepoURL string
	Branch  string
	Path    string
	WorkDir string
}

type Reconciler struct {
	cfg         Config
	deployments *registry.Registry[*v1.Deployment]
	resync      time.Duration
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
		return fmt.Errorf("gitops: fetch: %w", err)
	}

	manifestDir := filepath.Join(r.cfg.WorkDir, r.cfg.Path)
	entries, err := os.ReadDir(manifestDir)
	if err != nil {
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

		if err := r.deployments.Put(ctx, meta.GetNamespace(), meta.GetName(), &d); err != nil {
			log.Printf("gitops-reconciler: apply %s/%s: %v", meta.GetNamespace(), meta.GetName(), err)
			continue
		}
		log.Printf("gitops-reconciler: applied %s/%s from %s", meta.GetNamespace(), meta.GetName(), e.Name())
	}
	return nil
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
