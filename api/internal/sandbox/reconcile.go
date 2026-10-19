package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"time"

	"flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/objectstore"
)

// ReconcilerStore is what the sandbox reconciler persists through, beyond the
// frozen Store contract (track A additions; *store.SQLiteStore implements it).
type ReconcilerStore interface {
	Store
	// SaveSandboxObservationAt stores an observation, releases the lease and
	// schedules the next reconcile of the row at next (CAS on the lease).
	SaveSandboxObservationAt(ctx context.Context, owner, id string, status SandboxStatus, attempts int, next time.Time) error
	// SetSandboxLogsKey records the archived runner log (CAS on the lease).
	SetSandboxLogsKey(ctx context.Context, owner, id, key string) error
	// AddSandboxBilledSeconds advances billed_seconds from → to (CAS on the
	// old value) and reports whether this caller advanced it.
	AddSandboxBilledSeconds(ctx context.Context, owner, id string, from, to int64) (bool, error)
}

// UsageRecorder writes metered usage (coding.UsageStore, track B).
type UsageRecorder interface {
	RecordCodingUsage(ctx context.Context, record domain.CodingUsageRecord) error
}

// TokenRevoker revokes a sandbox's tokens when it stops (coding.SessionStore,
// track B); used when the store implements it.
type TokenRevoker interface {
	RevokeSandboxTokens(ctx context.Context, sandboxID string) error
}

// Reconciler timing (design §5). The per-phase start budget mirrors
// coding.PhasePolicies(starting): FLOW_CODING_START_TIMEOUT and
// FLOW_CODING_START_RETRIES (the sandbox package cannot import coding).
const (
	ReconcileLease     = 30 * time.Second
	ReconcileBatch     = 50
	ObserveInterval    = 15 * time.Second
	TerminalInterval   = time.Minute
	BillingIncrement   = 5 * time.Minute
	operationTimeout   = 45 * time.Second
	maxArchivedLogSize = 8 << 20
	logsPrefix         = "coding-logs"
)

// Backoff is the retry delay after attempt n (0-based): 2s·2ⁿ, at most 1 min
// (the same curve as coding.Backoff).
func Backoff(attempt int) time.Duration {
	delay := 2 * time.Second
	for i := 0; i < attempt && delay < time.Minute; i++ {
		delay *= 2
	}
	return min(delay, time.Minute)
}

// Reconciler converges coding_sandboxes rows to the provider: it starts
// sandboxes whose desired state is running (minting a fresh token right
// before StartSandbox), observes them, enforces deadlines and the start
// budget, stops sandboxes whose desired state is stopped, archives their logs
// and meters their runtime. It holds no state between passes: everything is
// read from leased rows, so any number of replicas may run it and a restart
// resumes from the database (design §5).
type Reconciler struct {
	Store    ReconcilerStore
	Provider Provider
	Config   config.CodingConfig
	// Owner identifies this process in leases.
	Owner string
	// Usage meters runtime (optional).
	Usage UsageRecorder
	// Logs archives runner logs on stop (optional).
	Logs    objectstore.Store
	Metrics *Metrics
	Logger  *slog.Logger
	Now     func() time.Time

	wakeOnce sync.Once
	wake     chan struct{}
}

func (r *Reconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Reconciler) logger() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

func (r *Reconciler) wakeChannel() chan struct{} {
	r.wakeOnce.Do(func() { r.wake = make(chan struct{}, 1) })
	return r.wake
}

// Wake runs a pass soon (an optimization; the interval is the guarantee).
func (r *Reconciler) Wake() {
	select {
	case r.wakeChannel() <- struct{}{}:
	default:
	}
}

// Run reconciles every FLOW_CODING_RECONCILE_INTERVAL until ctx ends.
func (r *Reconciler) Run(ctx context.Context) {
	interval := r.Config.ReconcileInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := r.RunOnce(ctx); err != nil && ctx.Err() == nil {
			r.logger().Warn("coding sandbox reconcile failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-r.wakeChannel():
		}
	}
}

// RunOnce leases due rows and reconciles each; it returns how many rows it
// processed.
func (r *Reconciler) RunOnce(ctx context.Context) (int, error) {
	started := time.Now()
	defer r.Metrics.reconciled("sandbox", started)
	records, err := r.Store.LeaseSandboxes(ctx, r.Owner, ReconcileLease, ReconcileBatch)
	if err != nil {
		return 0, err
	}
	for _, record := range records {
		if ctx.Err() != nil {
			break
		}
		r.reconcileOne(ctx, record)
	}
	return len(records), nil
}

// outcome is the result of one reconcile step.
type outcome struct {
	status   SandboxStatus
	attempts int
	next     time.Duration
}

func (r *Reconciler) reconcileOne(ctx context.Context, record Record) {
	stepCtx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	log := r.logger().With("sandbox_id", record.ID, "session_id", record.SessionID, "kind", record.Kind, "desired", record.Desired, "phase", record.Phase)
	var result outcome
	if record.Desired == DesiredStopped {
		result = r.stop(stepCtx, record, log)
	} else {
		result = r.converge(stepCtx, record, log)
	}
	r.bill(stepCtx, record, result.status, log)
	if result.status.Phase == PhaseRunning && record.Phase != PhaseRunning && result.status.StartedAt != nil && !record.CreatedAt.IsZero() {
		r.Metrics.sandboxStarted(record.Kind, record.Size, record.Kind == KindWarm, result.status.StartedAt.Sub(record.CreatedAt).Seconds())
	}
	if result.status.Phase != record.Phase {
		log.Info("coding sandbox phase changed", "to", result.status.Phase, "reason", result.status.Reason)
	}
	next := r.now().Add(result.next)
	// The observation is saved with a fresh context: a step that hit its
	// timeout must still release the row.
	saveCtx, saveCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer saveCancel()
	if err := r.Store.SaveSandboxObservationAt(saveCtx, r.Owner, record.ID, result.status, result.attempts, next); err != nil {
		log.Warn("coding sandbox observation not saved", "error", err)
	}
}

// keep carries the recorded failure into an observation that has none.
func keep(record Record, status SandboxStatus) SandboxStatus {
	if status.Reason == ReasonNone {
		status.Reason, status.Message = record.Reason, record.Message
	}
	if status.ProviderRef == "" {
		status.ProviderRef = record.ProviderRef
	}
	if status.ID == "" {
		status.ID = record.ID
	}
	return status
}

func recorded(record Record) SandboxStatus {
	return SandboxStatus{ID: record.ID, Kind: record.Kind, Phase: record.Phase, Reason: record.Reason, Message: record.Message, ProviderRef: record.ProviderRef, Region: record.Region, StartedAt: record.StartedAt, FinishedAt: record.EndedAt}
}

// converge drives a desired=running row.
func (r *Reconciler) converge(ctx context.Context, record Record, log *slog.Logger) outcome {
	now := r.now()
	if record.Phase == PhaseSucceeded || record.Phase == PhaseFailed || record.Phase == PhaseMissing {
		// Terminal for this sandbox id: the session layer decides what next
		// (a retry uses a new id). Only refresh the observation slowly.
		status, err := r.Provider.Status(ctx, record.ID)
		if err != nil || status.Phase == PhaseMissing {
			return outcome{status: recorded(record), attempts: record.Attempts, next: TerminalInterval}
		}
		return outcome{status: keep(record, status), attempts: record.Attempts, next: TerminalInterval}
	}
	if !record.DeadlineAt.IsZero() && now.After(record.DeadlineAt) {
		log.Info("coding sandbox deadline passed; stopping")
		if err := r.Provider.StopSandbox(ctx, record.ID, StopOptions{Grace: r.Config.Timeouts.StopGrace, KeepVolume: record.KeepVolume}); err != nil {
			r.Metrics.providerError("stop", err)
			return outcome{status: recorded(record), attempts: record.Attempts, next: Backoff(0)}
		}
		failed := recorded(record)
		failed.Phase, failed.Reason, failed.Message = PhaseFailed, ReasonDeadlineExceeded, "sandbox deadline exceeded"
		failed.FinishedAt = &now
		return outcome{status: failed, attempts: record.Attempts, next: TerminalInterval}
	}
	status, err := r.Provider.Status(ctx, record.ID)
	if err != nil {
		r.Metrics.providerError("status", err)
		log.Warn("coding sandbox status failed", "error", err)
		return outcome{status: recorded(record), attempts: record.Attempts, next: Backoff(1)}
	}
	if status.Phase != PhaseMissing {
		// Exists (possibly created by a previous process that crashed
		// before saving): adopt it.
		status = r.startBudget(ctx, record, status, now, log)
		return outcome{status: status, attempts: record.Attempts, next: r.observeDelay(status)}
	}
	if record.StartedAt != nil || record.Phase == PhaseRunning {
		// It ran and is gone (evicted, deleted, node lost): sandboxLost.
		lost := recorded(record)
		lost.Phase, lost.Message = PhaseMissing, "sandbox disappeared from the provider"
		if lost.Reason == ReasonNone {
			lost.Reason = ReasonEvicted
		}
		lost.FinishedAt = &now
		return outcome{status: lost, attempts: record.Attempts, next: TerminalInterval}
	}
	return r.start(ctx, record, now, log)
}

// startBudget fails a sandbox that has not reached running within
// FLOW_CODING_START_TIMEOUT, stopping it so it does not linger.
func (r *Reconciler) startBudget(ctx context.Context, record Record, status SandboxStatus, now time.Time, log *slog.Logger) SandboxStatus {
	if status.Phase != PhasePending && status.Phase != PhaseStarting {
		return status
	}
	budget := r.Config.Timeouts.Start
	if budget <= 0 || record.CreatedAt.IsZero() || now.Sub(record.CreatedAt) <= budget || record.Kind == KindWarm && now.Sub(record.CreatedAt) <= 2*budget {
		return status
	}
	log.Warn("coding sandbox did not start in time; stopping", "reason", status.Reason)
	if err := r.Provider.StopSandbox(ctx, record.ID, StopOptions{Grace: time.Second, KeepVolume: record.KeepVolume}); err != nil {
		r.Metrics.providerError("stop", err)
	}
	status.Phase = PhaseFailed
	if status.Reason == ReasonNone {
		status.Reason = ReasonStartFailed
	}
	status.Message = fmt.Sprintf("sandbox did not start within %s: %s", budget, status.Message)
	status.FinishedAt = &now
	return status
}

func (r *Reconciler) observeDelay(status SandboxStatus) time.Duration {
	switch status.Phase {
	case PhasePending, PhaseStarting:
		if interval := r.Config.ReconcileInterval; interval > 0 {
			return interval
		}
		return 5 * time.Second
	case PhaseRunning:
		return ObserveInterval
	default:
		return TerminalInterval
	}
}

// start mints a token and starts the sandbox, within the start budget.
func (r *Reconciler) start(ctx context.Context, record Record, now time.Time, log *slog.Logger) outcome {
	pending := recorded(record)
	pending.Phase = PhasePending
	budget := r.Config.Timeouts.Start
	if budget > 0 && !record.CreatedAt.IsZero() && now.Sub(record.CreatedAt) > budget {
		pending.Phase, pending.FinishedAt = PhaseFailed, &now
		if pending.Reason == ReasonNone {
			pending.Reason = ReasonStartFailed
		}
		pending.Message = fmt.Sprintf("sandbox did not start within %s: %s", budget, record.Message)
		return outcome{status: pending, attempts: record.Attempts, next: TerminalInterval}
	}
	spec := r.Spec(record, now)
	ttl := spec.Deadline + r.Config.Timeouts.TokenGrace
	token, err := r.Store.MintSandboxToken(ctx, record, ttl)
	if err != nil {
		log.Warn("coding sandbox token mint failed", "error", err)
		pending.Message = "token mint failed: " + err.Error()
		return outcome{status: pending, attempts: record.Attempts, next: Backoff(record.Attempts)}
	}
	spec.Token = token
	status, err := r.Provider.StartSandbox(ctx, spec)
	if err == nil {
		status.Message = ""
		return outcome{status: keep(Record{ID: record.ID}, status), attempts: record.Attempts + 1, next: r.observeDelay(status)}
	}
	r.Metrics.providerError("start", err)
	log.Warn("coding sandbox start failed", "error", err, "attempt", record.Attempts+1)
	pending.Message = err.Error()
	switch {
	case errors.Is(err, ErrQuota):
		// Wait for capacity without spending attempts (bounded by the
		// start budget above).
		pending.Reason = ReasonQuota
		return outcome{status: pending, attempts: record.Attempts, next: Backoff(3)}
	case IsTransient(err) || errors.Is(err, ErrImagePull):
		attempts := record.Attempts + 1
		if errors.Is(err, ErrImagePull) {
			pending.Reason = ReasonImagePull
		}
		if attempts >= max(r.Config.Retries.Start, 0)+1 { // one try plus Retries.Start retries
			pending.Phase, pending.FinishedAt = PhaseFailed, &now
			if pending.Reason == ReasonNone {
				pending.Reason = ReasonStartFailed
			}
			return outcome{status: pending, attempts: attempts, next: TerminalInterval}
		}
		return outcome{status: pending, attempts: attempts, next: Backoff(attempts - 1)}
	default:
		pending.Phase, pending.Reason, pending.FinishedAt = PhaseFailed, ReasonStartFailed, &now
		return outcome{status: pending, attempts: record.Attempts + 1, next: TerminalInterval}
	}
}

// Spec builds the deterministic sandbox spec of a record (without a token).
func (r *Reconciler) Spec(record Record, now time.Time) SandboxSpec {
	spec := SandboxSpec{
		ID: record.ID, Kind: record.Kind, WorkspaceKey: record.WorkspaceKey, SessionID: record.SessionID, SnapshotID: record.SnapshotID,
		Image: record.Image, Resources: ResourcesFor(r.Config, record.Size), Region: record.Region, Env: record.Env, Workspace: record.Workspace,
		ProxyURL: r.Config.Egress.ProxyURL,
	}
	if !record.DeadlineAt.IsZero() {
		spec.Deadline = max(record.DeadlineAt.Sub(now), time.Minute)
	}
	return CompleteSpec(r.Config, spec)
}

// stop drives a desired=stopped row to PhaseMissing.
func (r *Reconciler) stop(ctx context.Context, record Record, log *slog.Logger) outcome {
	now := r.now()
	if record.ProviderRef != "" || record.Attempts > 0 {
		r.archiveLogs(ctx, record, log)
	}
	if err := r.Provider.StopSandbox(ctx, record.ID, StopOptions{Grace: r.Config.Timeouts.StopGrace, KeepVolume: record.KeepVolume}); err != nil {
		r.Metrics.providerError("stop", err)
		log.Warn("coding sandbox stop failed", "error", err)
		return outcome{status: recorded(record), attempts: record.Attempts, next: Backoff(1)}
	}
	if revoker, ok := r.Store.(TokenRevoker); ok {
		if err := revoker.RevokeSandboxTokens(ctx, record.ID); err != nil {
			log.Warn("coding sandbox token revoke failed", "error", err)
		}
	}
	status, err := r.Provider.Status(ctx, record.ID)
	if err != nil {
		r.Metrics.providerError("status", err)
		return outcome{status: recorded(record), attempts: record.Attempts, next: Backoff(1)}
	}
	if status.Phase != PhaseMissing {
		// Still terminating (grace period); look again soon.
		status = keep(record, status)
		return outcome{status: status, attempts: record.Attempts, next: r.observeDelay(SandboxStatus{Phase: PhasePending})}
	}
	gone := recorded(record)
	gone.Phase = PhaseMissing
	if gone.FinishedAt == nil {
		gone.FinishedAt = &now
	}
	return outcome{status: gone, attempts: record.Attempts, next: TerminalInterval}
}

// archiveLogs copies the runner log to the object store once (best effort).
func (r *Reconciler) archiveLogs(ctx context.Context, record Record, log *slog.Logger) {
	if r.Logs == nil || record.LogsKey != "" {
		return
	}
	stream, err := r.Provider.Logs(ctx, record.ID, LogOptions{})
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			log.Warn("coding sandbox logs unavailable", "error", err)
		}
		return
	}
	defer stream.Close()
	key := LogsKey(record.WorkspaceKey, record.ID)
	if _, err := r.Logs.Put(ctx, key, io.LimitReader(stream, maxArchivedLogSize), "text/plain; charset=utf-8"); err != nil {
		log.Warn("coding sandbox logs not archived", "error", err)
		return
	}
	if err := r.Store.SetSandboxLogsKey(ctx, r.Owner, record.ID, key); err != nil {
		log.Warn("coding sandbox logs key not saved", "error", err)
	}
}

// LogsKey is the object key of a sandbox's archived runner log.
func LogsKey(workspaceKey, sandboxID string) string {
	return FlatObjectKey(logsPrefix, logsPrefix, WorkspaceLabel(workspaceKey), sandboxID) + ".log"
}

// bill meters runtime in 5-minute increments while running and the exact
// remainder once the sandbox ended. billed_seconds is advanced with a CAS
// before the usage row is written, so a crash can under-bill one increment
// but never double-bill. Warm sandboxes are not billed (operator cost).
func (r *Reconciler) bill(ctx context.Context, record Record, status SandboxStatus, log *slog.Logger) {
	if r.Usage == nil || record.Kind == KindWarm {
		return
	}
	startedAt := status.StartedAt
	if startedAt == nil {
		startedAt = record.StartedAt
	}
	if startedAt == nil {
		return
	}
	end := r.now()
	final := status.Phase.Terminal() || record.Desired == DesiredStopped
	if status.FinishedAt != nil && status.FinishedAt.Before(end) {
		end = *status.FinishedAt
	}
	elapsed := end.Sub(*startedAt)
	if elapsed <= 0 {
		return
	}
	due := int64(elapsed/BillingIncrement) * int64(BillingIncrement/time.Second)
	if final {
		due = int64(math.Ceil(elapsed.Seconds()))
	}
	if due <= record.BilledSeconds {
		return
	}
	advanced, err := r.Store.AddSandboxBilledSeconds(ctx, r.Owner, record.ID, record.BilledSeconds, due)
	if err != nil || !advanced {
		if err != nil {
			log.Warn("coding sandbox billing not advanced", "error", err)
		}
		return
	}
	seconds := due - record.BilledSeconds
	price := r.Config.RuntimePricesUSD[record.Size]
	cost := int64(math.Round(price * float64(seconds) / 3600 * 1e6))
	usage := domain.CodingUsageRecord{
		ID: "rt-" + record.ID + "-" + strconv.FormatInt(due, 10), WorkspaceKey: record.WorkspaceKey, SessionID: record.SessionID, SandboxID: record.ID,
		UserID: record.UserID, LoopID: record.LoopID, Kind: "runtime", RuntimeSeconds: seconds, Size: record.Size, CostMicros: cost, CreatedAt: r.now(),
	}
	if err := r.Usage.RecordCodingUsage(ctx, usage); err != nil {
		log.Warn("coding sandbox runtime usage not recorded", "error", err, "seconds", seconds)
		return
	}
	r.Metrics.cost("runtime", cost)
}
