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

type teamPermissionActor struct {
	client *http.Client
	user   domain.User
}

func TestTeamMembershipPermissionMatrix(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "team-permissions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	server := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer server.Close()

	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, server.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, server.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	teamID := bootstrap.Teams[0].ID
	authRequest[domain.TeamSettings](t, admin, http.MethodPatch, server.URL+"/api/teams/"+teamID+"/settings", map[string]any{
		"access": "public", "membershipRestriction": "open", "memberPermission": "allMembers",
	}, "test-workspace", http.StatusOK)

	teamOwner := inviteTeamPermissionActor(t, server.URL, admin, "Team owner", "team-owner@example.test", "member", []string{teamID})
	teamMember := inviteTeamPermissionActor(t, server.URL, admin, "Team member", "team-member@example.test", "member", []string{teamID})
	outsider := inviteTeamPermissionActor(t, server.URL, admin, "Workspace member", "workspace-member@example.test", "member", nil)
	guest := inviteTeamPermissionActor(t, server.URL, admin, "Guest", "guest@example.test", "guest", []string{teamID})
	target := inviteTeamPermissionActor(t, server.URL, admin, "Target", "target@example.test", "member", nil)

	membershipURL := func(id string) string {
		return server.URL + "/api/workspaces/test-workspace/teams/" + teamID + "/members/" + id
	}
	authRequest[any](t, admin, http.MethodPut, membershipURL(teamOwner.user.ID), map[string]any{"member": true, "role": "owner"}, "", http.StatusNoContent)

	for _, allowed := range []struct {
		name  string
		actor *http.Client
	}{
		{name: "workspace admin", actor: admin},
		{name: "team owner", actor: teamOwner.client},
		{name: "team member", actor: teamMember.client},
	} {
		t.Run(allowed.name+" may manage ordinary members", func(t *testing.T) {
			status, message := teamMembershipRequest(t, allowed.actor, membershipURL(target.user.ID), true, "member")
			if status != http.StatusNoContent {
				t.Fatalf("add status=%d error=%q", status, message)
			}
			status, message = teamMembershipRequest(t, allowed.actor, membershipURL(target.user.ID), false, "member")
			if status != http.StatusNoContent {
				t.Fatalf("remove status=%d error=%q", status, message)
			}
		})
	}

	for _, denied := range []struct {
		name        string
		actor       *http.Client
		wantMessage string
	}{
		{name: "non-team workspace member", actor: outsider.client, wantMessage: "Team membership required"},
		{name: "guest", actor: guest.client, wantMessage: "Guests cannot manage team settings"},
	} {
		t.Run(denied.name+" cannot manage members", func(t *testing.T) {
			status, message := teamMembershipRequest(t, denied.actor, membershipURL(target.user.ID), true, "member")
			if status != http.StatusForbidden || message != denied.wantMessage {
				t.Fatalf("status=%d error=%q", status, message)
			}
		})
	}

	t.Run("team members cannot elevate themselves or alter owners", func(t *testing.T) {
		status, message := teamMembershipRequest(t, teamMember.client, membershipURL(teamMember.user.ID), true, "owner")
		if status != http.StatusForbidden || !strings.Contains(message, "owner roles") {
			t.Fatalf("self-promotion status=%d error=%q", status, message)
		}
		status, message = teamMembershipRequest(t, teamMember.client, membershipURL(teamOwner.user.ID), false, "member")
		if status != http.StatusForbidden || !strings.Contains(message, "owner roles") {
			t.Fatalf("owner removal status=%d error=%q", status, message)
		}
	})

	t.Run("team owners can promote and demote while the last owner is protected", func(t *testing.T) {
		status, message := teamMembershipRequest(t, teamOwner.client, membershipURL(target.user.ID), true, "owner")
		if status != http.StatusNoContent {
			t.Fatalf("promotion status=%d error=%q", status, message)
		}
		status, message = teamMembershipRequest(t, teamOwner.client, membershipURL(target.user.ID), true, "member")
		if status != http.StatusNoContent {
			t.Fatalf("demotion status=%d error=%q", status, message)
		}

		solo := authRequest[domain.Team](t, admin, http.MethodPost, server.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Solo owner", "key": "SOLO"}, "", http.StatusCreated)
		status, message = teamMembershipRequest(t, admin, server.URL+"/api/workspaces/test-workspace/teams/"+solo.ID+"/members/"+bootstrap.Viewer.ID, true, "member")
		if status != http.StatusConflict || message != store.ErrLastTeamOwner.Error() {
			t.Fatalf("last-owner demotion status=%d error=%q", status, message)
		}
	})

	t.Run("members can join open teams and leave teams regardless of visibility", func(t *testing.T) {
		status, message := teamMembershipRequest(t, outsider.client, membershipURL(outsider.user.ID), true, "member")
		if status != http.StatusNoContent {
			t.Fatalf("self-join status=%d error=%q", status, message)
		}
		status, message = teamMembershipRequest(t, outsider.client, membershipURL(outsider.user.ID), false, "member")
		if status != http.StatusNoContent {
			t.Fatalf("self-leave status=%d error=%q", status, message)
		}

		privateTeam := authRequest[domain.Team](t, admin, http.MethodPost, server.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Private", "key": "PRIV", "private": true}, "", http.StatusCreated)
		privateURL := server.URL + "/api/workspaces/test-workspace/teams/" + privateTeam.ID + "/members/" + outsider.user.ID
		authRequest[any](t, admin, http.MethodPut, privateURL, map[string]any{"member": true, "role": "member"}, "", http.StatusNoContent)
		status, message = teamMembershipRequest(t, outsider.client, privateURL, false, "member")
		if status != http.StatusNoContent {
			t.Fatalf("private self-leave status=%d error=%q", status, message)
		}
		status, message = teamMembershipRequest(t, outsider.client, privateURL, true, "member")
		if status != http.StatusForbidden || message != "This team requires an invitation" {
			t.Fatalf("private self-join status=%d error=%q", status, message)
		}
	})

	t.Run("parent membership cannot be removed while a sub-team membership remains", func(t *testing.T) {
		parent := authRequest[domain.Team](t, admin, http.MethodPost, server.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Parent", "key": "PARENT"}, "", http.StatusCreated)
		child := authRequest[domain.Team](t, admin, http.MethodPost, server.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Child", "key": "CHILD", "parentTeamId": parent.ID}, "", http.StatusCreated)
		childURL := server.URL + "/api/workspaces/test-workspace/teams/" + child.ID + "/members/" + target.user.ID
		authRequest[any](t, admin, http.MethodPut, childURL, map[string]any{"member": true, "role": "member"}, "", http.StatusNoContent)
		parentURL := server.URL + "/api/workspaces/test-workspace/teams/" + parent.ID + "/members/" + target.user.ID
		status, message := teamMembershipRequest(t, admin, parentURL, false, "member")
		if status != http.StatusConflict || message != store.ErrTeamHasSubteamMembership.Error() {
			t.Fatalf("parent removal status=%d error=%q", status, message)
		}
	})

	t.Run("SCIM managed membership is visible and cannot be changed manually", func(t *testing.T) {
		managedUser, err := repository.ProvisionSCIMUser(t.Context(), bootstrap.Workspace.ID, "managed-user", "managed-user@example.test", "Managed user", "managed-user@example.test", "", "member", true)
		if err != nil {
			t.Fatal(err)
		}
		group, err := repository.CreateSCIMGroup(t.Context(), bootstrap.Workspace.ID, "managed-team-group", "Managed team", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = repository.ReplaceSCIMGroupMembers(t.Context(), bootstrap.Workspace.ID, group.ID, []string{managedUser.User.ID}); err != nil {
			t.Fatal(err)
		}
		if err = repository.SyncSCIMGroupTeam(t.Context(), bootstrap.Workspace.ID, group.ID, teamID); err != nil {
			t.Fatal(err)
		}
		status, message := teamMembershipRequest(t, admin, membershipURL(managedUser.User.ID), false, "member")
		if status != http.StatusConflict || message != store.ErrManagedTeamMembership.Error() {
			t.Fatalf("managed removal status=%d error=%q", status, message)
		}
		current := authRequest[domain.Bootstrap](t, admin, http.MethodGet, server.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
		for _, membership := range current.TeamMembers {
			if membership.TeamID == teamID && membership.UserID == managedUser.User.ID {
				if !membership.Managed || membership.ManagedSource != "scim" {
					t.Fatalf("managed projection=%#v", membership)
				}
				return
			}
		}
		t.Fatal("managed membership missing from bootstrap")
	})
}

func inviteTeamPermissionActor(t *testing.T, baseURL string, admin *http.Client, name, email, role string, teamIDs []string) teamPermissionActor {
	t.Helper()
	client, user := verifiedAuthClient(t, baseURL, name, email)
	invitations := authRequest[[]domain.Invitation](t, admin, http.MethodPost, baseURL+"/api/workspaces/test-workspace/invitations", map[string]any{
		"emails": []string{email}, "role": role, "teamIds": teamIDs,
	}, "", http.StatusCreated)
	if len(invitations) != 1 || invitations[0].Token == "" {
		t.Fatalf("invitation missing for %s", email)
	}
	authRequest[domain.WorkspaceMembership](t, client, http.MethodPost, baseURL+"/api/invitations/accept", map[string]string{"token": invitations[0].Token}, "", http.StatusOK)
	return teamPermissionActor{client: client, user: user}
}

func teamMembershipRequest(t *testing.T, client *http.Client, url string, member bool, role string) (int, string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"member": member, "role": role})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	var result map[string]string
	_ = json.Unmarshal(body, &result)
	return response.StatusCode, result["error"]
}
