package controller

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"time"

	"google.golang.org/protobuf/proto"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/registry"
	"github.com/gh0st-nemesis/nimbuscore/internal/scheduler"
	"github.com/gh0st-nemesis/nimbuscore/internal/store"
)

const OwnerDeploymentLabel = "nimbuscore.io/owner-deployment"

type DeploymentReconciler struct {
	deployments *registry.Registry[*v1.Deployment]
	pods        *registry.Registry[*v1.Pod]
	nodes       *registry.Registry[*v1.Node]
	volumes     *registry.Registry[*v1.Volume]
	scheduler   scheduler.Scheduler
	resync      time.Duration
}

func NewDeploymentReconciler(
	deployments *registry.Registry[*v1.Deployment],
	pods *registry.Registry[*v1.Pod],
	nodes *registry.Registry[*v1.Node],
	volumes *registry.Registry[*v1.Volume],
	sched scheduler.Scheduler,
	resync time.Duration,
) *DeploymentReconciler {
	if resync <= 0 {
		resync = 5 * time.Second
	}
	return &DeploymentReconciler{deployments: deployments, pods: pods, nodes: nodes, volumes: volumes, scheduler: sched, resync: resync}
}

func (r *DeploymentReconciler) Name() string { return "deployment-controller" }

func (r *DeploymentReconciler) Reconcile(ctx context.Context) error {
	ticker := time.NewTicker(r.resync)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			r.reconcileAll(ctx)
		}
	}
}

func (r *DeploymentReconciler) reconcileAll(ctx context.Context) {
	deployments, err := r.deployments.List(ctx, "")
	if err != nil {
		log.Printf("deployment-controller: list deployments: %v", err)
		return
	}
	for _, d := range deployments {
		if err := r.reconcileOne(ctx, d); err != nil {
			log.Printf("deployment-controller: %s/%s: %v", d.GetMetadata().GetNamespace(), d.GetMetadata().GetName(), err)
		}
	}
}

func (r *DeploymentReconciler) reconcileOne(ctx context.Context, d *v1.Deployment) error {
	owned, err := r.ownedPods(ctx, d)
	if err != nil {
		return fmt.Errorf("list owned pods: %w", err)
	}

	mounts := d.GetSpec().GetTemplate().GetVolumes()

	want := int(d.GetSpec().GetReplicas())
	if len(mounts) > 0 && want > 1 {
		log.Printf("deployment-controller: %s/%s: clamping replicas %d to 1, volume-backed deployments cannot scale beyond a single replica",
			d.GetMetadata().GetNamespace(), d.GetMetadata().GetName(), want)
		want = 1
	}
	have := len(owned)

	switch {
	case have < want:
		claimedNode := ""
		if len(mounts) > 0 {
			node, err := r.resolveVolumeNode(ctx, d.GetMetadata().GetNamespace(), mounts)
			if err != nil {
				return fmt.Errorf("resolve volume node: %w", err)
			}
			claimedNode = node
		}
		for i := have; i < want; i++ {
			pod := r.newPod(d, i)
			if claimedNode != "" {
				pod.Spec.NodeName = claimedNode
			}
			if err := r.pods.Put(ctx, pod.GetMetadata().GetNamespace(), pod.GetMetadata().GetName(), pod); err != nil {
				return fmt.Errorf("create pod: %w", err)
			}
			log.Printf("deployment-controller: %s/%s: created %s", d.GetMetadata().GetNamespace(), d.GetMetadata().GetName(), pod.GetMetadata().GetName())
		}
	case have > want:
		for i := want; i < have; i++ {
			p := owned[i]
			if err := r.pods.Delete(ctx, p.GetMetadata().GetNamespace(), p.GetMetadata().GetName()); err != nil {
				return fmt.Errorf("delete pod: %w", err)
			}
			log.Printf("deployment-controller: %s/%s: deleted %s", d.GetMetadata().GetNamespace(), d.GetMetadata().GetName(), p.GetMetadata().GetName())
		}
	}

	owned, err = r.ownedPods(ctx, d)
	if err != nil {
		return fmt.Errorf("re-list owned pods: %w", err)
	}

	if err := r.scheduleUnassigned(ctx, owned); err != nil {
		log.Printf("deployment-controller: %s/%s: schedule: %v", d.GetMetadata().GetNamespace(), d.GetMetadata().GetName(), err)
	}

	if len(mounts) > 0 {
		if err := r.claimVolumeNodes(ctx, d, owned); err != nil {
			log.Printf("deployment-controller: %s/%s: claim volume node: %v", d.GetMetadata().GetNamespace(), d.GetMetadata().GetName(), err)
		}
	}

	var ready int32
	for _, p := range owned {
		if p.GetStatus().GetPhase() == v1.PodPhase_POD_PHASE_RUNNING {
			ready++
		}
	}

	current, err := r.deployments.Get(ctx, d.GetMetadata().GetNamespace(), d.GetMetadata().GetName())
	if err != nil {
		return fmt.Errorf("re-fetch deployment before status update: %w", err)
	}
	current.Status = &v1.DeploymentStatus{
		Replicas:      int32(len(owned)),
		ReadyReplicas: ready,
	}
	return r.deployments.Put(ctx, current.GetMetadata().GetNamespace(), current.GetMetadata().GetName(), current)
}

func (r *DeploymentReconciler) ownedPods(ctx context.Context, d *v1.Deployment) ([]*v1.Pod, error) {
	all, err := r.pods.List(ctx, d.GetMetadata().GetNamespace())
	if err != nil {
		return nil, err
	}

	owned := all[:0]
	for _, p := range all {
		if p.GetMetadata().GetLabels()[OwnerDeploymentLabel] != d.GetMetadata().GetName() {
			continue
		}

		if nodeName := p.GetSpec().GetNodeName(); nodeName != "" {
			ready, err := r.nodeReady(ctx, nodeName)
			if err != nil {
				return nil, err
			}
			if !ready {
				log.Printf("deployment-controller: %s/%s: evicting %s from dead node %s",
					d.GetMetadata().GetNamespace(), d.GetMetadata().GetName(), p.GetMetadata().GetName(), nodeName)
				if err := r.pods.Delete(ctx, p.GetMetadata().GetNamespace(), p.GetMetadata().GetName()); err != nil {
					return nil, err
				}
				continue
			}
		}

		if phase := p.GetStatus().GetPhase(); phase == v1.PodPhase_POD_PHASE_SUCCEEDED || phase == v1.PodPhase_POD_PHASE_FAILED {
			log.Printf("deployment-controller: %s/%s: replacing %s, reached terminal phase %s",
				d.GetMetadata().GetNamespace(), d.GetMetadata().GetName(), p.GetMetadata().GetName(), phase)
			if err := r.pods.Delete(ctx, p.GetMetadata().GetNamespace(), p.GetMetadata().GetName()); err != nil {
				return nil, err
			}
			continue
		}

		owned = append(owned, p)
	}
	return owned, nil
}

func (r *DeploymentReconciler) nodeReady(ctx context.Context, name string) (bool, error) {
	node, err := r.nodes.Get(ctx, "", name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return node.GetStatus().GetReady(), nil
}

type nodeResourceUsage struct {
	cpuMillis    int64
	memoryBytes  int64
	accelerators map[string]int64
	ports        map[int32]bool
}

func (r *DeploymentReconciler) nodeUsage(ctx context.Context) (map[string]nodeResourceUsage, error) {
	return computeNodeUsage(ctx, r.pods)
}

func computeNodeUsage(ctx context.Context, pods *registry.Registry[*v1.Pod]) (map[string]nodeResourceUsage, error) {
	all, err := pods.List(ctx, "")
	if err != nil {
		return nil, err
	}

	usage := make(map[string]nodeResourceUsage)
	for _, p := range all {
		nodeName := p.GetSpec().GetNodeName()
		if nodeName == "" {
			continue
		}
		u := usage[nodeName]
		if u.accelerators == nil {
			u.accelerators = make(map[string]int64)
		}
		if u.ports == nil {
			u.ports = make(map[int32]bool)
		}
		for _, c := range p.GetSpec().GetContainers() {
			u.cpuMillis += c.GetResources().GetRequests().GetCpuMillis()
			u.memoryBytes += c.GetResources().GetRequests().GetMemoryBytes()
			for name, count := range c.GetResources().GetRequests().GetAccelerators() {
				u.accelerators[name] += count
			}
			for _, port := range c.GetContainerPorts() {
				u.ports[port] = true
			}
		}
		usage[nodeName] = u
	}
	return usage, nil
}

func (r *DeploymentReconciler) scheduleUnassigned(ctx context.Context, pods []*v1.Pod) error {
	var unassigned []*v1.Pod
	for _, p := range pods {
		if p.GetSpec().GetNodeName() == "" {
			unassigned = append(unassigned, p)
		}
	}
	if len(unassigned) == 0 {
		return nil
	}

	usage, err := r.nodeUsage(ctx)
	if err != nil {
		return fmt.Errorf("compute node usage: %w", err)
	}

	candidates, err := buildNodeCandidates(ctx, r.nodes, usage)
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}

	for _, p := range unassigned {
		if err := schedulePod(ctx, r.pods, r.scheduler, candidates, p); err != nil {
			return err
		}
	}
	return nil
}

// resolveVolumeNode returns the node a set of VolumeMounts is already pinned
// to, if any of the referenced Volumes has already been claimed by a node.
// Hostpath volumes are node-local, so once claimed every pod using them must
// land on that same node.
func (r *DeploymentReconciler) resolveVolumeNode(ctx context.Context, namespace string, mounts []*v1.VolumeMount) (string, error) {
	for _, m := range mounts {
		vol, err := r.volumes.Get(ctx, namespace, m.GetVolumeName())
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return "", err
		}
		if nodeName := vol.GetStatus().GetNodeName(); nodeName != "" {
			return nodeName, nil
		}
	}
	return "", nil
}

// claimVolumeNodes stamps the node a Deployment's volumes were scheduled onto
// back onto the Volume objects themselves (first pod to use a volume claims
// it for that node, permanently), and records ownership so a volume can't be
// silently shared between two different Deployments.
func (r *DeploymentReconciler) claimVolumeNodes(ctx context.Context, d *v1.Deployment, owned []*v1.Pod) error {
	mounts := d.GetSpec().GetTemplate().GetVolumes()
	if len(mounts) == 0 {
		return nil
	}
	namespace := d.GetMetadata().GetNamespace()

	var nodeName string
	for _, p := range owned {
		if n := p.GetSpec().GetNodeName(); n != "" {
			nodeName = n
			break
		}
	}
	if nodeName == "" {
		return nil
	}

	for _, m := range mounts {
		vol, err := r.volumes.Get(ctx, namespace, m.GetVolumeName())
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return err
		}
		if vol.GetStatus().GetNodeName() != "" {
			continue
		}
		if vol.Status == nil {
			vol.Status = &v1.VolumeStatus{}
		}
		vol.Status.NodeName = nodeName
		if vol.Metadata.Labels == nil {
			vol.Metadata.Labels = make(map[string]string, 1)
		}
		vol.Metadata.Labels[OwnerDeploymentLabel] = d.GetMetadata().GetName()
		if err := r.volumes.Put(ctx, namespace, vol.GetMetadata().GetName(), vol); err != nil {
			return err
		}
		log.Printf("deployment-controller: %s/%s: claimed volume %s onto node %s", namespace, d.GetMetadata().GetName(), m.GetVolumeName(), nodeName)
	}
	return nil
}

func (r *DeploymentReconciler) newPod(d *v1.Deployment, index int) *v1.Pod {
	labels := make(map[string]string, len(d.GetSpec().GetSelector())+1)
	maps.Copy(labels, d.GetSpec().GetSelector())
	labels[OwnerDeploymentLabel] = d.GetMetadata().GetName()

	return &v1.Pod{
		Metadata: &v1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%d", d.GetMetadata().GetName(), index),
			Namespace: d.GetMetadata().GetNamespace(),
			Labels:    labels,
		},
		// Cloned so each pod owns its Spec independently — callers (e.g. volume
		// node-pinning) set fields like NodeName per-pod before Put, and every
		// replica would otherwise share the exact same template pointer.
		Spec: proto.Clone(d.GetSpec().GetTemplate()).(*v1.PodSpec),
		Status: &v1.PodStatus{
			Phase: v1.PodPhase_POD_PHASE_PENDING,
		},
	}
}
