package scheduler

import (
	"context"
	"errors"
)

var ErrNoNodeAvailable = errors.New("scheduler: no node available")

type NodeCandidate struct {
	Name                string
	CPUCapacity         int64
	MemCapacity         int64
	CPUUsed             int64
	MemUsed             int64
	AcceleratorCapacity map[string]int64
	AcceleratorUsed     map[string]int64
	UsedPorts           map[int32]bool
}

type PodRequest struct {
	Name                  string
	CPURequest            int64
	MemRequest            int64
	AcceleratorsRequested map[string]int64
	ContainerPorts        []int32
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
		if !acceleratorsFit(pod.AcceleratorsRequested, n.AcceleratorCapacity, n.AcceleratorUsed) {
			continue
		}
		if !portsFit(pod.ContainerPorts, n.UsedPorts) {
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

func acceleratorsFit(requested, capacity, used map[string]int64) bool {
	for name, want := range requested {
		if want <= 0 {
			continue
		}
		free := capacity[name] - used[name]
		if free < want {
			return false
		}
	}
	return true
}

func portsFit(requested []int32, used map[int32]bool) bool {
	for _, port := range requested {
		if used[port] {
			return false
		}
	}
	return true
}
