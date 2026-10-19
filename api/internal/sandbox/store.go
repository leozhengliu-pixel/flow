package sandbox

import (
	"context"
	"time"
)

// Desired is what Flow wants a sandbox to be.
type Desired string

const (
	DesiredRunning Desired = "running"
	DesiredStopped Desired = "stopped"
)

// Record is a coding_sandboxes row as the sandbox reconciler sees it. The
// session layer (internal/coding, track B) creates records and flips Desired;
// the sandbox reconciler (track A) converges the provider to them and writes
// the observation back. Neither layer calls the other directly.
type Record struct {
	ID           string
	WorkspaceKey string
	SessionID    string
	SnapshotID   string
	Kind         Kind
	Desired      Desired
	Phase        Phase
	Reason       Reason
	Message      string
	Image        string
	Size         string
	Region       string
	Workspace    VolumeSource
	Env          map[string]string
	DeadlineAt   time.Time
	Attempts     int
	ProviderRef  string
	CreatedAt    time.Time
	StartedAt    *time.Time
	EndedAt      *time.Time
	HeartbeatAt  *time.Time

	// Track A additions (read by the sandbox reconciler).

	// UserID is who runtime is billed to: the session creator, the snapshot
	// requester, or the debug terminal user (stored in data for the latter).
	// Track K's environment launcher sets it for snapshot and debug sandboxes.
	UserID string
	// LoopID is the loop a loop-started session is charged to.
	LoopID string
	// BilledSeconds is the runtime already metered (coding_sandboxes.billed_seconds).
	BilledSeconds int64
	// LogsKey is the object-store key of the archived runner log, once saved.
	LogsKey string
	// KeepVolume keeps the workspace volume when the sandbox stops (a
	// volumesnapshot build whose PVC is snapshotted afterwards).
	KeepVolume bool
}

// Store is the persistence the sandbox reconciler needs; *store.SQLiteStore
// implements it (api/internal/store/coding_sandboxes.go, track A).
type Store interface {
	// LeaseSandboxes returns up to limit records that are not settled
	// (Desired != observed) or due for an observation, leased to owner.
	LeaseSandboxes(ctx context.Context, owner string, lease time.Duration, limit int) ([]Record, error)
	// SaveSandboxObservation stores what the provider reported (CAS on the lease).
	SaveSandboxObservation(ctx context.Context, owner, id string, status SandboxStatus, attempts int) error
	// MintSandboxToken issues the sandbox's short-lived runtime token (only its hash is stored).
	MintSandboxToken(ctx context.Context, record Record, ttl time.Duration) (string, error)
	// KnownSandboxIDs reports which ids still have a live row (orphan GC).
	KnownSandboxIDs(ctx context.Context, ids []string) (map[string]bool, error)
	// WarmSandboxes counts unclaimed warm sandboxes per size and region.
	WarmSandboxes(ctx context.Context) (map[string]int, error)
	// CreateWarmSandbox inserts a warm-pool record.
	CreateWarmSandbox(ctx context.Context, record Record) error
}
