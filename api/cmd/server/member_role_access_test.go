package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// TestMemberRoleMatchesReference walks what a workspace member may do, as measured
// in the reference app's member view: administration pages and admin-configured settings
// are refused server-side, member features stay open, and a session request
// that names no workspace acts only in the viewer's own workspace.
func TestMemberRoleMatchesReference(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "member-role.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	server := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer server.Close()

	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, server.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, server.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	teamID, teamKey := bootstrap.Teams[0].ID, bootstrap.Teams[0].Key
	authRequest[any](t, admin, http.MethodPatch, server.URL+"/api/workspace/preferences", map[string]any{"labelPermission": "members", "apiKeyPermission": "members"}, "test-workspace", http.StatusOK)
	member := inviteTeamPermissionActor(t, server.URL, admin, "Workspace member", "workspace-member@example.test", "member", []string{teamID})

	status := func(client *http.Client, method, path string, input any, workspace string) int {
		t.Helper()
		var body io.Reader
		if input != nil {
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			body = bytes.NewReader(raw)
		}
		request, err := http.NewRequest(method, server.URL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		if input != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		if workspace != "" {
			request.Header.Set("X-Workspace-Key", workspace)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		return response.StatusCode
	}

	t.Run("administration and admin-configured settings are refused", func(t *testing.T) {
		for _, item := range []struct{ method, path string }{
			{http.MethodGet, "/api/workspace/audit-log"},
			{http.MethodGet, "/api/workspace/audit-log-stream"},
			{http.MethodGet, "/api/workspace/preferences"},
			{http.MethodPatch, "/api/workspace/preferences"},
			{http.MethodGet, "/api/webhooks"},
			{http.MethodPost, "/api/webhooks"},
			{http.MethodGet, "/api/oauth-applications"},
			{http.MethodPost, "/api/oauth-applications"},
			{http.MethodGet, "/api/application-installations"},
			{http.MethodGet, "/api/identity-providers"},
			{http.MethodPut, "/api/sla-settings"},
			{http.MethodPost, "/api/sla-rules"},
			{http.MethodPost, "/api/project-statuses"},
			{http.MethodPut, "/api/project-update-settings"},
			{http.MethodPost, "/api/customer-statuses"},
			{http.MethodPost, "/api/customer-tiers"},
			{http.MethodPatch, "/api/workspaces/test-workspace"},
			{http.MethodPost, "/api/imports/preview"},
		} {
			if got := status(member.client, item.method, item.path, map[string]any{}, "test-workspace"); got != http.StatusForbidden {
				t.Errorf("member %s %s = %d, want 403", item.method, item.path, got)
			}
		}
	})

	t.Run("member features stay open", func(t *testing.T) {
		for _, item := range []struct {
			method, path string
			input        any
		}{
			{http.MethodGet, "/api/bootstrap", nil},
			{http.MethodGet, "/api/api-keys", nil},
			{http.MethodPost, "/api/labels", map[string]any{"name": "QA member label", "color": "#888888"}},
			{http.MethodPost, "/api/customers", map[string]any{"name": "QA member customer"}},
			{http.MethodPost, "/api/release-pipelines", map[string]any{"name": "QA member pipeline"}},
		} {
			if got := status(member.client, item.method, item.path, item.input, "test-workspace"); got < 200 || got > 299 {
				t.Errorf("member %s %s = %d, want 2xx", item.method, item.path, got)
			}
		}
	})

	t.Run("a request without a workspace stays in the viewer's own workspace", func(t *testing.T) {
		// Another account's workspace is written last, so a store read that
		// defaults an empty key would land in it.
		outsider, _ := verifiedAuthClient(t, server.URL, "Outsider", "outsider@example.test")
		authRequest[any](t, outsider, http.MethodPost, server.URL+"/api/workspaces", map[string]any{"name": "Outside", "urlKey": "outside"}, "", http.StatusCreated)
		authRequest[any](t, outsider, http.MethodPost, server.URL+"/api/labels", map[string]any{"name": "Outside label", "color": "#888888"}, "outside", http.StatusCreated)
		if got := status(member.client, http.MethodGet, "/api/workspace/audit-log", nil, ""); got != http.StatusForbidden {
			t.Fatalf("member audit log without a workspace = %d, want 403 (their own workspace)", got)
		}
		unscoped := authRequest[[]domain.AuditLogEntry](t, admin, http.MethodGet, server.URL+"/api/workspace/audit-log", nil, "", http.StatusOK)
		own := authRequest[[]domain.AuditLogEntry](t, admin, http.MethodGet, server.URL+"/api/workspace/audit-log", nil, "test-workspace", http.StatusOK)
		if len(unscoped) != len(own) {
			t.Fatalf("admin audit log without a workspace has %d entries, own workspace %d", len(unscoped), len(own))
		}
		stranger, _ := verifiedAuthClient(t, server.URL, "Stranger", "stranger@example.test")
		if got := status(stranger, http.MethodGet, "/api/workspace/audit-log", nil, ""); got != http.StatusForbidden {
			t.Fatalf("account without a workspace reading an audit log = %d, want 403", got)
		}
		if got := status(stranger, http.MethodGet, "/api/account/bootstrap", nil, ""); got != http.StatusOK {
			t.Fatalf("account bootstrap without a workspace = %d, want 200", got)
		}
	})

	t.Run("members cannot leave their last active team; admins may still remove them", func(t *testing.T) {
		url := server.URL + "/api/workspaces/test-workspace/teams/" + teamID + "/members/" + member.user.ID
		if code, message := teamMembershipRequest(t, member.client, url, false, "member"); code != http.StatusConflict || message != "You should be a member of at least one active team" {
			t.Fatalf("leaving the last team = %d %q", code, message)
		}
		if got := status(member.client, http.MethodDelete, "/api/teams/"+teamID+"/members/"+member.user.ID, nil, "test-workspace"); got != http.StatusConflict {
			t.Fatalf("removing yourself through the team member API = %d, want 409", got)
		}
		// The route takes the team key too; the permission gate must resolve it like the handler does.
		if got := status(member.client, http.MethodDelete, "/api/teams/"+strings.ToLower(teamKey)+"/members/"+member.user.ID, nil, "test-workspace"); got != http.StatusConflict {
			t.Fatalf("removing yourself by team key = %d, want 409", got)
		}
		// The reference app's team member list lets an admin remove someone from their only team.
		if code, message := teamMembershipRequest(t, admin, url, false, "member"); code != http.StatusNoContent {
			t.Fatalf("admin removal = %d %q", code, message)
		}
	})
}
