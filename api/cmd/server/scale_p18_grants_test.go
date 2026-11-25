package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP18GuestGrantAndRevocation(t *testing.T) {
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace, private := f.data.Workspace.URLKey, "p18-granted-private"
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p18.seeded", "", nil, func(data *domain.Bootstrap) error {
			data.Teams = append(data.Teams, domain.Team{ID: private, Key: "P18G", Private: true})
			data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := repo.SetTeamMembership(t.Context(), f.data.Workspace.ID, private, f.data.Viewer.ID, "owner", true); err != nil {
			t.Fatal(err)
		}
		_, guest := scaleP03Join(t, f, "P18 granted guest", idgen.New("p18-granted-")+"@example.test", "guest", f.data.Teams[0].ID)
		issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "P18 explicitly granted issue", "teamId": private}, workspace, http.StatusCreated)
		permissions := "/api/issues/" + issue.ID + "/permissions"
		authRequest[[]domain.IssuePermission](t, f.admin, http.MethodPut, f.host.URL+permissions, map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": guest.ID, "role": "commenter"}}}, workspace, http.StatusOK)
		actor := mcpActor{WorkspaceKey: workspace, User: f.data.Viewer, AuthorizationUserID: guest.ID, APIKey: domain.APIKey{Scopes: []string{"read", "write"}}}
		ctx := context.WithValue(t.Context(), authUserContextKey{}, actor.User)
		for _, mode := range []string{"off", "on"} {
			t.Setenv("FLOW_MCP_ISSUE_RECORDS", mode)
			t.Setenv("FLOW_MCP_WRITE_RECORDS", mode)
			if _, err := f.service.callFlowTool(ctx, actor, "get_issue", map[string]any{"id": issue.ID}); err != nil {
				t.Fatalf("%s denied explicit issue grant: %v", mode, err)
			}
			if _, err := f.service.callFlowTool(ctx, actor, "save_comment", map[string]any{"issueId": issue.ID, "body": "P18 delegated comment " + mode}); err != nil {
				t.Fatalf("%s denied explicit commenter grant: %v", mode, err)
			}
		}
		authRequest[[]domain.IssuePermission](t, f.admin, http.MethodPut, f.host.URL+permissions, map[string]any{"permissions": []any{}}, workspace, http.StatusOK)
		for _, mode := range []string{"off", "on"} {
			t.Setenv("FLOW_MCP_ISSUE_RECORDS", mode)
			if _, err := f.service.callFlowTool(ctx, actor, "get_issue", map[string]any{"id": issue.ID}); err == nil {
				t.Fatalf("%s reused a revoked grant on replay", mode)
			}
		}
	}
}

func TestScaleP18QueuedKeyRechecksCurrentScope(t *testing.T) {
	repo, actor, ctx := scaleP18Fixture(t)
	s := &server{store: repo}
	key := domain.APIKey{ID: "p18-retained-key", SecretHash: secretHash(idgen.New("p18-token-")), CreatorID: actor.User.ID, Scopes: []string{"read"}}
	if err := repo.MutateWorkspace(ctx, actor.WorkspaceKey, "api_key.created", key.ID, nil, func(data *domain.Bootstrap) error {
		data.APIKeys = append(data.APIKeys, key)
		data.Projects = append(data.Projects, domain.Project{ID: "p18-key-project", TeamIDs: []string{data.Teams[0].ID}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	actor.APIKey = key
	t.Setenv("FLOW_MCP_PROJECT_RECORDS", "on")
	if _, err := s.callFlowTool(ctx, actor, "list_projects", nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.MutateWorkspace(ctx, actor.WorkspaceKey, "api_key.updated", key.ID, nil, func(data *domain.Bootstrap) error {
		for i := range data.APIKeys {
			if data.APIKeys[i].ID == key.ID {
				data.APIKeys[i].TeamRestriction, data.APIKeys[i].TeamIDs = "selected", []string{}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	page, err := s.callFlowTool(ctx, actor, "list_projects", nil)
	if err != nil || len(page.(map[string]any)["items"].([]domain.Project)) != 0 {
		t.Fatalf("retained credential widened the current scope: %v", err)
	}
	if err := repo.MutateWorkspace(ctx, actor.WorkspaceKey, "api_key.revoked", key.ID, nil, func(data *domain.Bootstrap) error {
		for i := range data.APIKeys {
			if data.APIKeys[i].ID == key.ID {
				now := time.Now().UTC()
				data.APIKeys[i].RevokedAt = &now
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.callFlowTool(ctx, actor, "list_projects", nil); err == nil {
		t.Fatal("queued/replayed credential survived revocation")
	}
}
