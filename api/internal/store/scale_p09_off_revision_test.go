package store

import "testing"

func TestScaleP09OffPreservesNoopCacheRevision(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	before, err := readMetadataStamp(t.Context(), f.db, workspace)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(t.Context(), `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key='absent-p09-row'`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 0 {
		t.Fatalf("expected a no-op metadata statement: rows=%d err=%v", n, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	after, err := readMetadataStamp(t.Context(), f.db, workspace)
	if err != nil || after.revision != before.revision+1 {
		t.Fatalf("off changed the legacy no-op invalidation: before=%+v after=%+v err=%v", before, after, err)
	}
	if tx.journalToken != "" {
		t.Fatal("off started a journal for a no-op write")
	}
}
