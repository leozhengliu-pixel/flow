package main

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21WebhookJSONRoutingDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, err := store.OpenDatabase(scaleP03DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP03Fixture(t, repo)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("webhook-json-route-")
	allowed, hidden := prefix+"allowed", prefix+"hidden"
	hooks := []domain.Webhook{
		{ID: prefix + "selected", URL: "https://example.test/selected", Enabled: true, PayloadVersion: 2, TeamRestriction: "selected", TeamIDs: []string{allowed}},
		{ID: prefix + "hidden", URL: "https://example.test/hidden", Enabled: true, PayloadVersion: 2, TeamRestriction: "selected", TeamIDs: []string{hidden}},
		{ID: prefix + "empty", URL: "https://example.test/empty", Enabled: true, PayloadVersion: 2, TeamRestriction: "selected"},
		{ID: prefix + "all", URL: "https://example.test/all", Enabled: true},
		{ID: prefix + "disabled", URL: "https://example.test/disabled"},
	}
	oldSlots := webhookDeliverySlots
	webhookDeliverySlots = make(chan struct{})
	t.Cleanup(func() { webhookDeliverySlots = oldSlots })
	clear := func() {
		t.Helper()
		for _, hook := range hooks {
			if err := repo.DeleteWebhookDeliveries(t.Context(), workspace, hook.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() {
		for _, hook := range hooks {
			_ = repo.DeleteWebhookDeliveries(context.Background(), workspace, hook.ID)
		}
	})
	raw := func(value any) json.RawMessage {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	target := prefix + "event"
	entity := map[string]any{"id": target, "teamId": allowed, "body": strings.Repeat("unrelated body ", 50000)}
	cases := []struct {
		name      string
		settings  map[string]any
		payload   json.RawMessage
		eventID   string
		directIDs []string
	}{
		{"fallback-event-id", map[string]any{"nested": entity}, raw(map[string]any{"name": "new"}), target, nil},
		{"direct-false-is-terminal", map[string]any{"nested": entity}, raw(map[string]any{"teamId": hidden}), target, nil},
		{"nonmatching-array-falls-through", map[string]any{"nested": entity}, raw(map[string]any{"teamIds": []any{"unmatched", 3}}), target, nil},
		{"matching-array", map[string]any{"nested": entity}, raw(map[string]any{"teamIds": []any{allowed}}), "missing", nil},
		{"empty-direct-is-terminal", map[string]any{"nested": entity}, raw(map[string]any{"teamId": ""}), target, nil},
		{"missing-event-id", map[string]any{"nested": entity}, raw(map[string]any{}), prefix + "aggregate-is-not-event", nil},
		{"direct-entity-team-skips-fallback", map[string]any{"nested": entity}, raw(map[string]any{"teamId": allowed}), target, []string{hidden}},
		{"raw-nested-id", map[string]any{"raw": json.RawMessage(raw(entity))}, raw(map[string]any{}), target, nil},
	}
	type queued struct {
		ID, Hook, EventID, EventType, Action, Status, URL, Secret string
		Attempt, MaxAttempts                                      int
		Payload                                                   json.RawMessage
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "settings", "webhooks"), workspace, "scale_p21.webhook_json_route_seed", "", nil, func(data *domain.Bootstrap) error {
				data.Settings, data.Webhooks = test.settings, hooks
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			var want []queued
			for _, bounded := range []bool{false, true} {
				clear()
				source := repo
				if bounded {
					source = probe
				}
				s := &server{store: source, allowedOrigin: "https://flow.example.test"}
				s.enqueueWebhookEvent(t.Context(), workspace, hooks, webhookEvent{
					eventID: test.eventID, eventType: "project.updated", resource: "projects", legacy: "projects",
					action: "update", createdAt: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC),
					data: raw(map[string]any{"id": prefix + "different-aggregate"}), payload: test.payload, teamIDs: test.directIDs,
				})
				var got []queued
				for _, hook := range hooks {
					page, err := repo.WebhookDeliveries(t.Context(), store.WebhookDeliveryQuery{Workspace: workspace, WebhookID: hook.ID, Limit: 100})
					if err != nil {
						t.Fatal(err)
					}
					for _, item := range page.Items {
						delivery, err := repo.WebhookDelivery(t.Context(), workspace, item.ID)
						if err != nil {
							t.Fatal(err)
						}
						got = append(got, queued{delivery.ID, delivery.WebhookID, delivery.EventID, delivery.EventType, delivery.Action, delivery.Status, delivery.URL, delivery.SecretRef, delivery.Attempt, delivery.MaxAttempts, delivery.Payload})
					}
				}
				slices.SortFunc(got, func(a, b queued) int { return strings.Compare(a.ID, b.ID) })
				if !bounded {
					want = got
					if len(got) < 2 {
						t.Fatal("literal fixture did not queue unrestricted hooks", len(got))
					}
				} else if !reflect.DeepEqual(got, want) || source.WorkspaceMetadataDecodes() != 0 || source.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("actual queue/envelope output differs or leaked admission", len(got), len(want), source.MetadataWorkUsedForTesting())
				}
			}
		})
	}
}
