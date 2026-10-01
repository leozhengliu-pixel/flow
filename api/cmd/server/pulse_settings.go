package main

import (
	"context"
	"encoding/json"
	"fmt"
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

// pulseMaxUsersPerTick bounds one scheduler tick; later ticks pick up the rest.
const pulseMaxUsersPerTick = 1000

type pulseDue struct {
	user       domain.User
	schedule   string
	start, end time.Time
	count      int
}

func (s *server) preparePulseSummaries(ctx context.Context, key string, now time.Time) error {
	// Each tick reads only the fields the schedule check needs; the
	// per-viewer projection is built only when updates were posted, once per
	// group of viewers who see the same things.
	metadata, ok := s.store.WorkspaceMetadataFields(key, "workspaceSettings")
	if !ok || !workspaceFeatureEnabled(metadata.WorkspaceSettings, "pulse") {
		return nil
	}
	metadata, ok = s.store.WorkspaceMetadataFields(key, "workspaceSettings", "settings", "users", "userSettings", "teamMembers", "teamSettings", "projectUpdates", "initiativeUpdates")
	if !ok {
		return nil
	}
	cursors := pulseCursors(&metadata)
	firstTeam := map[string]string{}
	for _, membership := range metadata.TeamMembers {
		if _, seen := firstTeam[membership.UserID]; !seen {
			firstTeam[membership.UserID] = membership.TeamID
		}
	}
	zones := map[string]*time.Location{}
	due := []pulseDue{}
	for _, user := range metadata.Users {
		schedule := metadata.UserSettings[user.ID].PulseSchedule
		if schedule == "" || schedule == "default" {
			schedule = metadata.WorkspaceSettings.FeatureSettings.PulseWorkspaceSchedule
		}
		zone := time.UTC
		if teamID, ok := firstTeam[user.ID]; ok {
			name := metadata.TeamSettings[teamID].Timezone
			if cached, ok := zones[name]; ok {
				zone = cached
			} else if location, err := time.LoadLocation(name); err == nil {
				zones[name], zone = location, location
			}
		}
		start, end, isDue := pulseWindow(schedule, now, zone)
		if !isDue || !cursors[user.ID].Before(end) {
			continue
		}
		if cursors[user.ID].After(start) {
			start = cursors[user.ID]
		}
		due = append(due, pulseDue{user: user, schedule: schedule, start: start, end: end})
		if len(due) >= pulseMaxUsersPerTick {
			break
		}
	}
	if len(due) == 0 {
		return nil
	}
	// The visible count is zero when nothing was posted in the window, so the
	// projection (a clone of the workspace) is skipped entirely.
	posted := false
	for _, item := range due {
		if pulseUpdatesPosted(&metadata, item.start, item.end) {
			posted = true
			break
		}
	}
	if posted {
		visibility, err := s.store.ViewerVisibilityKeys(ctx, key)
		if err != nil {
			return err
		}
		groups := map[string][]int{}
		order := []string{}
		for index, item := range due {
			group, ok := visibility[item.user.ID]
			if !ok {
				continue // not an active member: nothing visible
			}
			if _, seen := groups[group]; !seen {
				order = append(order, group)
			}
			groups[group] = append(groups[group], index)
		}
		for _, group := range order {
			members := groups[group]
			view, err := s.store.PagedWorkspaceMetadata(ctx, key, due[members[0]].user.ID)
			if err != nil {
				continue
			}
			for _, index := range members {
				due[index].count = pulseVisibleUpdates(&view, due[index].start, due[index].end)
			}
		}
	}
	// One write for every due viewer: cursors live in one settings value, so
	// per-viewer writes would each rewrite it.
	pulseCtx := store.WithMetadataFields(ctx, "settings", "workspaceSettings", "userSettings", "notificationPreferences", "pushSubscriptions")
	return s.store.MutateWorkspace(pulseCtx, key, "pulse.summary_scheduled", "", nil, func(data *domain.Bootstrap) error {
		if !workspaceFeatureEnabled(data.WorkspaceSettings, "pulse") {
			return store.ErrNoMutation
		}
		current := pulseCursors(data)
		existing := map[string]bool{}
		for _, notification := range data.Notifications {
			if notification.Type == "pulseSummary" {
				existing[notification.ID] = true
			}
		}
		changed := false
		for _, item := range due {
			user := item.user
			if !current[user.ID].Before(item.end) {
				continue
			}
			currentSchedule := data.UserSettings[user.ID].PulseSchedule
			if currentSchedule == "" || currentSchedule == "default" {
				currentSchedule = data.WorkspaceSettings.FeatureSettings.PulseWorkspaceSchedule
			}
			if currentSchedule != item.schedule {
				continue
			}
			preferences, ok := data.NotificationPreferences[user.ID]
			if !ok {
				preferences = defaultPreferences(user.ID)
			}
			if item.count > 0 && preferences.Inbox.Enabled && categoryEnabled(preferences.Inbox, "pulse") {
				id := fmt.Sprintf("pulse_%s_%d", user.ID, item.end.Unix())
				if !existing[id] {
					notification := domain.Notification{ID: id, RecipientID: user.ID, Type: "pulseSummary", SourceType: "pulse", SourceID: item.end.Format(time.RFC3339), Category: "pulse", GroupKey: id, OccurrenceCount: item.count, Actor: domain.User{ID: "flow", Name: "Flow", DisplayName: "Flow"}, CreatedAt: now, UpdatedAt: now}
					data.Notifications = append(data.Notifications, notification)
					enqueueNotificationDeliveries(data, notification, preferences)
					existing[id] = true
				}
			}
			current[user.ID] = item.end
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

// pulseVisibleUpdates counts the updates a viewer's projection shows in the window.
func pulseVisibleUpdates(view *domain.Bootstrap, start, end time.Time) int {
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
	return count
}

func pulseUpdatesPosted(data *domain.Bootstrap, start, end time.Time) bool {
	within := func(at time.Time) bool { return at.After(start) && !at.After(end) }
	for _, updates := range data.ProjectUpdates {
		for _, update := range updates {
			if within(update.CreatedAt) {
				return true
			}
		}
	}
	for _, updates := range data.InitiativeUpdates {
		for _, update := range updates {
			if within(update.CreatedAt) {
				return true
			}
		}
	}
	return false
}
