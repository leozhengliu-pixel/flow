package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// TestMutationFastPathsMatchFullPath runs everyday project, release,
// document, cycle and initiative edits twice — once through the
// metadata-only and scoped mutation paths, once forced through the full
// workspace path — and requires the same persisted workspace, realtime
// payloads and domain events (including webhook previous values).
func TestMutationFastPathsMatchFullPath(t *testing.T) {
	fast := runMutationScript(t, false)
	full := runMutationScript(t, true)
	if len(fast.events) < 20 || len(fast.webhooks) < 20 {
		t.Fatalf("captured %d realtime and %d webhook events; the sinks are not wired", len(fast.events), len(fast.webhooks))
	}
	if len(fast.events) != len(full.events) {
		t.Fatalf("realtime events: fast %d, full %d\nfast=%v\nfull=%v", len(fast.events), len(full.events), fast.events, full.events)
	}
	for index := range fast.events {
		if fast.events[index] != full.events[index] {
			t.Errorf("realtime event %d differs:\nfast %s\nfull %s", index, fast.events[index], full.events[index])
		}
	}
	if len(fast.webhooks) != len(full.webhooks) {
		t.Fatalf("webhook events: fast %d, full %d", len(fast.webhooks), len(full.webhooks))
	}
	for index := range fast.webhooks {
		if fast.webhooks[index] != full.webhooks[index] {
			t.Errorf("webhook event %d differs:\nfast %s\nfull %s", index, fast.webhooks[index], full.webhooks[index])
		}
	}
	for key, value := range full.state {
		if fast.state[key] != value {
			t.Errorf("bootstrap %q differs:\nfast %.2000s\nfull %.2000s", key, fast.state[key], value)
		}
	}
	if fast.remindersArchived != 2 {
		t.Fatalf("archived %d seeded update reminders, want 2", fast.remindersArchived)
	}
	if !fast.automated {
		t.Fatal("release completion automation did not move the release issue")
	}
}

type mutationScriptResult struct {
	events            []string
	webhooks          []string
	state             map[string]string
	automated         bool
	remindersArchived int
	cleaned           bool
}

func runMutationScript(t *testing.T, forceFull bool) mutationScriptResult {
	t.Helper()
	restore := store.ForceFullMutationsForTesting(forceFull)
	defer restore()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	result := mutationScriptResult{state: map[string]string{}}
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	// newHandler installs the server's sinks; capture the store's output instead.
	repository.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		result.events = append(result.events, event.Type+" "+mutationNanoID.ReplaceAllString(event.AggregateID, "<n>")+" "+normalizeMutationJSON(event.Payload, true))
	})
	repository.SetWebhookSink(func(_ string, event domain.DomainEvent) {
		result.webhooks = append(result.webhooks, event.Type+" "+mutationNanoID.ReplaceAllString(event.AggregateID, "<n>")+" "+normalizeMutationJSON(event.Payload, false)+" "+normalizeMutationJSON(event.PreviousValues, false))
	})
	call := func(method, path string, input any, want int) map[string]any {
		t.Helper()
		value := requestJSON[any](t, handler, method, path, input, want)
		object, _ := value.(map[string]any)
		return object
	}
	id := func(value map[string]any) string { text, _ := value["id"].(string); return text }
	fixture := repository.Bootstrap()
	team, project, user := fixture.Teams[0], fixture.Projects[0], fixture.Users[0]
	doneState := ""
	for _, state := range requestJSON[[]domain.WorkflowState](t, handler, http.MethodGet, "/api/teams/"+team.ID+"/states", nil, http.StatusOK) {
		if state.Type == "completed" && doneState == "" {
			doneState = state.ID
		}
	}
	// An event-triggered loop makes every mutation compute webhook previous values.
	call(http.MethodPost, "/api/loops", map[string]any{"name": "Watcher", "status": "published", "triggerType": "issue", "triggerConfig": map[string]any{"event": "created"}, "instructions": "Watch"}, http.StatusCreated)
	// Pending update reminders that posting an update, and later turning
	// reminders off, must archive.
	seedReminder := func(id, kind string) {
		t.Helper()
		if err := repository.MutateWorkspace(context.Background(), fixture.Workspace.URLKey, "test.reminder_seeded", project.ID, nil, func(data *domain.Bootstrap) error {
			now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			data.Notifications = append(data.Notifications, domain.Notification{ID: id, RecipientID: user.ID, Type: kind, SourceType: "project", SourceID: project.ID, ProjectID: project.ID, Actor: user, Category: "reminders", GroupKey: id, OccurrenceCount: 1, LatestActorIDs: []string{user.ID}, CreatedAt: now, UpdatedAt: now})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	seedReminder("notification_reminder_seed", "projectUpdateReminder")
	call(http.MethodPatch, "/api/teams/"+team.ID+"/settings", map[string]any{"releaseAutomations": []map[string]any{{"id": "rule_done", "name": "Done", "trigger": "*", "action": doneState, "enabled": true}}}, http.StatusOK)

	projectPath := "/api/projects/" + project.ID
	call(http.MethodPatch, "/api/account/settings", map[string]any{"fontSize": "small", "homeView": "inbox"}, http.StatusOK)
	call(http.MethodPatch, "/api/account/settings", map[string]any{"fontSize": "large"}, http.StatusOK)
	call(http.MethodPatch, projectPath, map[string]any{"icon": "Rocket", "color": "#4ea7fc"}, http.StatusOK)
	milestone := call(http.MethodPost, projectPath+"/milestones", map[string]any{"name": "Beta"}, http.StatusCreated)
	issue := call(http.MethodPost, "/api/issues", map[string]any{"title": "Scoped", "teamId": team.ID, "projectId": project.ID, "projectMilestoneId": id(milestone)}, http.StatusCreated)
	other := call(http.MethodPost, "/api/issues", map[string]any{"title": "Other", "teamId": team.ID}, http.StatusCreated)
	call(http.MethodDelete, projectPath+"/milestones/"+id(milestone), nil, http.StatusNoContent)
	update := call(http.MethodPost, projectPath+"/updates", map[string]any{"body": "On track", "health": "onTrack"}, http.StatusCreated)
	if stored, _ := repository.BootstrapFor(fixture.Workspace.URLKey); !slices.ContainsFunc(stored.Notifications, func(item domain.Notification) bool {
		return item.ID == "notification_reminder_seed" && item.ArchivedAt != nil
	}) {
		t.Fatal("posting a project update did not archive the pending reminder")
	}
	call(http.MethodPatch, projectPath+"/updates/"+id(update), map[string]any{"body": "On track (edited)"}, http.StatusOK)
	call(http.MethodPost, projectPath+"/updates/"+id(update)+"/comments", map[string]any{"body": "Nice"}, http.StatusCreated)
	call(http.MethodPost, projectPath+"/updates/"+id(update)+"/reactions", map[string]any{"emoji": "👍"}, http.StatusOK)
	comment := call(http.MethodPost, projectPath+"/comments", map[string]any{"body": "Looks good"}, http.StatusCreated)
	call(http.MethodPatch, projectPath+"/comments/"+id(comment), map[string]any{"body": "Looks great"}, http.StatusOK)
	call(http.MethodPost, projectPath+"/comments/"+id(comment)+"/reactions", map[string]any{"emoji": "🎉"}, http.StatusOK)
	seedReminder("notification_due_reminder_seed", "projectUpdateDueReminder")
	call(http.MethodPatch, projectPath, map[string]any{"updateSchedule": map[string]any{"mode": "never", "frequencyDays": 7, "weekday": 1, "hour": 9, "timezone": "UTC"}}, http.StatusOK)
	document := call(http.MethodPost, "/api/documents", map[string]any{"title": "Spec", "issueId": id(issue)}, http.StatusCreated)
	call(http.MethodPatch, "/api/documents/"+id(document), map[string]any{"content": "Hello", "issueId": id(other)}, http.StatusOK)
	requestJSON[any](t, handler, http.MethodPatch, "/api/documents/"+id(document), map[string]any{"issueId": "issue_missing"}, http.StatusBadRequest)
	if len(fixture.Cycles) > 0 {
		call(http.MethodPatch, "/api/cycles/"+fixture.Cycles[0].ID, map[string]any{"description": "Focus"}, http.StatusOK)
	}
	initiative := call(http.MethodPost, "/api/initiatives", map[string]any{"name": "North star"}, http.StatusCreated)
	call(http.MethodPatch, "/api/initiatives/"+id(initiative), map[string]any{"summary": "Ship", "projectIds": []string{project.ID}}, http.StatusOK)

	pipeline := call(http.MethodPost, "/api/release-pipelines", map[string]any{"name": "Production"}, http.StatusCreated)
	release := call(http.MethodPost, "/api/releases", map[string]any{"name": "1.0", "pipelineId": id(pipeline), "issueIds": []string{id(issue)}}, http.StatusCreated)
	releasePath := "/api/releases/" + id(release)
	call(http.MethodPatch, releasePath, map[string]any{"name": "1.0.0", "targetDate": "2026-12-15"}, http.StatusOK)
	call(http.MethodPatch, releasePath, map[string]any{"issueIds": []string{id(issue), id(other)}}, http.StatusOK)
	requestJSON[any](t, handler, http.MethodPatch, releasePath, map[string]any{"issueIds": []string{"issue_missing"}}, http.StatusBadRequest)
	call(http.MethodPatch, releasePath, map[string]any{"stage": "In Progress"}, http.StatusOK)
	call(http.MethodPost, releasePath+"/notes", map[string]any{"title": "Notes", "body": "Body"}, http.StatusCreated)
	call(http.MethodPatch, releasePath, map[string]any{"stage": "Released"}, http.StatusOK)
	second := call(http.MethodPost, "/api/releases", map[string]any{"name": "1.1", "pipelineId": id(pipeline)}, http.StatusCreated)
	call(http.MethodDelete, "/api/releases/"+id(second), nil, http.StatusNoContent)

	moved := requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+id(issue), nil, http.StatusOK)
	result.automated = moved.State.ID == doneState
	bootstrap := requestJSON[map[string]json.RawMessage](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	for key, raw := range bootstrap {
		result.state[key] = normalizeMutationJSON(raw, false)
	}
	// The HTTP bootstrap filters the inbox to the viewer; compare every
	// stored record-backed collection as well.
	stored, ok := repository.BootstrapFor(fixture.Workspace.URLKey)
	if !ok {
		t.Fatal("workspace missing")
	}
	for key, value := range map[string]any{"store.notifications": stored.Notifications, "store.deliveries": stored.NotificationDeliveries, "store.activities": stored.Activities, "store.comments": stored.Comments, "store.issues": stored.Issues} {
		raw, _ := json.Marshal(value)
		result.state[key] = normalizeMutationJSON(raw, false)
	}
	archived := 0
	for _, notification := range stored.Notifications {
		if strings.HasPrefix(notification.ID, "notification_") && strings.HasSuffix(notification.ID, "reminder_seed") && notification.ArchivedAt != nil {
			archived++
		}
	}
	result.remindersArchived = archived
	return result
}

var (
	mutationTimestamp = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$`)
	mutationNanoID    = regexp.MustCompile(`\d{12,}`)
)

// normalizeMutationJSON removes values that legitimately differ between two
// runs (clock readings and nanosecond ids). Realtime payloads also drop
// denormalized issue counts, which are recomputed on every read.
func normalizeMutationJSON(raw []byte, dropCounts bool) string {
	if len(raw) == 0 {
		return ""
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	var walk func(any, string) any
	walk = func(value any, key string) any {
		switch typed := value.(type) {
		case map[string]any:
			result := map[string]any{}
			for field, item := range typed {
				// Weekly progress histories are refreshed by issue writes
				// and workspace reloads; project.updated has always skipped
				// them on its metadata-only path.
				if field == "slugId" || strings.HasSuffix(field, "History") || dropCounts && field == "issueCount" {
					continue
				}
				result[mutationNanoID.ReplaceAllString(field, "<n>")] = walk(item, field)
			}
			return result
		case []any:
			items := make([]any, len(typed))
			for index, item := range typed {
				items[index] = walk(item, key)
			}
			if key == "notifications" || key == "notificationDeliveries" || key == "auditLog" {
				sort.Slice(items, func(i, j int) bool { return fmt.Sprint(items[i]) < fmt.Sprint(items[j]) })
			}
			return items
		case string:
			if mutationTimestamp.MatchString(typed) {
				return "<time>"
			}
			return mutationNanoID.ReplaceAllString(typed, "<n>")
		}
		return value
	}
	encoded, _ := json.Marshal(walk(value, ""))
	return strings.TrimSpace(string(encoded))
}

// Page loads run the schedule maintenance. When a reminder cannot be
// delivered the trigger stays true; the job must then leave the workspace (and
// every other client's realtime stream) alone instead of rewriting it on each
// load.
func TestScheduleMaintenanceSkipsNoOpWrites(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	maintained := 0
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	repository.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		if event.Type == "schedules.maintained" {
			maintained++
		}
	})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	project := bootstrap.Projects[0]
	requestJSON[map[string]any](t, handler, http.MethodPut, "/api/project-update-settings", map[string]any{"cadenceDays": 1}, http.StatusOK)
	if err := repository.MutateWorkspace(context.Background(), bootstrap.Workspace.URLKey, "test.project_aged", project.ID, nil, func(data *domain.Bootstrap) error {
		for index := range data.Projects {
			if data.Projects[index].ID == project.ID {
				data.Projects[index].CreatedAt = time.Now().UTC().Add(-72 * time.Hour)
				data.Projects[index].Health = "onTrack"
			}
		}
		delete(data.ProjectUpdates, project.ID)
		if data.NotificationPreferences == nil {
			data.NotificationPreferences = map[string]domain.NotificationPreferences{}
		}
		for _, user := range data.Users {
			preferences := data.NotificationPreferences[user.ID]
			preferences.UserID = user.ID
			preferences.Inbox.Enabled = false
			data.NotificationPreferences[user.ID] = preferences
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if maintained != 1 {
		t.Fatalf("first maintenance published %d events, want 1 (health %q)", maintained, projectFromStore(t, repository, project.ID).Health)
	}
	stored := projectFromStore(t, repository, project.ID)
	if stored.Health != "noUpdate" {
		t.Fatalf("overdue project health = %q", stored.Health)
	}
	for range 3 {
		requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	}
	if maintained != 1 {
		t.Fatalf("no-op maintenance kept publishing: %d events", maintained)
	}
}

// Personal settings writes rewrite only the actor's settings record; they
// must survive a restart like any other workspace write.
func TestUserSettingsRecordWritePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	repository, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	events := 0
	repository.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		if event.Type == "user_settings.updated" {
			events++
		}
	})
	updated := requestJSON[domain.UserSettings](t, handler, http.MethodPatch, "/api/account/settings", map[string]any{"fontSize": "large", "homeView": "inbox"}, http.StatusOK)
	if updated.FontSize != "large" || updated.HomeView != "inbox" || events != 1 {
		t.Fatalf("settings = %+v, events = %d", updated, events)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	repository, err = store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	data, _ := repository.WorkspaceMetadata("test-workspace")
	if settings := data.UserSettings[updated.UserID]; settings.FontSize != "large" || settings.HomeView != "inbox" {
		t.Fatalf("settings after restart = %+v", settings)
	}
}
