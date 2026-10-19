package coding_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"flow/api/internal/coding"
	"flow/api/internal/codingenv"
	"flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
	"flow/api/internal/store"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// fakeStrategy is an archive-like SnapshotStrategy that records calls.
type fakeStrategy struct {
	mu         sync.Mutex
	finished   []string
	deleted    []string
	finishErrs []error
	volume     bool
}

func (f *fakeStrategy) Name() string { return sandbox.StrategyArchive }
func (f *fakeStrategy) PlanBuild(_ context.Context, snapshotID, _ string) (sandbox.BuildPlan, error) {
	plan := sandbox.BuildPlan{UploadURL: "http://flow/internal/coding-runtime/snapshots/" + snapshotID + "/archive"}
	if f.volume {
		plan.Volume = &sandbox.VolumeSpec{ID: "vol-" + snapshotID}
		plan.Workspace = sandbox.VolumeSource{VolumeID: "vol-" + snapshotID}
	}
	return plan, nil
}
func (f *fakeStrategy) Finish(_ context.Context, snapshotID string, _ sandbox.BuildPlan) (string, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.finishErrs) > 0 {
		err := f.finishErrs[0]
		f.finishErrs = f.finishErrs[1:]
		return "", 0, err
	}
	f.finished = append(f.finished, snapshotID)
	return "archive/" + snapshotID, 1234, nil
}
func (f *fakeStrategy) Restore(_ context.Context, storageRef, _, _ string) (sandbox.VolumeSource, error) {
	return sandbox.VolumeSource{ArchiveURL: "http://flow/" + storageRef}, nil
}
func (f *fakeStrategy) Delete(_ context.Context, storageRef string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, storageRef)
	return nil
}

type snapshotHarness struct {
	t        *testing.T
	store    *store.SQLiteStore
	provider *sandbox.Fake
	strategy *fakeStrategy
	clock    *testClock
	usage    []domain.CodingUsageRecord
	minted   int
	env      domain.CodingEnvironment
	config   config.CodingConfig
}

func newSnapshotHarness(t *testing.T) *snapshotHarness {
	t.Helper()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	clock := &testClock{now: time.Now().Truncate(time.Millisecond)}
	h := &snapshotHarness{t: t, store: repository, provider: sandbox.NewFake(clock.Now), strategy: &fakeStrategy{}, clock: clock}
	h.config = config.CodingConfig{
		Image: "flow-sandbox:test", RuntimeURL: "http://flow", InstanceID: "test",
		Sizes:            map[string]config.SandboxSize{"medium": {CPU: "2", Memory: "16Gi"}},
		RuntimePricesUSD: map[string]float64{"medium": 0.36},
		Retries:          config.CodingRetries{Snapshot: 1},
		Snapshot:         config.CodingSnapshotConfig{Timeout: 30 * time.Minute, TTL: 7 * 24 * time.Hour, Retain: 2, RefreshInterval: 24 * time.Hour},
		Timeouts:         config.CodingTimeouts{TokenGrace: time.Minute, StopGrace: time.Second},
	}
	h.env, err = repository.CreateCodingEnvironment(context.Background(), domain.CodingEnvironment{WorkspaceKey: "ws-snap", Repository: "acme/web", PrepareScript: "npm ci", CreatedBy: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *snapshotHarness) reconciler(owner string) *coding.SnapshotReconciler {
	return &coding.SnapshotReconciler{
		Store: h.store, Provider: h.provider, Strategy: h.strategy, Config: h.config, Owner: owner, Now: h.clock.Now,
		Launcher: coding.ProviderLauncher{Provider: h.provider, Config: h.config, Now: h.clock.Now,
			Mint: func(_ context.Context, record sandbox.Record, ttl time.Duration) (string, error) {
				h.minted++
				if record.Kind != sandbox.KindSnapshot || record.UserID != "u1" || ttl != h.config.Snapshot.Timeout+time.Minute {
					h.t.Errorf("mint record: %+v ttl %s", record, ttl)
				}
				return "fcs_test", nil
			},
			Usage: func(_ context.Context, record domain.CodingUsageRecord) error {
				h.usage = append(h.usage, record)
				return nil
			},
		},
	}
}

// tick advances the clock past any retry delay and runs one reconcile pass.
func (h *snapshotHarness) tick(r *coding.SnapshotReconciler) {
	h.t.Helper()
	h.clock.Advance(61 * time.Second)
	if err := r.Tick(context.Background()); err != nil {
		h.t.Fatal(err)
	}
}

func (h *snapshotHarness) snapshot(id string) codingenv.SnapshotJob {
	h.t.Helper()
	job, err := h.store.CodingSnapshotJob(context.Background(), "ws-snap", id)
	if err != nil {
		h.t.Fatal(err)
	}
	return job
}

func (h *snapshotHarness) request(r *coding.SnapshotReconciler) domain.CodingEnvironmentSnapshot {
	h.t.Helper()
	snapshot, created, err := r.Request(context.Background(), h.env, "u1")
	if err != nil || !created || snapshot.Status != domain.CodingSnapshotQueued {
		h.t.Fatalf("request: %+v %t %v", snapshot, created, err)
	}
	return snapshot
}

func (h *snapshotHarness) report(id string, result codingenv.SnapshotResult) {
	h.t.Helper()
	job := h.snapshot(id)
	if err := h.store.RecordCodingSnapshotResult(context.Background(), "ws-snap", id, job.Snapshot.SandboxID, result); err != nil {
		h.t.Fatal(err)
	}
}

func TestSnapshotReconcilerSuccess(t *testing.T) {
	h := newSnapshotHarness(t)
	r := h.reconciler("a")
	snapshot := h.request(r)
	if again, created, err := r.Request(context.Background(), h.env, "u2"); err != nil || created || again.ID != snapshot.ID {
		t.Fatalf("same config while queued must reuse: %+v %t %v", again, created, err)
	}
	h.tick(r)
	job := h.snapshot(snapshot.ID)
	if job.Snapshot.Status != domain.CodingSnapshotBuilding || job.Snapshot.SandboxID == "" || job.Attempts != 1 || job.Snapshot.StartedAt == nil {
		t.Fatalf("building: %+v", job)
	}
	spec, ok := h.provider.Spec(job.Snapshot.SandboxID)
	if !ok || spec.Kind != sandbox.KindSnapshot || spec.Env[sandbox.EnvMode] != coding.ModeSnapshot || spec.Env[sandbox.EnvFlowSnapshot] != "1" || spec.Env[sandbox.EnvSnapshotting] != "1" || spec.SnapshotID != snapshot.ID || spec.Image != "flow-sandbox:test" || h.minted != 1 {
		t.Fatalf("sandbox spec: %+v", spec)
	}
	h.tick(r) // still running, nothing reported
	if h.snapshot(snapshot.ID).Snapshot.Status != domain.CodingSnapshotBuilding {
		t.Fatal("still building")
	}
	h.report(snapshot.ID, codingenv.SnapshotResult{Status: "ready", BaseSHA: "abc123", SizeBytes: 99})
	h.tick(r)
	job = h.snapshot(snapshot.ID)
	if job.Snapshot.Status != domain.CodingSnapshotReady || job.Snapshot.StorageRef != "archive/"+snapshot.ID || job.Snapshot.SizeBytes != 1234 || job.Snapshot.BaseSHA != "abc123" || job.Snapshot.ExpiresAt == nil || job.Snapshot.FinishedAt == nil {
		t.Fatalf("ready: %+v", job.Snapshot)
	}
	if status, _ := h.provider.Status(context.Background(), job.Snapshot.SandboxID); status.Phase != sandbox.PhaseMissing {
		t.Fatalf("build sandbox must be stopped: %+v", status)
	}
	if len(h.usage) != 1 || h.usage[0].UserID != "u1" || h.usage[0].Kind != "runtime" || h.usage[0].RuntimeSeconds <= 0 || h.usage[0].CostMicros <= 0 {
		t.Fatalf("runtime billed to the requester: %+v", h.usage)
	}
	if ready, err := h.store.LatestReadyCodingSnapshot(context.Background(), "ws-snap", h.env.ID, snapshot.ConfigHash); err != nil || ready.ID != snapshot.ID {
		t.Fatalf("restorable: %+v %v", ready, err)
	}
	// Ticking a finished snapshot changes nothing.
	h.tick(r)
	if h.snapshot(snapshot.ID).Snapshot.Status != domain.CodingSnapshotReady {
		t.Fatal("ready is terminal")
	}
}

func TestSnapshotReconcilerSetupFailureDoesNotRetry(t *testing.T) {
	h := newSnapshotHarness(t)
	r := h.reconciler("a")
	snapshot := h.request(r)
	h.tick(r)
	h.report(snapshot.ID, codingenv.SnapshotResult{Status: "failed", ErrorCode: domain.CodingErrEnvironmentSetup, Error: "npm ci exited 1"})
	h.tick(r)
	job := h.snapshot(snapshot.ID)
	if job.Snapshot.Status != domain.CodingSnapshotFailed || job.Snapshot.ErrorCode != domain.CodingErrEnvironmentSetup || job.Snapshot.Error != "npm ci exited 1" || job.Attempts != 1 {
		t.Fatalf("failed: %+v", job)
	}
}

func TestSnapshotReconcilerRetriesLostSandbox(t *testing.T) {
	h := newSnapshotHarness(t)
	r := h.reconciler("a")
	snapshot := h.request(r)
	h.tick(r)
	first := h.snapshot(snapshot.ID).Snapshot.SandboxID
	if err := h.provider.SetPhase(first, sandbox.PhaseFailed, sandbox.ReasonOOMKilled); err != nil {
		t.Fatal(err)
	}
	h.tick(r) // lost → requeued
	job := h.snapshot(snapshot.ID)
	if job.Snapshot.Status != domain.CodingSnapshotQueued || job.Snapshot.SandboxID != "" {
		t.Fatalf("requeued: %+v", job)
	}
	h.tick(r) // second attempt
	job = h.snapshot(snapshot.ID)
	if job.Attempts != 2 || job.Snapshot.SandboxID == "" || job.Snapshot.SandboxID == first {
		t.Fatalf("second attempt uses a new sandbox: %+v", job)
	}
	if err := h.provider.SetPhase(job.Snapshot.SandboxID, sandbox.PhaseFailed, sandbox.ReasonImagePull); err != nil {
		t.Fatal(err)
	}
	h.tick(r)
	job = h.snapshot(snapshot.ID)
	if job.Snapshot.Status != domain.CodingSnapshotFailed || job.Snapshot.ErrorCode != domain.CodingErrImagePullFailed {
		t.Fatalf("retries exhausted: %+v", job.Snapshot)
	}
	// The failed attempt's late result is rejected.
	if err := h.store.RecordCodingSnapshotResult(context.Background(), "ws-snap", snapshot.ID, first, codingenv.SnapshotResult{Status: "ready"}); !errors.Is(err, coding.ErrVersionConflict) {
		t.Fatalf("late result: %v", err)
	}
}

func TestSnapshotReconcilerRunnerExitWithoutResult(t *testing.T) {
	h := newSnapshotHarness(t)
	h.config.Retries.Snapshot = 0
	r := h.reconciler("a")
	snapshot := h.request(r)
	h.tick(r)
	if err := h.provider.SetPhase(h.snapshot(snapshot.ID).Snapshot.SandboxID, sandbox.PhaseSucceeded, ""); err != nil {
		t.Fatal(err)
	}
	r.CompleteGrace = 90 * time.Second
	h.tick(r) // first seen exited
	h.tick(r) // 61s later: still inside the grace
	if h.snapshot(snapshot.ID).Snapshot.Status != domain.CodingSnapshotBuilding {
		t.Fatal("waits for the complete call during the grace period")
	}
	h.tick(r)
	job := h.snapshot(snapshot.ID)
	if job.Snapshot.Status != domain.CodingSnapshotFailed || job.Snapshot.ErrorCode != domain.CodingErrSnapshotFailed {
		t.Fatalf("exit without result: %+v", job.Snapshot)
	}
}

func TestSnapshotReconcilerCancel(t *testing.T) {
	h := newSnapshotHarness(t)
	r := h.reconciler("a")
	queued := h.request(r)
	if _, err := h.store.RequestCodingSnapshotCancel(context.Background(), "ws-snap", queued.ID); err != nil {
		t.Fatal(err)
	}
	h.tick(r)
	if job := h.snapshot(queued.ID); job.Snapshot.Status != domain.CodingSnapshotCanceled || job.Snapshot.SandboxID != "" {
		t.Fatalf("queued cancel: %+v", job)
	}
	building := h.request(r)
	h.tick(r)
	sandboxID := h.snapshot(building.ID).Snapshot.SandboxID
	if _, err := h.store.RequestCodingSnapshotCancel(context.Background(), "ws-snap", building.ID); err != nil {
		t.Fatal(err)
	}
	h.tick(r)
	if job := h.snapshot(building.ID); job.Snapshot.Status != domain.CodingSnapshotCanceled {
		t.Fatalf("building cancel: %+v", job)
	}
	if status, _ := h.provider.Status(context.Background(), sandboxID); status.Phase != sandbox.PhaseMissing {
		t.Fatal("cancel stops the build sandbox")
	}
}

func TestSnapshotReconcilerTransientStartAndFinish(t *testing.T) {
	h := newSnapshotHarness(t)
	h.strategy.finishErrs = []error{fmt.Errorf("upload busy: %w", sandbox.ErrTransient)}
	r := h.reconciler("a")
	snapshot := h.request(r)
	h.provider.FailNext("StartSandbox", fmt.Errorf("api 503: %w", sandbox.ErrTransient))
	h.tick(r)
	job := h.snapshot(snapshot.ID)
	if job.Snapshot.Status != domain.CodingSnapshotQueued || job.StartFailures != 1 {
		t.Fatalf("transient start stays queued: %+v", job)
	}
	h.tick(r)
	if h.snapshot(snapshot.ID).Snapshot.Status != domain.CodingSnapshotBuilding {
		t.Fatal("retry starts the build")
	}
	if h.snapshot(snapshot.ID).Attempts != 1 {
		t.Fatal("a transient start failure is not an attempt")
	}
	h.report(snapshot.ID, codingenv.SnapshotResult{Status: "ready"})
	h.tick(r) // Finish fails transiently; the result is kept
	if job := h.snapshot(snapshot.ID); job.Snapshot.Status != domain.CodingSnapshotBuilding || job.FinishFailures != 1 || job.Result == nil {
		t.Fatalf("transient finish: %+v", job)
	}
	h.tick(r)
	if job := h.snapshot(snapshot.ID); job.Snapshot.Status != domain.CodingSnapshotReady {
		t.Fatalf("finish retried: %+v", job.Snapshot)
	}
	// A permanent start failure fails the snapshot.
	h.env.PrepareScript = "make"
	if _, err := h.store.UpdateCodingEnvironment(context.Background(), "ws-snap", h.env.ID, 0, func(e *domain.CodingEnvironment) error { e.PrepareScript = "make"; return nil }); err != nil {
		t.Fatal(err)
	}
	second := h.request(r)
	h.provider.FailNext("StartSandbox", errors.New("forbidden"))
	h.tick(r)
	if job := h.snapshot(second.ID); job.Snapshot.Status != domain.CodingSnapshotFailed || job.Snapshot.ErrorCode != domain.CodingErrSandboxStartFailed {
		t.Fatalf("permanent start failure: %+v", job.Snapshot)
	}
}

func TestSnapshotReconcilerRestartAndTimeout(t *testing.T) {
	h := newSnapshotHarness(t)
	first := h.reconciler("replica-1")
	snapshot := h.request(first)
	h.tick(first)
	sandboxID := h.snapshot(snapshot.ID).Snapshot.SandboxID
	// replica-1 dies; replica-2 picks the row up once it is due and adopts the sandbox.
	second := h.reconciler("replica-2")
	h.report(snapshot.ID, codingenv.SnapshotResult{Status: "ready"})
	h.tick(second)
	job := h.snapshot(snapshot.ID)
	if job.Snapshot.Status != domain.CodingSnapshotReady || job.Snapshot.SandboxID != sandboxID || job.Attempts != 1 {
		t.Fatalf("resumed after restart: %+v", job)
	}
	// Timeout.
	h.env.PrepareScript = "slow"
	if _, err := h.store.UpdateCodingEnvironment(context.Background(), "ws-snap", h.env.ID, 0, func(e *domain.CodingEnvironment) error { e.PrepareScript = "slow"; return nil }); err != nil {
		t.Fatal(err)
	}
	slow := h.request(second)
	h.tick(second)
	h.clock.Advance(31 * time.Minute)
	h.tick(second)
	if job := h.snapshot(slow.ID); job.Snapshot.Status != domain.CodingSnapshotFailed || job.Snapshot.ErrorCode != domain.CodingErrSnapshotFailed {
		t.Fatalf("timeout: %+v", job.Snapshot)
	}
}

func TestSnapshotReconcilerVolumePlan(t *testing.T) {
	h := newSnapshotHarness(t)
	h.strategy.volume = true
	r := h.reconciler("a")
	snapshot := h.request(r)
	h.tick(r)
	if _, err := h.provider.VolumeStatus(context.Background(), "vol-"+snapshot.ID); err != nil {
		t.Fatalf("build volume created: %v", err)
	}
	h.report(snapshot.ID, codingenv.SnapshotResult{Status: "ready"})
	h.tick(r)
	if _, err := h.provider.VolumeStatus(context.Background(), "vol-"+snapshot.ID); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("build volume deleted after finish: %v", err)
	}
}

func TestSnapshotReconcilerMaintenance(t *testing.T) {
	h := newSnapshotHarness(t)
	r := h.reconciler("a")
	build := func(script string) string {
		if _, err := h.store.UpdateCodingEnvironment(context.Background(), "ws-snap", h.env.ID, 0, func(e *domain.CodingEnvironment) error { e.PrepareScript = script; return nil }); err != nil {
			t.Fatal(err)
		}
		env, _ := h.store.CodingEnvironment(context.Background(), "ws-snap", h.env.ID)
		h.env = env
		snapshot := h.request(r)
		h.tick(r)
		h.report(snapshot.ID, codingenv.SnapshotResult{Status: "ready", BaseSHA: "sha-1"})
		h.tick(r)
		return snapshot.ID
	}
	ids := []string{build("a"), build("b"), build("c")}
	if err := r.Maintain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.strategy.deleted) != 1 || h.strategy.deleted[0] != "archive/"+ids[0] {
		t.Fatalf("retention deletes the oldest: %v", h.strategy.deleted)
	}
	if job := h.snapshot(ids[0]); job.Snapshot.Status != domain.CodingSnapshotSuperseded || job.Snapshot.StorageRef != "" {
		t.Fatalf("pruned: %+v", job.Snapshot)
	}
	// TTL expiry.
	h.clock.Advance(8 * 24 * time.Hour)
	if err := r.Maintain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if job := h.snapshot(ids[2]); job.Snapshot.Status != domain.CodingSnapshotExpired {
		t.Fatalf("expired: %+v", job.Snapshot)
	}
}

func TestSnapshotReconcilerAutoRefresh(t *testing.T) {
	h := newSnapshotHarness(t)
	r := h.reconciler("a")
	snapshot := h.request(r)
	h.tick(r)
	h.report(snapshot.ID, codingenv.SnapshotResult{Status: "ready", BaseSHA: "sha-1"})
	h.tick(r)
	head := "sha-1"
	r.BranchHead = func(context.Context, domain.CodingEnvironment) (string, error) { return head, nil }
	// Snapshot timestamps come from the clock; make it old relative to real time used by the store query.
	r.Now = func() time.Time { return h.clock.Now().Add(48 * time.Hour) }
	if err := r.Maintain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if active, _ := h.store.ActiveCodingSnapshots(context.Background(), "ws-snap", h.env.ID); len(active) != 0 {
		t.Fatal("unchanged branch: no refresh")
	}
	head = "sha-2"
	if err := r.Maintain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if active, _ := h.store.ActiveCodingSnapshots(context.Background(), "ws-snap", h.env.ID); len(active) != 1 || active[0].RequestedBy != "u1" {
		t.Fatalf("moved branch queues a refresh: %+v", active)
	}
}

func TestSnapshotRequestRequiresSandboxes(t *testing.T) {
	h := newSnapshotHarness(t)
	r := &coding.SnapshotReconciler{Store: h.store, Config: h.config}
	if _, _, err := r.Request(context.Background(), h.env, "u1"); !errors.Is(err, coding.ErrSnapshotsUnavailable) {
		t.Fatalf("disabled: %v", err)
	}
	archived, err := h.store.SetCodingEnvironmentArchived(context.Background(), "ws-snap", h.env.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.reconciler("a").Request(context.Background(), archived, "u1"); !errors.Is(err, coding.ErrInvalidEnvironment) {
		t.Fatalf("archived: %v", err)
	}
}

type snapshotLookupFake struct {
	names    []string
	ready    map[string]domain.CodingEnvironmentSnapshot // by config hash
	lastHash string
}

func (f *snapshotLookupFake) CodingEnvironmentSecretNames(context.Context, string, string) ([]string, error) {
	return f.names, nil
}

func (f *snapshotLookupFake) LatestReadyCodingSnapshot(_ context.Context, _, _, hash string) (domain.CodingEnvironmentSnapshot, error) {
	f.lastHash = hash
	if snapshot, ok := f.ready[hash]; ok {
		return snapshot, nil
	}
	return domain.CodingEnvironmentSnapshot{}, domain.ErrCodingNotFound
}

func TestStoreSnapshotResolverMatchesConfigHash(t *testing.T) {
	ctx := context.Background()
	environment := domain.CodingEnvironment{ID: "env1", WorkspaceKey: "ws", Repository: "acme/web", DefaultBranch: "main"}
	fake := &snapshotLookupFake{names: []string{"TOKEN"}, ready: map[string]domain.CodingEnvironmentSnapshot{}}
	resolver := coding.StoreSnapshotResolver{Store: fake, Image: "img:1"}
	if _, ok, err := resolver.ResolveCodingSnapshot(ctx, "ws", environment); err != nil || ok {
		t.Fatalf("no snapshot: ok=%v err=%v", ok, err)
	}
	hash := coding.SnapshotConfigHash(environment, "img:1", coding.SnapshotBaseRef(environment), []string{"TOKEN"})
	if fake.lastHash != hash {
		t.Fatalf("looked up hash %q, want %q", fake.lastHash, hash)
	}
	fake.ready[hash] = domain.CodingEnvironmentSnapshot{ID: "snap1", Strategy: sandbox.StrategyArchive, StorageRef: "k"}
	if snapshot, ok, err := resolver.ResolveCodingSnapshot(ctx, "ws", environment); err != nil || !ok || snapshot.ID != "snap1" {
		t.Fatalf("ready snapshot: %+v ok=%v err=%v", snapshot, ok, err)
	}
	// A changed environment (other image) no longer matches.
	if _, ok, _ := (coding.StoreSnapshotResolver{Store: fake, Image: "img:2"}).ResolveCodingSnapshot(ctx, "ws", environment); ok {
		t.Fatal("a snapshot built for another image must not be restored")
	}
}
