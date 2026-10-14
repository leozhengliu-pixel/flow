package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// The reference app's private-team rule, measured in the reference app: a workspace admin outside a
// private team sees it in Settings › Teams and manages its settings, members
// and lifecycle ("As an admin you have access to the team's settings"), but
// its issues, projects and team pages stay closed until the admin joins.
// Non-admins outside the team see nothing of it; members see everything.
func TestPrivateTeamAccessSplitsAdminSettingsFromContent(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	host := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer host.Close()
	const workspace = "test-workspace"
	owner := authClient(t)
	authRequest[domain.AuthSession](t, owner, http.MethodPost, host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	join := func(name, email, role string) (*http.Client, domain.User) {
		client, user := verifiedAuthClient(t, host.URL, name, email)
		invite := authRequest[[]domain.Invitation](t, owner, http.MethodPost, host.URL+"/api/workspaces/"+workspace+"/invitations", map[string]any{"emails": []string{email}, "role": role}, "", http.StatusCreated)
		authRequest[domain.WorkspaceMembership](t, client, http.MethodPost, host.URL+"/api/invitations/accept", map[string]string{"token": invite[0].Token}, "", http.StatusOK)
		return client, user
	}
	admin, adminUser := join("Outside admin", "outside-admin@example.test", "admin")
	outsider, _ := join("Outside member", "outside-member@example.test", "member")
	insider, insiderUser := join("Team member", "team-member@example.test", "member")

	team := authRequest[domain.Team](t, owner, http.MethodPost, host.URL+"/api/workspaces/"+workspace+"/teams", map[string]any{"name": "QA private vis", "key": "QPV", "private": true}, "", http.StatusCreated)
	authRequest[any](t, owner, http.MethodPut, host.URL+"/api/workspaces/"+workspace+"/teams/"+team.ID+"/members/"+insiderUser.ID, map[string]any{"member": true, "role": "member"}, "", http.StatusNoContent)
	issue := authRequest[domain.Issue](t, owner, http.MethodPost, host.URL+"/api/issue-records", map[string]any{"title": "QA private vis issue", "teamId": team.ID}, workspace, http.StatusCreated)
	project := authRequest[domain.Project](t, owner, http.MethodPost, host.URL+"/api/projects", map[string]any{"name": "QA private vis project", "teamIds": []string{team.ID}}, workspace, http.StatusCreated)

	hasTeam := func(data domain.Bootstrap) bool {
		return slices.ContainsFunc(data.Teams, func(item domain.Team) bool { return item.ID == team.ID })
	}
	hasIssue := func(data domain.Bootstrap) bool {
		return slices.ContainsFunc(data.Issues, func(item domain.Issue) bool { return item.ID == issue.ID })
	}
	hasProject := func(data domain.Bootstrap) bool {
		return slices.ContainsFunc(data.Projects, func(item domain.Project) bool { return item.ID == project.ID })
	}
	teamIssues := func(client *http.Client) int {
		return len(authRequest[store.IssueRecordPage](t, client, http.MethodGet, host.URL+"/api/issue-records?teamId="+team.ID, nil, workspace, http.StatusOK).Items)
	}

	// Admin outside the team: the team row and its settings, none of its content.
	data := authRequest[domain.Bootstrap](t, admin, http.MethodGet, host.URL+"/api/bootstrap", nil, workspace, http.StatusOK)
	if !hasTeam(data) || hasIssue(data) || hasProject(data) {
		t.Fatalf("admin bootstrap: team=%v issue=%v project=%v, want only the team row", hasTeam(data), hasIssue(data), hasProject(data))
	}
	if _, ok := data.TeamSettings[team.ID]; !ok {
		t.Fatal("admin lost the private team's settings")
	}
	authRequest[domain.Team](t, admin, http.MethodGet, host.URL+"/api/teams/"+team.ID, nil, workspace, http.StatusOK)
	authRequest[any](t, admin, http.MethodGet, host.URL+"/api/issue-records/"+issue.ID, nil, workspace, http.StatusNotFound)
	if got := teamIssues(admin); got != 0 {
		t.Fatalf("admin listed %d private issues", got)
	}
	authRequest[any](t, admin, http.MethodGet, host.URL+"/api/projects/"+project.ID, nil, workspace, http.StatusForbidden)
	authRequest[any](t, admin, http.MethodPost, host.URL+"/api/issue-records", map[string]any{"title": "Not a member", "teamId": team.ID}, workspace, http.StatusForbidden)
	authRequest[any](t, admin, http.MethodPost, host.URL+"/api/projects", map[string]any{"name": "Not a member", "teamIds": []string{team.ID}}, workspace, http.StatusForbidden)
	authRequest[domain.TeamSettings](t, admin, http.MethodPatch, host.URL+"/api/teams/"+team.ID+"/settings", map[string]any{"description": "Managed by an admin"}, workspace, http.StatusOK)
	authRequest[domain.Team](t, admin, http.MethodPatch, host.URL+"/api/workspaces/"+workspace+"/teams/"+team.ID, map[string]any{"name": "QA private vis renamed"}, "", http.StatusOK)

	// Non-admin outside the team: nothing at all.
	data = authRequest[domain.Bootstrap](t, outsider, http.MethodGet, host.URL+"/api/bootstrap", nil, workspace, http.StatusOK)
	if hasTeam(data) || hasIssue(data) || hasProject(data) {
		t.Fatal("non-member saw the private team")
	}
	authRequest[any](t, outsider, http.MethodGet, host.URL+"/api/teams/"+team.ID, nil, workspace, http.StatusForbidden)
	authRequest[any](t, outsider, http.MethodGet, host.URL+"/api/issue-records/"+issue.ID, nil, workspace, http.StatusNotFound)
	authRequest[any](t, outsider, http.MethodPatch, host.URL+"/api/teams/"+team.ID+"/settings", map[string]any{"description": "Not allowed"}, workspace, http.StatusForbidden)

	// Members, and the admin once they join, read everything.
	data = authRequest[domain.Bootstrap](t, insider, http.MethodGet, host.URL+"/api/bootstrap", nil, workspace, http.StatusOK)
	if !hasTeam(data) || !hasIssue(data) || !hasProject(data) {
		t.Fatal("team member lost private content")
	}
	authRequest[domain.Issue](t, insider, http.MethodGet, host.URL+"/api/issue-records/"+issue.ID, nil, workspace, http.StatusOK)
	authRequest[any](t, admin, http.MethodPut, host.URL+"/api/workspaces/"+workspace+"/teams/"+team.ID+"/members/"+adminUser.ID, map[string]any{"member": true, "role": "member"}, "", http.StatusNoContent)
	data = authRequest[domain.Bootstrap](t, admin, http.MethodGet, host.URL+"/api/bootstrap", nil, workspace, http.StatusOK)
	if !hasIssue(data) || !hasProject(data) {
		t.Fatal("admin who joined still cannot read the team")
	}
	authRequest[domain.Issue](t, admin, http.MethodGet, host.URL+"/api/issue-records/"+issue.ID, nil, workspace, http.StatusOK)
	if got := teamIssues(admin); got != 1 {
		t.Fatalf("joined admin listed %d issues, want 1", got)
	}
}
