package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21DisableMutation(target, notification string, now time.Time, observed func(*domain.Bootstrap)) func(*domain.Bootstrap) error {
	return func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Webhooks, func(hook domain.Webhook) bool { return hook.ID == target })
		if index < 0 || !data.Webhooks[index].Enabled {
			return ErrNoMutation
		}
		if observed != nil {
			observed(data)
		}
		hook := &data.Webhooks[index]
		hook.Enabled, hook.DisabledReason, hook.DisabledAt, hook.UpdatedAt = false, "deliveryFailures", &now, now
		data.AuditLog = append([]domain.AuditLogEntry{{ID: notification + "-audit", Actor: data.Viewer, Action: "disabled", ResourceType: "webhook", ResourceID: target, CreatedAt: now}}, data.AuditLog...)
		recipient := data.Users[0]
		data.Notifications = append(data.Notifications, domain.Notification{ID: notification, RecipientID: recipient.ID, Actor: recipient, SourceID: target, SourceType: "webhook", Category: "integrations", Type: "integration", CreatedAt: now, UpdatedAt: now})
		data.NotificationDeliveries = append(data.NotificationDeliveries, domain.NotificationDelivery{ID: "delivery_" + notification + "_desktop", NotificationID: notification, RecipientID: recipient.ID, Channel: "desktop", Status: "pending", CreatedAt: now, UpdatedAt: now})
		return nil
	}
}

func scaleP21DisableContent(t *testing.T, repo *SQLiteStore, workspace string) [][]any {
	t.Helper()
	rows, err := repo.db.QueryContext(t.Context(), `SELECT kind,resource_id,id,data FROM workspace_content_records WHERE workspace_key=? ORDER BY kind,resource_id,id`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := [][]any{}
	for rows.Next() {
		var kind, resource, id string
		var raw []byte
		if err := rows.Scan(&kind, &resource, &id, &raw); err != nil {
			t.Fatal(err)
		}
		result = append(result, []any{kind, resource, id, slices.Clone(raw)})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestScaleP21WebhookDisableWriteDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, target := f.data.Workspace.URLKey, idgen.New("wh-disable-")
	restorePhysicalIDs := func(ctx context.Context) error {
		if f.dialect != "sqlite" {
			return nil
		}
		// Keep imported BLOB identities through comparison. On retirement,
		// this id-only UPDATE does not fire the content journal trigger.
		_, err := f.db.ExecContext(ctx, `UPDATE workspace_content_records SET id=CAST(id AS TEXT) WHERE workspace_key=? AND typeof(id)='blob'`, workspace)
		return err
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := restorePhysicalIDs(ctx); err != nil {
			t.Errorf("retire owned historical content identities: %v", err)
		}
	})
	assertPhysicalInputs := func() {
		t.Helper()
		if f.dialect != "sqlite" {
			return
		}
		var blobs int
		if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_content_records WHERE workspace_key=? AND typeof(id)='blob'`, workspace).Scan(&blobs); err != nil || blobs != 3 {
			t.Fatal("historical content inputs lost their physical BLOB identities", blobs, err)
		}
		if recordJournalMode() != "off" {
			var enabled, capturing int
			if err := f.db.QueryRowContext(t.Context(), `SELECT enabled,capturing FROM record_journal_mode WHERE id=1`).Scan(&enabled, &capturing); err != nil || enabled != 1 || capturing != 1 || !f.RecordJournalReady() {
				t.Fatal("historical content fixture disabled inherited journal capture", enabled, capturing, err)
			}
		}
	}
	at := time.Date(2026, 11, 14, 3, 4, 5, 6, time.UTC)
	type effects struct {
		Hooks, Documents                    [][]any
		Content                             [][]any
		Audits                              []domain.AuditLogEntry
		Payload, Before, Snapshot, Realtime json.RawMessage
		Events                              int
	}
	for _, reload := range []bool{false, true} {
		var want effects
		for _, bounded := range []bool{false, true} {
			if err := restorePhysicalIDs(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "webhooks", "documents", "notificationPreferences", "pushSubscriptions"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
				data.Webhooks = []domain.Webhook{{ID: target, Enabled: true, TeamRestriction: "all", Name: "Native\x00name", URL: "https://example.test", CreatorID: f.data.Viewer.ID}}
				data.Documents = []domain.Document{{ID: target + "-document", Content: strings.Repeat("unrelated ", 50000), ContentData: map[string]any{"object": map[string]any{"id": target, "name": "Earlier metadata winner"}}}}
				data.NotificationPreferences = map[string]domain.NotificationPreferences{f.data.Viewer.ID: {Email: domain.NotificationChannelPreferences{Enabled: true, Categories: map[string]bool{}}, EmailFormat: "digest"}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.ExecContext(t.Context(), `DELETE FROM workspace_content_records WHERE workspace_key=?`, workspace); err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.ExecContext(t.Context(), `DELETE FROM audit_events WHERE workspace_id=? AND resource_id=?`, f.data.Workspace.ID, target); err != nil {
				t.Fatal(err)
			}
			tx, err := f.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeContentRecord(t.Context(), tx, workspace, "notification", target, domain.Notification{ID: target + "-old", IssueID: target, RecipientID: f.data.Viewer.ID, Actor: f.data.Viewer, CreatedAt: at}); err != nil {
				t.Fatal(err)
			}
			if err := writeContentRecord(t.Context(), tx, workspace, "delivery", "", domain.NotificationDelivery{ID: target + "-old-delivery", NotificationID: target + "-old", RecipientID: f.data.Viewer.ID, Channel: "desktop", Status: "sent", CreatedAt: at}); err != nil {
				t.Fatal(err)
			}
			if err := writeContentRecord(t.Context(), tx, workspace, "notification", target, domain.Notification{ID: "", IssueID: target, RecipientID: f.data.Viewer.ID, Actor: f.data.Viewer, CreatedAt: at}); err != nil {
				t.Fatal(err)
			}
			if err := writeContentRecord(t.Context(), tx, workspace, "delivery", "", domain.NotificationDelivery{ID: target + "-empty-parent", NotificationID: "", RecipientID: f.data.Viewer.ID, Channel: "email", Status: "digest", CreatedAt: at}); err != nil {
				t.Fatal(err)
			}
			if err := writeContentRecord(t.Context(), tx, workspace, "comment", target, domain.Comment{ID: target + "-comment", User: f.data.Viewer, Body: "Owned comment", BodyData: map[string]any{"nested": map[string]any{"id": target, "name": "Scoped comment winner"}}, CreatedAt: at}); err != nil {
				t.Fatal(err)
			}
			for _, item := range [][2]string{{"comment", target + "-comment"}, {"notification", target + "-old"}, {"delivery", target + "-old-delivery"}} {
				var physical any = item[1] + "-physical"
				if f.dialect == "sqlite" {
					physical = []byte(item[1] + "-physical")
				}
				if _, err := tx.ExecContext(t.Context(), `UPDATE workspace_content_records SET id=? WHERE workspace_key=? AND kind=? AND id=?`, physical, workspace, item[0], item[1]); err != nil {
					t.Fatal(err)
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			assertPhysicalInputs()
			if reload {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			source := f.SQLiteStore
			if bounded {
				source, err = f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
				if err != nil {
					t.Fatal(err)
				}
				defer source.releaseMetadataLogicalSources()
			}
			var got effects
			source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
				got.Events++
				got.Payload, got.Before, got.Snapshot = event.Payload, event.PreviousValues, event.Snapshot
			})
			source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { got.Realtime = event.Payload })
			mutate := scaleP21DisableMutation(target, target+"-new", at, nil)
			ctx := WithMutationScope(t.Context(), MutationScope{Resources: []string{target}})
			statements := 0
			ctx = WithSQLStatementObserver(ctx, func(string) { statements++ })
			if bounded {
				err = source.MutateFailingWebhookDisable(ctx, workspace, target, mutate)
			} else {
				err = source.MutateWorkspace(ctx, workspace, "webhook.auto_disabled", target, map[string]any{"reason": "deliveryFailures"}, mutate)
			}
			source.SetWebhookSink(nil)
			source.SetRealtimeSink(nil)
			if err != nil {
				t.Fatal("disable", reload, bounded, err)
			}
			got.Hooks = scaleP21WebhookFailureRows(t, source, workspace, "webhooks")
			got.Documents = scaleP21WebhookFailureRows(t, source, workspace, "documents")
			got.Content = scaleP21DisableContent(t, source, workspace)
			assertPhysicalInputs()
			got.Audits, err = source.ListAuditEntries(t.Context(), f.data.Workspace.ID, AuditQuery{ResourceID: target})
			if err != nil {
				t.Fatal(err)
			}
			for index := range got.Audits {
				got.Audits[index].Seq = 0
				got.Audits[index].Hash = ""
			}
			t.Logf("reload=%v bounded=%v SQL=%d", reload, bounded, statements)
			if !bounded {
				want = got
			} else {
				for index := range got.Hooks {
					if index >= len(want.Hooks) || got.Hooks[index][0] != target {
						continue
					}
					left, right := got.Hooks[index][2].([]byte), want.Hooks[index][2].([]byte)
					if bytes.Equal(left, right) {
						continue
					}
					var a, b any
					for _, value := range []struct {
						raw []byte
						out *any
					}{{left, &a}, {right, &b}} {
						decoder := json.NewDecoder(bytes.NewReader(value.raw))
						decoder.UseNumber()
						if err := decoder.Decode(value.out); err != nil {
							t.Fatal(err)
						}
					}
					if !reflect.DeepEqual(a, b) {
						t.Fatalf("changed hook values differ:\nbounded=%s\nlegacy=%s", left, right)
					}
					t.Logf("changed hook has equal JSON values with different object key order: bounded=%s legacy=%s", left, right)
					got.Hooks[index][2] = right
				}
				if !reflect.DeepEqual(got, want) {
					a, _ := json.Marshal(got)
					b, _ := json.Marshal(want)
					t.Fatalf("disable differential changed reload=%v equal=%v/%v/%v/%v/%v/%v/%v/%v lengths=%d/%d", reload,
						reflect.DeepEqual(got.Hooks, want.Hooks), reflect.DeepEqual(got.Documents, want.Documents), reflect.DeepEqual(got.Content, want.Content), reflect.DeepEqual(got.Audits, want.Audits),
						bytes.Equal(got.Payload, want.Payload), bytes.Equal(got.Before, want.Before), bytes.Equal(got.Snapshot, want.Snapshot), bytes.Equal(got.Realtime, want.Realtime), len(a), len(b))
				}
			}
			if bounded && (source.metadataResidency.work.used != 0 || source.WorkspaceMetadataDecodes() != 0) {
				t.Fatal("disable leaked work or decoded full metadata")
			}
		}
	}
}

func TestScaleP21WebhookDisableWriteRollbackAndExactlyOnce(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, target := f.data.Workspace.URLKey, idgen.New("wh-disable-atomic-")
	now := time.Date(2026, 11, 14, 3, 4, 5, 0, time.UTC)
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "webhooks"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
		data.Webhooks = []domain.Webhook{{ID: target, Enabled: true, TeamRestriction: "all", CreatorID: f.data.Viewer.ID}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	old, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Release()
	original := scaleP21WebhookFailureRows(t, view, workspace, "webhooks")
	for _, phase := range []string{"canceled", "parser-quota", "content-write"} {
		ctx, cancel := context.WithCancel(t.Context())
		limit := view.metadataResidency.work.limit
		if phase == "canceled" {
			cancel()
		}
		if phase == "parser-quota" {
			view.metadataResidency.work.limit = 4 << 20
		}
		if phase == "content-write" {
			ctx = WithSQLStatementObserver(ctx, func(query string) {
				if strings.HasPrefix(query, "INSERT INTO workspace_content_records") {
					cancel()
				}
			})
		}
		err := view.MutateFailingWebhookDisable(ctx, workspace, target, scaleP21DisableMutation(target, target+"-new", now, nil))
		cancel()
		view.metadataResidency.work.limit = limit
		if phase == "parser-quota" && !errors.Is(err, ErrMetadataWorkBudget) || phase != "parser-quota" && !errors.Is(err, context.Canceled) {
			t.Fatal("failure injection did not reject", phase, err)
		}
		if !reflect.DeepEqual(original, scaleP21WebhookFailureRows(t, view, workspace, "webhooks")) || len(scaleP21DisableContent(t, view, workspace)) != 0 || view.metadataResidency.work.used != 0 {
			t.Fatal("rollback changed webhook/content or leaked work", phase)
		}
		entries, err := view.ListAuditEntries(t.Context(), f.data.Workspace.ID, AuditQuery{ResourceID: target})
		if err != nil || len(entries) != 0 {
			t.Fatal("rollback committed audit", phase, err)
		}
	}
	var group sync.WaitGroup
	result := make(chan error, 8)
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			result <- view.MutateFailingWebhookDisable(t.Context(), workspace, target, scaleP21DisableMutation(target, target+"-new", now, nil))
		}()
	}
	group.Wait()
	close(result)
	for err := range result {
		if err != nil {
			t.Fatal(err)
		}
	}
	content := scaleP21DisableContent(t, view, workspace)
	entries, err := view.ListAuditEntries(t.Context(), f.data.Workspace.ID, AuditQuery{ResourceID: target})
	if err != nil || len(content) != 2 || len(entries) != 1 || view.metadataResidency.work.used != 0 {
		t.Fatal("concurrent disable did not commit exactly once", len(content), len(entries), err)
	}
	hook, found, err := webhookDisableFirst(t.Context(), old, "webhooks", target)
	if err != nil || !found {
		t.Fatal(err)
	}
	value, err := hook.Value(t.Context(), int(hook.cost))
	if err != nil || !value.Interface().(domain.Webhook).Enabled {
		t.Fatal("retained old generation changed", err)
	}
}

func TestScaleP21WebhookDisableWriteInlineNative(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	notificationID := idgen.New("inline-disable-")
	now := time.Date(2026, 11, 14, 3, 4, 5, 6, time.UTC)
	for _, target := range []string{"duplicate-hook", ""} {
		for _, reload := range []bool{false, true} {
			var want []domain.Webhook
			var wantRows [][]any
			var wantPayload, wantBefore, wantSnapshot json.RawMessage
			var wantInput domain.Webhook
			for _, bounded := range []bool{false, true} {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
				restore := ForceFullMutationsForTesting(true)
				err := f.MutateWorkspace(t.Context(), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
					data.Webhooks = []domain.Webhook{
						{ID: target, Name: "First\x00native", Enabled: true, TeamRestriction: "all", CreatorID: f.data.Viewer.ID, CreatedAt: now},
						{ID: target, Name: "Second", Enabled: true, TeamRestriction: "all", CreatorID: f.data.Viewer.ID, CreatedAt: now},
					}
					return nil
				})
				restore()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.db.ExecContext(t.Context(), `DELETE FROM workspace_content_records WHERE workspace_key=?`, workspace); err != nil {
					t.Fatal(err)
				}
				if _, err := f.db.ExecContext(t.Context(), `DELETE FROM audit_events WHERE workspace_id=? AND id=?`, f.data.Workspace.ID, notificationID+"-audit"); err != nil {
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
				}
				var payload, before, snapshot json.RawMessage
				source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
					payload, before, snapshot = event.Payload, event.PreviousValues, event.Snapshot
				})
				var input domain.Webhook
				mutate := scaleP21DisableMutation(target, notificationID, now, func(data *domain.Bootstrap) { input = data.Webhooks[0] })
				ctx := WithMutationScope(t.Context(), MutationScope{Resources: []string{target}})
				if bounded {
					err = source.MutateFailingWebhookDisable(ctx, workspace, target, mutate)
				} else {
					err = source.MutateWorkspace(ctx, workspace, "webhook.auto_disabled", target, map[string]any{"reason": "deliveryFailures"}, mutate)
				}
				source.SetWebhookSink(nil)
				if err != nil {
					t.Fatal("inline disable", target, reload, bounded, err)
				}
				var hooks []domain.Webhook
				if bounded {
					handle, err := source.retainMetadataLogicalSource(t.Context(), workspace)
					if err != nil {
						t.Fatal(err)
					}
					node := webhookFailureWriteField(handle, "webhooks")
					value, err := node.Value(t.Context(), int(node.cost))
					handle.Release()
					if err != nil {
						t.Fatal(err)
					}
					hooks = value.Interface().([]domain.Webhook)
				} else {
					data, _ := source.WorkspaceMetadata(workspace)
					hooks = data.Webhooks
				}
				rows := scaleP21WebhookFailureRows(t, source, workspace, "webhooks")
				if !bounded {
					want, wantInput, wantRows = hooks, input, rows
					wantPayload, wantBefore, wantSnapshot = payload, before, snapshot
					if input.ID != target || input.Name != "First\x00native" && input.Name != "Firstnative" {
						t.Fatal("literal inline callback did not receive first native hook", input)
					}
					t.Logf("target=%q reload=%v literal postimage count=%d", target, reload, len(hooks))
				} else {
					if !reflect.DeepEqual(hooks, want) || !reflect.DeepEqual(input, wantInput) || !reflect.DeepEqual(rows, wantRows) ||
						!bytes.Equal(payload, wantPayload) || !bytes.Equal(before, wantBefore) || !bytes.Equal(snapshot, wantSnapshot) {
						t.Fatalf("inline native differential changed target=%q reload=%v hooks=%v input=%v rows=%v images=%v/%v/%v\ngot=%+v\nwant=%+v", target, reload,
							reflect.DeepEqual(hooks, want), reflect.DeepEqual(input, wantInput), reflect.DeepEqual(rows, wantRows),
							bytes.Equal(payload, wantPayload), bytes.Equal(before, wantBefore), bytes.Equal(snapshot, wantSnapshot), hooks, want)
					}
					if source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0 {
						t.Fatal("inline native work or full decode leaked")
					}
					source.releaseMetadataLogicalSources()
				}
			}
		}
	}
}

func TestScaleP21WebhookDisableWriteBodyBound(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, target := f.data.Workspace.URLKey, idgen.New("wh-disable-body-")
	now := time.Date(2026, 11, 14, 3, 4, 5, 0, time.UTC)
	wantPeak := map[bool]int{}
	for _, scale := range []int{1, 10} {
		for _, reload := range []bool{false, true} {
			if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "webhooks", "documents"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
				data.Webhooks = []domain.Webhook{{ID: target, Enabled: true, TeamRestriction: "all", CreatorID: f.data.Viewer.ID}}
				data.Documents = []domain.Document{{ID: target + "-document", Content: strings.Repeat("unrelated body ", scale*20000)}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.ExecContext(t.Context(), `DELETE FROM workspace_content_records WHERE workspace_key=?`, workspace); err != nil {
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
			if err := view.MutateFailingWebhookDisable(ctx, workspace, target, scaleP21DisableMutation(target, idgen.New("body-notification-"), now, nil)); err != nil {
				t.Fatal(err)
			}
			if scale == 1 {
				wantPeak[reload] = peak
			} else if peak != wantPeak[reload] {
				t.Fatal("unrelated body growth increased admitted work", reload, peak, wantPeak[reload])
			}
			if !reflect.DeepEqual(documents, scaleP21WebhookFailureRows(t, view, workspace, "documents")) || view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("unrelated body changed, work leaked, or full metadata decoded")
			}
			t.Logf("scale=%d reload=%v admittedPeak=%d", scale, reload, peak)
			view.releaseMetadataLogicalSources()
		}
	}
}
