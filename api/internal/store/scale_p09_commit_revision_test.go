package store

import (
	"context"
	"testing"
	"time"

	"flow/api/internal/idgen"
)

func TestScaleP09FinalFlushPreservesCopyRevision(t *testing.T) {
	for _, mode := range []string{"shadow", "on"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_JOURNAL", mode)
			f := scaleP09Database(t)
			workspace := f.data.Workspace.URLKey
			before, err := readMetadataStamp(t.Context(), f.db, workspace)
			if err != nil {
				t.Fatal(err)
			}
			ctx, write := f.watchMetadataWriteFrom(t.Context(), workspace, copyRevision{workspaceID: before.workspaceID, revision: before.revision}, true)
			tx, err := f.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			scaleP09Put(t, tx, workspace, "projects", "revision-project", map[string]string{"id": "revision-project", "name": "Committed"})
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			after, err := readMetadataStamp(t.Context(), f.db, workspace)
			if err != nil || after.revision != before.revision+1 {
				t.Fatalf("metadata revision advanced more than once: before=%+v after=%+v err=%v", before, after, err)
			}
			write.mu.Lock()
			defer write.mu.Unlock()
			if !write.exact || write.next != after.revision {
				t.Fatalf("copy lost the preincrement stamp: exact=%v next=%d revision=%d", write.exact, write.next, after.revision)
			}
		})
	}
}

func TestScaleP09FinalFlushFailureRollsBackRevisions(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "shadow")
	f := scaleP09Database(t)
	if f.dialect != "sqlite" {
		t.Skip("SQLite fused statement failure")
	}
	workspace := f.data.Workspace.URLKey
	before, err := readMetadataStamp(t.Context(), f.db, workspace)
	if err != nil {
		t.Fatal(err)
	}
	trigger := idgen.New("flow_p09_abort_")
	if _, err := f.db.ExecContext(t.Context(), `CREATE TRIGGER `+trigger+` BEFORE INSERT ON record_journal WHEN NEW.workspace_id='`+f.data.Workspace.ID+`' BEGIN SELECT RAISE(ABORT,'p09 commit failure'); END`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
		defer cancel()
		if _, err := f.db.ExecContext(ctx, `DROP TRIGGER IF EXISTS `+trigger); err != nil {
			t.Errorf("clean up owned failure trigger: %v", err)
		}
	})
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	scaleP09Put(t, tx, workspace, "projects", "failed-revision", map[string]string{"id": "failed-revision", "name": "Rolled back"})
	if err := tx.Commit(); err == nil {
		t.Fatal("journal failure did not abort the write")
	}
	after, err := readMetadataStamp(t.Context(), f.db, workspace)
	if err != nil || after != before {
		t.Fatalf("cache revision survived rollback: before=%+v after=%+v err=%v", before, after, err)
	}
	for _, table := range []string{"workspace_metadata_records", "record_versions", "metadata_catalog"} {
		var count int
		if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table+` WHERE workspace_key=? AND field='projects' AND record_key='failed-revision'`, workspace).Scan(&count); err != nil || count != 0 {
			t.Fatalf("failed commit retained %s: count=%d err=%v", table, count, err)
		}
	}
}
