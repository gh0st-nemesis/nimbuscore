package apiserver

import "github.com/gh0st-nemesis/nimbuscore/internal/rbac"

var methodResourceVerb = map[string]struct{ Resource, Verb string }{
	"/nimbuscore.v1.PodService/CreatePod":       {"pods", "create"},
	"/nimbuscore.v1.PodService/GetPod":          {"pods", "get"},
	"/nimbuscore.v1.PodService/ListPods":        {"pods", "list"},
	"/nimbuscore.v1.PodService/DeletePod":       {"pods", "delete"},
	"/nimbuscore.v1.PodService/UpdatePodStatus": {"pods", "update"},

	"/nimbuscore.v1.NodeService/CreateNode": {"nodes", "create"},
	"/nimbuscore.v1.NodeService/GetNode":    {"nodes", "get"},
	"/nimbuscore.v1.NodeService/ListNodes":  {"nodes", "list"},
	"/nimbuscore.v1.NodeService/DeleteNode": {"nodes", "delete"},
	"/nimbuscore.v1.NodeService/Heartbeat":  {"nodes", "update"},

	"/nimbuscore.v1.DeploymentService/CreateDeployment": {"deployments", "create"},
	"/nimbuscore.v1.DeploymentService/GetDeployment":    {"deployments", "get"},
	"/nimbuscore.v1.DeploymentService/ListDeployments":  {"deployments", "list"},
	"/nimbuscore.v1.DeploymentService/DeleteDeployment": {"deployments", "delete"},

	"/nimbuscore.v1.VolumeService/CreateVolume": {"volumes", "create"},
	"/nimbuscore.v1.VolumeService/GetVolume":    {"volumes", "get"},
	"/nimbuscore.v1.VolumeService/ListVolumes":  {"volumes", "list"},
	"/nimbuscore.v1.VolumeService/DeleteVolume": {"volumes", "delete"},

	"/nimbuscore.v1.NetworkPolicyService/CreateNetworkPolicy": {"networkpolicies", "create"},
	"/nimbuscore.v1.NetworkPolicyService/GetNetworkPolicy":    {"networkpolicies", "get"},
	"/nimbuscore.v1.NetworkPolicyService/ListNetworkPolicies": {"networkpolicies", "list"},
	"/nimbuscore.v1.NetworkPolicyService/DeleteNetworkPolicy": {"networkpolicies", "delete"},

	"/nimbuscore.v1.PolicyService/CreatePolicy": {"policies", "create"},
	"/nimbuscore.v1.PolicyService/GetPolicy":    {"policies", "get"},
	"/nimbuscore.v1.PolicyService/ListPolicies": {"policies", "list"},
	"/nimbuscore.v1.PolicyService/DeletePolicy": {"policies", "delete"},

	"/nimbuscore.v1.SecretService/CreateSecret": {"secrets", "create"},
	"/nimbuscore.v1.SecretService/GetSecret":    {"secrets", "get"},
	"/nimbuscore.v1.SecretService/ListSecrets":  {"secrets", "list"},
	"/nimbuscore.v1.SecretService/DeleteSecret": {"secrets", "delete"},

	"/nimbuscore.v1.ImageRegistryService/PushImage":   {"images", "create"},
	"/nimbuscore.v1.ImageRegistryService/GetImage":    {"images", "get"},
	"/nimbuscore.v1.ImageRegistryService/ListImages":  {"images", "list"},
	"/nimbuscore.v1.ImageRegistryService/DeleteImage": {"images", "delete"},

	"/nimbuscore.v1.BackupService/CreateBackup":  {"backup", "create"},
	"/nimbuscore.v1.BackupService/RestoreBackup": {"backup", "restore"},

	"/nimbuscore.v1.FederationService/RegisterCluster":   {"federation", "create"},
	"/nimbuscore.v1.FederationService/UnregisterCluster": {"federation", "delete"},
	"/nimbuscore.v1.FederationService/ListClusters":      {"federation", "list"},
	"/nimbuscore.v1.FederationService/ListFederatedPods": {"federation", "list"},

	"/nimbuscore.v1.AdminService/JoinRaft": {"admin", "join"},

	"/nimbuscore.v1.FinOpsService/GetCostReport": {"finops", "get"},
}

func DefaultRBACBindings() []rbac.Binding {
	nodeRole := rbac.Role{
		Name: "node",
		Rules: []rbac.Rule{
			{Resources: []string{"nodes"}, Verbs: []string{"create", "update"}},
			{Resources: []string{"pods"}, Verbs: []string{"list", "update"}},
		},
	}

	clientRole := rbac.Role{
		Name: "client",
		Rules: []rbac.Rule{
			{Resources: []string{"pods", "deployments", "volumes", "networkpolicies", "policies", "secrets", "images", "backup", "federation", "finops"}, Verbs: []string{rbac.Wildcard}},
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
