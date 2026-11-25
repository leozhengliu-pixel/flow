package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP18ToolFamilyCompatibility(t *testing.T) {
	repo, actor, ctx := scaleP18Fixture(t)
	service := &server{store: repo}
	var project domain.Project
	if err := repo.MutateWorkspace(ctx, actor.WorkspaceKey, "scale_p18.seeded", "", nil, func(data *domain.Bootstrap) error {
		project = domain.Project{ID: "p18-project", Name: "P18 target", SlugID: "new-p18", PreviousSlugIDs: []string{"old-p18"}, TeamIDs: []string{data.Teams[0].ID}, Description: "Body search target", Milestones: []domain.ProjectMilestone{{ID: "p18-milestone", Name: "Complete milestone", Description: "Full milestone history"}}}
		data.Projects = append(data.Projects, project)
		data.Projects = append(data.Projects, domain.Project{ID: "p18-name-first", Name: "p18-ambiguous", TeamIDs: project.TeamIDs}, domain.Project{ID: "p18-ambiguous", Name: "Other project", TeamIDs: project.TeamIDs})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		args map[string]any
	}{
		{"list_projects", map[string]any{"limit": float64(1)}},
		{"list_projects", map[string]any{"query": "body search"}},
		{"list_projects", map[string]any{"cursor": "bad"}},
		{"list_projects", map[string]any{"cursor": "9999"}},
		{"get_project", map[string]any{"query": project.ID}},
		{"get_project", map[string]any{"query": project.Name}},
		{"get_project", map[string]any{"query": "old-p18"}},
		{"get_project", map[string]any{"query": "p18-ambiguous"}},
		{"list_milestones", map[string]any{"project": project.ID}},
		{"list_issues", map[string]any{"limit": float64(1)}},
		{"count_issues", map[string]any{}},
	} {
		t.Run(test.name+fmt.Sprint(test.args), func(t *testing.T) {
			var before any
			for _, mode := range []string{"off", "on"} {
				t.Setenv(internalToolRecordFamily(test.name), mode)
				result, err := service.callFlowTool(ctx, actor, test.name, test.args)
				if err != nil {
					t.Fatal(err)
				}
				var decoded any
				raw, _ := json.Marshal(result)
				if err := json.Unmarshal(raw, &decoded); err != nil {
					t.Fatal(err)
				}
				if mode == "off" {
					before = decoded
				} else if !reflect.DeepEqual(before, decoded) {
					t.Fatalf("off/on differ: off=%v on=%v", before, decoded)
				}
			}
		})
	}
}

func TestScaleP18ChatAuthorizationOnlyPreservesPolicy(t *testing.T) {
	repo, actor, ctx := scaleP18Fixture(t)
	s := &server{store: repo}
	identity := domain.ChatIdentity{UserID: actor.User.ID, AuthProvider: "password", AuthenticatedAt: time.Now().UTC()}
	for _, mode := range []string{"off", "on"} {
		t.Setenv("FLOW_INTERNAL_CHAT_RECORDS", mode)
		if err := s.chatAuthorizedMutation(withChatIdentityAuthentication(ctx, identity), actor.WorkspaceKey, actor.User.ID, http.MethodGet, "/api/issue-records/bootstrap", nil, nil); err != nil {
			t.Fatal(err)
		}
		if err := s.chatAuthorizedMutation(ctx, actor.WorkspaceKey, actor.User.ID, http.MethodGet, "/api/issue-records/bootstrap", nil, nil); err == nil {
			t.Fatal("consent check accepted an unverified caller")
		}
		if err := s.chatAuthorizedMutation(withChatIdentityAuthentication(ctx, identity), actor.WorkspaceKey, actor.User.ID, http.MethodPost, "/api/api-keys", nil, nil); err == nil {
			t.Fatal("internal call escaped the route allowlist")
		}
	}
	if err := repo.MutateWorkspace(ctx, actor.WorkspaceKey, "workspace.settings_updated", "", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.RequireTwoFactor, data.WorkspaceSettings.DisableAdminBypass = true, true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"off", "on"} {
		t.Setenv("FLOW_INTERNAL_CHAT_RECORDS", mode)
		if err := s.chatAuthorizedMutation(withChatIdentityAuthentication(ctx, identity), actor.WorkspaceKey, actor.User.ID, http.MethodGet, "/api/issue-records/bootstrap", nil, nil); err == nil {
			t.Fatal("new workspace MFA policy was bypassed")
		}
	}
}

func TestScaleP18DelegationAndCrossWorkspace(t *testing.T) {
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		_, requester := scaleP03Join(t, f, "P18 requester", idgen.New("p18-requester-")+"@example.test", "member", f.data.Teams[0].ID)
		private := "p18-private"
		if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p18.seeded", "", nil, func(data *domain.Bootstrap) error {
			data.Teams = append(data.Teams, domain.Team{ID: private, Key: "P18P", Private: true})
			data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
			data.Projects = append(data.Projects, domain.Project{ID: "p18-private-project", TeamIDs: []string{private}}, domain.Project{ID: "p18-public-project", Name: "Keep this title", TeamIDs: []string{data.Teams[0].ID}})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := repo.SetTeamMembership(t.Context(), f.data.Workspace.ID, private, requester.ID, "member", true); err != nil {
			t.Fatal(err)
		}
		t.Setenv("FLOW_MCP_PROJECT_RECORDS", "on")
		actor := mcpActor{WorkspaceKey: f.data.Workspace.URLKey, User: f.data.Viewer, AuthorizationUserID: requester.ID, APIKey: domain.APIKey{Scopes: []string{"read", "write"}}}
		ctx := context.WithValue(t.Context(), authUserContextKey{}, actor.User)
		// Requester sees this private team; the acting principal has not joined.
		if _, err := f.service.callFlowTool(ctx, actor, "get_project", map[string]any{"query": "p18-private-project"}); err == nil {
			t.Fatal("delegation widened the acting principal's access")
		}
		_, guest := scaleP03Join(t, f, "P18 guest requester", idgen.New("p18-guest-")+"@example.test", "guest", f.data.Teams[0].ID)
		t.Setenv("FLOW_MCP_WRITE_RECORDS", "on")
		actor.AuthorizationUserID = guest.ID
		if _, err := f.service.callFlowTool(ctx, actor, "save_project", map[string]any{"id": "p18-public-project", "name": "Allowed guest edit"}); err != nil {
			t.Fatalf("guest project edit differs from HTTP: %v", err)
		}
		if _, err := f.service.callFlowTool(ctx, actor, "create_issue_label", map[string]any{"name": "Forbidden delegated label"}); err == nil {
			t.Fatal("delegated write bypassed the requester's HTTP permissions")
		}
		actor.WorkspaceKey = idgen.New("p18-absent-workspace-")
		if _, err := f.service.callFlowTool(ctx, actor, "list_projects", nil); err == nil {
			t.Fatal("cross-workspace call succeeded")
		}
	}
}
