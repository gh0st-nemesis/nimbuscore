package admission

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

var decisionsCounter = sync.OnceValue(func() metric.Int64Counter {
	c, _ := otel.Meter("nimbuscore/admission").Int64Counter(
		"nimbuscore.admission.decisions",
		metric.WithDescription("Admission chain decisions by result"),
	)
	return c
})

type Request struct {
	Namespace string
	Name      string
	Labels    map[string]string
	Spec      *v1.PodSpec
	Replicas  int32
}

type Validator interface {
	Admit(ctx context.Context, req *Request) error
}

type Chain struct {
	validators []Validator
}

func NewChain(validators ...Validator) *Chain {
	return &Chain{validators: validators}
}

func (c *Chain) Admit(ctx context.Context, req *Request) error {
	if req.Replicas <= 0 {
		req.Replicas = 1
	}
	for _, v := range c.validators {
		if err := v.Admit(ctx, req); err != nil {
			decisionsCounter().Add(ctx, 1, metric.WithAttributes(attribute.String("result", "rejected")))
			return fmt.Errorf("admission: %w", err)
		}
	}
	decisionsCounter().Add(ctx, 1, metric.WithAttributes(attribute.String("result", "admitted")))
	return nil
}
