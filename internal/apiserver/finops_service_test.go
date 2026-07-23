package apiserver_test

import (
	"context"
	"testing"
	"time"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/apiserver"
	"github.com/gh0st-nemesis/nimbuscore/internal/finops"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

func TestGetCostReportEstimatesFromPodResourceRequests(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemStore()
	pods := registry.New(s, "pods", func() *v1.Pod { return &v1.Pod{} })

	now := time.Now().Truncate(time.Second)
	if err := pods.Put(ctx, "team-a", "web-0", &v1.Pod{
		Metadata: &v1.ObjectMeta{
			Name:          "web-0",
			Namespace:     "team-a",
			CreatedAtUnix: now.Add(-time.Hour).Unix(),
		},
		Spec: &v1.PodSpec{Containers: []*v1.Container{{
			Name:      "app",
			Resources: &v1.ResourceRequirements{Requests: &v1.ResourceList{CpuMillis: 1000}},
		}}},
	}); err != nil {
		t.Fatalf("seed pod: %v", err)
	}

	svc := apiserver.NewFinOpsService(pods, finops.CostModel{CPUCoreHour: 0.10})
	report, err := svc.GetCostReport(ctx, &v1.CostReportRequest{})
	if err != nil {
		t.Fatalf("GetCostReport: %v", err)
	}

	if report.GetTotal() < 0.0999 || report.GetTotal() > 0.1001 {
		t.Errorf("Total = %v, want ~0.10 (1 core x $0.10/h x ~1h)", report.GetTotal())
	}
	if got := report.GetByNamespace()["team-a"]; got < 0.0999 || got > 0.1001 {
		t.Errorf("ByNamespace[team-a] = %v, want ~0.10", got)
	}
}

func TestGetCostReportFiltersByNamespace(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemStore()
	pods := registry.New(s, "pods", func() *v1.Pod { return &v1.Pod{} })

	now := time.Now().Truncate(time.Second)
	seed := func(namespace, name string) {
		if err := pods.Put(ctx, namespace, name, &v1.Pod{
			Metadata: &v1.ObjectMeta{Name: name, Namespace: namespace, CreatedAtUnix: now.Add(-time.Hour).Unix()},
			Spec: &v1.PodSpec{Containers: []*v1.Container{{
				Name:      "app",
				Resources: &v1.ResourceRequirements{Requests: &v1.ResourceList{CpuMillis: 1000}},
			}}},
		}); err != nil {
			t.Fatalf("seed pod %s/%s: %v", namespace, name, err)
		}
	}
	seed("team-a", "web-0")
	seed("team-b", "web-0")

	svc := apiserver.NewFinOpsService(pods, finops.CostModel{CPUCoreHour: 0.10})
	report, err := svc.GetCostReport(ctx, &v1.CostReportRequest{Namespace: "team-a"})
	if err != nil {
		t.Fatalf("GetCostReport: %v", err)
	}

	if _, ok := report.GetByNamespace()["team-b"]; ok {
		t.Errorf("ByNamespace unexpectedly includes team-b when filtered to team-a: %v", report.GetByNamespace())
	}
	if got := report.GetByNamespace()["team-a"]; got < 0.0999 || got > 0.1001 {
		t.Errorf("ByNamespace[team-a] = %v, want ~0.10", got)
	}
}
