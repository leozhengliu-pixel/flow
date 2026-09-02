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

func TestPrivateIssueExplicitShareAndTeamOwnerInheritance(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "issue-acl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	server := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer server.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, server.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	member, memberUser := verifiedAuthClient(t, server.URL, "Issue guest", "issue-share@example.com")
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, server.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	team := authRequest[domain.Team](t, admin, http.MethodPost, server.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Private ACL", "key": "PAC", "private": true}, "", http.StatusCreated)
	issue := authRequest[domain.Issue](t, admin, http.MethodPost, server.URL+"/api/issues", map[string]any{"title": "Shared private issue", "teamId": team.ID}, "test-workspace", http.StatusCreated)
	invite := authRequest[[]domain.Invitation](t, admin, http.MethodPost, server.URL+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{memberUser.Email}, "role": "member"}, "", http.StatusCreated)
	authRequest[domain.WorkspaceMembership](t, member, http.MethodPost, server.URL+"/api/invitations/accept", map[string]string{"token": invite[0].Token}, "", http.StatusOK)
	// A workspace member who is not in the private team cannot see the issue.
	before := authRequest[domain.Bootstrap](t, member, http.MethodGet, server.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	if slices.ContainsFunc(before.Issues, func(item domain.Issue) bool { return item.ID == issue.ID }) {
		t.Fatalf("private issue leaked before share: %#v", before.Issues)
	}
	permissions := authRequest[[]domain.IssuePermission](t, admin, http.MethodPut, server.URL+"/api/issues/"+issue.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": memberUser.ID, "role": "commenter"}}}, "test-workspace", http.StatusOK)
	if len(permissions) != 1 || permissions[0].Role != "commenter" {
		t.Fatalf("permissions = %#v", permissions)
	}
	after := authRequest[domain.Bootstrap](t, member, http.MethodGet, server.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	if !slices.ContainsFunc(after.Issues, func(item domain.Issue) bool { return item.ID == issue.ID }) {
		t.Fatalf("shared private issue not projected: %#v", after.Issues)
	}
	authRequest[any](t, member, http.MethodPatch, server.URL+"/api/issues/"+issue.ID, map[string]string{"title": "blocked"}, "test-workspace", http.StatusForbidden)
	// The workspace owner inherits team-owner access for a child team.
	child := authRequest[domain.Team](t, admin, http.MethodPost, server.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Child", "key": "CHD", "parentTeamId": team.ID}, "", http.StatusCreated)
	authRequest[any](t, admin, http.MethodPatch, server.URL+"/api/workspaces/test-workspace/teams/"+child.ID, map[string]string{"name": "Child renamed"}, "", http.StatusOK)
	shared := authRequest[map[string]any](t, admin, http.MethodPost, server.URL+"/api/issues/"+issue.ID+"/share", nil, "test-workspace", http.StatusOK)
	shareToken, _ := shared["token"].(string)
	if shareToken == "" {
		t.Fatalf("private issue share token missing: %#v", shared)
	}
	public := authRequest[map[string]any](t, authClient(t), http.MethodGet, server.URL+"/api/shared/issues/"+shareToken+"?workspace=test-workspace", nil, "", http.StatusOK)
	publicIssue, _ := public["issue"].(map[string]any)
	publicTeam, _ := publicIssue["team"].(map[string]any)
	if publicIssue["id"] != issue.ID || publicIssue["shareToken"] != nil || publicTeam["id"] != "" || publicTeam["name"] != "" || publicTeam["key"] != "" {
		t.Fatalf("shared private issue projection leaked token or issue: %#v", public)
	}
	authRequest[any](t, member, http.MethodPost, server.URL+"/api/issues/"+issue.ID+"/share", nil, "test-workspace", http.StatusNotFound)
	authRequest[any](t, admin, http.MethodDelete, server.URL+"/api/issues/"+issue.ID+"/share", nil, "test-workspace", http.StatusNoContent)
	authRequest[any](t, authClient(t), http.MethodGet, server.URL+"/api/shared/issues/"+shareToken+"?workspace=test-workspace", nil, "", http.StatusNotFound)
	_ = bootstrap
}
