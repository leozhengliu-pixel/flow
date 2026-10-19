package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/config"
	"flow/api/internal/domain"
)

// memStore is an in-memory ReconcilerStore + MaintenanceStore with the same
// lease and CAS semantics as the SQL implementation.
type memStore struct {
	mu        sync.Mutex
	now       func() time.Time
	rows      map[string]*memRow
	tokens    map[string]time.Time // token → expiry
	revoked   map[string]bool
	snapshots map[string]string
	usage     []domain.CodingUsageRecord
	mints     int
}

type memRow struct {
	record     Record
	leaseOwner string
	leaseUntil time.Time
}

func newMemStore(now func() time.Time) *memStore {
	return &memStore{now: now, rows: map[string]*memRow{}, tokens: map[string]time.Time{}, revoked: map[string]bool{}, snapshots: map[string]string{}}
}

func settled(r Record) bool { return r.Desired == DesiredStopped && r.Phase == PhaseMissing }

func (m *memStore) add(record Record) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = m.now()
	}
	if record.Phase == "" {
		record.Phase = PhasePending
	}
	m.rows[record.ID] = &memRow{record: record}
}

func (m *memStore) get(id string) Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rows[id].record
}

func (m *memStore) setDesired(id string, desired Desired) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[id].record.Desired = desired
	m.rows[id].leaseUntil = time.Time{}
	m.rows[id].leaseOwner = ""
}

func (m *memStore) LeaseSandboxes(_ context.Context, owner string, lease time.Duration, limit int) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	ids := []string{}
	for id, row := range m.rows {
		if !settled(row.record) && row.leaseUntil.Before(now) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := []Record{}
	for _, id := range ids {
		if len(out) == limit {
			break
		}
		row := m.rows[id]
		row.leaseOwner, row.leaseUntil = owner, now.Add(lease)
		out = append(out, row.record)
	}
	return out, nil
}

func (m *memStore) SaveSandboxObservation(ctx context.Context, owner, id string, status SandboxStatus, attempts int) error {
	return m.SaveSandboxObservationAt(ctx, owner, id, status, attempts, m.now().Add(ObserveInterval))
}

func (m *memStore) SaveSandboxObservationAt(_ context.Context, owner, id string, status SandboxStatus, attempts int, next time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[id]
	if !ok || row.leaseOwner != owner {
		return errors.New("lease lost")
	}
	r := &row.record
	r.Phase, r.Reason, r.Message, r.Attempts = status.Phase, status.Reason, status.Message, attempts
	if status.ProviderRef != "" {
		r.ProviderRef = status.ProviderRef
	}
	if r.StartedAt == nil && status.StartedAt != nil {
		r.StartedAt = status.StartedAt
	}
	if r.EndedAt == nil && status.Phase.Terminal() {
		end := m.now()
		if status.FinishedAt != nil {
			end = *status.FinishedAt
		}
		r.EndedAt = &end
	}
	row.leaseOwner, row.leaseUntil = "", next
	return nil
}

func (m *memStore) SetSandboxLogsKey(_ context.Context, owner, id, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows[id].leaseOwner != owner {
		return errors.New("lease lost")
	}
	m.rows[id].record.LogsKey = key
	return nil
}

func (m *memStore) AddSandboxBilledSeconds(_ context.Context, owner, id string, from, to int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row := m.rows[id]
	if row.leaseOwner != owner || row.record.BilledSeconds != from {
		return false, nil
	}
	row.record.BilledSeconds = to
	return true, nil
}

func (m *memStore) RecordCodingUsage(_ context.Context, record domain.CodingUsageRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usage = append(m.usage, record)
	return nil
}

func (m *memStore) RevokeSandboxTokens(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revoked[id] = true
	return nil
}

func (m *memStore) MintSandboxToken(_ context.Context, record Record, ttl time.Duration) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mints++
	token := fmt.Sprintf("fcs_%s_%d", record.ID, m.mints)
	m.tokens[token] = m.now().Add(ttl)
	return token, nil
}

func (m *memStore) KnownSandboxIDs(_ context.Context, ids []string) (map[string]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]bool{}
	for _, id := range ids {
		if row, ok := m.rows[id]; ok && !settled(row.record) {
			out[id] = true
		}
	}
	return out, nil
}

func (m *memStore) WarmSandboxes(context.Context) (map[string]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]int{}
	for _, row := range m.rows {
		r := row.record
		if r.Kind == KindWarm && r.SessionID == "" && r.Desired == DesiredRunning && !r.Phase.Terminal() {
			out[WarmKey(r.Size, r.Region)]++
		}
	}
	return out, nil
}

func (m *memStore) CreateWarmSandbox(_ context.Context, record Record) error {
	m.add(record)
	return nil
}

func (m *memStore) RecycleWarmSandboxes(_ context.Context, before time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, row := range m.rows {
		r := &row.record
		if r.Kind == KindWarm && r.SessionID == "" && r.Desired == DesiredRunning && (r.CreatedAt.Before(before) || r.Phase.Terminal()) {
			r.Desired = DesiredStopped
			row.leaseUntil = time.Time{}
			n++
		}
	}
	return n, nil
}

func (m *memStore) DeleteExpiredSandboxTokens(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for token, expiry := range m.tokens {
		if expiry.Before(before) {
			delete(m.tokens, token)
			n++
		}
	}
	return n, nil
}

func (m *memStore) SnapshotStates(_ context.Context, ids []string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for _, id := range ids {
		if status, ok := m.snapshots[id]; ok {
			out[id] = status
		}
	}
	return out, nil
}

func (m *memStore) SandboxCounts(context.Context) (map[Kind]map[Phase]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[Kind]map[Phase]int{}
	for _, row := range m.rows {
		if settled(row.record) {
			continue
		}
		if out[row.record.Kind] == nil {
			out[row.record.Kind] = map[Phase]int{}
		}
		out[row.record.Kind][row.record.Phase]++
	}
	return out, nil
}

var (
	_ ReconcilerStore  = (*memStore)(nil)
	_ MaintenanceStore = (*memStore)(nil)
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func reconcilerConfig() config.CodingConfig {
	return config.CodingConfig{
		RuntimeURL: "http://flow:8090", InstanceID: "flow", Image: "img:1", ReconcileInterval: 5 * time.Second,
		GCInterval: 5 * time.Minute, OrphanGrace: 10 * time.Minute, WarmPoolMaxAge: time.Hour,
		Sizes:            map[string]config.SandboxSize{"small": {CPU: "1", Memory: "8Gi"}, "medium": {CPU: "2", Memory: "16Gi"}},
		Timeouts:         config.CodingTimeouts{Start: 10 * time.Minute, StopGrace: time.Minute, MaxSession: 4 * time.Hour, TokenGrace: 5 * time.Minute},
		Retries:          config.CodingRetries{Start: 2},
		RuntimePricesUSD: map[string]float64{"medium": 0.36},
		Egress:           config.CodingEgressConfig{ProxyURL: "http://proxy:3128"},
	}
}

type world struct {
	clock    *clock
	store    *memStore
	provider *Fake
	logs     *memObjects
}

func newWorld() *world {
	c := &clock{t: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}
	return &world{clock: c, store: newMemStore(c.Now), provider: NewFake(c.Now), logs: newMemObjects()}
}

func (w *world) reconciler(owner string) *Reconciler {
	return &Reconciler{Store: w.store, Provider: w.provider, Config: reconcilerConfig(), Owner: owner, Usage: w.store, Logs: w.logs, Now: w.clock.Now}
}

// pass advances past every scheduled observation and runs one pass.
func (w *world) pass(t *testing.T, r *Reconciler) {
	t.Helper()
	w.clock.Advance(2 * time.Minute)
	if _, err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func sessionRecord(id string) Record {
	return Record{ID: id, WorkspaceKey: "acme", SessionID: "sess-" + id, Kind: KindSession, Desired: DesiredRunning, Size: "medium", UserID: "u1", Env: map[string]string{"A": "1"}}
}

func TestReconcilerStartsObservesAndStops(t *testing.T) {
	w := newWorld()
	r := w.reconciler("flow-a")
	w.store.add(sessionRecord("s1"))
	w.pass(t, r)
	got := w.store.get("s1")
	if got.Phase != PhaseRunning || got.Attempts != 1 || got.ProviderRef != "flow-cs-s1" || got.StartedAt == nil {
		t.Fatalf("after start: %+v", got)
	}
	spec, ok := w.provider.Spec("s1")
	if !ok || spec.SessionID != "sess-s1" || spec.Resources.CPU != "2" || spec.ProxyURL != "http://proxy:3128" || spec.Env["A"] != "1" || spec.Deadline != 4*time.Hour {
		t.Fatalf("spec = %+v", spec)
	}
	if w.store.mints != 1 {
		t.Fatalf("one token per start, got %d", w.store.mints)
	}
	for _, expiry := range w.store.tokens {
		if want := w.clock.Now().Add(4*time.Hour + 5*time.Minute); !expiry.Equal(want) {
			t.Fatalf("token ttl must be deadline + grace: %s vs %s", expiry, want)
		}
	}
	// Steady observation does not restart or re-mint.
	w.pass(t, r)
	w.pass(t, r)
	if w.store.mints != 1 || w.store.get("s1").Phase != PhaseRunning {
		t.Fatalf("steady state changed: mints=%d %+v", w.store.mints, w.store.get("s1"))
	}
	w.provider.AppendLog("s1", "runner output\n")
	w.clock.Advance(10 * time.Minute)
	w.store.setDesired("s1", DesiredStopped)
	w.pass(t, r)
	got = w.store.get("s1")
	if got.Phase != PhaseMissing || got.EndedAt == nil || !w.store.revoked["s1"] {
		t.Fatalf("after stop: %+v revoked=%v", got, w.store.revoked["s1"])
	}
	if got.LogsKey != LogsKey("acme", "s1") || string(w.logs.objects[got.LogsKey]) != "runner output\n" {
		t.Fatalf("logs = %q %q", got.LogsKey, w.logs.objects[got.LogsKey])
	}
	if status, _ := w.provider.Status(context.Background(), "s1"); status.Phase != PhaseMissing {
		t.Fatal("the provider sandbox must be gone")
	}
	// Settled rows are not leased again.
	if n, _ := r.RunOnce(context.Background()); n != 0 {
		t.Fatalf("settled rows leased: %d", n)
	}
}

func TestReconcilerRestartAdoptsWithoutDuplicates(t *testing.T) {
	w := newWorld()
	// Process A starts the sandbox but crashes before saving the observation.
	a := w.reconciler("flow-a")
	w.store.add(sessionRecord("s1"))
	records, _ := w.store.LeaseSandboxes(context.Background(), "flow-a", ReconcileLease, 10)
	spec := a.Spec(records[0], w.clock.Now())
	spec.Token = "fcs_lost"
	if _, err := w.provider.StartSandbox(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	// Process B (a restarted Flow) waits for the lease to expire and adopts it.
	b := w.reconciler("flow-b")
	if n, _ := b.RunOnce(context.Background()); n != 0 {
		t.Fatal("a live lease must not be stolen")
	}
	w.pass(t, b)
	got := w.store.get("s1")
	if got.Phase != PhaseRunning || got.ProviderRef != "flow-cs-s1" {
		t.Fatalf("adopted = %+v", got)
	}
	if w.store.mints != 0 {
		t.Fatal("adopting an existing sandbox must not mint a token or start it again")
	}
	objects, _ := w.provider.ListManaged(context.Background(), ListFilter{SandboxID: "s1"})
	if len(objects) != 1 {
		t.Fatalf("duplicate sandboxes: %+v", objects)
	}
	// A stale save by the crashed owner is rejected.
	if err := w.store.SaveSandboxObservation(context.Background(), "flow-a", "s1", SandboxStatus{Phase: PhaseFailed}, 9); err == nil {
		t.Fatal("a lost lease must not write")
	}
}

func TestReconcilerRetriesAndErrorMapping(t *testing.T) {
	t.Run("transient errors retry up to the cap", func(t *testing.T) {
		w := newWorld()
		r := w.reconciler("o")
		w.store.add(sessionRecord("s1"))
		for i := 0; i < 3; i++ {
			w.provider.FailNext("StartSandbox", fmt.Errorf("boom: %w", ErrTransient))
			w.pass(t, r)
		}
		got := w.store.get("s1")
		if got.Phase != PhaseFailed || got.Reason != ReasonStartFailed || got.Attempts != 3 {
			t.Fatalf("got %+v", got)
		}
		if code := ErrorCode(got.Kind, got.Phase, got.Reason); code != domain.CodingErrSandboxStartFailed {
			t.Fatalf("code = %s", code)
		}
	})
	t.Run("a transient error then success", func(t *testing.T) {
		w := newWorld()
		r := w.reconciler("o")
		w.store.add(sessionRecord("s1"))
		w.provider.FailNext("StartSandbox", fmt.Errorf("boom: %w", ErrTransient))
		w.pass(t, r)
		if got := w.store.get("s1"); got.Phase != PhasePending || got.Attempts != 1 || !strings.Contains(got.Message, "boom") {
			t.Fatalf("after failure: %+v", got)
		}
		w.pass(t, r)
		if got := w.store.get("s1"); got.Phase != PhaseRunning || got.Attempts != 2 || got.Message != "" {
			t.Fatalf("after retry: %+v", got)
		}
	})
	t.Run("non-transient errors fail fast", func(t *testing.T) {
		w := newWorld()
		r := w.reconciler("o")
		w.store.add(sessionRecord("s1"))
		w.provider.FailNext("StartSandbox", errors.New("invalid spec"))
		w.pass(t, r)
		if got := w.store.get("s1"); got.Phase != PhaseFailed || got.Reason != ReasonStartFailed {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("image pull failures map to imagePullFailed", func(t *testing.T) {
		w := newWorld()
		r := w.reconciler("o")
		w.store.add(sessionRecord("s1"))
		for i := 0; i < 3; i++ {
			w.provider.FailNext("StartSandbox", fmt.Errorf("pull: %w", ErrImagePull))
			w.pass(t, r)
		}
		got := w.store.get("s1")
		if got.Phase != PhaseFailed || ErrorCode(got.Kind, got.Phase, got.Reason) != domain.CodingErrImagePullFailed {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("quota waits without spending attempts until the start timeout", func(t *testing.T) {
		w := newWorld()
		r := w.reconciler("o")
		w.store.add(sessionRecord("s1"))
		for i := 0; i < 4; i++ {
			w.provider.FailNext("StartSandbox", fmt.Errorf("quota: %w", ErrQuota))
			w.pass(t, r)
		}
		if got := w.store.get("s1"); got.Phase != PhasePending || got.Attempts != 0 || got.Reason != ReasonQuota {
			t.Fatalf("got %+v", got)
		}
		w.clock.Advance(10 * time.Minute)
		w.pass(t, r)
		got := w.store.get("s1")
		if got.Phase != PhaseFailed || ErrorCode(got.Kind, got.Phase, got.Reason) != domain.CodingErrCapacityUnavailable {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("a sandbox stuck starting is stopped after the start timeout", func(t *testing.T) {
		w := newWorld()
		w.provider.StartPhase = PhasePending
		r := w.reconciler("o")
		w.store.add(sessionRecord("s1"))
		w.pass(t, r)
		_ = w.provider.SetPhase("s1", PhasePending, ReasonUnschedulable)
		w.clock.Advance(10 * time.Minute)
		w.pass(t, r)
		got := w.store.get("s1")
		if got.Phase != PhaseFailed || ErrorCode(got.Kind, got.Phase, got.Reason) != domain.CodingErrUnschedulable {
			t.Fatalf("got %+v", got)
		}
		if status, _ := w.provider.Status(context.Background(), "s1"); status.Phase != PhaseMissing {
			t.Fatal("the stuck sandbox must be stopped")
		}
	})
	t.Run("a vanished running sandbox is lost and never recreated", func(t *testing.T) {
		w := newWorld()
		r := w.reconciler("o")
		w.store.add(sessionRecord("s1"))
		w.pass(t, r)
		_ = w.provider.StopSandbox(context.Background(), "s1", StopOptions{})
		w.pass(t, r)
		got := w.store.get("s1")
		if got.Phase != PhaseMissing || ErrorCode(got.Kind, got.Phase, got.Reason) != domain.CodingErrSandboxLost {
			t.Fatalf("got %+v", got)
		}
		w.pass(t, r)
		if status, _ := w.provider.Status(context.Background(), "s1"); status.Phase != PhaseMissing || w.store.mints != 1 {
			t.Fatal("a lost sandbox must not be restarted under the same id")
		}
	})
	t.Run("failures observed from the provider stay sticky", func(t *testing.T) {
		w := newWorld()
		r := w.reconciler("o")
		w.store.add(sessionRecord("s1"))
		w.pass(t, r)
		_ = w.provider.SetPhase("s1", PhaseFailed, ReasonOOMKilled)
		w.pass(t, r)
		w.pass(t, r)
		got := w.store.get("s1")
		if got.Phase != PhaseFailed || got.Reason != ReasonOOMKilled || ErrorCode(got.Kind, got.Phase, got.Reason) != domain.CodingErrSandboxLost {
			t.Fatalf("got %+v", got)
		}
		w.store.setDesired("s1", DesiredStopped)
		w.pass(t, r)
		if got := w.store.get("s1"); got.Phase != PhaseMissing || got.Reason != ReasonOOMKilled {
			t.Fatalf("the failure reason must survive the stop: %+v", got)
		}
	})
	t.Run("the deadline stops the sandbox", func(t *testing.T) {
		w := newWorld()
		r := w.reconciler("o")
		record := sessionRecord("s1")
		record.DeadlineAt = w.clock.Now().Add(time.Hour)
		w.store.add(record)
		w.pass(t, r)
		w.clock.Advance(time.Hour)
		w.pass(t, r)
		got := w.store.get("s1")
		if got.Phase != PhaseFailed || got.Reason != ReasonDeadlineExceeded || ErrorCode(got.Kind, got.Phase, got.Reason) != domain.CodingErrSessionTimeout {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("status errors keep the row and retry", func(t *testing.T) {
		w := newWorld()
		r := w.reconciler("o")
		w.store.add(sessionRecord("s1"))
		w.pass(t, r)
		w.provider.FailNext("Status", fmt.Errorf("api down: %w", ErrTransient))
		w.pass(t, r)
		if got := w.store.get("s1"); got.Phase != PhaseRunning {
			t.Fatalf("a failed observation must not change the phase: %+v", got)
		}
	})
}

func TestReconcilerBillsRuntimeOnce(t *testing.T) {
	w := newWorld()
	r := w.reconciler("o")
	w.store.add(sessionRecord("s1"))
	w.pass(t, r) // starts at T
	w.clock.Advance(11 * time.Minute)
	w.pass(t, r) // 13 minutes elapsed → two increments
	if len(w.store.usage) != 1 || w.store.usage[0].RuntimeSeconds != 600 || w.store.get("s1").BilledSeconds != 600 {
		t.Fatalf("usage = %+v", w.store.usage)
	}
	usage := w.store.usage[0]
	if usage.Kind != "runtime" || usage.UserID != "u1" || usage.SessionID != "sess-s1" || usage.SandboxID != "s1" || usage.CostMicros != 60000 {
		t.Fatalf("record = %+v", usage)
	}
	w.store.setDesired("s1", DesiredStopped)
	w.pass(t, r) // 15 minutes total, billed exactly at stop
	total := int64(0)
	for _, item := range w.store.usage {
		total += item.RuntimeSeconds
	}
	if total != 15*60 {
		t.Fatalf("total billed %d", total)
	}
	before := len(w.store.usage)
	w.pass(t, r)
	if len(w.store.usage) != before {
		t.Fatal("billing must not repeat")
	}
	// Warm sandboxes are never billed.
	warm := Record{ID: "w1", Kind: KindWarm, Desired: DesiredRunning, Size: "medium"}
	w.store.add(warm)
	w.pass(t, r)
	w.clock.Advance(time.Hour)
	w.pass(t, r)
	for _, item := range w.store.usage {
		if item.SandboxID == "w1" {
			t.Fatal("warm sandboxes are an operator cost")
		}
	}
}

func TestWarmPool(t *testing.T) {
	w := newWorld()
	c := reconcilerConfig()
	c.WarmPool = map[string]int{"small": 2, "medium": 1}
	c.Regions = []string{"eu", "us"}
	woken := 0
	pool := &WarmPool{Store: w.store, Config: c, Now: w.clock.Now, Wake: func() { woken++ }}
	ctx := context.Background()
	if err := pool.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	counts, _ := w.store.WarmSandboxes(ctx)
	if counts["small/eu"] != 2 || counts["small/us"] != 2 || counts["medium/eu"] != 1 || counts["medium/us"] != 1 || woken != 1 {
		t.Fatalf("counts = %v woken=%d", counts, woken)
	}
	if err := pool.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(w.store.rows) != 6 {
		t.Fatalf("the pool must not over-provision: %d rows", len(w.store.rows))
	}
	r := w.reconciler("o")
	w.pass(t, r)
	for _, row := range w.store.rows {
		if row.record.Phase != PhaseRunning || row.record.Kind != KindWarm || row.record.DeadlineAt.Sub(row.record.CreatedAt) != time.Hour+4*time.Hour {
			t.Fatalf("warm row = %+v", row.record)
		}
		spec, _ := w.provider.Spec(row.record.ID)
		if spec.SessionID != "" || spec.Kind != KindWarm || spec.Region != row.record.Region {
			t.Fatalf("warm spec = %+v", spec)
		}
	}
	// A failed warm sandbox is recycled and replaced.
	var failed string
	for id, row := range w.store.rows {
		if row.record.Size == "medium" && row.record.Region == "eu" {
			failed = id
		}
	}
	_ = w.provider.SetPhase(failed, PhaseFailed, ReasonExitNonZero)
	w.pass(t, r)
	if err := pool.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if w.store.get(failed).Desired != DesiredStopped {
		t.Fatal("a failed warm sandbox must be recycled")
	}
	counts, _ = w.store.WarmSandboxes(ctx)
	if counts["medium/eu"] != 1 {
		t.Fatalf("replacement missing: %v", counts)
	}
	// After max age everything is recycled and replaced.
	w.clock.Advance(2 * time.Hour)
	if err := pool.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	w.pass(t, r)
	running := 0
	for _, row := range w.store.rows {
		if row.record.Desired == DesiredRunning {
			running++
			if w.clock.Now().Sub(row.record.CreatedAt) > time.Hour {
				t.Fatalf("an old warm sandbox survived: %+v", row.record)
			}
		}
	}
	if running != 6 {
		t.Fatalf("running warm sandboxes = %d", running)
	}
	// Only the leader runs a pass.
	follower := &WarmPool{Store: w.store, Config: c, Now: w.clock.Now, Leader: func(context.Context, string, func() error) (bool, error) { return false, nil }}
	before := len(w.store.rows)
	w.clock.Advance(2 * time.Hour)
	if err := follower.RunOnce(ctx); err != nil || len(w.store.rows) != before {
		t.Fatal("a follower must not touch the pool")
	}
}

func TestOrphanGC(t *testing.T) {
	w := newWorld()
	ctx := context.Background()
	gc := &GC{Store: w.store, Provider: w.provider, Config: reconcilerConfig(), Now: w.clock.Now}
	start := func(id string) {
		if _, err := w.provider.StartSandbox(ctx, SandboxSpec{ID: id, Kind: KindSession, Token: "t"}); err != nil {
			t.Fatal(err)
		}
	}
	start("live")
	start("orphan")
	start("stopped")
	w.store.add(Record{ID: "live", Kind: KindSession, Desired: DesiredRunning, Phase: PhaseRunning})
	w.store.add(Record{ID: "stopped", Kind: KindSession, Desired: DesiredStopped, Phase: PhaseMissing})
	w.store.tokens["fcs_old"] = w.clock.Now().Add(-time.Minute)
	w.store.tokens["fcs_new"] = w.clock.Now().Add(time.Hour)
	var swept int
	gc.AddSweeper(Sweeper{Name: "test", Run: func(context.Context) error { swept++; return nil }})

	// Within the grace period nothing is deleted.
	if n, err := gc.RunOnce(ctx); err != nil || n != 0 {
		t.Fatalf("within grace: %d %v", n, err)
	}
	w.clock.Advance(11 * time.Minute)
	n, err := gc.RunOnce(ctx)
	if err != nil || n != 2 {
		t.Fatalf("deleted %d %v", n, err)
	}
	for id, want := range map[string]Phase{"live": PhaseRunning, "orphan": PhaseMissing, "stopped": PhaseMissing} {
		if status, _ := w.provider.Status(ctx, id); status.Phase != want {
			t.Errorf("%s = %s, want %s", id, status.Phase, want)
		}
	}
	if _, ok := w.store.tokens["fcs_old"]; ok {
		t.Fatal("expired tokens must be swept")
	}
	if _, ok := w.store.tokens["fcs_new"]; !ok {
		t.Fatal("valid tokens must stay")
	}
	if swept != 2 {
		t.Fatalf("sweepers ran %d times", swept)
	}
}

func TestGCSnapshotObjects(t *testing.T) {
	cases := []struct {
		objectType, status string
		exists, keep       bool
	}{
		{"volumesnapshot", domain.CodingSnapshotReady, true, true},
		{"volumesnapshot", domain.CodingSnapshotFailed, true, false},
		{"volumesnapshot", "", false, false},
		{"pvc", domain.CodingSnapshotBuilding, true, true},
		{"pvc", domain.CodingSnapshotReady, true, false},
	}
	for _, tc := range cases {
		provider := &listProvider{Fake: NewFake(nil), objects: []ManagedObject{{Type: tc.objectType, Name: "x", Labels: map[string]string{LabelSnapshotID: "snap1"}}}}
		store := newMemStore(time.Now)
		if tc.exists {
			store.snapshots["snap1"] = tc.status
		}
		gc := &GC{Store: store, Provider: provider, Config: reconcilerConfig()}
		n, err := gc.RunOnce(context.Background())
		if err != nil || (n == 0) != tc.keep {
			t.Errorf("%s/%s exists=%v: deleted %d %v", tc.objectType, tc.status, tc.exists, n, err)
		}
	}
}

// listProvider overrides ListManaged/DeleteManaged.
type listProvider struct {
	*Fake
	objects []ManagedObject
	deleted []string
}

func (l *listProvider) ListManaged(context.Context, ListFilter) ([]ManagedObject, error) {
	return l.objects, nil
}

func (l *listProvider) DeleteManaged(_ context.Context, object ManagedObject) error {
	l.deleted = append(l.deleted, object.Name)
	return nil
}

func TestBackoffAndIDs(t *testing.T) {
	if Backoff(0) != 2*time.Second || Backoff(3) != 16*time.Second || Backoff(10) != time.Minute {
		t.Fatal("backoff curve")
	}
	seen := map[string]bool{}
	now := time.Now()
	for i := 0; i < 1000; i++ {
		id := NewID(now)
		if len(id) != 26 || !ValidID(id) || seen[id] {
			t.Fatalf("bad id %q", id)
		}
		seen[id] = true
	}
	if a, b := NewID(now), NewID(now.Add(time.Millisecond)); a[:10] >= b[:10] {
		t.Fatalf("ids must sort by time: %s %s", a, b)
	}
	_ = io.Discard
}
