package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type schemaColumn struct {
	name, kind string
}

// sqliteSchema lists every table of the store's SQLite database with its
// columns.
func sqliteSchema(t *testing.T, repo *SQLiteStore) map[string][]schemaColumn {
	t.Helper()
	tables, err := repo.workspaceStrings(context.Background(), `SELECT name FROM sqlite_master WHERE type='table' AND name<>?`, "")
	if err != nil {
		t.Fatal(err)
	}
	schema := map[string][]schemaColumn{}
	for _, table := range tables {
		rows, err := repo.db.QueryContext(context.Background(), `SELECT name,type FROM pragma_table_info(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var column schemaColumn
			if err := rows.Scan(&column.name, &column.kind); err != nil {
				t.Fatal(err)
			}
			schema[table] = append(schema[table], column)
		}
		rows.Close()
	}
	return schema
}

func hasColumn(columns []schemaColumn, name string) bool {
	return slices.ContainsFunc(columns, func(column schemaColumn) bool { return column.name == name })
}

// TestWorkspacePurgeCoversEverySchemaTable fails when a table is added
// without saying how a workspace purge treats it: every table that has a
// workspace_key or workspace_id column must be in workspacePurgeTables, and
// every other table must be listed as indirect or exempt with a reason.
func TestWorkspacePurgeCoversEverySchemaTable(t *testing.T) {
	repo, err := OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	schema := sqliteSchema(t, repo)
	direct := map[string]workspacePurgeTable{}
	for _, table := range workspacePurgeTables {
		if _, dup := direct[table.table]; dup {
			t.Errorf("%s is listed twice in workspacePurgeTables", table.table)
		}
		direct[table.table] = table
		columns, ok := schema[table.table]
		if !ok {
			t.Errorf("workspacePurgeTables lists %s, which the schema does not have", table.table)
			continue
		}
		if !hasColumn(columns, table.column) {
			t.Errorf("%s has no column %s", table.table, table.column)
		}
		if want := table.column == "workspace_id"; table.byID != want {
			t.Errorf("%s: byID=%v for column %s", table.table, table.byID, table.column)
		}
	}
	for table, columns := range schema {
		if _, ok := direct[table]; ok {
			continue
		}
		if _, ok := workspacePurgeKept[table]; ok {
			continue
		}
		reason, indirect := workspacePurgeIndirect[table]
		if !indirect {
			reason = workspacePurgeExempt[table]
		}
		if hasColumn(columns, "workspace_key") || hasColumn(columns, "workspace_id") {
			t.Errorf("table %s has a workspace column but is not in workspacePurgeTables (listed as %q)", table, reason)
			continue
		}
		if reason == "" {
			t.Errorf("table %s is not classified for workspace purges: add it to workspacePurgeTables, workspacePurgeIndirect or workspacePurgeExempt", table)
		}
	}
	for table := range workspacePurgeIndirect {
		if _, ok := workspacePurgeExempt[table]; ok {
			t.Errorf("%s is both indirect and exempt", table)
		}
	}
}

// insertSyntheticRow inserts a row whose workspace column is value and whose
// other columns hold placeholder values of their type (ignored when the
// primary key already exists).
func insertSyntheticRow(t *testing.T, repo *SQLiteStore, table string, columns []schemaColumn, column, value, userID string, serial int) {
	t.Helper()
	insert := func(realUser bool) error {
		names, marks, args := []string{}, []string{}, []any{}
		for _, item := range columns {
			names = append(names, item.name)
			marks = append(marks, "?")
			kind := strings.ToUpper(item.kind)
			switch {
			case item.name == column:
				args = append(args, value)
			case realUser && (item.name == "user_id" || item.name == "inviter_id"):
				// These user columns reference auth_users.
				args = append(args, userID)
			case strings.Contains(kind, "INT"):
				args = append(args, serial)
			case strings.Contains(kind, "BLOB"):
				args = append(args, []byte(`{}`))
			default:
				args = append(args, fmt.Sprintf("purge-%s-%d", item.name, serial))
			}
		}
		_, err := repo.db.ExecContext(context.Background(), `INSERT OR IGNORE INTO `+table+`(`+strings.Join(names, ",")+`) VALUES(`+strings.Join(marks, ",")+`)`, args...)
		return err
	}
	err := insert(false)
	if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
		err = insert(true)
	}
	if err != nil {
		t.Fatalf("insert into %s: %v", table, err)
	}
}

func countWhere(t *testing.T, repo *SQLiteStore, table, column, value string) int {
	t.Helper()
	var count int
	if err := repo.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table+` WHERE `+column+`=?`, value).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

// TestDeleteWorkspaceRemovesEveryWorkspaceRow puts a row of the workspace in
// every table that holds workspace rows (and one of another workspace),
// deletes the workspace and checks that only the other workspace's rows are
// left.
func TestDeleteWorkspaceRemovesEveryWorkspaceRow(t *testing.T) {
	ctx := context.Background()
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data, ok := repo.BootstrapFor("test-workspace")
	if !ok {
		t.Fatal("fixture workspace missing")
	}
	target := WorkspacePurgeTarget{Key: "test-workspace", ID: data.Workspace.ID}
	other := WorkspacePurgeTarget{Key: "purge-other", ID: "purge-other-id"}
	schema := sqliteSchema(t, repo)
	var userID string
	if err := repo.db.QueryRowContext(ctx, `SELECT id FROM auth_users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	for index, table := range workspacePurgeTables {
		for ownerIndex, owner := range []WorkspacePurgeTarget{target, other} {
			value := owner.Key
			if table.byID {
				value = owner.ID
			}
			insertSyntheticRow(t, repo, table.table, schema[table.table], table.column, value, userID, index*2+ownerIndex)
		}
	}
	// More rows than one delete batch.
	for i := 0; i < workspacePurgeBatch+20; i++ {
		if _, err := repo.db.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,created_at,workspace_id) VALUES(?,?,?,?,?,?)`, fmt.Sprintf("evt_purge_%d", i), "test.event", "agg", []byte(`{}`), time.Now().UTC().Format(time.RFC3339Nano), target.ID); err != nil {
			t.Fatal(err)
		}
	}
	// Rows found through other columns.
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO code_host_accounts(id,user_id,code_host,host_name,base_url,external_user_id,login,email,auth_type,scopes,status,status_reason,workspace_key,connection_id,linked_at,updated_at) VALUES('cha_purge','usr','github','github.com','','1','login','','oauth','','active','',?,'','','')`, []any{target.Key}},
		{`INSERT INTO connector_secrets(id,ciphertext,expires_at) VALUES(?,?,?)`, []any{CodeHostAccountSecretID("cha_purge"), []byte("sealed"), "2999-01-01T00:00:00Z"}},
		{`INSERT INTO connector_secrets(id,ciphertext,expires_at) VALUES(?,?,?)`, []any{"unrelated-secret", []byte("sealed"), "2999-01-01T00:00:00Z"}},
		{`INSERT INTO application_agent_tasks(id,workspace_key,issue_id,app_user_id,status,version,data,created_at) VALUES('task_purge',?,'issue','app','open',1,?,0)`, []any{target.Key, []byte(`{}`)}},
		{`INSERT INTO application_agent_activities(session_id,id,data,created_at) VALUES('task_purge','act_1',?,'2026-01-01')`, []any{[]byte(`{}`)}},
		{`INSERT INTO oauth_refresh_tokens(token_hash,data,expires_at,created_at) VALUES('rt_purge',?,'2999-01-01','2026-01-01')`, []any{[]byte(`{"workspaceKey":"test-workspace","userId":"usr"}`)}},
		{`INSERT INTO oauth_refresh_tokens(token_hash,data,expires_at,created_at) VALUES('rt_other',?,'2999-01-01','2026-01-01')`, []any{[]byte(`{"workspaceKey":"purge-other","userId":"usr"}`)}},
		{`INSERT INTO oauth_authorization_codes(code_hash,data,expires_at,created_at) VALUES('code_purge',?,'2999-01-01','2026-01-01')`, []any{[]byte(`{"workspaceKey":"test-workspace"}`)}},
		{`INSERT OR REPLACE INTO workspace_state(id,data,updated_at) VALUES(1,?,'2026-01-01')`, []any{[]byte(`{"workspace":{"id":"` + target.ID + `","urlKey":"test-workspace"}}`)}},
	} {
		if _, err := repo.db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("%s: %v", statement.query, err)
		}
	}
	for _, table := range workspacePurgeTables {
		value := target.Key
		if table.byID {
			value = target.ID
		}
		if countWhere(t, repo, table.table, table.column, value) == 0 {
			t.Fatalf("setup left %s without a row of the workspace", table.table)
		}
	}

	if err := repo.DeleteWorkspace(ctx, target.Key); err != nil {
		t.Fatal(err)
	}
	if _, ok := repo.BootstrapFor(target.Key); ok {
		t.Fatal("workspace still in the catalog")
	}
	for _, table := range workspacePurgeTables {
		value, otherValue := target.Key, other.Key
		if table.byID {
			value, otherValue = target.ID, other.ID
		}
		if got := countWhere(t, repo, table.table, table.column, value); got != 0 {
			t.Errorf("%s keeps %d rows of the deleted workspace", table.table, got)
		}
		if got := countWhere(t, repo, table.table, table.column, otherValue); got == 0 {
			t.Errorf("%s lost the other workspace's row", table.table)
		}
	}
	for table, check := range map[string][2]string{
		"connector_secrets (code host)":   {`SELECT COUNT(*) FROM connector_secrets WHERE id=?`, CodeHostAccountSecretID("cha_purge")},
		"application_agent_activities":    {`SELECT COUNT(*) FROM application_agent_activities WHERE session_id=?`, "task_purge"},
		"oauth_refresh_tokens":            {`SELECT COUNT(*) FROM oauth_refresh_tokens WHERE token_hash=?`, "rt_purge"},
		"oauth_authorization_codes":       {`SELECT COUNT(*) FROM oauth_authorization_codes WHERE code_hash=?`, "code_purge"},
		"workspace_state (legacy)":        {`SELECT COUNT(*) FROM workspace_state WHERE id=1 AND ?<>''`, "x"},
		"issue_search_fts (via triggers)": {`SELECT COUNT(*) FROM issue_search_documents WHERE workspace_key=?`, target.Key},
	} {
		var count int
		if err := repo.db.QueryRowContext(ctx, check[0], check[1]).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("%s keeps %d rows of the deleted workspace", table, count)
		}
	}
	for table, check := range map[string][2]string{
		"connector_secrets (unrelated)": {`SELECT COUNT(*) FROM connector_secrets WHERE id=?`, "unrelated-secret"},
		"oauth_refresh_tokens (other)":  {`SELECT COUNT(*) FROM oauth_refresh_tokens WHERE token_hash=?`, "rt_other"},
	} {
		var count int
		if err := repo.db.QueryRowContext(ctx, check[0], check[1]).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("%s: %d rows, want 1", table, count)
		}
	}
	// Running the purge again finds nothing left.
	counts := map[string]int64{}
	if err := repo.PurgeWorkspaceRows(ctx, target, counts); err != nil {
		t.Fatal(err)
	}
	if len(counts) != 0 {
		t.Fatalf("second purge deleted %v", counts)
	}
}

func TestWorkspacePurgeReceiptsAndCancellation(t *testing.T) {
	ctx := context.Background()
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if _, _, err := repo.BeginWorkspacePurge(ctx, "test-workspace", time.Now()); err != nil {
		t.Fatal(err)
	}
	if receipts, _ := repo.WorkspaceDeletionReceipts(ctx, "", 10); len(receipts) != 0 {
		t.Fatalf("a workspace without a deletion request got a receipt: %#v", receipts)
	}
	scheduled, err := repo.ScheduleWorkspaceDeletion(ctx, "test-workspace")
	if err != nil {
		t.Fatal(err)
	}
	if due := repo.WorkspacesDueForDeletion(scheduled.DeletionRequestedAt.Add(-time.Second)); len(due) != 0 {
		t.Fatalf("due before its grace period: %#v", due)
	}
	if due := repo.WorkspacesDueForDeletion(time.Now()); len(due) != 1 || due[0].ID != scheduled.ID {
		t.Fatalf("due = %#v", due)
	}
	// A purge that waits for a later cutoff does not start.
	if _, started, err := repo.BeginWorkspacePurge(ctx, "test-workspace", scheduled.DeletionRequestedAt.Add(-time.Second)); err != nil || started {
		t.Fatalf("early purge started=%v err=%v", started, err)
	}
	receipt, started, err := repo.BeginWorkspacePurge(ctx, "test-workspace", time.Now())
	if err != nil || !started {
		t.Fatalf("purge started=%v err=%v", started, err)
	}
	if receipt.Status != WorkspacePurgePurging || receipt.WorkspaceID != scheduled.ID || receipt.RequestRef == "" || !receipt.RequestedAt.Equal(scheduled.DeletionRequestedAt.UTC()) {
		t.Fatalf("receipt = %#v", receipt)
	}
	if _, err := repo.CancelWorkspaceDeletion(ctx, "test-workspace"); !errors.Is(err, ErrWorkspacePurging) {
		t.Fatalf("cancel after the purge started: %v", err)
	}
	again, started, err := repo.BeginWorkspacePurge(ctx, "test-workspace", time.Now())
	if err != nil || !started || again.StartedAt != receipt.StartedAt {
		t.Fatalf("second begin = %#v started=%v err=%v", again, started, err)
	}
	receipt.AddCount("issue_records", 3)
	now := time.Now().UTC()
	receipt.Status, receipt.CompletedAt = WorkspacePurgeCompleted, &now
	if err := repo.SaveWorkspaceDeletionReceipt(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	stored, found, err := repo.WorkspaceDeletionReceipt(ctx, scheduled.ID)
	if err != nil || !found || stored.Status != WorkspacePurgeCompleted || stored.Counts["issue_records"] != 3 || stored.CompletedAt == nil {
		t.Fatalf("stored receipt = %#v found=%v err=%v", stored, found, err)
	}
	if pending, _ := repo.WorkspaceDeletionReceipts(ctx, WorkspacePurgePurging, 10); len(pending) != 0 {
		t.Fatalf("pending after completion: %#v", pending)
	}
}

func TestWorkspaceDeletionCanBeCanceledDuringTheGracePeriod(t *testing.T) {
	ctx := context.Background()
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if _, err := repo.ScheduleWorkspaceDeletion(ctx, "test-workspace"); err != nil {
		t.Fatal(err)
	}
	canceled, err := repo.CancelWorkspaceDeletion(ctx, "test-workspace")
	if err != nil || canceled.DeletionRequestedAt != nil {
		t.Fatalf("cancel = %#v, %v", canceled, err)
	}
	if due := repo.WorkspacesDueForDeletion(time.Now().Add(time.Hour)); len(due) != 0 {
		t.Fatalf("canceled workspace still due: %#v", due)
	}
	if _, started, err := repo.BeginWorkspacePurge(ctx, "test-workspace", time.Now().Add(time.Hour)); err != nil || started {
		t.Fatalf("purge of a canceled deletion started=%v err=%v", started, err)
	}
}

func TestPurgeWorkspaceRowsRefusesALiveOrReusedKey(t *testing.T) {
	ctx := context.Background()
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data, _ := repo.BootstrapFor("test-workspace")
	if err := repo.PurgeWorkspaceRows(ctx, WorkspacePurgeTarget{Key: "test-workspace", ID: data.Workspace.ID}, map[string]int64{}); err == nil {
		t.Fatal("purged the rows of a workspace still in the catalog")
	}
	if err := repo.PurgeWorkspaceRows(ctx, WorkspacePurgeTarget{Key: "test-workspace", ID: "ws_gone"}, map[string]int64{}); !errors.Is(err, ErrWorkspaceKeyReused) {
		t.Fatalf("reused key: %v", err)
	}
	if countWhere(t, repo, "workspace_states", "workspace_key", "test-workspace") != 1 {
		t.Fatal("the live workspace lost its state")
	}
}

func TestAttachmentUploadsAndObjectReferences(t *testing.T) {
	raw := []byte(`{"attachments":[{"url":"/uploads/attachment_1_Report Q1.pdf"},{"url":"https://example.com/uploads/x.png"}],"description":"see ![a](/uploads/asset_2.png) and [b](/uploads/asset%203.txt)","pulseSummary":{"text":"Week in review"}}`)
	if got, want := AttachmentUploads(raw), []string{"attachment_1_Report Q1.pdf"}; !slices.Equal(got, want) {
		t.Fatalf("attachment uploads = %v, want %v", got, want)
	}
	collector := newObjectCollector()
	collector.add(raw)
	for _, name := range []string{"attachment_1_Report Q1.pdf", "asset_2.png", "asset 3.txt"} {
		if !collector.uploads[name] {
			t.Errorf("reference %q not found in %v", name, collector.uploads)
		}
	}
	if collector.uploads["x.png"] {
		t.Error("an external site's /uploads/ path was taken for a local upload")
	}
	if !collector.pulseTexts["Week in review"] {
		t.Error("pulse summary text not collected")
	}
	collector = newObjectCollector()
	collector.add(gzipBody([]byte(`{"body":"![x](/uploads/inside_gzip.png)"}`)))
	if !collector.uploads["inside_gzip.png"] {
		t.Error("gzipped body not scanned")
	}
	if validUploadName("../etc/passwd") || validUploadName("a/b") || !validUploadName("ok.txt") {
		t.Error("upload name validation")
	}
}

func TestWorkspaceObjectReferencesSkipsFilesOtherWorkspacesUse(t *testing.T) {
	ctx := context.Background()
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data, _ := repo.BootstrapFor("test-workspace")
	target := WorkspacePurgeTarget{Key: "test-workspace", ID: data.Workspace.ID}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data) VALUES(?,?,?,?,?,?)`, []any{target.Key, "comment", "issue_1", "c1", "2026", []byte(`{"body":"![a](/uploads/mine.png) ![b](/uploads/theirs.png)"}`)}},
		{`INSERT INTO file_uploads(id,workspace_key,user_id,object_key,filename,content_type,expected_size,actual_size,purpose,title,subtitle,status,expires_at,created_at,updated_at) VALUES('up1','elsewhere','usr','theirs.png','theirs.png','image/png',0,0,'asset','','','completed','','','')`, nil},
		{`INSERT INTO file_uploads(id,workspace_key,user_id,object_key,filename,content_type,expected_size,actual_size,purpose,title,subtitle,status,expires_at,created_at,updated_at) VALUES('up2',?,'usr','logo.png','logo.png','image/png',0,0,'workspace_logo','','','completed','','','')`, []any{target.Key}},
		{`INSERT INTO coding_session_artifacts(id,session_id,workspace_key,object_key,data,created_at) VALUES('art1','cs1',?,'coding-artifact_cs1_art1.txt',?,0)`, []any{target.Key, []byte(`{}`)}},
	} {
		if _, err := repo.db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("%s: %v", statement.query, err)
		}
	}
	objects, err := repo.WorkspaceObjectReferences(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(objects.Uploads, "mine.png") || !slices.Contains(objects.Uploads, "logo.png") || slices.Contains(objects.Uploads, "theirs.png") {
		t.Fatalf("uploads = %v", objects.Uploads)
	}
	if !slices.Contains(objects.CodingObjects, "coding-artifact_cs1_art1.txt") {
		t.Fatalf("coding objects = %v", objects.CodingObjects)
	}
}
