package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func scaleP21PulseDispatch(input store.PulseSummaryDispatchInput) (store.PulseSummaryDispatch, error) {
	preferences := input.Preferences
	if !input.HasPreferences {
		preferences = defaultPreferences(input.Notification.RecipientID)
	}
	if !preferences.Inbox.Enabled || !categoryEnabled(preferences.Inbox, "pulse") {
		return store.PulseSummaryDispatch{}, nil
	}
	data := domain.Bootstrap{}
	if input.PushEnabled {
		data.PushSubscriptions = []domain.PushSubscription{{UserID: input.Notification.RecipientID, Enabled: true}}
	}
	enqueueNotificationDeliveries(&data, input.Notification, preferences)
	return store.PulseSummaryDispatch{Send: true, Deliveries: data.NotificationDeliveries}, nil
}

func scaleP21PulseLegacyWrite(ctx context.Context, repo *store.SQLiteStore, workspace string, due []pulseDue, now time.Time) error {
	ctx = store.WithMetadataFields(ctx, "settings", "workspaceSettings", "userSettings", "notificationPreferences", "pushSubscriptions")
	return repo.MutateWorkspace(ctx, workspace, "pulse.summary_scheduled", "", nil, func(data *domain.Bootstrap) error {
		if !workspaceFeatureEnabled(data.WorkspaceSettings, "pulse") {
			return store.ErrNoMutation
		}
		current := pulseCursors(data)
		changed := false
		for _, item := range due {
			if !current[item.user.ID].Before(item.end) {
				continue
			}
			if effectivePulseSchedule(data.UserSettings[item.user.ID].PulseSchedule, data.WorkspaceSettings.FeatureSettings.PulseWorkspaceSchedule) != item.schedule {
				continue
			}
			preferences, ok := data.NotificationPreferences[item.user.ID]
			if !ok {
				preferences = defaultPreferences(item.user.ID)
			}
			if item.eligible && item.total > 0 && preferences.Inbox.Enabled && categoryEnabled(preferences.Inbox, "pulse") {
				data.Notifications = append(data.Notifications, newPulseSummaryNotification(item, now))
				enqueueNotificationDeliveries(data, data.Notifications[len(data.Notifications)-1], preferences)
			}
			current[item.user.ID] = item.end
			changed = true
		}
		if !changed {
			return store.ErrNoMutation
		}
		if data.Settings == nil {
			data.Settings = map[string]any{}
		}
		data.Settings["pulseDeliveryCursors"] = current
		return nil
	})
}

func TestScaleP21PulseSummaryWriterServerPolicyDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	previousDefaults := domain.CurrentCreationDefaults()
	defer domain.SetCreationDefaults(previousDefaults)
	email, desktop, format := false, true, "immediate"
	operator := previousDefaults
	operator.User.EmailNotifications, operator.User.DesktopNotifications, operator.User.EmailFormat = &email, &desktop, &format
	domain.SetCreationDefaults(operator)
	for _, repo := range scaleP03ServerTestStores(t) {
		enabled := domain.NotificationChannelPreferences{Enabled: true}
		empty := domain.NotificationChannelPreferences{Enabled: true, Categories: map[string]bool{}}
		pulseOff := domain.NotificationChannelPreferences{Enabled: true, Categories: map[string]bool{"pulse": false}}
		for index, scenario := range []struct {
			name     string
			prefs    *domain.NotificationPreferences
			push     bool
			disabled bool
			schedule string
			seen     bool
			eligible bool
			total    int
		}{
			{name: "operator-defaults-missing", eligible: true, total: 2},
			{name: "nil-categories-digest", prefs: &domain.NotificationPreferences{UserID: "different-map-key", Inbox: enabled, Email: enabled, Desktop: enabled, EmailFormat: "digest", DelayLowPriority: true}, eligible: true, total: 2},
			{name: "empty-categories-chat-push", prefs: &domain.NotificationPreferences{Inbox: empty, Email: empty, Desktop: empty, Chat: &empty, EmailFormat: "immediate"}, push: true, eligible: true, total: 2},
			{name: "explicit-pulse-category-off", prefs: &domain.NotificationPreferences{Inbox: pulseOff, Email: enabled, Desktop: enabled}, eligible: true, total: 2},
			{name: "present-zero-preferences", prefs: &domain.NotificationPreferences{}, eligible: true, total: 2},
			{name: "disabled-feature", disabled: true, eligible: true, total: 2},
			{name: "queued-schedule-changed", schedule: "weekly", eligible: true, total: 2},
			{name: "already-delivered-cursor", seen: true, eligible: true, total: 2},
			{name: "ineligible-cursor-only", eligible: false, total: 2},
			{name: "no-updates-cursor-only", eligible: true, total: 0},
		} {
			t.Run(scenario.name, func(t *testing.T) {
				// Missing preferences are normalized on reload. Keep each case's
				// canonical preimage independent of previous case deletions.
				f := scaleP03Fixture(t, repo)
				if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
					t.Fatal(err)
				}
				workspace, user := f.data.Workspace.URLKey, f.data.Viewer
				end := time.Date(2026, 11, 20+index, 6, 0, 0, 0, time.UTC)
				now := end.Add(17*time.Hour + 30*time.Minute)
				due := []pulseDue{{user: user, schedule: "daily", start: end.Add(-24 * time.Hour), end: end, eligible: scenario.eligible, total: scenario.total,
					refs: []domain.PulseUpdateRef{{ID: "complete-update", Kind: "project", SourceID: "source", Source: "Complete source", At: end.Add(-time.Hour)}}}}
				notification := newPulseSummaryNotification(due[0], now)
				var outcomes []any
				for _, bounded := range []bool{false, true} {
					repo.SetWebhookSink(nil)
					repo.SetRealtimeSink(nil)
					if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
					if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.pulse_fixture", "", nil, func(data *domain.Bootstrap) error {
						data.WorkspaceSettings.FeatureFlags["pulse"] = !scenario.disabled
						data.WorkspaceSettings.FeatureSettings.PulseWorkspaceSchedule = "daily"
						cursor := end.Add(-24 * time.Hour)
						if scenario.seen {
							cursor = end
						}
						data.Settings["pulseDeliveryCursors"] = map[string]time.Time{user.ID: cursor}
						settings := data.UserSettings[user.ID]
						settings.PulseSchedule = scenario.schedule
						data.UserSettings[user.ID] = settings
						delete(data.NotificationPreferences, user.ID)
						if scenario.prefs != nil {
							data.NotificationPreferences[user.ID] = *scenario.prefs
						}
						data.PushSubscriptions = slices.DeleteFunc(data.PushSubscriptions, func(item domain.PushSubscription) bool { return item.UserID == user.ID })
						if scenario.push {
							data.PushSubscriptions = append(data.PushSubscriptions, domain.PushSubscription{ID: "summary-policy-push", UserID: user.ID, Enabled: true, Endpoint: "https://example.test/push"})
						}
						data.Notifications = slices.DeleteFunc(data.Notifications, func(item domain.Notification) bool { return item.ID == notification.ID })
						data.NotificationDeliveries = slices.DeleteFunc(data.NotificationDeliveries, func(item domain.NotificationDelivery) bool { return item.NotificationID == notification.ID })
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					staged, found := repo.WorkspaceMetadata(workspace)
					prefs, hasPrefs := staged.NotificationPreferences[user.ID]
					if !found || hasPrefs != (scenario.prefs != nil) || hasPrefs && !reflect.DeepEqual(prefs, *scenario.prefs) {
						t.Fatal("fixture did not stage exact native preferences", bounded, prefs, hasPrefs)
					}
					policy, policyErr := repo.ReadCanonicalRecords(t.Context(), workspace, []store.RecordKey{{Kind: "notificationPreferences", ID: user.ID}}, "detail")
					if policyErr != nil || len(policy.Items) != 1 || policy.Items[0].Omitted || policy.Items[0].Missing != (scenario.prefs == nil) {
						t.Fatal("fixture did not stage canonical preferences", bounded, policy, policyErr)
					}
					if scenario.prefs != nil {
						var canonical domain.NotificationPreferences
						if err := json.Unmarshal(policy.Items[0].Data, &canonical); err != nil || !reflect.DeepEqual(canonical, *scenario.prefs) {
							t.Fatal("canonical preferences differ from native preimage", bounded, canonical, err)
						}
					}
					source := repo
					if bounded {
						var err error
						source, err = repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
						if err != nil {
							t.Fatal(err)
						}
					}
					var events []any
					capture := func(_ string, event domain.RealtimeEvent) {
						if event.Type == "pulse.summary_scheduled" || event.Type == "notification.created" {
							events = append(events, []any{event.Type, event.AggregateID, string(event.Payload)})
						}
					}
					repo.SetRealtimeSink(capture)
					source.SetRealtimeSink(capture)
					var err error
					if bounded {
						candidate := store.PulseSummaryCandidate{UserID: user.ID, Schedule: "daily", End: end}
						if scenario.eligible && scenario.total > 0 {
							candidate.Notification = &notification
						}
						err = source.CommitPulseSummaries(t.Context(), workspace, []store.PulseSummaryCandidate{candidate}, scaleP21PulseDispatch)
					} else {
						err = scaleP21PulseLegacyWrite(t.Context(), source, workspace, due, now)
					}
					if err != nil {
						t.Fatal("summary policy write failed", bounded, err)
					}
					stored, recordErr := repo.NotificationRecord(t.Context(), workspace, user.ID, notification.ID)
					if recordErr != nil && !errors.Is(recordErr, sql.ErrNoRows) {
						t.Fatal(recordErr)
					}
					deliveries, err := repo.NotificationDeliveries(t.Context(), workspace, []string{"pending", "digest"}, now.AddDate(0, 0, 8))
					if err != nil {
						t.Fatal(err)
					}
					items := slices.DeleteFunc(deliveries.NotificationDeliveries, func(item domain.NotificationDelivery) bool { return item.NotificationID != notification.ID })
					slices.SortFunc(items, func(a, b domain.NotificationDelivery) int { return strings.Compare(a.ID, b.ID) })
					batch, err := repo.ReadCanonicalRecords(t.Context(), workspace, []store.RecordKey{{Kind: "settings", ID: "pulseDeliveryCursors"}}, "detail")
					if err != nil || len(batch.Items) != 1 || batch.Items[0].Omitted {
						t.Fatal("read canonical summary cursor", err)
					}
					raw, _ := json.Marshal([]any{recordErr == nil, stored, items, batch.Items[0].Data, events})
					outcomes = append(outcomes, string(raw))
					if bounded && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
						t.Fatal("typed policy writer leaked work or decoded full metadata")
					}
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatal("operator/default/nil/category/digest/push/queued policy differential", outcomes)
				}
			})
		}
	}
}

func TestScaleP21PulseSummaryWriterSchedulerDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
			t.Fatal(err)
		}
		workspace, user := f.data.Workspace.URLKey, f.data.Viewer
		if len(f.data.Teams) == 0 {
			t.Fatal("scheduler fixture has no team")
		}
		now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
		end := time.Date(2026, 11, 16, 6, 0, 0, 0, time.UTC)
		notificationID := "pulse_" + user.ID + "_" + strconv.FormatInt(end.Unix(), 10)
		projectID := "pulse-scheduler-" + user.ID
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.pulse_fixture", "", nil, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.FeatureFlags["pulse"] = true
			data.WorkspaceSettings.FeatureSettings.PulseWorkspaceSchedule = "daily"
			settings := data.UserSettings[user.ID]
			settings.PulseSchedule, settings.Timezone = "daily", "UTC"
			data.UserSettings[user.ID] = settings
			data.Projects = append(data.Projects, domain.Project{
				ID: projectID, Name: "Scheduler project", TeamIDs: []string{f.data.Teams[0].ID}, MemberIDs: []string{user.ID},
			})
			data.ProjectUpdates[projectID] = []domain.ProjectUpdate{{
				ID: "scheduler-update", ProjectID: projectID, Body: "Complete scheduler update",
				User: domain.User{ID: "pulse-scheduler-writer", Name: "Writer", DisplayName: "Writer"}, CreatedAt: now.Add(-5 * time.Hour),
			}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		var outcomes []string
		for _, bounded := range []bool{false, true} {
			repo.SetRealtimeSink(nil)
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.pulse_fixture", "", nil, func(data *domain.Bootstrap) error {
				data.Settings["pulseDeliveryCursors"] = map[string]time.Time{user.ID: end.Add(-24 * time.Hour)}
				data.Notifications = slices.DeleteFunc(data.Notifications, func(item domain.Notification) bool { return item.ID == notificationID })
				data.NotificationDeliveries = slices.DeleteFunc(data.NotificationDeliveries, func(item domain.NotificationDelivery) bool { return item.NotificationID == notificationID })
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			source := repo
			if bounded {
				var err error
				source, err = repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
				if err != nil {
					t.Fatal(err)
				}
			}
			var events []any
			capture := func(key string, event domain.RealtimeEvent) {
				if key == workspace && (event.Type == "pulse.summary_scheduled" || event.Type == "notification.created") {
					events = append(events, []any{event.Type, event.AggregateID, string(event.Payload)})
				}
			}
			repo.SetRealtimeSink(capture)
			source.SetRealtimeSink(capture)
			scheduler := &server{store: source}
			if err := scheduler.preparePulseSummaries(t.Context(), workspace, now); err != nil {
				t.Fatal("scheduler write failed", bounded, err)
			}
			written := len(events)
			if err := scheduler.preparePulseSummaries(t.Context(), workspace, now); err != nil || len(events) != written || written != 2 {
				t.Fatal("scheduler did not publish one summary and one recipient signal", bounded, err, events)
			}
			notification, err := repo.NotificationRecord(t.Context(), workspace, user.ID, notificationID)
			if err != nil || notification.Payload == nil || !slices.Equal(notification.Payload.UpdateIDs, []string{"scheduler-update"}) {
				t.Fatal("scheduler summary is incomplete", bounded, notification, err)
			}
			deliveries, err := repo.NotificationDeliveries(t.Context(), workspace, []string{"pending", "digest"}, now.AddDate(0, 0, 8))
			if err != nil {
				t.Fatal(err)
			}
			items := slices.DeleteFunc(deliveries.NotificationDeliveries, func(item domain.NotificationDelivery) bool { return item.NotificationID != notificationID })
			slices.SortFunc(items, func(a, b domain.NotificationDelivery) int { return strings.Compare(a.ID, b.ID) })
			cursor, err := repo.ReadCanonicalRecords(t.Context(), workspace, []store.RecordKey{{Kind: "settings", ID: "pulseDeliveryCursors"}}, "detail")
			if err != nil || len(cursor.Items) != 1 || cursor.Items[0].Omitted {
				t.Fatal("scheduler cursor is unavailable", bounded, err)
			}
			raw, err := json.Marshal([]any{notification, items, cursor.Items[0].Data, events})
			if err != nil {
				t.Fatal(err)
			}
			outcomes = append(outcomes, string(raw))
			if bounded && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
				t.Fatal("scheduler writer leaked work or decoded full metadata")
			}
		}
		if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
			t.Fatal("scheduler cursor/notification/delivery/publication differential", outcomes)
		}
	}
}
