package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func pulseWindow(schedule string, now time.Time, zone *time.Location) (time.Time, time.Time, bool) {
	local := now.In(zone)
	end := time.Date(local.Year(), local.Month(), local.Day(), 9, 0, 0, 0, zone)
	if now.Before(end) {
		return time.Time{}, time.Time{}, false
	}
	switch schedule {
	case "daily":
		return end.AddDate(0, 0, -1).UTC(), end.UTC(), true
	case "weekly":
		if local.Weekday() == time.Monday {
			return end.AddDate(0, 0, -7).UTC(), end.UTC(), true
		}
	}
	return time.Time{}, time.Time{}, false
}

func pulseCursors(data *domain.Bootstrap) map[string]time.Time {
	cursors := map[string]time.Time{}
	raw, _ := json.Marshal(data.Settings["pulseDeliveryCursors"])
	_ = json.Unmarshal(raw, &cursors)
	if cursors == nil {
		cursors = map[string]time.Time{}
	}
	return cursors
}

func (s *server) preparePulseSummaries(ctx context.Context, key string, now time.Time) error {
	metadata, ok := s.store.WorkspaceMetadata(key)
	if !ok || !workspaceFeatureEnabled(metadata.WorkspaceSettings, "pulse") {
		return nil
	}
	cursors := pulseCursors(&metadata)
	processed := 0
	for _, user := range metadata.Users {
		schedule := metadata.UserSettings[user.ID].PulseSchedule
		if schedule == "" || schedule == "default" {
			schedule = metadata.WorkspaceSettings.FeatureSettings.PulseWorkspaceSchedule
		}
		zone := time.UTC
		for _, membership := range metadata.TeamMembers {
			if membership.UserID == user.ID {
				if location, err := time.LoadLocation(metadata.TeamSettings[membership.TeamID].Timezone); err == nil {
					zone = location
				}
				break
			}
		}
		start, end, due := pulseWindow(schedule, now, zone)
		if !due || !cursors[user.ID].Before(end) {
			continue
		}
		if cursors[user.ID].After(start) {
			start = cursors[user.ID]
		}
		view, err := s.store.PagedWorkspaceMetadata(ctx, key, user.ID)
		if err != nil {
			continue
		}
		count := 0
		for _, project := range view.Projects {
			if project.ArchivedAt != nil {
				continue
			}
			for _, update := range view.ProjectUpdates[project.ID] {
				if update.CreatedAt.After(start) && !update.CreatedAt.After(end) {
					count++
				}
			}
		}
		for _, initiative := range view.Initiatives {
			for _, update := range view.InitiativeUpdates[initiative.ID] {
				if update.CreatedAt.After(start) && !update.CreatedAt.After(end) {
					count++
				}
			}
		}
		err = s.store.MutateWorkspace(ctx, key, "pulse.summary_scheduled", user.ID, nil, func(data *domain.Bootstrap) error {
			current := pulseCursors(data)
			if !workspaceFeatureEnabled(data.WorkspaceSettings, "pulse") || !current[user.ID].Before(end) {
				return store.ErrNoMutation
			}
			currentSchedule := data.UserSettings[user.ID].PulseSchedule
			if currentSchedule == "" || currentSchedule == "default" {
				currentSchedule = data.WorkspaceSettings.FeatureSettings.PulseWorkspaceSchedule
			}
			if currentSchedule != schedule {
				return store.ErrNoMutation
			}
			preferences, ok := data.NotificationPreferences[user.ID]
			if !ok {
				preferences = defaultPreferences(user.ID)
			}
			if count > 0 && preferences.Inbox.Enabled && categoryEnabled(preferences.Inbox, "pulse") {
				id := fmt.Sprintf("pulse_%s_%d", user.ID, end.Unix())
				if !slices.ContainsFunc(data.Notifications, func(n domain.Notification) bool { return n.ID == id }) {
					notification := domain.Notification{ID: id, RecipientID: user.ID, Type: "pulseSummary", SourceType: "pulse", SourceID: end.Format(time.RFC3339), Category: "pulse", GroupKey: id, OccurrenceCount: count, Actor: domain.User{ID: "flow", Name: "Flow", DisplayName: "Flow"}, CreatedAt: now, UpdatedAt: now}
					data.Notifications = append(data.Notifications, notification)
					enqueueNotificationDeliveries(data, notification, preferences)
				}
			}
			current[user.ID] = end
			if data.Settings == nil {
				data.Settings = map[string]any{}
			}
			data.Settings["pulseDeliveryCursors"] = current
			return nil
		})
		if err != nil {
			return err
		}
		processed++
		if processed >= 100 {
			break
		}
	}
	return nil
}
