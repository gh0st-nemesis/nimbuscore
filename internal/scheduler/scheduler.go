// Package scheduler implements the two-phase filter-then-score placement
// algorithm described in the design doc (section 03).
package scheduler

import (
	"context"
	"errors"
)

// ErrNoNodeAvailable is returned when no candidate node passes filtering.
var ErrNoNodeAvailable = errors.New("scheduler: no node available")

// NodeCandidate is the minimal view a Scheduler needs of a node to filter
// and score it. Stands in for the full Node resource until the Protobuf
// schema (step B) is defined.
type NodeCandidate struct {
	Name        string
	CPUCapacity int64
	MemCapacity int64
	CPUUsed     int64
	MemUsed     int64
}

// PodRequest is the minimal placement request a Scheduler acts on.
type PodRequest struct {
	Name       string
	CPURequest int64
	MemRequest int64
}

// Scheduler assigns pods to nodes. Phase 1 ships a single built-in
// strategy; the design doc calls for a pluggable component so later
// phases can add strategies (e.g. TPM attestation constraints) without
// rewriting callers.
type Scheduler interface {
	Schedule(ctx context.Context, pod PodRequest, nodes []NodeCandidate) (string, error)
}

type basic struct{}

// New returns the default filter-then-score Scheduler.
func New() Scheduler {
	return &basic{}
}

func (b *basic) Schedule(_ context.Context, pod PodRequest, nodes []NodeCandidate) (string, error) {
	var best *NodeCandidate
	var bestScore int64 = -1

	for i := range nodes {
		n := &nodes[i]
		freeCPU := n.CPUCapacity - n.CPUUsed
		freeMem := n.MemCapacity - n.MemUsed
		if freeCPU < pod.CPURequest || freeMem < pod.MemRequest {
			continue // filter phase
		}
		score := freeCPU + freeMem // scoring phase — live-usage weighting lands in Phase 5
		if score > bestScore {
			bestScore = score
			best = n
		}
	}

	if best == nil {
		return "", ErrNoNodeAvailable
	}
	return best.Name, nil
}
