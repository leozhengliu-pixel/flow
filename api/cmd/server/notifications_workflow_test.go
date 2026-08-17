package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestNotificationRoutingMentionsAggregationAndPreferences(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	data := repository.Bootstrap()
	if len(data.Users) < 2 || len(data.Issues) == 0 {
		t.Fatal("seed needs two users and an issue")
	}
	actor, recipient := data.Users[0], data.Users[1]
	issue := data.Issues[0]
	issue.SubscriberIDs = []string{recipient.ID}
	data.NotificationPreferences = map[string]domain.NotificationPreferences{recipient.ID: defaultPreferences(recipient.ID)}
	data.Notifications = nil
	data.NotificationDeliveries = nil

	activity := domain.ActivityEvent{ID: "activity_assignment", Type: "issue.updated", Actor: actor, CreatedAt: time.Now().UTC(), Metadata: map[string]string{"assignee": recipient.ID, "previousAssignee": actor.ID}}
	appendIssueNotifications(&data, issue, activity, nil)
	assignment := slices.IndexFunc(data.Notifications, func(item domain.Notification) bool {
		return item.RecipientID == recipient.ID && item.Category == "assignments"
	})
	if assignment < 0 {
		t.Fatalf("new assignee did not receive assignment: %#v", data.Notifications)
	}
	if slices.ContainsFunc(data.Notifications, func(item domain.Notification) bool { return item.RecipientID == actor.ID }) {
		t.Fatal("actor received their own notification")
	}
	if !slices.ContainsFunc(data.NotificationDeliveries, func(item domain.NotificationDelivery) bool {
		return item.NotificationID == data.Notifications[assignment].ID && item.Channel == "desktop"
	}) {
		t.Fatal("desktop delivery was not queued")
	}

	comment := domain.Comment{ID: "comment_mention", Body: "Please review", BodyData: map[string]any{"type": "doc", "content": []any{map[string]any{"type": "mention", "userId": recipient.ID}}}, User: actor, CreatedAt: activity.CreatedAt.Add(time.Minute)}
	activity = domain.ActivityEvent{ID: "activity_comment", Type: "comment.created", Actor: actor, CreatedAt: comment.CreatedAt, Metadata: map[string]string{"commentId": comment.ID}}
	appendIssueNotifications(&data, issue, activity, &comment)
	mention := slices.IndexFunc(data.Notifications, func(item domain.Notification) bool { return item.Category == "mentions" })
	if mention < 0 || data.Notifications[mention].OccurrenceCount != 1 {
		t.Fatalf("structured mention missing: %#v", data.Notifications)
	}
	comment.ID, activity.ID, comment.CreatedAt, activity.CreatedAt = "comment_mention_2", "activity_comment_2", comment.CreatedAt.Add(time.Minute), activity.CreatedAt.Add(time.Minute)
	appendIssueNotifications(&data, issue, activity, &comment)
	if data.Notifications[mention].OccurrenceCount != 2 {
		t.Fatalf("mention did not aggregate: %#v", data.Notifications[mention])
	}

	prefs := data.NotificationPreferences[recipient.ID]
	prefs.Inbox.Categories["mentions"] = false
	data.NotificationPreferences[recipient.ID] = prefs
	comment.ID, activity.ID, comment.CreatedAt, activity.CreatedAt = "comment_mention_3", "activity_comment_3", comment.CreatedAt.Add(time.Minute), activity.CreatedAt.Add(time.Minute)
	appendIssueNotifications(&data, issue, activity, &comment)
	if data.Notifications[mention].OccurrenceCount != 2 {
		t.Fatal("disabled mention category still generated an inbox occurrence")
	}
}

func TestNotificationPreferencesAndBatchCleanupAPI(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	prefs := requestJSON[domain.NotificationPreferences](t, handler, http.MethodGet, "/api/notification-preferences", nil, http.StatusOK)
	prefs.Email.Enabled = false
	prefs.Email.Categories["comments"] = false
	updated := requestJSON[domain.NotificationPreferences](t, handler, http.MethodPatch, "/api/notification-preferences", prefs, http.StatusOK)
	if updated.Email.Enabled || updated.Email.Categories["comments"] {
		t.Fatalf("preferences did not persist: %#v", updated)
	}
	result := requestJSON[map[string]int](t, handler, http.MethodPost, "/api/notifications/batch", map[string]any{"action": "deleteAll"}, http.StatusOK)
	if result["updated"] == 0 {
		t.Fatal("bulk delete did not update any notifications")
	}
	listed := requestJSON[domain.NotificationList](t, handler, http.MethodGet, "/api/notifications", nil, http.StatusOK)
	if len(listed.Notifications) != 0 {
		t.Fatalf("bulk-deleted notifications remain visible: %#v", listed.Notifications)
	}
}

func TestWorkflowStateConstraintsAndTeamDefaults(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	created := requestJSON[domain.WorkflowState](t, handler, http.MethodPost, "/api/teams/team_cleantrack/states", map[string]any{"name": "Ready for review", "type": "started", "color": "#4f8cff", "default": true}, http.StatusCreated)
	if created.TeamID != "team_cleantrack" || !created.Default {
		t.Fatalf("custom team state = %#v", created)
	}
	settings := requestJSON[domain.TeamSettings](t, handler, http.MethodGet, "/api/teams/team_cleantrack/settings", nil, http.StatusOK)
	if settings.DefaultStateID != created.ID {
		t.Fatalf("default state = %q, want %q", settings.DefaultStateID, created.ID)
	}
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Uses team default", "teamId": "team_cleantrack"}, http.StatusCreated)
	if issue.State.ID != created.ID {
		t.Fatalf("new issue state = %q, want default %q", issue.State.ID, created.ID)
	}

	states := requestJSON[[]domain.WorkflowState](t, handler, http.MethodGet, "/api/teams/team_cleantrack/states", nil, http.StatusOK)
	reserved := states[slices.IndexFunc(states, func(item domain.WorkflowState) bool { return item.Reserved })]
	requestJSON[any](t, handler, http.MethodDelete, "/api/teams/team_cleantrack/states/"+reserved.ID, map[string]any{}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodDelete, "/api/teams/team_cleantrack/states/"+created.ID, map[string]any{}, http.StatusBadRequest)

	patched := requestJSON[domain.TeamSettings](t, handler, http.MethodPatch, "/api/teams/team_cleantrack/settings", map[string]any{"identifier": "OPS", "defaultPriority": 2}, http.StatusOK)
	if patched.DefaultPriority != 2 {
		t.Fatalf("default priority = %d", patched.DefaultPriority)
	}
	newIssue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Uses identifier", "teamId": "team_cleantrack"}, http.StatusCreated)
	if newIssue.Identifier[:4] != "OPS-" || newIssue.Priority != 2 {
		t.Fatalf("new issue did not use team defaults: %#v", newIssue)
	}
	if issue.Identifier[:4] == "OPS-" {
		t.Fatal("changing team identifier rewrote an existing issue identifier")
	}
}
