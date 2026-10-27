package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// loopAppendStoreOpeners returns, per dialect, a function that opens the
// store: SQLite always, and MySQL or Postgres when
// FLOW_TEST_DATABASE_DRIVER and FLOW_TEST_DATABASE_URL name a throwaway
// database. Calling the opener again reopens the same database.
func loopAppendStoreOpeners(t *testing.T) map[string]func() *SQLiteStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flow.db")
	openers := map[string]func() *SQLiteStore{
		"sqlite": func() *SQLiteStore {
			repository, err := OpenSQLiteTestFixture(path)
			if err != nil {
				t.Fatal(err)
			}
			return repository
		},
	}
	if driver, url := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL"); driver != "" && url != "" {
		openers[driver] = func() *SQLiteStore {
			repository, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: url, FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 8, MaxIdleConns: 4})
			if err != nil {
				t.Fatal(err)
			}
			return repository
		}
	}
	return openers
}

func TestLoopDocumentAppendClaims(t *testing.T) {
	for name, open := range loopAppendStoreOpeners(t) {
		t.Run(name, func(t *testing.T) { testLoopDocumentAppendClaims(t, open) })
	}
}

func testLoopDocumentAppendClaims(t *testing.T, open func() *SQLiteStore) {
	ctx := context.Background()
	repository := open()
	defer func() { repository.Close() }()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	workspace, loopID, otherLoop := "lda-ws-"+suffix, "lda-loop-"+suffix, "lda-other-"+suffix
	secondWorkspace := "lda-ws2-" + suffix
	longWorkspace, longLoop := strings.Repeat("w", 191), strings.Repeat("循", 191)
	cleanup := func() {
		_ = repository.DeleteLoopRuns(ctx, workspace, loopID)
		_ = repository.DeleteLoopRuns(ctx, workspace, otherLoop)
		_ = repository.DeleteLoopRuns(ctx, secondWorkspace, loopID)
		_ = repository.DeleteLoopRuns(ctx, longWorkspace, longLoop)
	}
	cleanup()
	t.Cleanup(func() { cleanup() })
	claim := func(run, occurrence, document string) LoopDocumentAppend {
		return LoopDocumentAppend{LoopID: loopID, Occurrence: occurrence, DocumentID: document, RunID: run, TextKey: "key-" + run, TextProbe: "probe " + run, VersionBefore: 7}
	}
	const occurrence = "2026-10-27"

	// Creation is idempotent.
	for range 2 {
		if err := repository.ensureLoopDocumentAppends(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// A claim inserts; reading it back returns what was stored.
	first, won, err := repository.ClaimLoopDocumentAppend(ctx, workspace, claim("run-a", occurrence, "doc-1"))
	if err != nil || !won || first.Status != LoopAppendPending || first.RunID != "run-a" {
		t.Fatalf("first claim: %+v %v %v", first, won, err)
	}
	stored, err := repository.LoopDocumentAppend(ctx, workspace, loopID, occurrence, "doc-1")
	if err != nil || stored.RunID != "run-a" || stored.TextKey != "key-run-a" || stored.TextProbe != "probe run-a" || stored.VersionBefore != 7 || stored.VersionAfter != 0 || stored.Status != LoopAppendPending || stored.CreatedAt.IsZero() {
		t.Fatalf("stored claim: %+v %v", stored, err)
	}
	if _, err := repository.LoopDocumentAppend(ctx, workspace, loopID, occurrence, "doc-missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing claim error = %v, want sql.ErrNoRows", err)
	}

	// A second claim for the same key finds the first; other documents,
	// occurrences and workspaces are independent.
	again, won, err := repository.ClaimLoopDocumentAppend(ctx, workspace, claim("run-b", occurrence, "doc-1"))
	if err != nil || won || again.RunID != "run-a" || again.Status != LoopAppendPending {
		t.Fatalf("second claim: %+v %v %v", again, won, err)
	}
	for _, other := range []struct{ workspace, occurrence, document string }{
		{workspace, occurrence, "doc-2"},
		{workspace, "2026-10-28", "doc-1"},
		{secondWorkspace, occurrence, "doc-1"},
	} {
		if _, won, err := repository.ClaimLoopDocumentAppend(ctx, other.workspace, claim("run-c", other.occurrence, other.document)); err != nil || !won {
			t.Fatalf("independent claim %+v: %v %v", other, won, err)
		}
	}

	// Concurrent claims for one key: exactly one wins and everyone else sees
	// the winner.
	const racers = 6
	var winners atomic.Int32
	results := make([]LoopDocumentAppend, racers)
	var wait sync.WaitGroup
	start := make(chan struct{})
	for index := range racers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			run := fmt.Sprintf("racer-%d", index)
			got, won, err := repository.ClaimLoopDocumentAppend(ctx, workspace, claim(run, "2026-11-01", "doc-race"))
			if err != nil {
				t.Errorf("racer %d: %v", index, err)
				return
			}
			if won {
				winners.Add(1)
			}
			results[index] = got
		}()
	}
	close(start)
	wait.Wait()
	if winners.Load() != 1 {
		t.Fatalf("%d claims won, want 1", winners.Load())
	}
	stored, err = repository.LoopDocumentAppend(ctx, workspace, loopID, "2026-11-01", "doc-race")
	if err != nil {
		t.Fatal(err)
	}
	for index, got := range results {
		if got.RunID != stored.RunID {
			t.Fatalf("racer %d saw run %q, stored claim is %q", index, got.RunID, stored.RunID)
		}
	}

	// Takeover: the claim moves to the new run once; a second taker with the
	// same previous run loses.
	taker := claim("run-d", occurrence, "doc-1")
	taker.VersionBefore = 9
	if ok, err := repository.TakeOverLoopDocumentAppend(ctx, workspace, taker, "run-a"); err != nil || !ok {
		t.Fatalf("takeover: %v %v", ok, err)
	}
	if ok, err := repository.TakeOverLoopDocumentAppend(ctx, workspace, claim("run-e", occurrence, "doc-1"), "run-a"); err != nil || ok {
		t.Fatalf("second takeover: %v %v", ok, err)
	}
	stored, err = repository.LoopDocumentAppend(ctx, workspace, loopID, occurrence, "doc-1")
	if err != nil || stored.RunID != "run-d" || stored.TextKey != "key-run-d" || stored.VersionBefore != 9 || stored.Status != LoopAppendPending {
		t.Fatalf("after takeover: %+v %v", stored, err)
	}

	// The superseded run settling does not disturb the new owner's claim.
	if err := repository.SettleLoopDocumentAppend(ctx, workspace, claim("run-a", occurrence, "doc-1"), false, 0); err != nil {
		t.Fatal(err)
	}
	if err := repository.SettleLoopDocumentAppend(ctx, workspace, claim("run-a", occurrence, "doc-1"), true, 99); err != nil {
		t.Fatal(err)
	}
	if stored, err = repository.LoopDocumentAppend(ctx, workspace, loopID, occurrence, "doc-1"); err != nil || stored.RunID != "run-d" || stored.Status != LoopAppendPending || stored.VersionAfter != 0 {
		t.Fatalf("stale settle changed the claim: %+v %v", stored, err)
	}

	// Recording the append marks it appended; it can no longer be taken
	// over or released.
	if err := repository.SettleLoopDocumentAppend(ctx, workspace, taker, true, 10); err != nil {
		t.Fatal(err)
	}
	if stored, err = repository.LoopDocumentAppend(ctx, workspace, loopID, occurrence, "doc-1"); err != nil || stored.Status != LoopAppendAppended || stored.VersionAfter != 10 {
		t.Fatalf("after settle: %+v %v", stored, err)
	}
	if ok, err := repository.TakeOverLoopDocumentAppend(ctx, workspace, claim("run-f", occurrence, "doc-1"), "run-d"); err != nil || ok {
		t.Fatalf("takeover of an appended claim: %v %v", ok, err)
	}
	if err := repository.SettleLoopDocumentAppend(ctx, workspace, taker, false, 0); err != nil {
		t.Fatal(err)
	}
	if stored, err = repository.LoopDocumentAppend(ctx, workspace, loopID, occurrence, "doc-1"); err != nil || stored.Status != LoopAppendAppended {
		t.Fatalf("release removed an appended claim: %+v %v", stored, err)
	}
	if got, won, err := repository.ClaimLoopDocumentAppend(ctx, workspace, claim("run-g", occurrence, "doc-1")); err != nil || won || got.Status != LoopAppendAppended || got.RunID != "run-d" {
		t.Fatalf("claim after append: %+v %v %v", got, won, err)
	}

	// Release on failure: the pending claim goes away and a later run wins.
	if err := repository.SettleLoopDocumentAppend(ctx, workspace, claim("run-c", "2026-10-28", "doc-1"), false, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.LoopDocumentAppend(ctx, workspace, loopID, "2026-10-28", "doc-1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("released claim still there: %v", err)
	}
	if _, won, err := repository.ClaimLoopDocumentAppend(ctx, workspace, claim("run-h", "2026-10-28", "doc-1")); err != nil || !won {
		t.Fatalf("claim after release: %v %v", won, err)
	}

	// Long and non-ASCII values fit the key and text columns.
	long := LoopDocumentAppend{LoopID: longLoop, Occurrence: strings.Repeat("o", 64), DocumentID: strings.Repeat("文", 191), RunID: strings.Repeat("r", 191), TextKey: strings.Repeat("k", 191), TextProbe: strings.Repeat("探", 30000), VersionBefore: 1 << 40}
	if _, won, err := repository.ClaimLoopDocumentAppend(ctx, longWorkspace, long); err != nil || !won {
		t.Fatalf("long claim: %v %v", won, err)
	}
	if got, err := repository.LoopDocumentAppend(ctx, longWorkspace, longLoop, long.Occurrence, long.DocumentID); err != nil || got.TextProbe != long.TextProbe || got.VersionBefore != 1<<40 {
		t.Fatalf("long claim round trip: %v", err)
	}

	// Claims are kept across a reopen and creation does not reset them.
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	repository = open()
	if stored, err = repository.LoopDocumentAppend(ctx, workspace, loopID, occurrence, "doc-1"); err != nil || stored.RunID != "run-d" || stored.Status != LoopAppendAppended {
		t.Fatalf("after reopen: %+v %v", stored, err)
	}

	// LoopRun.Occurrence round-trips through the stored run.
	run := domain.LoopRun{ID: "lda-run-" + suffix, LoopID: loopID, Status: "completed", Trigger: "schedule", Occurrence: "2026-10-27T09", StartedAt: time.Now().UTC().Truncate(time.Second)}
	if err := repository.CreateLoopRun(ctx, workspace, run, LoopRunLease{}); err != nil {
		t.Fatal(err)
	}
	if got, err := repository.LoopRun(ctx, workspace, run.ID); err != nil || got.Occurrence != "2026-10-27T09" {
		t.Fatalf("run occurrence: %q %v", got.Occurrence, err)
	}
	updated, err := repository.UpdateLoopRun(ctx, workspace, run.ID, nil, func(next *domain.LoopRun) error {
		next.Output = "done"
		return nil
	})
	if err != nil || updated.Occurrence != "2026-10-27T09" {
		t.Fatalf("updated run occurrence: %q %v", updated.Occurrence, err)
	}
	if runs, err := repository.ListLoopRuns(ctx, workspace, loopID, 10); err != nil || len(runs) != 1 || runs[0].Occurrence != "2026-10-27T09" {
		t.Fatalf("listed runs: %+v %v", runs, err)
	}

	// Deleting a loop deletes its claims and leaves other loops' alone.
	otherClaim := claim("run-i", occurrence, "doc-1")
	otherClaim.LoopID = otherLoop
	if _, won, err := repository.ClaimLoopDocumentAppend(ctx, workspace, otherClaim); err != nil || !won {
		t.Fatalf("other loop claim: %v %v", won, err)
	}
	if err := repository.DeleteLoopRuns(ctx, workspace, loopID); err != nil {
		t.Fatal(err)
	}
	for _, key := range [][2]string{{occurrence, "doc-1"}, {"2026-10-28", "doc-1"}, {occurrence, "doc-2"}, {"2026-11-01", "doc-race"}} {
		if _, err := repository.LoopDocumentAppend(ctx, workspace, loopID, key[0], key[1]); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("claim %v survived loop deletion: %v", key, err)
		}
	}
	if _, err := repository.LoopRun(ctx, workspace, run.ID); !errors.Is(err, ErrLoopRunNotFound) {
		t.Fatalf("run survived loop deletion: %v", err)
	}
	if got, err := repository.LoopDocumentAppend(ctx, workspace, otherLoop, occurrence, "doc-1"); err != nil || got.RunID != "run-i" {
		t.Fatalf("other loop's claim: %+v %v", got, err)
	}
	// The deleted loop's key can be claimed again.
	if _, won, err := repository.ClaimLoopDocumentAppend(ctx, workspace, claim("run-j", occurrence, "doc-1")); err != nil || !won {
		t.Fatalf("claim after loop deletion: %v %v", won, err)
	}
}
