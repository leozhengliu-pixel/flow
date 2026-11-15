package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21WebhookFailureLiteral(ctx context.Context, repo *SQLiteStore, workspace string, failure domain.WebhookFailureEvent, status int) error {
	return repo.MutateWorkspace(ctx, workspace, "webhook.delivery_failed", failure.WebhookID, map[string]any{"executionId": failure.ExecutionID, "httpStatus": status}, func(data *domain.Bootstrap) error {
		if !slices.ContainsFunc(data.Webhooks, func(hook domain.Webhook) bool { return hook.ID == failure.WebhookID }) {
			return nil
		}
		data.WebhookFailureEvents = append(data.WebhookFailureEvents, failure)
		count := 0
		kept := make([]domain.WebhookFailureEvent, 0, len(data.WebhookFailureEvents))
		for index := len(data.WebhookFailureEvents) - 1; index >= 0; index-- {
			item := data.WebhookFailureEvents[index]
			if item.WebhookID == failure.WebhookID {
				if count >= 50 {
					continue
				}
				count++
			}
			kept = append(kept, item)
		}
		slices.Reverse(kept)
		data.WebhookFailureEvents = kept
		return nil
	})
}

func scaleP21WebhookFailureRows(t *testing.T, repo *SQLiteStore, workspace, field string) [][]any {
	t.Helper()
	rows, err := repo.db.QueryContext(t.Context(), `SELECT record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field=? ORDER BY collection_order,record_key`, workspace, field)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := [][]any{}
	for rows.Next() {
		var id string
		var order int
		var raw []byte
		if err := rows.Scan(&id, &order, &raw); err != nil {
			t.Fatal(err)
		}
		result = append(result, []any{id, order, append([]byte(nil), raw...)})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func scaleP21WebhookFailureSource(t *testing.T, repo *SQLiteStore, workspace string) []domain.WebhookFailureEvent {
	t.Helper()
	source, err := repo.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	node := webhookFailureWriteField(source, "webhookFailureEvents")
	value, err := node.WireValue(t.Context(), int(node.cost))
	if err != nil {
		t.Fatal(err)
	}
	return value.Interface().([]domain.WebhookFailureEvent)
}

func TestScaleP21WebhookFailureWriteDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("webhook-failure-")
	target, other := prefix+"target", prefix+"other"
	at := time.Date(2026, 11, 14, 15, 16, 17, 18, time.FixedZone("failure-native", 3661))
	failure := domain.WebhookFailureEvent{ID: prefix + "appended", WebhookID: target, ExecutionID: "execution", URL: "https://example.test/hook", ResponseOrError: "failed", CreatedAt: at}
	type effects struct {
		Failures, Documents, Webhooks [][]any
		Native                        []domain.WebhookFailureEvent
		Payload, Previous, Snapshot   json.RawMessage
		Realtime                      json.RawMessage
		Count                         int
	}
	for _, reload := range []bool{false, true} {
		for _, missing := range []bool{false, true} {
			var want effects
			for _, bounded := range []bool{false, true} {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
				if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "webhooks", "webhookFailureEvents", "documents"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
					data.Webhooks = []domain.Webhook{{ID: other, Enabled: true, URL: "https://example.test/other", Name: strings.Repeat("unrelated hook ", 2000)}}
					if !missing {
						data.Webhooks = append(data.Webhooks, domain.Webhook{ID: target, Enabled: true, URL: failure.URL})
					}
					// An earlier nested object wins event aggregate resolution.
					data.Documents = []domain.Document{{ID: prefix + "document", Content: strings.Repeat("unrelated document ", 2000), ContentData: map[string]any{"object": map[string]any{"id": target, "name": "Earlier nested object"}}}}
					data.WebhookFailureEvents = nil
					for index := range 160 {
						id := target
						if index%3 == 1 {
							id = other
						}
						data.WebhookFailureEvents = append(data.WebhookFailureEvents, domain.WebhookFailureEvent{
							ID: fmt.Sprintf("%s%03d", prefix, index), WebhookID: id, ExecutionID: fmt.Sprint(index),
							ResponseOrError: strings.Repeat("old body ", 100), CreatedAt: at.Add(time.Duration(160-index) * time.Hour),
						})
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if reload {
					if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
				}
				source := f.SQLiteStore
				if bounded {
					var err error
					source, err = f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
					if err != nil {
						t.Fatal(err)
					}
					defer source.releaseMetadataLogicalSources()
				}
				var got effects
				source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
					got.Count++
					got.Payload, got.Previous, got.Snapshot = event.Payload, event.PreviousValues, event.Snapshot
				})
				source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { got.Realtime = event.Payload })
				var err error
				statements := 0
				observed := WithSQLStatementObserver(t.Context(), func(string) { statements++ })
				if bounded {
					err = source.RecordWebhookDeliveryFailure(observed, workspace, failure, 503)
				} else {
					err = scaleP21WebhookFailureLiteral(observed, source, workspace, failure, 503)
				}
				source.SetWebhookSink(nil)
				source.SetRealtimeSink(nil)
				if err != nil {
					t.Fatal("failure writer", reload, missing, bounded, err)
				}
				t.Logf("point reload=%v missing=%v bounded=%v SQL=%d", reload, missing, bounded, statements)
				got.Failures = scaleP21WebhookFailureRows(t, source, workspace, "webhookFailureEvents")
				got.Documents = scaleP21WebhookFailureRows(t, source, workspace, "documents")
				got.Webhooks = scaleP21WebhookFailureRows(t, source, workspace, "webhooks")
				if bounded {
					got.Native = scaleP21WebhookFailureSource(t, source, workspace)
				} else {
					data, _ := source.WorkspaceMetadata(workspace)
					got.Native = data.WebhookFailureEvents
				}
				if got.Count != 1 {
					t.Fatal("missing-webhook event behavior changed", got.Count)
				}
				if !missing {
					if count := len(slices.DeleteFunc(slices.Clone(got.Native), func(item domain.WebhookFailureEvent) bool { return item.WebhookID != target })); count != 50 {
						t.Fatal("retention did not keep exactly 50", count, "reload", reload, "missing", missing, "bounded", bounded)
					}
					if got.Native[len(got.Native)-1].ID != failure.ID {
						t.Fatal("append order changed")
					}
				}
				if !bounded {
					want = got
				} else {
					a, _ := json.Marshal(got)
					b, _ := json.Marshal(want)
					if !bytes.Equal(a, b) {
						t.Fatalf("differential changed reload=%v missing=%v: failures=%v documents=%v hooks=%v native=%v payload=%v previous=%v snapshot=%v realtime=%v",
							reload, missing, reflect.DeepEqual(got.Failures, want.Failures), reflect.DeepEqual(got.Documents, want.Documents),
							reflect.DeepEqual(got.Webhooks, want.Webhooks), reflect.DeepEqual(got.Native, want.Native),
							bytes.Equal(got.Payload, want.Payload), bytes.Equal(got.Previous, want.Previous), bytes.Equal(got.Snapshot, want.Snapshot), bytes.Equal(got.Realtime, want.Realtime))
					}
					if source.WorkspaceMetadataDecodes() != 0 || source.metadataResidency.work.used != 0 {
						t.Fatal("complete metadata decode or leaked work")
					}
				}
			}
		}
	}
}

func TestScaleP21WebhookFailureWriteInlineDuplicates(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	at := time.Date(2026, 11, 14, 1, 2, 3, 0, time.UTC)
	failure := domain.WebhookFailureEvent{ID: "new", WebhookID: "hook", CreatedAt: at}
	for _, retained := range []bool{false, true} {
		for _, reload := range []bool{false, true} {
			var wantNative []domain.WebhookFailureEvent
			var wantRows [][]any
			var wantPayload, wantSnapshot json.RawMessage
			for _, bounded := range []bool{false, true} {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
				if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "webhooks", "webhookFailureEvents"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
					data.Webhooks = []domain.Webhook{{ID: "hook", Enabled: true, URL: "https://example.test"}}
					data.WebhookFailureEvents = nil
					for index := range 52 {
						id := fmt.Sprintf("f%02d", index)
						first := 0
						if retained {
							first = 49
						}
						if index == first || index == 51 {
							id = "duplicate"
						}
						data.WebhookFailureEvents = append(data.WebhookFailureEvents, domain.WebhookFailureEvent{ID: id, WebhookID: "hook", ExecutionID: fmt.Sprint(index), CreatedAt: at})
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if reload {
					if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
				}
				source := f.SQLiteStore
				if bounded {
					var err error
					source, err = f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
					if err != nil {
						t.Fatal(err)
					}
					defer source.releaseMetadataLogicalSources()
				}
				if bounded {
					handle, err := source.retainMetadataLogicalSource(t.Context(), workspace)
					if err != nil {
						t.Fatal(err)
					}
					var rootBytes int
					if err := source.db.QueryRowContext(t.Context(), `SELECT LENGTH(data) FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&rootBytes); err != nil {
						t.Fatal(err)
					}
					requested, err := handle.Transform(t.Context(), func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
						if name == "webhookFailureEvents" {
							return webhookFailureWriteCollection{base: node, webhookID: failure.WebhookID, drop: 3, failure: failure}, true, nil
						}
						return metadataAggregateStoredNode{node}, false, nil
					})
					if err != nil {
						t.Fatal(err)
					}
					beforeCost, afterCost := webhookFailureWriteField(handle, "webhookFailureEvents").cost, webhookFailureWriteField(requested, "webhookFailureEvents").cost
					required := (beforeCost+afterCost)*8 + uint64(rootBytes)*12 + 64<<10
					t.Logf("inline input retained=%v reload=%v beforeCost=%d afterCost=%d rootBytes=%d required=%d", retained, reload, beforeCost, afterCost, rootBytes, required)
					requested.Release()
					handle.Release()
				}
				var payload, snapshot json.RawMessage
				source.SetWebhookSink(func(_ string, event domain.DomainEvent) { payload, snapshot = event.Payload, event.Snapshot })
				var err error
				statements := 0
				observed := WithSQLStatementObserver(t.Context(), func(string) { statements++ })
				if bounded {
					err = source.RecordWebhookDeliveryFailure(observed, workspace, failure, 500)
				} else {
					err = scaleP21WebhookFailureLiteral(observed, source, workspace, failure, 500)
				}
				source.SetWebhookSink(nil)
				if err != nil {
					t.Fatal("inline duplicates", retained, reload, bounded, err)
				}
				t.Logf("inline retained=%v reload=%v bounded=%v SQL=%d", retained, reload, bounded, statements)
				var native []domain.WebhookFailureEvent
				if bounded {
					native = scaleP21WebhookFailureSource(t, source, workspace)
				} else {
					data, _ := source.WorkspaceMetadata(workspace)
					native = data.WebhookFailureEvents
				}
				rows := scaleP21WebhookFailureRows(t, source, workspace, "webhookFailureEvents")
				if !bounded {
					wantNative, wantRows, wantPayload, wantSnapshot = native, rows, payload, snapshot
					expected := 49
					if retained {
						expected = 50
					}
					if len(native) != expected {
						t.Fatal("literal duplicate fixture changed", retained, reload, len(native))
					}
				} else if !reflect.DeepEqual(native, wantNative) || !reflect.DeepEqual(rows, wantRows) || !bytes.Equal(payload, wantPayload) || !bytes.Equal(snapshot, wantSnapshot) {
					t.Fatal("inline duplicate differential changed", retained, reload, len(native), len(wantNative))
				}
				if bounded && (source.metadataResidency.work.used != 0 || source.WorkspaceMetadataDecodes() != 0) {
					t.Fatal("inline duplicate lease or full decode leaked")
				}
			}
		}
	}
}

func TestScaleP21WebhookFailureWriteInlineBoundaryAndCancel(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	at := time.Date(2026, 11, 14, 1, 2, 3, 0, time.UTC)
	for _, wide := range []bool{false, true} {
		if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "webhooks", "webhookFailureEvents"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
			data.Webhooks = []domain.Webhook{{ID: "hook"}}
			data.WebhookFailureEvents = nil
			for index := range 52 {
				id := fmt.Sprintf("f%02d", index)
				duplicate := "duplicate"
				if wide {
					id = fmt.Sprintf("w%02d", index)
					duplicate = "wide-duplicate"
				}
				if index == 0 || index == 51 {
					id = duplicate
				}
				text := ""
				if wide {
					text = strings.Repeat("x", 3000)
				}
				data.WebhookFailureEvents = append(data.WebhookFailureEvents, domain.WebhookFailureEvent{ID: id, WebhookID: "hook", CreatedAt: at, ResponseOrError: text})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		beforeSource, err := view.retainMetadataLogicalSource(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		var before []byte
		if err := view.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&before); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		if !wide {
			ctx = WithSQLStatementObserver(ctx, func(query string) {
				if strings.HasPrefix(query, "UPDATE workspace_states") {
					cancel()
				}
			})
		}
		limit := view.metadataResidency.work.limit
		if wide {
			view.metadataResidency.work.limit = 3 << 20
		}
		err = view.RecordWebhookDeliveryFailure(ctx, workspace, domain.WebhookFailureEvent{ID: "new", WebhookID: "hook", CreatedAt: at}, 500)
		cancel()
		view.metadataResidency.work.limit = limit
		if wide && !errors.Is(err, ErrMetadataWorkBudget) || !wide && !errors.Is(err, context.Canceled) {
			t.Fatal("inline failure boundary did not reject before publication", wide, err)
		}
		var after []byte
		if err := view.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&after); err != nil {
			t.Fatal(err)
		}
		current, err := view.retainMetadataLogicalSource(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) || current != beforeSource || view.metadataResidency.work.used != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("inline failure boundary changed SQL/source or leaked ownership", wide)
		}
		current.Release()
		if !wide {
			oversized := append(slices.Clone(before), bytes.Repeat([]byte(" "), MetadataRecordPageMaxBytes)...)
			if _, err := view.db.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, oversized, workspace); err != nil {
				t.Fatal(err)
			}
			err := view.RecordWebhookDeliveryFailure(t.Context(), workspace, domain.WebhookFailureEvent{ID: "new", WebhookID: "hook", CreatedAt: at}, 500)
			if !errors.Is(err, ErrMetadataPageBudget) {
				t.Fatal("root above 256KiB was not explicitly rejected", err)
			}
			if _, err := view.db.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, before, workspace); err != nil {
				t.Fatal(err)
			}
			current, err := view.retainMetadataLogicalSource(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			if current != beforeSource || view.metadataResidency.work.used != 0 {
				t.Fatal("oversized root published source or leaked work")
			}
			current.Release()
		}
		beforeSource.Release()
		view.releaseMetadataLogicalSources()
	}
}

func TestScaleP21WebhookFailureWriteBodyBound(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, target := f.data.Workspace.URLKey, idgen.New("webhook-failure-body-")
	at := time.Date(2026, 11, 14, 4, 5, 6, 7, time.UTC)
	wantPeak := map[bool]int{}
	for _, scale := range []int{1, 10} {
		for _, reload := range []bool{false, true} {
			if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "webhooks", "webhookFailureEvents", "documents"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
				data.Webhooks = []domain.Webhook{{ID: target, Enabled: true, URL: "https://example.test"}}
				data.Documents = []domain.Document{{ID: target + "-document", Content: strings.Repeat("unrelated document ", scale*20000)}}
				data.WebhookFailureEvents = nil
				for index := range 60 {
					webhook := target
					if index%11 == 0 {
						webhook += "-other"
					}
					data.WebhookFailureEvents = append(data.WebhookFailureEvents, domain.WebhookFailureEvent{
						ID: fmt.Sprintf("%s-%03d", target, index), WebhookID: webhook, CreatedAt: at,
						ResponseOrError: strings.Repeat("failure body ", scale*2000),
					})
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if reload {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			documents := scaleP21WebhookFailureRows(t, view, workspace, "documents")
			peak := 0
			ctx := WithSQLStatementObserver(t.Context(), func(string) {
				view.metadataResidency.work.mu.Lock()
				peak = max(peak, view.metadataResidency.work.used)
				view.metadataResidency.work.mu.Unlock()
			})
			if err := view.RecordWebhookDeliveryFailure(ctx, workspace, domain.WebhookFailureEvent{ID: target + "-new", WebhookID: target, CreatedAt: at}, 500); err != nil {
				t.Fatal(err)
			}
			if scale == 1 {
				wantPeak[reload] = peak
			} else if peak != wantPeak[reload] {
				t.Fatal("body growth increased admitted writer work", reload, peak, wantPeak[reload])
			}
			if !reflect.DeepEqual(documents, scaleP21WebhookFailureRows(t, view, workspace, "documents")) || view.metadataResidency.work.used != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("unrelated body changed, complete decode, or leaked work")
			}
			t.Logf("scale=%d reload=%v admittedPeak=%d", scale, reload, peak)
			view.releaseMetadataLogicalSources()
		}
	}
}

func TestScaleP21WebhookFailureWriteRollbackAndConcurrentRetention(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, target := f.data.Workspace.URLKey, idgen.New("webhook-failure-atomic-")
	now := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "webhooks", "webhookFailureEvents"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
		data.Webhooks = []domain.Webhook{{ID: target, Enabled: true, URL: "https://example.test"}}
		for index := range 100 {
			data.WebhookFailureEvents = append(data.WebhookFailureEvents, domain.WebhookFailureEvent{ID: fmt.Sprintf("%s-%03d", target, index), WebhookID: target, CreatedAt: now})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	failure := domain.WebhookFailureEvent{ID: target + "-append", WebhookID: target, CreatedAt: now}
	before := scaleP21WebhookFailureRows(t, view, workspace, "webhookFailureEvents")
	oldSource, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer oldSource.Release()
	stamp := oldSource.Stamp()
	for _, phase := range []string{"canceled", "budget", "delete"} {
		ctx, cancel := context.WithCancel(t.Context())
		if phase == "canceled" {
			cancel()
		}
		limit := view.metadataResidency.work.limit
		if phase == "budget" {
			view.metadataResidency.work.limit = 1
		}
		if phase == "delete" {
			ctx = WithSQLStatementObserver(ctx, func(query string) {
				if strings.HasPrefix(query, "DELETE FROM workspace_metadata_records") {
					cancel()
				}
			})
		}
		err := view.RecordWebhookDeliveryFailure(ctx, workspace, failure, 500)
		cancel()
		view.metadataResidency.work.limit = limit
		if phase == "budget" && !errors.Is(err, ErrMetadataWorkBudget) || phase != "budget" && !errors.Is(err, context.Canceled) {
			t.Fatal("failure injection did not fail", phase, err)
		}
		if !reflect.DeepEqual(before, scaleP21WebhookFailureRows(t, view, workspace, "webhookFailureEvents")) || view.metadataResidency.work.used != 0 {
			t.Fatal("failed transaction changed rows or leaked work", phase)
		}
		current, err := view.retainMetadataLogicalSource(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		same := current == oldSource && current.Stamp() == stamp
		current.Release()
		if !same {
			t.Fatal("rollback published a new logical source", phase)
		}
	}
	var group sync.WaitGroup
	results := make(chan error, 12)
	for index := range 12 {
		group.Add(1)
		go func() {
			defer group.Done()
			item := failure
			item.ID = fmt.Sprintf("%s-concurrent-%d", target, index)
			results <- view.RecordWebhookDeliveryFailure(t.Context(), workspace, item, 500)
		}()
	}
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	rows := scaleP21WebhookFailureRows(t, view, workspace, "webhookFailureEvents")
	if len(rows) != 50 || len(scaleP21WebhookFailureSource(t, view, workspace)) != 50 || view.metadataResidency.work.used != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("concurrent retention or work lifetime changed", len(rows))
	}
	node := webhookFailureWriteField(oldSource, "webhookFailureEvents")
	old, err := node.Value(t.Context(), int(node.cost))
	if err != nil || old.Len() != 100 || oldSource.Stamp() != stamp {
		t.Fatal("retained precommit generation changed", err)
	}
}
