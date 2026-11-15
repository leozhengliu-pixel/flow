package main

import (
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

func TestScaleP21HotWebhookDisableCallbackDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	previousDefaults := domain.CurrentCreationDefaults()
	defer domain.SetCreationDefaults(previousDefaults)
	defaults := previousDefaults
	email, desktop, format := true, false, "immediate"
	defaults.User.EmailNotifications, defaults.User.DesktopNotifications, defaults.User.EmailFormat = &email, &desktop, &format
	domain.SetCreationDefaults(defaults)
	repo, err := store.OpenDatabase(scaleP03DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	enabled := domain.NotificationChannelPreferences{Enabled: true}
	disabled := domain.NotificationChannelPreferences{Enabled: true, Categories: map[string]bool{"integrations": false}}
	for _, scenario := range []struct {
		name                                                                                                   string
		priority                                                                                               int
		breached, missingPrefs, noRecipient, inactive, missingHook, disabledHook, categoriesOff, duplicateUser bool
	}{
		{name: "digest-timezone", priority: 3},
		{name: "urgent-empty-issue", priority: 1},
		{name: "breached-empty-issue", priority: 3, breached: true},
		{name: "operator-defaults", priority: 3, missingPrefs: true},
		{name: "explicit-category-off", priority: 3, categoriesOff: true},
		{name: "missing-creator", priority: 3, noRecipient: true},
		{name: "inactive-creator", priority: 3, inactive: true},
		{name: "missing-hook", priority: 3, missingHook: true},
		{name: "already-disabled", priority: 3, disabledHook: true},
		{name: "first-duplicate-creator", priority: 3, duplicateUser: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			workspace, target := f.data.Workspace.URLKey, idgen.New("wh-disable-callback-")
			now := time.Date(2026, 11, 13, 22, 0, 0, 0, time.UTC)
			type result struct {
				Hooks                      []domain.Webhook
				Notifications              []domain.Notification
				Deliveries                 []domain.NotificationDelivery
				Audits                     []domain.AuditLogEntry
				Events                     []json.RawMessage
				Before, Snapshot, Realtime json.RawMessage
			}
			for _, full := range []bool{false, true} {
				for _, reload := range []bool{false, true} {
					var want result
					for _, bounded := range []bool{false, true} {
						if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
						if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.webhook_disable_fixture", "", nil, func(data *domain.Bootstrap) error {
							data.Users = slices.Clone(f.data.Users)
							if len(data.Users) == 0 {
								data.Users = []domain.User{f.data.Viewer}
							}
							creator := f.data.Viewer.ID
							for index := range data.Users {
								if data.Users[index].ID == creator {
									data.Users[index].Active = !scenario.inactive
								}
							}
							if scenario.duplicateUser {
								user := f.data.Viewer
								user.Name, user.Active = "Later duplicate", false
								data.Users = append(data.Users, user)
							}
							if scenario.noRecipient {
								creator = target + "-missing"
							}
							failing := now.Add(-72 * time.Hour)
							data.Webhooks = []domain.Webhook{{ID: target, Name: "Notify creator", Enabled: !scenario.disabledHook, TeamRestriction: "all", CreatorID: creator, URL: "https://example.test/hook", FailingSince: &failing}}
							if scenario.missingHook {
								data.Webhooks = nil
							}
							data.NotificationPreferences = map[string]domain.NotificationPreferences{}
							if !scenario.missingPrefs {
								channel := enabled
								if scenario.categoriesOff {
									channel = disabled
								}
								data.NotificationPreferences[f.data.Viewer.ID] = domain.NotificationPreferences{
									UserID: "different-native-map-key", Email: channel, Desktop: channel, Chat: &channel,
									EmailFormat: "digest", ImmediateUrgent: true, DelayLowPriority: true,
								}
							}
							data.PushSubscriptions = []domain.PushSubscription{{ID: target + "-push", UserID: f.data.Viewer.ID, Enabled: true, Endpoint: "https://example.test/push"}}
							team := f.data.Teams[0]
							settings := data.TeamSettings[team.ID]
							settings.Timezone = "America/Los_Angeles"
							data.TeamSettings[team.ID] = settings
							data.Issues = []domain.Issue{{
								ID: "", Identifier: "EMPTY-1", Title: "Legal empty native ID", Priority: scenario.priority,
								Team: team, State: domain.WorkflowState{ID: "disable-state", Type: "backlog"}, Creator: f.data.Viewer, Assignee: &f.data.Viewer,
								CreatedAt: now, UpdatedAt: now, Description: strings.Repeat("unrelated empty issue body ", 10000),
								DocumentContent: &domain.DocumentContent{ContentData: map[string]any{"nested": map[string]any{"id": target, "name": "Empty issue nested winner"}}},
							}}
							data.IssueSLAs = nil
							if scenario.breached {
								data.IssueSLAs = []domain.IssueSLA{{ID: target + "-sla", IssueID: "", Status: "breached"}}
							}
							data.Notifications, data.NotificationDeliveries = []domain.Notification{}, []domain.NotificationDelivery{}
							return nil
						}); err != nil {
							t.Fatal(err)
						}
						if reload {
							if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
								t.Fatal(err)
							}
						}
						source := repo
						if bounded {
							source, err = repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
							if err != nil {
								t.Fatal(err)
							}
						}
						var got result
						source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
							got.Events = append(got.Events, event.Payload)
							got.Before, got.Snapshot = event.PreviousValues, event.Snapshot
						})
						source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { got.Realtime = event.Payload })
						restore := store.ForceFullMutationsForTesting(full)
						s := &server{store: source}
						s.disableFailingWebhook(workspace, target, now)
						s.disableFailingWebhook(workspace, target, now)
						restore()
						source.SetWebhookSink(nil)
						source.SetRealtimeSink(nil)
						if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
						data, ok := repo.BootstrapForContext(t.Context(), workspace)
						if !ok {
							t.Fatal("postimage unavailable")
						}
						got.Hooks = data.Webhooks
						got.Notifications = slices.DeleteFunc(data.Notifications, func(item domain.Notification) bool { return item.SourceID != target })
						for index := range got.Notifications {
							id := got.Notifications[index].ID
							got.Notifications[index].ID = "generated"
							for _, delivery := range data.NotificationDeliveries {
								if delivery.NotificationID == id {
									delivery.ID, delivery.NotificationID = "generated-"+delivery.Channel, "generated"
									if delivery.Attempts > 0 {
										if delivery.UpdatedAt.IsZero() {
											t.Fatal("post-commit delivery attempt omitted its timestamp")
										}
										delivery.UpdatedAt = now
									}
									got.Deliveries = append(got.Deliveries, delivery)
								}
							}
						}
						slices.SortFunc(got.Deliveries, func(a, b domain.NotificationDelivery) int { return strings.Compare(a.Channel, b.Channel) })
						entries, err := repo.ListAuditEntries(t.Context(), f.data.Workspace.ID, store.AuditQuery{ResourceID: target})
						if err != nil {
							t.Fatal(err)
						}
						if len(entries) > 0 && len(got.Events) > 0 {
							got.Audits = entries[:1]
							got.Audits[0].ID, got.Audits[0].Seq, got.Audits[0].Hash, got.Audits[0].CreatedAt = "generated", 0, "", now
						}
						expectedEvents := 1
						if scenario.missingHook || scenario.disabledHook {
							expectedEvents = 0
						}
						if len(got.Events) != expectedEvents {
							t.Fatal("enabled guard/event count changed", full, reload, bounded, len(got.Events))
						}
						if !scenario.noRecipient && !scenario.inactive && expectedEvents == 1 && len(got.Notifications) != 1 {
							t.Fatal("creator notification missing", full, reload, bounded, len(got.Notifications))
						}
						if full && !scenario.missingHook && !scenario.disabledHook {
							entity := snapshotEntity(domain.DomainEvent{Snapshot: got.Snapshot})
							if entity["name"] != "Empty issue nested winner" {
								t.Fatal("forced-full issue event image was narrowed", entity["name"])
							}
						}
						for _, delivery := range got.Deliveries {
							if delivery.Channel != "email" || scenario.missingPrefs || scenario.categoriesOff {
								continue
							}
							status := "digest"
							if full && (scenario.priority == 1 || scenario.breached) {
								status = "pending-disabled"
							}
							if delivery.Status != status {
								t.Fatal("empty issue urgency changed", full, scenario.name, delivery.Status, status)
							}
							if status == "digest" && full && delivery.NextAttemptAt != nil && !delivery.NextAttemptAt.Equal(now.Add(time.Hour)) {
								t.Fatal("empty issue timezone changed", full, delivery.NextAttemptAt)
							}
						}
						if !bounded {
							want = got
						} else {
							left, _ := json.Marshal(got)
							right, _ := json.Marshal(want)
							if !reflect.DeepEqual(json.RawMessage(left), json.RawMessage(right)) || source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0 {
								t.Fatalf("disable callback differential changed full=%v reload=%v hooks=%v notifications=%v deliveries=%v audits=%v events=%v before=%v snapshot=%v realtime=%v work=%d", full, reload,
									reflect.DeepEqual(got.Hooks, want.Hooks), reflect.DeepEqual(got.Notifications, want.Notifications), reflect.DeepEqual(got.Deliveries, want.Deliveries), reflect.DeepEqual(got.Audits, want.Audits),
									reflect.DeepEqual(got.Events, want.Events), reflect.DeepEqual(got.Before, want.Before), reflect.DeepEqual(got.Snapshot, want.Snapshot), reflect.DeepEqual(got.Realtime, want.Realtime), source.MetadataWorkUsedForTesting())
							}
						}
					}
				}
			}
		})
	}
}
