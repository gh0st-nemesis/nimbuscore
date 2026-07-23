package rbac

import "testing"

func TestAuthorizerDeniesByDefault(t *testing.T) {
	a := NewAuthorizer()
	if a.Allow("/node/worker-1", "pods", "get") {
		t.Fatal("Allow returned true with no bindings configured, want false")
	}
}

func TestAuthorizerMatchesPathPrefix(t *testing.T) {
	a := NewAuthorizer(Binding{
		PathPrefix: "/node/",
		Role: Role{Rules: []Rule{
			{Resources: []string{"nodes"}, Verbs: []string{"create", "update"}},
		}},
	})

	if !a.Allow("/node/worker-1", "nodes", "update") {
		t.Error("expected /node/worker-1 to be allowed to update nodes")
	}
	if a.Allow("/node/worker-1", "nodes", "delete") {
		t.Error("expected /node/worker-1 to be denied deleting nodes")
	}
	if a.Allow("/client/someone", "nodes", "update") {
		t.Error("expected /client/someone not to match the /node/ binding")
	}
}

func TestAuthorizerWildcardResourceAndVerb(t *testing.T) {
	a := NewAuthorizer(Binding{
		PathPrefix: "/control-plane/",
		Role: Role{Rules: []Rule{
			{Resources: []string{Wildcard}, Verbs: []string{Wildcard}},
		}},
	})

	for _, rv := range []struct{ resource, verb string }{
		{"pods", "create"}, {"nodes", "delete"}, {"deployments", "list"}, {"admin", "join"},
	} {
		if !a.Allow("/control-plane/node-1", rv.resource, rv.verb) {
			t.Errorf("expected control-plane wildcard role to allow %s:%s", rv.resource, rv.verb)
		}
	}
}

func TestAuthorizerFirstMatchingBindingWins(t *testing.T) {
	a := NewAuthorizer(
		Binding{PathPrefix: "/client/", Role: Role{Rules: []Rule{{Resources: []string{"pods"}, Verbs: []string{"get"}}}}},
	)

	if a.Allow("/client/tester", "pods", "delete") {
		t.Fatal("expected delete to be denied when only get is granted")
	}
}
