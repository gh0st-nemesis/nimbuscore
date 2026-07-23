package netpolicy

import (
	"testing"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

func TestAllowedDeniesByDefaultWithNoPolicies(t *testing.T) {
	if Allowed(nil, map[string]string{"app": "client"}, map[string]string{"app": "server"}, "TCP", 80) {
		t.Fatal("Allowed returned true with zero NetworkPolicies, want deny-by-default")
	}
}

func TestAllowedDeniesWhenNoPolicySelectsDestination(t *testing.T) {
	policies := []*v1.NetworkPolicy{
		{Spec: &v1.NetworkPolicySpec{PodSelector: map[string]string{"app": "unrelated"}}},
	}
	if Allowed(policies, nil, map[string]string{"app": "server"}, "TCP", 80) {
		t.Fatal("Allowed returned true for a destination no policy selects")
	}
}

func TestAllowedGrantsAllTrafficWithEmptyRule(t *testing.T) {
	policies := []*v1.NetworkPolicy{
		{Spec: &v1.NetworkPolicySpec{
			PodSelector: map[string]string{"app": "server"},
			Ingress:     []*v1.NetworkPolicyIngressRule{{}},
		}},
	}
	if !Allowed(policies, map[string]string{"app": "anything"}, map[string]string{"app": "server"}, "TCP", 12345) {
		t.Fatal("an ingress rule with empty from/ports should allow all sources and ports")
	}
}

func TestAllowedRestrictsToMatchingPeer(t *testing.T) {
	policies := []*v1.NetworkPolicy{
		{Spec: &v1.NetworkPolicySpec{
			PodSelector: map[string]string{"app": "server"},
			Ingress: []*v1.NetworkPolicyIngressRule{{
				From: []*v1.NetworkPolicyPeer{{PodSelector: map[string]string{"app": "client"}}},
			}},
		}},
	}

	if !Allowed(policies, map[string]string{"app": "client"}, map[string]string{"app": "server"}, "TCP", 80) {
		t.Error("expected traffic from the allowed peer to be permitted")
	}
	if Allowed(policies, map[string]string{"app": "someone-else"}, map[string]string{"app": "server"}, "TCP", 80) {
		t.Error("expected traffic from a non-matching peer to be denied")
	}
}

func TestAllowedRestrictsToMatchingPort(t *testing.T) {
	policies := []*v1.NetworkPolicy{
		{Spec: &v1.NetworkPolicySpec{
			PodSelector: map[string]string{"app": "server"},
			Ingress: []*v1.NetworkPolicyIngressRule{{
				Ports: []*v1.NetworkPolicyPort{{Protocol: "TCP", Port: 443}},
			}},
		}},
	}

	if !Allowed(policies, nil, map[string]string{"app": "server"}, "TCP", 443) {
		t.Error("expected TCP/443 to be allowed")
	}
	if Allowed(policies, nil, map[string]string{"app": "server"}, "TCP", 80) {
		t.Error("expected TCP/80 to be denied")
	}
	if Allowed(policies, nil, map[string]string{"app": "server"}, "UDP", 443) {
		t.Error("expected UDP/443 to be denied (protocol mismatch)")
	}
}

func TestAllowedOrsMultiplePolicies(t *testing.T) {
	policies := []*v1.NetworkPolicy{
		{Spec: &v1.NetworkPolicySpec{
			PodSelector: map[string]string{"app": "server"},
			Ingress: []*v1.NetworkPolicyIngressRule{{
				From: []*v1.NetworkPolicyPeer{{PodSelector: map[string]string{"app": "client-a"}}},
			}},
		}},
		{Spec: &v1.NetworkPolicySpec{
			PodSelector: map[string]string{"app": "server"},
			Ingress: []*v1.NetworkPolicyIngressRule{{
				From: []*v1.NetworkPolicyPeer{{PodSelector: map[string]string{"app": "client-b"}}},
			}},
		}},
	}

	for _, client := range []string{"client-a", "client-b"} {
		if !Allowed(policies, map[string]string{"app": client}, map[string]string{"app": "server"}, "TCP", 80) {
			t.Errorf("expected %s to be allowed by one of the two policies", client)
		}
	}
	if Allowed(policies, map[string]string{"app": "client-c"}, map[string]string{"app": "server"}, "TCP", 80) {
		t.Error("expected client-c to be denied, it matches neither policy")
	}
}
