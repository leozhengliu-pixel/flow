package store

import (
	"strings"
	"testing"
)

func TestScaleP09CommitPlan(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "shadow")
	f := scaleP09Database(t)
	if f.dialect != "sqlite" {
		t.Skip("SQLite commit plan")
	}
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := tx.beginRecordJournal(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `WITH RECURSIVE items(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM items WHERE i<2000) INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) SELECT ?,'projects',printf('plan-%04d',i),i,json_object('id',printf('plan-%04d',i),'name','Indexed project') FROM items`, f.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `ANALYZE`); err != nil {
		t.Fatal(err)
	}
	rows, err := f.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+recordJournalSQLCommit, "plan-token", "2026-11-12T00:00:00Z", []byte(`[]`))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		t.Logf("%d/%d %s", id, parent, detail)
		if strings.Contains(detail, "SCAN m ") || strings.Contains(detail, "SCAN c ") || strings.Contains(detail, "SCAN v ") {
			t.Errorf("commit scans a canonical/catalog/version table: %s", detail)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
