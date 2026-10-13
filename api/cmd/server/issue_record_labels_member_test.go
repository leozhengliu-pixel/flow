package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Members (and team-restricted API keys) read issue labels with their name,
// color and description, the same as /api/labels returns them: the access
// projection only decides which labels are visible.
func TestIssueRecordLabelsKeepTheirFieldsForMembers(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	host := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer host.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", 200)
	member, memberUser := verifiedAuthClient(t, host.URL, "Label reader", "label-reader@example.test")
	invite := authRequest[[]domain.Invitation](t, admin, "POST", host.URL+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{memberUser.Email}, "role": "member"}, "", 201)
	authRequest[domain.WorkspaceMembership](t, member, "POST", host.URL+"/api/invitations/accept", map[string]string{"token": invite[0].Token}, "", 200)
	team := authRequest[domain.Team](t, admin, "POST", host.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Label team", "key": "LBL"}, "", 201)
	workspaceLabel := authRequest[domain.IssueLabel](t, admin, "POST", host.URL+"/api/labels", map[string]any{"name": "开发任务", "color": "#4cb782", "description": "dev work"}, "test-workspace", 201)
	teamLabel := authRequest[domain.IssueLabel](t, admin, "POST", host.URL+"/api/teams/"+team.ID+"/labels", map[string]any{"name": "Team only", "color": "#eb5757"}, "test-workspace", 201)
	issue := authRequest[domain.Issue](t, admin, "POST", host.URL+"/api/issue-records", map[string]any{"title": "Labelled", "teamId": team.ID, "labelIds": []string{workspaceLabel.ID, teamLabel.ID}}, "test-workspace", 201)
	if len(issue.Labels) != 2 {
		t.Fatalf("created labels = %#v", issue.Labels)
	}
	want := map[string]domain.IssueLabel{workspaceLabel.ID: workspaceLabel, teamLabel.ID: teamLabel}
	check := func(source string, labels []domain.IssueLabel) {
		t.Helper()
		if len(labels) != 2 {
			t.Fatalf("%s: labels = %#v", source, labels)
		}
		for _, label := range labels {
			expected := want[label.ID]
			if label.Name != expected.Name || label.Color != expected.Color || label.Description != expected.Description {
				t.Errorf("%s: label %s = %+v, want name %q color %q description %q", source, label.ID, label, expected.Name, expected.Color, expected.Description)
			}
		}
	}
	detail := authRequest[struct {
		Issue domain.Issue `json:"issue"`
	}](t, member, "GET", host.URL+"/api/issue-records/"+issue.ID+"/context", nil, "test-workspace", 200)
	check("context", detail.Issue.Labels)
	check("detail", authRequest[domain.Issue](t, member, "GET", host.URL+"/api/issue-records/"+issue.ID, nil, "test-workspace", 200).Labels)
	page := authRequest[store.IssueRecordPage](t, member, "GET", host.URL+"/api/issue-records?teamId="+team.ID, nil, "test-workspace", 200)
	if len(page.Items) != 1 {
		t.Fatalf("page = %#v", page.Items)
	}
	check("list", page.Items[0].Labels)
	list := authRequest[store.IssueRecordPage](t, member, "GET", host.URL+"/api/issue-records?projection=list&teamId="+team.ID, nil, "test-workspace", 200)
	check("list projection", list.Items[0].Labels)
	check("identifier", authRequest[domain.Issue](t, member, "GET", host.URL+"/api/issue-records/"+issue.Identifier, nil, "test-workspace", 200).Labels)

	key := authRequest[struct {
		Secret string `json:"secret"`
	}](t, admin, http.MethodPost, host.URL+"/api/api-keys", map[string]any{"name": "Team reader", "scopes": []string{"read"}, "teamRestriction": "selected", "teamIds": []string{team.ID}}, "test-workspace", http.StatusCreated)
	bearer := func(path string, target any) {
		t.Helper()
		request, err := http.NewRequest(http.MethodGet, host.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+key.Secret)
		request.Header.Set("X-Workspace-Key", "test-workspace")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s returned %d", path, response.StatusCode)
		}
		if err := json.NewDecoder(response.Body).Decode(target); err != nil {
			t.Fatal(err)
		}
	}
	var keyIssue domain.Issue
	bearer("/api/issue-records/"+issue.Identifier, &keyIssue)
	check("api key detail", keyIssue.Labels)
	var keyPage store.IssueRecordPage
	bearer("/api/issue-records?teamId="+team.ID, &keyPage)
	if len(keyPage.Items) != 1 {
		t.Fatalf("api key page = %#v", keyPage.Items)
	}
	check("api key list", keyPage.Items[0].Labels)
}
