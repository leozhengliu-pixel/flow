package coding

// Snapshot reconciler (docs/coding-sessions-design.md §5 loop 3, §10). Owner: track K.
//
// It drives coding_environment_snapshots rows:
//
//	queued ─► building (a snapshot sandbox runs mise install + the prepare script,
//	   │          then archives /workspace or exits for a VolumeSnapshot)
//	   │        ├─ runner reports ready ─► SnapshotStrategy.Finish ─► ready
//	   │        ├─ runner reports failed / sandbox lost ─► retry (new sandbox) or failed
//	   │        └─ timeout (FLOW_CODING_SNAPSHOT_TIMEOUT) ─► failed
//	   └─ cancel ─► canceled          (cancel while building also stops the sandbox)
//	ready ─► expired (TTL) │ superseded (retention keeps FLOW_CODING_SNAPSHOT_RETAIN)
//
// Rows are leased (lease_owner, lease_until) so any number of Flow replicas can
// run the loop; lease_until doubles as the next due time. Each step is
// idempotent: the build sandbox id is stored before it is launched and
// launching an existing id is a no-op, Finish and Delete are idempotent, and
// every transition is saved under the lease. A Flow restart resumes from the
// rows alone.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"flow/api/internal/codingenv"
	"flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
)

// SnapshotJob is a snapshot row with the reconciler's build state.
type SnapshotJob = codingenv.SnapshotJob

// StaleEnvironment is an auto-refresh candidate.
type StaleEnvironment = codingenv.StaleEnvironment

// ErrInvalidEnvironment wraps every environment validation failure.
var ErrInvalidEnvironment = codingenv.ErrInvalidEnvironment

func decodePlan(job SnapshotJob) *sandbox.BuildPlan {
	if len(job.Plan) == 0 {
		return nil
	}
	var plan sandbox.BuildPlan
	if json.Unmarshal(job.Plan, &plan) != nil {
		return nil
	}
	return &plan
}

// SnapshotStore is what the reconciler persists (implemented by
// *store.SQLiteStore in api/internal/store/coding_environments.go).
type SnapshotStore interface {
	CodingEnvironment(ctx context.Context, workspaceKey, id string) (domain.CodingEnvironment, error)
	CodingEnvironmentSecretNames(ctx context.Context, workspaceKey, environmentID string) ([]string, error)
	CreateCodingSnapshot(ctx context.Context, workspaceKey string, snapshot domain.CodingEnvironmentSnapshot) error
	ActiveCodingSnapshots(ctx context.Context, workspaceKey, environmentID string) ([]domain.CodingEnvironmentSnapshot, error)
	SupersedeQueuedCodingSnapshots(ctx context.Context, workspaceKey, environmentID, exceptID string, now time.Time) (int, error)
	LeaseCodingSnapshotJobs(ctx context.Context, owner string, now time.Time, lease time.Duration, limit int) ([]SnapshotJob, error)
	// SaveCodingSnapshotJob writes job under owner's lease and makes the row
	// due again at next. A runner Result stored meanwhile is kept unless the
	// job consumed it (job.Result == nil and the stored one is older).
	SaveCodingSnapshotJob(ctx context.Context, owner string, job SnapshotJob, next time.Time) error
	PrunableCodingSnapshots(ctx context.Context, now time.Time, retain, limit int) ([]SnapshotJob, error)
	MarkCodingSnapshotPruned(ctx context.Context, workspaceKey, id, status string, now time.Time) error
	StaleCodingEnvironments(ctx context.Context, readyBefore time.Time, limit int) ([]StaleEnvironment, error)
	TrimCodingSnapshotHistory(ctx context.Context, keep int) (int, error)
}

// SnapshotReconciler converges snapshot rows.
type SnapshotReconciler struct {
	Store    SnapshotStore
	Launcher SandboxLauncher
	// Provider creates build volumes for strategies that need one (volumesnapshot).
	Provider sandbox.Provider
	Strategy sandbox.SnapshotStrategy
	Config   config.CodingConfig
	// BranchHead returns the head SHA of the environment's default branch
	// (auto-refresh); nil disables auto-refresh.
	BranchHead func(ctx context.Context, environment domain.CodingEnvironment) (string, error)
	Owner      string
	Now        func() time.Time
	Logger     *slog.Logger
	// PollInterval is how often a building snapshot is observed (default 5s).
	PollInterval time.Duration
	// CompleteGrace is how long an exited build sandbox may wait for its
	// complete call before the build fails (default 30s).
	CompleteGrace time.Duration
	// Lease is the per-row lease (default 30s).
	Lease time.Duration
}

const (
	maxSnapshotStartFailures  = 5
	maxSnapshotFinishFailures = 5
	snapshotHistoryKeep       = 20
)

func (r *SnapshotReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *SnapshotReconciler) logger() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

func (r *SnapshotReconciler) poll() time.Duration {
	if r.PollInterval > 0 {
		return r.PollInterval
	}
	return 5 * time.Second
}

func (r *SnapshotReconciler) lease() time.Duration {
	if r.Lease > 0 {
		return r.Lease
	}
	return 30 * time.Second
}

func (r *SnapshotReconciler) grace() time.Duration {
	if r.CompleteGrace > 0 {
		return r.CompleteGrace
	}
	return 30 * time.Second
}

func (r *SnapshotReconciler) maxAttempts() int {
	if r.Config.Retries.Snapshot < 0 {
		return 1
	}
	return 1 + r.Config.Retries.Snapshot
}

// Run reconciles until ctx ends: due rows every FLOW_CODING_RECONCILE_INTERVAL,
// maintenance (TTL, retention, auto-refresh) every minute.
func (r *SnapshotReconciler) Run(ctx context.Context) {
	interval := r.Config.ReconcileInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	lastMaintenance := time.Time{}
	for {
		if err := r.Tick(ctx); err != nil && ctx.Err() == nil {
			r.logger().Warn("coding snapshot reconcile", "error", err)
		}
		if now := r.now(); now.Sub(lastMaintenance) >= time.Minute {
			lastMaintenance = now
			if err := r.Maintain(ctx); err != nil && ctx.Err() == nil {
				r.logger().Warn("coding snapshot maintenance", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Tick leases due rows and advances each one step.
func (r *SnapshotReconciler) Tick(ctx context.Context) error {
	jobs, err := r.Store.LeaseCodingSnapshotJobs(ctx, r.Owner, r.now(), r.lease(), 20)
	if err != nil {
		return err
	}
	var errs []error
	for _, job := range jobs {
		stepCtx, cancel := context.WithTimeout(ctx, r.lease()-time.Second)
		next, delay := r.Step(stepCtx, job)
		cancel()
		if err := r.Store.SaveCodingSnapshotJob(ctx, r.Owner, next, r.now().Add(delay)); err != nil {
			errs = append(errs, fmt.Errorf("snapshot %s: %w", job.Snapshot.ID, err))
		}
	}
	return errors.Join(errs...)
}

// Step advances one job. It never returns an error: failures become the
// snapshot's status or a retry delay.
func (r *SnapshotReconciler) Step(ctx context.Context, job SnapshotJob) (SnapshotJob, time.Duration) {
	switch job.Snapshot.Status {
	case domain.CodingSnapshotQueued:
		return r.stepQueued(ctx, job)
	case domain.CodingSnapshotBuilding:
		return r.stepBuilding(ctx, job)
	default:
		return job, time.Hour // terminal rows are not leased; park defensively
	}
}

func (r *SnapshotReconciler) stepQueued(ctx context.Context, job SnapshotJob) (SnapshotJob, time.Duration) {
	now := r.now()
	if job.CancelRequested {
		return r.finish(job, domain.CodingSnapshotCanceled, "", "", now), 0
	}
	environment, err := r.Store.CodingEnvironment(ctx, job.WorkspaceKey, job.Snapshot.EnvironmentID)
	if errors.Is(err, ErrNotFound) {
		return r.finish(job, domain.CodingSnapshotCanceled, "", "environment no longer exists", now), 0
	}
	if err != nil {
		return job, r.poll()
	}
	if environment.ArchivedAt != nil {
		return r.finish(job, domain.CodingSnapshotCanceled, "", "environment is archived", now), 0
	}
	if r.Strategy == nil || r.Launcher == nil {
		return r.finish(job, domain.CodingSnapshotFailed, domain.CodingErrSandboxDisabled, "coding sandboxes are disabled", now), 0
	}
	plan := decodePlan(job)
	if plan == nil {
		built, err := r.Strategy.PlanBuild(ctx, job.Snapshot.ID, job.WorkspaceKey)
		if err != nil {
			return r.startFailure(job, err, now)
		}
		encoded, err := json.Marshal(built)
		if err != nil {
			return r.startFailure(job, err, now)
		}
		plan, job.Plan = &built, encoded
	}
	if plan.Volume != nil && r.Provider != nil {
		if _, err := r.Provider.EnsureVolume(ctx, *plan.Volume); err != nil {
			return r.startFailure(job, err, now)
		}
	}
	if job.Snapshot.SandboxID == "" {
		job.Snapshot.SandboxID = NewResourceID(now)
		job.Attempts++
		job.Result, job.SucceededAt = nil, nil
	}
	request := LaunchRequest{
		ID: job.Snapshot.SandboxID, WorkspaceKey: job.WorkspaceKey, Kind: sandbox.KindSnapshot, SnapshotID: job.Snapshot.ID,
		EnvironmentID: environment.ID, UserID: job.Snapshot.RequestedBy, Size: environment.SandboxSize,
		Workspace: plan.Workspace, Deadline: r.Config.Snapshot.Timeout,
		Env: map[string]string{sandbox.EnvMode: ModeSnapshot, sandbox.EnvFlowSnapshot: "1"},
	}
	if err := r.Launcher.Launch(ctx, request); err != nil {
		return r.startFailure(job, err, now)
	}
	job.StartFailures = 0
	job.Snapshot.Status = domain.CodingSnapshotBuilding
	if job.Snapshot.StartedAt == nil {
		job.Snapshot.StartedAt = &now
	}
	r.logger().Info("coding snapshot build started", "snapshot", job.Snapshot.ID, "sandbox", job.Snapshot.SandboxID, "attempt", job.Attempts)
	return job, r.poll()
}

func (r *SnapshotReconciler) startFailure(job SnapshotJob, err error, now time.Time) (SnapshotJob, time.Duration) {
	job.StartFailures++
	if errors.Is(err, sandbox.ErrTransient) && job.StartFailures < maxSnapshotStartFailures {
		r.logger().Warn("coding snapshot start retry", "snapshot", job.Snapshot.ID, "error", err)
		return job, Backoff(job.StartFailures)
	}
	code := domain.CodingErrSandboxStartFailed
	if errors.Is(err, sandbox.ErrDisabled) {
		code = domain.CodingErrSandboxDisabled
	}
	return r.finish(job, domain.CodingSnapshotFailed, code, err.Error(), now), 0
}

func (r *SnapshotReconciler) stepBuilding(ctx context.Context, job SnapshotJob) (SnapshotJob, time.Duration) {
	now := r.now()
	if job.CancelRequested {
		r.release(ctx, job)
		return r.finish(job, domain.CodingSnapshotCanceled, "", "", now), 0
	}
	if job.Result != nil {
		return r.consumeResult(ctx, job, now)
	}
	if job.Snapshot.StartedAt != nil && r.Config.Snapshot.Timeout > 0 && now.Sub(*job.Snapshot.StartedAt) > r.Config.Snapshot.Timeout {
		r.release(ctx, job)
		return r.finish(job, domain.CodingSnapshotFailed, domain.CodingErrSnapshotFailed, fmt.Sprintf("snapshot build timed out after %s", r.Config.Snapshot.Timeout), now), 0
	}
	status, err := r.Launcher.Observe(ctx, job.WorkspaceKey, job.Snapshot.SandboxID)
	if err != nil {
		return job, r.poll()
	}
	switch status.Phase {
	case sandbox.PhaseFailed, sandbox.PhaseMissing:
		message := "the snapshot sandbox stopped unexpectedly"
		if status.Reason != "" {
			message = fmt.Sprintf("the snapshot sandbox failed: %s", status.Reason)
		}
		if status.Message != "" {
			message += ": " + status.Message
		}
		return r.retryOrFail(ctx, job, reasonCode(status.Reason), message, now)
	case sandbox.PhaseSucceeded:
		if job.SucceededAt == nil {
			job.SucceededAt = &now
		}
		if now.Sub(*job.SucceededAt) >= r.grace() {
			return r.retryOrFail(ctx, job, domain.CodingErrSnapshotFailed, "the snapshot runner exited without reporting a result", now)
		}
	}
	return job, r.poll()
}

func (r *SnapshotReconciler) consumeResult(ctx context.Context, job SnapshotJob, now time.Time) (SnapshotJob, time.Duration) {
	result := *job.Result
	if result.Status != domain.CodingSnapshotReady {
		code := result.ErrorCode
		if code == "" {
			code = domain.CodingErrEnvironmentSetup
		}
		message := result.Error
		if message == "" {
			message = "environment setup failed"
		}
		// A failing prepare script fails the same way again: no retry.
		if code == domain.CodingErrEnvironmentSetup {
			r.release(ctx, job)
			return r.finish(job, domain.CodingSnapshotFailed, code, message, now), 0
		}
		return r.retryOrFail(ctx, job, code, message, now)
	}
	plan := decodePlan(job)
	storageRef, size, err := r.Strategy.Finish(ctx, job.Snapshot.ID, planOrEmpty(plan))
	if err != nil {
		job.FinishFailures++
		if errors.Is(err, sandbox.ErrTransient) && job.FinishFailures < maxSnapshotFinishFailures {
			return job, Backoff(job.FinishFailures)
		}
		r.release(ctx, job)
		return r.finish(job, domain.CodingSnapshotFailed, domain.CodingErrSnapshotFailed, "storing the snapshot failed: "+err.Error(), now), 0
	}
	r.release(ctx, job)
	if plan != nil && plan.Volume != nil && r.Provider != nil {
		// The snapshot holds the data now; the build volume can go.
		if err := r.Provider.DeleteVolume(ctx, plan.Volume.ID); err != nil && !errors.Is(err, sandbox.ErrNotFound) {
			r.logger().Warn("coding snapshot build volume cleanup", "snapshot", job.Snapshot.ID, "error", err)
		}
	}
	job.Snapshot.StorageRef = storageRef
	if size <= 0 {
		size = result.SizeBytes
	}
	job.Snapshot.SizeBytes = size
	if result.BaseSHA != "" {
		job.Snapshot.BaseSHA = result.BaseSHA
	}
	job = r.finish(job, domain.CodingSnapshotReady, "", "", now)
	if r.Config.Snapshot.TTL > 0 {
		expires := now.Add(r.Config.Snapshot.TTL)
		job.Snapshot.ExpiresAt = &expires
	}
	r.logger().Info("coding snapshot ready", "snapshot", job.Snapshot.ID, "bytes", size)
	return job, 0
}

func (r *SnapshotReconciler) retryOrFail(ctx context.Context, job SnapshotJob, code domain.CodingErrorCode, message string, now time.Time) (SnapshotJob, time.Duration) {
	r.release(ctx, job)
	if job.Attempts < r.maxAttempts() {
		r.logger().Warn("coding snapshot build retry", "snapshot", job.Snapshot.ID, "attempt", job.Attempts, "error", message)
		job.Snapshot.Status = domain.CodingSnapshotQueued
		job.Snapshot.SandboxID = ""
		job.Result, job.SucceededAt = nil, nil
		return job, Backoff(job.Attempts)
	}
	return r.finish(job, domain.CodingSnapshotFailed, code, message, now), 0
}

func (r *SnapshotReconciler) release(ctx context.Context, job SnapshotJob) {
	if job.Snapshot.SandboxID == "" || r.Launcher == nil {
		return
	}
	if err := r.Launcher.Release(ctx, job.WorkspaceKey, job.Snapshot.SandboxID); err != nil {
		// GC removes it if this keeps failing; the build outcome stands.
		r.logger().Warn("coding snapshot sandbox release", "snapshot", job.Snapshot.ID, "sandbox", job.Snapshot.SandboxID, "error", err)
	}
}

func (r *SnapshotReconciler) finish(job SnapshotJob, status string, code domain.CodingErrorCode, message string, now time.Time) SnapshotJob {
	job.Snapshot.Status = status
	job.Snapshot.ErrorCode = code
	job.Snapshot.Error = message
	job.Snapshot.FinishedAt = &now
	job.Result, job.SucceededAt = nil, nil
	if plan := decodePlan(job); status != domain.CodingSnapshotReady && plan != nil && plan.Volume != nil && r.Provider != nil {
		if err := r.Provider.DeleteVolume(context.Background(), plan.Volume.ID); err != nil && !errors.Is(err, sandbox.ErrNotFound) {
			r.logger().Warn("coding snapshot build volume cleanup", "snapshot", job.Snapshot.ID, "error", err)
		}
	}
	return job
}

func planOrEmpty(plan *sandbox.BuildPlan) sandbox.BuildPlan {
	if plan == nil {
		return sandbox.BuildPlan{}
	}
	return *plan
}

func reasonCode(reason sandbox.Reason) domain.CodingErrorCode {
	switch reason {
	case sandbox.ReasonImagePull:
		return domain.CodingErrImagePullFailed
	case sandbox.ReasonUnschedulable, sandbox.ReasonQuota:
		return domain.CodingErrUnschedulable
	case sandbox.ReasonInitFailed, sandbox.ReasonExitNonZero:
		return domain.CodingErrSnapshotFailed
	default:
		return domain.CodingErrSnapshotFailed
	}
}

// SnapshotBaseRef is the base ref recorded on a snapshot ("HEAD" = the
// repository's default branch when the environment does not name one).
func SnapshotBaseRef(environment domain.CodingEnvironment) string {
	if branch := strings.TrimSpace(environment.DefaultBranch); branch != "" {
		return branch
	}
	return "HEAD"
}

// ErrSnapshotsUnavailable reports a refresh while sandboxes are disabled.
var ErrSnapshotsUnavailable = errors.New("coding: snapshots need FLOW_CODING_ENABLED and a sandbox provider")

// Request queues a snapshot build of environment ("Refresh", a config change,
// auto-refresh, or a session that ran setup inline). A queued or building
// snapshot with the same config hash is returned instead of a new one
// (created=false); other queued builds are superseded.
func (r *SnapshotReconciler) Request(ctx context.Context, environment domain.CodingEnvironment, requestedBy string) (domain.CodingEnvironmentSnapshot, bool, error) {
	if r.Strategy == nil || r.Launcher == nil {
		return domain.CodingEnvironmentSnapshot{}, false, ErrSnapshotsUnavailable
	}
	if environment.ArchivedAt != nil {
		return domain.CodingEnvironmentSnapshot{}, false, fmt.Errorf("%w: archived environments cannot build snapshots", ErrInvalidEnvironment)
	}
	names, err := r.Store.CodingEnvironmentSecretNames(ctx, environment.WorkspaceKey, environment.ID)
	if err != nil {
		return domain.CodingEnvironmentSnapshot{}, false, err
	}
	baseRef := SnapshotBaseRef(environment)
	hash := SnapshotConfigHash(environment, r.Config.Image, baseRef, names)
	active, err := r.Store.ActiveCodingSnapshots(ctx, environment.WorkspaceKey, environment.ID)
	if err != nil {
		return domain.CodingEnvironmentSnapshot{}, false, err
	}
	for _, existing := range active {
		if existing.ConfigHash == hash {
			return existing, false, nil
		}
	}
	now := r.now()
	snapshot := domain.CodingEnvironmentSnapshot{
		ID: NewResourceID(now), EnvironmentID: environment.ID, Status: domain.CodingSnapshotQueued, Strategy: r.Strategy.Name(),
		ConfigHash: hash, Image: r.Config.Image, BaseRef: baseRef, RequestedBy: requestedBy, CreatedAt: now,
	}
	if err := r.Store.CreateCodingSnapshot(ctx, environment.WorkspaceKey, snapshot); err != nil {
		return domain.CodingEnvironmentSnapshot{}, false, err
	}
	if _, err := r.Store.SupersedeQueuedCodingSnapshots(ctx, environment.WorkspaceKey, environment.ID, snapshot.ID, now); err != nil {
		r.logger().Warn("coding snapshot supersede", "environment", environment.ID, "error", err)
	}
	return snapshot, true, nil
}

// Maintain expires snapshots past their TTL, deletes ready snapshots beyond
// the retention count, and queues refreshes for environments whose default
// branch moved since a snapshot older than the refresh interval.
func (r *SnapshotReconciler) Maintain(ctx context.Context) error {
	var errs []error
	now := r.now()
	if r.Strategy != nil {
		prunable, err := r.Store.PrunableCodingSnapshots(ctx, now, max(r.Config.Snapshot.Retain, 1), 50)
		if err != nil {
			errs = append(errs, err)
		}
		for _, job := range prunable {
			if job.Snapshot.StorageRef != "" {
				if err := r.Strategy.Delete(ctx, job.Snapshot.StorageRef); err != nil && !errors.Is(err, sandbox.ErrNotFound) {
					errs = append(errs, fmt.Errorf("delete snapshot %s: %w", job.Snapshot.ID, err))
					continue
				}
			}
			status := domain.CodingSnapshotSuperseded
			if job.Snapshot.ExpiresAt != nil && !job.Snapshot.ExpiresAt.After(now) {
				status = domain.CodingSnapshotExpired
			}
			if err := r.Store.MarkCodingSnapshotPruned(ctx, job.WorkspaceKey, job.Snapshot.ID, status, now); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if _, err := r.Store.TrimCodingSnapshotHistory(ctx, snapshotHistoryKeep); err != nil {
		errs = append(errs, err)
	}
	if r.BranchHead != nil && r.Config.Snapshot.RefreshInterval > 0 && r.Strategy != nil {
		stale, err := r.Store.StaleCodingEnvironments(ctx, now.Add(-r.Config.Snapshot.RefreshInterval), 50)
		if err != nil {
			errs = append(errs, err)
		}
		for _, item := range stale {
			head, err := r.BranchHead(ctx, item.Environment)
			if err != nil || head == "" || head == item.Latest.BaseSHA {
				continue // unknown or unchanged: keep the snapshot
			}
			if _, _, err := r.Request(ctx, item.Environment, item.Latest.RequestedBy); err != nil {
				errs = append(errs, fmt.Errorf("refresh environment %s: %w", item.Environment.ID, err))
			}
		}
	}
	return errors.Join(errs...)
}

// SnapshotBootstrap is the runner bootstrap of a snapshot sandbox (mode
// snapshot): the repository, the environment's non-secret setup and where to
// upload the archive. The session core (track B) answers GET /bootstrap with
// it for sandboxes whose grant has a SnapshotID and no SessionID.
func SnapshotBootstrap(c config.CodingConfig, environment domain.CodingEnvironment, snapshot domain.CodingEnvironmentSnapshot, sandboxID, cloneURL string, plan sandbox.BuildPlan, hasSecrets bool) BootstrapResponse {
	return BootstrapResponse{
		SandboxID: sandboxID, SnapshotID: snapshot.ID, Mode: ModeSnapshot,
		Repo:        BootstrapRepo{CodeHost: environment.CodeHost, HostName: environment.HostName, FullName: environment.Repository, CloneURL: cloneURL, BaseRef: snapshot.BaseRef},
		Environment: environmentBootstrap(environment, hasSecrets),
		Snapshot:    &BootstrapSnapshot{ID: snapshot.ID, Strategy: snapshot.Strategy, UploadURL: plan.UploadURL},
		Limits:      BootstrapLimits{SetupTimeoutSeconds: int(c.Snapshot.Timeout.Seconds()), HeartbeatSeconds: 15, StopGraceSeconds: int(c.Timeouts.StopGrace.Seconds())},
	}
}

// DebugBootstrap is the runner bootstrap of a debug-terminal sandbox (mode
// debug). Setup secrets are never delivered to it.
func DebugBootstrap(c config.CodingConfig, environment domain.CodingEnvironment, sandboxID, cloneURL string, restored bool) BootstrapResponse {
	return BootstrapResponse{
		SandboxID: sandboxID, Mode: ModeDebug, Restored: restored,
		Repo:        BootstrapRepo{CodeHost: environment.CodeHost, HostName: environment.HostName, FullName: environment.Repository, CloneURL: cloneURL, BaseRef: SnapshotBaseRef(environment)},
		Environment: environmentBootstrap(environment, false),
		Limits:      BootstrapLimits{HeartbeatSeconds: 15, StopGraceSeconds: int(c.Timeouts.StopGrace.Seconds())},
	}
}

func environmentBootstrap(environment domain.CodingEnvironment, hasSecrets bool) BootstrapEnv {
	return BootstrapEnv{EnvironmentID: environment.ID, Mise: environment.Mise, PrepareScript: environment.PrepareScript, DependencyManagement: DependencyMode(environment.DependencyManagement), HasSecrets: hasSecrets}
}

// SnapshotLookupStore is the part of EnvironmentStore StoreSnapshotResolver reads.
type SnapshotLookupStore interface {
	CodingEnvironmentSecretNames(ctx context.Context, workspaceKey, environmentID string) ([]string, error)
	LatestReadyCodingSnapshot(ctx context.Context, workspaceKey, environmentID, configHash string) (domain.CodingEnvironmentSnapshot, error)
}

// StoreSnapshotResolver is the session core's SnapshotResolver (track B's
// Options.Snapshots): the newest ready, unexpired snapshot whose config hash
// matches the environment as it is now (the same hash Request builds with).
// A session whose environment changed since the last build runs setup inline.
type StoreSnapshotResolver struct {
	Store SnapshotLookupStore
	// Image is FLOW_CODING_IMAGE (part of the config hash).
	Image string
}

var _ SnapshotResolver = StoreSnapshotResolver{}

func (r StoreSnapshotResolver) ResolveCodingSnapshot(ctx context.Context, workspaceKey string, environment domain.CodingEnvironment) (domain.CodingEnvironmentSnapshot, bool, error) {
	if r.Store == nil || environment.ID == "" {
		return domain.CodingEnvironmentSnapshot{}, false, nil
	}
	names, err := r.Store.CodingEnvironmentSecretNames(ctx, workspaceKey, environment.ID)
	if err != nil {
		return domain.CodingEnvironmentSnapshot{}, false, err
	}
	hash := SnapshotConfigHash(environment, r.Image, SnapshotBaseRef(environment), names)
	snapshot, err := r.Store.LatestReadyCodingSnapshot(ctx, workspaceKey, environment.ID, hash)
	if errors.Is(err, ErrNotFound) {
		return domain.CodingEnvironmentSnapshot{}, false, nil
	}
	if err != nil {
		return domain.CodingEnvironmentSnapshot{}, false, err
	}
	return snapshot, snapshot.StorageRef != "" || snapshot.Strategy != sandbox.StrategyVolumeSnapshot, nil
}
