package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW1ImplicitActorDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	beforeUserID, afterUserID := idgen.New("usr_sw1_before_"), idgen.New("usr_sw1_after_")
	t.Cleanup(func() {
		for _, table := range []string{"audit_events", "audit_chain_heads"} {
			if _, err := f.db.ExecContext(context.Background(), `DELETE FROM `+table+` WHERE workspace_id=?`, workspaceID); err != nil {
				t.Errorf("clean up owned actor audit: %v", err)
			}
		}
	})
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projects", "webhooks"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: "sw1-actor-unrelated", Description: strings.Repeat("unrelated ", 40000)})
		data.Webhooks = append(data.Webhooks, domain.Webhook{ID: "sw1-actor-webhook", URL: "https://example.test/hook", Enabled: true})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	unrelated := func() []byte {
		t.Helper()
		var raw []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key='sw1-actor-unrelated'`, workspace).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	originalUnrelated := unrelated()
	type userRow struct {
		ID    string
		Order int64
		Raw   json.RawMessage
	}
	users := func() []userRow {
		t.Helper()
		rows, err := f.db.QueryContext(t.Context(), `SELECT record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field='users' ORDER BY collection_order,record_key`, workspace)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		result := []userRow{}
		for rows.Next() {
			var row userRow
			if err := rows.Scan(&row.ID, &row.Order, &row.Raw); err != nil {
				t.Fatal(err)
			}
			result = append(result, row)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	rejected := errors.New("owned callback failure")
	for _, spec := range []struct {
		name, event, membership string
		fields                  []string
		absent, empty, noActor  bool
		callbackErr             error
	}{
		{name: "existing", event: "application_policy.updated"},
		{name: "new-append", event: "application_policy.updated", absent: true},
		{name: "first-user", event: "application_policy.updated", absent: true, empty: true},
		{name: "fields-exclude-users", event: "application_policy.updated", fields: []string{"settings"}},
		{name: "fields-exclude-new-user", event: "application_policy.updated", fields: []string{"settings"}, absent: true},
		{name: "fields-include-users", event: "application_policy.updated", fields: []string{"settings", "users"}},
		{name: "nonmetadata-ignores-fields", event: "scale_p21.actor_fixture", fields: []string{"settings"}},
		{name: "suspended", event: "application_policy.updated", membership: "suspended"},
		{name: "missing-membership", event: "application_policy.updated", membership: "missing"},
		{name: "deferred-audit", event: "audit.workspace_member", membership: "suspended", fields: []string{"settings", "auditLog"}},
		{name: "rollback-error", event: "application_policy.updated", callbackErr: rejected},
		{name: "rollback-noop", event: "application_policy.updated", callbackErr: ErrNoMutation, absent: true},
		{name: "no-actor", event: "application_policy.updated", noActor: true},
	} {
		t.Run(spec.name, func(t *testing.T) {
			type outcome struct {
				Viewer   domain.User
				Role     string
				Teams    []string
				Users    []userRow
				Target   json.RawMessage
				Audit    json.RawMessage
				Shape    string
				Webhooks []domain.DomainEvent
				Realtime []domain.RealtimeEvent
			}
			actor := f.data.Viewer
			if spec.membership == "missing" {
				actor.ID = idgen.New("usr_sw1_missing_")
			}
			actor.DisplayName = "Fresh account display name"
			previousActor := actor
			previousActor.DisplayName = "Old account display name"
			baseline := []domain.User{{ID: beforeUserID, DisplayName: "Before"}}
			if !spec.absent {
				baseline = append(baseline, previousActor)
			}
			baseline = append(baseline, domain.User{ID: afterUserID, DisplayName: "After"})
			if spec.empty {
				baseline = nil
			}
			var results []outcome
			for _, bounded := range []bool{false, true} {
				f.SetWebhookSink(nil)
				f.SetRealtimeSink(nil)
				if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET status='active' WHERE workspace_id=? AND user_id=?`, workspaceID, f.data.Viewer.ID); err != nil {
					t.Fatal(err)
				}
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
				if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "users", "settings"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
					data.Users = slices.Clone(baseline)
					data.Settings["sw1-actor-target"] = "before"
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if spec.membership == "suspended" {
					if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, workspaceID, f.data.Viewer.ID); err != nil {
						t.Fatal(err)
					}
				}
				before := users()
				source := f.SQLiteStore
				if bounded {
					var err error
					source, err = f.BoundedMetadataViewForTesting(t.Context())
					if err != nil {
						t.Fatal(err)
					}
				}
				ctx := ContextWithRealtimeClient(t.Context(), "sw1-actor-client")
				if !spec.noActor {
					ctx = ContextWithActor(ctx, actor)
				}
				if spec.fields != nil {
					ctx = WithMetadataFields(ctx, spec.fields...)
				}
				got := outcome{}
				source.SetWebhookSink(func(_ string, event domain.DomainEvent) { got.Webhooks = append(got.Webhooks, event) })
				source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { got.Realtime = append(got.Realtime, event) })
				auditID := idgen.New("sw1-audit-")
				called := false
				// Legacy event enrichment sees the aggregate user even when
				// the audit-only callback hid its users collection.
				scope := MetadataRecordScope{
					Read:  []RecordKey{{Kind: "users", ID: actor.ID}},
					Write: []RecordKey{{Kind: "settings", ID: "sw1-actor-target"}},
				}
				err := source.MutateWorkspaceScoped(ctx, workspace, spec.event, actor.ID, map[string]any{"changed": true}, scope, func(data *domain.Bootstrap) error {
					called = true
					got.Viewer, got.Role, got.Teams = data.Viewer, data.ViewerRole, slices.Clone(data.ViewerTeamIDs)
					data.Settings["sw1-actor-target"] = "after"
					data.AuditLog = append(data.AuditLog, domain.AuditLogEntry{ID: auditID, Actor: data.Viewer, Action: "sw1.actor", ResourceType: "settings", ResourceID: "sw1-actor-target", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
					return spec.callbackErr
				})
				wantErr := spec.callbackErr
				if errors.Is(wantErr, ErrNoMutation) {
					wantErr = nil
				}
				if !called || !errors.Is(err, wantErr) {
					t.Fatalf("callback=%v err=%v want=%v", called, err, wantErr)
				}
				got.Users = users()
				var rootRaw []byte
				if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&rootRaw); err != nil {
					t.Fatal(err)
				}
				var root map[string]json.RawMessage
				var shapes map[string]string
				if err := json.Unmarshal(rootRaw, &root); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(root[metadataCollectionsKey], &shapes); err != nil {
					t.Fatal(err)
				}
				got.Shape = shapes["users"]
				if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='settings' AND record_key='sw1-actor-target'`, workspace).Scan(&got.Target); err != nil {
					t.Fatal(err)
				}
				var auditRaw []byte
				err = f.db.QueryRowContext(t.Context(), `SELECT actor FROM audit_events WHERE workspace_id=? AND id=?`, workspaceID, auditID).Scan(&auditRaw)
				got.Audit = auditRaw
				if spec.callbackErr != nil {
					if !errors.Is(err, sql.ErrNoRows) || !reflect.DeepEqual(before, got.Users) || string(got.Target) != `"before"` || len(got.Webhooks) != 0 || len(got.Realtime) != 0 {
						t.Fatalf("rollback changed canonical rows or side effects: %#v auditErr=%v", got, err)
					}
				} else {
					if err != nil || len(got.Webhooks) != 1 || len(got.Realtime) != 1 || string(got.Target) != `"after"` {
						t.Fatalf("committed effects missing: %#v auditErr=%v", got, err)
					}
					refresh := !spec.noActor && !(metadataOnlyMutation(spec.event, nil) && len(spec.fields) > 0 && !slices.Contains(spec.fields, "users"))
					if refresh {
						if got.Shape != "array" {
							t.Fatalf("actor insertion lost users array shape: %q", got.Shape)
						}
						expected := slices.Clone(before)
						raw, err := json.Marshal(actor)
						if err != nil {
							t.Fatal(err)
						}
						index := slices.IndexFunc(expected, func(row userRow) bool { return row.ID == actor.ID })
						if index >= 0 {
							expected[index].Raw = raw
						} else {
							order := int64(0)
							for _, row := range expected {
								order = max(order, row.Order+1)
							}
							expected = append(expected, userRow{ID: actor.ID, Order: order, Raw: raw})
						}
						if !reflect.DeepEqual(expected, got.Users) {
							t.Fatalf("implicit actor row/order: got=%#v want=%#v", got.Users, expected)
						}
					} else if !reflect.DeepEqual(before, got.Users) {
						t.Fatal("excluded actor users record changed")
					}
				}
				for i := range got.Webhooks {
					got.Webhooks[i].ID, got.Webhooks[i].CreatedAt = "", time.Time{}
				}
				for i := range got.Realtime {
					got.Realtime[i].ID, got.Realtime[i].Seq, got.Realtime[i].CreatedAt = "", 0, time.Time{}
				}
				if bounded && (source.WorkspaceMetadataDecodes() != 0 || source.metadataResidency.work.used != 0) {
					t.Fatal("actor refresh decoded complete metadata or leaked admission")
				}
				if !bytes.Equal(originalUnrelated, unrelated()) {
					t.Fatal("actor refresh rewrote unrelated large metadata")
				}
				results = append(results, got)
			}
			if !reflect.DeepEqual(results[0], results[1]) {
				t.Fatalf("actor semantics differ: %#v / %#v", results[0], results[1])
			}
		})
	}
	f.SetWebhookSink(nil)
	f.SetRealtimeSink(nil)
}

func TestScaleP21SW1ActorRetryIsolation(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	actor := f.data.Viewer
	until := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	actor.AppScopes = []string{"read", "write"}
	actor.AppTeamIDs = []string{idgen.New("sw1-team-")}
	actor.OutOfOfficeUntil = &until
	actor.Via = &domain.ActorBot{ID: idgen.New("sw1-app-"), Type: domain.ActorBotTypeOAuthApplication, Name: "Original application"}
	wantActor := mutationInputActor(actor)
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	events, realtime := 0, 0
	probe.SetWebhookSink(func(_ string, _ domain.DomainEvent) { events++ })
	probe.SetRealtimeSink(func(_ string, _ domain.RealtimeEvent) { realtime++ })
	attempts := 0
	err = probe.MutateWorkspaceScoped(ContextWithActor(t.Context(), actor), workspace, "application_policy.updated", actor.ID, nil,
		MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "sw1-actor-retry"}}}, func(data *domain.Bootstrap) error {
			attempts++
			if !reflect.DeepEqual(data.Viewer, wantActor) {
				t.Fatalf("attempt %d inherited mutated actor attribution: %#v want=%#v", attempts, data.Viewer, wantActor)
			}
			index := slices.IndexFunc(data.Users, func(user domain.User) bool { return user.ID == actor.ID })
			if index < 0 || !reflect.DeepEqual(data.Users[index], wantActor) {
				t.Fatalf("attempt %d inherited mutated actor directory input: %#v", attempts, data.Users)
			}
			if attempts == 1 {
				data.Viewer.AppScopes[0] = "first-attempt"
				data.Viewer.AppTeamIDs[0] = "first-attempt-team"
				*data.Viewer.OutOfOfficeUntil = until.Add(time.Hour)
				data.Viewer.Via.Name = "First attempt application"
				data.Settings["sw1-actor-retry"] = "rolled back"
				return &metadataConflictError{keys: []metadataRecordKey{{field: "settings", key: "sw1-actor-retry"}}}
			}
			data.Settings["sw1-actor-retry"] = "committed"
			return nil
		})
	if err != nil || attempts != 2 || events != 1 || realtime != 1 {
		t.Fatalf("retry outcome: attempts=%d events=%d realtime=%d err=%v", attempts, events, realtime, err)
	}
	if !reflect.DeepEqual(actor, wantActor) {
		t.Fatal("callback mutated caller-owned actor slices or pointers")
	}
	var raw []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='users' AND record_key=?`, workspace, actor.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	expected, err := json.Marshal(wantActor)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, expected) {
		t.Fatalf("failed-attempt actor mutation reached canonical row: %s want=%s", raw, expected)
	}
	if probe.WorkspaceMetadataDecodes() != 0 || probe.metadataResidency.work.used != 0 {
		t.Fatal("actor retry decoded complete metadata or leaked work admission")
	}
}
