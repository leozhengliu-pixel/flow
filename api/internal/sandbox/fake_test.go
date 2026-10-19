package sandbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"flow/api/internal/config"
)

// Compile-time contract checks.
var (
	_ Provider = (*Fake)(nil)
	_ Provider = Disabled{}
)

func TestOpenSelectsProvider(t *testing.T) {
	provider, err := Open(Dependencies{Config: config.CodingConfig{Provider: config.CodingProviderDisabled}})
	if err != nil || provider.Name() != config.CodingProviderDisabled {
		t.Fatalf("disabled: %v %v", provider, err)
	}
	provider, err = Open(Dependencies{Config: config.CodingConfig{Provider: config.CodingProviderFake}})
	if err != nil || provider.Name() != config.CodingProviderFake {
		t.Fatalf("fake: %v %v", provider, err)
	}
	if _, err := Open(Dependencies{Config: config.CodingConfig{Provider: "nomad"}}); err == nil {
		t.Fatal("unknown provider must fail")
	}
}

func TestFakeLifecycleIsIdempotent(t *testing.T) {
	ctx := context.Background()
	fake := NewFake(nil)
	if _, err := fake.EnsureVolume(ctx, VolumeSpec{ID: "v1"}); err != nil {
		t.Fatal(err)
	}
	spec := SandboxSpec{ID: "s1", Kind: KindSession, SessionID: "cs1", Token: "fcs_secret", Workspace: VolumeSource{VolumeID: "v1"}}
	first, err := fake.StartSandbox(ctx, spec)
	if err != nil || first.Phase != PhaseRunning {
		t.Fatalf("start: %+v %v", first, err)
	}
	again, err := fake.StartSandbox(ctx, spec)
	if err != nil || again.CreatedAt != first.CreatedAt {
		t.Fatalf("restart must be a no-op: %+v %v", again, err)
	}
	if stored, _ := fake.Spec("s1"); stored.Token != "" {
		t.Fatal("the fake must not retain sandbox tokens")
	}
	var out bytes.Buffer
	if result, err := fake.Exec(ctx, "s1", ExecRequest{Command: []string{"bash"}, Stdout: &out}); err != nil || result.ExitCode != 0 || out.Len() == 0 {
		t.Fatalf("exec: %v %v", result, err)
	}
	fake.AppendLog("s1", "hello")
	logs, err := fake.Logs(ctx, "s1", LogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := io.ReadAll(logs); string(raw) != "hello" {
		t.Fatalf("logs: %q", raw)
	}
	objects, _ := fake.ListManaged(ctx, ListFilter{SessionID: "cs1"})
	if len(objects) != 1 || objects[0].Labels[LabelManagedBy] != ManagedByValue {
		t.Fatalf("managed: %+v", objects)
	}
	if err := fake.StopSandbox(ctx, "s1", StopOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := fake.StopSandbox(ctx, "s1", StopOptions{}); err != nil {
		t.Fatal("stopping twice must succeed")
	}
	if status, _ := fake.Status(ctx, "s1"); status.Phase != PhaseMissing {
		t.Fatalf("stopped sandbox must be missing: %+v", status)
	}
}

func TestFakeSnapshotsAndFailures(t *testing.T) {
	ctx := context.Background()
	fake := NewFake(nil)
	if _, err := fake.CreateSnapshot(ctx, SnapshotSpec{ID: "snap", VolumeID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("snapshot of a missing volume: %v", err)
	}
	_, _ = fake.EnsureVolume(ctx, VolumeSpec{ID: "v"})
	if _, err := fake.CreateSnapshot(ctx, SnapshotSpec{ID: "snap", VolumeID: "v"}); err != nil {
		t.Fatal(err)
	}
	if volume, err := fake.RestoreWorkspace(ctx, RestoreSpec{VolumeID: "v2", SnapshotID: "snap"}); err != nil || !volume.Ready {
		t.Fatalf("restore: %+v %v", volume, err)
	}
	fake.FailNext("StartSandbox", ErrTransient)
	if _, err := fake.StartSandbox(ctx, SandboxSpec{ID: "x"}); !errors.Is(err, ErrTransient) {
		t.Fatalf("injected failure: %v", err)
	}
	if _, err := fake.StartSandbox(ctx, SandboxSpec{ID: "x"}); err != nil {
		t.Fatalf("failure must apply once: %v", err)
	}
	if err := fake.SetPhase("x", PhaseFailed, ReasonOOMKilled); err != nil {
		t.Fatal(err)
	}
	if status, _ := fake.Status(ctx, "x"); status.Reason != ReasonOOMKilled || status.FinishedAt == nil {
		t.Fatalf("phase: %+v", status)
	}
}
