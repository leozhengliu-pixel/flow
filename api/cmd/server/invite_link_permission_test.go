package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// The join link is a credential: reading, rotating and disabling it need the
// invite permission, and no bootstrap carries its token to anyone without it.
func TestWorkspaceInviteLinkNeedsInvitePermission(t *testing.T) {
	for _, bounded := range []string{"off", "on"} {
		t.Run("bounded metadata "+bounded, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_HYDRATION", "on")
			t.Setenv("FLOW_RECORD_JOURNAL", "on")
			t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
			repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "invite-link.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer repository.Close()
			serving := repository
			if bounded == "on" {
				if serving, err = repository.BoundedMetadataViewForTesting(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			host := httptest.NewServer(newHandler(&server{store: serving, uploadPath: t.TempDir()}))
			defer host.Close()
			const workspace = "test-workspace"
			url := func(path string) string { return host.URL + path }
			admin := authClient(t)
			authRequest[domain.AuthSession](t, admin, http.MethodPost, url("/api/auth/login"), map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
			authRequest[domain.WorkspaceSettings](t, admin, http.MethodPatch, url("/api/workspace/preferences"), map[string]any{"inviteLinksEnabled": true, "invitePermission": "admins"}, workspace, http.StatusOK)
			teamID := authRequest[domain.Bootstrap](t, admin, http.MethodGet, url("/api/bootstrap"), nil, workspace, http.StatusOK).Teams[0].ID
			join := func(name, email, role string) *http.Client {
				client, user := verifiedAuthClient(t, host.URL, name, email)
				invitation := authRequest[[]domain.Invitation](t, admin, http.MethodPost, url("/api/workspaces/"+workspace+"/invitations"), map[string]any{"emails": []string{user.Email}, "role": role, "teamIds": []string{teamID}}, "", http.StatusCreated)
				authRequest[domain.WorkspaceMembership](t, client, http.MethodPost, url("/api/invitations/accept"), map[string]string{"token": invitation[0].Token}, "", http.StatusOK)
				return client
			}
			member := join("Link member", "link-member@example.com", "member")
			guest := join("Link guest", "link-guest@example.com", "guest")
			linkPath := "/api/workspaces/" + workspace + "/invite-link"
			link := authRequest[domain.WorkspaceInviteLink](t, admin, http.MethodPost, url(linkPath), map[string]any{}, workspace, http.StatusOK)
			if link.Token == "" {
				t.Fatal("admin got no link")
			}
			if got := authRequest[domain.WorkspaceInviteLink](t, admin, http.MethodGet, url(linkPath), nil, workspace, http.StatusOK); got.Token != link.Token {
				t.Fatal("admin read a different link")
			}
			adminBootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, url("/api/bootstrap"), nil, workspace, http.StatusOK)
			if adminBootstrap.WorkspaceInviteLink == nil || adminBootstrap.WorkspaceInviteLink.Token != link.Token {
				t.Fatal("admin bootstrap lost the link")
			}
			for name, client := range map[string]*http.Client{"member": member, "guest": guest} {
				for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
					if got := authRequestStatus(t, client, method, url(linkPath), nil, workspace); got != http.StatusForbidden {
						t.Errorf("%s %s invite-link = %d, want 403", name, method, got)
					}
				}
				boot := authRequest[domain.Bootstrap](t, client, http.MethodGet, url("/api/bootstrap"), nil, workspace, http.StatusOK)
				if boot.WorkspaceInviteLink != nil {
					t.Errorf("%s bootstrap carries the invite link", name)
				}
			}
			if got := authRequest[domain.WorkspaceInviteLink](t, admin, http.MethodGet, url(linkPath), nil, workspace, http.StatusOK); got.Token != link.Token {
				t.Fatal("a refused member rotated the link")
			}

			// A workspace that lets members invite lets them manage the link.
			authRequest[domain.WorkspaceSettings](t, admin, http.MethodPatch, url("/api/workspace/preferences"), map[string]any{"invitePermission": "members"}, workspace, http.StatusOK)
			authRequest[domain.WorkspaceInviteLink](t, member, http.MethodGet, url(linkPath), nil, workspace, http.StatusOK)
			authRequest[domain.WorkspaceInviteLink](t, member, http.MethodPost, url(linkPath), map[string]any{}, workspace, http.StatusOK)
			if boot := authRequest[domain.Bootstrap](t, member, http.MethodGet, url("/api/bootstrap"), nil, workspace, http.StatusOK); boot.WorkspaceInviteLink == nil || boot.WorkspaceInviteLink.Token == "" {
				t.Error("permitted member bootstrap lost the link")
			}
			if got := authRequestStatus(t, guest, http.MethodGet, url(linkPath), nil, workspace); got != http.StatusForbidden {
				t.Errorf("guest invite-link = %d with member invites on", got)
			}
		})
	}
}
