package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestAccountAuthenticationAndSessionLifecycle(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := t.Context()
	user, verificationToken, err := repository.Register(ctx, "New Member", "member2@example.test", "initial-password")
	if err != nil || user.ID == "" || verificationToken == "" {
		t.Fatalf("register user=%#v token=%q err=%v", user, verificationToken, err)
	}
	if _, _, err := repository.Login(ctx, user.Email, "initial-password"); err == nil {
		t.Fatalf("unverified login error=%v", err)
	}
	if err := repository.VerifyEmail(ctx, verificationToken); err != nil {
		t.Fatal(err)
	}
	session, token, err := repository.Login(ctx, user.Email, "initial-password")
	if err != nil || token == "" || session.User.ID != user.ID {
		t.Fatalf("login session=%#v token=%q err=%v", session, token, err)
	}
	authenticated, err := repository.AuthenticateSession(ctx, token)
	if err != nil || authenticated.ID != user.ID {
		t.Fatalf("authenticated=%#v err=%v", authenticated, err)
	}
	loadedSession, err := repository.Session(ctx, token)
	if err != nil || loadedSession.User.ID != user.ID {
		t.Fatalf("session=%#v err=%v", loadedSession, err)
	}
	if !repository.EnforceSessionDuration(ctx, token, 30) {
		t.Fatal("valid session duration was rejected")
	}
	updated, err := repository.UpdateProfile(ctx, user.ID, "Updated Member", "updated.member", "https://example.test/avatar.png")
	if err != nil || updated.DisplayName != "Updated Member" || updated.Name != "updated.member" {
		t.Fatalf("profile=%#v err=%v", updated, err)
	}
	updated, err = repository.UpdateMemberIdentity(ctx, user.ID, "Workspace Member", "workspace.member", "workspace.member@example.test")
	if err != nil || updated.Email != "workspace.member@example.test" {
		t.Fatalf("identity=%#v err=%v", updated, err)
	}
	if err := repository.ChangePassword(ctx, user.ID, "wrong-password", "next-password"); !errors.Is(err, ErrAuthInvalid) {
		t.Fatalf("wrong password error=%v", err)
	}
	if err := repository.ChangePassword(ctx, user.ID, "initial-password", "next-password"); err != nil {
		t.Fatal(err)
	}
	secondSession, secondToken, err := repository.Login(ctx, updated.Email, "next-password")
	if err != nil || secondSession.User.ID != user.ID {
		t.Fatalf("second login session=%#v err=%v", secondSession, err)
	}
	sessions, err := repository.ListSessions(ctx, user.ID, secondToken)
	if err != nil || len(sessions) < 2 {
		t.Fatalf("sessions=%#v err=%v", sessions, err)
	}
	for _, accountSession := range sessions {
		if !accountSession.Current {
			if err := repository.RevokeSession(ctx, user.ID, accountSession.ID, secondToken); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if err := repository.RevokeOtherSessions(ctx, user.ID, secondToken); err != nil {
		t.Fatal(err)
	}
	sessions, err = repository.ListSessions(ctx, user.ID, secondToken)
	if err != nil || len(sessions) != 1 || !sessions[0].Current {
		t.Fatalf("sessions after revoke=%#v err=%v", sessions, err)
	}
	if err := repository.Logout(ctx, secondToken); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.AuthenticateSession(ctx, secondToken); !errors.Is(err, ErrAuthInvalid) {
		t.Fatalf("logged-out session error=%v", err)
	}
}

func TestAccountWorkspaceMembershipAndRecoveryLifecycle(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "membership.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := t.Context()
	data := repository.Bootstrap()
	user, _, err := repository.Register(ctx, "Invited Member", "invited@example.test", "initial-password")
	if err != nil {
		t.Fatal(err)
	}
	verificationToken, err := repository.RequestEmailVerification(ctx, user.Email)
	if err != nil || verificationToken == "" {
		t.Fatalf("verification token=%q err=%v", verificationToken, err)
	}
	if err := repository.VerifyEmail(ctx, verificationToken); err != nil {
		t.Fatal(err)
	}
	if err := repository.EnsureWorkspaceMembership(ctx, data.Workspace.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	role, status, err := repository.WorkspaceRole(ctx, data.Workspace.ID, user.ID)
	if err != nil || role != "member" || status != "active" {
		t.Fatalf("workspace role=%q status=%q err=%v", role, status, err)
	}
	if err := repository.SetTeamMembership(ctx, data.Workspace.ID, data.Teams[0].ID, user.ID, "member", true); err != nil {
		t.Fatal(err)
	}
	if role, err := repository.TeamRole(ctx, data.Workspace.ID, data.Teams[0].ID, user.ID); err != nil || role != "member" {
		t.Fatalf("team role=%q err=%v", role, err)
	}
	account, err := repository.AccountForUser(ctx, user.ID)
	if err != nil || len(account.Workspaces) != 1 {
		t.Fatalf("account=%#v err=%v", account, err)
	}
	if err := repository.SetLastWorkspace(ctx, user.ID, data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	projected, ok, err := repository.BootstrapForUser(ctx, data.Workspace.URLKey, user.ID)
	if err != nil || !ok || projected.Viewer.ID != user.ID {
		t.Fatalf("projected=%#v ok=%v err=%v", projected.Viewer, ok, err)
	}
	resetToken, err := repository.RequestPasswordReset(ctx, user.Email)
	if err != nil || resetToken == "" {
		t.Fatalf("reset token=%q err=%v", resetToken, err)
	}
	if err := repository.ResetPassword(ctx, resetToken, "recovered-password"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.Login(ctx, user.Email, "recovered-password"); err != nil {
		t.Fatal(err)
	}
	if err := repository.SetTeamMembership(ctx, data.Workspace.ID, data.Teams[0].ID, user.ID, "member", false); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.TeamRole(ctx, data.Workspace.ID, data.Teams[0].ID, user.ID); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("removed team membership error=%v", err)
	}
}

func TestWorkspaceInvitationAndMemberAdministrationLifecycle(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "member-admin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := t.Context()
	data := repository.Bootstrap()
	workspaceID := data.Workspace.ID
	teamID := data.Teams[0].ID
	adminID := data.Viewer.ID

	if _, err := repository.Invite(ctx, workspaceID, adminID, "guest@example.test", "guest", nil); err == nil {
		t.Fatal("guest invitation without a team was accepted")
	}
	member, _, err := repository.Register(ctx, "Invited user", "invitee@example.test", "initial-password")
	if err != nil {
		t.Fatal(err)
	}
	invitation, err := repository.Invite(ctx, workspaceID, adminID, member.Email, "member", []string{teamID})
	if err != nil || invitation.Token == "" {
		t.Fatalf("invitation=%#v err=%v", invitation, err)
	}
	preview, workspace, err := repository.InvitationPreview(ctx, invitation.Token)
	if err != nil || preview.ID != invitation.ID || workspace.ID != workspaceID {
		t.Fatalf("preview=%#v workspace=%#v err=%v", preview, workspace, err)
	}
	invitations, err := repository.ListInvitations(ctx, workspaceID)
	if err != nil || len(invitations) != 1 || invitations[0].Status != "pending" {
		t.Fatalf("invitations=%#v err=%v", invitations, err)
	}
	resent, err := repository.ResendInvitation(ctx, workspaceID, invitation.ID)
	if err != nil || resent.ID == invitation.ID || resent.Token == invitation.Token {
		t.Fatalf("resent=%#v err=%v", resent, err)
	}
	if _, _, err := repository.InvitationPreview(ctx, invitation.Token); !errors.Is(err, ErrAuthExpired) {
		t.Fatalf("revoked invitation preview error=%v", err)
	}
	membership, err := repository.AcceptInvitation(ctx, resent.Token, member.ID)
	if err != nil || membership.Workspace.ID != workspaceID || membership.Role != "Member" {
		t.Fatalf("membership=%#v err=%v", membership, err)
	}
	members, err := repository.ListMembers(ctx, workspaceID)
	if err != nil || len(members) != 3 {
		t.Fatalf("members=%#v err=%v", members, err)
	}
	teamMembers, err := repository.ListTeamMembers(ctx, workspaceID)
	if err != nil || len(teamMembers) < 2 {
		t.Fatalf("team members=%#v err=%v", teamMembers, err)
	}
	for _, fixtureUser := range data.Users {
		if fixtureUser.ID != adminID {
			if err := repository.UpdateMemberRole(ctx, workspaceID, fixtureUser.ID, "member"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := repository.SuspendMember(ctx, workspaceID, adminID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("last administrator suspension error=%v", err)
	}
	if err := repository.UpdateMemberRole(ctx, workspaceID, member.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := repository.SuspendMember(ctx, workspaceID, member.ID); err != nil {
		t.Fatal(err)
	}
	if _, status, err := repository.WorkspaceRole(ctx, workspaceID, member.ID); err != nil || status != "suspended" {
		t.Fatalf("suspended status=%q err=%v", status, err)
	}
	if err := repository.ResumeMember(ctx, workspaceID, member.ID); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateMemberRole(ctx, workspaceID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	if err := repository.RemoveMember(ctx, workspaceID, member.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.WorkspaceRole(ctx, workspaceID, member.ID); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("removed member role error=%v", err)
	}

	revoked, err := repository.Invite(ctx, workspaceID, adminID, "revoked@example.test", "member", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.RevokeInvitation(ctx, workspaceID, revoked.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.InvitationPreview(ctx, revoked.Token); !errors.Is(err, ErrAuthExpired) {
		t.Fatalf("revoked invitation error=%v", err)
	}
	if err := repository.DeleteTeamMemberships(ctx, workspaceID, teamID); err != nil {
		t.Fatal(err)
	}
}
