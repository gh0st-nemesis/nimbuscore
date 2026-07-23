package finops

import (
	"time"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

type CostModel struct {
	CPUCoreHour     float64
	MemoryGBHour    float64
	AcceleratorHour map[string]float64
}

func DefaultCostModel() CostModel {
	return CostModel{
		CPUCoreHour:  0.03,
		MemoryGBHour: 0.004,
	}
}

type Report struct {
	Total       float64
	ByNamespace map[string]float64
	ByLabel     map[string]float64
}

func newReport() Report {
	return Report{ByNamespace: make(map[string]float64), ByLabel: make(map[string]float64)}
}

func Estimate(pods []*v1.Pod, model CostModel, labelKey string, now time.Time) Report {
	report := newReport()

	for _, p := range pods {
		hours := runningHours(p, now)
		if hours <= 0 {
			continue
		}

		var podCost float64
		for _, c := range p.GetSpec().GetContainers() {
			req := c.GetResources().GetRequests()
			podCost += (float64(req.GetCpuMillis()) / 1000) * model.CPUCoreHour * hours
			podCost += (float64(req.GetMemoryBytes()) / (1 << 30)) * model.MemoryGBHour * hours
			for name, count := range req.GetAccelerators() {
				podCost += float64(count) * model.AcceleratorHour[name] * hours
			}
		}

		report.Total += podCost
		report.ByNamespace[p.GetMetadata().GetNamespace()] += podCost

		if labelKey != "" {
			if v, ok := p.GetMetadata().GetLabels()[labelKey]; ok {
				report.ByLabel[v] += podCost
			} else {
				report.ByLabel["unlabeled"] += podCost
			}
		}
	}

	return report
}

func runningHours(p *v1.Pod, now time.Time) float64 {
	createdAt := p.GetMetadata().GetCreatedAtUnix()
	if createdAt <= 0 {
		return 0
	}
	elapsed := now.Sub(time.Unix(createdAt, 0))
	if elapsed <= 0 {
		return 0
	}
	return elapsed.Hours()
}
