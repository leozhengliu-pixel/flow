package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"flow/api/internal/domain"
)

func TestWorkspacePermissionMatrix(t *testing.T) {
	settings := domain.WorkspaceSettings{
		InvitePermission: "admins", TeamCreatePermission: "members", LabelPermission: "everyone", TemplatePermission: "admins", APIKeyPermission: "members",
	}
	cases := []struct {
		name       string
		permission string
		allowed    bool
	}{
		{"invite-member", "invite", false},
		{"team-member", "team", true},
		{"label-member", "label", true},
		{"template-member", "template", false},
		{"api-key-member", "apiKey", true},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := workspacePermissionAllows(settings, item.permission, "member"); got != item.allowed {
				t.Fatalf("member %s permission = %v, want %v", item.permission, got, item.allowed)
			}
		})
	}
	for _, role := range []string{"owner", "admin"} {
		for permission := range map[string]struct{}{"invite": {}, "team": {}, "label": {}, "template": {}, "apiKey": {}} {
			if !workspacePermissionAllows(settings, permission, role) {
				t.Fatalf("%s should bypass %s permission", role, permission)
			}
		}
	}
	for permission := range map[string]struct{}{"invite": {}, "team": {}, "label": {}, "template": {}, "apiKey": {}} {
		if workspacePermissionAllows(settings, permission, "guest") {
			t.Fatalf("guest should not pass %s permission", permission)
		}
	}
}

func TestPermissionPathClassification(t *testing.T) {
	permissionCases := []struct {
		method string
		path   string
		want   string
	}{
		{http.MethodPost, "/api/workspaces/acme/invitations", "invite"},
		{http.MethodPost, "/api/workspaces/acme/teams", "team"},
		{http.MethodPatch, "/api/labels/label-1", "label"},
		{http.MethodPost, "/api/document-templates", "template"},
		{http.MethodPost, "/api/api-keys", "apiKey"},
		{http.MethodGet, "/api/labels", ""},
	}
	for _, item := range permissionCases {
		r := httptest.NewRequest(item.method, item.path, nil)
		if got := permissionForRequest(r); got != item.want {
			t.Errorf("permissionForRequest(%s %s) = %q, want %q", item.method, item.path, got, item.want)
		}
	}
	adminCases := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodGet, "/api/webhooks", true},
		{http.MethodGet, "/api/release-pipelines/p-1", false},
		{http.MethodPost, "/api/release-pipelines/p-1/access-key", true},
		{http.MethodGet, "/api/workspaces/acme/members/u-1", true},
		{http.MethodGet, "/api/workspaces/acme/teams/t-1/members", false},
	}
	for _, item := range adminCases {
		r := httptest.NewRequest(item.method, item.path, nil)
		if got := adminOnlyRequest(r); got != item.want {
			t.Errorf("adminOnlyRequest(%s %s) = %v, want %v", item.method, item.path, got, item.want)
		}
	}
	for _, path := range []string{"/api/initiatives", "/api/customers", "/api/customer-requests", "/api/views", "/api/analytics", "/api/dashboards"} {
		if !guestRestrictedPath(path) {
			t.Errorf("guestRestrictedPath(%q) = false", path)
		}
	}
}
