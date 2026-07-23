package apiserver

import (
	"context"
	"time"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/finops"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type finopsService struct {
	v1.UnimplementedFinOpsServiceServer
	pods  *registry.Registry[*v1.Pod]
	model finops.CostModel
}

func NewFinOpsService(pods *registry.Registry[*v1.Pod], model finops.CostModel) v1.FinOpsServiceServer {
	return &finopsService{pods: pods, model: model}
}

func (svc *finopsService) GetCostReport(ctx context.Context, req *v1.CostReportRequest) (*v1.CostReport, error) {
	pods, err := svc.pods.List(ctx, req.GetNamespace())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list pods: %v", err)
	}

	report := finops.Estimate(pods, svc.model, req.GetLabelKey(), time.Now())
	return &v1.CostReport{
		Total:       report.Total,
		ByNamespace: report.ByNamespace,
		ByLabel:     report.ByLabel,
	}, nil
}
