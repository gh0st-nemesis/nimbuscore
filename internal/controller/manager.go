package controller

import (
	"context"
	"log"
	"sync"
	"time"
)

type Reconciler interface {
	Name() string

	Reconcile(ctx context.Context) error
}

type Manager struct {
	reconcilers []Reconciler
}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) Register(r Reconciler) {
	m.reconcilers = append(m.reconcilers, r)
}

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

type LeaderChecker interface {
	IsLeader() bool
}

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
