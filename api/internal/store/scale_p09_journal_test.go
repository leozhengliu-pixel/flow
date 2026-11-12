package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

type scaleP09Fixture struct {
	*SQLiteStore
	data domain.Bootstrap
}

func scaleP09Database(t *testing.T) scaleP09Fixture {
	t.Helper()
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if (driver == "") != (databaseURL == "") {
		t.Fatal("P09 requires both FLOW_TEST_DATABASE_DRIVER and FLOW_TEST_DATABASE_URL")
	}
	config := DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4}
	if driver == "" {
		config.Driver, config.Path = "sqlite", filepath.Join(t.TempDir(), "p09.db")
	} else if driver == "sqlite" {
		config.Path = databaseURL
	}
	repo, err := OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	user, token, err := repo.Register(t.Context(), "P09 owner", idgen.New("scale-p09-")+"@example.test", "p09-fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	workspace := ""
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if workspace != "" {
			if err := repo.DeleteWorkspace(ctx, workspace); err != nil {
				t.Errorf("clean up owned workspace: %v", err)
			}
		}
		if err := repo.EraseAccount(ctx, user.ID); err != nil {
			t.Errorf("clean up owned account: %v", err)
		}
	})
	if err := repo.VerifyEmail(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	user.EmailVerified = true
	data, err := repo.CreateWorkspace(ContextWithActor(t.Context(), user), "P09 isolated", idgen.New("scale-p09-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	workspace = data.Workspace.URLKey
	return scaleP09Fixture{repo, data}
}

func scaleP09Head(t *testing.T, f scaleP09Fixture) string {
	t.Helper()
	head, err := f.RecordJournalHead(t.Context(), f.data.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	return head
}

func scaleP09Put(t *testing.T, tx *sqlTx, workspace, kind, id string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,0,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET data=excluded.data`, workspace, kind, id, raw); err != nil {
		t.Fatal(err)
	}
}

func scaleP09Event(t *testing.T, f scaleP09Fixture, tx *sqlTx, payload []byte) string {
	t.Helper()
	id := idgen.New("p09-event-")
	if _, err := insertDomainEvent(t.Context(), tx, domainEventRow{ID: id, Type: "project.updated", WorkspaceKey: f.data.Workspace.URLKey, WorkspaceID: f.data.Workspace.ID, AggregateID: "p09-project", Payload: payload, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestScaleP09TransactionalJournal(t *testing.T) {
	for _, mode := range []string{"shadow", "on"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_JOURNAL", mode)
			f := scaleP09Database(t)
			workspace := f.data.Workspace.URLKey
			after := scaleP09Head(t, f)
			tx, err := f.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			project := domain.Project{ID: "p09-project", Name: "First", Description: "private full body"}
			raw, _ := json.Marshal(project)
			if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"projects", project.ID}, metadataRecord{data: raw}, nil); err != nil {
				t.Fatal(err)
			}
			// The separate team/bulk helper must share the same journal.
			if err := writeMetadataRecordUpserts(t.Context(), tx, workspace, []metadataRecordChange{{field: "teamSettings", key: "p09-team", raw: json.RawMessage(`{"teamId":"p09-team","timezone":"UTC"}`)}}); err != nil {
				t.Fatal(err)
			}
			payload := []byte(`{"name":"First","legacy":null}`)
			event := scaleP09Event(t, f, tx, payload)
			second := scaleP09Event(t, f, tx, []byte(`{"second":true}`))
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			page, err := f.ReadCommittedChanges(t.Context(), f.data.Workspace.ID, after, 100, 256<<10)
			if err != nil || len(page.Transactions) != 1 || page.ResyncRequired || page.HasMore {
				t.Fatalf("transaction page: %+v %v", page, err)
			}
			change := page.Transactions[0]
			secondCursor, cursorErr := f.RecordJournalEventSeq(t.Context(), second)
			if cursorErr != nil || len(change.Records) != 2 || change.Seq != secondCursor || page.Next != change.Seq || change.AuthzEpoch != page.AuthzEpoch {
				t.Fatalf("journal contract: %+v", page)
			}
			encoded, _ := json.Marshal(change)
			if len(encoded) > 2048 || bytes.Contains(encoded, []byte("private full body")) {
				t.Fatalf("journal copied full row or exceeded small-write budget: %d bytes", len(encoded))
			}
			var canonical, eventRaw, summary []byte
			if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key=?`, workspace, project.ID).Scan(&canonical); err != nil {
				t.Fatal(err)
			}
			if err := f.db.QueryRowContext(t.Context(), `SELECT payload FROM domain_events WHERE id=?`, event).Scan(&eventRaw); err != nil {
				t.Fatal(err)
			}
			if err := f.db.QueryRowContext(t.Context(), `SELECT summary FROM metadata_catalog WHERE workspace_key=? AND field='projects' AND record_key=?`, workspace, project.ID).Scan(&summary); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(canonical, raw) || !bytes.Equal(payload, eventRaw) || bytes.Contains(summary, []byte("private full body")) || !bytes.Contains(summary, []byte("First")) {
				t.Fatal("canonical/event payload changed or summary is not bounded")
			}
			t.Logf("small_write_journal_bytes=%d records=%d", len(encoded), len(change.Records))
		})
	}
}

func TestScaleP09RollbackIsAtomic(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "shadow")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	after := scaleP09Head(t, f)
	epoch, err := f.AuthzEpoch(t.Context(), f.data.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("p09 precommit fault")
	hook := recordJournalCommitHook(func(context.Context, *sqlTx, string, []RecordDelta) error { return injected })
	f.db.journalCommitHook.Store(&hook)
	t.Cleanup(func() { f.db.journalCommitHook.Store(nil) })
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	scaleP09Put(t, tx, workspace, "projects", "p09-rollback", domain.Project{ID: "p09-rollback", Name: "Must vanish"})
	event := scaleP09Event(t, f, tx, []byte(`{}`))
	if err := tx.Commit(); !errors.Is(err, injected) {
		t.Fatalf("injected failure = %v", err)
	}
	f.db.journalCommitHook.Store(nil)
	for _, table := range []string{"workspace_metadata_records", "metadata_catalog", "record_versions"} {
		var n int
		if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table+` WHERE workspace_key=? AND field='projects' AND record_key='p09-rollback'`, workspace).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s survived rollback: %d %v", table, n, err)
		}
	}
	var events int
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domain_events WHERE id=?`, event).Scan(&events); err != nil || events != 0 {
		t.Fatalf("event survived rollback: %d %v", events, err)
	}
	got, err := f.AuthzEpoch(t.Context(), f.data.Workspace.ID)
	if err != nil || got != epoch || scaleP09Head(t, f) != after {
		t.Fatalf("epoch/head survived rollback: %s -> %s: %v", epoch, got, err)
	}
}

func TestScaleP09DirectSQLVersionsAndTombstones(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "shadow")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	after := scaleP09Head(t, f)
	key := RecordKey{"projects", "p09-recreate"}
	for index, query := range []string{
		`INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'projects','p09-recreate',0,'{"id":"p09-recreate","name":"one"}')`,
		`DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key='p09-recreate'`,
		`INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'projects','p09-recreate',0,'{"id":"p09-recreate","name":"three"}')`,
	} {
		if _, err := f.db.ExecContext(t.Context(), query, workspace); err != nil {
			t.Fatal(err)
		}
		version, deleted, err := f.RecordVersion(t.Context(), workspace, key)
		if err != nil || version != strconv.Itoa(index+1) || deleted != (index == 1) {
			t.Fatalf("version %d: %s %t %v", index, version, deleted, err)
		}
		page, err := f.ReadCommittedChanges(t.Context(), f.data.Workspace.ID, after, 1, 1024)
		if err != nil || len(page.Transactions) != 1 || len(page.Transactions[0].Records) != 1 || page.Transactions[0].Records[0].Deleted != deleted {
			t.Fatalf("direct SQL journal: %+v %v", page, err)
		}
		after = page.Next
	}
	if version, _, err := f.RecordVersion(t.Context(), workspace+"-other", key); err != nil || version != "" {
		t.Fatalf("cross-workspace version leaked: %s %v", version, err)
	}
}

func TestScaleP09OldWritersFailCoverageAndRevokeAccess(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "shadow")
	f := scaleP09Database(t)
	access, err := f.ResolveAccess(t.Context(), f.data.Workspace.URLKey, f.data.Viewer.ID, nil)
	if err != nil || strings.HasPrefix(access.AuthzEpoch, "p03:") {
		t.Fatalf("authoritative epoch missing: %+v %v", access, err)
	}
	after := scaleP09Head(t, f)
	// Bypass sqlDatabase exactly as an old replica or direct SQL tool does.
	if _, err := f.db.DB.ExecContext(t.Context(), rewriteSQL(`UPDATE workspace_memberships SET role='member' WHERE workspace_id=? AND user_id=?`, f.dialect), f.data.Workspace.ID, f.data.Viewer.ID); err != nil {
		t.Fatal(err)
	}
	for _, batch := range []string{"queued", "replay"} {
		t.Run(batch, func(t *testing.T) {
			if err := f.ValidateAccess(t.Context(), access); !errors.Is(err, ErrAuthForbidden) {
				t.Fatalf("old writer did not invalidate access: %v", err)
			}
		})
	}
	page, err := f.ReadCommittedChanges(t.Context(), f.data.Workspace.ID, after, 100, 4096)
	if !errors.Is(err, ErrRecordJournalCoverage) || !page.ResyncRequired || len(page.Transactions) != 0 {
		t.Fatalf("uncovered writer silently served: %+v %v", page, err)
	}
	remaining, err := f.RepairRecordJournal(t.Context(), f.data.Workspace.ID)
	if err != nil || remaining {
		t.Fatalf("bounded old-writer repair: remaining=%t %v", remaining, err)
	}
	page, err = f.ReadCommittedChanges(t.Context(), f.data.Workspace.ID, after, 100, 4096)
	if err != nil || len(page.Transactions) != 1 || len(page.Transactions[0].Records) != 0 || !slices.Contains(page.Transactions[0].Invalidations, "workspace") {
		t.Fatalf("repair published partial records or failed recovery: %+v %v", page, err)
	}
}

func TestScaleP09AuthorizationWriters(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "shadow")
	f := scaleP09Database(t)
	workspace, workspaceID, user := f.data.Workspace.URLKey, f.data.Workspace.ID, f.data.Viewer.ID
	// Enumerate ACL, key, membership and account writers. Use one identity
	// with its own workspace; never assume a seeded admin exists in CI.
	writes := []struct {
		name, query string
		args        []any
	}{
		{"workspace-role", `UPDATE workspace_memberships SET role='admin' WHERE workspace_id=? AND user_id=?`, []any{workspaceID, user}},
		{"workspace-status", `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, []any{workspaceID, user}},
		{"workspace-resume", `UPDATE workspace_memberships SET status='active' WHERE workspace_id=? AND user_id=?`, []any{workspaceID, user}},
		{"workspace-delete", `DELETE FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, []any{workspaceID, user}},
		{"workspace-insert", `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at,last_seen_at) VALUES(?,?,'admin','active','','')`, []any{workspaceID, user}},
		{"team-insert", `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) VALUES(?,'p09-private',?,'member','')`, []any{workspaceID, user}},
		{"team-role", `UPDATE team_memberships SET role='owner' WHERE workspace_id=? AND team_id='p09-private' AND user_id=?`, []any{workspaceID, user}},
		{"team-origin", `UPDATE team_memberships SET origin='scim' WHERE workspace_id=? AND team_id='p09-private' AND user_id=?`, []any{workspaceID, user}},
		{"team-delete", `DELETE FROM team_memberships WHERE workspace_id=? AND team_id='p09-private' AND user_id=?`, []any{workspaceID, user}},
		{"account-deactivate", `UPDATE auth_users SET active=0 WHERE id=?`, []any{user}},
		{"account-activate", `UPDATE auth_users SET active=1 WHERE id=?`, []any{user}},
		{"key-create", `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'apiKeys','p09-key',0,'{"id":"p09-key","scopes":["read"],"teamRestriction":"selected","teamIds":[]}')`, []any{workspace}},
		{"key-scope", `UPDATE workspace_metadata_records SET data='{"id":"p09-key","scopes":["read","write"],"teamRestriction":"selected","teamIds":["p09-private"]}' WHERE workspace_key=? AND field='apiKeys' AND record_key='p09-key'`, []any{workspace}},
		{"key-revoke", `UPDATE workspace_metadata_records SET data='{"id":"p09-key","revokedAt":"2026-11-11T00:00:00Z"}' WHERE workspace_key=? AND field='apiKeys' AND record_key='p09-key'`, []any{workspace}},
		{"key-delete", `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='apiKeys' AND record_key='p09-key'`, []any{workspace}},
		{"document-acl", `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'documents','p09-document',0,'{"id":"p09-document","permissions":[{"subjectType":"user","subjectId":"guest","role":"viewer"}]}')`, []any{workspace}},
		{"document-acl-update", `UPDATE workspace_metadata_records SET data='{"id":"p09-document","permissions":[]}' WHERE workspace_key=? AND field='documents' AND record_key='p09-document'`, []any{workspace}},
		{"document-acl-delete", `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='documents' AND record_key='p09-document'`, []any{workspace}},
		{"issue-acl-insert", `INSERT INTO issue_permission_records(workspace_key,issue_id,subject_type,subject_id,role) VALUES(?,'p09-issue','user','guest','viewer')`, []any{workspace}},
		{"issue-acl-update", `UPDATE issue_permission_records SET role='editor' WHERE workspace_key=? AND issue_id='p09-issue'`, []any{workspace}},
		{"issue-acl-delete", `DELETE FROM issue_permission_records WHERE workspace_key=? AND issue_id='p09-issue'`, []any{workspace}},
	}
	for _, write := range writes {
		t.Run(write.name, func(t *testing.T) {
			before, err := f.AuthzEpoch(t.Context(), workspaceID)
			if err != nil {
				t.Fatal(err)
			}
			after := scaleP09Head(t, f)
			if _, err := f.db.ExecContext(t.Context(), write.query, write.args...); err != nil {
				t.Fatal(err)
			}
			current, err := f.AuthzEpoch(t.Context(), workspaceID)
			if err != nil || before == current {
				t.Fatalf("writer did not advance epoch: %s -> %s %v", before, current, err)
			}
			page, err := f.ReadCommittedChanges(t.Context(), workspaceID, after, 100, 4096)
			if err != nil || len(page.Transactions) != 1 || page.Transactions[0].AuthzEpoch != current {
				t.Fatalf("writer not journaled atomically: %+v %v", page, err)
			}
		})
	}
	before, _ := f.AuthzEpoch(t.Context(), workspaceID)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET last_seen_at=? WHERE workspace_id=? AND user_id=?`, time.Now().UTC().Format(time.RFC3339Nano), workspaceID, user); err != nil {
		t.Fatal(err)
	}
	if after, _ := f.AuthzEpoch(t.Context(), workspaceID); before != after {
		t.Fatal("activity telemetry changed authorization")
	}
}

func TestScaleP09BoundedTransactions(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "shadow")
	f := scaleP09Database(t)
	observed := 0
	f.setRecordJournalCommitHook(func(_ context.Context, _ *sqlTx, _ string, records []RecordDelta) error {
		observed += len(records)
		return nil
	})
	t.Cleanup(func() { f.setRecordJournalCommitHook(nil) })
	after := scaleP09Head(t, f)
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := range 700 {
		scaleP09Put(t, tx, f.data.Workspace.URLKey, "projects", fmt.Sprintf("p09-bulk-%04d", i), domain.Project{ID: fmt.Sprintf("p09-bulk-%04d", i), Name: strings.Repeat("title", 20)})
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.setRecordJournalCommitHook(nil)
	if observed != 700 {
		t.Fatalf("commit participant missed bulk records: %d", observed)
	}
	page, err := f.ReadCommittedChanges(t.Context(), f.data.Workspace.ID, after, 100, 1024)
	if err != nil || len(page.Transactions) != 1 || len(page.Transactions[0].Records) != 0 || !slices.Contains(page.Transactions[0].Invalidations, "workspace") || page.HasMore {
		t.Fatalf("oversized transaction was split or unbounded: %+v %v", page, err)
	}
	raw, _ := json.Marshal(page)
	if len(raw) > 1024 {
		t.Fatalf("bounded page = %d bytes", len(raw))
	}
}

func TestScaleP09OffRestoresLegacyWrites(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	f := scaleP09Database(t)
	after := scaleP09Head(t, f)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET role='admin' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, f.data.Viewer.ID); err != nil {
		t.Fatal(err)
	}
	if scaleP09Head(t, f) != after {
		t.Fatal("disabled journal allocated a cursor")
	}
	if _, err := f.ReadCommittedChanges(t.Context(), f.data.Workspace.ID, after, 100, 4096); !errors.Is(err, ErrRecordJournalUnavailable) {
		t.Fatalf("disabled journal served changes: %v", err)
	}
	for input, want := range map[string]string{"": "off", "unknown": "off", "on": "on", "shadow": "shadow", "off": "off"} {
		t.Setenv("FLOW_RECORD_JOURNAL", input)
		if got := recordJournalMode(); got != want {
			t.Fatalf("mode %q = %q, want %q", input, got, want)
		}
	}
}

func TestScaleP09CommitBeforeNotificationRecovery(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "shadow")
	f := scaleP09Database(t)
	after := scaleP09Head(t, f)
	// Simulate losing the writer after durable commit, before sequencing and
	// notification. The reader's existing stale-sequencing rescue must finish it.
	f.db.commitHooks.Store(&commitHooks{afterCommit: func(tx *sqlTx) { tx.sequencing = nil }})
	t.Cleanup(func() { f.db.commitHooks.Store(nil) })
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	scaleP09Put(t, tx, f.data.Workspace.URLKey, "projects", "p09-recover", domain.Project{ID: "p09-recover", Name: "Durable"})
	event := scaleP09Event(t, f, tx, []byte(`{"name":"Durable"}`))
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.db.commitHooks.Store(nil)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE domain_event_sequencing SET created_ms=0 WHERE workspace_id=?`, f.data.Workspace.ID); err != nil {
		t.Fatal(err)
	}
	page, err := f.ReadCommittedChanges(t.Context(), f.data.Workspace.ID, after, 100, 4096)
	if err != nil || len(page.Transactions) != 1 || f.DomainEventSeq(t.Context(), event) == 0 {
		t.Fatalf("committed journal not recoverable: %+v %v", page, err)
	}
}

func TestScaleP09LegacySequencesRemainGapFree(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "shadow")
	f := scaleP09Database(t)
	legacy, err := f.WorkspaceChangeHead(t.Context(), f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	after := scaleP09Head(t, f)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET role='admin' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, f.data.Viewer.ID); err != nil {
		t.Fatal(err)
	}
	current, err := f.WorkspaceChangeHead(t.Context(), f.data.Workspace.URLKey)
	if err != nil || current != legacy {
		t.Fatalf("journal-only write changed legacy head: %d -> %d %v", legacy, current, err)
	}
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	event := scaleP09Event(t, f, tx, []byte(`{"name":"Existing payload"}`))
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if seq := f.DomainEventSeq(t.Context(), event); seq != legacy+1 {
		t.Fatalf("legacy event sequence has a gap: got %d after %d", seq, legacy)
	}
	page, err := f.ReadCommittedChanges(t.Context(), f.data.Workspace.ID, after, 100, 4096)
	if err != nil || len(page.Transactions) != 2 {
		t.Fatalf("journal stream lost an eventless transaction: %+v %v", page, err)
	}
	if !slices.Contains(page.Transactions[1].Invalidations, "workspace") {
		t.Fatal("event-only side effects were acknowledged as an empty change")
	}
}

func TestScaleP09OrderedPagesAndRetention(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	after := scaleP09Head(t, f)
	for i := range 3 {
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		scaleP09Put(t, tx, workspace, "projects", "p09-versioned", domain.Project{ID: "p09-versioned", Name: fmt.Sprintf("Version %d", i)})
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := f.ReadCanonicalRecords(t.Context(), workspace, []RecordKey{{"projects", "p09-versioned"}}, "detail")
	if err != nil || len(batch.Items) != 1 || batch.Items[0].Version != "3" {
		t.Fatalf("canonical page has no ordered version: %+v %v", batch, err)
	}
	for {
		progress, err := f.BackfillMetadataCatalog(t.Context(), workspace, "projects")
		if err != nil {
			t.Fatal(err)
		}
		if progress.Complete {
			break
		}
	}
	page, err := f.ReadMetadataCatalogPage(t.Context(), workspace, PageRequest{Kind: "projects"})
	if err != nil || len(page.Items) != 1 || page.Items[0].Version != "3" {
		t.Fatalf("catalog page has no ordered version: %+v %v", page, err)
	}
	if n, err := f.PruneRecordJournal(t.Context(), f.data.Workspace.ID, time.Now().Add(time.Hour), 1024); err != nil || n == 0 || n > 128 {
		t.Fatalf("bounded journal retention: %d %v", n, err)
	}
	changes, err := f.ReadCommittedChanges(t.Context(), f.data.Workspace.ID, after, 100, 4096)
	if err != nil || !changes.ResyncRequired || len(changes.Transactions) != 0 {
		t.Fatalf("expired journal cursor accepted: %+v %v", changes, err)
	}
	version, deleted, err := f.RecordVersion(t.Context(), workspace, RecordKey{"projects", "p09-versioned"})
	if err != nil || version != "3" || deleted {
		t.Fatalf("journal pruning removed version state: %s %t %v", version, deleted, err)
	}
}

func TestScaleP09UsageTelemetryDoesNotRevoke(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "shadow")
	f := scaleP09Database(t)
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	scaleP09Put(t, tx, f.data.Workspace.URLKey, "apiKeys", "p09-use", domain.APIKey{ID: "p09-use", Scopes: []string{"read"}})
	scaleP09Put(t, tx, f.data.Workspace.URLKey, "projects", "p09-use-project", domain.Project{ID: "p09-use-project", Name: "Still authorized"})
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	before, err := f.AuthzEpoch(t.Context(), f.data.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	access, err := f.ResolveAccess(t.Context(), f.data.Workspace.URLKey, f.data.Viewer.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.writeAPIKeyUse(t.Context(), apiKeyUse{workspace: f.data.Workspace.URLKey, keyID: "p09-use", at: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	after, err := f.AuthzEpoch(t.Context(), f.data.Workspace.ID)
	if err != nil || before != after {
		t.Fatalf("credential usage revoked policy: %s -> %s %v", before, after, err)
	}
	if _, allowed, err := f.ResourceReadMetadata(t.Context(), access, "projects", "p09-use-project"); err != nil || !allowed {
		t.Fatalf("usage-only metadata revision denied an authorized read: %t %v", allowed, err)
	}
}

func TestScaleP09RepairStrandedJournal(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "shadow")
	f := scaleP09Database(t)
	after := scaleP09Head(t, f)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET role='admin' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, f.data.Viewer.ID); err != nil {
		t.Fatal(err)
	}
	// An old sequencer discarded the journal marker, while the original
	// transaction and its event association remained durable.
	if _, err := f.db.ExecContext(t.Context(), `UPDATE record_journal SET seq=NULL WHERE workspace_id=? AND seq=(SELECT head FROM workspace_record_journal_heads WHERE workspace_id=?)`, f.data.Workspace.ID, f.data.Workspace.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ReadCommittedChanges(t.Context(), f.data.Workspace.ID, after, 100, 4096); !errors.Is(err, ErrRecordJournalCoverage) {
		t.Fatalf("stranded journal silently skipped: %v", err)
	}
	if remaining, err := f.RepairRecordJournal(t.Context(), f.data.Workspace.ID); err != nil || remaining {
		t.Fatalf("stranded journal repair: remaining=%t %v", remaining, err)
	}
	page, err := f.ReadCommittedChanges(t.Context(), f.data.Workspace.ID, after, 100, 4096)
	if err != nil || len(page.Transactions) != 1 || len(page.Transactions[0].Records) != 0 || !slices.Contains(page.Transactions[0].Invalidations, "workspace") {
		t.Fatalf("stranded journal did not become one complete invalidation: %+v %v", page, err)
	}
}

func TestScaleP09ContentKeysRetainParentScope(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "shadow")
	f := scaleP09Database(t)
	after := scaleP09Head(t, f)
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, pair := range [][2]string{{"p:a", "b"}, {"p", "a:b"}} {
		if err := writeContentRecord(t.Context(), tx, f.data.Workspace.URLKey, "comment", pair[0], domain.Comment{ID: pair[1], Body: "Full comment body"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	page, err := f.ReadCommittedChanges(t.Context(), f.data.Workspace.ID, after, 100, 4096)
	if err != nil || len(page.Transactions) != 1 || len(page.Transactions[0].Records) != 2 {
		t.Fatalf("content journal lost a scoped key: %+v %v", page, err)
	}
	keys := map[string]bool{}
	for _, record := range page.Transactions[0].Records {
		var pair []string
		if err := json.Unmarshal([]byte(record.Key.ID), &pair); err != nil || len(pair) != 2 || record.Key.Kind != "comments" || record.Version != "1" {
			t.Fatalf("content key contract: %+v %v", record, err)
		}
		keys[record.Key.ID] = true
	}
	if len(keys) != 2 {
		t.Fatal("different parent/ID pairs collide")
	}
}

func TestScaleP09MySQLStaleSessionToken(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "shadow")
	if os.Getenv("FLOW_TEST_DATABASE_DRIVER") != "mysql" {
		t.Skip("requires the shared CI MySQL database")
	}
	f := scaleP09Database(t)
	const teamID, projectID = "p09-session-team", "p09-session-project"
	if err := f.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p09.seeded", projectID, nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: teamID, Key: "P9S", Name: "Session policy"})
		data.TeamSettings[teamID] = domain.TeamSettings{TeamID: teamID, Access: "public"}
		data.Projects = append(data.Projects, domain.Project{ID: projectID, TeamIDs: []string{teamID}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	access, err := f.ResolveAccess(t.Context(), f.data.Workspace.URLKey, f.data.Viewer.ID, nil)
	if err != nil || !access.policy.teams.Content[teamID] {
		t.Fatalf("public team setup: %v", err)
	}
	conn, err := f.db.DB.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// A canceled transaction can return a pooled connection before its
	// nontransactional session variable is cleared. A raw/disabled writer
	// must still invalidate installed policy when it reuses that connection.
	if _, err := conn.ExecContext(t.Context(), `SET @flow_record_tx='p09_aborted'`); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(context.WithoutCancel(t.Context()), `SET @flow_record_tx=''`)
	team, _ := json.Marshal(domain.Team{ID: teamID, Key: "P9S", Name: "Session policy", Private: true})
	settings, _ := json.Marshal(domain.TeamSettings{TeamID: teamID, Access: "private"})
	for kind, raw := range map[string][]byte{"teams": team, "teamSettings": settings} {
		if _, err := conn.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field=? AND record_key=?`, raw, f.data.Workspace.URLKey, kind, teamID); err != nil {
			t.Fatal(err)
		}
	}
	fresh, err := f.ResolveAccess(t.Context(), f.data.Workspace.URLKey, f.data.Viewer.ID, nil)
	if err != nil || fresh.policy.teams.Content[teamID] || fresh.AuthzEpoch == access.AuthzEpoch {
		t.Fatalf("stale session token retained old installed policy: %v", err)
	}
}

func TestScaleP09RetentionWorkerStops(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "shadow")
	f := scaleP09Database(t)
	if f.db.journalStop == nil {
		t.Fatal("journal retention worker was not started")
	}
	f.db.journalStop()
	done := make(chan struct{})
	go func() {
		f.db.journalWorkers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("journal retention worker outlived cancellation")
	}
}

func TestScaleP09JournalPreservesLegacyWriteBudget(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "shadow")
	for _, phase := range []string{"journal", "audit"} {
		t.Run(phase, func(t *testing.T) {
			f := scaleP09Database(t)
			workspace := f.data.Workspace.URLKey
			tx, err := f.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			scaleP09Put(t, tx, workspace, "projects", "p09-budget", map[string]string{"id": "p09-budget", "name": "Before"})
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			for {
				progress, err := f.BackfillMetadataCatalog(t.Context(), workspace, "projects")
				if err != nil {
					t.Fatal(err)
				}
				if progress.Complete {
					break
				}
			}
			after := scaleP09Head(t, f)
			eventID, auditID := idgen.New("p09-budget-event-"), idgen.New("p09-budget-audit-")
			countKey := issueStatsKey{Scope: "team", ID: "p09-budget-count"}
			prepare := func() *sqlTx {
				tx, err := f.db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				scaleP09Put(t, tx, workspace, "projects", "p09-budget", map[string]string{"id": "p09-budget", "name": "Committed"})
				if _, err := insertDomainEvent(t.Context(), tx, domainEventRow{ID: eventID, Type: "project.updated", WorkspaceID: f.data.Workspace.ID, Payload: []byte(`{"name":"Committed"}`), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
					t.Fatal(err)
				}
				if phase == "audit" {
					tx.audit = &auditCollector{workspaceID: f.data.Workspace.ID, pending: []domain.AuditLogEntry{{ID: auditID, Action: "project.updated", ResourceType: "project", ResourceID: "p09-budget", Metadata: map[string]any{"padding": strings.Repeat("x", 8192)}}}}
					if err := writeIssueStats(t.Context(), tx, workspace, map[issueStatsKey]int64{countKey: 1}); err != nil {
						t.Fatal(err)
					}
				}
				return tx
			}
			// Measure the legacy transaction's actual deferred work, then roll
			// it back. The real write gets that budget plus a small margin.
			probe := prepare()
			if err := probe.flushAudit(); err != nil {
				t.Fatal(err)
			}
			if err := probe.flushIssueCounts(); err != nil {
				t.Fatal(err)
			}
			budget := probe.writtenBytes + 128
			if err := probe.Rollback(); err != nil {
				t.Fatal(err)
			}
			queued, reset := 0, 0
			f.setRecordJournalCommitHook(func(context.Context, *sqlTx, string, []RecordDelta) error { queued++; return nil })
			f.setRecordJournalRollbackHook(func(*sqlTx) { queued = 0; reset++ })
			t.Cleanup(func() { f.setRecordJournalCommitHook(nil); f.setRecordJournalRollbackHook(nil) })
			tx = prepare()
			tx.maxTransactionBytes = budget
			if err := tx.Commit(); err != nil {
				t.Fatalf("derived work rejected a legacy-valid business write: %v", err)
			}
			f.setRecordJournalCommitHook(nil)
			f.setRecordJournalRollbackHook(nil)
			if queued != 0 || reset != 1 {
				t.Fatalf("participant kept rolled-back publication state: queued=%d resets=%d", queued, reset)
			}
			var raw []byte
			if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key='p09-budget'`, workspace).Scan(&raw); err != nil || !bytes.Contains(raw, []byte("Committed")) {
				t.Fatalf("canonical write did not commit: %s %v", raw, err)
			}
			if _, err := f.ReadCommittedChanges(t.Context(), f.data.Workspace.ID, after, 100, 4096); !errors.Is(err, ErrRecordJournalCoverage) {
				t.Fatalf("deferred sidecars falsely claimed complete coverage: %v", err)
			}
			if _, err := f.ReadMetadataCatalogPage(t.Context(), workspace, PageRequest{Kind: "projects"}); !errors.Is(err, ErrMetadataCatalogGap) {
				t.Fatalf("deferred catalog served a stale summary: %v", err)
			}
			if f.DomainEventSeq(t.Context(), eventID) == 0 {
				t.Fatal("legacy event was lost while deferring the journal")
			}
			if phase == "audit" {
				var audits, total int
				if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_events WHERE workspace_id=? AND id=?`, f.data.Workspace.ID, auditID).Scan(&audits); err != nil || audits != 1 {
					t.Fatalf("legacy audit was lost or duplicated: %d %v", audits, err)
				}
				if err := f.db.QueryRowContext(t.Context(), `SELECT total FROM issue_scope_counts WHERE workspace_key=? AND scope_type='team' AND scope_id=?`, workspace, countKey.ID).Scan(&total); err != nil || total != 1 {
					t.Fatalf("legacy counter was lost or duplicated: %d %v", total, err)
				}
			}
		})
	}
}
