package admission

import (
	"context"
	"fmt"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

type PodLister interface {
	List(ctx context.Context, namespace string) ([]*v1.Pod, error)
}

type QuotaPolicy struct {
	pods  PodLister
	quota *v1.ResourceList
}

func NewQuotaPolicy(pods PodLister, quota *v1.ResourceList) *QuotaPolicy {
	return &QuotaPolicy{pods: pods, quota: quota}
}

func (p *QuotaPolicy) Admit(ctx context.Context, req *Request) error {
	if p.quota.GetCpuMillis() <= 0 && p.quota.GetMemoryBytes() <= 0 {
		return nil
	}

	existing, err := p.pods.List(ctx, req.Namespace)
	if err != nil {
		return fmt.Errorf("quota: list existing pods in %q: %w", req.Namespace, err)
	}

	var usedCPU, usedMem int64
	for _, pod := range existing {
		for _, c := range pod.GetSpec().GetContainers() {
			usedCPU += c.GetResources().GetRequests().GetCpuMillis()
			usedMem += c.GetResources().GetRequests().GetMemoryBytes()
		}
	}

	var reqCPU, reqMem int64
	for _, c := range req.Spec.GetContainers() {
		reqCPU += c.GetResources().GetRequests().GetCpuMillis()
		reqMem += c.GetResources().GetRequests().GetMemoryBytes()
	}
	reqCPU *= int64(req.Replicas)
	reqMem *= int64(req.Replicas)

	if p.quota.GetCpuMillis() > 0 && usedCPU+reqCPU > p.quota.GetCpuMillis() {
		return fmt.Errorf("namespace %q would exceed its CPU quota (%d existing + %d requested > %d millicores)",
			req.Namespace, usedCPU, reqCPU, p.quota.GetCpuMillis())
	}
	if p.quota.GetMemoryBytes() > 0 && usedMem+reqMem > p.quota.GetMemoryBytes() {
		return fmt.Errorf("namespace %q would exceed its memory quota (%d existing + %d requested > %d bytes)",
			req.Namespace, usedMem, reqMem, p.quota.GetMemoryBytes())
	}
	return nil
}
