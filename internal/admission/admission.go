package admission

import (
	"context"
	"fmt"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

type Request struct {
	Namespace string
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
			return fmt.Errorf("admission: %w", err)
		}
	}
	return nil
}
