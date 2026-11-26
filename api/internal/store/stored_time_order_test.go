package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// preparationDatabase runs on SQLite by default and on an isolated MySQL or
// PostgreSQL database when the external test configuration is present.
func TestStoredTimestampMigrationAndOrder(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	config := preparationDatabase(t)
	ctx := t.Context()
	open := func() *SQLiteStore {
		repo := openPreparationDatabase(t, config)
		t.Cleanup(func() { _ = repo.Close() })
		return repo
	}
	repo := open()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := repo.db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	text := func(query string, args ...any) string {
		t.Helper()
		var value string
		if err := repo.db.QueryRowContext(ctx, query, args...).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}

	const workspace, document = "timestamp-order", "doc_order"
	data, err := repo.CreateWorkspace(ctx, "Timestamp order", workspace, "us")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MutateWorkspace(WithColdWork(ctx, "timestamp fixture"), workspace, "document.created", document, nil, func(next *domain.Bootstrap) error {
		next.Documents = append(next.Documents, domain.Document{ID: document, Title: "Timestamp order", Creator: next.Viewer})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	const (
		whole  = "2026-01-02T03:04:05Z"
		early  = "2026-01-02T03:04:05.371Z"
		late   = "2026-01-02T03:04:05.3716Z"
		offset = "2026-01-01T22:04:05.371-05:00"
	)
	workspaceID, team, owner := data.Workspace.ID, data.Teams[0].ID, data.Viewer.ID
	if err := repo.ensureAccountEmailEvents(ctx); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO auth_email_events(user_id,kind,item_key,created_at) VALUES(?,?,?,?)`, owner, "timestamp-fixture", "first", offset)
	exec(`UPDATE team_memberships SET joined_at=? WHERE workspace_id=? AND team_id=? AND user_id=?`, early, workspaceID, team, owner)
	// Enough rows to cross the composite primary key scan's batch boundary.
	for n := range 130 {
		user := fmt.Sprintf("usr_timestamp_%03d", n)
		exec(`INSERT INTO auth_users(id,name,display_name,password_hash,created_at,updated_at) VALUES(?,?,?,'',?,?)`, user, user, user, whole, whole)
		exec(`INSERT INTO team_memberships(workspace_id,team_id,user_id,joined_at) VALUES(?,?,?,?)`, workspaceID, team, user, late)
	}
	ids := []string{"update_d", "update_b", "update_a", "update_c"}
	for i, stamp := range []string{whole, offset, early, late} {
		exec(`INSERT INTO document_collaboration_updates(update_id,workspace_key,document_id,client_id,update_data,created_at) VALUES(?,?,?,?,?,?)`, ids[i], workspace, document, "fixture", []byte{42}, stamp)
		task := domain.AgentTask{ID: ids[i], CreatedAt: mustParseTimestamp(t, stamp)}
		raw, err := json.Marshal(task)
		if err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO application_agent_tasks(id,workspace_key,issue_id,app_user_id,status,version,data,created_at) VALUES(?,?,?,'app','completed',1,?,?)`, task.ID, workspace, "fixture", raw, stamp)
	}
	exec(`UPDATE workspace_states SET updated_at=? WHERE workspace_key=?`, early, workspace)
	// Existing erasure markers are parsed or checked for presence. Updating
	// them would fire an authorization epoch trigger.
	exec(`UPDATE auth_users SET deleted_at=? WHERE id='usr_timestamp_129'`, early)
	exec(`UPDATE auth_users SET updated_at='invalid' WHERE id='usr_timestamp_129'`)
	exec(`INSERT INTO record_journal(id,workspace_id,seq,authz_epoch,payload,created_at) VALUES(?,?,NULL,?,?,?)`, "timestamp-fixture", workspaceID, "fixture:9", []byte(`{"txId":"timestamp-fixture","records":[]}`), early)
	journal := func() string {
		return text(`SELECT payload FROM record_journal WHERE id='timestamp-fixture' AND seq IS NULL AND authz_epoch='fixture:9'`)
	}
	payload := journal()
	root := text(`SELECT data FROM workspace_states WHERE workspace_key=?`, workspace)
	epoch, err := repo.AuthzEpoch(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	revisions := text(`SELECT COALESCE(SUM(revision),0) FROM workspace_cache_revisions WHERE workspace_key=?`, workspace)
	exec(`DELETE FROM schema_migrations WHERE version=?`, storedTimeMigrationVersion)
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo = open()
	if got := text(`SELECT updated_at FROM workspace_states WHERE workspace_key=?`, workspace); got != "2026-01-02T03:04:05.371000000Z" {
		t.Fatalf("migrated root timestamp = %q", got)
	}
	if got := text(`SELECT created_at FROM record_journal WHERE id='timestamp-fixture'`); got != "2026-01-02T03:04:05.371000000Z" {
		t.Fatalf("migrated journal timestamp = %q", got)
	}
	if got := text(`SELECT created_at FROM auth_email_events WHERE user_id=? AND kind='timestamp-fixture'`, owner); got != "2026-01-02T03:04:05.371000000Z" {
		t.Fatalf("migrated email ledger timestamp = %q", got)
	}
	if got, err := repo.AuthzEpoch(ctx, workspaceID); err != nil || got != epoch {
		t.Fatalf("authorization epoch changed: %v", err)
	}
	if root != text(`SELECT data FROM workspace_states WHERE workspace_key=?`, workspace) ||
		payload != journal() || revisions != text(`SELECT COALESCE(SUM(revision),0) FROM workspace_cache_revisions WHERE workspace_key=?`, workspace) {
		t.Fatal("timestamp migration changed root bytes, journal bytes, or cache revisions")
	}
	if got := text(`SELECT updated_at FROM auth_users WHERE id='usr_timestamp_129' AND deleted_at=? AND email_verified_at IS NULL`, early); got != "invalid" {
		t.Fatal("migration changed an invalid timestamp")
	}
	members, err := repo.ListTeamMembers(ctx, workspaceID)
	if err != nil || len(members) != 131 || members[0].UserID != owner || !members[0].JoinedAt.Equal(mustParseTimestamp(t, early)) || !members[130].JoinedAt.Equal(mustParseTimestamp(t, late)) {
		t.Fatalf("members count/order after migration: %d rows, %v", len(members), err)
	}
	if got := text(`SELECT COUNT(*) FROM team_memberships WHERE workspace_id=? AND joined_at=?`, workspaceID, normalizeStoredTime(late)); got != "130" {
		t.Fatalf("migrated composite-key rows = %s, want 130", got)
	}
	tasks, err := repo.ListAgentTasks(ctx, workspace, "fixture", "")
	if err != nil || len(tasks) != 4 || tasks[0].ID != "update_c" || tasks[3].ID != "update_d" {
		t.Fatalf("agent task order: %d rows, %v", len(tasks), err)
	}
	wantOrder := []string{"update_d", "update_a", "update_b", "update_c"}
	updates, err := repo.DocumentCollaborationUpdates(ctx, workspace, document)
	if err != nil || !slices.Equal(collectTimestampUpdateIDs(updates), wantOrder) {
		t.Fatalf("collaboration activity order: %v, %v", collectTimestampUpdateIDs(updates), err)
	}
	cursor := CollaborationRecoveryCursor{}
	var paged []string
	for range 5 {
		page, err := repo.ReadCollaborationRecoveryPage(ctx, workspace, document, "", document, cursor, 1)
		if err != nil || page.Conflict || len(page.Updates) != 1 {
			t.Fatalf("recover collaboration page: %d updates, conflict=%v, %v", len(page.Updates), page.Conflict, err)
		}
		paged = append(paged, page.Updates[0].ID)
		if !page.More {
			break
		}
		cursor = page.Cursor
		// An old-width continuation must not skip an equal-time row.
		cursor.AfterTime = mustParseTimestamp(t, cursor.AfterTime).Format(time.RFC3339Nano)
		cursor.ThroughTime = late
	}
	if !slices.Equal(paged, wantOrder) {
		t.Fatalf("collaboration keyset = %v, want %v", paged, wantOrder)
	}
	newAt := mustParseTimestamp(t, early)
	if inserted, err := repo.AppendDocumentCollaborationUpdate(ctx, workspace, DocumentCollaborationUpdate{ID: "new", DocumentID: document, ClientID: "fixture", Data: []byte{43}, CreatedAt: newAt}); err != nil || !inserted {
		t.Fatalf("append collaboration update: %t, %v", inserted, err)
	}
	if got := text(`SELECT created_at FROM document_collaboration_updates WHERE update_id='new'`); got != formatStoredTime(newAt) {
		t.Fatalf("new stored timestamp = %q", got)
	}
	got, err := json.Marshal(updates[1].CreatedAt)
	if err != nil || !bytes.Equal(got, []byte(`"`+early+`"`)) {
		t.Fatalf("API timestamp = %s, %v", got, err)
	}
	appliedAt := text(`SELECT applied_at FROM schema_migrations WHERE version=?`, storedTimeMigrationVersion)
	if err := repo.migrateStoredTimes(ctx); err != nil {
		t.Fatal(err)
	}
	if got := text(`SELECT applied_at FROM schema_migrations WHERE version=?`, storedTimeMigrationVersion); got != appliedAt {
		t.Fatal("migration recorded a second execution")
	}
}

func collectTimestampUpdateIDs(rows []DocumentCollaborationUpdate) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

func mustParseTimestamp(t *testing.T, raw string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestTimestampTextKeyOnJSON(t *testing.T) {
	repo := openPreparationDatabase(t, preparationDatabase(t))
	defer repo.Close()
	ctx := t.Context()
	for _, raw := range []string{
		"2026-01-02T03:04:05Z",
		"2026-01-02T03:04:05.371Z",
		"2026-01-02T03:04:05.371000001Z",
		"2026-01-02T03:04:05.371000000Z",
		"2026-01-01T22:04:05.371000001-05:00",
		"2026-01-02T08:34:05+05:30",
		"0001-01-01T00:00:00Z",
		"",
	} {
		var key string
		input := "CAST(? AS VARCHAR(40))"
		if repo.dialect == "mysql" {
			input = "CAST(? AS CHAR(40))"
		}
		query := "SELECT " + repo.timestampTextKey("source.stamp") + " FROM (SELECT " + input + " AS stamp) source"
		if err := repo.db.QueryRowContext(ctx, query, raw).Scan(&key); err != nil {
			t.Fatal(err)
		}
		if want := normalizeStoredTime(raw); key != want {
			t.Errorf("comparison key for %q = %q, want %q", raw, key, want)
		}
	}

	const workspace = "timestamp-json"
	policy, err := repo.CreateWorkspace(ctx, "JSON timestamps", workspace, "us")
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"d": "2026-01-02T03:04:05Z",
		"a": "2026-01-02T03:04:05.371Z",
		"b": "2026-01-01T22:04:05.371-05:00",
		"c": "2026-01-02T03:04:05.371000001Z",
	}
	// Force the search across its 64-result keyset boundary. A tie's key
	// must sort identically in the ORDER BY and the continuation predicate.
	for i := range 64 {
		values[fmt.Sprintf("early-%02d", i)] = values["d"]
	}
	ids := slices.Sorted(maps.Keys(values))
	err = repo.MutateWorkspace(WithColdWork(ctx, "timestamp JSON fixture"), workspace, "project.created", "", nil, func(next *domain.Bootstrap) error {
		for _, id := range ids {
			at := mustParseTimestamp(t, values[id])
			next.Projects = append(next.Projects, domain.Project{ID: id, Name: id, CreatedAt: at, UpdatedAt: at})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	query := SearchMetadataQuery{Scope: IssueRecordQuery{Workspace: workspace, Sort: "createdAt", Direction: "asc"}, Types: map[string]bool{"project": true}}
	for _, id := range ids {
		query.Recent = append(query.Recent, domain.RecentResource{ResourceType: "project", ResourceID: id})
	}
	read := func(q SearchMetadataQuery) []string {
		t.Helper()
		result, err := repo.SearchMetadata(ctx, policy, q)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(result.Projects))
		for _, item := range result.Projects {
			got = append(got, item.ID)
		}
		return got
	}
	got := read(query)
	if len(got) != len(ids) || got[0] != "d" || !slices.Equal(got[65:], []string{"a", "b", "c"}) {
		t.Fatalf("search order: %d rows, tail %v", len(got), got[max(len(got)-4, 0):])
	}
	query.Scope.Filter = IssueFilter{Field: "updatedAt", Operator: "is", Values: []string{values["b"]}}
	if got := read(query); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("equal instants: %v", got)
	}
	query.Scope.Filter.Operator = "after"
	if got := read(query); !slices.Equal(got, []string{"c"}) {
		t.Fatalf("after cutoff: %v", got)
	}
	rawTime := repo.jsonText("data", "createdAt")
	var persisted sql.NullString
	err = repo.db.QueryRowContext(ctx, "SELECT "+rawTime+" FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key='b'", workspace).Scan(&persisted)
	if err != nil || !persisted.Valid || !strings.HasSuffix(persisted.String, "-05:00") {
		t.Fatalf("JSON timestamp spelling changed: %v, %v", persisted, err)
	}

	t.Run("credential usage cutoff", func(t *testing.T) {
		last := mustParseTimestamp(t, values["b"])
		key := domain.APIKey{ID: "timestamp-key", LastUsedAt: &last}
		insertSearchMetadata(t, repo, workspace, "apiKeys", key.ID, key)
		for _, at := range []time.Time{last.Add(time.Minute), last.Add(time.Minute + time.Nanosecond).UTC()} {
			tx, err := repo.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.writeAPIKeyUseRows(ctx, tx, apiKeyUse{workspace: workspace, keyID: key.ID, at: at}); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			want := last
			if at.Sub(last) > time.Minute {
				want = at
			}
			var got string
			if err := repo.db.QueryRowContext(ctx, "SELECT "+repo.jsonText("data", "lastUsedAt")+" FROM workspace_metadata_records WHERE workspace_key=? AND field='apiKeys' AND record_key=?", workspace, key.ID).Scan(&got); err != nil || got != want.Format(time.RFC3339Nano) {
				t.Fatalf("usage timestamp = %q, want %s, %v", got, want.Format(time.RFC3339Nano), err)
			}
		}
	})
}
