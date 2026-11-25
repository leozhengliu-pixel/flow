package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// A version-0 row whose JSON has no id, or another id, is rewritten in place:
// keyed on the JSON id, the rewrite added a second row and left the original at
// version 0, so the migration selected it forever and the store never opened.
func TestContentIndexMigrationRewritesRowsUnderTheirStoredKey(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Format(time.RFC3339Nano)
	rows := map[string]string{
		"no-json-id":    `{"body":"legacy"}`,
		"other-json-id": `{"id":"somewhere-else","body":"hand written"}`,
		"same-json-id":  `{"id":"same-json-id","body":"ordinary"}`,
	}
	for id, data := range rows {
		if _, err := repo.db.ExecContext(t.Context(), `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data) VALUES(?,?,?,?,?,?)`, accessWorkspace, "comment", "migration-issue", id, stamp, []byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := repo.migrateContentIndexes(ctx); err != nil {
		t.Fatalf("migration did not finish: %v", err)
	}
	result, err := repo.db.QueryContext(t.Context(), `SELECT id,record_version FROM workspace_content_records WHERE workspace_key=? AND resource_id=? ORDER BY id`, accessWorkspace, "migration-issue")
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	seen := map[string]int{}
	for result.Next() {
		var id string
		var version int
		if err := result.Scan(&id, &version); err != nil {
			t.Fatal(err)
		}
		seen[id] = version
	}
	if err := result.Err(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != len(rows) {
		t.Fatalf("rows after migration = %v, want exactly %d rewritten in place", seen, len(rows))
	}
	for id := range rows {
		if seen[id] != 1 {
			t.Fatalf("row %s has record_version %d after migration, want 1 (%v)", id, seen[id], seen)
		}
	}
}
