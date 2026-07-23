package apiserver

import "github.com/gh0st-nemesis/nimbuscore/internal/rbac"

var methodResourceVerb = map[string]struct{ Resource, Verb string }{
	"/nimbuscore.v1.PodService/CreatePod": {"pods", "create"},
	"/nimbuscore.v1.PodService/GetPod":    {"pods", "get"},
	"/nimbuscore.v1.PodService/ListPods":  {"pods", "list"},
	"/nimbuscore.v1.PodService/DeletePod": {"pods", "delete"},

	"/nimbuscore.v1.NodeService/CreateNode": {"nodes", "create"},
	"/nimbuscore.v1.NodeService/GetNode":    {"nodes", "get"},
	"/nimbuscore.v1.NodeService/ListNodes":  {"nodes", "list"},
	"/nimbuscore.v1.NodeService/DeleteNode": {"nodes", "delete"},
	"/nimbuscore.v1.NodeService/Heartbeat":  {"nodes", "update"},

	"/nimbuscore.v1.DeploymentService/CreateDeployment": {"deployments", "create"},
	"/nimbuscore.v1.DeploymentService/GetDeployment":    {"deployments", "get"},
	"/nimbuscore.v1.DeploymentService/ListDeployments":  {"deployments", "list"},
	"/nimbuscore.v1.DeploymentService/DeleteDeployment": {"deployments", "delete"},

	"/nimbuscore.v1.AdminService/JoinRaft": {"admin", "join"},
}

func DefaultRBACBindings() []rbac.Binding {
	nodeRole := rbac.Role{
		Name: "node",
		Rules: []rbac.Rule{
			{Resources: []string{"nodes"}, Verbs: []string{"create", "update"}},
		},
	}

	clientRole := rbac.Role{
		Name: "client",
		Rules: []rbac.Rule{
			{Resources: []string{"pods", "deployments"}, Verbs: []string{rbac.Wildcard}},
			{Resources: []string{"nodes"}, Verbs: []string{"get", "list"}},
		},
	}

	controlPlaneRole := rbac.Role{
		Name: "control-plane",
		Rules: []rbac.Rule{
			{Resources: []string{rbac.Wildcard}, Verbs: []string{rbac.Wildcard}},
		},
	}

	return []rbac.Binding{
		{PathPrefix: "/node/", Role: nodeRole},
		{PathPrefix: "/client/", Role: clientRole},
		{PathPrefix: "/control-plane/", Role: controlPlaneRole},
	}
}
