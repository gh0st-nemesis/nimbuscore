package agent

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	tasksapi "github.com/containerd/containerd/api/services/tasks/v1"
	taskapi "github.com/containerd/containerd/api/types/task"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// containerdNamespace matches the "default" namespace `ctr` itself uses
// when no -n flag is given, keeping the agent compatible with containers
// inspected or managed via the ctr CLI.
const containerdNamespace = "default"

// containerdCallTimeout bounds the housekeeping calls (task listing, PID
// lookup) that the reconcile loop makes on every tick regardless of whether
// any pod actually uses containerd. Without a bound, a missing or
// unreachable daemon would stall the whole reconcile loop for as long as
// gRPC's connection backoff allows.
const containerdCallTimeout = 2 * time.Second

// containerdTeardownTimeout bounds container removal: killing a real running
// process and unmounting its overlay snapshot can legitimately take longer
// than containerdCallTimeout under load (e.g. a concurrent buildkit build
// hammering the same daemon). Cutting that short used to leave the old
// process's port not yet released by the time the restarted container tried
// to bind it, producing a spurious EADDRINUSE crash loop.
const containerdTeardownTimeout = 30 * time.Second

var (
	cdClientOnce sync.Once
	cdClient     *containerd.Client
	cdClientErr  error
)

// containerdClient returns a shared client connected to the local containerd
// daemon. The client is safe for concurrent use and is dialed once per agent
// process.
func containerdClient() (*containerd.Client, error) {
	cdClientOnce.Do(func() {
		cdClient, cdClientErr = containerd.New(defaults.DefaultAddress,
			containerd.WithDefaultNamespace(containerdNamespace),
			containerd.WithTimeout(containerdCallTimeout),
		)
	})
	return cdClient, cdClientErr
}

func pullContainerdImage(ctx context.Context, ref string) (containerd.Image, error) {
	cclient, err := containerdClient()
	if err != nil {
		return nil, err
	}
	image, err := cclient.Pull(ctx, ref, containerd.WithPullUnpack)
	if err != nil {
		return nil, fmt.Errorf("agent: pull image %s: %w", ref, err)
	}
	return image, nil
}

// localContainerdImage looks up an image already present in containerd's
// image store, without attempting a registry pull. Used for images buildctl
// just exported directly into containerd (buildImageFromSource's
// type=image,unpack=true output), which have no matching remote to pull from.
func localContainerdImage(ctx context.Context, ref string) (containerd.Image, error) {
	cclient, err := containerdClient()
	if err != nil {
		return nil, err
	}
	image, err := cclient.GetImage(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("agent: look up built image %s: %w", ref, err)
	}
	return image, nil
}

// containerdRunSpec describes a container run request, mirroring the ctr CLI
// flags the agent used to shell out with (-d --net-host --log-uri --cpus
// --memory-limit --env --mount).
type containerdRunSpec struct {
	id        string
	image     containerd.Image
	command   []string
	env       map[string]string
	cpuMillis int64
	memBytes  int64
	mounts    []specs.Mount
	logPath   string
}

func runContainerdContainer(ctx context.Context, spec containerdRunSpec) error {
	cclient, err := containerdClient()
	if err != nil {
		return err
	}

	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("agent: get hostname: %w", err)
	}

	envKeys := make([]string, 0, len(spec.env))
	for k := range spec.env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	envSlice := make([]string, 0, len(envKeys))
	for _, k := range envKeys {
		envSlice = append(envSlice, k+"="+spec.env[k])
	}

	specOpts := []oci.SpecOpts{
		oci.WithDefaultSpec(),
		oci.WithDefaultUnixDevices,
		oci.WithImageConfig(spec.image),
		oci.WithEnv(envSlice),
		// Mirrors ctr run --net-host: share the host's network namespace,
		// hosts file and resolv.conf instead of setting up a bridge/CNI.
		oci.WithHostNamespace(specs.NetworkNamespace),
		oci.WithHostHostsFile,
		oci.WithHostResolvconf,
		oci.WithEnv([]string{"HOSTNAME=" + hostname}),
	}
	if len(spec.command) > 0 {
		specOpts = append(specOpts, oci.WithProcessArgs(spec.command...))
	}
	if len(spec.mounts) > 0 {
		specOpts = append(specOpts, oci.WithMounts(spec.mounts))
	}
	if spec.cpuMillis > 0 {
		const period = uint64(100000)
		specOpts = append(specOpts, oci.WithCPUCFS(spec.cpuMillis*100, period)) // quota: 1000 millis == 1 CPU == full period
	}
	if spec.memBytes > 0 {
		specOpts = append(specOpts, oci.WithMemoryLimit(uint64(spec.memBytes)))
	}

	container, err := cclient.NewContainer(ctx, spec.id,
		containerd.WithImage(spec.image),
		containerd.WithNewSnapshot(spec.id, spec.image),
		containerd.WithNewSpec(specOpts...),
	)
	if err != nil {
		return fmt.Errorf("agent: create container %s: %w", spec.id, err)
	}

	ioCreator := cio.NullIO
	if spec.logPath != "" {
		ioCreator = cio.LogFile(spec.logPath)
	}

	containerTask, err := container.NewTask(ctx, ioCreator)
	if err != nil {
		container.Delete(ctx, containerd.WithSnapshotCleanup) //nolint:errcheck
		return fmt.Errorf("agent: create task for %s: %w", spec.id, err)
	}
	if err := containerTask.Start(ctx); err != nil {
		containerTask.Delete(ctx, containerd.WithProcessKill) //nolint:errcheck
		container.Delete(ctx, containerd.WithSnapshotCleanup)  //nolint:errcheck
		return fmt.Errorf("agent: start task for %s: %w", spec.id, err)
	}
	return nil
}

// removeContainerdContainer forcefully kills and removes a container's task
// (if any) along with the container and its snapshot. It is a no-op if the
// container does not exist.
func removeContainerdContainer(id string) {
	cclient, err := containerdClient()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), containerdTeardownTimeout)
	defer cancel()

	c, err := cclient.LoadContainer(ctx, id)
	if err != nil {
		return
	}
	if t, err := c.Task(ctx, nil); err == nil {
		t.Delete(ctx, containerd.WithProcessKill) //nolint:errcheck
	}
	c.Delete(ctx, containerd.WithSnapshotCleanup) //nolint:errcheck
}

// containerdTasks lists every task in the agent's containerd namespace in a
// single round trip, equivalent to `ctr task ls`.
func containerdTasks(ctx context.Context) ([]*taskapi.Process, error) {
	cclient, err := containerdClient()
	if err != nil {
		return nil, err
	}
	resp, err := cclient.TaskService().List(ctx, &tasksapi.ListTasksRequest{})
	if err != nil {
		return nil, err
	}
	return resp.Tasks, nil
}

func containerdTaskPID(containerID string) (int32, error) {
	ctx, cancel := context.WithTimeout(context.Background(), containerdCallTimeout)
	defer cancel()
	procs, err := containerdTasks(ctx)
	if err != nil {
		return 0, err
	}
	for _, p := range procs {
		if p.ID == containerID {
			return int32(p.Pid), nil
		}
	}
	return 0, fmt.Errorf("agent: task %s not found", containerID)
}

// listContainerdTaskStatuses reports, for every task in the namespace,
// whether it is currently running.
func listContainerdTaskStatuses() (map[string]bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), containerdCallTimeout)
	defer cancel()
	procs, err := containerdTasks(ctx)
	if err != nil {
		return nil, err
	}
	running := make(map[string]bool, len(procs))
	for _, p := range procs {
		running[p.ID] = p.Status == taskapi.Status_RUNNING
	}
	return running, nil
}
