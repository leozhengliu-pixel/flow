package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestTeamIssueViewDefaultsRoundTrip(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "view-defaults.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	teamID := repository.Bootstrap().Teams[0].ID
	path := "/api/teams/" + teamID + "/settings"
	saved := requestJSON[domain.TeamSettings](t, handler, http.MethodPatch, path, map[string]any{"issueViewDefaults": map[string]any{"board": map[string]any{"layout": "board", "grouping": "priority"}}}, http.StatusOK)
	if string(saved.IssueViewDefaults["board"]) == "" {
		t.Fatalf("default not stored: %#v", saved.IssueViewDefaults)
	}
	requestJSON[map[string]any](t, handler, http.MethodPatch, path, map[string]any{"issueViewDefaults": map[string]any{"unknown": map[string]any{}}}, http.StatusBadRequest)
	cleared := requestJSON[domain.TeamSettings](t, handler, http.MethodPatch, path, map[string]any{"issueViewDefaults": map[string]any{"board": nil}}, http.StatusOK)
	if _, ok := cleared.IssueViewDefaults["board"]; ok {
		t.Fatalf("default not cleared: %#v", cleared.IssueViewDefaults)
	}
}

func TestTeamIssueViewInsightsDefaultValidationAndPersistence(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "view-insights.db")
	repository, err := store.OpenSQLiteTestFixture(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	teamID := repository.Bootstrap().Teams[0].ID
	path := "/api/teams/" + teamID + "/settings"
	config := map[string]any{"measure": "issueCount", "slice": "status", "segment": "priority", "showArchived": true, "hideEmptySegment": true, "colors": "status", "timeInStatusIds": []string{}}
	saved := requestJSON[domain.TeamSettings](t, handler, http.MethodPatch, path, map[string]any{"issueViewInsights": map[string]any{"all": config}}, http.StatusOK)
	var stored map[string]any
	if err := json.Unmarshal(saved.IssueViewInsights["all"], &stored); err != nil || stored["segment"] != "priority" || stored["hideEmptySegment"] != true || stored["showArchived"] != true {
		t.Fatalf("insights default not stored: %s (%v)", saved.IssueViewInsights["all"], err)
	}
	for _, invalid := range []map[string]any{
		{"unknown": config},
		{"all": map[string]any{"measure": "velocity"}},
		{"all": map[string]any{"slice": "nope"}},
		{"all": map[string]any{"segment": "labelGroup:"}},
		{"all": map[string]any{"colors": "rainbow"}},
		{"all": map[string]any{"aggregations": []string{"p99"}}},
		{"all": map[string]any{"extra": true}},
		{"all": []string{"issueCount"}},
	} {
		requestJSON[map[string]any](t, handler, http.MethodPatch, path, map[string]any{"issueViewInsights": invalid}, http.StatusBadRequest)
	}
	// A display default saved later keeps the insights default (separate fields).
	requestJSON[domain.TeamSettings](t, handler, http.MethodPatch, path, map[string]any{"issueViewDefaults": map[string]any{"all": map[string]any{"grouping": "priority"}}}, http.StatusOK)
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	persisted := reopened.Bootstrap().TeamSettings[teamID]
	if len(persisted.IssueViewInsights["all"]) == 0 || len(persisted.IssueViewDefaults["all"]) == 0 {
		t.Fatalf("team view defaults not persisted: insights=%s display=%s", persisted.IssueViewInsights["all"], persisted.IssueViewDefaults["all"])
	}
	handler = newHandler(&server{store: reopened, uploadPath: t.TempDir(), authDisabled: true})
	cleared := requestJSON[domain.TeamSettings](t, handler, http.MethodPatch, path, map[string]any{"issueViewInsights": map[string]any{"all": nil}}, http.StatusOK)
	if _, ok := cleared.IssueViewInsights["all"]; ok {
		t.Fatalf("insights default not cleared: %#v", cleared.IssueViewInsights)
	}
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/views", map[string]any{"name": "Bad insights", "resource": "issues", "scope": "workspace", "insights": map[string]any{"measure": "velocity"}}, http.StatusBadRequest)
	view := requestJSON[domain.SavedView](t, handler, http.MethodPost, "/api/views", map[string]any{"name": "Good insights", "resource": "issues", "scope": "workspace", "insights": map[string]any{"measure": "issueCount", "slice": "assignee", "segment": "none", "hideEmptySegment": true}}, http.StatusCreated)
	if !strings.Contains(string(view.Insights), `"hideEmptySegment":true`) {
		t.Fatalf("saved view insights = %s", view.Insights)
	}
}

func TestTeamIssueViewInsightsDefaultRequiresAdmin(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "insights-admin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	workspace := data.Workspace.URLKey
	host := httptest.NewServer(newHandler(&server{store: repo, uploadPath: t.TempDir()}))
	defer host.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	member, memberUser := verifiedAuthClient(t, host.URL, "Insights member", "insights-member@example.test")
	invite, err := repo.Invite(t.Context(), data.Workspace.ID, data.Viewer.ID, memberUser.Email, "member", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AcceptInvitation(t.Context(), invite.Token, memberUser.ID); err != nil {
		t.Fatal(err)
	}
	path := host.URL + "/api/teams/" + data.Teams[0].ID + "/settings"
	body := map[string]any{"issueViewInsights": map[string]any{"all": map[string]any{"measure": "issueCount", "slice": "status", "segment": "priority"}}}
	authRequest[any](t, member, "PATCH", path, body, workspace, http.StatusForbidden)
	saved := authRequest[domain.TeamSettings](t, admin, "PATCH", path, body, workspace, http.StatusOK)
	if len(saved.IssueViewInsights["all"]) == 0 {
		t.Fatal("admin could not set the insights default")
	}
}
