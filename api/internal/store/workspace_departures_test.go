package store

import (
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
)

// A removed member is listed as departed (the reference app's "Left workspace") with the
// role and join date they had, only to admins, until they join again.
func TestRemovedMemberIsListedAsDepartedUntilTheyRejoin(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "departures.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := t.Context()
	data := repository.Bootstrap()
	workspaceID, adminID := data.Workspace.ID, data.Viewer.ID

	member, _, err := repository.Register(ctx, "Leaving user", "leaving@example.test", "initial-password")
	if err != nil {
		t.Fatal(err)
	}
	invitation, err := repository.Invite(ctx, workspaceID, adminID, member.Email, "guest", []string{data.Teams[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.AcceptInvitation(ctx, invitation.Token, member.ID); err != nil {
		t.Fatal(err)
	}
	departed, err := repository.ListDepartedMembers(ctx, workspaceID, data.Workspace.URLKey)
	if err != nil || len(departed) != 0 {
		t.Fatalf("departed before removal=%#v err=%v", departed, err)
	}
	members, _ := repository.ListMembers(ctx, workspaceID)
	joined := members[slices.IndexFunc(members, func(item domain.WorkspaceMember) bool { return item.User.ID == member.ID })].JoinedAt

	if err := repository.RemoveMember(ctx, workspaceID, member.ID); err != nil {
		t.Fatal(err)
	}
	departed, err = repository.ListDepartedMembers(ctx, workspaceID, data.Workspace.URLKey)
	if err != nil || len(departed) != 1 {
		t.Fatalf("departed=%#v err=%v", departed, err)
	}
	got := departed[0]
	if got.User.ID != member.ID || got.User.Email != member.Email || got.User.DisplayName != "Leaving user" || got.User.Active ||
		got.Role != "guest" || got.Status != MemberStatusDeparted || !got.JoinedAt.Equal(joined) || got.LastSeenAt == nil || got.LastSeenAt.Before(joined) {
		t.Fatalf("departed member=%#v", got)
	}

	admin, ok, err := repository.BootstrapForUser(ctx, data.Workspace.URLKey, adminID)
	if err != nil || !ok || len(admin.DepartedMembers) != 1 || admin.DepartedMembers[0].User.ID != member.ID {
		t.Fatalf("admin bootstrap departed=%#v ok=%v err=%v", admin.DepartedMembers, ok, err)
	}
	if slices.ContainsFunc(admin.Members, func(item domain.WorkspaceMember) bool { return item.User.ID == member.ID }) {
		t.Fatal("departed user still listed as a member")
	}
	other, _, err := repository.Register(ctx, "Plain member", "plain@example.test", "initial-password")
	if err != nil {
		t.Fatal(err)
	}
	invitation, err = repository.Invite(ctx, workspaceID, adminID, other.Email, "member", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.AcceptInvitation(ctx, invitation.Token, other.ID); err != nil {
		t.Fatal(err)
	}
	if view, ok, err := repository.BootstrapForUser(ctx, data.Workspace.URLKey, other.ID); err != nil || !ok || len(view.DepartedMembers) != 0 {
		t.Fatalf("member bootstrap departed=%#v ok=%v err=%v", view.DepartedMembers, ok, err)
	}

	invitation, err = repository.Invite(ctx, workspaceID, adminID, member.Email, "member", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.AcceptInvitation(ctx, invitation.Token, member.ID); err != nil {
		t.Fatal(err)
	}
	if departed, err := repository.ListDepartedMembers(ctx, workspaceID, data.Workspace.URLKey); err != nil || len(departed) != 0 {
		t.Fatalf("rejoined member still departed=%#v err=%v", departed, err)
	}
}
