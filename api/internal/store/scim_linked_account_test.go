package store

import (
	"errors"
	"testing"
)

// SCIM must not enrol an account it does not control: an existing account
// matched by email gets an invitation, not a membership, and the enterprise
// sign-in rule that links by trusted email keeps refusing it until the
// account's owner accepts.
func TestSCIMExistingAccountJoinsOnlyByAcceptingAnInvitation(t *testing.T) {
	f := newIdentityLinkFixture(t)
	ctx := t.Context()
	if _, err := f.repository.db.ExecContext(ctx, `DELETE FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, f.workspaceID, f.victimID); err != nil {
		t.Fatal(err)
	}
	policy := ExternalLinkPolicy{TrustedEmail: true, MembershipWorkspaceID: f.workspaceID}
	var nameBefore string
	if err := f.repository.db.QueryRowContext(ctx, `SELECT name FROM auth_users WHERE id=?`, f.victimID).Scan(&nameBefore); err != nil {
		t.Fatal(err)
	}

	item, err := f.repository.CreateSCIMUser(ctx, f.workspaceID, SCIMUserInput{UserName: "victim@example.com", ExternalID: "emp-9", DisplayName: "Renamed By Directory", Role: "admin", RoleExplicit: true, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if !item.Linked || !item.LinkedExisting || item.MemberStatus != "" || item.Active() || item.Invitation == nil || item.Invitation.Token == "" || item.Invitation.Email != "victim@example.com" {
		t.Fatalf("created = %+v", item)
	}
	if item.User.DisplayName == nameBefore && nameBefore != "Renamed By Directory" {
		// the resource shows the directory's own name for an account that has not joined
		t.Fatalf("resource leaked the account's name: %q", item.User.DisplayName)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, f.workspaceID, f.victimID); n != 0 {
		t.Fatalf("SCIM created %d memberships", n)
	}
	var name string
	var active int
	if err := f.repository.db.QueryRowContext(ctx, `SELECT name,active FROM auth_users WHERE id=?`, f.victimID).Scan(&name, &active); err != nil || name != nameBefore || active != 1 {
		t.Fatalf("SCIM rewrote the account: name=%q active=%d err=%v", name, active, err)
	}
	// Later writes from the directory keep the account untouched and not enrolled.
	if _, err := f.repository.UpdateSCIMUser(ctx, f.workspaceID, f.victimID, SCIMUserInput{UserName: "victim@example.com", ExternalID: "emp-9", DisplayName: "Renamed Again", Active: false}); err != nil {
		t.Fatal(err)
	}
	if err := f.repository.db.QueryRowContext(ctx, `SELECT name,active FROM auth_users WHERE id=?`, f.victimID).Scan(&name, &active); err != nil || name != nameBefore || active != 1 {
		t.Fatalf("SCIM update rewrote the account: name=%q active=%d err=%v", name, active, err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, f.workspaceID, f.victimID); n != 0 {
		t.Fatalf("a SCIM update created %d memberships", n)
	}
	// The pending state is not "an active member": the trusted-email link is refused.
	if _, _, err := f.repository.LoginExternalIdentityWithPolicy(ctx, "enterprise:corp", "https://idp.corp.example", "corp-sub", "victim", "victim@example.com", "Victim", "", `{}`, false, policy); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("pending account was linked by the enterprise sign-in: %v", err)
	}
	// Accepting while signed in as the account is what makes it a member.
	if _, err := f.repository.AcceptInvitation(ctx, item.Invitation.Token, f.victimID); err != nil {
		t.Fatal(err)
	}
	read, err := f.repository.SCIMUser(ctx, f.workspaceID, f.victimID)
	if err != nil || read.MemberStatus != "active" || !read.Active() || read.Role != "admin" {
		t.Fatalf("after acceptance: %+v err=%v", read, err)
	}
	if _, _, err := f.repository.LoginExternalIdentityWithPolicy(ctx, "enterprise:corp", "https://idp.corp.example", "corp-sub", "victim", "victim@example.com", "Victim", "", `{}`, false, policy); err != nil {
		t.Fatalf("accepted member was refused: %v", err)
	}
}

// New accounts are provisioned as before, and an existing account that is
// already a member of the workspace is linked without an invitation.
func TestSCIMNewAndAlreadyMemberAccountsUnchanged(t *testing.T) {
	f := newIdentityLinkFixture(t)
	ctx := t.Context()
	created, err := f.repository.CreateSCIMUser(ctx, f.workspaceID, SCIMUserInput{UserName: "fresh@example.com", ExternalID: "emp-1", Active: true})
	if err != nil || created.Linked || created.Invitation != nil || created.MemberStatus != "active" || !created.Active() {
		t.Fatalf("new user = %+v err=%v", created, err)
	}
	member, err := f.repository.CreateSCIMUser(ctx, f.workspaceID, SCIMUserInput{UserName: "victim@example.com", ExternalID: "emp-2", Active: true})
	if err != nil || !member.Linked || member.Invitation != nil || member.MemberStatus != "active" || member.User.ID != f.victimID {
		t.Fatalf("existing member = %+v err=%v", member, err)
	}
}
