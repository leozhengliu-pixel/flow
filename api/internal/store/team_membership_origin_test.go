package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func originTestStore(t *testing.T) (*SQLiteStore, domain.Bootstrap) {
	t.Helper()
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "team-origin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	return repository, repository.Bootstrap()
}

func originTestMember(t *testing.T, repository *SQLiteStore, data domain.Bootstrap, name string) domain.User {
	t.Helper()
	user, _, err := repository.Register(t.Context(), name, strings.ToLower(strings.ReplaceAll(name, " ", "-"))+"@example.test", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repository.db.ExecContext(t.Context(), `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at,last_seen_at) VALUES(?,?,'member','active','2026-01-01','2026-01-01') ON CONFLICT(workspace_id,user_id) DO NOTHING`, data.Workspace.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	return user
}

func originOf(t *testing.T, repository *SQLiteStore, workspaceID, teamID, userID string) (domain.TeamMember, bool) {
	t.Helper()
	member, found, err := repository.TeamMembership(t.Context(), workspaceID, teamID, userID)
	if err != nil {
		t.Fatal(err)
	}
	return member, found
}

func TestTeamMembershipOriginMigrationDefaultsExistingRowsToManual(t *testing.T) {
	repository, data := originTestStore(t)
	ctx := t.Context()
	user := originTestMember(t, repository, data, "Legacy member")
	// A row written the way every path wrote it before origins existed.
	if _, err := repository.db.ExecContext(ctx, `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) VALUES(?,?,?,'member','2026-01-01')`, data.Workspace.ID, data.Teams[0].ID, user.ID); err != nil {
		t.Fatal(err)
	}
	// The migration is idempotent (a second instance may run it again).
	if err := repository.addTeamMembershipOrigin(ctx); err != nil {
		t.Fatalf("re-running the origin migration: %v", err)
	}
	member, found := originOf(t, repository, data.Workspace.ID, data.Teams[0].ID, user.ID)
	if !found || member.Origin != domain.TeamMemberOriginManual {
		t.Fatalf("legacy membership origin = %q found=%v", member.Origin, found)
	}
	members, err := repository.ListTeamMembers(ctx, data.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range members {
		if item.Origin == "" {
			t.Fatalf("membership without origin: %+v", item)
		}
	}
	if domain.TeamMemberOriginOrDefault("") != domain.TeamMemberOriginManual || domain.TeamMemberOriginOrDefault("bogus") != domain.TeamMemberOriginManual {
		t.Fatal("unknown origins must read as manual")
	}
}

func TestOriginScopedRemovalDryRunAndOwnerProtection(t *testing.T) {
	repository, data := originTestStore(t)
	ctx := t.Context()
	teamID := data.Teams[0].ID
	synced := originTestMember(t, repository, data, "Synced member")
	manual := originTestMember(t, repository, data, "Manual member")

	added, err := repository.ChangeTeamMembership(ctx, TeamMembershipChangeRequest{WorkspaceID: data.Workspace.ID, TeamID: teamID, UserID: synced.ID, Member: true, Origin: domain.TeamMemberOriginSync})
	if err != nil {
		t.Fatal(err)
	}
	if len(added.Changes) != 1 || added.Changes[0].Action != domain.TeamMemberAdded || added.Changes[0].Origin != domain.TeamMemberOriginSync || added.Changes[0].TeamKey == "" {
		t.Fatalf("add changes = %+v", added.Changes)
	}
	if err := repository.SetTeamMembership(ctx, data.Workspace.ID, teamID, manual.ID, "member", true); err != nil {
		t.Fatal(err)
	}
	if member, _ := originOf(t, repository, data.Workspace.ID, teamID, manual.ID); member.Origin != domain.TeamMemberOriginManual {
		t.Fatalf("SetTeamMembership origin = %q, want manual", member.Origin)
	}

	// Dry run: reports the removal, writes nothing.
	dry, err := repository.ChangeTeamMembership(ctx, TeamMembershipChangeRequest{WorkspaceID: data.Workspace.ID, TeamID: teamID, UserID: synced.ID, RequireOrigins: []string{domain.TeamMemberOriginSync}, KeepOwners: true, DryRun: true})
	if err != nil || len(dry.Changes) != 1 || dry.Changes[0].Action != domain.TeamMemberRemoved || dry.Kept != nil {
		t.Fatalf("dry run = %+v err=%v", dry, err)
	}
	if _, found := originOf(t, repository, data.Workspace.ID, teamID, synced.ID); !found {
		t.Fatal("dry run removed the membership")
	}

	// A sync-scoped removal never touches a manual membership.
	_, err = repository.ChangeTeamMembership(ctx, TeamMembershipChangeRequest{WorkspaceID: data.Workspace.ID, TeamID: teamID, UserID: manual.ID, RequireOrigins: []string{domain.TeamMemberOriginSync}, KeepOwners: true})
	var mismatch TeamMemberOriginMismatchError
	if !errors.As(err, &mismatch) || mismatch.Origin != domain.TeamMemberOriginManual || !errors.Is(err, ErrTeamMemberOriginMismatch) {
		t.Fatalf("origin mismatch error = %v", err)
	}
	if _, found := originOf(t, repository, data.Workspace.ID, teamID, manual.ID); !found {
		t.Fatal("origin-scoped removal removed a manual member")
	}
	dryMismatch, err := repository.ChangeTeamMembership(ctx, TeamMembershipChangeRequest{WorkspaceID: data.Workspace.ID, TeamID: teamID, UserID: manual.ID, RequireOrigins: []string{domain.TeamMemberOriginSync}, DryRun: true})
	if err != nil || dryMismatch.Kept == nil || dryMismatch.Kept.Reason != domain.TeamMemberKeptOriginMismatch || dryMismatch.Kept.Origin != domain.TeamMemberOriginManual || len(dryMismatch.Changes) != 0 {
		t.Fatalf("dry-run mismatch = %+v err=%v", dryMismatch, err)
	}

	// Owners are kept by origin-scoped removals even when the origin matches.
	if _, err := repository.ChangeTeamMembership(ctx, TeamMembershipChangeRequest{WorkspaceID: data.Workspace.ID, TeamID: teamID, UserID: synced.ID, Member: true, Role: "owner"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ChangeTeamMembership(ctx, TeamMembershipChangeRequest{WorkspaceID: data.Workspace.ID, TeamID: teamID, UserID: synced.ID, RequireOrigins: []string{domain.TeamMemberOriginSync}, KeepOwners: true}); !errors.Is(err, ErrTeamOwnerProtected) {
		t.Fatalf("owner protection error = %v", err)
	}
	if member, _ := originOf(t, repository, data.Workspace.ID, teamID, synced.ID); member.Role != "owner" || member.Origin != domain.TeamMemberOriginSync {
		t.Fatalf("role change must keep the origin: %+v", member)
	}
	if _, err := repository.ChangeTeamMembership(ctx, TeamMembershipChangeRequest{WorkspaceID: data.Workspace.ID, TeamID: teamID, UserID: synced.ID, Member: true, Role: "member"}); err != nil {
		t.Fatal(err)
	}
	removed, err := repository.ChangeTeamMembership(ctx, TeamMembershipChangeRequest{WorkspaceID: data.Workspace.ID, TeamID: teamID, UserID: synced.ID, RequireOrigins: []string{domain.TeamMemberOriginSync}, KeepOwners: true})
	if err != nil || len(removed.Changes) != 1 || removed.Changes[0].Origin != domain.TeamMemberOriginSync || removed.Changes[0].RoleBefore != "member" {
		t.Fatalf("sync removal = %+v err=%v", removed, err)
	}
	if _, found := originOf(t, repository, data.Workspace.ID, teamID, synced.ID); found {
		t.Fatal("sync member was not removed")
	}

	// An explicit origin replaces the stored one (audited as origin_changed).
	claimed, err := repository.ChangeTeamMembership(ctx, TeamMembershipChangeRequest{WorkspaceID: data.Workspace.ID, TeamID: teamID, UserID: manual.ID, Member: true, Origin: domain.TeamMemberOriginSync, SetOrigin: true})
	if err != nil || len(claimed.Changes) != 1 || claimed.Changes[0].Action != domain.TeamMemberOriginChanged || claimed.Changes[0].OriginBefore != domain.TeamMemberOriginManual {
		t.Fatalf("origin change = %+v err=%v", claimed, err)
	}
	if _, err := repository.ChangeTeamMembership(ctx, TeamMembershipChangeRequest{WorkspaceID: data.Workspace.ID, TeamID: teamID, UserID: manual.ID, Member: true, Origin: "scim-ish"}); !errors.Is(err, ErrInvalidTeamMemberOrigin) {
		t.Fatalf("invalid origin error = %v", err)
	}
}

func TestTeamMembershipChangesIncludeInheritedParentMemberships(t *testing.T) {
	repository, data := originTestStore(t)
	ctx := t.Context()
	parentID, childID := data.Teams[0].ID, "team_origin_child"
	if err := repository.MutateWorkspace(ctx, data.Workspace.URLKey, "team.created", childID, nil, func(next *domain.Bootstrap) error {
		next.Teams = append(next.Teams, domain.Team{ID: childID, Name: "Child", Key: "CHLD", Color: "#123456"})
		settings := next.TeamSettings[childID]
		settings.ParentTeamID = parentID
		next.TeamSettings[childID] = settings
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	user := originTestMember(t, repository, data, "Nested member")
	result, err := repository.ChangeTeamMembership(ctx, TeamMembershipChangeRequest{WorkspaceID: data.Workspace.ID, TeamID: childID, UserID: user.ID, Member: true, Origin: domain.TeamMemberOriginSync})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changes) != 2 || result.Changes[1].TeamID != parentID || !result.Changes[1].Inherited || result.Changes[1].Origin != domain.TeamMemberOriginSync {
		t.Fatalf("changes = %+v", result.Changes)
	}
	if member, found := originOf(t, repository, data.Workspace.ID, parentID, user.ID); !found || member.Origin != domain.TeamMemberOriginSync {
		t.Fatalf("inherited parent membership = %+v found=%v", member, found)
	}
	// The parent membership stays while the sub-team membership exists.
	kept, err := repository.ChangeTeamMembership(ctx, TeamMembershipChangeRequest{WorkspaceID: data.Workspace.ID, TeamID: parentID, UserID: user.ID, RequireOrigins: []string{domain.TeamMemberOriginSync}, KeepOwners: true, DryRun: true})
	if err != nil || kept.Kept == nil || kept.Kept.Reason != domain.TeamMemberKeptSubTeamMember {
		t.Fatalf("parent removal dry run = %+v err=%v", kept, err)
	}
}

func TestRemoveMemberAndInvitationReportTeamMembershipChanges(t *testing.T) {
	repository, data := originTestStore(t)
	ctx := t.Context()
	teamID := data.Teams[0].ID
	invitee, _, err := repository.Register(ctx, "Invitee", "invitee@example.test", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	invitation, err := repository.Invite(ctx, data.Workspace.ID, data.Viewer.ID, "invitee@example.test", "member", []string{teamID})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := repository.AcceptInvitationWithChanges(ctx, invitation.Token, invitee.ID)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.InvitationID != invitation.ID || accepted.InviterID != data.Viewer.ID || len(accepted.Changes) != 1 || accepted.Changes[0].Origin != domain.TeamMemberOriginInvitation || accepted.Changes[0].Action != domain.TeamMemberAdded {
		t.Fatalf("acceptance = %+v", accepted)
	}
	if member, _ := originOf(t, repository, data.Workspace.ID, teamID, invitee.ID); member.Origin != domain.TeamMemberOriginInvitation {
		t.Fatalf("invited membership origin = %q", member.Origin)
	}
	removed, err := repository.RemoveMemberWithChanges(ctx, data.Workspace.ID, invitee.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0].TeamID != teamID || removed[0].Action != domain.TeamMemberRemoved || removed[0].Origin != domain.TeamMemberOriginInvitation {
		t.Fatalf("workspace removal changes = %+v", removed)
	}
}

func TestSCIMGroupSyncKeepsManualMembersAndReportsItsOwnChanges(t *testing.T) {
	repository, data := originTestStore(t)
	ctx := t.Context()
	teamID := data.Teams[0].ID
	manualUser, err := repository.ProvisionSCIMUser(ctx, data.Workspace.ID, "manual-1", "manual-1", "Manual", "manual-1@example.test", "", "member", true)
	if err != nil {
		t.Fatal(err)
	}
	scimUser, err := repository.ProvisionSCIMUser(ctx, data.Workspace.ID, "scim-1", "scim-1", "Provisioned", "scim-1@example.test", "", "member", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.SetTeamMembership(ctx, data.Workspace.ID, teamID, manualUser.User.ID, "member", true); err != nil {
		t.Fatal(err)
	}
	group, err := repository.CreateSCIMGroup(ctx, data.Workspace.ID, "eng", "Engineering", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repository.ReplaceSCIMGroupMembers(ctx, data.Workspace.ID, group.ID, []string{manualUser.User.ID, scimUser.User.ID}); err != nil {
		t.Fatal(err)
	}
	changes, err := repository.SyncSCIMGroupTeamChanges(ctx, data.Workspace.ID, group.ID, teamID)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].UserID != scimUser.User.ID || changes[0].Action != domain.TeamMemberAdded || changes[0].Origin != domain.TeamMemberOriginSCIM {
		t.Fatalf("SCIM add changes = %+v", changes)
	}
	members, err := repository.ListTeamMembers(ctx, data.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range members {
		if member.TeamID != teamID {
			continue
		}
		switch member.UserID {
		case scimUser.User.ID:
			if member.Origin != domain.TeamMemberOriginSCIM || !member.Managed {
				t.Fatalf("SCIM membership = %+v", member)
			}
		case manualUser.User.ID:
			if member.Origin != domain.TeamMemberOriginManual || member.Managed {
				t.Fatalf("manual membership claimed by SCIM = %+v", member)
			}
		}
	}
	// Emptying the IdP group removes only what SCIM added.
	if _, err = repository.ReplaceSCIMGroupMembers(ctx, data.Workspace.ID, group.ID, []string{}); err != nil {
		t.Fatal(err)
	}
	changes, err = repository.SyncSCIMGroupTeamChanges(ctx, data.Workspace.ID, group.ID, teamID)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].UserID != scimUser.User.ID || changes[0].Action != domain.TeamMemberRemoved {
		t.Fatalf("SCIM removal changes = %+v", changes)
	}
	if _, found := originOf(t, repository, data.Workspace.ID, teamID, manualUser.User.ID); !found {
		t.Fatal("SCIM removed a manually added member")
	}
	if _, found := originOf(t, repository, data.Workspace.ID, teamID, scimUser.User.ID); found {
		t.Fatal("SCIM did not remove the member it added")
	}
}

// The membership statements must survive the MySQL rewrite (production runs
// MySQL): ON CONFLICT ... DO NOTHING becomes INSERT IGNORE, and nothing else
// SQLite-specific remains.
func TestTeamMembershipOriginSQLRewritesForMySQL(t *testing.T) {
	statements := map[string]string{
		"INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at,origin) VALUES(?,?,?,'member',?,?) ON CONFLICT(workspace_id,team_id,user_id) DO NOTHING": "INSERT IGNORE INTO team_memberships(workspace_id,team_id,user_id,role,joined_at,origin) VALUES(?,?,?,'member',?,?)",
		"INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at,origin) VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING":                                      "INSERT IGNORE INTO team_memberships(workspace_id,team_id,user_id,role,joined_at,origin) VALUES(?,?,?,?,?,?)",
		"UPDATE team_memberships SET role=?,origin=? WHERE workspace_id=? AND team_id=? AND user_id=?":                                                                     "UPDATE team_memberships SET role=?,origin=? WHERE workspace_id=? AND team_id=? AND user_id=?",
		"ALTER TABLE team_memberships ADD COLUMN origin VARCHAR(16) NOT NULL DEFAULT 'manual'":                                                                             "ALTER TABLE team_memberships ADD COLUMN origin VARCHAR(16) NOT NULL DEFAULT 'manual'",
	}
	for input, want := range statements {
		if got := rewriteSQL(input, "mysql"); got != want {
			t.Errorf("rewriteSQL(%q) = %q, want %q", input, got, want)
		}
	}
}
