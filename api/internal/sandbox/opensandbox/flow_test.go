package opensandbox

// End-to-end flows of the shared sandbox reconciler and orphan GC over the
// opensandbox provider and the fake OpenSandbox server.

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/sandbox"
)

// flowStore is a minimal in-memory ReconcilerStore + MaintenanceStore
// (every row is due on every pass; leases always succeed).
type flowStore struct {
	mu      sync.Mutex
	rows    map[string]*sandbox.Record
	revoked map[string]bool
}

func newFlowStore() *flowStore {
	return &flowStore{rows: map[string]*sandbox.Record{}, revoked: map[string]bool{}}
}

func (s *flowStore) add(record sandbox.Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record.Phase == "" {
		record.Phase = sandbox.PhasePending
	}
	s.rows[record.ID] = &record
}

func (s *flowStore) get(id string) sandbox.Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.rows[id]
}

func (s *flowStore) setDesired(id string, desired sandbox.Desired) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[id].Desired = desired
}

func (s *flowStore) LeaseSandboxes(context.Context, string, time.Duration, int) ([]sandbox.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []sandbox.Record{}
	for _, row := range s.rows {
		if row.Desired == sandbox.DesiredStopped && row.Phase == sandbox.PhaseMissing {
			continue
		}
		out = append(out, *row)
	}
	return out, nil
}

func (s *flowStore) SaveSandboxObservation(ctx context.Context, owner, id string, status sandbox.SandboxStatus, attempts int) error {
	return s.SaveSandboxObservationAt(ctx, owner, id, status, attempts, time.Time{})
}

func (s *flowStore) SaveSandboxObservationAt(_ context.Context, _, id string, status sandbox.SandboxStatus, attempts int, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rows[id]
	r.Phase, r.Reason, r.Message, r.Attempts = status.Phase, status.Reason, status.Message, attempts
	if status.ProviderRef != "" {
		r.ProviderRef = status.ProviderRef
	}
	if r.StartedAt == nil && status.StartedAt != nil {
		r.StartedAt = status.StartedAt
	}
	return nil
}

func (s *flowStore) SetSandboxLogsKey(_ context.Context, _, id, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[id].LogsKey = key
	return nil
}

func (s *flowStore) AddSandboxBilledSeconds(context.Context, string, string, int64, int64) (bool, error) {
	return true, nil
}

func (s *flowStore) MintSandboxToken(_ context.Context, record sandbox.Record, _ time.Duration) (string, error) {
	return "fcs_minted_" + record.ID, nil
}

func (s *flowStore) KnownSandboxIDs(_ context.Context, ids []string) (map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	known := map[string]bool{}
	for _, id := range ids {
		if _, ok := s.rows[id]; ok {
			known[id] = true
		}
	}
	return known, nil
}

func (s *flowStore) WarmSandboxes(context.Context) (map[string]int, error) { return nil, nil }
func (s *flowStore) CreateWarmSandbox(_ context.Context, record sandbox.Record) error {
	s.add(record)
	return nil
}
func (s *flowStore) RecycleWarmSandboxes(context.Context, time.Time) (int, error) { return 0, nil }
func (s *flowStore) DeleteExpiredSandboxTokens(context.Context, time.Time) (int64, error) {
	return 0, nil
}
func (s *flowStore) SnapshotStates(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, nil
}
func (s *flowStore) SandboxCounts(context.Context) (map[sandbox.Kind]map[sandbox.Phase]int, error) {
	return nil, nil
}
func (s *flowStore) RevokeSandboxTokens(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked[id] = true
	return nil
}

func TestReconcilerLifecycle(t *testing.T) {
	h := newHarness(t, nil)
	store := newFlowStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reconciler := &sandbox.Reconciler{Store: store, Provider: h.provider, Config: h.coding, Owner: "test", Now: h.clock.Now, Logger: logger}
	store.add(sandbox.Record{ID: "sb1", Kind: sandbox.KindSession, Desired: sandbox.DesiredRunning, Size: "small", WorkspaceKey: "ws", SessionID: "s1", CreatedAt: h.clock.Now(), DeadlineAt: h.clock.Now().Add(time.Hour)})
	pass := func() {
		t.Helper()
		if _, err := reconciler.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		h.provider.Wait()
	}
	pass() // start: background create
	if got := store.get("sb1").Phase; got != sandbox.PhasePending {
		t.Fatalf("after start: %s", got)
	}
	pass() // observe: running
	record := store.get("sb1")
	if record.Phase != sandbox.PhaseRunning || record.ProviderRef != "sbx-001" || record.StartedAt == nil {
		t.Fatalf("after observe: %+v", record)
	}
	if got := string(h.only().Files["/tmp/flow/token"]); got != "fcs_minted_sb1" {
		t.Fatalf("minted token not delivered: %q", got)
	}
	// Flow restarts its reconciler (new provider, same server): still one sandbox.
	client, _ := NewClient(h.coding.OpenSandbox)
	restarted, _ := NewWithClient(sandbox.Dependencies{Config: h.coding, Now: h.clock.Now}, client)
	reconciler.Provider = restarted
	pass()
	if n := h.server.exact("POST /v1/sandboxes"); n != 1 || store.get("sb1").Phase != sandbox.PhaseRunning {
		t.Fatalf("restart duplicated the sandbox: %d creates, %+v", n, store.get("sb1"))
	}
	// The runner finishes; the session layer stops the sandbox.
	h.server.finishRunner("sbx-001", 0, "done")
	pass()
	if got := store.get("sb1").Phase; got != sandbox.PhaseSucceeded {
		t.Fatalf("after exit: %s", got)
	}
	store.setDesired("sb1", sandbox.DesiredStopped)
	pass()
	if record := store.get("sb1"); record.Phase != sandbox.PhaseMissing || !store.revoked["sb1"] {
		t.Fatalf("after stop: %+v", record)
	}
	if len(h.server.list()) != 0 {
		t.Fatal("the OpenSandbox sandbox must be deleted")
	}
}

func TestReconcilerQuotaWaitsWithoutSpendingAttempts(t *testing.T) {
	h := newHarness(t, nil)
	var quota atomic.Bool
	quota.Store(true)
	h.server.createHook = func(CreateRequest) *fakeError {
		if quota.Load() {
			return &fakeError{Status: 403, Code: "KUBERNETES::QUOTA_EXCEEDED", Message: "exceeded quota"}
		}
		return nil
	}
	store := newFlowStore()
	reconciler := &sandbox.Reconciler{Store: store, Provider: h.provider, Config: h.coding, Owner: "test", Now: h.clock.Now, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	store.add(sandbox.Record{ID: "sb1", Kind: sandbox.KindSession, Desired: sandbox.DesiredRunning, Size: "small", CreatedAt: h.clock.Now()})
	run := func() {
		_, _ = reconciler.RunOnce(context.Background())
		h.provider.Wait()
	}
	run() // pending (create in background)
	run() // Status: missing → start → reports the quota error
	record := store.get("sb1")
	if record.Phase != sandbox.PhasePending || record.Reason != sandbox.ReasonQuota {
		t.Fatalf("quota: %+v", record)
	}
	quota.Store(false)
	for i := 0; i < 3 && store.get("sb1").Phase != sandbox.PhaseRunning; i++ {
		run()
	}
	if got := store.get("sb1").Phase; got != sandbox.PhaseRunning {
		t.Fatalf("after capacity returned: %s", got)
	}
}

func TestOrphanGC(t *testing.T) {
	h := newHarness(t, nil)
	h.start(h.spec("live"))
	h.start(h.spec("orphan"))
	store := newFlowStore()
	store.add(sandbox.Record{ID: "live", Desired: sandbox.DesiredRunning})
	gc := &sandbox.GC{Store: store, Provider: h.provider, Config: h.coding, Now: h.clock.Now, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if deleted, err := gc.RunOnce(context.Background()); err != nil || deleted != 0 {
		t.Fatalf("young orphans are kept for the grace: %d %v", deleted, err)
	}
	h.clock.Advance(time.Hour)
	deleted, err := gc.RunOnce(context.Background())
	if err != nil || deleted != 1 {
		t.Fatalf("gc: %d %v", deleted, err)
	}
	remaining := h.server.list()
	if len(remaining) != 1 || remaining[0].Metadata[sandbox.LabelSandboxID] != "live" {
		t.Fatalf("remaining: %v", remaining)
	}
}
