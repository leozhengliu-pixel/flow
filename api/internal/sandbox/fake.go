package sandbox

import (
	"context"
	"fmt"
	"io"
	"maps"
	"strings"
	"sync"
	"time"

	"flow/api/internal/config"
)

func init() {
	Register(config.CodingProviderFake, func(deps Dependencies) (Provider, error) { return NewFake(deps.Now), nil })
}

// Fake is an in-memory provider for tests and FLOW_CODING_PROVIDER=fake. It
// honours the Provider contract (idempotency, not-found semantics, labels)
// and lets tests drive phases with SetPhase and inject failures with FailNext.
type Fake struct {
	mu        sync.Mutex
	now       func() time.Time
	sandboxes map[string]*fakeSandbox
	volumes   map[string]VolumeStatus
	snapshots map[string]SnapshotStatus
	failNext  map[string]error
	// StartPhase is the phase new sandboxes start in (default PhaseRunning).
	StartPhase Phase
}

type fakeSandbox struct {
	spec   SandboxSpec
	status SandboxStatus
	logs   strings.Builder
}

// NewFake returns an empty fake provider.
func NewFake(now func() time.Time) *Fake {
	if now == nil {
		now = time.Now
	}
	return &Fake{now: now, sandboxes: map[string]*fakeSandbox{}, volumes: map[string]VolumeStatus{}, snapshots: map[string]SnapshotStatus{}, failNext: map[string]error{}, StartPhase: PhaseRunning}
}

// FailNext makes the next call of method ("StartSandbox", "CreateSnapshot", …) return err.
func (f *Fake) FailNext(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failNext[method] = err
}

func (f *Fake) takeFailure(method string) error {
	if err, ok := f.failNext[method]; ok {
		delete(f.failNext, method)
		return err
	}
	return nil
}

// SetPhase moves a sandbox to a phase (tests simulate the runtime with it).
func (f *Fake) SetPhase(id string, phase Phase, reason Reason) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	box, ok := f.sandboxes[id]
	if !ok {
		return ErrNotFound
	}
	box.status.Phase, box.status.Reason = phase, reason
	now := f.now()
	if phase == PhaseRunning && box.status.StartedAt == nil {
		box.status.StartedAt = &now
	}
	if phase == PhaseSucceeded || phase == PhaseFailed {
		box.status.FinishedAt = &now
	}
	return nil
}

// AppendLog adds runner log output.
func (f *Fake) AppendLog(id, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if box, ok := f.sandboxes[id]; ok {
		box.logs.WriteString(text)
	}
}

// Spec returns the spec a sandbox was started with.
func (f *Fake) Spec(id string) (SandboxSpec, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	box, ok := f.sandboxes[id]
	if !ok {
		return SandboxSpec{}, false
	}
	return box.spec, true
}

func (f *Fake) Name() string { return config.CodingProviderFake }

func (f *Fake) Capabilities() Capabilities {
	return Capabilities{VolumeSnapshots: true, PersistentVolumes: true, Exec: true, TTY: true, Regions: true}
}

func (f *Fake) Preflight(context.Context) []PreflightResult {
	return []PreflightResult{{Name: "provider", OK: true, Warning: true, Message: "fake provider: sandboxes do not run"}}
}

func (f *Fake) EnsureVolume(_ context.Context, spec VolumeSpec) (VolumeStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.takeFailure("EnsureVolume"); err != nil {
		return VolumeStatus{}, err
	}
	if existing, ok := f.volumes[spec.ID]; ok {
		return existing, nil
	}
	status := VolumeStatus{ID: spec.ID, ProviderRef: ObjectName("ws", spec.ID), Ready: true, Phase: "Bound", Labels: maps.Clone(spec.Labels)}
	f.volumes[spec.ID] = status
	return status, nil
}

func (f *Fake) RestoreWorkspace(_ context.Context, spec RestoreSpec) (VolumeStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.takeFailure("RestoreWorkspace"); err != nil {
		return VolumeStatus{}, err
	}
	snapshot, ok := f.snapshots[spec.SnapshotID]
	if !ok || !snapshot.Ready {
		return VolumeStatus{}, fmt.Errorf("%w: snapshot %s", ErrNotFound, spec.SnapshotID)
	}
	if existing, ok := f.volumes[spec.VolumeID]; ok {
		return existing, nil
	}
	status := VolumeStatus{ID: spec.VolumeID, ProviderRef: ObjectName("ws", spec.VolumeID), Ready: true, Phase: "Bound", Labels: maps.Clone(spec.Labels)}
	f.volumes[spec.VolumeID] = status
	return status, nil
}

func (f *Fake) VolumeStatus(_ context.Context, id string) (VolumeStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	status, ok := f.volumes[id]
	if !ok {
		return VolumeStatus{}, ErrNotFound
	}
	return status, nil
}

func (f *Fake) DeleteVolume(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.volumes, id)
	return nil
}

func (f *Fake) CreateSnapshot(_ context.Context, spec SnapshotSpec) (SnapshotStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.takeFailure("CreateSnapshot"); err != nil {
		return SnapshotStatus{}, err
	}
	if existing, ok := f.snapshots[spec.ID]; ok {
		return existing, nil
	}
	if _, ok := f.volumes[spec.VolumeID]; !ok {
		return SnapshotStatus{}, fmt.Errorf("%w: volume %s", ErrNotFound, spec.VolumeID)
	}
	status := SnapshotStatus{ID: spec.ID, ProviderRef: ObjectName("snap", spec.ID), Ready: true, CreatedAt: f.now()}
	f.snapshots[spec.ID] = status
	return status, nil
}

func (f *Fake) SnapshotStatus(_ context.Context, id string) (SnapshotStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	status, ok := f.snapshots[id]
	if !ok {
		return SnapshotStatus{}, ErrNotFound
	}
	return status, nil
}

func (f *Fake) DeleteSnapshot(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.snapshots, id)
	return nil
}

func (f *Fake) StartSandbox(_ context.Context, spec SandboxSpec) (SandboxStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.takeFailure("StartSandbox"); err != nil {
		return SandboxStatus{}, err
	}
	if spec.ID == "" {
		return SandboxStatus{}, fmt.Errorf("sandbox: spec.ID is required")
	}
	if existing, ok := f.sandboxes[spec.ID]; ok {
		return existing.status, nil
	}
	if spec.Workspace.VolumeID != "" {
		if _, ok := f.volumes[spec.Workspace.VolumeID]; !ok {
			return SandboxStatus{}, fmt.Errorf("%w: volume %s", ErrNotFound, spec.Workspace.VolumeID)
		}
	}
	now := f.now()
	labels := maps.Clone(spec.Labels)
	if labels == nil {
		labels = map[string]string{}
	}
	labels[LabelManagedBy], labels[LabelKind], labels[LabelSandboxID] = ManagedByValue, string(spec.Kind), spec.ID
	if spec.SessionID != "" {
		labels[LabelSessionID] = spec.SessionID
	}
	status := SandboxStatus{ID: spec.ID, Kind: spec.Kind, Phase: f.StartPhase, ProviderRef: ObjectName("cs", spec.ID), Region: spec.Region, CreatedAt: now, Labels: labels}
	if status.Phase == PhaseRunning {
		status.StartedAt = &now
	}
	spec.Token = "" // never retained
	f.sandboxes[spec.ID] = &fakeSandbox{spec: spec, status: status}
	return status, nil
}

func (f *Fake) Status(_ context.Context, id string) (SandboxStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.takeFailure("Status"); err != nil {
		return SandboxStatus{}, err
	}
	box, ok := f.sandboxes[id]
	if !ok {
		return SandboxStatus{ID: id, Phase: PhaseMissing}, nil
	}
	return box.status, nil
}

func (f *Fake) StopSandbox(_ context.Context, id string, _ StopOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.takeFailure("StopSandbox"); err != nil {
		return err
	}
	delete(f.sandboxes, id)
	return nil
}

func (f *Fake) Exec(_ context.Context, id string, request ExecRequest) (ExecResult, error) {
	f.mu.Lock()
	box, ok := f.sandboxes[id]
	running := ok && box.status.Phase == PhaseRunning
	f.mu.Unlock()
	if !ok {
		return ExecResult{}, ErrNotFound
	}
	if !running {
		return ExecResult{}, fmt.Errorf("sandbox: %s is not running", id)
	}
	if request.Stdout != nil {
		_, _ = fmt.Fprintf(request.Stdout, "fake exec: %s\n", strings.Join(request.Command, " "))
	}
	return ExecResult{ExitCode: 0}, nil
}

func (f *Fake) Logs(_ context.Context, id string, _ LogOptions) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	box, ok := f.sandboxes[id]
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(strings.NewReader(box.logs.String())), nil
}

func (f *Fake) ListManaged(_ context.Context, filter ListFilter) ([]ManagedObject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := []ManagedObject{}
	for _, box := range f.sandboxes {
		if filter.Kind != "" && box.status.Kind != filter.Kind || filter.SandboxID != "" && box.status.ID != filter.SandboxID || filter.SessionID != "" && box.spec.SessionID != filter.SessionID {
			continue
		}
		result = append(result, ManagedObject{Type: "pod", Name: box.status.ProviderRef, Labels: maps.Clone(box.status.Labels), CreatedAt: box.status.CreatedAt})
	}
	return result, nil
}

func (f *Fake) DeleteManaged(_ context.Context, object ManagedObject) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id := object.Labels[LabelSandboxID]; id != "" {
		delete(f.sandboxes, id)
	}
	return nil
}
