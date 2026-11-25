package store

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW181RoleInputs(data domain.Bootstrap, document domain.Document) domain.Bootstrap {
	result := domain.Bootstrap{
		Workspace: domain.Workspace{ID: data.Workspace.ID, URLKey: data.Workspace.URLKey},
		Viewer: domain.User{
			ID: data.Viewer.ID, App: data.Viewer.App, Active: data.Viewer.Active,
			AppTeamIDs: data.Viewer.AppTeamIDs, AppScopes: data.Viewer.AppScopes,
		},
		ViewerRole: data.ViewerRole, ViewerTeamIDs: data.ViewerTeamIDs,
		TeamSettings: map[string]domain.TeamSettings{},
	}
	if data.Teams != nil {
		result.Teams = make([]domain.Team, len(data.Teams))
	}
	for index, team := range data.Teams {
		result.Teams[index] = domain.Team{
			ID: team.ID, Key: team.Key, ExternalSource: team.ExternalSource, Private: team.Private,
		}
		if team.ArchivedAt != nil {
			result.Teams[index].ArchivedAt = new(time.Time)
		}
		if team.RetiredAt != nil {
			result.Teams[index].RetiredAt = new(time.Time)
		}
		seen := map[string]bool{}
		for id := team.ID; id != "" && !seen[id]; id = data.TeamSettings[id].ParentTeamID {
			seen[id] = true
			settings := data.TeamSettings[id]
			result.TeamSettings[id] = domain.TeamSettings{
				ParentTeamID: settings.ParentTeamID, Access: settings.Access,
				MembershipRestriction: settings.MembershipRestriction,
			}
		}
	}
	if data.TeamMembers != nil {
		result.TeamMembers = make([]domain.TeamMember, len(data.TeamMembers))
	}
	for index, member := range data.TeamMembers {
		result.TeamMembers[index] = domain.TeamMember{
			UserID: member.UserID, TeamID: member.TeamID, Role: member.Role,
		}
	}
	header := domain.Document{
		ID: document.ID, SlugID: document.SlugID, CollaborationID: document.CollaborationID,
		Creator: domain.User{ID: document.Creator.ID},
		TeamIDs: document.TeamIDs, InheritedTeamIDs: document.InheritedTeamIDs,
	}
	if document.Permissions != nil {
		header.Permissions = make([]domain.DocumentPermission, len(document.Permissions))
	}
	for index, permission := range document.Permissions {
		header.Permissions[index] = domain.DocumentPermission{
			SubjectType: permission.SubjectType, SubjectID: permission.SubjectID, Role: permission.Role,
		}
	}
	result.Documents = []domain.Document{header}
	return result
}

func TestScaleP21SW181CollaborationBorrowedSnapshotDropsTransientTeams(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	prefix := idgen.New("sw181-")
	teamID, documentID, collaborationID := prefix+"team", prefix+"document", prefix+"generation"
	actor, token, err := f.Register(t.Context(), "SW181 collaboration member", prefix+"@example.test", "sw181-fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := f.ErasePersonFromWorkspace(ctx, workspace, actor.ID); err != nil {
			t.Error("clean up owned collaboration member", err)
		}
		if err := f.EraseAccount(ctx, actor.ID); err != nil {
			t.Error("clean up owned collaboration identity", err)
		}
	})
	if err := f.VerifyEmail(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	if err := f.EnsureWorkspaceMembership(t.Context(), workspaceID, actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw181_fixture", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: teamID, Key: prefix + "PUBLIC", Name: "SW181 public team"})
		data.TeamSettings[teamID] = domain.TeamSettings{TeamID: teamID, Access: "public"}
		data.Documents = append(data.Documents, domain.Document{
			ID: documentID, SlugID: prefix + "current", CollaborationID: collaborationID,
			Creator: f.data.Viewer, TeamIDs: []string{teamID},
			Permissions: []domain.DocumentPermission{
				{SubjectType: "workspace", SubjectID: workspaceID, Role: "viewer"},
				{SubjectType: "team", SubjectID: teamID, Role: "editor"},
				{SubjectType: "user", SubjectID: f.data.Viewer.ID, Role: "owner"},
			},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.SetTeamMembership(t.Context(), workspaceID, teamID, actor.ID, "member", false); err != nil {
		t.Fatal(err)
	}
	var membershipCount int
	if err := f.db.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM team_memberships WHERE workspace_id=? AND team_id=? AND user_id=?`,
		workspaceID, teamID, actor.ID).Scan(&membershipCount); err != nil || membershipCount != 0 {
		t.Fatal("regression actor unexpectedly has the editor grant's SQL membership", membershipCount, err)
	}
	for _, mode := range []struct {
		name   string
		reload bool
	}{{"warm", false}, {"reloaded", true}} {
		t.Run(mode.name, func(t *testing.T) {
			view := scaleP21IssueAuthorizationView(t, f, mode.reload)
			caller, found := f.WorkspaceMetadata(workspace)
			if !found {
				t.Fatal("owned caller snapshot missing")
			}
			caller.ViewerTeamIDs = []string{teamID}
			tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{
				database: f.db, workspace: workspace, reader: tx, metadata: caller,
			})
			legacy, err := f.PagedWorkspaceMetadata(ctx, workspace, actor.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(legacy.ViewerTeamIDs) != 0 {
				t.Fatal("literal paged clone did not clear json-omitted transient teams", legacy.ViewerTeamIDs)
			}
			index := slices.IndexFunc(legacy.Documents, func(document domain.Document) bool {
				return document.CollaborationID == collaborationID
			})
			if index < 0 {
				t.Fatal("public document witness was hidden by the literal projection")
			}
			document := legacy.Documents[index]
			read, err := view.BeginCollaborationDocumentRead(ctx, workspace, actor.ID, collaborationID, nil)
			if err != nil {
				t.Fatal("borrowed collaboration reader failed", err)
			}
			defer read.Close()
			if !read.Found || read.Conflict || read.Document.ID != documentID || len(read.Data.Documents) != 1 {
				t.Fatal("bounded selector lost the actual public document witness", read.Found, read.Conflict, read.Document)
			}
			if len(read.Data.ViewerTeamIDs) != 0 {
				t.Fatal("bounded collaboration leaked transient caller memberships", read.Data.ViewerTeamIDs)
			}
			want, got := scaleP21SW181RoleInputs(legacy, document), scaleP21SW181RoleInputs(read.Data, read.Document)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("literal documentRole inputs differ:\nlegacy=%#v\nbounded=%#v", want, got)
			}
			if !reflect.DeepEqual(read.Document, got.Documents[0]) {
				t.Fatal("bounded selected header retained undeclared document fields")
			}
			if legacy.ViewerRole != "member" || legacy.Viewer.App || document.Creator.ID == actor.ID ||
				len(document.InheritedTeamIDs) != 0 || len(document.Permissions) != 3 ||
				document.Permissions[0].SubjectType != "workspace" || document.Permissions[0].SubjectID != workspaceID ||
				document.Permissions[0].Role != "viewer" || document.Permissions[1].SubjectType != "team" ||
				document.Permissions[1].SubjectID != teamID || document.Permissions[1].Role != "editor" ||
				document.Permissions[2].SubjectType != "user" || document.Permissions[2].SubjectID != document.Creator.ID ||
				document.Permissions[2].Role != "owner" {
				t.Fatal("witness no longer isolates the workspace-viewer versus team-editor documentRole branches")
			}
			// documentRole's only editor grant in this witness is the team
			// permission. Match exactly the viewerOnTeam inputs it consumes.
			editorGrantApplies := func(data domain.Bootstrap) bool {
				return slices.Contains(data.ViewerTeamIDs, teamID) ||
					slices.ContainsFunc(data.TeamMembers, func(member domain.TeamMember) bool {
						return member.UserID == data.Viewer.ID && member.TeamID == teamID
					})
			}
			leaked := read.Data
			leaked.ViewerTeamIDs = caller.ViewerTeamIDs
			if editorGrantApplies(legacy) || editorGrantApplies(read.Data) || !editorGrantApplies(leaked) {
				t.Fatal("transient-team witness no longer distinguishes viewer from an incorrectly granted editor")
			}
			if !reflect.DeepEqual(caller.ViewerTeamIDs, []string{teamID}) {
				t.Fatal("reader mutated the caller-owned transient membership")
			}
			read.Close()
			if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("borrowed collaboration regression leaked work or decoded complete metadata")
			}
			var one int
			if err := tx.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
				t.Fatal("collaboration reader closed the caller's SQL snapshot", one, err)
			}
			t.Log("borrowed transient team dropped; exact documentRole inputs match; leaked ID would activate editor grant")
		})
	}
}
