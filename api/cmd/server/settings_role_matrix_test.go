package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// TestSettingsRoleMatrix exercises the settings APIs as a workspace admin, a
// team owner, a team member, a workspace member outside the team and a guest,
// and checks each endpoint against Linear's role model (and the Flow settings
// UI, which shows these pages read-only or hidden for the same roles).
func TestSettingsRoleMatrix(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "settings-roles.db"))
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
	other := authRequest[domain.Team](t, admin, http.MethodPost, server.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Other", "key": "OTHR"}, "", http.StatusCreated)
	authRequest[domain.TeamSettings](t, admin, http.MethodPatch, server.URL+"/api/teams/"+teamID+"/settings", map[string]any{"settingsPermission": "owners"}, "test-workspace", http.StatusOK)

	teamOwner := inviteTeamPermissionActor(t, server.URL, admin, "Team owner", "matrix-owner@example.test", "member", []string{teamID})
	teamMember := inviteTeamPermissionActor(t, server.URL, admin, "Team member", "matrix-member@example.test", "member", []string{teamID})
	outsider := inviteTeamPermissionActor(t, server.URL, admin, "Outsider", "matrix-outsider@example.test", "member", nil)
	guest := inviteTeamPermissionActor(t, server.URL, admin, "Guest", "matrix-guest@example.test", "guest", []string{teamID})
	authRequest[any](t, admin, http.MethodPut, server.URL+"/api/workspaces/test-workspace/teams/"+teamID+"/members/"+teamOwner.user.ID, map[string]any{"member": true, "role": "owner"}, "", http.StatusNoContent)

	state := authRequest[[]domain.WorkflowState](t, admin, http.MethodGet, server.URL+"/api/teams/"+teamID+"/states", nil, "test-workspace", http.StatusOK)
	if len(state) == 0 {
		t.Fatal("team has no workflow states")
	}

	actors := map[string]*http.Client{"admin": admin, "teamOwner": teamOwner.client, "teamMember": teamMember.client, "outsider": outsider.client, "guest": guest.client}
	call := func(actor, method, path string, body any) (int, []byte) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			reader = bytes.NewReader(raw)
		}
		request, err := http.NewRequest(method, server.URL+path, reader)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-Workspace-Key", "test-workspace")
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := actors[actor].Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		return response.StatusCode, raw
	}
	createPipeline := func(teamIDs []string) domain.ReleasePipeline {
		t.Helper()
		status, raw := call("admin", http.MethodPost, "/api/release-pipelines", map[string]any{"name": "Matrix pipeline", "teamIds": teamIDs})
		if status != http.StatusCreated {
			t.Fatalf("create pipeline status=%d body=%s", status, raw)
		}
		var pipeline domain.ReleasePipeline
		if err := json.Unmarshal(raw, &pipeline); err != nil {
			t.Fatal(err)
		}
		return pipeline
	}
	teamPipeline := createPipeline([]string{teamID})
	workspacePipeline := createPipeline(nil)

	type expectation map[string]int
	cases := []struct {
		name   string
		method string
		path   string
		body   func(actor string) any
		want   expectation
	}{
		{
			name: "create release pipeline (Linear canCreateReleasePipeline: any non-guest)", method: http.MethodPost, path: "/api/release-pipelines",
			body: func(actor string) any { return map[string]any{"name": "Pipeline by " + actor} },
			want: expectation{"admin": 201, "teamOwner": 201, "teamMember": 201, "outsider": 201, "guest": 403},
		},
		{
			name: "edit team pipeline (admins and owners of every pipeline team)", method: http.MethodPatch, path: "/api/release-pipelines/" + teamPipeline.ID,
			body: func(string) any { return map[string]any{"name": "Renamed"} },
			want: expectation{"admin": 200, "teamOwner": 200, "teamMember": 403, "outsider": 403, "guest": 403},
		},
		{
			name: "move team pipeline to a team the owner does not own", method: http.MethodPatch, path: "/api/release-pipelines/" + teamPipeline.ID,
			body: func(string) any { return map[string]any{"teamIds": []string{teamID, other.ID}} },
			want: expectation{"teamOwner": 403, "teamMember": 403},
		},
		{
			name: "edit pipeline without teams (admins only)", method: http.MethodPatch, path: "/api/release-pipelines/" + workspacePipeline.ID,
			body: func(string) any { return map[string]any{"name": "Workspace pipeline"} },
			want: expectation{"admin": 200, "teamOwner": 403, "teamMember": 403, "outsider": 403, "guest": 403},
		},
		{
			name: "rotate pipeline access key", method: http.MethodPost, path: "/api/release-pipelines/" + teamPipeline.ID + "/access-key",
			want: expectation{"admin": 201, "teamOwner": 201, "teamMember": 403, "outsider": 403, "guest": 403},
		},
		{
			name: "reorder workspace pipelines (admins only)", method: http.MethodPost, path: "/api/release-pipelines/reorder",
			body: func(string) any { return map[string]any{"ids": []string{}} },
			want: expectation{"teamOwner": 403, "teamMember": 403, "guest": 403},
		},
		{
			name: "SLA settings (Linear slas: admins only)", method: http.MethodPut, path: "/api/sla-settings",
			body: func(string) any { return map[string]any{"enabled": true} },
			want: expectation{"admin": 200, "teamOwner": 403, "teamMember": 403, "outsider": 403, "guest": 403},
		},
		{
			name: "SLA rules", method: http.MethodPost, path: "/api/sla-rules",
			body: func(string) any { return map[string]any{"name": "Urgent", "targetMinutes": 60} },
			want: expectation{"teamOwner": 403, "teamMember": 403, "outsider": 403, "guest": 403},
		},
		{
			name: "project update schedule (Linear scheduleManagement)", method: http.MethodPut, path: "/api/project-update-settings",
			body: func(string) any { return map[string]any{"enabled": true} },
			want: expectation{"teamOwner": 403, "teamMember": 403, "outsider": 403, "guest": 403},
		},
		{
			name: "project statuses (Linear projectStatusManagement)", method: http.MethodPost, path: "/api/project-statuses",
			body: func(string) any { return map[string]any{"name": "Review", "type": "started", "color": "#5e6ad2"} },
			want: expectation{"teamOwner": 403, "teamMember": 403, "outsider": 403, "guest": 403},
		},
		{
			name: "customer statuses (Linear customers)", method: http.MethodPost, path: "/api/customer-statuses",
			body: func(string) any { return map[string]any{"name": "Churned", "color": "#95a2b3"} },
			want: expectation{"teamOwner": 403, "teamMember": 403, "outsider": 403, "guest": 403},
		},
		{
			name: "workspace feature toggles (Linear workspaceSettings)", method: http.MethodPatch, path: "/api/workspace/preferences",
			body: func(string) any { return map[string]any{"featureFlags": map[string]bool{"pulse": true}} },
			want: expectation{"admin": 200, "teamOwner": 403, "teamMember": 403, "outsider": 403, "guest": 403},
		},
		{
			name: "connect a workspace integration (Linear integrationCreation)", method: http.MethodPut, path: "/api/integrations/slack",
			body: func(string) any { return map[string]any{"name": "Slack"} },
			want: expectation{"teamOwner": 403, "teamMember": 403, "outsider": 403, "guest": 403},
		},
		{
			name: "custom emojis (any member, not guests)", method: http.MethodPost, path: "/api/custom-emojis",
			body: func(actor string) any {
				return map[string]any{"name": "matrix-" + actor, "imageUrl": "data:image/png;base64,iVBORw0KGgo="}
			},
			want: expectation{"admin": 201, "teamOwner": 201, "teamMember": 201, "outsider": 201, "guest": 403},
		},
		{
			name: "team git automation (team settings permission: owners)", method: http.MethodPost, path: "/api/git-automations",
			body: func(string) any {
				return map[string]any{"teamId": teamID, "repository": "*", "event": "merged", "workflowStateId": state[0].ID}
			},
			want: expectation{"admin": 200, "teamOwner": 200, "teamMember": 403, "outsider": 403, "guest": 403},
		},
		{
			name: "team target branch", method: http.MethodPost, path: "/api/target-branches",
			body: func(actor string) any {
				return map[string]any{"teamId": teamID, "repository": "branch", "branch": "release-" + actor}
			},
			want: expectation{"admin": 200, "teamOwner": 200, "teamMember": 403, "outsider": 403, "guest": 403},
		},
		{
			name: "team cycle settings", method: http.MethodPatch, path: "/api/teams/" + teamID + "/settings",
			body: func(string) any { return map[string]any{"triageEnabled": true} },
			want: expectation{"admin": 200, "teamOwner": 200, "teamMember": 403, "outsider": 403, "guest": 403},
		},
		{
			name: "retire a team (Linear dangerousOperations: team owners)", method: http.MethodPatch, path: "/api/workspaces/test-workspace/teams/" + other.ID,
			body: func(string) any { return map[string]any{"private": true} },
			want: expectation{"teamMember": 403, "outsider": 403, "guest": 403},
		},
		{
			name: "admin-only administration pages", method: http.MethodGet, path: "/api/webhooks",
			want: expectation{"admin": 200, "teamOwner": 403, "teamMember": 403, "outsider": 403, "guest": 403},
		},
	}
	for _, item := range cases {
		for actor, want := range item.want {
			t.Run(item.name+"/"+actor, func(t *testing.T) {
				var body any
				if item.body != nil {
					body = item.body(actor)
				}
				status, raw := call(actor, item.method, item.path, body)
				if status != want {
					t.Fatalf("%s %s as %s: status=%d want=%d body=%s", item.method, item.path, actor, status, want, raw)
				}
			})
		}
	}

	t.Run("team owners can delete and restore their team's pipeline", func(t *testing.T) {
		pipeline := createPipeline([]string{teamID})
		if status, raw := call("teamMember", http.MethodDelete, "/api/release-pipelines/"+pipeline.ID, nil); status != http.StatusForbidden {
			t.Fatalf("team member delete status=%d body=%s", status, raw)
		}
		if status, raw := call("teamOwner", http.MethodDelete, "/api/release-pipelines/"+pipeline.ID, nil); status != http.StatusNoContent {
			t.Fatalf("team owner delete status=%d body=%s", status, raw)
		}
		current := authRequest[domain.Bootstrap](t, admin, http.MethodGet, server.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
		entryID := ""
		for _, entry := range current.Trash {
			if entry.ResourceType == "release_pipeline" && entry.ResourceID == pipeline.ID {
				entryID = entry.ID
			}
		}
		if entryID == "" {
			t.Fatal("deleted pipeline missing from trash")
		}
		if status, raw := call("teamMember", http.MethodPost, "/api/trash/"+entryID+"/restore", nil); status != http.StatusForbidden {
			t.Fatalf("team member restore status=%d body=%s", status, raw)
		}
		if status, raw := call("teamOwner", http.MethodPost, "/api/trash/"+entryID+"/restore", nil); status >= 300 {
			t.Fatalf("team owner restore status=%d body=%s", status, raw)
		}
	})

	t.Run("workspace who-can settings gate members", func(t *testing.T) {
		authRequest[any](t, admin, http.MethodPatch, server.URL+"/api/workspace/preferences", map[string]any{"labelPermission": "admins"}, "test-workspace", http.StatusOK)
		if status, raw := call("teamMember", http.MethodPost, "/api/labels", map[string]any{"name": "Matrix", "color": "#5e6ad2"}); status != http.StatusForbidden {
			t.Fatalf("restricted label status=%d body=%s", status, raw)
		}
		authRequest[any](t, admin, http.MethodPatch, server.URL+"/api/workspace/preferences", map[string]any{"labelPermission": "members"}, "test-workspace", http.StatusOK)
		if status, raw := call("teamMember", http.MethodPost, "/api/labels", map[string]any{"name": "Matrix", "color": "#5e6ad2"}); status >= 300 {
			t.Fatalf("member label status=%d body=%s", status, raw)
		}
		if status, raw := call("guest", http.MethodPost, "/api/labels", map[string]any{"name": "Guest label", "color": "#5e6ad2"}); status != http.StatusForbidden {
			t.Fatalf("guest label status=%d body=%s", status, raw)
		}
	})
}
