package finops

import (
	"math"
	"testing"
	"time"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func podRunningSince(now time.Time, name, namespace string, labels map[string]string, ago time.Duration, cpuMillis, memBytes int64) *v1.Pod {
	return &v1.Pod{
		Metadata: &v1.ObjectMeta{
			Name:          name,
			Namespace:     namespace,
			Labels:        labels,
			CreatedAtUnix: now.Add(-ago).Unix(),
		},
		Spec: &v1.PodSpec{
			Containers: []*v1.Container{{
				Name:      "app",
				Resources: &v1.ResourceRequirements{Requests: &v1.ResourceList{CpuMillis: cpuMillis, MemoryBytes: memBytes}},
			}},
		},
	}
}

func TestEstimateComputesCostFromResourceRequestsAndDuration(t *testing.T) {
	model := CostModel{CPUCoreHour: 0.10, MemoryGBHour: 0.01}
	now := time.Now().Truncate(time.Second)

	pod := &v1.Pod{
		Metadata: &v1.ObjectMeta{
			Name:          "web-0",
			Namespace:     "default",
			CreatedAtUnix: now.Add(-2 * time.Hour).Unix(),
		},
		Spec: &v1.PodSpec{Containers: []*v1.Container{{
			Name:      "app",
			Resources: &v1.ResourceRequirements{Requests: &v1.ResourceList{CpuMillis: 1000, MemoryBytes: 1 << 30}},
		}}},
	}

	report := Estimate([]*v1.Pod{pod}, model, "", now)

	want := 1.0*0.10*2 + 1.0*0.01*2
	if !almostEqual(report.Total, want) {
		t.Errorf("Total = %v, want %v", report.Total, want)
	}
}

func TestEstimateGroupsByNamespaceAndLabel(t *testing.T) {
	model := CostModel{CPUCoreHour: 0.10, MemoryGBHour: 0.01}
	now := time.Now().Truncate(time.Second)

	pods := []*v1.Pod{
		podRunningSince(now, "web-0", "team-a", map[string]string{"team": "payments"}, time.Hour, 1000, 0),
		podRunningSince(now, "web-1", "team-b", map[string]string{"team": "search"}, time.Hour, 1000, 0),
		podRunningSince(now, "web-2", "team-a", map[string]string{"team": "payments"}, time.Hour, 1000, 0),
	}

	report := Estimate(pods, model, "team", now)

	if !almostEqual(report.ByNamespace["team-a"], 0.20) {
		t.Errorf("ByNamespace[team-a] = %v, want 0.20", report.ByNamespace["team-a"])
	}
	if !almostEqual(report.ByNamespace["team-b"], 0.10) {
		t.Errorf("ByNamespace[team-b] = %v, want 0.10", report.ByNamespace["team-b"])
	}
	if !almostEqual(report.ByLabel["payments"], 0.20) {
		t.Errorf("ByLabel[payments] = %v, want 0.20", report.ByLabel["payments"])
	}
	if !almostEqual(report.ByLabel["search"], 0.10) {
		t.Errorf("ByLabel[search] = %v, want 0.10", report.ByLabel["search"])
	}
	if !almostEqual(report.Total, 0.30) {
		t.Errorf("Total = %v, want 0.30", report.Total)
	}
}

func TestEstimateIncludesAcceleratorCost(t *testing.T) {
	model := CostModel{AcceleratorHour: map[string]float64{"nvidia.com/gpu": 2.5}}
	now := time.Now().Truncate(time.Second)

	pod := &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: "trainer", Namespace: "ml", CreatedAtUnix: now.Add(-1 * time.Hour).Unix()},
		Spec: &v1.PodSpec{Containers: []*v1.Container{{
			Name: "app",
			Resources: &v1.ResourceRequirements{
				Requests: &v1.ResourceList{Accelerators: map[string]int64{"nvidia.com/gpu": 2}},
			},
		}}},
	}

	report := Estimate([]*v1.Pod{pod}, model, "", now)
	if !almostEqual(report.Total, 5.0) {
		t.Errorf("Total = %v, want 5.0 (2 GPUs x $2.50/h x 1h)", report.Total)
	}
}

func TestEstimateSkipsPodsWithoutCreationTimestamp(t *testing.T) {
	model := DefaultCostModel()
	pod := &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: "no-timestamp", Namespace: "default"},
		Spec: &v1.PodSpec{Containers: []*v1.Container{{
			Name:      "app",
			Resources: &v1.ResourceRequirements{Requests: &v1.ResourceList{CpuMillis: 1000}},
		}}},
	}

	report := Estimate([]*v1.Pod{pod}, model, "", time.Now())
	if report.Total != 0 {
		t.Errorf("Total = %v, want 0 for a pod with no creation timestamp", report.Total)
	}
}

func TestUnlabeledPodsBucketedSeparately(t *testing.T) {
	model := CostModel{CPUCoreHour: 0.10}
	now := time.Now().Truncate(time.Second)
	pod := podRunningSince(now, "web-0", "default", nil, time.Hour, 1000, 0)

	report := Estimate([]*v1.Pod{pod}, model, "team", now)
	if !almostEqual(report.ByLabel["unlabeled"], 0.10) {
		t.Errorf("ByLabel[unlabeled] = %v, want 0.10", report.ByLabel["unlabeled"])
	}
}
