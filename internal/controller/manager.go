// Package controller implements the reconciliation-loop pattern (desired
// state vs. observed state) that is the central mechanism of the
// declarative model (design doc section 03). Detailed reconciler
// behavior is tracked as step C.
package controller

import (
	"context"
	"log"
	"sync"
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
