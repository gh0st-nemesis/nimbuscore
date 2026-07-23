// Package controller implements the reconciliation-loop pattern (desired
// state vs. observed state) that is the central mechanism of the
// declarative model (design doc section 03). Detailed reconciler
// behavior is tracked as step C.
package controller

import (
	"context"
	"log"
	"sync"
	"time"
)

// Reconciler drives one resource kind toward its desired state. Each
// implementation runs in its own goroutine, isolated from the others —
// "chaque contrôleur isolé dans son propre espace d'exécution pour
// limiter le rayon d'explosion en cas de faille dans l'un d'eux."
type Reconciler interface {
	// Name identifies the reconciler in logs and diagnostics.
	Name() string
	// Reconcile runs until ctx is cancelled or an unrecoverable error
	// occurs.
	Reconcile(ctx context.Context) error
}

// Manager runs a set of registered Reconcilers concurrently.
type Manager struct {
	reconcilers []Reconciler
}

// NewManager returns an empty controller Manager.
func NewManager() *Manager {
	return &Manager{}
}

// Register adds a Reconciler to be started by Run.
func (m *Manager) Register(r Reconciler) {
	m.reconcilers = append(m.reconcilers, r)
}

// Run blocks until ctx is cancelled, running every registered reconciler
// in its own goroutine.
func (m *Manager) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, r := range m.reconcilers {
		wg.Add(1)
		go func(r Reconciler) {
			defer wg.Done()
			if err := r.Reconcile(ctx); err != nil && ctx.Err() == nil {
				log.Printf("controller %s: %v", r.Name(), err)
			}
		}(r)
	}
	wg.Wait()
}

// LeaderChecker reports whether the caller currently holds cluster
// leadership — satisfied by *store.RaftStore.
type LeaderChecker interface {
	IsLeader() bool
}

// RunWhileLeader runs mgr's reconcilers only while isLeader reports
// true, stopping them the moment leadership is lost and restarting them
// if it's regained. This is the same active-passive pattern Kubernetes'
// own controller-manager gets from client-go leader election, expressed
// directly in terms of the Raft leadership the store already tracks —
// reconciling on every replica at once would just make them race to
// write the same keys.
func RunWhileLeader(ctx context.Context, isLeader LeaderChecker, mgr *Manager, pollInterval time.Duration) {
	if pollInterval <= 0 {
		pollInterval = time.Second
	}

	for ctx.Err() == nil {
		if !isLeader.IsLeader() {
			sleep(ctx, pollInterval)
			continue
		}

		leaderCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			mgr.Run(leaderCtx)
			close(done)
		}()

		for ctx.Err() == nil && isLeader.IsLeader() {
			sleep(ctx, pollInterval)
		}
		cancel()
		<-done
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
