// Package sandbox runs coding-session sandboxes (docs/coding-sessions-design.md
// §5, §9-§11). A Provider is a thin, idempotent driver over one runtime
// (Kubernetes Pods, Docker containers, or an in-memory fake). Flow's database
// is the source of truth: the reconciler (reconcile.go, track A) converges
// coding_sandboxes rows to the provider, and every object a provider creates
// carries deterministic names and the labels below so a restarted Flow can
// adopt or garbage-collect it.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"flow/api/internal/config"
)

// Labels set on every object a provider creates.
const (
	LabelManagedBy  = "flow.dev/managed-by" // always "flow"
	LabelInstance   = "flow.dev/instance"   // FLOW_CODING_INSTANCE_ID
	LabelKind       = "flow.dev/kind"       // session | snapshot | debug | warm
	LabelSandboxID  = "flow.dev/sandbox-id"
	LabelSessionID  = "flow.dev/session-id"
	LabelWorkspace  = "flow.dev/workspace"
	LabelSnapshotID = "flow.dev/snapshot-id"
	LabelSize       = "flow.dev/size"
	LabelRegion     = "flow.dev/region"
	ManagedByValue  = "flow"
)

// Environment variables the runner sees (besides the environment's own).
const (
	EnvRuntimeURL = "FLOW_RUNTIME_URL" // Flow's runtime API base
	EnvTokenFile  = "FLOW_TOKEN_FILE"  // path of the sandbox token
	EnvSandboxID  = "FLOW_SANDBOX_ID"
	// EnvEnvironmentID is the coding environment of a snapshot or debug
	// sandbox (sessions carry it on the session row instead).
	EnvEnvironmentID = "FLOW_ENVIRONMENT_ID"
	EnvMode          = "FLOW_RUNNER_MODE"    // session | snapshot | debug | warm | restore
	EnvFlowSandbox   = "FLOW_SANDBOX"        // "1" in every sandbox
	EnvFlowSnapshot  = "FLOW_SNAPSHOT"       // "1" while building a snapshot
	TokenMountPath   = "/var/run/flow/token" // projected token file
	WorkspacePath    = "/workspace"          // repository checkout and $HOME/work
	HomePath         = "/home/flow"          // sandbox home (mise files land here)
)

// Kind is what a sandbox is for.
type Kind string

const (
	KindSession  Kind = "session"
	KindSnapshot Kind = "snapshot"
	KindDebug    Kind = "debug"
	KindWarm     Kind = "warm"
)

// Phase is the observed lifecycle of a sandbox.
type Phase string

const (
	PhasePending   Phase = "pending"   // accepted, not yet scheduled/pulled
	PhaseStarting  Phase = "starting"  // scheduled, init containers / image pull
	PhaseRunning   Phase = "running"   // runner container running
	PhaseSucceeded Phase = "succeeded" // runner exited 0
	PhaseFailed    Phase = "failed"    // runner exited non-zero or the pod failed
	PhaseMissing   Phase = "missing"   // the provider has no such sandbox
)

// Reason is a provider-level failure reason, mapped to domain error codes by
// the session reconciler.
type Reason string

const (
	ReasonNone             Reason = ""
	ReasonImagePull        Reason = "ImagePullFailed"
	ReasonUnschedulable    Reason = "Unschedulable"
	ReasonOOMKilled        Reason = "OOMKilled"
	ReasonDeadlineExceeded Reason = "DeadlineExceeded"
	ReasonEvicted          Reason = "Evicted"
	ReasonInitFailed       Reason = "InitFailed" // restore init container failed
	ReasonExitNonZero      Reason = "ExitNonZero"
	ReasonQuota            Reason = "QuotaExceeded" // ResourceQuota rejection
	ReasonUnknown          Reason = "Unknown"
)

var (
	// ErrNotFound reports a sandbox, volume or snapshot the provider does not have.
	ErrNotFound = errors.New("sandbox: not found")
	// ErrUnsupported reports an operation this provider cannot do (e.g. VolumeSnapshot on docker).
	ErrUnsupported = errors.New("sandbox: unsupported by provider")
	// ErrNotImplemented is returned by provider stubs until their track lands.
	ErrNotImplemented = errors.New("sandbox: provider not implemented")
	// ErrDisabled is returned by the disabled provider.
	ErrDisabled = errors.New("sandbox: coding sandboxes are disabled")
	// ErrTransient wraps API errors worth retrying (timeouts, 429, 5xx, conflicts).
	ErrTransient = errors.New("sandbox: transient provider error")
)

// Capabilities describes what a provider supports.
type Capabilities struct {
	VolumeSnapshots   bool // CreateSnapshot / RestoreWorkspace from a VolumeSnapshot
	PersistentVolumes bool
	Exec              bool
	TTY               bool
	RuntimeClass      bool
	UserNamespaces    bool
	NetworkPolicy     bool
	Regions           bool
}

// Resources are a sandbox's limits (requests derive from FLOW_CODING_CPU_REQUEST_RATIO).
type Resources struct {
	Size             string // small | medium | large
	CPU              string
	Memory           string
	EphemeralStorage string
	PIDs             int64
}

// VolumeSource says how a sandbox's /workspace volume is populated.
type VolumeSource struct {
	// VolumeID mounts an existing workspace volume (EnsureVolume/RestoreWorkspace).
	VolumeID string
	// ArchiveURL makes the restore init container download and unpack a
	// snapshot archive (archive strategy); authenticated with the sandbox token.
	ArchiveURL string
	// Empty: neither set → fresh emptyDir/volume; the runner clones.
}

// SandboxSpec is everything needed to start one sandbox. It is deterministic
// for an ID, so StartSandbox is idempotent: starting an existing ID returns
// its status without creating anything.
type SandboxSpec struct {
	ID           string // coding_sandboxes.id; object names derive from it
	Kind         Kind
	WorkspaceKey string
	SessionID    string
	SnapshotID   string
	Image        string
	Resources    Resources
	Region       string
	// Token is the short-lived sandbox token, delivered as a projected file
	// (never an env var). Providers store it in a Secret/temp file owned by
	// the sandbox so it disappears with it.
	Token     string
	Env       map[string]string // non-secret env only
	Workspace VolumeSource
	// Deadline bounds the sandbox's whole life (activeDeadlineSeconds).
	Deadline time.Duration
	Labels   map[string]string
	// ProxyURL is set as HTTP(S)_PROXY; NoProxy as NO_PROXY.
	ProxyURL string
	NoProxy  []string
}

// SandboxStatus is the observed state of a sandbox.
type SandboxStatus struct {
	ID          string
	Kind        Kind
	Phase       Phase
	Reason      Reason
	Message     string
	ProviderRef string // pod or container name
	Node        string
	Region      string
	ExitCode    *int
	CreatedAt   time.Time
	StartedAt   *time.Time
	FinishedAt  *time.Time
	Labels      map[string]string
}

// StopOptions controls StopSandbox.
type StopOptions struct {
	// Grace is the SIGTERM → SIGKILL window (FLOW_CODING_STOP_GRACE).
	Grace time.Duration
	// KeepVolume keeps the workspace volume for a later snapshot or follow-up.
	KeepVolume bool
}

// VolumeSpec describes a workspace volume (a PVC, a docker volume).
type VolumeSpec struct {
	ID           string
	WorkspaceKey string
	Size         string
	StorageClass string
	Labels       map[string]string
}

// VolumeStatus is an observed workspace volume.
type VolumeStatus struct {
	ID          string
	ProviderRef string
	Ready       bool
	Phase       string
	Labels      map[string]string
}

// SnapshotSpec creates a provider-native snapshot (VolumeSnapshot) of a
// workspace volume. The archive strategy does not use it: archives are made
// by the runner in a snapshot sandbox and stored through Flow (strategy.go).
type SnapshotSpec struct {
	ID       string
	VolumeID string
	Class    string
	Labels   map[string]string
}

// SnapshotStatus is an observed provider-native snapshot.
type SnapshotStatus struct {
	ID          string
	ProviderRef string
	Ready       bool
	SizeBytes   int64
	Error       string
	CreatedAt   time.Time
}

// RestoreSpec creates a workspace volume from a provider-native snapshot.
type RestoreSpec struct {
	VolumeID     string
	SnapshotID   string
	WorkspaceKey string
	Size         string
	StorageClass string
	Labels       map[string]string
}

// ExecRequest runs a command in a running sandbox (debug terminal, probes).
type ExecRequest struct {
	Container string // "" = runner
	Command   []string
	TTY       bool
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer // ignored with TTY
	// Resize delivers terminal sizes while TTY is set.
	Resize <-chan TerminalSize
}

// TerminalSize is a TTY size.
type TerminalSize struct{ Width, Height uint16 }

// ExecResult is the outcome of Exec.
type ExecResult struct{ ExitCode int }

// LogOptions selects sandbox logs.
type LogOptions struct {
	Container string // "" = runner; "restore" = the restore init container
	Follow    bool
	TailLines int64
	SinceTime *time.Time
	Previous  bool
}

// ManagedObject is anything a provider created (for orphan GC).
type ManagedObject struct {
	Type      string // pod | pvc | secret | volumesnapshot | container | volume
	Name      string
	Labels    map[string]string
	CreatedAt time.Time
}

// ListFilter narrows ListManaged.
type ListFilter struct {
	Kind      Kind
	SandboxID string
	SessionID string
}

// PreflightResult is one startup/diagnostic check (shown in Settings and logs).
type PreflightResult struct {
	Name    string
	OK      bool
	Warning bool // OK but degraded (e.g. runc without gVisor/Kata)
	Message string
}

// Provider drives one sandbox runtime. Every method is idempotent and safe to
// retry: names derive from IDs, "already exists" is success, "not found" on
// delete is success. Implementations wrap retryable failures in ErrTransient.
type Provider interface {
	Name() string
	Capabilities() Capabilities
	// Preflight verifies access and settings (namespace, RBAC, RuntimeClass,
	// StorageClass, VolumeSnapshotClass, NetworkPolicy support).
	Preflight(ctx context.Context) []PreflightResult

	// EnsureVolume creates an empty workspace volume if missing.
	EnsureVolume(ctx context.Context, spec VolumeSpec) (VolumeStatus, error)
	// RestoreWorkspace creates a workspace volume from a native snapshot.
	RestoreWorkspace(ctx context.Context, spec RestoreSpec) (VolumeStatus, error)
	VolumeStatus(ctx context.Context, volumeID string) (VolumeStatus, error)
	DeleteVolume(ctx context.Context, volumeID string) error

	// CreateSnapshot snapshots a workspace volume (VolumeSnapshot). Providers
	// without native snapshots return ErrUnsupported.
	CreateSnapshot(ctx context.Context, spec SnapshotSpec) (SnapshotStatus, error)
	SnapshotStatus(ctx context.Context, snapshotID string) (SnapshotStatus, error)
	DeleteSnapshot(ctx context.Context, snapshotID string) error

	StartSandbox(ctx context.Context, spec SandboxSpec) (SandboxStatus, error)
	Status(ctx context.Context, sandboxID string) (SandboxStatus, error)
	StopSandbox(ctx context.Context, sandboxID string, options StopOptions) error
	Exec(ctx context.Context, sandboxID string, request ExecRequest) (ExecResult, error)
	Logs(ctx context.Context, sandboxID string, options LogOptions) (io.ReadCloser, error)

	// ListManaged lists every object of this Flow instance, for orphan GC and
	// adoption after a restart; DeleteManaged removes one.
	ListManaged(ctx context.Context, filter ListFilter) ([]ManagedObject, error)
	DeleteManaged(ctx context.Context, object ManagedObject) error
}

// Dependencies are what providers may need from Flow.
type Dependencies struct {
	Config config.CodingConfig
	Now    func() time.Time
}

// Factory builds a provider.
type Factory func(deps Dependencies) (Provider, error)

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register makes a provider available under name (called from init in the
// provider packages; cmd/server imports them in coding_providers.go).
func Register(name string, factory Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		panic("sandbox: provider registered twice: " + name)
	}
	registry[name] = factory
}

// Providers lists the registered provider names.
func Providers() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Open returns the provider FLOW_CODING_PROVIDER selects.
func Open(deps Dependencies) (Provider, error) {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	name := deps.Config.Provider
	if name == "" || name == config.CodingProviderDisabled {
		return Disabled{}, nil
	}
	registryMu.RLock()
	factory, ok := registry[name]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("sandbox: unknown provider %q (registered: %v)", name, Providers())
	}
	return factory(deps)
}

// ObjectName is the deterministic object name of a sandbox's resources, e.g.
// ObjectName("cs", id) → "flow-cs-<id>". IDs are lowercase alphanumerics.
func ObjectName(kind, id string) string { return "flow-" + kind + "-" + id }

// Disabled is the provider used when FLOW_CODING_PROVIDER=disabled.
type Disabled struct{}

func (Disabled) Name() string               { return config.CodingProviderDisabled }
func (Disabled) Capabilities() Capabilities { return Capabilities{} }
func (Disabled) Preflight(context.Context) []PreflightResult {
	return []PreflightResult{{Name: "provider", OK: false, Message: "FLOW_CODING_PROVIDER=disabled"}}
}
func (Disabled) EnsureVolume(context.Context, VolumeSpec) (VolumeStatus, error) {
	return VolumeStatus{}, ErrDisabled
}
func (Disabled) RestoreWorkspace(context.Context, RestoreSpec) (VolumeStatus, error) {
	return VolumeStatus{}, ErrDisabled
}
func (Disabled) VolumeStatus(context.Context, string) (VolumeStatus, error) {
	return VolumeStatus{}, ErrDisabled
}
func (Disabled) DeleteVolume(context.Context, string) error { return nil }
func (Disabled) CreateSnapshot(context.Context, SnapshotSpec) (SnapshotStatus, error) {
	return SnapshotStatus{}, ErrDisabled
}
func (Disabled) SnapshotStatus(context.Context, string) (SnapshotStatus, error) {
	return SnapshotStatus{}, ErrDisabled
}
func (Disabled) DeleteSnapshot(context.Context, string) error { return nil }
func (Disabled) StartSandbox(context.Context, SandboxSpec) (SandboxStatus, error) {
	return SandboxStatus{}, ErrDisabled
}
func (Disabled) Status(_ context.Context, id string) (SandboxStatus, error) {
	return SandboxStatus{ID: id, Phase: PhaseMissing}, nil
}
func (Disabled) StopSandbox(context.Context, string, StopOptions) error { return nil }
func (Disabled) Exec(context.Context, string, ExecRequest) (ExecResult, error) {
	return ExecResult{}, ErrDisabled
}
func (Disabled) Logs(context.Context, string, LogOptions) (io.ReadCloser, error) {
	return nil, ErrDisabled
}
func (Disabled) ListManaged(context.Context, ListFilter) ([]ManagedObject, error) { return nil, nil }
func (Disabled) DeleteManaged(context.Context, ManagedObject) error               { return nil }
