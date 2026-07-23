package admission

import (
	"context"
	"testing"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

func BenchmarkChainAdmit(b *testing.B) {
	chain := NewChain(
		NewSecurityContextPolicy("NET_BIND_SERVICE"),
		NewImageSignaturePolicy(&fakeVerifier{signed: map[string]bool{"nginx:v1": true}}),
	)

	req := &Request{
		Namespace: "default",
		Replicas:  1,
		Spec: &v1.PodSpec{
			Containers: []*v1.Container{{
				Name:      "app",
				Image:     "nginx:v1",
				Resources: &v1.ResourceRequirements{Requests: &v1.ResourceList{CpuMillis: 100, MemoryBytes: 64 << 20}},
			}},
		},
	}

	ctx := context.Background()
	b.ResetTimer()
	for b.Loop() {
		if err := chain.Admit(ctx, req); err != nil {
			b.Fatalf("Admit: %v", err)
		}
	}
}
