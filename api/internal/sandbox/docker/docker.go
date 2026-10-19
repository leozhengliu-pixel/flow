// Package docker is the Docker sandbox provider for local development and CI
// (track A): one container per sandbox on FLOW_CODING_DOCKER_NETWORK through
// the Docker Engine API at FLOW_CODING_DOCKER_HOST (plain HTTP, no SDK).
// Workspaces are named volumes; VolumeSnapshots are unsupported (use the
// archive strategy).
//
// The container mirrors the Kubernetes Pod's security (design §9): uid 1000,
// all capabilities dropped, no-new-privileges, a read-only root filesystem
// with tmpfs /tmp and volumes for /workspace, /home/flow and the token,
// pids/memory/cpu limits, docker-init as PID 1 and the configured --runtime.
package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"flow/api/internal/config"
	"flow/api/internal/sandbox"
)

func init() { sandbox.Register(config.CodingProviderDocker, New) }

// Object types (sandbox.ManagedObject.Type).
const (
	TypeContainer = "container"
	TypeVolume    = "volume"
	// labelRestore marks containers whose entrypoint restores an archive
	// first, so exit code sandbox.RestoreExitCode maps to InitFailed.
	labelRestore = "flow.dev/restore"
	// labelVolumeID records the workspace volume a container mounts.
	labelVolumeID = "flow.dev/volume-id"
	tmpfsOptions  = "rw,nosuid,nodev,size=4g"
)

// ContainerName, WorkspaceVolume, HomeVolume and TokenVolume are the
// deterministic names of a sandbox's objects.
func ContainerName(sandboxID string) string  { return sandbox.ObjectName("cs", sandboxID) }
func WorkspaceVolume(volumeID string) string { return sandbox.ObjectName("ws", volumeID) }
func HomeVolume(sandboxID string) string     { return sandbox.ObjectName("home", sandboxID) }
func TokenVolume(sandboxID string) string    { return ContainerName(sandboxID) + "-token" }
func containerVolumeID(spec sandbox.SandboxSpec) string {
	if spec.Workspace.VolumeID != "" {
		return spec.Workspace.VolumeID
	}
	return spec.ID
}

// Provider drives sandboxes as Docker containers.
type Provider struct {
	coding config.CodingConfig
	client Client
	now    func() time.Time
	pullMu sync.Mutex
}

// New builds the provider from FLOW_CODING_DOCKER_*.
func New(deps sandbox.Dependencies) (sandbox.Provider, error) {
	client, err := NewHTTPClient(deps.Config.Docker.Host)
	if err != nil {
		return nil, fmt.Errorf("docker sandbox provider: %w", err)
	}
	return NewWithClient(deps, client)
}

// NewWithClient builds the provider over an Engine API client (tests).
func NewWithClient(deps sandbox.Dependencies, client Client) (*Provider, error) {
	c := deps.Config
	if c.InstanceID == "" {
		c.InstanceID = "flow"
	}
	if c.Snapshot.Strategy == sandbox.StrategyVolumeSnapshot {
		return nil, fmt.Errorf("docker sandbox provider: FLOW_CODING_SNAPSHOT_STRATEGY=volumesnapshot is not supported (use archive)")
	}
	for name, size := range c.Sizes {
		if _, _, err := limits(sandbox.Resources{CPU: size.CPU, Memory: size.Memory}); err != nil {
			return nil, fmt.Errorf("docker sandbox provider: FLOW_CODING_SIZE_%s: %w", strings.ToUpper(name), err)
		}
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &Provider{coding: c, client: client, now: now}, nil
}

func (p *Provider) Name() string { return config.CodingProviderDocker }

func (p *Provider) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{PersistentVolumes: true, Exec: true, TTY: true, RuntimeClass: p.coding.Docker.Runtime != ""}
}

// wrap maps Engine API errors to the sandbox contract.
func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	if IsNotFound(err) {
		return fmt.Errorf("%s: %w: %v", op, sandbox.ErrNotFound, err)
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.Status >= 500 || apiErr.Status == 409 || apiErr.Status == 429 {
			return fmt.Errorf("%s: %w: %v", op, sandbox.ErrTransient, err)
		}
		return fmt.Errorf("%s: %w", op, err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("%s: %w: %v", op, sandbox.ErrTransient, err)
	}
	return fmt.Errorf("%s: %w: %v", op, sandbox.ErrTransient, err)
}

// limits converts Kubernetes quantities to Docker's memory bytes and nano CPUs.
func limits(r sandbox.Resources) (memory, nanoCPUs int64, err error) {
	if r.Memory != "" {
		q, err := resource.ParseQuantity(r.Memory)
		if err != nil {
			return 0, 0, fmt.Errorf("memory %q: %w", r.Memory, err)
		}
		memory = q.Value()
	}
	if r.CPU != "" {
		q, err := resource.ParseQuantity(r.CPU)
		if err != nil {
			return 0, 0, fmt.Errorf("cpu %q: %w", r.CPU, err)
		}
		nanoCPUs = q.MilliValue() * 1_000_000
	}
	return memory, nanoCPUs, nil
}

func (p *Provider) labels(spec sandbox.SandboxSpec) map[string]string {
	labels := sandbox.ObjectLabels(p.coding, spec)
	labels[labelVolumeID] = containerVolumeID(spec)
	if spec.Workspace.ArchiveURL != "" {
		labels[labelRestore] = "1"
	}
	return labels
}

// BuildContainer is the container config of a sandbox (pure, deterministic).
func (p *Provider) BuildContainer(spec sandbox.SandboxSpec) (ContainerConfig, error) {
	spec = sandbox.CompleteSpec(p.coding, spec)
	mode := sandbox.Mode(spec.Kind)
	memory, nanoCPUs, err := limits(spec.Resources)
	if err != nil {
		return ContainerConfig{}, err
	}
	env := []string{}
	for _, item := range sandbox.RunnerEnv(p.coding, spec, mode) {
		env = append(env, item.Name+"="+item.Value)
	}
	stopTimeout := int(sandbox.TerminationGrace(p.coding.Timeouts.StopGrace) / time.Second)
	config := ContainerConfig{
		Image:        spec.Image,
		User:         strconv.Itoa(sandbox.RunnerUID) + ":" + strconv.Itoa(sandbox.RunnerUID),
		Env:          env,
		Entrypoint:   []string{"flow-runner"},
		Cmd:          []string{mode},
		WorkingDir:   sandbox.WorkspacePath,
		Labels:       p.labels(spec),
		StopSignal:   "SIGTERM",
		StopTimeout:  &stopTimeout,
		AttachStdout: false,
		AttachStderr: false,
	}
	if spec.Workspace.ArchiveURL != "" {
		// Docker has no init containers: restore inline with the same
		// runner mode the Pod's init container uses, then become the runner.
		config.Entrypoint = []string{"/bin/sh", "-c", fmt.Sprintf(`flow-runner restore || exit %d; exec flow-runner "$0"`, sandbox.RestoreExitCode)}
		config.Env = append(config.Env, sandbox.EnvArchiveURL+"="+spec.Workspace.ArchiveURL)
	}
	host := HostConfig{
		Init:           true,
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges:true"},
		ReadonlyRootfs: true,
		Tmpfs:          map[string]string{"/tmp": tmpfsOptions},
		Mounts: []Mount{
			{Type: "volume", Source: WorkspaceVolume(containerVolumeID(spec)), Target: sandbox.WorkspacePath},
			{Type: "volume", Source: HomeVolume(spec.ID), Target: sandbox.HomePath},
			// Written once before start through the archive API (as uid
			// 1000, mode 0400), so it works with remote daemons too.
			{Type: "volume", Source: TokenVolume(spec.ID), Target: "/var/run/flow"},
		},
		PidsLimit:   spec.Resources.PIDs,
		Memory:      memory,
		MemorySwap:  memory, // no swap beyond the memory limit
		NanoCPUs:    nanoCPUs,
		Runtime:     p.coding.Docker.Runtime,
		NetworkMode: p.coding.Docker.Network,
		IpcMode:     "private",
	}
	host.RestartPolicy.Name = "no"
	config.HostConfig = host
	return config, nil
}

func (p *Provider) ensureVolume(ctx context.Context, name string, labels map[string]string) (Volume, error) {
	volume, err := p.client.VolumeInspect(ctx, name)
	if err == nil {
		return volume, nil
	}
	if !IsNotFound(err) {
		return Volume{}, wrap("inspect volume", err)
	}
	volume, err = p.client.VolumeCreate(ctx, name, labels)
	if err != nil {
		return Volume{}, wrap("create volume", err)
	}
	return volume, nil
}

// tokenArchive is a tar with the token file owned by the runner, mode 0400.
func tokenArchive(token string, now time.Time) (*bytes.Buffer, error) {
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	if err := writer.WriteHeader(&tar.Header{Name: "token", Mode: 0o400, Size: int64(len(token)), Uid: sandbox.RunnerUID, Gid: sandbox.RunnerUID, ModTime: now, Typeflag: tar.TypeReg}); err != nil {
		return nil, err
	}
	if _, err := io.WriteString(writer, token); err != nil {
		return nil, err
	}
	return &buffer, writer.Close()
}

// StartSandbox creates the volumes and the container, writes the token and
// starts it. Starting an existing sandbox starts it if it was only created
// and otherwise returns its status.
func (p *Provider) StartSandbox(ctx context.Context, spec sandbox.SandboxSpec) (sandbox.SandboxStatus, error) {
	if !sandbox.ValidID(spec.ID) {
		return sandbox.SandboxStatus{}, fmt.Errorf("docker: invalid sandbox id %q", spec.ID)
	}
	name := ContainerName(spec.ID)
	existing, err := p.client.ContainerInspect(ctx, name)
	switch {
	case err == nil && existing.State.Status == "created":
		// A previous attempt crashed between create and start; the token
		// is rewritten so it matches the freshly minted one.
		if spec.Token != "" {
			if err := p.writeToken(ctx, name, spec.Token); err != nil {
				return sandbox.SandboxStatus{}, err
			}
		}
		if err := p.client.ContainerStart(ctx, name); err != nil {
			return sandbox.SandboxStatus{}, wrap("start container", err)
		}
		return p.Status(ctx, spec.ID)
	case err == nil:
		return p.containerStatus(existing), nil
	case !IsNotFound(err):
		return sandbox.SandboxStatus{}, wrap("inspect container", err)
	}
	if spec.Token == "" {
		return sandbox.SandboxStatus{}, fmt.Errorf("docker: sandbox %s has no token", spec.ID)
	}
	config, err := p.BuildContainer(spec)
	if err != nil {
		return sandbox.SandboxStatus{}, err
	}
	labels := sandbox.ObjectLabels(p.coding, sandbox.CompleteSpec(p.coding, spec))
	if spec.Workspace.VolumeID != "" {
		if _, err := p.client.VolumeInspect(ctx, WorkspaceVolume(spec.Workspace.VolumeID)); err != nil {
			return sandbox.SandboxStatus{}, wrap("workspace volume "+spec.Workspace.VolumeID, err)
		}
	} else if _, err := p.ensureVolume(ctx, WorkspaceVolume(spec.ID), labels); err != nil {
		return sandbox.SandboxStatus{}, err
	}
	for _, volume := range []string{HomeVolume(spec.ID), TokenVolume(spec.ID)} {
		if _, err := p.ensureVolume(ctx, volume, labels); err != nil {
			return sandbox.SandboxStatus{}, err
		}
	}
	if _, err := p.client.ContainerCreate(ctx, name, config); err != nil {
		switch {
		case IsConflict(err):
			// Created concurrently: fall through to start it.
		case IsNotFound(err):
			if err := p.pull(ctx, config.Image); err != nil {
				return sandbox.SandboxStatus{}, err
			}
			if _, err := p.client.ContainerCreate(ctx, name, config); err != nil && !IsConflict(err) {
				return sandbox.SandboxStatus{}, wrap("create container", err)
			}
		default:
			return sandbox.SandboxStatus{}, wrap("create container", err)
		}
	}
	if err := p.writeToken(ctx, name, spec.Token); err != nil {
		return sandbox.SandboxStatus{}, err
	}
	if err := p.client.ContainerStart(ctx, name); err != nil {
		return sandbox.SandboxStatus{}, wrap("start container", err)
	}
	return p.Status(ctx, spec.ID)
}

func (p *Provider) writeToken(ctx context.Context, container, token string) error {
	archive, err := tokenArchive(token, p.now())
	if err != nil {
		return err
	}
	if err := p.client.CopyToContainer(ctx, container, "/var/run/flow", archive); err != nil {
		return wrap("write token", err)
	}
	return nil
}

func (p *Provider) pull(ctx context.Context, image string) error {
	p.pullMu.Lock()
	defer p.pullMu.Unlock()
	if err := p.client.ImagePull(ctx, image); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("pull %s: %w: %v", image, sandbox.ErrTransient, err)
		}
		return fmt.Errorf("pull %s: %w: %v", image, sandbox.ErrImagePull, err)
	}
	return nil
}

func parseTime(value string) *time.Time {
	if value == "" || strings.HasPrefix(value, "0001-") {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	return &parsed
}

func (p *Provider) containerStatus(container ContainerJSON) sandbox.SandboxStatus {
	labels := maps.Clone(container.Config.Labels)
	status := sandbox.SandboxStatus{
		ID:          labels[sandbox.LabelSandboxID],
		Kind:        sandbox.Kind(labels[sandbox.LabelKind]),
		ProviderRef: strings.TrimPrefix(container.Name, "/"),
		Region:      labels[sandbox.LabelRegion],
		Labels:      labels,
		StartedAt:   parseTime(container.State.StartedAt),
	}
	if created := parseTime(container.Created); created != nil {
		status.CreatedAt = *created
	}
	switch container.State.Status {
	case "created":
		status.Phase = sandbox.PhaseStarting
		status.StartedAt = nil
	case "running", "paused", "restarting":
		status.Phase = sandbox.PhaseRunning
	case "exited", "dead":
		status.FinishedAt = parseTime(container.State.FinishedAt)
		code := container.State.ExitCode
		status.ExitCode = &code
		switch {
		case container.State.OOMKilled:
			status.Phase, status.Reason, status.Message = sandbox.PhaseFailed, sandbox.ReasonOOMKilled, "the runner ran out of memory; use a larger sandbox size"
		case code == 0:
			status.Phase = sandbox.PhaseSucceeded
		case code == sandbox.RestoreExitCode && labels[labelRestore] == "1":
			status.Phase, status.Reason, status.Message = sandbox.PhaseFailed, sandbox.ReasonInitFailed, "restoring the snapshot archive failed"
		default:
			status.Phase, status.Reason, status.Message = sandbox.PhaseFailed, sandbox.ReasonExitNonZero, container.State.Error
		}
	default: // removing
		status.Phase, status.Reason, status.Message = sandbox.PhaseFailed, sandbox.ReasonUnknown, "container is "+container.State.Status
	}
	return status
}

func (p *Provider) Status(ctx context.Context, sandboxID string) (sandbox.SandboxStatus, error) {
	container, err := p.client.ContainerInspect(ctx, ContainerName(sandboxID))
	if IsNotFound(err) {
		return sandbox.SandboxStatus{ID: sandboxID, Phase: sandbox.PhaseMissing}, nil
	}
	if err != nil {
		return sandbox.SandboxStatus{}, wrap("inspect container", err)
	}
	return p.containerStatus(container), nil
}

// StopSandbox stops (SIGTERM, SIGKILL after Grace) and removes the container,
// its home and token volumes and, unless KeepVolume, its own workspace volume.
func (p *Provider) StopSandbox(ctx context.Context, sandboxID string, options sandbox.StopOptions) error {
	name := ContainerName(sandboxID)
	grace := options.Grace
	if grace <= 0 {
		grace = p.coding.Timeouts.StopGrace
	}
	grace = sandbox.TerminationGrace(grace)
	volumeID := sandboxID
	container, err := p.client.ContainerInspect(ctx, name)
	switch {
	case IsNotFound(err):
	case err != nil:
		return wrap("inspect container", err)
	default:
		if id := container.Config.Labels[labelVolumeID]; id != "" {
			volumeID = id
		}
		if container.State.Running {
			if err := p.client.ContainerStop(ctx, name, grace); err != nil && !IsNotFound(err) && !isStatus(err, 304) {
				return wrap("stop container", err)
			}
		}
		if err := p.client.ContainerRemove(ctx, name); err != nil && !IsNotFound(err) && !IsConflict(err) {
			return wrap("remove container", err)
		}
	}
	volumes := []string{HomeVolume(sandboxID), TokenVolume(sandboxID)}
	if !options.KeepVolume && volumeID == sandboxID {
		volumes = append(volumes, WorkspaceVolume(sandboxID))
	}
	for _, volume := range volumes {
		if err := p.client.VolumeRemove(ctx, volume); err != nil && !IsNotFound(err) {
			return wrap("remove volume "+volume, err)
		}
	}
	return nil
}

func (p *Provider) EnsureVolume(ctx context.Context, spec sandbox.VolumeSpec) (sandbox.VolumeStatus, error) {
	if !sandbox.ValidID(spec.ID) {
		return sandbox.VolumeStatus{}, fmt.Errorf("docker: invalid volume id %q", spec.ID)
	}
	labels := sandbox.ManagedLabels(p.coding, spec.Labels)
	if ws := sandbox.WorkspaceLabel(spec.WorkspaceKey); ws != "" {
		labels[sandbox.LabelWorkspace] = ws
	}
	volume, err := p.ensureVolume(ctx, WorkspaceVolume(spec.ID), labels)
	if err != nil {
		return sandbox.VolumeStatus{}, err
	}
	return sandbox.VolumeStatus{ID: spec.ID, ProviderRef: volume.Name, Ready: true, Phase: "Bound", Labels: volume.Labels}, nil
}

func (p *Provider) RestoreWorkspace(context.Context, sandbox.RestoreSpec) (sandbox.VolumeStatus, error) {
	return sandbox.VolumeStatus{}, fmt.Errorf("docker: %w: VolumeSnapshots (use the archive strategy)", sandbox.ErrUnsupported)
}

func (p *Provider) VolumeStatus(ctx context.Context, volumeID string) (sandbox.VolumeStatus, error) {
	volume, err := p.client.VolumeInspect(ctx, WorkspaceVolume(volumeID))
	if err != nil {
		return sandbox.VolumeStatus{}, wrap("inspect volume", err)
	}
	return sandbox.VolumeStatus{ID: volumeID, ProviderRef: volume.Name, Ready: true, Phase: "Bound", Labels: volume.Labels}, nil
}

func (p *Provider) DeleteVolume(ctx context.Context, volumeID string) error {
	if err := p.client.VolumeRemove(ctx, WorkspaceVolume(volumeID)); err != nil && !IsNotFound(err) {
		return wrap("remove volume", err)
	}
	return nil
}

func (p *Provider) CreateSnapshot(context.Context, sandbox.SnapshotSpec) (sandbox.SnapshotStatus, error) {
	return sandbox.SnapshotStatus{}, fmt.Errorf("docker: %w: VolumeSnapshots (use the archive strategy)", sandbox.ErrUnsupported)
}

func (p *Provider) SnapshotStatus(context.Context, string) (sandbox.SnapshotStatus, error) {
	return sandbox.SnapshotStatus{}, fmt.Errorf("docker: %w: VolumeSnapshots", sandbox.ErrUnsupported)
}

func (p *Provider) DeleteSnapshot(context.Context, string) error { return nil }

// Logs returns the runner's combined stdout and stderr.
func (p *Provider) Logs(ctx context.Context, sandboxID string, options sandbox.LogOptions) (io.ReadCloser, error) {
	stream, err := p.client.ContainerLogs(ctx, ContainerName(sandboxID), LogsOptions{Follow: options.Follow, Tail: options.TailLines, Since: options.SinceTime})
	if err != nil {
		return nil, wrap("container logs", err)
	}
	return demuxed(stream), nil
}

// Exec runs a command as the runner user, with a TTY for the debug terminal.
func (p *Provider) Exec(ctx context.Context, sandboxID string, request sandbox.ExecRequest) (sandbox.ExecResult, error) {
	if len(request.Command) == 0 {
		return sandbox.ExecResult{}, errors.New("docker: exec needs a command")
	}
	name := ContainerName(sandboxID)
	container, err := p.client.ContainerInspect(ctx, name)
	if IsNotFound(err) {
		return sandbox.ExecResult{}, sandbox.ErrNotFound
	}
	if err != nil {
		return sandbox.ExecResult{}, wrap("inspect container", err)
	}
	if !container.State.Running {
		return sandbox.ExecResult{}, fmt.Errorf("docker: sandbox %s is not running (%s)", sandboxID, container.State.Status)
	}
	execID, err := p.client.ExecCreate(ctx, name, ExecConfig{
		Cmd: request.Command, User: strconv.Itoa(sandbox.RunnerUID) + ":" + strconv.Itoa(sandbox.RunnerUID), WorkingDir: sandbox.WorkspacePath,
		Tty: request.TTY, AttachStdin: request.Stdin != nil, AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return sandbox.ExecResult{}, wrap("create exec", err)
	}
	conn, err := p.client.ExecAttach(ctx, execID, request.TTY)
	if err != nil {
		return sandbox.ExecResult{}, wrap("start exec", err)
	}
	defer conn.Close()
	resizeDone := make(chan struct{})
	defer close(resizeDone)
	if request.TTY && request.Resize != nil {
		go func() {
			for {
				select {
				case <-resizeDone:
					return
				case <-ctx.Done():
					return
				case size, ok := <-request.Resize:
					if !ok {
						return
					}
					_ = p.client.ExecResize(ctx, execID, size.Height, size.Width)
				}
			}
		}()
	}
	if request.Stdin != nil {
		go func() {
			_, _ = io.Copy(conn, request.Stdin)
			if closer, ok := conn.(interface{ CloseWrite() error }); ok {
				_ = closer.CloseWrite()
			}
		}()
	}
	stdout := request.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	if request.TTY {
		_, err = io.Copy(stdout, conn)
	} else {
		err = demux(conn, stdout, request.Stderr)
	}
	if err != nil && !errors.Is(err, net.ErrClosed) && ctx.Err() == nil {
		return sandbox.ExecResult{}, fmt.Errorf("docker: exec stream: %w", err)
	}
	if ctx.Err() != nil {
		return sandbox.ExecResult{}, ctx.Err()
	}
	inspect, err := p.client.ExecInspect(ctx, execID)
	if err != nil {
		return sandbox.ExecResult{}, wrap("inspect exec", err)
	}
	return sandbox.ExecResult{ExitCode: inspect.ExitCode}, nil
}

func (p *Provider) filterLabels(filter sandbox.ListFilter) []string {
	labels := []string{sandbox.LabelManagedBy + "=" + sandbox.ManagedByValue, sandbox.LabelInstance + "=" + p.coding.InstanceID}
	if filter.Kind != "" {
		labels = append(labels, sandbox.LabelKind+"="+string(filter.Kind))
	}
	if filter.SandboxID != "" {
		labels = append(labels, sandbox.LabelSandboxID+"="+filter.SandboxID)
	}
	if filter.SessionID != "" {
		labels = append(labels, sandbox.LabelSessionID+"="+filter.SessionID)
	}
	return labels
}

// ListManaged lists this instance's containers and volumes.
func (p *Provider) ListManaged(ctx context.Context, filter sandbox.ListFilter) ([]sandbox.ManagedObject, error) {
	labels := p.filterLabels(filter)
	containers, err := p.client.ContainerList(ctx, labels)
	if err != nil {
		return nil, wrap("list containers", err)
	}
	result := []sandbox.ManagedObject{}
	for _, item := range containers {
		name := item.ID
		if len(item.Names) > 0 {
			name = strings.TrimPrefix(item.Names[0], "/")
		}
		result = append(result, sandbox.ManagedObject{Type: TypeContainer, Name: name, Labels: maps.Clone(item.Labels), CreatedAt: time.Unix(item.Created, 0)})
	}
	volumes, err := p.client.VolumeList(ctx, labels)
	if err != nil {
		return nil, wrap("list volumes", err)
	}
	for _, item := range volumes {
		created := time.Time{}
		if parsed := parseTime(item.CreatedAt); parsed != nil {
			created = *parsed
		}
		result = append(result, sandbox.ManagedObject{Type: TypeVolume, Name: item.Name, Labels: maps.Clone(item.Labels), CreatedAt: created})
	}
	return result, nil
}

// DeleteManaged removes one listed object of this instance.
func (p *Provider) DeleteManaged(ctx context.Context, object sandbox.ManagedObject) error {
	if object.Labels[sandbox.LabelManagedBy] != sandbox.ManagedByValue || object.Labels[sandbox.LabelInstance] != p.coding.InstanceID {
		return nil
	}
	var err error
	switch object.Type {
	case TypeContainer:
		err = p.client.ContainerRemove(ctx, object.Name)
	case TypeVolume:
		err = p.client.VolumeRemove(ctx, object.Name)
	default:
		return nil
	}
	if err != nil && !IsNotFound(err) {
		return wrap("delete "+object.Type+" "+object.Name, err)
	}
	return nil
}

// Preflight checks the daemon, the configured runtime and the network.
func (p *Provider) Preflight(ctx context.Context) []sandbox.PreflightResult {
	results := []sandbox.PreflightResult{}
	if err := p.client.Ping(ctx); err != nil {
		return append(results, sandbox.PreflightResult{Name: "docker", OK: false, Message: "cannot reach the Docker daemon at " + p.coding.Docker.Host + ": " + err.Error()})
	}
	results = append(results, sandbox.PreflightResult{Name: "docker", OK: true, Message: "Docker daemon is reachable"})
	if info, err := p.client.Info(ctx); err != nil {
		results = append(results, sandbox.PreflightResult{Name: "runtime", OK: true, Warning: true, Message: "cannot read daemon info: " + err.Error()})
	} else if runtime := p.coding.Docker.Runtime; runtime != "" {
		if _, ok := info.Runtimes[runtime]; ok {
			results = append(results, sandbox.PreflightResult{Name: "runtime", OK: true, Message: "runtime " + runtime + " is available"})
		} else {
			results = append(results, sandbox.PreflightResult{Name: "runtime", OK: false, Message: "runtime " + runtime + " is not configured in the Docker daemon"})
		}
	} else {
		results = append(results, sandbox.PreflightResult{Name: "runtime", OK: true, Warning: true, Message: "sandboxes run on " + info.DefaultRuntime + "; set FLOW_CODING_DOCKER_RUNTIME=runsc for gVisor"})
	}
	network, err := p.client.NetworkInspect(ctx, p.coding.Docker.Network)
	switch {
	case IsNotFound(err):
		results = append(results, sandbox.PreflightResult{Name: "network", OK: false, Message: "network " + p.coding.Docker.Network + " does not exist; create it with `docker network create --internal " + p.coding.Docker.Network + "`"})
	case err != nil:
		results = append(results, sandbox.PreflightResult{Name: "network", OK: true, Warning: true, Message: "cannot inspect network " + p.coding.Docker.Network + ": " + err.Error()})
	case !network.Internal:
		results = append(results, sandbox.PreflightResult{Name: "network", OK: true, Warning: true, Message: "network " + p.coding.Docker.Network + " is not internal: sandboxes can reach the internet without the egress proxy"})
	default:
		results = append(results, sandbox.PreflightResult{Name: "network", OK: true, Message: "network " + p.coding.Docker.Network + " is internal"})
	}
	return results
}
