package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestColdTemplateLargeDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_ACCESS", "off")
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "on")
	f := notificationsSetup(t)
	repo, workspace := f.repository, f.data.Workspace.URLKey
	role, status, err := repo.WorkspaceRole(t.Context(), f.data.Workspace.ID, f.data.Viewer.ID)
	if err != nil || status != "active" || !workspaceAdminRole(role) {
		t.Fatal("large template differential requires the real active admin", role, status, err)
	}
	_, session, err := repo.Login(t.Context(), f.data.Viewer.Email, "p03-fixture-password")
	if err != nil {
		t.Fatal("mint real large-template admin session", err)
	}
	prefix := idgen.New("sw244-")
	target, retained := prefix+"target", prefix+"retained"
	large := strings.Repeat("large target ", 30000)
	other := strings.Repeat("retained unrelated template ", 20000)
	if len(large) <= 256<<10 {
		t.Fatal("large target no longer exercises the former point-page ceiling")
	}
	at := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	reset := func() {
		t.Helper()
		if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw244_seed", "", nil, func(data *domain.Bootstrap) error {
			data.DocumentTemplates = []domain.DocumentTemplate{
				{ID: target, Name: "Before", Title: "Retained title", Creator: f.data.Viewer,
					Content: large, ContentState: "retained target state",
					ContentData: map[string]any{"type": "doc", "retained": true},
					CreatedAt:   at, UpdatedAt: at},
				{ID: retained, Name: "Unrelated", TeamID: f.public, Creator: f.data.Viewer,
					Content: other, ContentState: "retained unrelated state",
					ContentData: map[string]any{"type": "doc", "unrelated": true},
					CreatedAt:   at, UpdatedAt: at},
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
	}
	template := func(state coldSettingsOutcome, id string) json.RawMessage {
		t.Helper()
		for _, row := range state.Metadata {
			if row[0] == "documentTemplates" && row[1] == id {
				return bytes.Clone(row[3].(json.RawMessage))
			}
		}
		return nil
	}
	for _, test := range []struct {
		name, method, body string
		code               int
	}{
		{"update", http.MethodPatch, `{"name":"After"}`, http.StatusOK},
		{"delete", http.MethodDelete, "", http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			var expected any
			var expectedOrders [][]any
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				reset()
				orders := coldSettingsRows(t, f, `SELECT field,record_key,collection_order FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,record_key`, workspace)
				if source == repo {
					expectedOrders = orders
				} else {
					restoreOrders(t, f, expectedOrders)
					if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
					if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
						t.Fatal(err)
					}
					orders = coldSettingsRows(t, f, `SELECT field,record_key,collection_order FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,record_key`, workspace)
					if !reflect.DeepEqual(expectedOrders, orders) {
						t.Fatal("large template legs began with different canonical collection orders")
					}
				}
				before := coldSettingsState(t, f)
				retainedBefore := template(before, retained)
				if len(template(before, target)) <= 256<<10 || len(retainedBefore) == 0 {
					t.Fatal("large target or unrelated durable template is missing")
				}
				prior := map[any]bool{}
				for _, row := range coldSettingsRows(t, f, `SELECT id FROM domain_events WHERE workspace_id=?`, f.data.Workspace.ID) {
					prior[row[0]] = true
				}
				got := coldSettingsOutcome{}
				source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
					got.Webhooks = append(got.Webhooks, discussionRoutesEnvelope(t, map[string]any{"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID, "payload": event.Payload, "previousValues": event.PreviousValues, "snapshot": event.Snapshot}))
				})
				source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
					got.Realtime = append(got.Realtime, discussionRoutesEnvelope(t, map[string]any{"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID, "clientId": event.ClientID, "payload": event.Payload}))
				})
				t.Cleanup(func() {
					source.SetWebhookSink(nil)
					source.SetRealtimeSink(nil)
				})
				started := time.Now().UTC()
				response := coldSettingsServe(t, source, workspace, f.data.Viewer.ID, session, test.method, "/api/document-templates/"+target, test.body, nil)
				finished := time.Now().UTC()
				source.SetWebhookSink(nil)
				source.SetRealtimeSink(nil)
				state := coldSettingsState(t, f)
				state.Code, state.Header = response.Code, response.Header().Clone()
				if response.Body.Len() != 0 {
					state.Response = bytes.Clone(response.Body.Bytes())
				}
				state.Webhooks, state.Realtime = got.Webhooks, got.Realtime
				for _, row := range coldSettingsRows(t, f, `SELECT id,event_type,aggregate_id,payload,previous_values FROM domain_events WHERE workspace_id=? ORDER BY created_at,id`, f.data.Workspace.ID, 3, 4) {
					if !prior[row[0]] {
						state.Events = append(state.Events, discussionRoutesEnvelope(t, row[1:]))
					}
				}
				if state.Code != test.code || len(state.Events) != 1 || len(state.Webhooks) != 1 || len(state.Realtime) != 1 {
					t.Fatalf("bounded=%t large %s: status=%d events=%d/%d/%d", source.BoundedMetadataEnabled(), test.name, state.Code, len(state.Events), len(state.Webhooks), len(state.Realtime))
				}
				if !reflect.DeepEqual(before.Issues, state.Issues) || !reflect.DeepEqual(before.Content, state.Content) ||
					!bytes.Equal(retainedBefore, template(state, retained)) {
					t.Fatal("large template mutation changed issue/content rows or unrelated template bytes")
				}
				if test.method == http.MethodPatch {
					for _, raw := range []json.RawMessage{state.Response, template(state, target)} {
						var updated domain.DocumentTemplate
						if err := json.Unmarshal(raw, &updated); err != nil || updated.ID != target ||
							updated.Name != "After" || updated.Title != "Retained title" || updated.Content != large ||
							updated.ContentState != "retained target state" ||
							!reflect.DeepEqual(updated.ContentData, map[string]any{"type": "doc", "retained": true}) {
							t.Fatal("large update response/durable template lost untouched content", err)
						}
					}
				} else if response.Body.Len() != 0 || template(state, target) != nil {
					t.Fatal("large delete returned a body or retained its durable target")
				}
				actual := coldSettingsNormalize(t, state, "", started, finished)
				if source == repo {
					expected = actual
				} else if !reflect.DeepEqual(expected, actual) {
					t.Fatalf("large template output/SQL/publication differs: %s", policyFirstDifference(expected, actual))
				}
				if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
					t.Fatal("large template route retained output ownership or entered HOT hydration")
				}
			}
		})
	}
}
