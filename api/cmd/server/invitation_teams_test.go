package main

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"flow/api/internal/domain"
)

// An invitation of any role names the teams the invitee joins on accepting. Workspace admins may use any team;
// anyone else is held to the rule for adding a member to a team directly, so a member who may invite cannot put
// someone into a private team they are not on.
func TestInvitationTeamsApplyToEveryRoleAndRespectTheInviter(t *testing.T) {
	server := newSingleAuthServer(t)
	admin := peerAdmin(t, server.URL)
	base := server.URL + "/api/workspaces/test-workspace"
	open := authRequest[domain.Team](t, admin, http.MethodPost, base+"/teams", map[string]any{"name": "Invite Open", "key": "IOPN"}, "", http.StatusCreated)
	other := authRequest[domain.Team](t, admin, http.MethodPost, base+"/teams", map[string]any{"name": "Invite Other", "key": "IOTH"}, "", http.StatusCreated)
	private := authRequest[domain.Team](t, admin, http.MethodPost, base+"/teams", map[string]any{"name": "Invite Private", "key": "IPRV", "private": true}, "", http.StatusCreated)
	authRequest[domain.WorkspaceSettings](t, admin, http.MethodPatch, server.URL+"/api/workspace/preferences", map[string]any{"invitePermission": "members"}, "", http.StatusOK)

	teamsOf := func(userID string) []string {
		bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, server.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
		teams := []string{}
		for _, member := range bootstrap.TeamMembers {
			if member.UserID == userID {
				teams = append(teams, member.TeamID)
			}
		}
		slices.Sort(teams)
		return teams
	}
	invitedByAdmin := func(role string, emails []string, teams ...string) []domain.Invitation {
		return authRequest[[]domain.Invitation](t, admin, http.MethodPost, base+"/invitations", map[string]any{"emails": emails, "role": role, "teamIds": teams}, "", http.StatusCreated)
	}
	join := func(name, email, role string, teams ...string) (*http.Client, domain.User) {
		client, user := verifiedAuthClient(t, server.URL, name, email)
		invitations := invitedByAdmin(role, []string{user.Email}, teams...)
		authRequest[domain.WorkspaceMembership](t, client, http.MethodPost, server.URL+"/api/invitations/accept", map[string]string{"token": invitations[0].Token}, "", http.StatusOK)
		return client, user
	}

	// A Member (and an Admin) invitation carries its teams, private ones included for an admin inviter.
	_, member := join("Invited Member", "invited-member@example.com", "member", open.ID, private.ID)
	want := []string{open.ID, private.ID}
	slices.Sort(want)
	if teams := teamsOf(member.ID); !slices.Equal(teams, want) {
		t.Fatalf("a member invited with teams joins them on accepting: %v, want %v", teams, want)
	}
	_, admin2 := join("Invited Admin", "invited-admin@example.com", "admin", other.ID)
	if teams := teamsOf(admin2.ID); !slices.Equal(teams, []string{other.ID}) {
		t.Fatalf("an admin invited with a team joins it: %v", teams)
	}
	repeated := invitedByAdmin("member", []string{"repeat@example.com"}, open.ID, open.ID, "")
	if !slices.Equal(repeated[0].TeamIDs, []string{open.ID}) {
		t.Fatalf("repeated and empty team ids are dropped: %v", repeated[0].TeamIDs)
	}

	// A member who may invite is on the open team, not the private one.
	inviterClient, inviter := join("Inviter", "inviter@example.com", "member", open.ID)
	sent := 0
	invite := func(status int, teams ...string) {
		sent++
		authRequest[any](t, inviterClient, http.MethodPost, base+"/invitations", map[string]any{"emails": []string{fmt.Sprintf("from-member-%d@example.com", sent)}, "role": "member", "teamIds": teams}, "", status)
	}
	invite(http.StatusCreated, open.ID)
	invite(http.StatusForbidden, private.ID)
	invite(http.StatusForbidden, open.ID, private.ID)
	// A team that is not in the workspace is not a team.
	invite(http.StatusBadRequest, "team_missing")
	// Nothing was left behind by the refused invitations.
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, server.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	for _, pending := range bootstrap.Invitations {
		if pending.InviterID == inviter.ID && slices.Contains(pending.TeamIDs, private.ID) {
			t.Fatalf("a refused invitation was stored: %+v", pending)
		}
	}
}
