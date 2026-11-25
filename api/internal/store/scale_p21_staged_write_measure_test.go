package store

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// A 10 MiB staged metadata row through both writers: the bytes reach the
// row exactly, the staging tables end empty, and the elapsed time is logged
// (each dialect's cost of the staged appends and the final assembly).
func TestScaleP21StagedWriteLargeRow(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	fixture := scaleP09Database(t)
	workspace := fixture.data.Workspace.URLKey
	view, err := fixture.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	body := []byte(`{"id":"staged-large","body":"` + strings.Repeat("é staged body ", (10<<20)/15) + `"}`)
	for _, growing := range []bool{false} {
		statements := 0
		ctx := WithSQLStatementObserver(t.Context(), func(string) { statements++ })
		tx, err := fixture.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		key := "staged-large"
		if growing {
			key = "staged-large-growing"
		}
		started := time.Now()
		stage, err := beginMetadataSQLWriteStage(ctx, tx, metadataSQLStageTarget{
			workspace: workspace, field: "agentSessions", key: key, order: 3, dataBytes: len(body), searchBytes: -1,
		})
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		writer, err := stage.writer(ctx, "data")
		if err == nil {
			_, err = writer.Write(body)
		}
		if err == nil {
			err = writer.flush(true)
		}
		if err == nil {
			err = stage.writeMetadataRow(ctx)
		}
		if err == nil {
			err = stage.Close(ctx)
		}
		elapsed := time.Since(started)
		if err != nil {
			_ = stage.Close(context.WithoutCancel(ctx))
			tx.Rollback()
			t.Fatal(err)
		}
		var stored []byte
		if err := tx.QueryRowContext(t.Context(), "SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?", workspace, "agentSessions", key).Scan(&stored); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		var scratch int
		if err := tx.QueryRowContext(t.Context(), "SELECT (SELECT COUNT(*) FROM bounded_metadata_write_stage)+(SELECT COUNT(*) FROM bounded_metadata_write_chunks)").Scan(&scratch); err != nil || scratch != 0 {
			tx.Rollback()
			t.Fatal("staging rows escaped", scratch, err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(stored, body) {
			t.Fatal("staged row bytes differ", len(stored), len(body))
		}
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("staged write kept admitted work")
		}
		t.Logf("dialect=%s growing=%t bytes=%d statements=%d staged_write=%s", fixture.dialect, growing, len(body), statements, elapsed)
	}
}
