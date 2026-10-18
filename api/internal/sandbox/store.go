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
}

// Store is the persistence the sandbox reconciler needs; *store.SQLiteStore
// implements it (api/internal/store/coding_sandboxes.go, track B).
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
