package sandbox

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/config"
)

// MaintenanceStore is what the warm pool and orphan GC need beyond Store
// (track A additions; *store.SQLiteStore implements it).
type MaintenanceStore interface {
	Store
	// RecycleWarmSandboxes sets desired=stopped on unbound warm sandboxes
	// created before cutoff, or already failed/missing, and returns how many.
	RecycleWarmSandboxes(ctx context.Context, createdBefore time.Time) (int, error)
	// DeleteExpiredSandboxTokens deletes tokens that expired before t.
	DeleteExpiredSandboxTokens(ctx context.Context, before time.Time) (int64, error)
	// SnapshotStates returns the status of each snapshot id that has a row.
	SnapshotStates(ctx context.Context, ids []string) (map[string]string, error)
	// SandboxCounts counts sandbox rows that are not settled-stopped, by kind
	// and observed phase.
	SandboxCounts(ctx context.Context) (map[Kind]map[Phase]int, error)
}

// LeaderFunc runs action only on the elected instance (the Redis
// coordination lock; see coordination.Redis.WithLeaderLock). It reports
// whether action ran.
type LeaderFunc func(ctx context.Context, name string, action func() error) (bool, error)

// localLeader is the single-instance fallback (Flow without Redis is single
// instance by design, config.Validate).
func localLeader() LeaderFunc {
	var mu sync.Mutex
	return func(_ context.Context, _ string, action func() error) (bool, error) {
		if !mu.TryLock() {
			return false, nil
		}
		defer mu.Unlock()
		return true, action()
	}
}

// WarmKey is the WarmSandboxes key of a size and region ("medium/" without
// region).
func WarmKey(size, region string) string { return size + "/" + region }

// WarmPool keeps FLOW_CODING_WARM_POOL unbound sandboxes per size (and per
// region when regions are configured) and recycles them after
// FLOW_CODING_WARM_POOL_MAX_AGE. Warm sandboxes are ordinary rows of kind
// warm; the sandbox reconciler starts and stops them, and the session
// reconciler binds them (BindWarmSandbox).
type WarmPool struct {
	Store   MaintenanceStore
	Config  config.CodingConfig
	Leader  LeaderFunc
	Metrics *Metrics
	Logger  *slog.Logger
	Now     func() time.Time
	// Wake is called after rows were created or recycled (the sandbox
	// reconciler's Wake).
	Wake func()

	once sync.Once
}

func (w *WarmPool) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *WarmPool) logger() *slog.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return slog.Default()
}

// Enabled reports whether a pool is configured.
func (w *WarmPool) Enabled() bool {
	for _, n := range w.Config.WarmPool {
		if n > 0 {
			return true
		}
	}
	return false
}

// Targets are the wanted counts per WarmKey.
func (w *WarmPool) Targets() map[string]int {
	targets := map[string]int{}
	regions := w.Config.Regions
	if len(regions) == 0 {
		regions = []string{""}
	}
	for size, n := range w.Config.WarmPool {
		if n <= 0 {
			continue
		}
		for _, region := range regions {
			targets[WarmKey(size, region)] = n
		}
	}
	return targets
}

// Run tops the pool up every reconcile interval (at least every 10s) until
// ctx ends.
func (w *WarmPool) Run(ctx context.Context) {
	if !w.Enabled() {
		return
	}
	interval := max(w.Config.ReconcileInterval*2, 10*time.Second)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := w.RunOnce(ctx); err != nil && ctx.Err() == nil {
			w.logger().Warn("coding warm pool pass failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RunOnce recycles old or failed warm sandboxes and creates missing ones.
// It runs only on the leader, so replicas never over-provision.
func (w *WarmPool) RunOnce(ctx context.Context) error {
	w.once.Do(func() {
		if w.Leader == nil {
			w.Leader = localLeader()
		}
	})
	_, err := w.Leader(ctx, "coding-warm-pool", func() error { return w.pass(ctx) })
	return err
}

func (w *WarmPool) pass(ctx context.Context) error {
	started := time.Now()
	defer w.Metrics.reconciled("warmpool", started)
	now := w.now()
	maxAge := w.Config.WarmPoolMaxAge
	if maxAge <= 0 {
		maxAge = time.Hour
	}
	recycled, err := w.Store.RecycleWarmSandboxes(ctx, now.Add(-maxAge))
	if err != nil {
		return err
	}
	counts, err := w.Store.WarmSandboxes(ctx)
	if err != nil {
		return err
	}
	targets := w.Targets()
	created := 0
	for _, key := range slices.Sorted(maps.Keys(targets)) {
		size, region, _ := strings.Cut(key, "/")
		have := counts[key]
		w.Metrics.warmPool(size, region, have)
		for i := have; i < targets[key]; i++ {
			record := Record{
				ID: NewID(now), Kind: KindWarm, Desired: DesiredRunning, Phase: PhasePending,
				Image: w.Config.Image, Size: size, Region: region, CreatedAt: now,
				DeadlineAt: now.Add(DefaultDeadline(w.Config, KindWarm)),
			}
			if err := w.Store.CreateWarmSandbox(ctx, record); err != nil {
				return err
			}
			created++
		}
	}
	if created > 0 || recycled > 0 {
		w.logger().Info("coding warm pool updated", "created", created, "recycled", recycled)
		if w.Wake != nil {
			w.Wake()
		}
	}
	return nil
}
