package admission

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/cel-go/cel"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/policy"
)

type PolicyLister interface {
	List(ctx context.Context, namespace string) ([]*v1.Policy, error)
}

type PolicyValidator struct {
	engine   *policy.Engine
	policies PolicyLister

	mu       sync.Mutex
	compiled map[string]cel.Program
}

func NewPolicyValidator(engine *policy.Engine, policies PolicyLister) *PolicyValidator {
	return &PolicyValidator{engine: engine, policies: policies, compiled: make(map[string]cel.Program)}
}

func (p *PolicyValidator) Admit(ctx context.Context, req *Request) error {
	policies, err := p.policies.List(ctx, "")
	if err != nil {
		return fmt.Errorf("policy: list policies: %w", err)
	}
	if len(policies) == 0 {
		return nil
	}

	view := policy.PodView(req.Namespace, req.Labels, req.Spec)
	for _, pol := range policies {
		expr := pol.GetSpec().GetExpression()
		prg, err := p.compile(expr)
		if err != nil {
			return fmt.Errorf("policy %q: %w", pol.GetMetadata().GetName(), err)
		}

		ok, err := p.engine.Evaluate(prg, view)
		if err != nil {
			return fmt.Errorf("policy %q: %w", pol.GetMetadata().GetName(), err)
		}
		if !ok {
			msg := pol.GetSpec().GetMessage()
			if msg == "" {
				msg = fmt.Sprintf("denied by policy %q", pol.GetMetadata().GetName())
			}
			return fmt.Errorf("%s", msg)
		}
	}
	return nil
}

func (p *PolicyValidator) compile(expression string) (cel.Program, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if prg, ok := p.compiled[expression]; ok {
		return prg, nil
	}
	prg, err := p.engine.Compile(expression)
	if err != nil {
		return nil, err
	}
	p.compiled[expression] = prg
	return prg, nil
}
