package scheduler

import (
	"context"
	"errors"
)

var ErrNoNodeAvailable = errors.New("scheduler: no node available")

type NodeCandidate struct {
	Name        string
	CPUCapacity int64
	MemCapacity int64
	CPUUsed     int64
	MemUsed     int64
}

type PodRequest struct {
	Name       string
	CPURequest int64
	MemRequest int64
}

type Scheduler interface {
	Schedule(ctx context.Context, pod PodRequest, nodes []NodeCandidate) (string, error)
}

type basic struct{}

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
			continue
		}
		score := freeCPU + freeMem
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
