package sandbox

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"flow/api/internal/config"
	"flow/api/internal/domain"
)

// Sweeper is an extra GC task (for example track J's GitLab project-token
// sweep, or the deletion of archives of deleted snapshots).
type Sweeper struct {
	Name string
	Run  func(ctx context.Context) error
}

// GC deletes provider objects of this Flow instance that no live row owns
// (design §5.4): objects whose sandbox row is missing or settled-stopped, and
// snapshot objects (build PVCs, VolumeSnapshots) whose snapshot row is gone or
// no longer needs them. Objects younger than FLOW_CODING_ORPHAN_GRACE are
// left alone, so a row that is being created is never raced. It also deletes
// expired sandbox tokens and runs registered sweepers.
type GC struct {
	Store    MaintenanceStore
	Provider Provider
	Config   config.CodingConfig
	Leader   LeaderFunc
	Metrics  *Metrics
	Logger   *slog.Logger
	Now      func() time.Time

	mu       sync.Mutex
	sweepers []Sweeper
	once     sync.Once
}

// AddSweeper registers an extra task run on every GC pass.
func (g *GC) AddSweeper(sweeper Sweeper) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sweepers = append(g.sweepers, sweeper)
}

func (g *GC) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func (g *GC) logger() *slog.Logger {
	if g.Logger != nil {
		return g.Logger
	}
	return slog.Default()
}

// Run collects every FLOW_CODING_GC_INTERVAL until ctx ends.
func (g *GC) Run(ctx context.Context) {
	interval := g.Config.GCInterval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := g.RunOnce(ctx); err != nil && ctx.Err() == nil {
			g.logger().Warn("coding sandbox GC failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RunOnce runs one pass on the leader and returns how many objects it
// deleted.
func (g *GC) RunOnce(ctx context.Context) (int, error) {
	g.once.Do(func() {
		if g.Leader == nil {
			g.Leader = localLeader()
		}
	})
	deleted := 0
	_, err := g.Leader(ctx, "coding-sandbox-gc", func() error {
		var err error
		deleted, err = g.pass(ctx)
		return err
	})
	return deleted, err
}

// snapshotKeeps reports whether a snapshot status still needs an object.
func snapshotKeeps(objectType, status string) bool {
	switch objectType {
	case "volumesnapshot":
		return status == domain.CodingSnapshotReady || status == domain.CodingSnapshotBuilding || status == domain.CodingSnapshotQueued
	default: // build volumes
		return status == domain.CodingSnapshotBuilding || status == domain.CodingSnapshotQueued
	}
}

func (g *GC) pass(ctx context.Context) (int, error) {
	started := time.Now()
	defer g.Metrics.reconciled("gc", started)
	now := g.now()
	grace := g.Config.OrphanGrace
	if grace <= 0 {
		grace = 10 * time.Minute
	}
	objects, err := g.Provider.ListManaged(ctx, ListFilter{})
	if err != nil {
		return 0, err
	}
	sandboxIDs, snapshotIDs := map[string]bool{}, map[string]bool{}
	for _, object := range objects {
		if id := object.Labels[LabelSandboxID]; id != "" {
			sandboxIDs[id] = true
		} else if id := object.Labels[LabelSnapshotID]; id != "" {
			snapshotIDs[id] = true
		}
	}
	live, err := g.Store.KnownSandboxIDs(ctx, slices.Sorted(maps.Keys(sandboxIDs)))
	if err != nil {
		return 0, err
	}
	snapshots, err := g.Store.SnapshotStates(ctx, slices.Sorted(maps.Keys(snapshotIDs)))
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, object := range objects {
		if !object.CreatedAt.IsZero() && now.Sub(object.CreatedAt) < grace {
			continue
		}
		orphan := false
		if id := object.Labels[LabelSandboxID]; id != "" {
			orphan = !live[id]
		} else if id := object.Labels[LabelSnapshotID]; id != "" {
			status, ok := snapshots[id]
			orphan = !ok || !snapshotKeeps(object.Type, status)
		}
		if !orphan {
			continue
		}
		if err := g.Provider.DeleteManaged(ctx, object); err != nil {
			g.Metrics.providerError("gc-delete", err)
			g.logger().Warn("coding sandbox GC delete failed", "type", object.Type, "name", object.Name, "error", err)
			continue
		}
		g.Metrics.orphanDeleted(object.Type)
		g.logger().Info("coding sandbox GC deleted an orphan", "type", object.Type, "name", object.Name)
		deleted++
	}
	if n, err := g.Store.DeleteExpiredSandboxTokens(ctx, now); err != nil {
		g.logger().Warn("coding sandbox token sweep failed", "error", err)
	} else if n > 0 {
		g.logger().Info("coding sandbox tokens expired", "deleted", n)
	}
	if counts, err := g.Store.SandboxCounts(ctx); err == nil {
		for kind, phases := range counts {
			for phase, n := range phases {
				g.Metrics.sandboxCount(kind, phase, n)
			}
		}
	}
	g.mu.Lock()
	sweepers := slices.Clone(g.sweepers)
	g.mu.Unlock()
	for _, sweeper := range sweepers {
		if err := sweeper.Run(ctx); err != nil {
			g.logger().Warn("coding GC sweeper failed", "sweeper", sweeper.Name, "error", err)
		}
	}
	return deleted, nil
}
