package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"flow/api/internal/idgen"
)

func scaleP21SW218History(t *testing.T, tx *sqlTx, workspace, invalidID string) map[string]issueFieldHistory {
	t.Helper()
	rows, err := tx.QueryContext(t.Context(), `SELECT id,data FROM workspace_content_records WHERE workspace_key=? AND kind=?`, workspace, issueFieldHistoryKind)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string]issueFieldHistory{}
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			t.Fatal(err)
		}
		if id == invalidID {
			if string(raw) != "{" {
				t.Error("untouched invalid history changed", id)
			}
			continue
		}
		var value issueFieldHistory
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		result[id] = value
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestScaleP21SW218LegacyIssueWorkDifferential(t *testing.T) {
	f, view := scaleP21SW78Fixture(t)
	for _, variant := range []string{"legal-four-MiB", "admission-flush", "admission-flush-duplicate", "admission-flush-duplicate-absent-history", "admission-flush-duplicate-nil-fields", "unchanged-invalid-history", "skipped-invalid-history"} {
		t.Run(variant, func(t *testing.T) {
			forcedFlush := strings.HasPrefix(variant, "admission-flush")
			invalidHistory := strings.HasSuffix(variant, "invalid-history")
			prefix := idgen.New("sw218_")
			data := scaleP21SW93Data(f, prefix, 0)
			for i := range data.Issues {
				data.Issues[i].Version = 10
			}
			if variant == "legal-four-MiB" {
				data.Issues[0].Description = strings.Repeat("x", 4<<20)
			} else if forcedFlush {
				for len(data.Issues) < 13 {
					issue := data.Issues[1]
					issue.ID = fmt.Sprintf("%sissue-%02d", prefix, len(data.Issues))
					issue.Identifier = issue.ID
					issue.Number = len(data.Issues) + 1
					data.Issues = append(data.Issues, issue)
				}
			}
			var wantRows map[string][][]any
			var wantCounts map[string]map[issueStatsKey]int64
			var wantHistory map[string]issueFieldHistory
			var wantSignals []notificationSignal
			for _, bounded := range []bool{false, true} {
				tx, ctx := scaleP21SW93Begin(t, f, data)
				workspace := data.Workspace.URLKey
				oldBase := newRecordBase()
				loaded := cloneBootstrap(collectionMetadata(data))
				var err error
				loaded.Issues, err = f.readIssueRecordsWithBase(ctx, workspace, oldBase)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.hydrateContentRecordsWithBase(ctx, workspace, &loaded, oldBase); err != nil {
					t.Fatal(err)
				}
				refreshDisplayReferences(&loaded)
				refreshIssueReferences(&loaded)
				if err := oldBase.fingerprintLoaded(loaded); err != nil {
					t.Fatal(err)
				}
				stamp := copyStamp{workspaceID: data.Workspace.ID, revision: -1}
				source, err := captureMetadataLogicalSource(ctx, collectionMetadata(data), stamp)
				if err != nil {
					t.Fatal(err)
				}
				base, err := view.newLegacyRecordBase(ctx, source)
				source.Release()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(base.Close)
				scaleP21SW93Remember(t, base, tx, workspace)
				prepared, err := captureMetadataLogicalSource(ctx, loaded, stamp)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(prepared.Release)
				if err := base.FingerprintLoaded(ctx, prepared); err != nil {
					t.Fatal(err)
				}
				target := cloneBootstrap(loaded)
				for i := range target.Issues {
					if variant == "legal-four-MiB" && i != 0 {
						continue
					}
					if invalidHistory && i == 0 {
						continue
					}
					target.Issues[i].Title = fmt.Sprintf("Changed occurrence %d", i)
					target.Issues[i].Priority = 1
					target.Issues[i].Version = 11
					if variant != "legal-four-MiB" {
						target.Issues[i].Description = strings.Repeat("y", 1<<20)
					}
				}
				if strings.HasPrefix(variant, "admission-flush-duplicate") {
					duplicate := target.Issues[0]
					duplicate.Title = "Last occurrence after admission flush"
					duplicate.Priority = data.Issues[0].Priority
					duplicate.Version = 12
					target.Issues[len(target.Issues)-1] = duplicate
				}
				switch variant {
				case "admission-flush-duplicate-absent-history":
					if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_content_records WHERE workspace_key=? AND kind=? AND id=?`, workspace, issueFieldHistoryKind, loaded.Issues[0].ID); err != nil {
						t.Fatal(err)
					}
				case "admission-flush-duplicate-nil-fields":
					history := issueFieldHistory{ID: loaded.Issues[0].ID, CreatedAt: loaded.Issues[0].UpdatedAt, Version: 10, BaseVersion: 7}
					raw, err := json.Marshal(history)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := tx.ExecContext(ctx, `UPDATE workspace_content_records SET data=? WHERE workspace_key=? AND kind=? AND id=?`, raw, workspace, issueFieldHistoryKind, history.ID); err != nil {
						t.Fatal(err)
					}
				}
				invalidID := ""
				if invalidHistory {
					invalidID = loaded.Issues[0].ID
					if _, err := tx.ExecContext(ctx, `UPDATE workspace_content_records SET data=? WHERE workspace_key=? AND kind=? AND id=?`, []byte("{"), workspace, issueFieldHistoryKind, invalidID); err != nil {
						t.Fatal(err)
					}
					if variant == "skipped-invalid-history" {
						peer := loaded.Issues[0]
						peer.Title = "Canonical peer title"
						raw, err := json.Marshal(peer)
						if err != nil {
							t.Fatal(err)
						}
						if _, err := tx.ExecContext(ctx, `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, raw, workspace, invalidID); err != nil {
							t.Fatal(err)
						}
					}
				}
				candidate, err := captureMetadataLogicalSource(ctx, target, stamp)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(candidate.Release)
				var historyWrites atomic.Int64
				observed := WithSQLStatementObserver(ctx, func(query string) {
					if strings.HasPrefix(query, contentRecordUpsertPrefix) {
						historyWrites.Add(1)
					}
				})
				limit := view.metadataResidency.work.limit
				if bounded {
					if forcedFlush {
						// Leave room for one legal row while modelling other
						// requests' ownership in the process-wide budget.
						view.metadataResidency.work.limit = 64 << 20
					}
					err = base.Persist(observed, tx, workspace, candidate)
					view.metadataResidency.work.limit = limit
				} else {
					err = scaleP21SW93Literal(observed, f, tx, workspace, target, oldBase)
				}
				if err != nil {
					t.Fatalf("bounded=%t legal write refused: %v", bounded, err)
				}
				history := scaleP21SW218History(t, tx, workspace, invalidID)
				rows, counts := scaleP21SW93Rows(t, tx, workspace), scaleP21SW93Counts(tx)
				if bounded {
					if forcedFlush && historyWrites.Load() < 2 {
						t.Fatal("fixture did not force admission flush inside 64-target group")
					}
					if !reflect.DeepEqual(history, wantHistory) {
						if len(history) != len(wantHistory) {
							t.Errorf("history row counts differ: literal=%d bounded=%d", len(wantHistory), len(history))
						}
						for id, expected := range wantHistory {
							if actual, ok := history[id]; !ok || !reflect.DeepEqual(actual, expected) {
								before, _ := json.Marshal(expected)
								after, _ := json.Marshal(actual)
								t.Errorf("history %s differs: literal=%s bounded=%s", id, before, after)
							}
						}
					}
					for table, expected := range wantRows {
						if !reflect.DeepEqual(rows[table], expected) {
							t.Errorf("canonical %s rows differ", table)
						}
					}
					if !reflect.DeepEqual(counts, wantCounts) {
						t.Errorf("queued stats/counts differ: literal=%v bounded=%v", wantCounts, counts)
					}
					if !reflect.DeepEqual(tx.signals, wantSignals) {
						t.Error("queued notification effects differ")
					}
				} else {
					wantRows, wantCounts, wantHistory = rows, counts, history
					wantSignals = append([]notificationSignal(nil), tx.signals...)
				}
				t.Logf("bounded=%t history write batches=%d", bounded, historyWrites.Load())
				candidate.Release()
				prepared.Release()
				scaleP21SW70Usable(t, tx)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				base.Close()
				base.Close()
				if used := view.MetadataWorkUsedForTesting(); used != 0 {
					t.Fatalf("record work retained after rollback: %d", used)
				}
				var remaining int
				if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM issue_records WHERE workspace_key=? AND id LIKE ?`, workspace, prefix+"%").Scan(&remaining); err != nil || remaining != 0 {
					t.Fatal("rollback retained issue rows", remaining, err)
				}
			}
		})
	}
}

func TestScaleP21SW218LegacyIssueWorkHistoryDenialIsAtomic(t *testing.T) {
	f, view := scaleP21SW78Fixture(t)
	data := scaleP21SW93Data(f, idgen.New("sw218_history_"), 0)
	before := data.Issues[0]
	before.Version = 10
	issue := before
	issue.Version = 11
	issue.Title = "Denied title"
	previous, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	for _, allowance := range []int{1, 8 << 10} {
		t.Run(fmt.Sprint(allowance), func(t *testing.T) {
			fields := map[string]int64{"title": 10}
			history := &legacyRecordIssueHistory{
				store:     view,
				histories: map[string][]issueFieldHistory{issue.ID: {{ID: issue.ID, Version: 10, BaseVersion: 7, Fields: fields}}},
				loaded:    map[string]bool{issue.ID: true},
				owner:     &ResourceRead{store: view},
			}
			defer history.Close()
			baseline, limit := view.MetadataWorkUsedForTesting(), view.metadataResidency.work.limit
			view.metadataResidency.work.limit = baseline + allowance
			err := history.append(t.Context(), data.Workspace.URLKey, issue, previous)
			view.metadataResidency.work.limit = limit
			if !errors.Is(err, ErrMetadataWorkBudget) {
				t.Fatal("history append did not refuse admission", err)
			}
			if !reflect.DeepEqual(fields, map[string]int64{"title": 10}) {
				t.Fatal("denied append mutated frozen history fields", fields)
			}
			if len(history.rows) != 0 || len(history.rowOwners) != 0 {
				t.Fatal("denied append queued a checkpoint")
			}
			history.Close()
			if used := view.MetadataWorkUsedForTesting(); used != baseline {
				t.Fatal("denied append retained work", used, baseline)
			}
		})
	}
}

func TestScaleP21SW218LegacyIssueWorkCancellation(t *testing.T) {
	f, view := scaleP21SW78Fixture(t)
	for _, mode := range []string{"before-write", "after-history-write"} {
		t.Run(mode, func(t *testing.T) {
			for _, bounded := range []bool{false, true} {
				data := scaleP21SW93Data(f, idgen.New("sw218_cancel_"), 0)
				tx, ctx := scaleP21SW93Begin(t, f, data)
				oldBase := newRecordBase()
				loaded := cloneBootstrap(collectionMetadata(data))
				var err error
				loaded.Issues, err = f.readIssueRecordsWithBase(ctx, data.Workspace.URLKey, oldBase)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.hydrateContentRecordsWithBase(ctx, data.Workspace.URLKey, &loaded, oldBase); err != nil {
					t.Fatal(err)
				}
				refreshDisplayReferences(&loaded)
				refreshIssueReferences(&loaded)
				if err := oldBase.fingerprintLoaded(loaded); err != nil {
					t.Fatal(err)
				}
				stamp := copyStamp{workspaceID: data.Workspace.ID, revision: -1}
				source, err := captureMetadataLogicalSource(ctx, collectionMetadata(data), stamp)
				if err != nil {
					t.Fatal(err)
				}
				base, err := view.newLegacyRecordBase(ctx, source)
				source.Release()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(base.Close)
				scaleP21SW93Remember(t, base, tx, data.Workspace.URLKey)
				prepared, err := captureMetadataLogicalSource(ctx, loaded, stamp)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(prepared.Release)
				if err := base.FingerprintLoaded(ctx, prepared); err != nil {
					t.Fatal(err)
				}
				target := cloneBootstrap(loaded)
				target.Issues[0].Title = "Cancellation must roll this back"
				target.Issues[0].Description = strings.Repeat("z", 1<<20)
				candidate, err := captureMetadataLogicalSource(ctx, target, stamp)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(candidate.Release)
				cancelled, cancel := context.WithCancel(ctx)
				defer cancel()
				var reached atomic.Bool
				if mode == "before-write" {
					cancel()
				} else {
					cancelled = WithSQLStatementObserver(cancelled, func(query string) {
						if strings.HasPrefix(query, "INSERT INTO issue_records(") {
							reached.Store(true)
							cancel()
						}
					})
				}
				if bounded {
					err = base.Persist(cancelled, tx, data.Workspace.URLKey, candidate)
				} else {
					err = scaleP21SW93Literal(cancelled, f, tx, data.Workspace.URLKey, target, oldBase)
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("bounded=%t cancellation differs: %v", bounded, err)
				}
				if mode == "after-history-write" && !reached.Load() {
					t.Fatal("cancellation did not reach issue write")
				}
				candidate.Release()
				prepared.Release()
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				base.Close()
				if used := view.MetadataWorkUsedForTesting(); used != 0 {
					t.Fatalf("cancellation retained work: %d", used)
				}
				var remaining int
				if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM issue_records WHERE workspace_key=? AND id=?`, data.Workspace.URLKey, data.Issues[0].ID).Scan(&remaining); err != nil || remaining != 0 {
					t.Fatal("canceled transaction retained issue row", remaining, err)
				}
			}
		})
	}
}
