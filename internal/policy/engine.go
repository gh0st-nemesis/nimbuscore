package policy

import (
	"fmt"

	"github.com/google/cel-go/cel"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

type Engine struct {
	env *cel.Env
}

func NewEngine() (*Engine, error) {
	env, err := cel.NewEnv(
		cel.Variable("pod", cel.MapType(cel.StringType, cel.DynType)),
	)
	if err != nil {
		return nil, fmt.Errorf("policy: create CEL environment: %w", err)
	}
	return &Engine{env: env}, nil
}

func (e *Engine) Compile(expression string) (cel.Program, error) {
	ast, issues := e.env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("policy: compile %q: %w", expression, issues.Err())
	}
	prg, err := e.env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("policy: build program for %q: %w", expression, err)
	}
	return prg, nil
}

func (e *Engine) Evaluate(prg cel.Program, view map[string]any) (bool, error) {
	out, _, err := prg.Eval(map[string]any{"pod": view})
	if err != nil {
		return false, fmt.Errorf("policy: evaluate: %w", err)
	}
	result, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("policy: expression must evaluate to a bool, got %T", out.Value())
	}
	return result, nil
}

func PodView(namespace string, labels map[string]string, spec *v1.PodSpec) map[string]any {
	containers := make([]any, 0, len(spec.GetContainers()))
	for _, c := range spec.GetContainers() {
		containers = append(containers, map[string]any{
			"name":       c.GetName(),
			"image":      c.GetImage(),
			"privileged": c.GetSecurityContext().GetPrivileged(),
		})
	}

	celLabels := make(map[string]any, len(labels))
	for k, v := range labels {
		celLabels[k] = v
	}

	return map[string]any{
		"namespace":  namespace,
		"labels":     celLabels,
		"containers": containers,
	}
}
