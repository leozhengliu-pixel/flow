package store

import (
	"flow/api/internal/domain"
	"path/filepath"
	"slices"
	"testing"
)

func TestTeamHierarchyMembershipAndPrivateBoundary(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	data := repository.Bootstrap()
	ctx := t.Context()
	root := domain.Team{ID: "private-root", Private: true}
	child := domain.Team{ID: "public-child"}
	grandchild := domain.Team{ID: "grandchild"}
	err = repository.MutateWorkspace(ctx, data.Workspace.URLKey, "test.hierarchy", root.ID, nil, func(next *domain.Bootstrap) error {
		next.Teams = append(next.Teams, root, child, grandchild)
		next.TeamSettings[root.ID] = domain.TeamSettings{TeamID: root.ID, Access: "private"}
		next.TeamSettings[child.ID] = domain.TeamSettings{TeamID: child.ID, ParentTeamID: root.ID, Access: "public"}
		next.TeamSettings[grandchild.ID] = domain.TeamSettings{TeamID: grandchild.ID, ParentTeamID: child.ID, Access: "public"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.SetTeamMembership(ctx, data.Workspace.ID, grandchild.ID, data.Viewer.ID, "owner", true); err != nil {
		t.Fatal(err)
	}
	members, err := repository.ListTeamMembers(ctx, data.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{root.ID, child.ID, grandchild.ID} {
		index := slices.IndexFunc(members, func(member domain.TeamMember) bool { return member.TeamID == id && member.UserID == data.Viewer.ID })
		if index < 0 {
			t.Fatalf("membership did not propagate to %s", id)
		}
		if id != grandchild.ID && members[index].Role != "member" {
			t.Fatal("ancestor membership escalated to owner")
		}
	}
	if err = repository.SetTeamMembership(ctx, data.Workspace.ID, child.ID, data.Viewer.ID, "member", false); err == nil {
		t.Fatal("removed ancestor membership while user belongs to descendant")
	}
	projection, _ := repository.WorkspaceMetadata(data.Workspace.URLKey)
	projection.TeamMembers = members
	if teamVisibleToUser(projection, child.ID, "outsider", "member") {
		t.Fatal("public child exposed private parent boundary")
	}
	projection.TeamMembers = []domain.TeamMember{{TeamID: root.ID, UserID: "parent-member", Role: "member"}}
	if !teamVisibleToUser(projection, grandchild.ID, "parent-member", "member") {
		t.Fatal("parent member cannot see restricted descendant")
	}
	childSettings := projection.TeamSettings[child.ID]
	childSettings.Access = "restricted"
	projection.TeamSettings[child.ID] = childSettings
	if !teamVisibleToUser(projection, child.ID, "parent-member", "member") {
		t.Fatal("explicit restricted visibility ignored parent membership")
	}
	if !teamVisibleToUser(projection, grandchild.ID, "parent-member", "member") {
		t.Fatal("restricted intermediate team created an extra permission boundary")
	}
	projection.Teams = append(projection.Teams, domain.Team{ID: "private-child", Private: true})
	projection.TeamSettings["private-child"] = domain.TeamSettings{Access: "private", ParentTeamID: root.ID}
	if teamVisibleToUser(projection, "private-child", "parent-member", "member") {
		t.Fatal("private child should require its own membership")
	}
	projection.TeamMembers = append(projection.TeamMembers, domain.TeamMember{TeamID: root.ID, UserID: "parent-owner", Role: "owner"})
	if teamVisibleToUser(projection, "private-child", "parent-owner", "member") {
		t.Fatal("parent owner should not bypass a private child's direct membership boundary")
	}
	if slices.Contains(visibleIssueTeams(projection, "parent-owner", "member"), "private-child") {
		t.Fatal("issue visibility should not bypass a private child for parent owners")
	}
	guest, _, err := repository.Register(ctx, "Guest", "subteam-guest@example.test", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.ExecContext(ctx, `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at,last_seen_at) VALUES(?,?,'guest','active','2026-01-01','2026-01-01') ON CONFLICT(workspace_id,user_id) DO UPDATE SET role='guest',status='active'`, data.Workspace.ID, guest.ID); err != nil {
		t.Fatal(err)
	}
	if err := repository.SetTeamMembership(ctx, data.Workspace.ID, grandchild.ID, guest.ID, "member", true); err != nil {
		t.Fatal(err)
	}
	if repository.teamRoleDirect(ctx, data.Workspace.ID, root.ID, guest.ID) != "" || repository.teamRoleDirect(ctx, data.Workspace.ID, child.ID, guest.ID) != "" {
		t.Fatal("guest was automatically added to ancestor teams")
	}
	if err := repository.MutateWorkspace(ctx, data.Workspace.URLKey, "team.created", "other", nil, func(next *domain.Bootstrap) error {
		next.Teams = append(next.Teams, domain.Team{ID: "other"})
		next.TeamSettings["other"] = domain.TeamSettings{TeamID: "other"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.SetTeamMembership(ctx, data.Workspace.ID, "other", data.Viewer.ID, "owner", true); err != nil {
		t.Fatal(err)
	}
	if err := repository.MutateWorkspace(ctx, data.Workspace.URLKey, "team.settings_updated", child.ID, map[string]string{"parentTeamId": "other"}, func(next *domain.Bootstrap) error {
		settings := next.TeamSettings[child.ID]
		settings.ParentTeamID = "other"
		next.TeamSettings[child.ID] = settings
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if repository.teamRoleDirect(ctx, data.Workspace.ID, "other", data.Viewer.ID) != "owner" {
		t.Fatal("reparenting replaced existing owner role")
	}
	if repository.teamRoleDirect(ctx, data.Workspace.ID, "other", guest.ID) != "" {
		t.Fatal("reparenting propagated a guest")
	}
}
