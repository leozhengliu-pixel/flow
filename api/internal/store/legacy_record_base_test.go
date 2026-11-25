package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW93Rows(t *testing.T, tx *sqlTx, workspace string) map[string][][]any {
	t.Helper()
	result := map[string][][]any{}
	for _, table := range []string{"issue_records", "workspace_content_records", "issue_label_records", "issue_subscriber_records", "issue_actor_records", "issue_attribute_records", "issue_attachment_records", "issue_search_documents", "issue_identifier_aliases", "issue_permission_records", "issue_collection_counts", "issue_number_sequences"} {
		rows, err := tx.QueryContext(t.Context(), "SELECT * FROM "+table+" WHERE workspace_key=? ORDER BY 1,2", workspace)
		if err != nil {
			t.Fatal(table, err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			targets := make([]any, len(columns))
			for i := range values {
				targets[i] = &values[i]
			}
			if err := rows.Scan(targets...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			for i, value := range values {
				if raw, ok := value.([]byte); ok {
					values[i] = string(raw)
				}
			}
			result[table] = append(result[table], values)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if tx.dialect != "sqlite" {
			// ORDER BY 1,2 is not a total order; PostgreSQL and MySQL return
			// ties in physical order, which updates change. Compare as sets.
			slices.SortFunc(result[table], func(a, b []any) int { return strings.Compare(fmt.Sprint(a...), fmt.Sprint(b...)) })
		}
	}
	return result
}

func scaleP21SW93Counts(tx *sqlTx) map[string]map[issueStatsKey]int64 {
	result := map[string]map[issueStatsKey]int64{}
	if tx.issueCounts == nil {
		return result
	}
	for workspace, values := range tx.issueCounts.deltas {
		result[workspace] = map[issueStatsKey]int64{}
		for key, value := range values {
			result[workspace][key] = value
		}
	}
	return result
}

func scaleP21SW93Remember(t *testing.T, base *legacyRecordBase, tx *sqlTx, workspace string) {
	t.Helper()
	rows, err := tx.QueryContext(t.Context(), `SELECT id,data FROM issue_records WHERE workspace_key=? ORDER BY collection_order,id`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err := base.RememberStored(t.Context(), "issue", "", id, raw); err != nil {
			rows.Close()
			t.Fatal(err)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"activity", "comment", "notification", "delivery"} {
		rows, err := tx.QueryContext(t.Context(), `SELECT resource_id,id,data FROM workspace_content_records WHERE workspace_key=? AND kind=? ORDER BY created_at,id`, workspace, kind)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var resource, id string
			var raw []byte
			if err := rows.Scan(&resource, &id, &raw); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if err := base.RememberStored(t.Context(), kind, resource, id, raw); err != nil {
				rows.Close()
				t.Fatal(err)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func scaleP21SW93Data(f scaleP09Fixture, prefix string, repetitions int) domain.Bootstrap {
	at := time.Date(2026, 11, 15, 1, 2, 3, 456, time.UTC)
	data := cloneBootstrap(collectionMetadata(f.data))
	issue := domain.Issue{ID: prefix + "issue", Identifier: prefix + "ISSUE", Number: 1, Title: "Target issue", Team: data.Teams[0], Creator: data.Viewer, CreatedAt: at, UpdatedAt: at, SubscriberIDs: []string{data.Viewer.ID}, State: data.States[0]}
	other := issue
	other.ID, other.Identifier, other.Number = prefix+"other", prefix+"OTHER", 2
	other.Description = strings.Repeat("Unrelated full issue body. ", repetitions)
	data.Issues = []domain.Issue{issue, other}
	data.Comments = map[string][]domain.Comment{issue.ID: {{ID: prefix + "comment", Body: "Original callback comment", User: data.Viewer, CreatedAt: at, Version: 1}, {ID: prefix + "other-comment", Body: "Unchanged peer comment", User: data.Viewer, CreatedAt: at}}}
	data.Activities = map[string][]domain.ActivityEvent{issue.ID: {{ID: prefix + "activity", Type: "comment.created", Actor: data.Viewer, CreatedAt: at, Metadata: domain.ActivityMetadata{"commentId": prefix + "comment"}}}}
	data.Notifications = []domain.Notification{{ID: prefix + "notification", IssueID: issue.ID, RecipientID: data.Viewer.ID, Actor: data.Viewer, CreatedAt: at, UpdatedAt: at}}
	data.NotificationDeliveries = []domain.NotificationDelivery{{ID: prefix + "delivery", NotificationID: prefix + "notification", RecipientID: data.Viewer.ID, Channel: "desktop", Status: "pending", CreatedAt: at, UpdatedAt: at}}
	return data
}

func scaleP21SW93Begin(t *testing.T, f scaleP09Fixture, data domain.Bootstrap) (*sqlTx, context.Context) {
	t.Helper()
	tx, err := f.db.BeginTx(t.Context(), f.recordTxOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: data.Workspace.URLKey, reader: tx})
	workspace := data.Workspace.URLKey
	if err := f.replaceIssueRecords(ctx, tx, workspace, data.Issues, data, nil); err != nil {
		t.Fatal(err)
	}
	if err := syncContentRecordsFrom(ctx, tx, workspace, "activity", data.Activities, data, nil); err != nil {
		t.Fatal(err)
	}
	if err := syncContentRecordsFrom(ctx, tx, workspace, "comment", data.Comments, data, nil); err != nil {
		t.Fatal(err)
	}
	if err := syncContentRecordsFrom(ctx, tx, workspace, "notification", notificationRecords(data.Notifications), data, nil); err != nil {
		t.Fatal(err)
	}
	if err := syncContentRecordsFrom(ctx, tx, workspace, "delivery", map[string][]domain.NotificationDelivery{"": data.NotificationDeliveries}, data, nil); err != nil {
		t.Fatal(err)
	}
	return tx, ctx
}

func scaleP21SW93Literal(ctx context.Context, f scaleP09Fixture, tx *sqlTx, workspace string, target domain.Bootstrap, base *recordBase) error {
	if err := f.lockReplacedRecords(ctx, tx, workspace, base, true, []string{"activity", "comment", "delivery", "notification"}); err != nil {
		return err
	}
	if err := syncContentRecordsFrom(ctx, tx, workspace, "delivery", map[string][]domain.NotificationDelivery{"": target.NotificationDeliveries}, target, base.contentKind("delivery")); err != nil {
		return err
	}
	if err := syncContentRecordsFrom(ctx, tx, workspace, "notification", notificationRecords(target.Notifications), target, base.contentKind("notification")); err != nil {
		return err
	}
	if err := syncContentRecordsFrom(ctx, tx, workspace, "activity", target.Activities, target, base.contentKind("activity")); err != nil {
		return err
	}
	if err := syncContentRecordsFrom(ctx, tx, workspace, "comment", target.Comments, target, base.contentKind("comment")); err != nil {
		return err
	}
	return f.replaceIssueRecords(ctx, tx, workspace, target.Issues, target, base.issueRecords())
}

func scaleP21SW93Peer(t *testing.T, tx *sqlTx, workspace, mode string, data domain.Bootstrap) {
	t.Helper()
	putComment := func(value domain.Comment) {
		if err := writeContentRecord(t.Context(), tx, workspace, "comment", data.Issues[0].ID, value); err != nil {
			t.Fatal(err)
		}
	}
	switch mode {
	case "peer-unchanged", "peer-conflict":
		index := 1
		if mode == "peer-conflict" {
			index = 0
		}
		value := data.Comments[data.Issues[0].ID][index]
		value.Body = "Canonical peer body"
		putComment(value)
	case "peer-delete":
		value := data.Comments[data.Issues[0].ID][1]
		if _, err := tx.ExecContext(t.Context(), `DELETE FROM workspace_content_records WHERE workspace_key=? AND kind='comment' AND resource_id=? AND id=?`, workspace, data.Issues[0].ID, value.ID); err != nil {
			t.Fatal(err)
		}
	case "peer-insert":
		value := data.Comments[data.Issues[0].ID][1]
		value.ID += "-peer"
		value.Body = "Unknown peer row"
		putComment(value)
	case "peer-issue":
		issue := data.Issues[1]
		issue.Title = "Canonical peer issue title"
		raw, err := json.Marshal(issue)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(t.Context(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, raw, workspace, issue.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScaleP21SW93LegacyRecordBaseDifferential(t *testing.T) {
	f, view := scaleP21SW78Fixture(t)
	prefix := idgen.New("sw93_")
	for _, variant := range []string{"comment-edit", "comment-delete", "new-activity-notification-delivery", "unchanged", "peer-unchanged", "peer-delete", "peer-insert", "peer-conflict", "peer-issue", "state-type-index-refresh", "supplied-content", "physical-issue-id", "physical-comment-id", "physical-notification-resource", "duplicate-content-id", "duplicate-issues-one-batch", "duplicate-issues-next-batch", "duplicate-issues-next-batch-loaded"} {
		t.Run(variant, func(t *testing.T) {
			data := scaleP21SW93Data(f, prefix, 2000)
			var wantRows map[string][][]any
			var wantErr error
			var wantCounts map[string]map[issueStatsKey]int64
			var wantSignals []notificationSignal
			for _, bounded := range []bool{false, true} {
				tx, ctx := scaleP21SW93Begin(t, f, data)
				workspace := data.Workspace.URLKey
				switch variant {
				case "physical-issue-id":
					value := data.Issues[0]
					value.ID = prefix + "logical-issue"
					raw, err := json.Marshal(value)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := tx.ExecContext(ctx, `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, raw, workspace, data.Issues[0].ID); err != nil {
						t.Fatal(err)
					}
				case "physical-comment-id":
					value := data.Comments[data.Issues[0].ID][0]
					value.ID = prefix + "logical-comment"
					raw, err := json.Marshal(value)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := tx.ExecContext(ctx, `UPDATE workspace_content_records SET data=? WHERE workspace_key=? AND kind='comment' AND resource_id=? AND id=?`, raw, workspace, data.Issues[0].ID, data.Comments[data.Issues[0].ID][0].ID); err != nil {
						t.Fatal(err)
					}
				case "physical-notification-resource":
					if _, err := tx.ExecContext(ctx, `UPDATE workspace_content_records SET resource_id=? WHERE workspace_key=? AND kind='notification' AND id=?`, prefix+"foreign-resource", workspace, data.Notifications[0].ID); err != nil {
						t.Fatal(err)
					}
				}
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
				installed := collectionMetadata(data)
				if variant == "supplied-content" || variant == "duplicate-issues-one-batch" || variant == "duplicate-issues-next-batch" {
					installed = cloneBootstrap(data)
					oldBase = newRecordBase()
					loaded = cloneBootstrap(data)
				}
				stamp := copyStamp{workspaceID: data.Workspace.ID, revision: -1}
				source, err := captureMetadataLogicalSource(ctx, installed, stamp)
				if err != nil {
					t.Fatal(err)
				}
				base, err := view.newLegacyRecordBase(ctx, source)
				source.Release()
				if err != nil {
					t.Fatal(err)
				}
				if legacyRecordBaseCapture(contextWithLegacyRecordBase(ctx, base)) != base {
					base.Close()
					t.Fatal("capture binding lost its owner")
				}
				scaleP21SW93Remember(t, base, tx, workspace)
				refreshDisplayReferences(&loaded)
				refreshIssueReferences(&loaded)
				if err := oldBase.fingerprintLoaded(loaded); err != nil {
					base.Close()
					t.Fatal(err)
				}
				prepared, err := captureMetadataLogicalSource(ctx, loaded, stamp)
				if err != nil {
					base.Close()
					t.Fatal(err)
				}
				if err := base.FingerprintLoaded(ctx, prepared); err != nil {
					prepared.Release()
					base.Close()
					t.Fatal(err)
				}
				target := cloneBootstrap(loaded)
				if variant != "unchanged" {
					target.Comments[data.Issues[0].ID][0].Body = "Callback body"
					target.Comments[data.Issues[0].ID][0].Version++
				}
				if variant == "comment-delete" {
					target.Comments[data.Issues[0].ID] = target.Comments[data.Issues[0].ID][1:]
				}
				if variant == "new-activity-notification-delivery" {
					activity := target.Activities[data.Issues[0].ID][0]
					activity.ID += "-new"
					target.Activities[data.Issues[0].ID] = append(target.Activities[data.Issues[0].ID], activity)
					notification := target.Notifications[0]
					notification.ID += "-new"
					target.Notifications = append(target.Notifications, notification)
					delivery := target.NotificationDeliveries[0]
					delivery.ID += "-new"
					target.NotificationDeliveries = append(target.NotificationDeliveries, delivery)
				}
				if variant == "state-type-index-refresh" {
					for i := range target.States {
						if target.States[i].ID == target.Issues[0].State.ID {
							target.States[i].Type = "completed"
						}
					}
					refreshIssueReferences(&target)
				}
				if variant == "duplicate-content-id" {
					comment := target.Comments[data.Issues[0].ID][0]
					comment.Body = "Second occurrence"
					target.Comments[data.Issues[0].ID] = append(target.Comments[data.Issues[0].ID], comment)
				}
				if strings.HasPrefix(variant, "duplicate-issues-") {
					if variant == "duplicate-issues-one-batch" {
						target.Issues[0].Title = "First changed occurrence"
						duplicate := target.Issues[0]
						duplicate.Title = "Second changed occurrence"
						target.Issues = append(target.Issues, duplicate)
					} else {
						for len(target.Issues) < 63 {
							filler := loaded.Issues[1]
							filler.ID = fmt.Sprintf("%sfiller-%04d", prefix, len(target.Issues))
							filler.Identifier = filler.ID
							filler.Number = len(target.Issues) + 1
							target.Issues = append(target.Issues, filler)
						}
						first := loaded.Issues[0]
						first.Title = "Changed occurrence before boundary"
						second := loaded.Issues[0]
						second.Title = "Changed occurrence after boundary"
						target.Issues = append(target.Issues, first, second)
					}
				}
				scaleP21SW93Peer(t, tx, workspace, variant, loaded)
				candidate, err := captureMetadataLogicalSource(ctx, target, stamp)
				if err != nil {
					prepared.Release()
					base.Close()
					t.Fatal(err)
				}
				// PostgreSQL aborts the transaction on any failed statement (and
				// rejects repeated UPSERT keys in one statement).
				duplicateBatch := variant == "duplicate-issues-one-batch" || f.dialect == "postgres"
				if duplicateBatch {
					if _, err := tx.ExecContext(ctx, `SAVEPOINT sw93_duplicate_batch`); err != nil {
						t.Fatal(err)
					}
				}
				if bounded {
					err = base.Persist(ctx, tx, workspace, candidate)
				} else {
					err = scaleP21SW93Literal(ctx, f, tx, workspace, target, oldBase)
				}
				// PostgreSQL rejects repeated UPSERT keys in one statement.
				// Restore SQL after that expected error so the oracle can inspect
				// both transactions without reading an aborted transaction.
				if duplicateBatch && err != nil && !errors.Is(err, ErrConcurrentWrite) {
					if _, rollbackErr := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT sw93_duplicate_batch`); rollbackErr != nil {
						t.Fatal(rollbackErr)
					}
				}
				rows := scaleP21SW93Rows(t, tx, workspace)
				counts := scaleP21SW93Counts(tx)
				if !bounded {
					wantErr, wantRows, wantCounts = err, rows, counts
					wantSignals = append([]notificationSignal(nil), tx.signals...)
				} else {
					if reflect.TypeOf(err) != reflect.TypeOf(wantErr) || err != nil && err.Error() != wantErr.Error() {
						t.Fatalf("record persistence errors differ: old=%T %v bounded=%T %v", wantErr, wantErr, err, err)
					}
					if !reflect.DeepEqual(rows, wantRows) {
						t.Fatal("canonical content/issue/index/order rows differ")
					}
					if !reflect.DeepEqual(counts, wantCounts) {
						t.Fatal("queued issue count/stat effects differ", counts, wantCounts)
					}
					if !reflect.DeepEqual(tx.signals, wantSignals) {
						t.Fatal("queued notification signals differ", tx.signals, wantSignals)
					}
					unchanged, readErr := prepared.Select(ctx, scaleP21SW72FullScope(t, prepared), 64<<20)
					if readErr != nil || !reflect.DeepEqual(unchanged, loaded) {
						t.Fatal("record persistence changed its prepared source", readErr)
					}
				}
				candidate.Release()
				prepared.Release()
				scaleP21SW70Usable(t, tx)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				base.Close()
				base.Close()
				if view.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("record base leaked work")
				}
			}
		})
	}
}

func TestScaleP21SW93LegacyRecordPreviousContainerWork(t *testing.T) {
	view := &SQLiteStore{metadataResidency: newMetadataResidency()}
	source, err := captureMetadataLogicalSource(t.Context(), domain.Bootstrap{}, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	raw := []byte(`{"unknown":[` + strings.Repeat(`{},`, 20000) + `{}]}`)
	required, err := metadataAggregatePayloadJSONWork(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if required <= uint64(len(raw))*4 {
		t.Fatal("fixture did not distinguish raw bytes from JSON containers")
	}
	base := &legacyRecordBase{store: view}
	for _, limited := range []bool{true, false} {
		work := &ResourceRead{store: view}
		if err := work.reserve(t.Context(), uint64(len(raw))*4); err != nil {
			t.Fatal(err)
		}
		limit := view.metadataResidency.work.limit
		if limited {
			view.metadataResidency.work.limit = int(uint64(len(raw))*4 + required - 1)
		}
		refs, err := base.recordReferences(t.Context(), source, work, raw, raw)
		view.metadataResidency.work.limit = limit
		if limited {
			if !errors.Is(err, ErrMetadataWorkBudget) {
				t.Fatal("container-heavy previous JSON bypassed admission", err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			equivalent := append(append([]byte(nil), raw...), '\n')
			if !refs.equalDisplayData(raw, equivalent) {
				t.Fatal("admission changed literal canonical display equality")
			}
			if view.MetadataWorkUsedForTesting() < int(uint64(len(raw))*4+required*2) {
				t.Fatal("JSON containers were not charged through the comparison")
			}
		}
		work.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("previous JSON work leaked")
		}
	}
}

func TestScaleP21SW93LegacyRecordNotificationSignalWork(t *testing.T) {
	f, view := scaleP21SW78Fixture(t)
	data := scaleP21SW93Data(f, idgen.New("sw93_signal_"), 1)
	tx, ctx := scaleP21SW93Begin(t, f, data)
	base := &legacyRecordBase{store: view, owner: &ResourceRead{store: view}}
	defer base.Close()
	start := len(tx.signals)
	for i := range 32 {
		notification := data.Notifications[0]
		notification.ID = idgen.New("sw93_signal_")
		notification.RecipientID = data.Viewer.ID
		if err := base.writeContent(ctx, tx, data.Workspace.URLKey, "notification", notification.IssueID, reflect.ValueOf(notification)); err != nil {
			t.Fatal(i, err)
		}
	}
	if len(tx.signals) != start+32 {
		t.Fatal("original signal occurrences changed", len(tx.signals), start)
	}
	if view.MetadataWorkUsedForTesting() < 32*1024 {
		t.Fatal("pending transaction signals were released before commit")
	}
	before := view.MetadataWorkUsedForTesting()
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = before + 1
	notification := data.Notifications[0]
	notification.ID = idgen.New("sw93_signal_refused_")
	err := base.writeContent(ctx, tx, data.Workspace.URLKey, "notification", notification.IssueID, reflect.ValueOf(notification))
	view.metadataResidency.work.limit = limit
	if !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("signal admission refusal lost", err)
	}
	if len(tx.signals) != start+32 || view.MetadataWorkUsedForTesting() != before {
		t.Fatal("signal refusal changed pending state")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	base.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("signal work survived rollback/release")
	}
}

func TestScaleP21SW93LegacyRecordTenfoldPopulationWork(t *testing.T) {
	f, view := scaleP21SW78Fixture(t)
	prefix := idgen.New("sw93_population_")
	peaks := map[bool]int{}
	for _, supplied := range []bool{false, true} {
		for _, count := range []int{64, 640} {
			t.Run(fmt.Sprintf("supplied-%t/records-%d", supplied, count), func(t *testing.T) {
				data := scaleP21SW93Data(f, prefix, 1)
				for len(data.Issues) < count {
					issue := data.Issues[1]
					issue.ID = fmt.Sprintf("%sunchanged-%04d", prefix, len(data.Issues))
					issue.Identifier = issue.ID
					issue.Number = len(data.Issues) + 1
					data.Issues = append(data.Issues, issue)
				}
				var want map[string][][]any
				var wantCounts map[string]map[issueStatsKey]int64
				var observed atomic.Int64
				for _, bounded := range []bool{false, true} {
					tx, ctx := scaleP21SW93Begin(t, f, data)
					oldBase := newRecordBase()
					loaded := cloneBootstrap(collectionMetadata(data))
					installed := collectionMetadata(data)
					var err error
					if supplied {
						loaded, installed = cloneBootstrap(data), cloneBootstrap(data)
					} else {
						loaded.Issues, err = f.readIssueRecordsWithBase(ctx, data.Workspace.URLKey, oldBase)
						if err != nil {
							t.Fatal(err)
						}
						if err := f.hydrateContentRecordsWithBase(ctx, data.Workspace.URLKey, &loaded, oldBase); err != nil {
							t.Fatal(err)
						}
					}
					refreshDisplayReferences(&loaded)
					refreshIssueReferences(&loaded)
					if err := oldBase.fingerprintLoaded(loaded); err != nil {
						t.Fatal(err)
					}
					stamp := copyStamp{workspaceID: data.Workspace.ID, revision: -1}
					source, err := captureMetadataLogicalSource(ctx, installed, stamp)
					if err != nil {
						t.Fatal(err)
					}
					if bounded {
						ctx = scaleP21HotWebhookContext{Context: ctx, check: func() {
							budget := view.metadataResidency.work
							// Err also runs in database/sql background goroutines
							// and while admission already holds this mutex.
							if !budget.mu.TryLock() {
								return
							}
							current := int64(budget.used)
							budget.mu.Unlock()
							for previous := observed.Load(); current > previous; previous = observed.Load() {
								if observed.CompareAndSwap(previous, current) {
									break
								}
							}
						}}
					}
					base, err := view.newLegacyRecordBase(ctx, source)
					source.Release()
					if err != nil {
						t.Fatal(err)
					}
					scaleP21SW93Remember(t, base, tx, data.Workspace.URLKey)
					prepared, err := captureMetadataLogicalSource(ctx, loaded, stamp)
					if err != nil {
						base.Close()
						t.Fatal(err)
					}
					if err := base.FingerprintLoaded(ctx, prepared); err != nil {
						prepared.Release()
						base.Close()
						t.Fatal(err)
					}
					if bounded {
						err = base.Persist(ctx, tx, data.Workspace.URLKey, prepared)
					} else {
						err = scaleP21SW93Literal(ctx, f, tx, data.Workspace.URLKey, loaded, oldBase)
					}
					if err != nil {
						prepared.Release()
						base.Close()
						t.Fatal(err)
					}
					rows := scaleP21SW93Rows(t, tx, data.Workspace.URLKey)
					if bounded {
						if !reflect.DeepEqual(rows, want) || !reflect.DeepEqual(scaleP21SW93Counts(tx), wantCounts) {
							t.Fatal("tenfold population changed literal canonical rows/counts")
						}
					} else {
						want, wantCounts = rows, scaleP21SW93Counts(tx)
					}
					prepared.Release()
					if err := tx.Rollback(); err != nil {
						base.Close()
						t.Fatal(err)
					}
					base.Close()
					if view.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("population proof leaked work")
					}
				}
				peak := int(observed.Load())
				if peak == 0 {
					t.Fatal("population proof observed no admission")
				}
				if count == 64 {
					peaks[supplied] = peak
				} else if peak > peaks[supplied]+(1<<20) {
					t.Fatal("tenfold unchanged population exceeded fixed-row/batch work bound", peaks[supplied], peak)
				}
				t.Logf("supplied=%t records=%d peak_work=%d baseline_64=%d", supplied, count, peak, peaks[supplied])
			})
		}
	}
}

func TestScaleP21SW93LegacyRecordPreparedLockOrder(t *testing.T) {
	f, view := scaleP21SW78Fixture(t)
	data := scaleP21SW93Data(f, idgen.New("sw93_lock_order_"), 1)
	tx, ctx := scaleP21SW93Begin(t, f, data)
	source, err := captureMetadataLogicalSource(ctx, collectionMetadata(data), copyStamp{workspaceID: data.Workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	base, err := view.newLegacyRecordBase(ctx, source)
	source.Release()
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	scaleP21SW93Remember(t, base, tx, data.Workspace.URLKey)
	target, err := captureMetadataLogicalSource(ctx, data, copyStamp{workspaceID: data.Workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Release()
	if err := base.FingerprintLoaded(ctx, target); err != nil {
		t.Fatal(err)
	}
	var queries []string
	ctx = WithSQLStatementObserver(ctx, func(query string) { queries = append(queries, query) })
	if err := base.LockForPersistence(ctx, tx, data.Workspace.URLKey, target); err != nil {
		t.Fatal(err)
	}
	lockCount := 0
	for _, query := range queries {
		if strings.Contains(query, "FOR UPDATE") {
			lockCount++
		}
	}
	if f.dialect != "sqlite" && lockCount != 3 {
		t.Fatal("full replacement locks were not acquired before import boundary", queries)
	}
	boundary := len(queries)
	if err := base.LockForPersistence(ctx, tx, data.Workspace.URLKey, target); err != nil || len(queries) != boundary {
		t.Fatal("prepared locks were reacquired", err, queries)
	}
	other, err := captureMetadataLogicalSource(ctx, data, copyStamp{workspaceID: data.Workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release()
	for _, item := range []struct {
		tx        *sqlTx
		workspace string
		target    *metadataLogicalSource
	}{
		{&sqlTx{database: f.db, dialect: f.dialect}, data.Workspace.URLKey, target},
		{tx, data.Workspace.URLKey + "-other", target},
		{tx, data.Workspace.URLKey, other},
	} {
		if err := base.Persist(ctx, item.tx, item.workspace, item.target); !errors.Is(err, ErrMetadataRecordQuery) || len(queries) != boundary {
			t.Fatal("prepared locks accepted a different transaction/namespace/image", err)
		}
	}
	if err := base.Persist(ctx, tx, data.Workspace.URLKey, target); err != nil {
		t.Fatal(err)
	}
	for _, query := range queries[boundary:] {
		if strings.Contains(query, "FOR UPDATE") {
			t.Fatal("persistence repeated locks after import boundary", query)
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	base.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("prepared lock base leaked work")
	}
}

func TestScaleP21SW93LegacyRecordBaseCancellation(t *testing.T) {
	f, view := scaleP21SW78Fixture(t)
	data := scaleP21SW93Data(f, idgen.New("sw93_cancel_"), 2000)
	installed, err := captureMetadataLogicalSource(t.Context(), collectionMetadata(data), copyStamp{workspaceID: data.Workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer installed.Release()
	for _, variant := range []string{"canceled-constructor", "canceled-remember", "canceled-fingerprint", "canceled-persist", "work-refusal"} {
		t.Run(variant, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if variant == "canceled-constructor" {
				cancel()
				if base, err := view.newLegacyRecordBase(ctx, installed); base != nil || !errors.Is(err, context.Canceled) {
					if base != nil {
						base.Close()
					}
					t.Fatal("constructor ignored cancellation", err)
				}
				return
			}
			base, err := view.newLegacyRecordBase(ctx, installed)
			if err != nil {
				t.Fatal(err)
			}
			defer base.Close()
			tx, readCtx := scaleP21SW93Begin(t, f, data)
			source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: data.Workspace.ID})
			if err != nil {
				t.Fatal(err)
			}
			defer source.Release()
			before := scaleP21SW93Rows(t, tx, data.Workspace.URLKey)
			want := error(context.Canceled)
			limit := view.metadataResidency.work.limit
			switch variant {
			case "canceled-remember":
				cancel()
				err = base.RememberStored(ctx, "issue", "", data.Issues[0].ID, []byte("{}"))
			case "canceled-fingerprint":
				cancel()
				err = base.FingerprintLoaded(ctx, source)
			case "canceled-persist":
				cancel()
				err = base.Persist(ctx, tx, data.Workspace.URLKey, source)
			case "work-refusal":
				view.metadataResidency.work.limit = 1
				want = ErrMetadataWorkBudget
				err = base.Persist(readCtx, tx, data.Workspace.URLKey, source)
				view.metadataResidency.work.limit = limit
			}
			if !errors.Is(err, want) {
				t.Fatal("record base failure changed", variant, err, want)
			}
			if got := scaleP21SW93Rows(t, tx, data.Workspace.URLKey); !reflect.DeepEqual(got, before) {
				t.Fatal("record preflight failure wrote SQL")
			}
			base.Close()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("record failure leaked work")
			}
		})
	}
}
