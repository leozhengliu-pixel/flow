package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// TestTeamAdministrationFastPathsMatchFullPath runs team and membership
// administration (create, rename, parent/timezone, membership add/role/remove
// with issue cleanup, delete, restore) signed in and in development mode —
// once through the targeted paths, once forced through the full workspace
// path — and requires the same realtime events, webhook events and persisted
// workspace after a reopen.
func TestTeamAdministrationFastPathsMatchFullPath(t *testing.T) {
	for _, auth := range []bool{true, false} {
		fast := runTeamAdministrationScript(t, auth, false)
		full := runTeamAdministrationScript(t, auth, true)
		if len(fast.events) < 8 {
			t.Fatalf("auth=%v: captured %d realtime events; the sinks are not wired", auth, len(fast.events))
		}
		compareMutationLists(t, "realtime", fast.events, full.events)
		compareMutationLists(t, "webhook", fast.webhooks, full.webhooks)
		for key, value := range full.state {
			if fast.state[key] != value {
				t.Errorf("auth=%v: %q differs:\nfast %.3000s\nfull %.3000s", auth, key, fast.state[key], value)
			}
		}
		if auth && !fast.cleaned {
			t.Fatal("removing the member did not clear their team assignments and subscriptions")
		}
	}
}

func compareMutationLists(t *testing.T, kind string, fast, full []string) {
	t.Helper()
	if len(fast) != len(full) {
		t.Fatalf("%s events: fast %d, full %d\nfast=%v\nfull=%v", kind, len(fast), len(full), fast, full)
	}
	for index := range fast {
		if fast[index] != full[index] {
			t.Errorf("%s event %d differs:\nfast %s\nfull %s", kind, index, fast[index], full[index])
		}
	}
}

func runTeamAdministrationScript(t *testing.T, auth, forceFull bool) mutationScriptResult {
	t.Helper()
	restore := store.ForceFullMutationsForTesting(forceFull)
	defer restore()
	path := filepath.Join(t.TempDir(), "flow.db")
	repository, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	result := mutationScriptResult{state: map[string]string{}}
	if auth {
		t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	}
	api := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: !auth}))
	defer api.Close()
	repository.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		result.events = append(result.events, event.Type+" "+mutationNanoID.ReplaceAllString(event.AggregateID, "<n>")+" "+normalizeMutationJSON(event.Payload, true))
	})
	repository.SetWebhookSink(func(_ string, event domain.DomainEvent) {
		result.webhooks = append(result.webhooks, event.Type+" "+mutationNanoID.ReplaceAllString(event.AggregateID, "<n>")+" "+normalizeMutationJSON(event.Payload, false)+" "+normalizeMutationJSON(event.PreviousValues, false))
	})
	client := authClient(t)
	if auth {
		authRequest[domain.AuthSession](t, client, http.MethodPost, api.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	}
	call := func(method, path string, input any, want int) map[string]any {
		t.Helper()
		var body io.Reader
		if input != nil {
			raw, _ := json.Marshal(input)
			body = bytes.NewReader(raw)
		}
		request, _ := http.NewRequest(method, api.URL+path, body)
		request.Header.Set("X-Workspace-Key", "test-workspace")
		if input != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("%s %s: status %d want %d: %s", method, path, response.StatusCode, want, raw)
		}
		var object map[string]any
		_ = json.Unmarshal(raw, &object)
		return object
	}
	ws := "/api/workspaces/test-workspace"
	team := call(http.MethodPost, ws+"/teams", map[string]any{"name": "Platform", "key": "PLT"}, http.StatusCreated)
	teamID, _ := team["id"].(string)
	call(http.MethodPatch, ws+"/teams/"+teamID, map[string]any{"name": "Platform renamed"}, http.StatusOK)
	call(http.MethodPatch, "/api/teams/"+teamID+"/settings", map[string]any{"parentTeamId": "team_test"}, http.StatusOK)
	call(http.MethodPatch, "/api/teams/"+teamID+"/settings", map[string]any{"timezone": "Asia/Shanghai"}, http.StatusOK)
	call(http.MethodPatch, "/api/teams/"+teamID+"/settings", map[string]any{"parentTeamId": ""}, http.StatusOK)
	call(http.MethodPut, ws+"/teams/"+teamID+"/members/usr_member", map[string]any{"member": true, "role": "member"}, http.StatusNoContent)
	call(http.MethodPut, ws+"/teams/"+teamID+"/members/usr_member", map[string]any{"member": true, "role": "owner"}, http.StatusNoContent)
	call(http.MethodPut, ws+"/teams/"+teamID+"/members/usr_member", map[string]any{"member": true, "role": "member"}, http.StatusNoContent)
	call(http.MethodPut, ws+"/teams/"+teamID+"/members/usr_member", map[string]any{"member": false}, http.StatusNoContent)
	// Leaving team_test clears the member's assignment and subscriptions on
	// that team's issues (every fixture issue subscribes usr_member).
	call(http.MethodPatch, "/api/issue-records/issue_2", map[string]any{"assigneeId": "usr_member"}, http.StatusOK)
	call(http.MethodPut, ws+"/teams/team_test/members/usr_member", map[string]any{"member": true, "role": "member"}, http.StatusNoContent)
	call(http.MethodPut, ws+"/teams/team_test/members/usr_member", map[string]any{"member": false}, http.StatusNoContent)
	call(http.MethodDelete, ws+"/teams/"+teamID, nil, http.StatusNoContent)
	call(http.MethodPost, ws+"/deleted-teams/"+teamID+"/restore", nil, http.StatusOK)
	call(http.MethodDelete, ws+"/teams/"+teamID, nil, http.StatusNoContent)
	// Workspace settings and catalog writes that now persist as metadata deltas.
	call(http.MethodPut, "/api/workspace/project-display-default", map[string]any{"display": map[string]any{"layout": "board"}}, http.StatusOK)
	call(http.MethodPut, "/api/workspace/settings", map[string]any{"name": "Renamed workspace"}, http.StatusOK)
	call(http.MethodPatch, "/api/notification-preferences", map[string]any{"soundEnabled": false}, http.StatusOK)
	dashboard := call(http.MethodPost, "/api/dashboards", map[string]any{"name": "Health"}, http.StatusCreated)
	call(http.MethodPatch, "/api/dashboards/"+dashboard["id"].(string), map[string]any{"name": "Health v2"}, http.StatusOK)
	post := call(http.MethodPost, "/api/posts", map[string]any{"title": "Weekly", "body": "Notes"}, http.StatusCreated)
	call(http.MethodPatch, "/api/posts/"+post["id"].(string), map[string]any{"title": "Weekly v2"}, http.StatusOK)
	group := call(http.MethodPost, "/api/label-groups", map[string]any{"name": "Area"}, http.StatusCreated)
	call(http.MethodPatch, "/api/label-groups/"+group["id"].(string), map[string]any{"name": "Areas", "color": "#123456"}, http.StatusOK)
	// The first status edit materializes team-owned statuses (full path); the
	// second only recolors and renames one (no issue record changes).
	call(http.MethodPatch, "/api/teams/team_test/states/state_todo", map[string]any{"color": "#101010"}, http.StatusOK)
	call(http.MethodPatch, "/api/teams/team_test/states/state_todo", map[string]any{"color": "#202020", "name": "To do"}, http.StatusOK)
	ask := call(http.MethodPost, "/api/asks", map[string]any{"title": "Access request"}, http.StatusCreated)
	call(http.MethodPatch, "/api/asks/"+ask["id"].(string), map[string]any{"title": "Access request v2"}, http.StatusOK)
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stored, ok := reopened.BootstrapFor("test-workspace")
	if !ok {
		t.Fatal("workspace missing after reopen")
	}
	members, err := reopened.ListTeamMembers(t.Context(), stored.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(members, func(a, b domain.TeamMember) int {
		if a.TeamID != b.TeamID {
			return compareStrings(a.TeamID, b.TeamID)
		}
		return compareStrings(a.UserID, b.UserID)
	})
	// Nanosecond team ids normalize to the same key, so compare per-team
	// settings as a sorted list.
	sortedByJSON := func(values []any) []string {
		items := []string{}
		for _, value := range values {
			raw, _ := json.Marshal(value)
			items = append(items, normalizeMutationJSON(raw, false))
		}
		slices.Sort(items)
		return items
	}
	var teamSettings, cycleSettings []any
	for _, value := range stored.TeamSettings {
		teamSettings = append(teamSettings, value)
	}
	for id, value := range stored.CycleSettings {
		cycleSettings = append(cycleSettings, map[string]any{"id": id, "settings": value})
	}
	for key, value := range map[string]any{"teams": stored.Teams, "teamSettings": sortedByJSON(teamSettings), "cycleSettings": sortedByJSON(cycleSettings), "states": stored.States, "auditLog": stored.AuditLog, "teamMembers": stored.TeamMembers, "members": stored.Members, "persistedTeamMembers": members, "issues": stored.Issues, "subscriptions": stored.Subscriptions, "favorites": stored.Favorites, "workspace": stored.Workspace, "settings": stored.Settings, "workspaceSettings": stored.WorkspaceSettings, "labelGroups": stored.LabelGroups, "asks": stored.Asks, "projectDisplayDefault": stored.ProjectDisplayDefault, "notificationPreferences": stored.NotificationPreferences} {
		raw, _ := json.Marshal(value)
		result.state[key] = normalizeMutationJSON(raw, false)
	}
	for index := range result.events {
		result.events[index] = opaqueMutationID.ReplaceAllString(result.events[index], "<id>")
	}
	for index := range result.webhooks {
		result.webhooks[index] = opaqueMutationID.ReplaceAllString(result.webhooks[index], "<id>")
	}
	for key, value := range result.state {
		result.state[key] = opaqueMutationID.ReplaceAllString(value, "<id>")
	}
	result.cleaned = true
	for _, issue := range stored.Issues {
		if issue.Team.ID != "team_test" {
			continue
		}
		if issue.Assignee != nil && issue.Assignee.ID == "usr_member" || slices.Contains(issue.SubscriberIDs, "usr_member") {
			result.cleaned = false
		}
	}
	return result
}

// Dashboards, posts and feed items use random opaque ids.
var opaqueMutationID = regexp.MustCompile(`\b(dashboard|post|feed)_[A-Za-z0-9_-]{16}`)

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
