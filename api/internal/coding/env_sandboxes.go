package coding

// Sandboxes for environments: snapshot builds and debug terminals
// (docs/coding-sessions-design.md §10, §18). Owner: track K.
//
// The snapshot reconciler and the debug terminal start sandboxes that belong
// to an environment rather than a session. They go through SandboxLauncher,
// which has two implementations:
//
//   - RecordLauncher writes a coding_sandboxes row (desired=running) and lets
//     the sandbox reconciler (track A) start, observe, bill and garbage-collect
//     it, exactly like session sandboxes. This is the production path.
//   - ProviderLauncher drives a sandbox.Provider directly, minting the sandbox
//     token and recording runtime usage itself. Tests use it with sandbox.Fake;
//     the server falls back to it when the store has no sandbox records.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"flow/api/internal/codingenv"
	"flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
)

// LaunchRequest describes an environment sandbox. Launch is idempotent for an ID.
type LaunchRequest struct {
	ID            string
	WorkspaceKey  string
	Kind          sandbox.Kind // snapshot | debug
	SnapshotID    string
	EnvironmentID string
	// UserID is billed for the runtime (the requesting user).
	UserID    string
	Size      string
	Region    string
	Workspace sandbox.VolumeSource
	Env       map[string]string
	Deadline  time.Duration
}

// SandboxLauncher starts, observes and releases environment sandboxes.
type SandboxLauncher interface {
	Launch(ctx context.Context, request LaunchRequest) error
	Observe(ctx context.Context, workspaceKey, sandboxID string) (sandbox.SandboxStatus, error)
	// Release stops the sandbox; releasing a missing sandbox succeeds.
	Release(ctx context.Context, workspaceKey, sandboxID string) error
}

// SandboxRecordStore is the part of the sandbox store RecordLauncher needs:
// the CoreStore sandbox methods (track B; codingstore.Store implements them).
// CodingSandbox is not workspace-scoped, so RecordLauncher checks the
// workspace itself.
type SandboxRecordStore interface {
	CreateCodingSandbox(ctx context.Context, record sandbox.Record) error
	SetCodingSandboxDesired(ctx context.Context, id string, desired sandbox.Desired) error
	CodingSandbox(ctx context.Context, id string) (sandbox.Record, error)
}

// sandboxInWorkspace reads a record and hides other workspaces' sandboxes.
func (l RecordLauncher) sandboxInWorkspace(ctx context.Context, workspaceKey, id string) (sandbox.Record, error) {
	record, err := l.Store.CodingSandbox(ctx, id)
	if err != nil {
		return sandbox.Record{}, err
	}
	if record.WorkspaceKey != workspaceKey {
		return sandbox.Record{}, ErrNotFound
	}
	return record, nil
}

// RecordLauncher launches sandboxes as coding_sandboxes rows.
type RecordLauncher struct {
	Store  SandboxRecordStore
	Config config.CodingConfig
	Now    func() time.Time
}

func (l RecordLauncher) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l RecordLauncher) Launch(ctx context.Context, request LaunchRequest) error {
	if _, err := l.sandboxInWorkspace(ctx, request.WorkspaceKey, request.ID); err == nil {
		return nil
	} else if !errors.Is(err, ErrNotFound) && !errors.Is(err, sandbox.ErrNotFound) {
		return err
	}
	now := l.now()
	env := map[string]string{}
	for key, value := range request.Env {
		env[key] = value
	}
	if request.EnvironmentID != "" {
		// Token auth and bootstrap resolve the environment from it.
		env[sandbox.EnvEnvironmentID] = request.EnvironmentID
	}
	record := sandbox.Record{
		ID: request.ID, WorkspaceKey: request.WorkspaceKey, SnapshotID: request.SnapshotID, Kind: request.Kind,
		Desired: sandbox.DesiredRunning, Phase: sandbox.PhasePending, Image: l.Config.Image, Size: sizeOrDefault(request.Size),
		Region: request.Region, Workspace: request.Workspace, Env: env, DeadlineAt: now.Add(request.Deadline),
		CreatedAt: now, UserID: request.UserID,
	}
	if err := l.Store.CreateCodingSandbox(ctx, record); err != nil {
		// A concurrent launch of the same id already created it.
		if _, lookup := l.sandboxInWorkspace(ctx, request.WorkspaceKey, request.ID); lookup == nil {
			return nil
		}
		return err
	}
	return nil
}

func (l RecordLauncher) Observe(ctx context.Context, workspaceKey, sandboxID string) (sandbox.SandboxStatus, error) {
	record, err := l.sandboxInWorkspace(ctx, workspaceKey, sandboxID)
	if errors.Is(err, ErrNotFound) || errors.Is(err, sandbox.ErrNotFound) {
		return sandbox.SandboxStatus{ID: sandboxID, Phase: sandbox.PhaseMissing}, nil
	}
	if err != nil {
		return sandbox.SandboxStatus{}, err
	}
	return sandbox.SandboxStatus{ID: record.ID, Kind: record.Kind, Phase: record.Phase, Reason: record.Reason, Message: record.Message, ProviderRef: record.ProviderRef, Region: record.Region, CreatedAt: record.CreatedAt, StartedAt: record.StartedAt, FinishedAt: record.EndedAt}, nil
}

func (l RecordLauncher) Release(ctx context.Context, _ string, sandboxID string) error {
	err := l.Store.SetCodingSandboxDesired(ctx, sandboxID, sandbox.DesiredStopped)
	if errors.Is(err, ErrNotFound) || errors.Is(err, sandbox.ErrNotFound) {
		return nil
	}
	return err
}

// TokenMinter issues sandbox tokens (sandbox.Store.MintSandboxToken).
type TokenMinter func(ctx context.Context, record sandbox.Record, ttl time.Duration) (string, error)

// UsageRecorder records metered runtime (UsageStore.RecordCodingUsage).
type UsageRecorder func(ctx context.Context, record domain.CodingUsageRecord) error

// ProviderLauncher drives a provider directly.
type ProviderLauncher struct {
	Provider sandbox.Provider
	Config   config.CodingConfig
	Mint     TokenMinter   // nil: the sandbox gets no runtime token
	Usage    UsageRecorder // nil: runtime is not metered here
	Now      func() time.Time
}

func (l ProviderLauncher) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// Label keys ProviderLauncher adds so Release can bill after a restart.
const (
	labelBilledUser  = "flow.dev/user-id"
	labelEnvironment = "flow.dev/environment-id"
)

func (l ProviderLauncher) Launch(ctx context.Context, request LaunchRequest) error {
	status, err := l.Provider.Status(ctx, request.ID)
	if err == nil && status.Phase != sandbox.PhaseMissing {
		return nil
	}
	size := sizeOrDefault(request.Size)
	limits := l.Config.Sizes[size]
	record := sandbox.Record{ID: request.ID, WorkspaceKey: request.WorkspaceKey, SnapshotID: request.SnapshotID, Kind: request.Kind, Size: size, DeadlineAt: l.now().Add(request.Deadline), UserID: request.UserID}
	token := ""
	if l.Mint != nil {
		ttl := request.Deadline + l.Config.Timeouts.TokenGrace
		if token, err = l.Mint(ctx, record, ttl); err != nil {
			return fmt.Errorf("mint sandbox token: %w", err)
		}
	}
	env := map[string]string{}
	for key, value := range request.Env {
		env[key] = value
	}
	env[sandbox.EnvRuntimeURL] = l.Config.RuntimeURL
	env[sandbox.EnvTokenFile] = sandbox.TokenMountPath
	env[sandbox.EnvSandboxID] = request.ID
	env[sandbox.EnvFlowSandbox] = "1"
	labels := map[string]string{sandbox.LabelInstance: l.Config.InstanceID, sandbox.LabelSize: size}
	if request.SnapshotID != "" {
		labels[sandbox.LabelSnapshotID] = request.SnapshotID
	}
	if request.UserID != "" && labelSafe(request.UserID) {
		labels[labelBilledUser] = request.UserID
	}
	if request.EnvironmentID != "" && labelSafe(request.EnvironmentID) {
		labels[labelEnvironment] = request.EnvironmentID
	}
	_, err = l.Provider.StartSandbox(ctx, sandbox.SandboxSpec{
		ID: request.ID, Kind: request.Kind, WorkspaceKey: request.WorkspaceKey, SnapshotID: request.SnapshotID, Image: l.Config.Image,
		Resources: sandbox.Resources{Size: size, CPU: limits.CPU, Memory: limits.Memory, EphemeralStorage: limits.EphemeralStorage, PIDs: limits.PIDs},
		Region:    request.Region, Token: token, Env: env, Workspace: request.Workspace, Deadline: request.Deadline, Labels: labels,
		ProxyURL: l.Config.Egress.ProxyURL, NoProxy: l.Config.Egress.NoProxy,
	})
	return err
}

func (l ProviderLauncher) Observe(ctx context.Context, _ string, sandboxID string) (sandbox.SandboxStatus, error) {
	return l.Provider.Status(ctx, sandboxID)
}

func (l ProviderLauncher) Release(ctx context.Context, workspaceKey, sandboxID string) error {
	status, statusErr := l.Provider.Status(ctx, sandboxID)
	// A snapshot build's volume outlives the sandbox: the strategy snapshots
	// it and the reconciler deletes it afterwards.
	options := sandbox.StopOptions{Grace: l.Config.Timeouts.StopGrace, KeepVolume: status.Kind == sandbox.KindSnapshot}
	if err := l.Provider.StopSandbox(ctx, sandboxID, options); err != nil && !errors.Is(err, sandbox.ErrNotFound) {
		return err
	}
	if statusErr != nil || status.Phase == sandbox.PhaseMissing || status.StartedAt == nil || l.Usage == nil {
		return nil
	}
	end := l.now()
	if status.FinishedAt != nil {
		end = *status.FinishedAt
	}
	seconds := int64(math.Ceil(end.Sub(*status.StartedAt).Seconds()))
	if seconds <= 0 {
		return nil
	}
	size := status.Labels[sandbox.LabelSize]
	if size == "" {
		size = domain.CodingSizeMedium
	}
	return l.Usage(ctx, domain.CodingUsageRecord{
		ID: "usage_" + sandboxID, WorkspaceKey: workspaceKey, SandboxID: sandboxID, UserID: status.Labels[labelBilledUser],
		Kind: "runtime", RuntimeSeconds: seconds, Size: size, CostMicros: RuntimeCostMicros(l.Config.RuntimePricesUSD, size, seconds), CreatedAt: end,
	})
}

func sizeOrDefault(size string) string {
	switch size {
	case domain.CodingSizeSmall, domain.CodingSizeMedium, domain.CodingSizeLarge:
		return size
	}
	return domain.CodingSizeMedium
}

func labelSafe(value string) bool {
	if len(value) > 63 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

// NewResourceID returns a 26-character lowercase ULID (codingenv.NewResourceID).
func NewResourceID(now time.Time) string { return codingenv.NewResourceID(now) }
