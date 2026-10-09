package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// openLoopRunTestStores opens the SQLite test fixture and, when
// FLOW_TEST_DATABASE_DRIVER and FLOW_TEST_DATABASE_URL name a throwaway
// MySQL or Postgres database, that database too.
func openLoopRunTestStores(t *testing.T) map[string]*SQLiteStore {
	t.Helper()
	stores := map[string]*SQLiteStore{}
	sqlite, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlite.Close() })
	stores["sqlite"] = sqlite
	if driver, url := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL"); driver != "" && url != "" {
		external, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: url, FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 4})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { external.Close() })
		if _, err := external.db.ExecContext(context.Background(), `DELETE FROM loop_run_events`); err != nil {
			t.Fatal(err)
		}
		if _, err := external.db.ExecContext(context.Background(), `DELETE FROM loop_run_records`); err != nil {
			t.Fatal(err)
		}
		stores[driver] = external
	}
	return stores
}

func TestLoopRunRecordsRoundTrip(t *testing.T) {
	for name, repository := range openLoopRunTestStores(t) {
		t.Run(name, func(t *testing.T) { testLoopRunRecordsRoundTrip(t, repository) })
	}
}

func testLoopRunRecordsRoundTrip(t *testing.T, repository *SQLiteStore) {
	ctx := context.Background()
	const workspace = "test-workspace"
	base := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	lease := LoopRunLease{Owner: "host-a:1:x", ExpiresAt: base.Add(time.Hour)}
	for index := 0; index < 4; index++ {
		run := domain.LoopRun{ID: "run_" + string(rune('a'+index)), LoopID: "loop_1", Status: "completed", Trigger: "manual", StartedAt: base.Add(time.Duration(index) * time.Minute)}
		owner := LoopRunLease{}
		if index == 3 {
			run.Status, owner = "running", lease
		}
		if err := repository.CreateLoopRun(ctx, workspace, run, owner); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.CreateLoopRun(ctx, workspace, domain.LoopRun{ID: "run_other", LoopID: "loop_2", Status: "failed", StartedAt: base}, LoopRunLease{}); err != nil {
		t.Fatal(err)
	}
	runs, err := repository.ListLoopRuns(ctx, workspace, "loop_1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 4 || runs[0].ID != "run_d" || runs[3].ID != "run_a" || runs[0].Status != "running" {
		t.Fatalf("runs = %#v", runs)
	}

	// Scoped updates append to the event log in order.
	updated, err := repository.UpdateLoopRun(ctx, workspace, "run_d", []LoopRunEvent{{Kind: "step", Data: json.RawMessage(`{"title":"Gathering"}`)}, {Kind: "tool_started"}}, func(run *domain.LoopRun) error {
		run.Steps = append(run.Steps, domain.LoopRunStep{Order: 1, Title: "Gathering"})
		run.Output = "Working"
		return nil
	})
	if err != nil || updated.Output != "Working" || len(updated.Steps) != 1 {
		t.Fatalf("update = %#v, %v", updated, err)
	}
	if _, err := repository.UpdateLoopRun(ctx, workspace, "run_d", []LoopRunEvent{{Kind: "ignored"}}, func(*domain.LoopRun) error { return ErrNoMutation }); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.UpdateLoopRun(ctx, workspace, "run_d", []LoopRunEvent{{Kind: "tool_finished"}}, func(*domain.LoopRun) error { return nil }); err != nil {
		t.Fatal(err)
	}
	events, err := repository.LoopRunEvents(ctx, workspace, "run_d", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Kind != "step" || events[0].Seq != 1 || events[2].Kind != "tool_finished" || events[2].Seq != 3 {
		t.Fatalf("events = %#v", events)
	}
	if later, _ := repository.LoopRunEvents(ctx, workspace, "run_d", 2); len(later) != 1 {
		t.Fatalf("events after 2 = %#v", later)
	}
	if _, err := repository.UpdateLoopRun(ctx, workspace, "missing", nil, func(*domain.LoopRun) error { return nil }); !errors.Is(err, ErrLoopRunNotFound) {
		t.Fatalf("missing run err = %v", err)
	}
	if _, err := repository.LoopRun(ctx, workspace, "missing"); !errors.Is(err, ErrLoopRunNotFound) {
		t.Fatalf("missing run read err = %v", err)
	}

	// Heartbeats renew only the holder's lease and carry cancel requests.
	now := time.Now().UTC()
	if cancel, err := repository.HeartbeatLoopRun(ctx, workspace, "run_d", lease.Owner, now, now.Add(time.Minute)); err != nil || cancel {
		t.Fatalf("heartbeat = %v, %v", cancel, err)
	}
	if _, err := repository.HeartbeatLoopRun(ctx, workspace, "run_d", "host-b:2:y", now, now.Add(time.Minute)); !errors.Is(err, ErrLoopRunLeaseLost) {
		t.Fatalf("foreign heartbeat err = %v", err)
	}
	if requested, err := repository.RequestLoopRunCancel(ctx, workspace, "run_d"); err != nil || !requested {
		t.Fatalf("cancel request = %v, %v", requested, err)
	}
	if requested, _ := repository.RequestLoopRunCancel(ctx, workspace, "run_a"); requested {
		t.Fatal("a run without a lease cannot be cancelled remotely")
	}
	if cancel, err := repository.HeartbeatLoopRun(ctx, workspace, "run_d", lease.Owner, now, now.Add(time.Minute)); err != nil || !cancel {
		t.Fatalf("heartbeat after cancel request = %v, %v", cancel, err)
	}
	running, err := repository.RunningLoopRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(running) != 1 || running[0].ID != "run_d" || running[0].LeaseOwner != lease.Owner || !running[0].CancelRequested || running[0].LeaseExpiresAt.Before(now) {
		t.Fatalf("running = %#v", running)
	}
	if err := repository.ReleaseLoopRunLease(ctx, workspace, "run_d", lease.Owner); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.HeartbeatLoopRun(ctx, workspace, "run_d", lease.Owner, now, now.Add(time.Minute)); !errors.Is(err, ErrLoopRunLeaseLost) {
		t.Fatalf("heartbeat after release err = %v", err)
	}
	// A reply claims the lease of a finished run; a second claim is refused.
	if err := repository.ClaimLoopRunLease(ctx, workspace, "run_a", LoopRunLease{Owner: "host-a:1:x", ExpiresAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repository.ClaimLoopRunLease(ctx, workspace, "run_a", LoopRunLease{Owner: "host-b:2:y", ExpiresAt: now.Add(time.Minute)}); !errors.Is(err, ErrLoopRunLeaseLost) {
		t.Fatalf("second claim err = %v", err)
	}
	if running, _ := repository.RunningLoopRuns(ctx); len(running) != 2 {
		t.Fatalf("running with a reply lease = %#v", running)
	}

	counts, err := repository.LoopRunCounts(ctx, workspace, base.Add(90*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if counts["loop_1"] != 2 || counts["loop_2"] != 0 {
		t.Fatalf("counts = %v", counts)
	}
	// Trimming keeps the newest runs and never a running one.
	if err := repository.TrimLoopRuns(ctx, workspace, "loop_1", 2); err != nil {
		t.Fatal(err)
	}
	if runs, _ := repository.ListLoopRuns(ctx, workspace, "loop_1", 10); len(runs) != 2 || runs[0].ID != "run_d" || runs[1].ID != "run_c" {
		t.Fatalf("trimmed = %#v", runs)
	}
	if err := repository.DeleteLoopRuns(ctx, workspace, "loop_1"); err != nil {
		t.Fatal(err)
	}
	if runs, _ := repository.ListLoopRuns(ctx, workspace, "loop_1", 10); len(runs) != 0 {
		t.Fatalf("deleted = %#v", runs)
	}
	if events, _ := repository.LoopRunEvents(ctx, workspace, "run_d", 0); len(events) != 0 {
		t.Fatalf("events of a deleted run = %#v", events)
	}
	if other, err := repository.LoopRun(ctx, workspace, "run_other"); err != nil || other.Status != "failed" {
		t.Fatalf("other loop's run = %#v, %v", other, err)
	}
}

// Runs kept in the workspace metadata by earlier versions move into
// loop_run_records on open, once, and leave the metadata.
func TestLoopRunsMigrateFromWorkspaceMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	repository, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 10, 9, 6, 0, 0, 0, time.UTC)
	finished := started.Add(time.Minute)
	legacy := []domain.LoopRun{
		{ID: "loop_run_508", LoopID: "loop_weekly", Status: "running", Trigger: "manual", StartedAt: started.Add(2 * time.Minute), Output: "Listing projects"},
		{ID: "loop_run_507", LoopID: "loop_weekly", Status: "completed", Trigger: "manual", StartedAt: started, FinishedAt: &finished, Output: "Posted updates", Feedback: []domain.LoopRunFeedback{{UserID: "usr_admin", Rating: "up", At: finished}}},
	}
	if err := repository.MutateWorkspace(context.Background(), "test-workspace", "loop.run_started", "loop_weekly", nil, func(data *domain.Bootstrap) error {
		data.LoopRuns = legacy
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// An earlier, interrupted migration already copied one run.
	if err := repository.CreateLoopRun(context.Background(), "test-workspace", legacy[1], LoopRunLease{}); err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		repository, err = OpenSQLiteTestFixture(path)
		if err != nil {
			t.Fatal(err)
		}
		if data, _ := repository.WorkspaceMetadataFields("test-workspace", "loopRuns"); len(data.LoopRuns) != 0 {
			t.Fatalf("attempt %d: runs still in metadata: %#v", attempt, data.LoopRuns)
		}
		runs, err := repository.ListLoopRuns(context.Background(), "test-workspace", "loop_weekly", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) != 2 || runs[0].ID != "loop_run_508" || runs[0].Status != "running" || runs[1].Output != "Posted updates" || len(runs[1].Feedback) != 1 {
			t.Fatalf("attempt %d: runs = %#v", attempt, runs)
		}
		// The interrupted run has no lease, so the reconciler picks it up.
		running, err := repository.RunningLoopRuns(context.Background())
		if err != nil || len(running) != 1 || running[0].ID != "loop_run_508" || running[0].LeaseOwner != "" {
			t.Fatalf("attempt %d: running = %#v, %v", attempt, running, err)
		}
		var migrated int
		if err := repository.db.QueryRowContext(context.Background(), `SELECT migrated_runs FROM loop_run_migrations WHERE workspace_key='test-workspace'`).Scan(&migrated); err != nil || migrated != 2 {
			t.Fatalf("attempt %d: checkpoint = %d, %v", attempt, migrated, err)
		}
		if err := repository.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
