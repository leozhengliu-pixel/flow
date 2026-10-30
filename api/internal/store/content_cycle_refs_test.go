package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// TestIssueActivityCycleReferences covers the activity-to-cycle index the
// cycle graph reads instead of scanning every activity row.
func TestIssueActivityCycleReferences(t *testing.T) {
	ctx := context.Background()
	repository, workspace := perfWorkspace(t)
	key := workspace.Workspace.URLKey
	now := time.Now().UTC()
	event := domain.ActivityEvent{
		ID: "activity_cycle_ref", Type: "issue.updated", CreatedAt: now, Actor: workspace.Viewer,
		Metadata: map[string]string{"cycle": "cycle_new", "cycleBefore": "cycle_old"},
	}
	row, err := contentRecordRowFor(key, "activity", "issue_perf_001", event)
	if err != nil {
		t.Fatal(err)
	}
	if len(row.cycleRefs) != 2 {
		t.Fatalf("cycle refs: %#v", row.cycleRefs)
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeContentRecordRows(ctx, tx, []contentRecordRow{row}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, cycle := range []string{"cycle_new", "cycle_old"} {
		ids, err := repository.CycleHistoryIssueIDs(ctx, key, cycle)
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) != 1 || ids[0] != "issue_perf_001" {
			t.Fatalf("cycle %s ids: %#v", cycle, ids)
		}
	}
}

// TestActivityCycleBackfillIndexesExistingRows covers the one-time migration
// for activities written before the side table existed.
func TestActivityCycleBackfillIndexesExistingRows(t *testing.T) {
	ctx := context.Background()
	repository, workspace := perfWorkspace(t)
	key := workspace.Workspace.URLKey
	now := time.Now().UTC()
	event := domain.ActivityEvent{
		ID: "activity_backfill", Type: "issue.updated", CreatedAt: now, Actor: workspace.Viewer,
		Metadata: map[string]string{"initialCycle": "cycle_initial"},
	}
	raw, _ := json.Marshal(event)
	if _, err := repository.db.ExecContext(ctx, `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data,owner_id,parent_id,status,next_attempt_at,record_version) VALUES(?,?,?,?,?,?,?,?,?,?,1)`,
		key, "activity", "issue_perf_002", event.ID, now.Format(time.RFC3339Nano), raw, "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.ExecContext(ctx, `UPDATE issue_activity_cycles_migrations SET complete=0 WHERE workspace_key=?`, key); err != nil {
		t.Fatal(err)
	}
	// Before the backfill reaches the workspace the read falls back to the
	// activity JSON and still answers.
	fallback, err := repository.CycleHistoryIssueIDs(ctx, key, "cycle_initial")
	if err != nil {
		t.Fatal(err)
	}
	if len(fallback) != 1 || fallback[0] != "issue_perf_002" {
		t.Fatalf("fallback ids: %#v", fallback)
	}
	if err := repository.migrateContentActivityCycles(ctx); err != nil {
		t.Fatal(err)
	}
	ids, err := repository.CycleHistoryIssueIDs(ctx, key, "cycle_initial")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "issue_perf_002" {
		t.Fatalf("backfilled ids: %#v", ids)
	}
}
