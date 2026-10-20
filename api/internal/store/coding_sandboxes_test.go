package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openCodingSandboxStore(t *testing.T) *SQLiteStore {
	t.Helper()
	repo, err := OpenSQLite(filepath.Join(t.TempDir(), "coding.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	return repo
}

func insertTestSession(t *testing.T, repo *SQLiteStore, id, workspace, creator, source, sourceID string) {
	t.Helper()
	_, err := repo.db.ExecContext(context.Background(), `INSERT INTO coding_sessions(id,workspace_key,agent_session_id,creator_id,source,source_id,state,phase,phase_started_at,data,created_at,updated_at) VALUES(?,?,?,?,?,?,'active','running',0,?,0,0)`,
		id, workspace, "anchor-"+id, creator, source, sourceID, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
}

func TestCodingSandboxLeaseObserveAndSettle(t *testing.T) {
	repo := openCodingSandboxStore(t)
	ctx := context.Background()
	insertTestSession(t, repo, "sess1", "acme", "user1", "loop", "loop9")
	deadline := time.Now().Add(4 * time.Hour).Truncate(time.Millisecond).UTC()
	row := CodingSandbox{ID: "s1", WorkspaceKey: "acme", SessionID: "sess1", Kind: "session", Size: "medium", Region: "eu", Image: "img:1",
		VolumeID: "v1", ArchiveURL: "http://flow/archive", Env: map[string]string{"A": "1"}, DeadlineAt: deadline}
	if err := repo.insertCodingSandbox(ctx, repo.db, row, "fake"); err != nil {
		t.Fatal(err)
	}
	if err := repo.insertCodingSandbox(ctx, repo.db, row, "fake"); err != nil {
		t.Fatalf("insert is idempotent: %v", err)
	}
	leased, err := repo.LeaseSandboxes(ctx, "owner-a", 30*time.Second, 10)
	if err != nil || len(leased) != 1 {
		t.Fatalf("lease = %+v %v", leased, err)
	}
	got := leased[0]
	if got.Desired != "running" || got.Phase != "pending" || got.UserID != "user1" || got.LoopID != "loop9" || got.VolumeID != "v1" ||
		got.ArchiveURL != "http://flow/archive" || got.Env["A"] != "1" || got.Image != "img:1" || !got.DeadlineAt.Equal(deadline) {
		t.Fatalf("record = %+v", got)
	}
	// A second owner cannot lease it while the lease holds.
	if again, _ := repo.LeaseSandboxes(ctx, "owner-b", 30*time.Second, 10); len(again) != 0 {
		t.Fatal("double lease")
	}
	if err := repo.SaveSandboxObservationAt(ctx, "owner-b", "s1", CodingSandboxObservation{Phase: "running"}, 1, time.Now()); !errors.Is(err, ErrCodingLeaseLost) {
		t.Fatalf("a foreign save must fail: %v", err)
	}
	started := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	if err := repo.SaveSandboxObservationAt(ctx, "owner-a", "s1", CodingSandboxObservation{Phase: "running", ProviderRef: "flow-cs-s1", StartedAt: &started, Message: "ok"}, 1, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if ok, err := repo.AddSandboxBilledSeconds(ctx, "owner-a", "s1", 0, 300); ok || err != nil {
		t.Fatal("billing needs the lease")
	}
	leased, _ = repo.LeaseSandboxes(ctx, "owner-b", 30*time.Second, 10)
	if len(leased) != 1 || leased[0].Phase != "running" || leased[0].ProviderRef != "flow-cs-s1" || leased[0].Attempts != 1 || leased[0].StartedAt == nil || !leased[0].StartedAt.Equal(started) || leased[0].Message != "ok" {
		t.Fatalf("after observation = %+v", leased)
	}
	if ok, err := repo.AddSandboxBilledSeconds(ctx, "owner-b", "s1", 0, 300); !ok || err != nil {
		t.Fatalf("billing = %v %v", ok, err)
	}
	if ok, _ := repo.AddSandboxBilledSeconds(ctx, "owner-b", "s1", 0, 600); ok {
		t.Fatal("billing CAS must reject a stale from")
	}
	if err := repo.SetSandboxLogsKey(ctx, "owner-b", "s1", "coding-logs-x-s1.log"); err != nil {
		t.Fatal(err)
	}
	// Stop: desired=stopped, observed missing → settled, never leased again.
	if _, err := repo.db.ExecContext(ctx, `UPDATE coding_sandboxes SET desired='stopped' WHERE id='s1'`); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveSandboxObservationAt(ctx, "owner-b", "s1", CodingSandboxObservation{Phase: "missing"}, 1, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if again, _ := repo.LeaseSandboxes(ctx, "owner-c", 30*time.Second, 10); len(again) != 0 {
		t.Fatalf("settled rows must not be leased: %+v", again)
	}
	rows, _ := repo.codingSandboxesByID(ctx, []string{"s1"})
	if rows[0].EndedAt == nil || rows[0].BilledSeconds != 300 || rows[0].LogsKey != "coding-logs-x-s1.log" || rows[0].ProviderRef != "flow-cs-s1" {
		t.Fatalf("final row = %+v", rows[0])
	}
	known, err := repo.KnownSandboxIDs(ctx, []string{"s1", "nope"})
	if err != nil || len(known) != 0 {
		t.Fatalf("settled rows are not live: %v %v", known, err)
	}
	counts, _ := repo.SandboxCounts(ctx)
	if len(counts) != 0 {
		t.Fatalf("counts = %v", counts)
	}
	if err := repo.insertCodingSandbox(ctx, repo.db, CodingSandbox{ID: "Bad-ID"}, ""); err == nil {
		t.Fatal("invalid ids are rejected")
	}
}

func TestCodingSandboxConcurrentLeasesNeverOverlap(t *testing.T) {
	repo := openCodingSandboxStore(t)
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		id := "s" + strings.Repeat("a", i+1)
		if err := repo.insertCodingSandbox(ctx, repo.db, CodingSandbox{ID: id, Kind: "session", Size: "small"}, ""); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	owners := map[string]string{}
	var group sync.WaitGroup
	for _, owner := range []string{"a", "b", "c", "d"} {
		group.Add(1)
		go func(owner string) {
			defer group.Done()
			rows, err := repo.LeaseSandboxes(ctx, owner, time.Minute, 7)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, row := range rows {
				if previous, ok := owners[row.ID]; ok {
					t.Errorf("%s leased by %s and %s", row.ID, previous, owner)
				}
				owners[row.ID] = owner
			}
		}(owner)
	}
	group.Wait()
	// Concurrent claimers may lose CAS races (fewer rows each), never overlap;
	// whatever they lost is still due for the next claimer.
	for len(owners) < 20 {
		rows, err := repo.LeaseSandboxes(ctx, "e", time.Minute, 20)
		if err != nil || len(rows) == 0 {
			break
		}
		for _, row := range rows {
			if previous, ok := owners[row.ID]; ok {
				t.Fatalf("%s leased by %s and e", row.ID, previous)
			}
			owners[row.ID] = "e"
		}
	}
	if len(owners) != 20 {
		t.Fatalf("leased %d of 20", len(owners))
	}
}

func TestCodingSandboxTokensWarmPoolAndBinding(t *testing.T) {
	repo := openCodingSandboxStore(t)
	ctx := context.Background()
	now := time.Now()
	for _, id := range []string{"w1", "w2", "w3"} {
		if err := repo.CreateWarmSandbox(ctx, CodingSandbox{ID: id, Size: "medium", Region: "eu", Desired: "running", CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.CreateWarmSandbox(ctx, CodingSandbox{ID: "old", Size: "medium", Region: "eu", CreatedAt: now.Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	counts, err := repo.WarmSandboxes(ctx)
	if err != nil || counts["medium/eu"] != 4 {
		t.Fatalf("warm counts = %v %v", counts, err)
	}
	token, err := repo.MintSandboxToken(ctx, CodingSandbox{ID: "w1", WorkspaceKey: ""}, time.Hour)
	if err != nil || !strings.HasPrefix(token, "fcs_") || len(token) < 40 {
		t.Fatalf("token = %q %v", token, err)
	}
	var stored string
	if err := repo.db.QueryRowContext(ctx, `SELECT token_hash FROM coding_sandbox_tokens WHERE sandbox_id='w1'`).Scan(&stored); err != nil || stored != tokenHash(token) || strings.Contains(stored, token) {
		t.Fatalf("only the hash may be stored: %q", stored)
	}
	if _, err := repo.MintSandboxToken(ctx, CodingSandbox{ID: "w2"}, -time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE coding_sandbox_tokens SET expires_at=? WHERE sandbox_id='w2'`, millis(now.Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}
	if n, err := repo.DeleteExpiredSandboxTokens(ctx, now); err != nil || n != 1 {
		t.Fatalf("expired sweep = %d %v", n, err)
	}

	// Only running warm sandboxes are bound.
	if _, err := repo.db.ExecContext(ctx, `UPDATE coding_sandboxes SET phase='running',started_at=? WHERE id IN ('w1','w2')`, millis(now.Add(-10*time.Minute))); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.BindWarmCodingSandbox(ctx, "medium", "eu", "missing-session", ""); !errors.Is(err, ErrCodingNotFound) {
		t.Fatalf("unknown session = %v", err)
	}
	insertTestSession(t, repo, "sess1", "acme", "user1", "issue", "")
	insertTestSession(t, repo, "sess2", "acme", "user1", "issue", "")
	insertTestSession(t, repo, "sess3", "acme", "user1", "issue", "")
	bound, err := repo.BindWarmCodingSandbox(ctx, "medium", "eu", "sess1", "")
	if err != nil || bound.ID != "w1" || bound.SessionID != "sess1" || bound.Kind != "session" || bound.WorkspaceKey != "acme" || bound.BilledSeconds < 599 {
		t.Fatalf("bound = %+v %v", bound, err)
	}
	var tokenSession string
	_ = repo.db.QueryRowContext(ctx, `SELECT session_id FROM coding_sandbox_tokens WHERE sandbox_id='w1'`).Scan(&tokenSession)
	if tokenSession != "sess1" {
		t.Fatalf("the token must move to the session, got %q", tokenSession)
	}
	if second, err := repo.BindWarmCodingSandbox(ctx, "medium", "", "sess2", ""); err != nil || second.ID != "w2" {
		t.Fatalf("second bind = %+v %v", second, err)
	}
	if _, err := repo.BindWarmCodingSandbox(ctx, "medium", "eu", "sess3", ""); !errors.Is(err, ErrCodingNotFound) {
		t.Fatalf("no running warm sandbox left = %v", err)
	}
	// Recycle: the old one (age) and a failed one.
	if _, err := repo.db.ExecContext(ctx, `UPDATE coding_sandboxes SET phase='failed' WHERE id='w3'`); err != nil {
		t.Fatal(err)
	}
	if n, err := repo.RecycleWarmSandboxes(ctx, now.Add(-time.Hour)); err != nil || n != 2 {
		t.Fatalf("recycled %d %v", n, err)
	}
	if counts, _ := repo.WarmSandboxes(ctx); counts["medium/eu"] != 0 {
		t.Fatalf("counts after recycle = %v", counts)
	}
	if err := repo.RevokeSandboxTokens(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	var revoked int64
	_ = repo.db.QueryRowContext(ctx, `SELECT revoked_at FROM coding_sandbox_tokens WHERE sandbox_id='w1'`).Scan(&revoked)
	if revoked == 0 {
		t.Fatal("token not revoked")
	}
	known, _ := repo.KnownSandboxIDs(ctx, []string{"w1", "w2", "old", "zzz"})
	if !known["w1"] || !known["w2"] || !known["old"] || known["zzz"] {
		t.Fatalf("known = %v", known)
	}
	counts2, _ := repo.SandboxCounts(ctx)
	if counts2["session"]["running"] != 2 {
		t.Fatalf("sandbox counts = %v", counts2)
	}
}

func TestCodingSnapshotStatesAndRuntimeBilling(t *testing.T) {
	repo := openCodingSandboxStore(t)
	ctx := context.Background()
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO coding_environment_snapshots(id,workspace_key,environment_id,status,strategy,config_hash,data,created_at,updated_at) VALUES('snap1','acme','env1','ready','archive','h',?,0,0)`, []byte(`{"requestedBy":"user7"}`)); err != nil {
		t.Fatal(err)
	}
	states, err := repo.SnapshotStates(ctx, []string{"snap1", "gone"})
	if err != nil || states["snap1"] != "ready" || len(states) != 1 {
		t.Fatalf("states = %v %v", states, err)
	}
	// A snapshot build is billed to its requester.
	if err := repo.insertCodingSandbox(ctx, repo.db, CodingSandbox{ID: "b1", WorkspaceKey: "acme", SnapshotID: "snap1", Kind: "snapshot", Size: "small"}, ""); err != nil {
		t.Fatal(err)
	}
	// A debug terminal is billed to the user stored on the row.
	if err := repo.insertCodingSandbox(ctx, repo.db, CodingSandbox{ID: "d1", WorkspaceKey: "acme", Kind: "debug", Size: "small", UserID: "user8"}, ""); err != nil {
		t.Fatal(err)
	}
	rows, err := repo.LeaseSandboxes(ctx, "o", time.Minute, 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %+v %v", rows, err)
	}
	users := map[string]string{}
	for _, row := range rows {
		users[row.ID] = row.UserID
	}
	if users["b1"] != "user7" || users["d1"] != "user8" {
		t.Fatalf("users = %v", users)
	}
}
