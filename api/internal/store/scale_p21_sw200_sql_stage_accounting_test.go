package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

type scaleP21SW200Result struct {
	row, search []byte
	budget      bool
	billed      int
}

func scaleP21SW200Write(t *testing.T, fixture scaleP09Fixture, raw []byte, limit int, staged bool) scaleP21SW200Result {
	t.Helper()
	tx, err := fixture.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	tx.maxTransactionBytes = limit
	workspace, id := fixture.data.Workspace.URLKey, "sw200-"+fixture.data.Workspace.ID
	stored := StripJSONNUL(raw)
	search := capSearchText(metadataSearchText(stored), fixture.db.searchMaxChars)
	if !staged {
		err = writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"projects", id}, metadataRecord{data: raw, order: 7}, nil)
	} else {
		var stage *metadataSQLWriteStage
		stage, err = beginMetadataSQLWriteStage(t.Context(), tx, metadataSQLStageTarget{
			workspace: workspace, field: "projects", key: id, order: 7,
			dataBytes: len(stored), searchBytes: len(search),
		})
		if err == nil {
			defer stage.Close(context.WithoutCancel(t.Context()))
			var data, content *metadataSQLStageWriter
			data, err = stage.writer(t.Context(), "data")
			if err == nil {
				_, err = data.Write(stored)
			}
			if err == nil {
				err = data.flush(true)
			}
			if err == nil {
				err = stage.writeMetadataRow(t.Context())
			}
			if err == nil {
				content, err = stage.writer(t.Context(), "content")
			}
			if err == nil {
				_, err = content.Write([]byte(search))
			}
			if err == nil {
				err = content.flush(true)
			}
			if err == nil {
				err = stage.writeSearchDocument(t.Context())
			}
			if err == nil {
				err = stage.Close(t.Context())
			}
			if err == nil {
				chunks := (len(stored)+metadataRecordChunkBytes-1)/metadataRecordChunkBytes +
					(len(search)+metadataRecordChunkBytes-1)/metadataRecordChunkBytes
				if stage.statements != 4+chunks {
					t.Fatal("SQL stage write count exceeded its per-chunk bound", stage.statements, chunks)
				}
				if stage.inputBytes > tx.writtenBytes+stage.statements*1024 {
					t.Fatal("staging SQL input overhead exceeded its fixed per-statement bound", stage.inputBytes, tx.writtenBytes, stage.statements)
				}
				legacyInput := tx.writtenBytes +
					len(rewriteSQL(metadataSQLStageLegacyRowSQL, tx.dialect)) - len(metadataSQLStageLegacyRowSQL) +
					len(rewriteSQL(metadataSQLStageLegacySearchSQL(tx.dialect), tx.dialect)) - len(metadataSQLStageLegacySearchSQL(tx.dialect))
				t.Logf("legacy_logical_billed_bytes=%d legacy_driver_input_bytes=%d staged_driver_input_bytes=%d legacy_write_queries=2 staged_write_queries=%d payload_bytes=%d search_bytes=%d",
					tx.writtenBytes, legacyInput, stage.inputBytes, stage.statements, len(stored), len(search))
			}
		}
	}
	result := scaleP21SW200Result{budget: errors.Is(err, ErrTransactionWriteBudget), billed: tx.writtenBytes}
	if err != nil {
		if !result.budget {
			t.Fatal(err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := fixture.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM bounded_metadata_write_stage`).Scan(&count); err != nil || count != 0 {
			t.Fatal("budget refusal retained staged input", count, err)
		}
		return result
	}
	if err := tx.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key=?`, workspace, id).Scan(&result.row); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRowContext(t.Context(), `SELECT content FROM metadata_search_documents WHERE workspace_key=? AND field='projects' AND record_key=?`, workspace, id).Scan(&result.search); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := tx.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM bounded_metadata_write_stage`).Scan(&count); err != nil || count != 0 {
		t.Fatal("successful write retained staged input", count, err)
	}
	return result
}

func TestScaleP21SW200SQLStageLegacyBudgetBoundaries(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	fixture := scaleP09Database(t)
	for _, size := range []int{32, 3*metadataRecordChunkBytes + 11} {
		project := domain.Project{
			ID: "sw200-" + fixture.data.Workspace.ID, Name: "Budget <project>",
			Description: strings.Repeat("x", size) + "\x00\u4e2d\U0001f600",
			Status:      domain.ProjectStatus{ID: "active", Name: "Active", Type: "started"},
		}
		raw, err := json.Marshal(project)
		if err != nil {
			t.Fatal(err)
		}
		stored := StripJSONNUL(raw)
		workspace, key := fixture.data.Workspace.URLKey, project.ID
		rowBill := len(metadataSQLStageLegacyRowSQL) + len(workspace) + len("projects") + len(key) + 16 + len(stored)
		fullBill := rowBill + len(metadataSQLStageLegacySearchSQL(fixture.dialect)) + len(workspace) + len("projects") + len(key) + len(capSearchText(metadataSearchText(stored), fixture.db.searchMaxChars))
		for _, boundary := range []int{rowBill, fullBill} {
			for _, delta := range []int{-1, 0, 1} {
				t.Run(fmt.Sprintf("bytes=%d/boundary=%d/delta=%d", size, boundary, delta), func(t *testing.T) {
					limit := boundary + delta
					want := scaleP21SW200Write(t, fixture, raw, limit, false)
					got := scaleP21SW200Write(t, fixture, raw, limit, true)
					if !reflect.DeepEqual(got, want) {
						t.Fatal("internal chunk controls changed literal transaction budget boundary", limit, got.billed, want.billed, got.budget, want.budget)
					}
					if (limit < fullBill) != got.budget {
						t.Fatal("boundary fixture failed to exercise exact original SQL cap", limit, fullBill, got.budget)
					}
				})
			}
		}
	}
}

func TestScaleP21SW200SQLStageRejectsAccountingForgery(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	fixture := scaleP09Database(t)
	tx, err := fixture.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	target := metadataSQLStageTarget{
		workspace: fixture.data.Workspace.URLKey, field: "agentSessions", key: "sw200-private",
		dataBytes: 8, searchBytes: -1,
	}
	stage, err := beginMetadataSQLWriteStage(t.Context(), tx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close(context.WithoutCancel(t.Context()))
	before := tx.writtenBytes
	var beforeRows int
	if err := tx.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=?`, target.workspace).Scan(&beforeRows); err != nil {
		t.Fatal(err)
	}
	claim := &metadataSQLStageStatement{stage: stage, operation: metadataSQLStageAppendData}
	ctx := context.WithValue(t.Context(), metadataSQLStageStatementKey{}, claim)
	if _, err := tx.ExecContext(ctx, "DELETE FROM workspace_metadata_records WHERE workspace_key=?", target.workspace); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("stage accounting accepted an arbitrary product write", err)
	}
	query, err := stage.statementQuery(metadataSQLStageAppendData)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, query, "small", "another-operation"); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("stage accounting accepted another operation's row", err)
	}
	if _, err := tx.ExecContext(ctx, query, strings.Repeat("x", 9), stage.id); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("stage accounting exceeded its admitted final bytes", err)
	}
	other, err := fixture.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Rollback()
	if _, err := other.ExecContext(ctx, query, "small", stage.id); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("stage accounting crossed transaction ownership", err)
	}
	if tx.writtenBytes != before {
		t.Fatal("rejected accounting claims changed the bill", before, tx.writtenBytes)
	}
	var afterRows int
	if err := tx.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=?`, target.workspace).Scan(&afterRows); err != nil || afterRows != beforeRows {
		t.Fatal("a forged control claim changed product rows", beforeRows, afterRows, err)
	}
	copyOfStage := *stage
	forged := context.WithValue(t.Context(), metadataSQLStageStatementKey{}, &metadataSQLStageStatement{
		stage: &copyOfStage, operation: metadataSQLStageAppendData,
	})
	if _, err := tx.ExecContext(forged, query, "small", stage.id); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("an unregistered stage copy received accounting authority", err)
	}
	stage.target.dataBytes++
	if _, err := tx.ExecContext(ctx, query, "small", stage.id); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("a changed declaration received accounting authority", err)
	}
	stage.target = stage.admittedTarget
	writer, err := stage.writer(t.Context(), "data")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("12345678")); err != nil {
		t.Fatal(err)
	}
	if err := writer.flush(true); err != nil {
		t.Fatal(err)
	}
	if err := stage.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, query, "small", stage.id); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("closed stage still bypassed control-statement accounting", err)
	}
}

func TestScaleP21SW200SQLStageLargeDeclaredBytesUseTransactionBudget(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	fixture := scaleP09Database(t)
	tx, err := fixture.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	statements := 0
	ctx := WithSQLStatementObserver(t.Context(), func(_ string) { statements++ })
	_, err = beginMetadataSQLWriteStage(ctx, tx, metadataSQLStageTarget{
		workspace: fixture.data.Workspace.URLKey, field: "agentSessions", key: "sw200-large-declaration",
		dataBytes: metadataWorkBytes + 1, searchBytes: -1,
	})
	if !errors.Is(err, ErrTransactionWriteBudget) || statements != 0 {
		t.Fatal("streamed SQL size was confused with heap admission or grew before billing", statements, err)
	}
	if tx.metadataSQLStages != 0 || len(tx.metadataSQLStageOwners) != 0 {
		t.Fatal("refused declaration retained stage authority")
	}
}

func TestScaleP21SW200SQLStageCanonicalChargeMatchesLiteral(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	fixture := scaleP09Database(t)
	project := domain.Project{ID: "sw200-" + fixture.data.Workspace.ID, Name: "SQL charge"}
	raw, _ := json.Marshal(project)
	want := scaleP21SW200Write(t, fixture, raw, 0, false)
	got := scaleP21SW200Write(t, fixture, raw, 0, true)
	if want.billed != got.billed || !bytes.Equal(want.row, got.row) || !bytes.Equal(want.search, got.search) {
		t.Fatal("staged logical charges drifted from original canonical statements", got.billed, want.billed)
	}
}

func scaleP21SW200RootWrite(t *testing.T, fixture scaleP09Fixture, raw []byte, updated string, limit int, staged bool) scaleP21SW200Result {
	t.Helper()
	tx, err := fixture.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	tx.maxTransactionBytes = limit
	workspace, workspaceID := fixture.data.Workspace.URLKey, fixture.data.Workspace.ID
	if !staged {
		_, err = tx.ExecContext(t.Context(), `UPDATE workspace_states SET workspace_id=?,data=?,updated_at=? WHERE workspace_key=?`,
			workspaceID, raw, updated, workspace)
	} else {
		var stage *metadataSQLWriteStage
		stage, err = beginMetadataSQLWriteStage(t.Context(), tx, metadataSQLStageTarget{
			workspaceState: true, workspace: workspace, workspaceID: workspaceID,
			updatedAt: updated, dataBytes: len(raw), searchBytes: -1,
		})
		if err == nil {
			defer stage.Close(context.WithoutCancel(t.Context()))
			var writer *metadataSQLStageWriter
			writer, err = stage.writer(t.Context(), "data")
			if err == nil {
				_, err = writer.Write(raw)
			}
			if err == nil {
				err = writer.flush(true)
			}
			if err == nil {
				err = stage.writeWorkspaceState(t.Context())
			}
			if err == nil {
				err = stage.Close(t.Context())
			}
			if err == nil {
				chunks := (len(raw) + metadataRecordChunkBytes - 1) / metadataRecordChunkBytes
				if stage.statements != 3+chunks || stage.inputBytes > tx.writtenBytes+stage.statements*1024 {
					t.Fatal("root staging exceeded SQL input/count overhead bound", stage.statements, chunks, stage.inputBytes, tx.writtenBytes)
				}
				t.Logf("root_legacy_billed_bytes=%d root_staged_driver_input_bytes=%d legacy_write_queries=1 staged_write_queries=%d",
					tx.writtenBytes, stage.inputBytes, stage.statements)
			}
		}
	}
	result := scaleP21SW200Result{budget: errors.Is(err, ErrTransactionWriteBudget), billed: tx.writtenBytes}
	if err != nil {
		if !result.budget {
			t.Fatal(err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := fixture.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM bounded_metadata_write_stage`).Scan(&count); err != nil || count != 0 {
			t.Fatal("root budget refusal retained staging data", count, err)
		}
		return result
	}
	var storedID, storedAt string
	if err := tx.QueryRowContext(t.Context(), `SELECT workspace_id,data,updated_at FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&storedID, &result.row, &storedAt); err != nil {
		t.Fatal(err)
	}
	if storedID != workspaceID || storedAt != updated {
		t.Fatal("root staging changed bound identity or timestamp", storedID, storedAt)
	}
	return result
}

func TestScaleP21SW200SQLStageRootBudgetBoundaries(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	fixture := scaleP09Database(t)
	for _, size := range []int{32, 3*metadataRecordChunkBytes + 11} {
		raw, err := json.Marshal(map[string]any{
			"workspace": map[string]any{"id": fixture.data.Workspace.ID},
			"settings":  map[string]any{"body": strings.Repeat("x", size) + "\u4e2d\U0001f600"},
		})
		if err != nil {
			t.Fatal(err)
		}
		updated := "2026-11-16T05:10:00.123456789Z"
		bill := len(metadataSQLStageLegacyRootSQL) + len(fixture.data.Workspace.ID) + len(raw) + len(updated) + len(fixture.data.Workspace.URLKey)
		for _, delta := range []int{-1, 0, 1} {
			t.Run(fmt.Sprintf("bytes=%d/delta=%d", size, delta), func(t *testing.T) {
				want := scaleP21SW200RootWrite(t, fixture, raw, updated, bill+delta, false)
				got := scaleP21SW200RootWrite(t, fixture, raw, updated, bill+delta, true)
				if !reflect.DeepEqual(got, want) || (delta < 0) != got.budget {
					t.Fatal("staged root changed literal budget or exact postimage", delta, got.billed, want.billed, got.budget, want.budget)
				}
			})
		}
	}
}
