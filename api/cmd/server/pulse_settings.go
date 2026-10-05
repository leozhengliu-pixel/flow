package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// pulseDeliveryHour is the local hour Pulse summaries arrive (Linear sends
// them in the morning, around 6 AM in the member's time zone).
const pulseDeliveryHour = 6

// pulseSummaryRefLimit caps the update references stored on one summary
// notification; OccurrenceCount and Payload.Total keep the full count.
const pulseSummaryRefLimit = 100

// pulseWindow reports the summary window ending at today's 06:00 in zone.
// Daily summaries arrive every day, weekly summaries on Mondays.
func pulseWindow(schedule string, now time.Time, zone *time.Location) (time.Time, time.Time, bool) {
	local := now.In(zone)
	end := time.Date(local.Year(), local.Month(), local.Day(), pulseDeliveryHour, 0, 0, 0, zone)
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

// effectivePulseSchedule resolves a personal schedule: "" (legacy) and
// "default" follow the workspace default.
func effectivePulseSchedule(personal, workspaceDefault string) string {
	if personal == "" || personal == "default" {
		return workspaceDefault
	}
	return personal
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
	eligible   bool
	role       string
	refs       []domain.PulseUpdateRef
	total      int
}

type pulseZones map[string]*time.Location

func (zones pulseZones) load(name string) (*time.Location, bool) {
	if name == "" {
		return nil, false
	}
	if zone, ok := zones[name]; ok {
		return zone, zone != nil
	}
	zone, err := time.LoadLocation(name)
	if err != nil {
		zones[name] = nil
		return nil, false
	}
	zones[name] = zone
	return zone, true
}

// pulseUserZone is the member's browser time zone, else their first team's
// time zone, else UTC.
func pulseUserZone(zones pulseZones, settings domain.UserSettings, memberships []domain.TeamMember, teamSettings map[string]domain.TeamSettings) *time.Location {
	if zone, ok := zones.load(settings.Timezone); ok {
		return zone
	}
	for _, membership := range memberships {
		if zone, ok := zones.load(teamSettings[membership.TeamID].Timezone); ok {
			return zone
		}
	}
	return time.UTC
}

// pulseMaxWindow bounds how far back a summary reaches when the member's
// delivery cursor is very old (a schedule that was "never" for months).
const pulseMaxWindow = 31 * 24 * time.Hour

func (s *server) preparePulseSummaries(ctx context.Context, key string, now time.Time) error {
	metadata, ok := s.store.WorkspaceMetadataFields(key, "workspaceSettings")
	if !ok || !workspaceFeatureEnabled(metadata.WorkspaceSettings, "pulse") {
		return nil
	}
	metadata, ok = s.store.WorkspaceMetadataFields(key, "workspaceSettings", "settings", "userSettings", "teamSettings")
	if !ok {
		return nil
	}
	cursors := pulseCursors(&metadata)
	workspaceDefault := metadata.WorkspaceSettings.FeatureSettings.PulseWorkspaceSchedule
	// Every active member is a candidate, including invited members who never
	// wrote anything (the metadata user list only grows on writes).
	members, err := s.store.ListMembers(ctx, metadata.Workspace.ID)
	if err != nil {
		return err
	}
	zones := pulseZones{}
	roles := map[string]string{}
	candidates := []domain.User{}
	for _, member := range members {
		if member.Status != "active" {
			continue
		}
		user := member.User
		settings := metadata.UserSettings[user.ID]
		schedule := effectivePulseSchedule(settings.PulseSchedule, workspaceDefault)
		if schedule != "daily" && schedule != "weekly" {
			continue
		}
		// A member with a browser time zone is ruled out here when their
		// cursor already reaches today's window end; the others need their
		// team's zone below.
		if zone, ok := zones.load(settings.Timezone); ok {
			if _, end, due := pulseWindow(schedule, now, zone); !due || !cursors[user.ID].Before(end) {
				continue
			}
		}
		roles[user.ID] = member.Role
		candidates = append(candidates, user)
	}
	if len(candidates) == 0 {
		return nil
	}
	memberships, err := s.store.ListTeamMembers(ctx, metadata.Workspace.ID)
	if err != nil {
		return err
	}
	byUser := map[string][]domain.TeamMember{}
	for _, membership := range memberships {
		byUser[membership.UserID] = append(byUser[membership.UserID], membership)
	}
	due := []pulseDue{}
	for _, user := range candidates {
		settings := metadata.UserSettings[user.ID]
		schedule := effectivePulseSchedule(settings.PulseSchedule, workspaceDefault)
		zone := pulseUserZone(zones, settings, byUser[user.ID], metadata.TeamSettings)
		start, end, isDue := pulseWindow(schedule, now, zone)
		cursor := cursors[user.ID]
		if !isDue || !cursor.Before(end) {
			continue
		}
		// The window starts where the previous summary ended, so switching
		// from daily to weekly (or moving to another time zone) never drops
		// updates; a cursor older than pulseMaxWindow is clamped.
		if !cursor.IsZero() {
			start = cursor
			if limit := end.Add(-pulseMaxWindow); start.Before(limit) {
				start = limit
			}
		}
		role := roles[user.ID]
		// Guests never receive Pulse; app users neither.
		due = append(due, pulseDue{user: user, schedule: schedule, start: start, end: end, role: role, eligible: role != "guest" && role != "app" && !user.App})
		if len(due) >= pulseMaxUsersPerTick {
			break
		}
	}
	if len(due) == 0 {
		return nil
	}
	if snapshot, ok := s.store.PulseFeed(key); ok {
		rules := newPulseRules(snapshot)
		subscriptions := map[string][]domain.Subscription{}
		for _, subscription := range snapshot.Subscriptions {
			subscriptions[subscription.UserID] = append(subscriptions[subscription.UserID], subscription)
		}
		for index := range due {
			item := &due[index]
			if !item.eligible {
				continue
			}
			viewer := newPulseViewerFrom(snapshot, item.user.ID, item.role, byUser[item.user.ID], subscriptions[item.user.ID], nil)
			entries, total := rules.pulseForMeSince(viewer, item.start, item.end, pulseSummaryRefLimit)
			item.total = total
			for _, entry := range entries {
				item.refs = append(item.refs, domain.PulseUpdateRef{ID: entry.UpdateID, Kind: entry.Kind, SourceID: entry.SourceID, Source: rules.sourceName(entry.Kind, entry.SourceID), At: entry.CreatedAt})
			}
		}
	}
	// One write for every due member: cursors live in one settings value, so
	// per-member writes would each rewrite it.
	pulseCtx := store.WithMetadataFields(ctx, "settings", "workspaceSettings", "userSettings", "notificationPreferences", "pushSubscriptions")
	return s.store.MutateWorkspace(pulseCtx, key, "pulse.summary_scheduled", "", nil, func(data *domain.Bootstrap) error {
		if !workspaceFeatureEnabled(data.WorkspaceSettings, "pulse") {
			return store.ErrNoMutation
		}
		current := pulseCursors(data)
		changed := false
		for _, item := range due {
			user := item.user
			if !current[user.ID].Before(item.end) {
				continue
			}
			if effectivePulseSchedule(data.UserSettings[user.ID].PulseSchedule, data.WorkspaceSettings.FeatureSettings.PulseWorkspaceSchedule) != item.schedule {
				continue
			}
			preferences, ok := data.NotificationPreferences[user.ID]
			if !ok {
				preferences = defaultPreferences(user.ID)
			}
			if item.eligible && item.total > 0 && preferences.Inbox.Enabled && categoryEnabled(preferences.Inbox, "pulse") {
				data.Notifications = append(data.Notifications, newPulseSummaryNotification(item, now))
				enqueueNotificationDeliveries(data, data.Notifications[len(data.Notifications)-1], preferences)
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

func newPulseSummaryNotification(item pulseDue, now time.Time) domain.Notification {
	id := fmt.Sprintf("pulse_%s_%d", item.user.ID, item.end.Unix())
	start, end := item.start, item.end
	payload := &domain.NotificationPayload{Schedule: item.schedule, WindowStart: &start, WindowEnd: &end, Updates: item.refs, Total: item.total, UpdateIDs: make([]string, 0, len(item.refs))}
	for _, ref := range item.refs {
		payload.UpdateIDs = append(payload.UpdateIDs, ref.ID)
	}
	return domain.Notification{
		ID: id, RecipientID: item.user.ID, Type: "pulseSummary", SourceType: "pulse", SourceID: item.end.Format(time.RFC3339), Category: "pulse", GroupKey: id,
		OccurrenceCount: item.total, Actor: domain.User{ID: "flow", Name: "Flow", DisplayName: "Flow"}, LatestActorIDs: []string{},
		Title: pulseSummaryTitle(item.schedule), Text: pulseSummaryText(item.refs, item.total), Payload: payload, CreatedAt: now, UpdatedAt: now,
	}
}
