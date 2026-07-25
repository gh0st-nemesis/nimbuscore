package controller

import (
	"context"
	"testing"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/scheduler"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

func newTestReconciler() (*DeploymentReconciler, *registry.Registry[*v1.Deployment], *registry.Registry[*v1.Pod], *registry.Registry[*v1.Node]) {
	s := store.NewMemStore()
	deployments := registry.New(s, "deployments", func() *v1.Deployment { return &v1.Deployment{} })
	pods := registry.New(s, "pods", func() *v1.Pod { return &v1.Pod{} })
	nodes := registry.New(s, "nodes", func() *v1.Node { return &v1.Node{} })
	volumes := registry.New(s, "volumes", func() *v1.Volume { return &v1.Volume{} })
	return NewDeploymentReconciler(deployments, pods, nodes, volumes, scheduler.New(), 0), deployments, pods, nodes
}

func seedReadyNode(t testing.TB, ctx context.Context, nodes *registry.Registry[*v1.Node], name string) {
	t.Helper()
	n := &v1.Node{
		Metadata: &v1.ObjectMeta{Name: name},
		Status: &v1.NodeStatus{
			Ready:       true,
			Allocatable: &v1.ResourceList{CpuMillis: 4000, MemoryBytes: 4 << 30},
		},
	}
	if err := nodes.Put(ctx, "", name, n); err != nil {
		t.Fatalf("seed node %s: %v", name, err)
	}
}

func TestReconcileOneScalesUp(t *testing.T) {
	ctx := context.Background()
	r, deployments, pods, nodes := newTestReconciler()
	seedReadyNode(t, ctx, nodes, "node-1")

	d := &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas: 3,
			Selector: map[string]string{"app": "web"},
			Template: &v1.PodSpec{Containers: []*v1.Container{{Name: "web", Image: "nginx"}}},
		},
	}
	if err := deployments.Put(ctx, "default", "web", d); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}

	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne: %v", err)
	}

	got, err := pods.List(ctx, "default")
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 pods, got %d", len(got))
	}
	for _, p := range got {
		if p.GetMetadata().GetLabels()[OwnerDeploymentLabel] != "web" {
			t.Errorf("pod %s missing owner label", p.GetMetadata().GetName())
		}
		if p.GetSpec().GetNodeName() != "node-1" {
			t.Errorf("pod %s not scheduled onto node-1, got %q", p.GetMetadata().GetName(), p.GetSpec().GetNodeName())
		}
	}

	updated, err := deployments.Get(ctx, "default", "web")
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if updated.GetStatus().GetReplicas() != 3 {
		t.Errorf("status.replicas = %d, want 3", updated.GetStatus().GetReplicas())
	}
}

func TestReconcileOneScalesDown(t *testing.T) {
	ctx := context.Background()
	r, deployments, pods, nodes := newTestReconciler()
	seedReadyNode(t, ctx, nodes, "node-1")

	d := &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas: 3,
			Selector: map[string]string{"app": "web"},
			Template: &v1.PodSpec{Containers: []*v1.Container{{Name: "web", Image: "nginx"}}},
		},
	}
	if err := deployments.Put(ctx, "default", "web", d); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne (scale up): %v", err)
	}

	d.Spec.Replicas = 1
	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne (scale down): %v", err)
	}

	got, err := pods.List(ctx, "default")
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 pod after scale down, got %d", len(got))
	}
}

func TestReconcileOneCountsReadyReplicas(t *testing.T) {
	ctx := context.Background()
	r, deployments, pods, nodes := newTestReconciler()
	seedReadyNode(t, ctx, nodes, "node-1")

	d := &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas: 2,
			Selector: map[string]string{"app": "web"},
			Template: &v1.PodSpec{Containers: []*v1.Container{{Name: "web", Image: "nginx"}}},
		},
	}
	if err := deployments.Put(ctx, "default", "web", d); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne: %v", err)
	}

	all, err := pods.List(ctx, "default")
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	all[0].Status.Phase = v1.PodPhase_POD_PHASE_RUNNING
	if err := pods.Put(ctx, "default", all[0].GetMetadata().GetName(), all[0]); err != nil {
		t.Fatalf("update pod status: %v", err)
	}

	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne (recount): %v", err)
	}

	updated, err := deployments.Get(ctx, "default", "web")
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if updated.GetStatus().GetReadyReplicas() != 1 {
		t.Errorf("status.ready_replicas = %d, want 1", updated.GetStatus().GetReadyReplicas())
	}
}

func TestReconcileOneStaysPendingWithoutCapacity(t *testing.T) {
	ctx := context.Background()
	r, deployments, pods, _ := newTestReconciler()

	d := &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas: 1,
			Selector: map[string]string{"app": "web"},
			Template: &v1.PodSpec{Containers: []*v1.Container{{Name: "web", Image: "nginx"}}},
		},
	}
	if err := deployments.Put(ctx, "default", "web", d); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne: %v", err)
	}

	got, err := pods.List(ctx, "default")
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 pod, got %d", len(got))
	}
	if got[0].GetSpec().GetNodeName() != "" {
		t.Errorf("pod should be unscheduled, got node %q", got[0].GetSpec().GetNodeName())
	}
}

func TestReconcileOneEvictsPodFromDeadNode(t *testing.T) {
	ctx := context.Background()
	r, deployments, pods, nodes := newTestReconciler()
	seedReadyNode(t, ctx, nodes, "node-1")

	d := &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas: 1,
			Selector: map[string]string{"app": "web"},
			Template: &v1.PodSpec{Containers: []*v1.Container{{Name: "web", Image: "nginx"}}},
		},
	}
	if err := deployments.Put(ctx, "default", "web", d); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne (initial): %v", err)
	}

	before, err := pods.List(ctx, "default")
	if err != nil || len(before) != 1 {
		t.Fatalf("expected 1 scheduled pod before eviction, got %d (err=%v)", len(before), err)
	}
	originalName := before[0].GetMetadata().GetName()

	node, err := nodes.Get(ctx, "", "node-1")
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	node.Status.Ready = false
	if err := nodes.Put(ctx, "", "node-1", node); err != nil {
		t.Fatalf("mark node not-ready: %v", err)
	}
	seedReadyNode(t, ctx, nodes, "node-2")

	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne (after node death): %v", err)
	}

	after, err := pods.List(ctx, "default")
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("want 1 pod after eviction+replacement, got %d", len(after))
	}
	if after[0].GetMetadata().GetName() == originalName && after[0].GetSpec().GetNodeName() == "node-1" {
		t.Fatalf("pod was not evicted from dead node-1")
	}
	if after[0].GetSpec().GetNodeName() != "node-2" {
		t.Errorf("replacement pod scheduled onto %q, want node-2", after[0].GetSpec().GetNodeName())
	}
}

func TestReconcileOneReplacesTerminalPods(t *testing.T) {
	ctx := context.Background()
	r, deployments, pods, nodes := newTestReconciler()
	seedReadyNode(t, ctx, nodes, "node-1")

	d := &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "job", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas: 1,
			Selector: map[string]string{"app": "job"},
			Template: &v1.PodSpec{Containers: []*v1.Container{{Name: "job", Image: "busybox"}}},
		},
	}
	if err := deployments.Put(ctx, "default", "job", d); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne (initial): %v", err)
	}

	before, err := pods.List(ctx, "default")
	if err != nil || len(before) != 1 {
		t.Fatalf("expected 1 pod, got %d (err=%v)", len(before), err)
	}
	originalName := before[0].GetMetadata().GetName()
	before[0].Status.Phase = v1.PodPhase_POD_PHASE_FAILED
	if err := pods.Put(ctx, "default", originalName, before[0]); err != nil {
		t.Fatalf("mark pod failed: %v", err)
	}

	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne (after failure): %v", err)
	}

	after, err := pods.List(ctx, "default")
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("want 1 pod after replacement, got %d", len(after))
	}
	if after[0].GetStatus().GetPhase() == v1.PodPhase_POD_PHASE_FAILED {
		t.Fatal("the failed pod was not replaced")
	}
}

func TestScheduleUnassignedAccountsForExistingUsage(t *testing.T) {
	ctx := context.Background()
	r, deployments, pods, nodes := newTestReconciler()

	tight := &v1.Node{
		Metadata: &v1.ObjectMeta{Name: "tight"},
		Status: &v1.NodeStatus{
			Ready:       true,
			Allocatable: &v1.ResourceList{CpuMillis: 1000, MemoryBytes: 1 << 30},
		},
	}
	if err := nodes.Put(ctx, "", "tight", tight); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	seedReadyNode(t, ctx, nodes, "roomy")

	existing := &v1.Pod{
		Metadata: &v1.ObjectMeta{Name: "hog", Namespace: "default"},
		Spec: &v1.PodSpec{
			NodeName: "tight",
			Containers: []*v1.Container{{
				Name:      "hog",
				Resources: &v1.ResourceRequirements{Requests: &v1.ResourceList{CpuMillis: 900}},
			}},
		},
	}
	if err := pods.Put(ctx, "default", "hog", existing); err != nil {
		t.Fatalf("seed existing pod: %v", err)
	}

	d := &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas: 1,
			Selector: map[string]string{"app": "web"},
			Template: &v1.PodSpec{Containers: []*v1.Container{{
				Name:      "web",
				Resources: &v1.ResourceRequirements{Requests: &v1.ResourceList{CpuMillis: 500}},
			}}},
		},
	}
	if err := deployments.Put(ctx, "default", "web", d); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}

	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne: %v", err)
	}

	all, err := pods.List(ctx, "default")
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	var newPod *v1.Pod
	for _, p := range all {
		if p.GetMetadata().GetName() != "hog" {
			newPod = p
		}
	}
	if newPod == nil {
		t.Fatal("new pod not found")
	}
	if newPod.GetSpec().GetNodeName() != "roomy" {
		t.Errorf("scheduled onto %q, want roomy (tight only has 100m free, needs 500m)", newPod.GetSpec().GetNodeName())
	}
}

func TestVolumeBackedDeploymentReplicasClampedToOne(t *testing.T) {
	ctx := context.Background()
	r, deployments, pods, nodes := newTestReconciler()
	seedReadyNode(t, ctx, nodes, "node-1")

	d := &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "site", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas: 3,
			Selector: map[string]string{"app": "site"},
			Template: &v1.PodSpec{
				Containers: []*v1.Container{{Name: "site", Image: "nginx"}},
				Volumes:    []*v1.VolumeMount{{VolumeName: "data", MountPath: "/usr/share/nginx/html"}},
			},
		},
	}
	if err := deployments.Put(ctx, "default", "site", d); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne: %v", err)
	}

	got, err := pods.List(ctx, "default")
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("volume-backed deployment requesting 3 replicas should be clamped to 1, got %d pods", len(got))
	}
}

func TestVolumeBackedPodPinnedToClaimedNode(t *testing.T) {
	ctx := context.Background()
	r, deployments, pods, nodes := newTestReconciler()
	seedReadyNode(t, ctx, nodes, "node-1")
	seedReadyNode(t, ctx, nodes, "node-2")

	vol := &v1.Volume{
		Metadata: &v1.ObjectMeta{Name: "data", Namespace: "default"},
		Spec:     &v1.VolumeSpec{RequestedBytes: 1 << 30},
		Status:   &v1.VolumeStatus{Phase: v1.VolumePhase_VOLUME_PHASE_BOUND},
	}
	if err := r.volumes.Put(ctx, "default", "data", vol); err != nil {
		t.Fatalf("seed volume: %v", err)
	}

	d := &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "site", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas: 1,
			Selector: map[string]string{"app": "site"},
			Template: &v1.PodSpec{
				Containers: []*v1.Container{{Name: "site", Image: "nginx"}},
				Volumes:    []*v1.VolumeMount{{VolumeName: "data", MountPath: "/usr/share/nginx/html"}},
			},
		},
	}
	if err := deployments.Put(ctx, "default", "site", d); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne (initial): %v", err)
	}

	vol, err := r.volumes.Get(ctx, "default", "data")
	if err != nil {
		t.Fatalf("get volume: %v", err)
	}
	claimedNode := vol.GetStatus().GetNodeName()
	if claimedNode == "" {
		t.Fatal("volume was not claimed by any node")
	}
	if vol.GetMetadata().GetLabels()[OwnerDeploymentLabel] != "site" {
		t.Errorf("volume owner label = %q, want %q", vol.GetMetadata().GetLabels()[OwnerDeploymentLabel], "site")
	}

	// Simulate the pod being lost (e.g. deleted) and recreated: the replacement
	// must land on the SAME node the volume is already claimed on, regardless
	// of what the generic scheduler would otherwise pick.
	before, err := pods.List(ctx, "default")
	if err != nil || len(before) != 1 {
		t.Fatalf("expected 1 pod, got %d (err=%v)", len(before), err)
	}
	if err := pods.Delete(ctx, "default", before[0].GetMetadata().GetName()); err != nil {
		t.Fatalf("delete pod: %v", err)
	}

	if err := r.reconcileOne(ctx, d); err != nil {
		t.Fatalf("reconcileOne (after pod loss): %v", err)
	}

	after, err := pods.List(ctx, "default")
	if err != nil || len(after) != 1 {
		t.Fatalf("expected 1 replacement pod, got %d (err=%v)", len(after), err)
	}
	if after[0].GetSpec().GetNodeName() != claimedNode {
		t.Errorf("replacement pod landed on %q, want the already-claimed node %q", after[0].GetSpec().GetNodeName(), claimedNode)
	}
}

func TestReconcileOneRecordsPendingReasonOnPortConflict(t *testing.T) {
	ctx := context.Background()
	r, deployments, pods, nodes := newTestReconciler()
	seedReadyNode(t, ctx, nodes, "node-1")

	first := &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "web-1", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas: 1,
			Selector: map[string]string{"app": "web-1"},
			Template: &v1.PodSpec{Containers: []*v1.Container{{Name: "web-1", Image: "nginx", ContainerPorts: []int32{80}}}},
		},
	}
	second := &v1.Deployment{
		Metadata: &v1.ObjectMeta{Name: "web-2", Namespace: "default"},
		Spec: &v1.DeploymentSpec{
			Replicas: 1,
			Selector: map[string]string{"app": "web-2"},
			Template: &v1.PodSpec{Containers: []*v1.Container{{Name: "web-2", Image: "nginx", ContainerPorts: []int32{80}}}},
		},
	}
	if err := deployments.Put(ctx, "default", "web-1", first); err != nil {
		t.Fatalf("seed first deployment: %v", err)
	}
	if err := deployments.Put(ctx, "default", "web-2", second); err != nil {
		t.Fatalf("seed second deployment: %v", err)
	}

	if err := r.reconcileOne(ctx, first); err != nil {
		t.Fatalf("reconcileOne(first): %v", err)
	}
	if err := r.reconcileOne(ctx, second); err != nil {
		t.Fatalf("reconcileOne(second): %v", err)
	}

	firstPods, err := pods.List(ctx, "default")
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	var p1, p2 *v1.Pod
	for _, p := range firstPods {
		switch p.GetMetadata().GetLabels()[OwnerDeploymentLabel] {
		case "web-1":
			p1 = p
		case "web-2":
			p2 = p
		}
	}
	if p1 == nil || p1.GetSpec().GetNodeName() != "node-1" {
		t.Fatalf("web-1's pod should have scheduled onto node-1, got %+v", p1)
	}
	if p2 == nil {
		t.Fatal("web-2's pod not found")
	}
	if p2.GetSpec().GetNodeName() != "" {
		t.Errorf("web-2's pod should be unscheduled (port 80 conflict), got node %q", p2.GetSpec().GetNodeName())
	}
	if p2.GetStatus().GetMessage() == "" {
		t.Error("web-2's pod should have a non-empty pending reason message")
	}

	// Free up the port (delete web-1's pod so port 80 is available again) and
	// confirm web-2 schedules and its pending message clears.
	if err := pods.Delete(ctx, "default", p1.GetMetadata().GetName()); err != nil {
		t.Fatalf("delete web-1 pod: %v", err)
	}
	if err := r.reconcileOne(ctx, second); err != nil {
		t.Fatalf("reconcileOne(second, after port freed): %v", err)
	}
	afterPods, err := pods.List(ctx, "default")
	if err != nil {
		t.Fatalf("list pods (after): %v", err)
	}
	var p2After *v1.Pod
	for _, p := range afterPods {
		if p.GetMetadata().GetLabels()[OwnerDeploymentLabel] == "web-2" {
			p2After = p
		}
	}
	if p2After == nil || p2After.GetSpec().GetNodeName() != "node-1" {
		t.Fatalf("web-2's pod should now be scheduled onto node-1, got %+v", p2After)
	}
	if p2After.GetStatus().GetMessage() != "" {
		t.Errorf("pending message should be cleared once scheduled, got %q", p2After.GetStatus().GetMessage())
	}
}
