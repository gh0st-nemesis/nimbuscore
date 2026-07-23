package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

type fakePodServiceClient struct {
	mu   sync.Mutex
	pods map[string]*v1.Pod
}

func newFakePodServiceClient() *fakePodServiceClient {
	return &fakePodServiceClient{pods: make(map[string]*v1.Pod)}
}

func (f *fakePodServiceClient) seed(pod *v1.Pod) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pods[podKey(pod)] = pod
}

func (f *fakePodServiceClient) statusOf(key string) *v1.PodStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.pods[key]; ok {
		return p.GetStatus()
	}
	return nil
}

func (f *fakePodServiceClient) CreatePod(context.Context, *v1.CreatePodRequest, ...grpc.CallOption) (*v1.Pod, error) {
	panic("not used by agent")
}
func (f *fakePodServiceClient) GetPod(context.Context, *v1.GetPodRequest, ...grpc.CallOption) (*v1.Pod, error) {
	panic("not used by agent")
}
func (f *fakePodServiceClient) DeletePod(context.Context, *v1.DeletePodRequest, ...grpc.CallOption) (*v1.DeletePodResponse, error) {
	panic("not used by agent")
}

func (f *fakePodServiceClient) ListPods(context.Context, *v1.ListPodsRequest, ...grpc.CallOption) (*v1.ListPodsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]*v1.Pod, 0, len(f.pods))
	for _, p := range f.pods {
		items = append(items, p)
	}
	return &v1.ListPodsResponse{Items: items}, nil
}

func (f *fakePodServiceClient) UpdatePodStatus(_ context.Context, req *v1.UpdatePodStatusRequest, _ ...grpc.CallOption) (*v1.Pod, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := req.GetNamespace() + "/" + req.GetName()
	pod, ok := f.pods[key]
	if !ok {
		return nil, context.Canceled
	}
	pod.Status = req.GetStatus()
	return pod, nil
}

func waitForPhase(t *testing.T, client *fakePodServiceClient, key string, phase v1.PodPhase) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if status := client.statusOf(key); status != nil && status.GetPhase() == phase {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for pod %s to reach phase %s (last: %v)", key, phase, client.statusOf(key))
}

func TestAgentRunsAssignedPodToCompletion(t *testing.T) {
	client := newFakePodServiceClient()
	pod := testPod("done", helperCommand(t, "exit0"), v1.RestartPolicy_RESTART_POLICY_NEVER)
	client.seed(pod)

	a := New(Config{NodeName: "test-node", Interval: 30 * time.Millisecond}, client, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)

	waitForPhase(t, client, podKey(pod), v1.PodPhase_POD_PHASE_SUCCEEDED)
}

func TestAgentRestartsFailedPodUnderAlwaysPolicy(t *testing.T) {
	client := newFakePodServiceClient()
	pod := testPod("flaky", helperCommand(t, "exit1"), v1.RestartPolicy_RESTART_POLICY_ALWAYS)
	client.seed(pod)

	a := New(Config{NodeName: "test-node", Interval: 30 * time.Millisecond}, client, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if status := client.statusOf(podKey(pod)); status != nil && status.GetRestartCount() >= 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pod was not restarted at least twice within the deadline (last status: %v)", client.statusOf(podKey(pod)))
}

func TestAgentIgnoresPodsAssignedToOtherNodes(t *testing.T) {
	client := newFakePodServiceClient()
	pod := testPod("elsewhere", helperCommand(t, "exit0"), v1.RestartPolicy_RESTART_POLICY_NEVER)
	pod.Spec.NodeName = "some-other-node"
	client.seed(pod)

	a := New(Config{NodeName: "test-node", Interval: 30 * time.Millisecond}, client, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)

	time.Sleep(300 * time.Millisecond)
	if a.runtime.has(podKey(pod)) {
		t.Error("agent started a pod assigned to a different node")
	}
}
