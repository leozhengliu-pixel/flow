package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type recurringIssueListResponse struct {
	Issues []struct {
		domain.Issue
		SubIssueCount int `json:"subIssueCount"`
	} `json:"issues"`
}

type recurringIssueCreateResponse struct {
	Issue     domain.Issue   `json:"issue"`
	SubIssues []domain.Issue `json:"subIssues"`
}

func TestTeamRecurringIssueEndpoints(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "recurring-api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	srv := &server{store: repository, uploadPath: t.TempDir(), realtime: newRealtimeHub()}
	repository.SetRealtimeSink(srv.publishRealtime)
	httpServer := httptest.NewServer(newHandler(srv))
	defer httpServer.Close()
	base := httpServer.URL

	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, base+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, base+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	teamID := bootstrap.Teams[0].ID
	var labelID string
	for _, label := range bootstrap.Labels {
		if label.ResourceType == "issue" && label.ArchivedAt == nil && (label.Scope == "" || label.Scope == "Workspace" || label.Scope == teamID) {
			labelID = label.ID
			break
		}
	}
	member := inviteTeamPermissionActor(t, base, admin, "Team member", "recurring-member@example.test", "member", []string{teamID})
	outsider := inviteTeamPermissionActor(t, base, admin, "Outsider", "recurring-outsider@example.test", "member", nil)
	listURL := func(team string) string { return base + "/api/teams/" + team + "/recurring-issues" }
	list := func(client *http.Client, team string) recurringIssueListResponse {
		t.Helper()
		return authRequest[recurringIssueListResponse](t, client, http.MethodGet, listURL(team), nil, "test-workspace", http.StatusOK)
	}
	if initial := list(admin, teamID); len(initial.Issues) != 0 {
		t.Fatalf("fixture has recurring issues: %#v", initial.Issues)
	}

	// Validation.
	for _, input := range []map[string]any{
		{"title": "", "dueDate": "2026-10-10", "recurrence": "FREQ=WEEKLY"},
		{"title": "No due", "recurrence": "FREQ=WEEKLY"},
		{"title": "Bad due", "dueDate": "10/10/2026", "recurrence": "FREQ=WEEKLY"},
		{"title": "No schedule", "dueDate": "2026-10-10"},
		{"title": "Bad schedule", "dueDate": "2026-10-10", "recurrence": "FREQ=HOURLY"},
		{"title": "Empty sub-issue", "dueDate": "2026-10-10", "recurrence": "FREQ=WEEKLY", "subIssues": []map[string]any{{"title": " "}}},
	} {
		authRequest[any](t, admin, http.MethodPost, listURL(teamID), input, "test-workspace", http.StatusBadRequest)
	}
	// Atomic: an invalid sub-issue creates nothing at all.
	authRequest[any](t, admin, http.MethodPost, listURL(teamID), map[string]any{
		"title": "Half created", "dueDate": "2026-10-10", "recurrence": "FREQ=WEEKLY",
		"subIssues": []map[string]any{{"title": "Valid"}, {"title": "Invalid", "labelIds": []string{"label_missing"}}},
	}, "test-workspace", http.StatusBadRequest)
	data, _ := repository.BootstrapFor("test-workspace")
	if slices.ContainsFunc(data.Issues, func(issue domain.Issue) bool { return issue.Title == "Half created" || issue.Title == "Valid" }) {
		t.Fatal("a rejected recurring issue left records behind")
	}

	// Create: the first instance and its sub-issues, due on the first due date.
	created := authRequest[recurringIssueCreateResponse](t, admin, http.MethodPost, listURL(teamID), map[string]any{
		"title": "Weekly report", "description": "Send the numbers", "priority": 2, "icon": "Repeat", "labelIds": []string{labelID},
		"dueDate": "2026-10-14", "recurrence": "FREQ=WEEKLY;INTERVAL=2", "assigneeId": "usr_member",
		"subIssues": []map[string]any{{"title": "Collect numbers", "priority": 3}, {"title": "Write summary", "assigneeId": "usr_member"}},
	}, "test-workspace", http.StatusCreated)
	issue := created.Issue
	if issue.ID == "" || issue.Title != "Weekly report" || issue.Icon != "Repeat" || issue.Priority != 2 || optionalID(issue.DueDate) != "2026-10-14" || issue.Assignee == nil || issue.Assignee.ID != "usr_member" {
		t.Fatalf("created issue = %#v", issue)
	}
	if issue.Recurrence != "FREQ=WEEKLY;INTERVAL=2;BYDAY=WE" || issue.NextOccurrenceAt == nil || !issue.NextOccurrenceAt.Equal(time.Date(2026, 10, 15, 0, 1, 0, 0, time.UTC)) {
		t.Fatalf("schedule = %q next %v", issue.Recurrence, issue.NextOccurrenceAt)
	}
	if len(issue.Labels) != 1 || issue.Labels[0].ID != labelID {
		t.Fatalf("labels = %#v", issue.Labels)
	}
	if len(created.SubIssues) != 2 || created.SubIssues[0].Title != "Collect numbers" || created.SubIssues[0].Priority != 3 || created.SubIssues[1].Assignee == nil || !slices.Equal(issue.SubIssueIDs, []string{created.SubIssues[0].ID, created.SubIssues[1].ID}) {
		t.Fatalf("sub-issues = %#v (ids %v)", created.SubIssues, issue.SubIssueIDs)
	}
	for _, child := range created.SubIssues {
		if child.ParentID == nil || *child.ParentID != issue.ID || child.Recurrence != "" {
			t.Fatalf("sub-issue = %#v", child)
		}
	}
	srv.realtime.mu.Lock()
	events := slices.Clone(srv.realtime.history["test-workspace"])
	srv.realtime.mu.Unlock()
	for _, id := range []string{issue.ID, created.SubIssues[0].ID, created.SubIssues[1].ID} {
		if !slices.ContainsFunc(events, func(event domain.RealtimeEvent) bool {
			var payload struct {
				Entity domain.Issue `json:"entity"`
			}
			return event.Type == "issue.created" && event.AggregateID == id && json.Unmarshal(event.Payload, &payload) == nil && payload.Entity.ID == id
		}) {
			t.Fatalf("no issue.created realtime event for %s", id)
		}
	}

	// A second recurring issue due earlier, and an archived one.
	earlier := authRequest[recurringIssueCreateResponse](t, member.client, http.MethodPost, listURL(teamID), map[string]any{
		"title": "Daily standup notes", "dueDate": "2026-10-05", "recurrence": "daily",
	}, "test-workspace", http.StatusCreated).Issue
	archived := authRequest[recurringIssueCreateResponse](t, admin, http.MethodPost, listURL(teamID), map[string]any{
		"title": "Archived schedule", "dueDate": "2026-10-06", "recurrence": "FREQ=MONTHLY",
	}, "test-workspace", http.StatusCreated).Issue
	authRequest[domain.Issue](t, admin, http.MethodPatch, base+"/api/issue-records/"+archived.ID, map[string]any{"archived": true}, "test-workspace", http.StatusOK)

	listed := list(member.client, teamID)
	if len(listed.Issues) != 2 || listed.Issues[0].ID != earlier.ID || listed.Issues[1].ID != issue.ID || listed.Issues[1].SubIssueCount != 2 || listed.Issues[0].SubIssueCount != 0 || listed.Issues[1].Icon != "Repeat" {
		t.Fatalf("recurring issues = %#v", listed.Issues)
	}

	// Editing goes through the issue PATCH: a new due date moves the schedule.
	moved := authRequest[domain.Issue](t, admin, http.MethodPatch, base+"/api/issue-records/"+issue.ID, map[string]any{"dueDate": "2026-10-16"}, "test-workspace", http.StatusOK)
	if moved.Recurrence != "FREQ=WEEKLY;INTERVAL=2;BYDAY=FR" || !moved.NextOccurrenceAt.Equal(time.Date(2026, 10, 17, 0, 1, 0, 0, time.UTC)) {
		t.Fatalf("moved schedule = %q next %v", moved.Recurrence, moved.NextOccurrenceAt)
	}
	// Stopping keeps the due date and removes it from the list.
	stopped := authRequest[domain.Issue](t, admin, http.MethodPatch, base+"/api/issue-records/"+earlier.ID, map[string]any{"recurrence": ""}, "test-workspace", http.StatusOK)
	if stopped.Recurrence != "" || stopped.NextOccurrenceAt != nil || optionalID(stopped.DueDate) != "2026-10-05" {
		t.Fatalf("stopped = %q %v due %v", stopped.Recurrence, stopped.NextOccurrenceAt, stopped.DueDate)
	}
	if listed := list(admin, teamID); len(listed.Issues) != 1 || listed.Issues[0].ID != issue.ID {
		t.Fatalf("after stop = %#v", listed.Issues)
	}

	// Permissions: templatePermission ("Who can manage team templates and
	// recurring issues") gates creation; reading follows team visibility.
	authRequest[domain.TeamSettings](t, admin, http.MethodPatch, base+"/api/teams/"+teamID+"/settings", map[string]any{"templatePermission": "owners"}, "test-workspace", http.StatusOK)
	authRequest[any](t, member.client, http.MethodPost, listURL(teamID), map[string]any{"title": "Denied", "dueDate": "2026-10-10", "recurrence": "daily"}, "test-workspace", http.StatusForbidden)
	authRequest[any](t, outsider.client, http.MethodPost, listURL(teamID), map[string]any{"title": "Denied", "dueDate": "2026-10-10", "recurrence": "daily"}, "test-workspace", http.StatusForbidden)
	authRequest[recurringIssueCreateResponse](t, admin, http.MethodPost, listURL(teamID), map[string]any{"title": "Admin allowed", "dueDate": "2026-10-10", "recurrence": "daily"}, "test-workspace", http.StatusCreated)

	private := authRequest[domain.Team](t, admin, http.MethodPost, base+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Private recurring", "key": "PRVR", "private": true}, "", http.StatusCreated)
	secret := authRequest[recurringIssueCreateResponse](t, admin, http.MethodPost, listURL(private.ID), map[string]any{"title": "Secret schedule", "dueDate": "2026-10-10", "recurrence": "weekly"}, "test-workspace", http.StatusCreated).Issue
	if listed := list(admin, private.ID); len(listed.Issues) != 1 || listed.Issues[0].ID != secret.ID {
		t.Fatalf("private team list = %#v", listed.Issues)
	}
	authRequest[any](t, outsider.client, http.MethodGet, listURL(private.ID), nil, "test-workspace", http.StatusForbidden)
	authRequest[any](t, outsider.client, http.MethodPost, listURL(private.ID), map[string]any{"title": "Denied", "dueDate": "2026-10-10", "recurrence": "daily"}, "test-workspace", http.StatusForbidden)
	authRequest[any](t, admin, http.MethodGet, listURL("team_missing"), nil, "test-workspace", http.StatusForbidden)
}
