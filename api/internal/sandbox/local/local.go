// Package local is the development-only "local" sandbox provider: each
// sandbox is a process on Flow's own host, started through an operator-given
// launcher (FLOW_CODING_LOCAL_COMMAND) that is responsible for isolating it
// (for example with bubblewrap). It exists to run the real sandbox-runner and
// harness end to end without Docker or Kubernetes. It is off by default, has
// no resource limits, network policy, exec or snapshots of its own, and must
// never be used in production.
//
// For every sandbox the provider creates <root>/sandboxes/<id>/ with:
//
//	token      the sandbox token (mode 0600), what the runner reads at /var/run/flow/token
//	home/      the sandbox home (/home/flow)
//	runner.log the launcher's stdout and stderr
//
// and a workspace directory (<root>/volumes/<volume id>/, or
// <root>/sandboxes/<id>/workspace without a volume). It then runs
//
//	<FLOW_CODING_LOCAL_COMMAND> <mode>
//
// (preceded by `<FLOW_CODING_LOCAL_COMMAND> restore` when the sandbox
// restores a snapshot archive, FLOW_ARCHIVE_URL)
// with the runner environment of a container sandbox (sandbox.RunnerEnv: the
// in-sandbox paths /var/run/flow/token, /home/flow and /workspace) plus
// FLOW_LOCAL_TOKEN_FILE, FLOW_LOCAL_HOME_DIR and FLOW_LOCAL_WORKSPACE_DIR,
// the host paths the launcher must map to those in-sandbox paths. The
// launcher's environment is only that: nothing of Flow's own environment
// (provider keys, database URLs) is inherited.
package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"flow/api/internal/config"
	"flow/api/internal/sandbox"
)

// Environment the launcher receives in addition to sandbox.RunnerEnv.
const (
	EnvTokenFile    = "FLOW_LOCAL_TOKEN_FILE"
	EnvHomeDir      = "FLOW_LOCAL_HOME_DIR"
	EnvWorkspaceDir = "FLOW_LOCAL_WORKSPACE_DIR"
)

func init() {
	sandbox.Register(config.CodingProviderLocal, func(deps sandbox.Dependencies) (sandbox.Provider, error) {
		return New(deps.Config, deps.Now)
	})
}

// Provider runs sandboxes as local processes. Volumes are directories;
// volume snapshots are not supported (use the archive strategy).
type Provider struct {
	config  config.CodingConfig
	command string
	root    string
	now     func() time.Time

	mu        sync.Mutex
	sandboxes map[string]*process
}

type process struct {
	spec   sandbox.SandboxSpec
	status sandbox.SandboxStatus
	cmd    *exec.Cmd
	done   chan struct{}
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// New builds the provider from FLOW_CODING_LOCAL_*.
func New(c config.CodingConfig, now func() time.Time) (*Provider, error) {
	if now == nil {
		now = time.Now
	}
	command := strings.TrimSpace(c.Local.Command)
	if command == "" {
		return nil, fmt.Errorf("local sandbox provider: FLOW_CODING_LOCAL_COMMAND is required")
	}
	if !filepath.IsAbs(command) {
		return nil, fmt.Errorf("local sandbox provider: FLOW_CODING_LOCAL_COMMAND must be an absolute path")
	}
	root := strings.TrimSpace(c.Local.Root)
	if root == "" {
		root = filepath.Join(os.TempDir(), "flow-local-sandboxes")
	}
	if c.Snapshot.Strategy == sandbox.StrategyVolumeSnapshot {
		return nil, fmt.Errorf("local sandbox provider: FLOW_CODING_SNAPSHOT_STRATEGY=volumesnapshot is not supported (use archive)")
	}
	p := &Provider{config: c, command: command, root: root, now: now, sandboxes: map[string]*process{}}
	// Sandboxes do not survive a Flow restart (the launcher should die with
	// Flow): drop the directories, and with them the tokens, of earlier
	// processes. The sandbox reconciler sees those sandboxes as missing.
	if entries, err := os.ReadDir(filepath.Join(root, "sandboxes")); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && checkID(entry.Name()) == nil {
				_ = os.RemoveAll(p.sandboxDir(entry.Name()))
			}
		}
	}
	return p, nil
}

func (p *Provider) Name() string { return config.CodingProviderLocal }

func (p *Provider) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{PersistentVolumes: true}
}

func (p *Provider) Preflight(context.Context) []sandbox.PreflightResult {
	results := []sandbox.PreflightResult{{Name: "provider", OK: true, Warning: true, Message: "local provider: sandboxes are host processes isolated only by " + p.command + "; development only"}}
	if info, err := os.Stat(p.command); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		results = append(results, sandbox.PreflightResult{Name: "launcher", OK: false, Message: "FLOW_CODING_LOCAL_COMMAND is not an executable file"})
	}
	return results
}

func (p *Provider) volumeDir(id string) string  { return filepath.Join(p.root, "volumes", id) }
func (p *Provider) sandboxDir(id string) string { return filepath.Join(p.root, "sandboxes", id) }

func checkID(id string) error {
	if !safeID.MatchString(id) {
		return fmt.Errorf("local sandbox provider: invalid id %q", id)
	}
	return nil
}

func (p *Provider) EnsureVolume(_ context.Context, spec sandbox.VolumeSpec) (sandbox.VolumeStatus, error) {
	if err := checkID(spec.ID); err != nil {
		return sandbox.VolumeStatus{}, err
	}
	if err := os.MkdirAll(p.volumeDir(spec.ID), 0o700); err != nil {
		return sandbox.VolumeStatus{}, err
	}
	return sandbox.VolumeStatus{ID: spec.ID, ProviderRef: p.volumeDir(spec.ID), Ready: true, Phase: "Bound", Labels: maps.Clone(spec.Labels)}, nil
}

func (p *Provider) RestoreWorkspace(context.Context, sandbox.RestoreSpec) (sandbox.VolumeStatus, error) {
	return sandbox.VolumeStatus{}, sandbox.ErrUnsupported
}

func (p *Provider) VolumeStatus(_ context.Context, id string) (sandbox.VolumeStatus, error) {
	if checkID(id) != nil {
		return sandbox.VolumeStatus{}, sandbox.ErrNotFound
	}
	if info, err := os.Stat(p.volumeDir(id)); err != nil || !info.IsDir() {
		return sandbox.VolumeStatus{}, sandbox.ErrNotFound
	}
	return sandbox.VolumeStatus{ID: id, ProviderRef: p.volumeDir(id), Ready: true, Phase: "Bound"}, nil
}

func (p *Provider) DeleteVolume(_ context.Context, id string) error {
	if err := checkID(id); err != nil {
		return err
	}
	return os.RemoveAll(p.volumeDir(id))
}

func (p *Provider) CreateSnapshot(context.Context, sandbox.SnapshotSpec) (sandbox.SnapshotStatus, error) {
	return sandbox.SnapshotStatus{}, sandbox.ErrUnsupported
}

func (p *Provider) SnapshotStatus(context.Context, string) (sandbox.SnapshotStatus, error) {
	return sandbox.SnapshotStatus{}, sandbox.ErrNotFound
}

func (p *Provider) DeleteSnapshot(context.Context, string) error { return nil }

// StartSandbox writes the token, prepares the directories and starts the
// launcher. It is idempotent: a known sandbox returns its current status.
func (p *Provider) StartSandbox(_ context.Context, spec sandbox.SandboxSpec) (sandbox.SandboxStatus, error) {
	if err := checkID(spec.ID); err != nil {
		return sandbox.SandboxStatus{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.sandboxes[spec.ID]; ok {
		return existing.status, nil
	}
	if spec.Token == "" {
		return sandbox.SandboxStatus{}, fmt.Errorf("local: sandbox %s has no token", spec.ID)
	}
	dir := p.sandboxDir(spec.ID)
	workspace := filepath.Join(dir, "workspace")
	if spec.Workspace.VolumeID != "" {
		if err := checkID(spec.Workspace.VolumeID); err != nil {
			return sandbox.SandboxStatus{}, err
		}
		workspace = p.volumeDir(spec.Workspace.VolumeID)
		if info, err := os.Stat(workspace); err != nil || !info.IsDir() {
			return sandbox.SandboxStatus{}, fmt.Errorf("%w: volume %s", sandbox.ErrNotFound, spec.Workspace.VolumeID)
		}
	}
	home := filepath.Join(dir, "home")
	for _, path := range []string{dir, home, workspace} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return sandbox.SandboxStatus{}, err
		}
	}
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte(spec.Token), 0o600); err != nil {
		return sandbox.SandboxStatus{}, err
	}
	logFile, err := os.OpenFile(filepath.Join(dir, "runner.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return sandbox.SandboxStatus{}, err
	}
	defer logFile.Close()
	mode := sandbox.Mode(spec.Kind)
	env := []string{}
	for _, item := range sandbox.RunnerEnv(p.config, spec, mode) {
		env = append(env, item.Name+"="+item.Value)
	}
	env = append(env, "PATH=/usr/local/bin:/usr/bin:/bin", EnvTokenFile+"="+tokenFile, EnvHomeDir+"="+home, EnvWorkspaceDir+"="+workspace)
	cmd := exec.Command(p.command, mode)
	if spec.Workspace.ArchiveURL != "" {
		// No init containers: restore the archive first, as the docker
		// provider does (exit RestoreExitCode when the restore fails).
		cmd = exec.Command("/bin/sh", "-c", fmt.Sprintf(`"$0" restore || exit %d; exec "$0" "$1"`, sandbox.RestoreExitCode), p.command, mode)
	}
	cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr = env, dir, logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	now := p.now()
	labels := maps.Clone(spec.Labels)
	if labels == nil {
		labels = map[string]string{}
	}
	labels[sandbox.LabelManagedBy], labels[sandbox.LabelKind], labels[sandbox.LabelSandboxID] = sandbox.ManagedByValue, string(spec.Kind), spec.ID
	if spec.SessionID != "" {
		labels[sandbox.LabelSessionID] = spec.SessionID
	}
	status := sandbox.SandboxStatus{ID: spec.ID, Kind: spec.Kind, Phase: sandbox.PhaseRunning, ProviderRef: sandbox.ObjectName("cs", spec.ID), CreatedAt: now, StartedAt: &now, Labels: labels}
	if err := cmd.Start(); err != nil {
		return sandbox.SandboxStatus{}, fmt.Errorf("local: start %s: %w", p.command, err)
	}
	spec.Token = "" // never retained in memory
	box := &process{spec: spec, status: status, cmd: cmd, done: make(chan struct{})}
	p.sandboxes[spec.ID] = box
	go p.wait(box)
	return status, nil
}

func (p *Provider) wait(box *process) {
	err := box.cmd.Wait()
	code := box.cmd.ProcessState.ExitCode()
	p.mu.Lock()
	finished := p.now()
	box.status.FinishedAt, box.status.ExitCode = &finished, &code
	if err == nil && code == 0 {
		box.status.Phase = sandbox.PhaseSucceeded
	} else {
		box.status.Phase, box.status.Reason = sandbox.PhaseFailed, sandbox.ReasonExitNonZero
		if code == sandbox.RestoreExitCode && box.spec.Workspace.ArchiveURL != "" {
			box.status.Reason = sandbox.ReasonInitFailed
		}
		if err != nil {
			box.status.Message = err.Error()
		}
	}
	p.mu.Unlock()
	close(box.done)
}

func (p *Provider) Status(_ context.Context, id string) (sandbox.SandboxStatus, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	box, ok := p.sandboxes[id]
	if !ok {
		return sandbox.SandboxStatus{ID: id, Phase: sandbox.PhaseMissing}, nil
	}
	status := box.status
	status.Labels = maps.Clone(box.status.Labels)
	return status, nil
}

// StopSandbox sends SIGTERM to the launcher's process group, SIGKILL after
// the grace period, and removes the sandbox directory (token, home, log).
func (p *Provider) StopSandbox(_ context.Context, id string, options sandbox.StopOptions) error {
	p.mu.Lock()
	box, ok := p.sandboxes[id]
	delete(p.sandboxes, id)
	p.mu.Unlock()
	if ok {
		stopProcess(box, options.Grace)
	}
	if checkID(id) != nil {
		return nil
	}
	return os.RemoveAll(p.sandboxDir(id))
}

func stopProcess(box *process, grace time.Duration) {
	select {
	case <-box.done:
		return
	default:
	}
	pgid := box.cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	if grace <= 0 {
		grace = 10 * time.Second
	}
	select {
	case <-box.done:
	case <-time.After(grace):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-box.done
	}
}

func (p *Provider) Exec(context.Context, string, sandbox.ExecRequest) (sandbox.ExecResult, error) {
	return sandbox.ExecResult{}, sandbox.ErrUnsupported
}

// Logs returns runner.log (Follow is not supported; it returns what exists).
func (p *Provider) Logs(_ context.Context, id string, _ sandbox.LogOptions) (io.ReadCloser, error) {
	if checkID(id) != nil {
		return nil, sandbox.ErrNotFound
	}
	file, err := os.Open(filepath.Join(p.sandboxDir(id), "runner.log"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, sandbox.ErrNotFound
	}
	return file, err
}

func (p *Provider) ListManaged(_ context.Context, filter sandbox.ListFilter) ([]sandbox.ManagedObject, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := []sandbox.ManagedObject{}
	for _, box := range p.sandboxes {
		if filter.Kind != "" && box.status.Kind != filter.Kind || filter.SandboxID != "" && box.status.ID != filter.SandboxID || filter.SessionID != "" && box.spec.SessionID != filter.SessionID {
			continue
		}
		result = append(result, sandbox.ManagedObject{Type: "process", Name: box.status.ProviderRef, Labels: maps.Clone(box.status.Labels), CreatedAt: box.status.CreatedAt})
	}
	return result, nil
}

func (p *Provider) DeleteManaged(ctx context.Context, object sandbox.ManagedObject) error {
	if id := object.Labels[sandbox.LabelSandboxID]; id != "" {
		return p.StopSandbox(ctx, id, sandbox.StopOptions{})
	}
	return nil
}
