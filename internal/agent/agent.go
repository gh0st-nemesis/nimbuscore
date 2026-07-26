package agent

import (
	"context"
	"log"
	"strings"
	"time"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

type Config struct {
	NodeName     string
	InternalIP   string
	Interval     time.Duration
	LogDir       string
	BuildDir     string
	BuildkitAddr string
	VolumesDir   string
}

type Agent struct {
	cfg       Config
	pods      v1.PodServiceClient
	services  v1.ServiceServiceClient
	runtime   *processRuntime
	nodePorts *nodePortManager
}

func New(cfg Config, pods v1.PodServiceClient, services v1.ServiceServiceClient) *Agent {
	if cfg.Interval <= 0 {
		cfg.Interval = 2 * time.Second
	}
	runtime := newProcessRuntime()
	runtime.logDir = cfg.LogDir
	runtime.buildDir = cfg.BuildDir
	runtime.buildkitAddr = cfg.BuildkitAddr
	runtime.volumesDir = cfg.VolumesDir
	return &Agent{cfg: cfg, pods: pods, services: services, runtime: runtime, nodePorts: newNodePortManager()}
}

func (a *Agent) Run(ctx context.Context) error {
	log.Printf("agent: node %q watching assigned pods", a.cfg.NodeName)
	ticker := time.NewTicker(a.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			a.runtime.stopAll()
			a.nodePorts.stopAll()
			return ctx.Err()
		case ev := <-a.runtime.exited:
			a.handleExit(ctx, ev)
		case <-ticker.C:
			a.reconcile(ctx)
			a.reconcileServices(ctx)
		}
	}
}

func (a *Agent) reconcileServices(ctx context.Context) {
	if a.services == nil || a.cfg.InternalIP == "" {
		return
	}
	resp, err := a.services.ListServices(ctx, &v1.ListServicesRequest{})
	if err != nil {
		log.Printf("agent: list services: %v", err)
		return
	}
	a.nodePorts.reconcile(resp.GetItems(), a.cfg.InternalIP)
}

func (a *Agent) reconcile(ctx context.Context) {
	resp, err := a.pods.ListPods(ctx, &v1.ListPodsRequest{})
	if err != nil {
		log.Printf("agent: list pods: %v", err)
		return
	}

	desired := make(map[string]*v1.Pod)
	for _, pod := range resp.GetItems() {
		if pod.GetSpec().GetNodeName() != a.cfg.NodeName {
			continue
		}
		desired[podKey(pod)] = pod
	}

	a.runtime.checkContainerdExits()
	a.reconcileOrphanContainers(desired)

	for key := range desired {
		if !a.runtime.has(key) {
			pod := desired[key]
			if a.runtime.adoptIfRunning(pod) {
				log.Printf("agent: adopted already-running pod %s", key)
			} else {
				a.startPod(ctx, pod)
			}
		}
	}

	for _, key := range a.runtime.keys() {
		if _, ok := desired[key]; !ok {
			a.runtime.stop(key)
		}
	}

	for _, key := range a.runtime.keys() {
		pod := a.runtime.podOf(key)
		if pod == nil {
			continue
		}
		a.reportStatus(ctx, pod, v1.PodPhase_POD_PHASE_RUNNING, "", a.runtime.restartCount(key), a.runtime.memoryUsageBytes(key))
	}
}

func (a *Agent) reconcileOrphanContainers(desired map[string]*v1.Pod) {
	wanted := make(map[string]bool, len(desired))
	for _, pod := range desired {
		wanted[containerName(pod)] = true
	}
	removeOrphanContainers(wanted)
}

func removeOrphanContainers(wanted map[string]bool) {
	statuses, err := listContainerdTaskStatuses()
	if err != nil {
		return
	}

	for name := range statuses {
		if !strings.HasPrefix(name, "nimbus-") || wanted[name] {
			continue
		}
		log.Printf("agent: removing orphaned container %s (no matching pod)", name)
		removeContainerdContainer(name)
	}
}

func (a *Agent) startPod(ctx context.Context, pod *v1.Pod) {
	key := podKey(pod)
	if err := a.runtime.start(pod); err != nil {
		log.Printf("agent: start pod %s: %v", key, err)
		a.reportStatus(ctx, pod, v1.PodPhase_POD_PHASE_FAILED, err.Error(), 0, 0)
		return
	}
	log.Printf("agent: started pod %s", key)
	a.reportStatus(ctx, pod, v1.PodPhase_POD_PHASE_RUNNING, "", 0, 0)
}

func (a *Agent) handleExit(ctx context.Context, ev exitEvent) {
	pod := a.runtime.podOf(ev.key)
	if pod == nil {
		return
	}
	if a.runtime.stillRunning(ev.key) {
		// Stale event: queued while the single-threaded reconcile loop was
		// busy elsewhere (e.g. rebuilding a different pod's image), and no
		// longer reflects reality now that we're getting around to it.
		log.Printf("agent: ignoring stale exit event for pod %s (task is still running)", ev.key)
		return
	}

	restartPolicy := pod.GetSpec().GetRestartPolicy()
	shouldRestart := restartPolicy == v1.RestartPolicy_RESTART_POLICY_UNSPECIFIED ||
		restartPolicy == v1.RestartPolicy_RESTART_POLICY_ALWAYS ||
		(restartPolicy == v1.RestartPolicy_RESTART_POLICY_ON_FAILURE && ev.exitCode != 0)

	if shouldRestart {
		log.Printf("agent: pod %s exited (code %d), restarting", ev.key, ev.exitCode)
		if err := a.runtime.restart(pod); err != nil {
			log.Printf("agent: restart pod %s: %v", ev.key, err)
			a.reportStatus(ctx, pod, v1.PodPhase_POD_PHASE_FAILED, err.Error(), a.runtime.restartCount(ev.key), 0)
			return
		}
		a.reportStatus(ctx, pod, v1.PodPhase_POD_PHASE_RUNNING, "", a.runtime.restartCount(ev.key), 0)
		return
	}

	phase := v1.PodPhase_POD_PHASE_SUCCEEDED
	if ev.exitCode != 0 {
		phase = v1.PodPhase_POD_PHASE_FAILED
	}
	log.Printf("agent: pod %s exited (code %d), terminal phase %s", ev.key, ev.exitCode, phase)
	a.runtime.stop(ev.key)
	a.reportStatus(ctx, pod, phase, "", 0, 0)
}

func (a *Agent) reportStatus(ctx context.Context, pod *v1.Pod, phase v1.PodPhase, message string, restarts int32, memoryUsageBytes int64) {
	meta := pod.GetMetadata()
	_, err := a.pods.UpdatePodStatus(ctx, &v1.UpdatePodStatusRequest{
		Namespace: meta.GetNamespace(),
		Name:      meta.GetName(),
		Status: &v1.PodStatus{
			Phase:            phase,
			Message:          message,
			RestartCount:     restarts,
			MemoryUsageBytes: memoryUsageBytes,
		},
	})
	if err != nil {
		log.Printf("agent: report status for %s: %v", podKey(pod), err)
	}
}
