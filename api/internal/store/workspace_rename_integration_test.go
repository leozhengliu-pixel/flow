//go:build integration

package store

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
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
