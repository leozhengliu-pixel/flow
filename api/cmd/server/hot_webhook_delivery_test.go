package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21HotWebhookDeliveryRoutingAndActors(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, err := store.OpenDatabase(scaleP03DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP03Fixture(t, repo)
	workspace, public := f.data.Workspace.URLKey, f.data.Teams[0].ID
	probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	prefix, private := idgen.New("hot-webhook-routing-"), idgen.New("hot-webhook-private-")
	hooks := []domain.Webhook{
		{ID: prefix + "legacy", URL: "https://example.test/v1", Enabled: true, PayloadVersion: 1},
		{ID: prefix + "private", URL: "https://example.test/private", Enabled: true, PayloadVersion: 2, ResourceTypes: []string{"issues", "projects", "documents", "milestones"}, TeamRestriction: "selected", TeamIDs: []string{private}},
		{ID: prefix + "empty-selected", URL: "https://example.test/empty", Enabled: true, PayloadVersion: 2, TeamRestriction: "selected", TeamIDs: []string{}},
		{ID: prefix + "denied", URL: "https://example.test/denied", Enabled: true, PayloadVersion: 2, ResourceTypes: []string{"issues"}, TeamRestriction: "selected", TeamIDs: []string{public}},
		{ID: prefix + "users", URL: "https://example.test/users", Enabled: true, PayloadVersion: 2, ResourceTypes: []string{"users"}},
		{ID: prefix + "audit", URL: "https://example.test/audit", Enabled: true, AuditLog: true},
		{ID: prefix + "disabled", URL: "https://example.test/disabled"},
	}
	if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "webhooks", "users", "documents"), workspace, "scale_p21.webhook_routing_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Webhooks = hooks
		for index := range data.Users {
			if data.Users[index].ID == f.data.Viewer.ID {
				data.Users[index].Name, data.Users[index].DisplayName, data.Users[index].Username = "Native actor", "Displayed actor", "native-handle"
				data.Users[index].JobTitle = "Member subject keeps this field"
			}
		}
		data.Documents = []domain.Document{{ID: prefix + "unrelated", Content: strings.Repeat("unrelated body ", 100000)}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	oldSlots := webhookDeliverySlots
	webhookDeliverySlots = make(chan struct{}, 1)
	webhookDeliverySlots <- struct{}{}
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
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	cases := []domain.DomainEvent{
		{ID: prefix + "issue-event", Type: "issue.created", AggregateID: prefix + "issue", ActorID: f.data.Viewer.ID, CreatedAt: now,
			Payload: raw(map[string]any{"title": "Private issue"}), Snapshot: raw(map[string]any{"entity": map[string]any{"id": prefix + "issue", "identifier": "HWR-1", "title": "Private issue", "team": map[string]any{"id": private}}})},
		{ID: prefix + "deleted-event", Type: "issue.deleted", AggregateID: prefix + "deleted", ActorID: "missing-actor", CreatedAt: now,
			Payload:  raw(map[string]any{"ids": []string{prefix + "deleted"}, "issueIds": []string{prefix + "deleted"}, "issueAccess": map[string]any{"secret": private}}),
			Snapshot: raw(map[string]any{"deleted": true, "entity": map[string]any{"id": prefix + "deleted", "identifier": "HWR-2", "title": "Deleted", "team": map[string]any{"id": private}}})},
		{ID: prefix + "project-event", Type: "project.updated", AggregateID: prefix + "project", ActorID: f.data.Viewer.ID, CreatedAt: now,
			Payload: raw(map[string]any{"name": "New project"}), PreviousValues: raw(map[string]any{"name": "Previous project"}),
			Snapshot: raw(map[string]any{"entity": map[string]any{"id": prefix + "project", "name": "New project", "slugId": "project-alias", "teamIds": []string{private}}})},
		{ID: prefix + "document-event", Type: "document.updated", AggregateID: prefix + "document", ActorID: f.data.Viewer.ID, CreatedAt: now,
			Payload: raw(map[string]any{"title": "Document"}), Snapshot: raw(map[string]any{"entity": map[string]any{"id": prefix + "document", "slugId": "document-alias", "teamIds": []string{private}}})},
		{ID: prefix + "member-event", Type: "workspace_member.suspended", AggregateID: f.data.Viewer.ID, ActorID: f.data.Viewer.ID, CreatedAt: now, Payload: raw(map[string]any{"status": "suspended"})},
		{ID: prefix + "milestone-event", Type: "project.milestones_reordered", AggregateID: prefix + "project", ActorID: f.data.Viewer.ID, CreatedAt: now,
			PreviousValues: raw(map[string]any{"milestones": []any{map[string]any{"id": "first"}, map[string]any{"id": "second"}}}),
			Snapshot:       raw(map[string]any{"entity": map[string]any{"id": prefix + "project", "slugId": "project-alias", "teamIds": []string{private}, "milestones": []any{map[string]any{"id": "second", "name": "Second"}, map[string]any{"id": "first", "name": "First"}}}})},
	}
	type queued struct {
		ID, Hook, URL, Secret, EventID, EventType, Action, Status, Kind string
		MaxAttempts, Attempt                                            int
		Payload                                                         json.RawMessage
	}
	for _, reload := range []bool{false, true} {
		if reload {
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}
		if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		var expected []queued
		for _, bounded := range []bool{false, true} {
			clear()
			source := repo
			if bounded {
				source = probe
			}
			s := &server{store: source, allowedOrigin: "https://flow.example.test"}
			for _, event := range cases {
				s.dispatchWebhookEvent(workspace, event)
			}
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
					got = append(got, queued{delivery.ID, delivery.WebhookID, delivery.URL, delivery.SecretRef, delivery.EventID, delivery.EventType, delivery.Action, delivery.Status, delivery.Kind, delivery.MaxAttempts, delivery.Attempt, delivery.Payload})
					if hook.ID == prefix+"audit" || hook.ID == prefix+"disabled" || hook.ID == prefix+"denied" {
						t.Fatal("ineligible or private-denied webhook queued", hook.ID)
					}
				}
			}
			slices.SortFunc(got, func(a, b queued) int { return strings.Compare(a.ID, b.ID) })
			if !bounded {
				expected = got
				if len(got) < 16 {
					t.Fatal("literal routing fixture did not queue enough envelopes", len(got))
				}
			} else if !reflect.DeepEqual(got, expected) || source.WorkspaceMetadataDecodes() != 0 || source.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("actual routing/envelope/actor output differs or leaked ownership", reload, len(expected), len(got), source.MetadataWorkUsedForTesting())
			}
			t.Logf("reload=%t bounded=%t queued=%d full_decodes=%d held_after=%d", reload, bounded, len(got), source.WorkspaceMetadataDecodes(), source.MetadataWorkUsedForTesting())
		}
	}
}

func TestScaleP21HotWebhookDeliveryHealthAggregateCollision(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, err := store.OpenDatabase(scaleP03DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP03Fixture(t, repo)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("hot-webhook-health-")
	probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	target, unrelated, document := prefix+"target", prefix+"unrelated", prefix+"document"
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	seed := func() {
		t.Helper()
		if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "webhooks", "documents"), workspace, "scale_p21.webhook_health_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Webhooks = []domain.Webhook{{ID: target, Name: "Target", URL: "https://example.test/target", Enabled: true}, {ID: unrelated, Name: strings.Repeat("unrelated hook ", 100000), URL: "https://example.test/other", Enabled: true}}
			data.Documents = []domain.Document{{ID: document, ContentData: map[string]any{"nested": map[string]any{"id": target, "name": "Earlier document object"}}}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	record := func(field, id string) []byte {
		t.Helper()
		var body []byte
		if err := repo.StreamMetadataRecords(t.Context(), workspace, field, func(key store.RecordKey, input io.Reader) error {
			if key.ID != id {
				return nil
			}
			var err error
			body, err = io.ReadAll(input)
			return err
		}); err != nil || len(body) == 0 {
			t.Fatal("canonical record unavailable", field, id, err)
		}
		return body
	}
	type effects struct {
		Target, Unrelated, Document []byte
		Payload, Previous, Snapshot json.RawMessage
		Realtime                    json.RawMessage
		Events                      int
	}
	for _, reload := range []bool{false, true} {
		var expected effects
		for _, bounded := range []bool{false, true} {
			seed()
			if reload {
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			source := repo
			if bounded {
				if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
				source = probe
			}
			beforeUnrelated, beforeDocument := record("webhooks", unrelated), record("documents", document)
			var original domain.Webhook
			if err := json.Unmarshal(record("webhooks", target), &original); err != nil || original.FailingSince != nil {
				t.Fatal("health fixture did not restore canonical preimage", reload, bounded, err, original.FailingSince)
			}
			var got effects
			source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
				got.Payload, got.Previous, got.Snapshot = event.Payload, event.PreviousValues, event.Snapshot
				got.Events++
			})
			source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { got.Realtime = event.Payload })
			s := &server{store: source}
			for range 2 {
				s.updateWebhookHealth(workspace, target, func(hook *domain.Webhook) bool {
					if hook.FailingSince != nil {
						return false
					}
					hook.FailingSince = &now
					return true
				})
			}
			source.SetWebhookSink(nil)
			source.SetRealtimeSink(nil)
			got.Target, got.Unrelated, got.Document = record("webhooks", target), record("webhooks", unrelated), record("documents", document)
			var hook domain.Webhook
			if err := json.Unmarshal(got.Target, &hook); err != nil || hook.FailingSince == nil || !hook.FailingSince.Equal(now) || got.Events != 1 {
				t.Fatal("health change/no-op semantics failed", reload, bounded, err, got.Events)
			}
			entity := snapshotEntity(domain.DomainEvent{Snapshot: got.Snapshot})
			if entity["name"] != "Earlier document object" || !bytes.Equal(got.Unrelated, beforeUnrelated) || !bytes.Equal(got.Document, beforeDocument) {
				t.Fatal("aggregate collision or unrelated records changed", bounded, entity)
			}
			if !bounded {
				expected = got
			} else if !reflect.DeepEqual(got, expected) || source.WorkspaceMetadataDecodes() != 0 || source.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("health canonical/hooks/previous values differ or leak", reload)
			}
		}
	}
}
