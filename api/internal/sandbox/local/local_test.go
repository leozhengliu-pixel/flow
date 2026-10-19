package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/config"
	"flow/api/internal/sandbox"
)

// launcher writes a script that records its arguments and environment in
// the sandbox directory (its working directory), then runs body.
func launcher(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "launcher.sh")
	script := "#!/bin/sh\necho \"$1\" >> args\nenv > env.$1\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func newProvider(t *testing.T, command string) *Provider {
	t.Helper()
	provider, err := New(config.CodingConfig{Provider: config.CodingProviderLocal, RuntimeURL: "http://127.0.0.1:8097", Local: config.LocalSandboxConfig{Command: command, Root: t.TempDir()}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func waitPhase(t *testing.T, p *Provider, id string, phase sandbox.Phase) sandbox.SandboxStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		status, err := p.Status(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if status.Phase == phase {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("sandbox %s: phase %s, want %s", id, status.Phase, phase)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestNewValidatesCommand(t *testing.T) {
	for _, command := range []string{"", "relative/launcher.sh"} {
		if _, err := New(config.CodingConfig{Local: config.LocalSandboxConfig{Command: command}}, nil); err == nil {
			t.Fatalf("command %q must be rejected", command)
		}
	}
	if _, err := New(config.CodingConfig{Local: config.LocalSandboxConfig{Command: "/bin/true"}, Snapshot: config.CodingSnapshotConfig{Strategy: sandbox.StrategyVolumeSnapshot}}, nil); err == nil {
		t.Fatal("volume snapshots are not supported")
	}
	if err := (config.CodingConfig{Enabled: true, Provider: config.CodingProviderLocal, RuntimeURL: "http://127.0.0.1:8097", SecretKey: "k", Snapshot: config.CodingSnapshotConfig{Strategy: "archive"}, Kubernetes: config.KubernetesSandboxConfig{WorkspaceVolume: "auto"}}).Validate(); err == nil || !strings.Contains(err.Error(), "FLOW_CODING_LOCAL_COMMAND") {
		t.Fatalf("validate: %v", err)
	}
}

func TestStartRunsLauncherWithRunnerEnvironment(t *testing.T) {
	t.Setenv("FLOW_TEST_PARENT_SECRET", "must-not-leak")
	p := newProvider(t, launcher(t, "exit 0"))
	ctx := context.Background()
	if _, err := p.EnsureVolume(ctx, sandbox.VolumeSpec{ID: "vol1"}); err != nil {
		t.Fatal(err)
	}
	spec := sandbox.SandboxSpec{ID: "sb1", Kind: sandbox.KindSession, SessionID: "s1", Token: "fcs_secret", Workspace: sandbox.VolumeSource{VolumeID: "vol1"}, Resources: sandbox.Resources{Size: "small"}}
	status, err := p.StartSandbox(ctx, spec)
	if err != nil || status.Phase != sandbox.PhaseRunning || status.Labels[sandbox.LabelSessionID] != "s1" {
		t.Fatalf("start: %+v %v", status, err)
	}
	again, err := p.StartSandbox(ctx, spec)
	if err != nil || again.CreatedAt != status.CreatedAt {
		t.Fatalf("start must be idempotent: %+v %v", again, err)
	}
	final := waitPhase(t, p, "sb1", sandbox.PhaseSucceeded)
	if final.ExitCode == nil || *final.ExitCode != 0 || final.FinishedAt == nil {
		t.Fatalf("final: %+v", final)
	}
	dir := p.sandboxDir("sb1")
	token, err := os.ReadFile(filepath.Join(dir, "token"))
	if err != nil || string(token) != "fcs_secret" {
		t.Fatalf("token file: %q %v", token, err)
	}
	if info, _ := os.Stat(filepath.Join(dir, "token")); info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode %v", info.Mode())
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	if strings.TrimSpace(string(args)) != "session" {
		t.Fatalf("launcher args %q", args)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "env.session"))
	env := string(raw)
	for _, want := range []string{
		"FLOW_RUNTIME_URL=http://127.0.0.1:8097", "FLOW_SANDBOX_ID=sb1", "FLOW_RUNNER_MODE=session", "FLOW_TOKEN_FILE=/var/run/flow/token",
		EnvTokenFile + "=" + filepath.Join(dir, "token"), EnvWorkspaceDir + "=" + p.volumeDir("vol1"), EnvHomeDir + "=" + filepath.Join(dir, "home"),
	} {
		if !strings.Contains(env, want+"\n") {
			t.Errorf("launcher env lacks %s", want)
		}
	}
	if strings.Contains(env, "FLOW_TEST_PARENT_SECRET") || strings.Contains(env, "fcs_secret") {
		t.Fatal("the launcher must not inherit Flow's environment or get the token as a variable")
	}
	if logs, err := p.Logs(ctx, "sb1", sandbox.LogOptions{}); err != nil {
		t.Fatal(err)
	} else {
		logs.Close()
	}
}

func TestFailedLauncherAndMissingVolume(t *testing.T) {
	p := newProvider(t, launcher(t, "echo boom >&2; exit 3"))
	ctx := context.Background()
	if _, err := p.StartSandbox(ctx, sandbox.SandboxSpec{ID: "sb1", Token: "fcs_x", Workspace: sandbox.VolumeSource{VolumeID: "nope"}}); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("missing volume: %v", err)
	}
	if _, err := p.StartSandbox(ctx, sandbox.SandboxSpec{ID: "../escape", Token: "fcs_x"}); err == nil {
		t.Fatal("unsafe ids must be rejected")
	}
	if _, err := p.StartSandbox(ctx, sandbox.SandboxSpec{ID: "sb2"}); err == nil {
		t.Fatal("a sandbox needs a token")
	}
	if _, err := p.StartSandbox(ctx, sandbox.SandboxSpec{ID: "sb3", Token: "fcs_x"}); err != nil {
		t.Fatal(err)
	}
	status := waitPhase(t, p, "sb3", sandbox.PhaseFailed)
	if status.Reason != sandbox.ReasonExitNonZero || status.ExitCode == nil || *status.ExitCode != 3 {
		t.Fatalf("failed: %+v", status)
	}
}

func TestStopKillsAndCleansUp(t *testing.T) {
	p := newProvider(t, launcher(t, "trap '' TERM; sleep 30 & wait"))
	ctx := context.Background()
	if _, err := p.StartSandbox(ctx, sandbox.SandboxSpec{ID: "sb1", Kind: sandbox.KindSession, Token: "fcs_x"}); err != nil {
		t.Fatal(err)
	}
	if objects, _ := p.ListManaged(ctx, sandbox.ListFilter{SandboxID: "sb1"}); len(objects) != 1 {
		t.Fatalf("managed: %+v", objects)
	}
	started := time.Now()
	if err := p.StopSandbox(ctx, "sb1", sandbox.StopOptions{Grace: 200 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatal("stop must SIGKILL after the grace period")
	}
	if status, _ := p.Status(ctx, "sb1"); status.Phase != sandbox.PhaseMissing {
		t.Fatalf("after stop: %+v", status)
	}
	if _, err := os.Stat(p.sandboxDir("sb1")); !os.IsNotExist(err) {
		t.Fatalf("the sandbox directory (token) must be removed: %v", err)
	}
	if err := p.StopSandbox(ctx, "sb1", sandbox.StopOptions{}); err != nil {
		t.Fatalf("stop must be idempotent: %v", err)
	}
}

func TestArchiveRestoreRunsFirst(t *testing.T) {
	p := newProvider(t, launcher(t, `[ "$1" = restore ] && [ -n "$FLOW_ARCHIVE_URL" ] && [ -f fail-restore ] && exit 1; exit 0`))
	ctx := context.Background()
	if _, err := p.StartSandbox(ctx, sandbox.SandboxSpec{ID: "sb1", Token: "fcs_x", Workspace: sandbox.VolumeSource{ArchiveURL: "http://flow/archive"}}); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, p, "sb1", sandbox.PhaseSucceeded)
	if args, _ := os.ReadFile(filepath.Join(p.sandboxDir("sb1"), "args")); string(args) != "restore\nsession\n" {
		t.Fatalf("launcher calls %q", args)
	}
	// A failed restore stops before the runner and reports InitFailed.
	if err := os.MkdirAll(p.sandboxDir("sb2"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.sandboxDir("sb2"), "fail-restore"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.StartSandbox(ctx, sandbox.SandboxSpec{ID: "sb2", Token: "fcs_x", Workspace: sandbox.VolumeSource{ArchiveURL: "http://flow/archive"}}); err != nil {
		t.Fatal(err)
	}
	status := waitPhase(t, p, "sb2", sandbox.PhaseFailed)
	if status.Reason != sandbox.ReasonInitFailed {
		t.Fatalf("restore failure: %+v", status)
	}
	if args, _ := os.ReadFile(filepath.Join(p.sandboxDir("sb2"), "args")); string(args) != "restore\n" {
		t.Fatalf("the runner must not start after a failed restore: %q", args)
	}
}

func TestVolumes(t *testing.T) {
	p := newProvider(t, "/bin/true")
	ctx := context.Background()
	if _, err := p.VolumeStatus(ctx, "vol1"); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("missing volume: %v", err)
	}
	if _, err := p.EnsureVolume(ctx, sandbox.VolumeSpec{ID: "vol1"}); err != nil {
		t.Fatal(err)
	}
	if status, err := p.VolumeStatus(ctx, "vol1"); err != nil || !status.Ready {
		t.Fatalf("volume: %+v %v", status, err)
	}
	if err := p.DeleteVolume(ctx, "vol1"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.VolumeStatus(ctx, "vol1"); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("deleted volume: %v", err)
	}
	if _, err := p.CreateSnapshot(ctx, sandbox.SnapshotSpec{ID: "s", VolumeID: "vol1"}); !errors.Is(err, sandbox.ErrUnsupported) {
		t.Fatalf("snapshots: %v", err)
	}
	if _, err := p.Exec(ctx, "sb", sandbox.ExecRequest{}); !errors.Is(err, sandbox.ErrUnsupported) {
		t.Fatalf("exec: %v", err)
	}
}

func TestNewRemovesStaleSandboxDirectories(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, "sandboxes", "old1")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "token"), []byte("fcs_old"), 0o600); err != nil {
		t.Fatal(err)
	}
	volume := filepath.Join(root, "volumes", "vol1")
	if err := os.MkdirAll(volume, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := New(config.CodingConfig{Local: config.LocalSandboxConfig{Command: "/bin/true", Root: root}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("a stale sandbox directory (and its token) must be removed: %v", err)
	}
	if _, err := os.Stat(volume); err != nil {
		t.Fatalf("workspace volumes are kept: %v", err)
	}
}
