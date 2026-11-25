//go:build integration

package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// TestExternalDatabaseWorkspaceRename runs the rename checks on PostgreSQL or
// MySQL: every table with a workspace_key column is classified, a rename
// moves a row of each and rewrites JSON naming the workspace, and rows
// written under the previous key afterwards (including ones whose unique key
// the current key already has) are adopted by AdoptRenamedWorkspaceRows.
func TestExternalDatabaseWorkspaceRename(t *testing.T) {
	driver, url := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || url == "" {
		t.Skip("external database not configured")
	}
	// Arbitrary rows in every keyed table include deliberately invalid
	// journal checkpoints and catalog records. This tests table ownership.
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	ctx := context.Background()
	repo, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: url, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	schema := externalSchema(t, repo)
	moved := map[string]bool{}
	for _, table := range workspaceRenameTables() {
		moved[table] = true
	}
	for table, columns := range schema {
		for _, column := range columns {
			if column.name == "workspace_key" && moved[table] == (workspaceRenameSkipped[table] != "") {
				t.Errorf("%s table %s has a workspace_key column but a rename neither moves nor skips it", driver, table)
			}
		}
	}

	const oldKey, newKey, otherKey = "rename-integration", "rename-integration-new", "rename-integration-other"
	for _, key := range []string{oldKey, newKey} {
		_ = repo.DeleteWorkspace(ctx, key)
	}
	cleanup := func() {
		for _, key := range []string{oldKey, newKey, otherKey} {
			for _, table := range workspaceRenameTables() {
				_, _ = repo.deleteWorkspaceRows(ctx, table, "workspace_key", key)
			}
		}
		_, _ = repo.db.ExecContext(ctx, `DELETE FROM oauth_refresh_tokens WHERE token_hash='rt_rename_integration'`)
	}
	cleanup()
	created, err := repo.CreateWorkspace(ctx, "Rename integration", oldKey, "us")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = repo.DeleteWorkspace(ctx, newKey)
		cleanup()
	}()
	insert := func(key string, base int) {
		for index, table := range workspaceRenameTables() {
			values := map[string]any{"workspace_key": key}
			if table == "application_agent_tasks" {
				values["data"] = []byte(`{"workspaceKey":"` + key + `"}`)
			}
			if err := insertExternalRow(t, repo, table, schema[table], values, base+index); err != nil {
				t.Fatalf("insert into %s: %v", table, err)
			}
		}
	}
	insert(oldKey, 910000)
	insert(otherKey, 920000)
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO oauth_refresh_tokens(token_hash,data,expires_at,created_at) VALUES(?,?,?,?)`, "rt_rename_integration", []byte(`{"workspaceKey":"`+oldKey+`"}`), "2999-01-01", "2026-01-01"); err != nil {
		t.Fatal(err)
	}
	count := func(table, key string) int {
		var n int
		if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE workspace_key=?`, key).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}
	workspace := created.Workspace
	workspace.URLKey = newKey
	if _, err := repo.UpdateWorkspace(ctx, oldKey, workspace); err != nil {
		t.Fatal(err)
	}
	for _, table := range workspaceRenameTables() {
		if n := count(table, oldKey); n != 0 {
			t.Errorf("%s keeps %d rows under the old key", table, n)
		}
		if count(table, newKey) == 0 {
			t.Errorf("%s has no row under the new key", table)
		}
		if count(table, otherKey) == 0 {
			t.Errorf("%s lost another workspace's row", table)
		}
	}
	readData := func(query string, args ...any) []byte {
		var data []byte
		if err := repo.db.QueryRowContext(ctx, query, args...).Scan(&data); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return data
	}
	if data := readData(`SELECT data FROM oauth_refresh_tokens WHERE token_hash='rt_rename_integration'`); !bytes.Contains(data, []byte(`"workspaceKey":"`+newKey+`"`)) {
		t.Errorf("OAuth grant after the rename: %s", data)
	}
	if data := readData(`SELECT data FROM application_agent_tasks WHERE workspace_key=?`, newKey); !bytes.Contains(data, []byte(`"workspaceKey":"`+newKey+`"`)) {
		t.Errorf("agent task after the rename: %s", data)
	}
	if data := readData(`SELECT data FROM application_agent_tasks WHERE workspace_key=?`, otherKey); !bytes.Contains(data, []byte(`"workspaceKey":"`+otherKey+`"`)) {
		t.Errorf("another workspace's agent task changed: %s", data)
	}

	// Late writes under the previous key, many of them colliding with the
	// moved rows' unique keys.
	insert(oldKey, 910000)
	insert(oldKey, 930000)
	report, err := repo.AdoptRenamedWorkspaceRows(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if report.Moved == 0 {
		t.Fatalf("adopt report = %+v", report)
	}
	for _, table := range workspaceRenameTables() {
		if n := count(table, oldKey); n != 0 {
			t.Errorf("after adopting, %s keeps %d rows under the previous key", table, n)
		}
		if count(table, otherKey) == 0 {
			t.Errorf("adopting removed another workspace's row from %s", table)
		}
	}
	if table, err := repo.workspaceKeyInUse(ctx, oldKey); err != nil || table != "" {
		t.Fatalf("previous key still in use by %q (%v)", table, err)
	}
}

func TestExternalDatabaseWorkspaceRenameJournal(t *testing.T) {
	driver, url := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || url == "" {
		t.Skip("external database not configured")
	}
	ctx := t.Context()
	open := func() *SQLiteStore {
		t.Helper()
		repo, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: url, MaxOpenConns: 4, MaxIdleConns: 2})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.Close() })
		if !repo.BoundedMetadataEnabled() || !repo.RecordJournalReady() {
			t.Fatal("expected the external database defaults")
		}
		return repo
	}
	const oldKey, newKey = "rename-journal", "rename-journal-new"
	repo := open()
	for _, key := range []string{oldKey, newKey} {
		_ = repo.DeleteWorkspace(ctx, key)
	}
	created, err := repo.CreateWorkspace(ctx, "Rename journal", oldKey, "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, key := range []string{oldKey, newKey} {
			_ = repo.DeleteWorkspace(context.Background(), key)
		}
	})
	project := domain.Project{ID: "rename-journal-project", Name: "Initial", TeamIDs: []string{created.Teams[0].ID}}
	if err := repo.MutateWorkspace(WithColdWork(ctx, "rename fixture"), oldKey, "project.created", project.ID, nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, project)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	stale := open()
	// A previous-version writer commits a canonical edit before the rename.
	// Its trigger captures the edit but leaves the journal work pending.
	project.Name = "Legacy project edit"
	raw, err := json.Marshal(project)
	if err != nil {
		t.Fatal(err)
	}
	query := `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='projects' AND record_key=?`
	if _, err := repo.db.DB.ExecContext(ctx, rewriteSQL(query, driver), raw, oldKey, project.ID); err != nil {
		t.Fatal(err)
	}
	if err := checkRecordJournalCoverage(ctx, repo.db, oldKey); !errors.Is(err, ErrRecordJournalCoverage) {
		t.Fatal("legacy edit did not leave pending journal work", err)
	}
	workspace := created.Workspace
	workspace.URLKey = newKey
	if _, err := repo.UpdateWorkspace(ctx, oldKey, workspace); err != nil {
		t.Fatal(err)
	}
	assertReady := func(reader *SQLiteStore) {
		t.Helper()
		if reader.HasWorkspace(oldKey) {
			t.Fatal("previous key still installed")
		}
		data, ok := reader.WorkspaceMetadata(newKey)
		if !ok || data.Workspace.ID != workspace.ID || len(data.Projects) != 1 || data.Projects[0].Name != project.Name {
			t.Fatal("renamed workspace lost its project or the last committed edit")
		}
		var report BoundedMetadataPreparation
		if err := reader.boundedMetadataReadiness(ctx, &report); err != nil || !report.Ready {
			t.Fatal("renamed workspace lost journal or catalog readiness", err)
		}
		if _, err := reader.ReadCommittedChanges(ctx, workspace.ID, "", 100, MetadataRecordPageMaxBytes); err != nil {
			t.Fatal("renamed workspace journal cannot be read", err)
		}
	}
	assertReady(repo)
	// Work that began before the rename can leave a task under the old
	// key. A replica that still has that key installed must adopt it.
	schema := externalSchema(t, stale)
	if err := insertExternalRow(t, stale, "application_agent_tasks", schema["application_agent_tasks"], map[string]any{
		"workspace_key": oldKey,
		"data":          []byte(`{"workspaceKey":"` + oldKey + `"}`),
	}, 940000); err != nil {
		t.Fatal(err)
	}
	if !stale.HasWorkspace(oldKey) || stale.HasWorkspace(newKey) {
		t.Fatal("adopting replica is not stale")
	}
	report, err := stale.AdoptRenamedWorkspaceRows(ctx, time.Now().Add(-time.Hour))
	if err != nil || report.Moved == 0 || report.Ambiguous != 0 {
		t.Fatal("late task was not adopted", err)
	}
	var task []byte
	if err := repo.db.QueryRowContext(ctx, `SELECT data FROM application_agent_tasks WHERE workspace_key=?`, newKey).Scan(&task); err != nil || !bytes.Contains(task, []byte(`"workspaceKey":"`+newKey+`"`)) {
		t.Fatal("late task was not moved and rewritten", err)
	}
	if report, err := stale.AdoptRenamedWorkspaceRows(ctx, time.Now().Add(-time.Hour)); err != nil || report.Moved != 0 {
		t.Fatal("adoption is not idempotent", err)
	}
	assertReady(stale)
	assertReady(open())
}
