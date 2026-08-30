package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestMCPHTTPProtocolLifecycle(t *testing.T) {
	repository, actor, _ := newMCPToolTestContext(t)
	const secret = "flow_test_mcp_secret"
	if err := repository.MutateWorkspace(t.Context(), actor.WorkspaceKey, "test.api_key", "mcp-key", nil, func(data *domain.Bootstrap) error {
		data.APIKeys = append(data.APIKeys, domain.APIKey{ID: "mcp-key", Name: "MCP test", SecretHash: secretHash(secret), CreatorID: actor.User.ID, Scopes: []string{"read", "write"}, CreatedAt: data.Workspace.CreatedAt})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := &server{store: repository, uploadPath: t.TempDir(), authLimiter: newAuthRateLimiter()}
	readWrite := service.mcpHTTP(false)
	readonly := service.mcpHTTP(true)
	unauthorized := httptest.NewRecorder()
	readWrite.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorized.Code)
	}
	methodNotAllowed := mcpProtocolRequest(t, readWrite, http.MethodGet, secret, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"})
	if methodNotAllowed.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status=%d", methodNotAllowed.Code)
	}
	invalid := mcpProtocolRequest(t, readWrite, http.MethodPost, secret, map[string]any{"jsonrpc": "1.0", "id": 2, "method": "ping"})
	if invalid.Code != http.StatusOK || !strings.Contains(invalid.Body.String(), "Invalid Request") {
		t.Fatalf("invalid response=%d %s", invalid.Code, invalid.Body.String())
	}
	for _, request := range []map[string]any{
		{"jsonrpc": "2.0", "id": 3, "method": "initialize"},
		{"jsonrpc": "2.0", "id": 4, "method": "ping"},
		{"jsonrpc": "2.0", "id": 5, "method": "tools/list"},
		{"jsonrpc": "2.0", "id": 6, "method": "tools/call", "params": map[string]any{"name": "get_workspace", "arguments": map[string]any{}}},
	} {
		response := mcpProtocolRequest(t, readWrite, http.MethodPost, secret, request)
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"error"`) {
			t.Fatalf("request=%#v response=%d %s", request, response.Code, response.Body.String())
		}
	}
	initialized := mcpProtocolRequest(t, readWrite, http.MethodPost, secret, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	if initialized.Code != http.StatusAccepted {
		t.Fatalf("initialized status=%d", initialized.Code)
	}
	unknownMethod := mcpProtocolRequest(t, readWrite, http.MethodPost, secret, map[string]any{"jsonrpc": "2.0", "id": 7, "method": "unknown"})
	if !strings.Contains(unknownMethod.Body.String(), "Method not found") {
		t.Fatalf("unknown method response=%s", unknownMethod.Body.String())
	}
	unknownTool := mcpProtocolRequest(t, readWrite, http.MethodPost, secret, map[string]any{"jsonrpc": "2.0", "id": 8, "method": "tools/call", "params": map[string]any{"name": "unknown", "arguments": map[string]any{}}})
	if !strings.Contains(unknownTool.Body.String(), "Unknown tool") {
		t.Fatalf("unknown tool response=%s", unknownTool.Body.String())
	}
	readonlyWrite := mcpProtocolRequest(t, readonly, http.MethodPost, secret, map[string]any{"jsonrpc": "2.0", "id": 9, "method": "tools/call", "params": map[string]any{"name": "save_issue", "arguments": map[string]any{"title": "Denied"}}})
	if !strings.Contains(readonlyWrite.Body.String(), "write scope is required") {
		t.Fatalf("readonly write response=%s", readonlyWrite.Body.String())
	}
}

func mcpProtocolRequest(t *testing.T, handler http.Handler, method, secret string, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, "/mcp", bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestMCPReadToolInventoryAgainstWorkspaceData(t *testing.T) {
	repository, actor, ctx := newMCPToolTestContext(t)
	service := &server{store: repository, uploadPath: t.TempDir()}
	data := repository.Bootstrap()
	tests := []struct {
		name string
		args map[string]any
	}{
		{"get_workspace", nil},
		{"list_teams", map[string]any{"query": "Test"}},
		{"get_team", map[string]any{"query": data.Teams[0].ID}},
		{"list_users", map[string]any{"query": "Test", "team": data.Teams[0].ID}},
		{"get_user", map[string]any{"query": data.Viewer.ID}},
		{"list_issue_statuses", map[string]any{"team": data.Teams[0].ID}},
		{"get_issue_status", map[string]any{"team": data.Teams[0].ID, "id": data.States[0].ID}},
		{"list_issue_labels", map[string]any{"name": "Requirement", "team": data.Teams[0].ID}},
		{"list_project_labels", nil},
		{"list_issues", map[string]any{"query": "Test", "team": data.Teams[0].ID, "priority": 2}},
		{"list_cycles", map[string]any{"teamId": data.Teams[0].ID, "type": "current"}},
		{"list_projects", map[string]any{"query": "Test"}},
		{"get_project", map[string]any{"query": data.Projects[0].ID}},
		{"list_milestones", map[string]any{"project": data.Projects[0].ID}},
		{"list_initiatives", map[string]any{"status": "planned"}},
		{"list_documents", map[string]any{"query": "missing"}},
		{"list_comments", map[string]any{"issueId": data.Issues[0].ID}},
		{"get_status_updates", map[string]any{"type": "project"}},
		{"list_release_pipelines", map[string]any{"isProduction": true}},
		{"list_releases", map[string]any{"includeArchived": true}},
		{"list_release_notes", map[string]any{"includeArchived": true}},
		{"list_diffs", map[string]any{"query": "Test"}},
		{"get_diff", map[string]any{"urlOrId": data.Reviews[0].ID}},
		{"get_diff_threads", map[string]any{"urlOrId": data.Reviews[0].ID}},
		{"extract_images", map[string]any{"markdown": "![diagram](https://example.test/diagram.png)"}},
		{"list_agent_skills", nil},
		{"search_documentation", map[string]any{"query": "projects", "page": 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := service.callFlowTool(ctx, actor, test.name, test.args)
			if err != nil {
				t.Fatalf("%s: %v", test.name, err)
			}
			if result == nil {
				t.Fatalf("%s returned nil", test.name)
			}
		})
	}
}

func TestMCPWriteToolsPersistCoreResources(t *testing.T) {
	repository, actor, ctx := newMCPToolTestContext(t)
	service := &server{store: repository, uploadPath: t.TempDir()}
	data := repository.Bootstrap()
	if _, err := service.callFlowTool(ctx, actor, "create_issue_label", map[string]any{"name": "MCP label", "color": "#5e6ad2"}); err != nil {
		t.Fatal(err)
	}
	issueResult, err := service.callFlowTool(ctx, actor, "save_issue", map[string]any{"title": "Created through MCP", "description": "Initial body", "team": data.Teams[0].ID, "priority": 3, "labels": []string{"MCP label"}})
	if err != nil {
		t.Fatal(err)
	}
	var issue domain.Issue
	if err := jsonClone(issueResult, &issue); err != nil {
		t.Fatal(err)
	}
	data = repository.Bootstrap()
	projectResult, err := service.callFlowTool(ctx, actor, "save_project", map[string]any{"name": "MCP project", "summary": "Project summary", "setTeams": []any{data.Teams[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	var project domain.Project
	if err := jsonClone(projectResult, &project); err != nil {
		t.Fatal(err)
	}
	data = repository.Bootstrap()
	initiativeResult, err := service.callFlowTool(ctx, actor, "save_initiative", map[string]any{"name": "MCP initiative", "summary": "Initiative summary", "status": "planned", "leadTeam": data.Teams[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	var initiative domain.Initiative
	if err := jsonClone(initiativeResult, &initiative); err != nil {
		t.Fatal(err)
	}
	data = repository.Bootstrap()
	if _, err := service.callFlowTool(ctx, actor, "save_milestone", map[string]any{"project": project.ID, "name": "MCP milestone", "description": "Milestone body"}); err != nil {
		t.Fatal(err)
	}
	data = repository.Bootstrap()
	commentResult, err := service.callFlowTool(ctx, actor, "save_comment", map[string]any{"issueId": issue.ID, "body": "MCP comment"})
	if err != nil {
		t.Fatal(err)
	}
	var comment domain.Comment
	if err := jsonClone(commentResult, &comment); err != nil {
		t.Fatal(err)
	}
	data = repository.Bootstrap()
	if _, err := service.callFlowTool(ctx, actor, "save_issue", map[string]any{"id": issue.ID, "title": "Updated through MCP", "patch": []any{map[string]any{"op": "replace", "old_string": "Initial", "new_string": "Updated"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.callFlowTool(ctx, actor, "delete_comment", map[string]any{"id": comment.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.callFlowTool(ctx, actor, "unknown_tool", nil); err == nil {
		t.Fatal("unknown write tool was accepted")
	}
	latest := repository.Bootstrap()
	if _, err := mcpFindProject(latest, project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := mcpFindInitiative(latest, initiative.ID); err != nil {
		t.Fatal(err)
	}
}

func TestMCPArgumentAndPatchHelpers(t *testing.T) {
	value, err := applyTextPatches("middle", []any{
		map[string]any{"op": "prepend", "text": "start-"},
		map[string]any{"op": "append", "text": "-end"},
		map[string]any{"op": "insert_before", "anchor": "middle", "text": "before-"},
		map[string]any{"op": "insert_after", "anchor": "middle", "text": "-after"},
		map[string]any{"op": "replace", "old_string": "before", "new_string": "prior"},
		map[string]any{"op": "replace_range", "from": "middle", "to": "after", "new_string": "center"},
	})
	if err != nil || value != "start-prior-center-end" {
		t.Fatalf("patched value=%q err=%v", value, err)
	}
	value, err = applyTextPatches("repeat repeat", []any{map[string]any{"op": "replace", "old_string": "repeat", "new_string": "done", "replace_all": true}})
	if err != nil || value != "done done" {
		t.Fatalf("replace all value=%q err=%v", value, err)
	}
	for _, patches := range [][]any{
		{"invalid"},
		{map[string]any{"op": "unknown"}},
		{map[string]any{"op": "replace", "old_string": "missing", "new_string": "x"}},
		{map[string]any{"op": "insert_before", "anchor": "missing", "text": "x"}},
		{map[string]any{"op": "insert_after", "anchor": "missing", "text": "x"}},
		{map[string]any{"op": "replace_range", "from": "missing", "to": "end", "new_string": "x"}},
		{map[string]any{"op": "replace_range", "from": "middle", "to": "missing", "new_string": "x"}},
	} {
		if _, err := applyTextPatches("middle", patches); err == nil {
			t.Fatalf("invalid patches accepted: %#v", patches)
		}
	}
	args := map[string]any{"text": " value ", "integer": float64(4), "truth": true, "items": []any{" one ", "", 2, "two"}, "nil": nil}
	if stringArg(args, "text") != "value" || intArg(args, "integer", 0) != 4 || !boolArg(args, "truth") || len(stringsArg(args, "items")) != 2 {
		t.Fatalf("argument coercion failed: %#v", args)
	}
	if value, present := nullableStringArg(args, "nil"); !present || value != "" {
		t.Fatalf("nullable value=%q present=%v", value, present)
	}
	if !containsFold("Flow Project", "project") || !equalFoldAny("FLOW", "flow", "other") {
		t.Fatal("case-insensitive matching failed")
	}
	if _, err := nullableRFC3339("invalid"); err == nil {
		t.Fatal("invalid RFC3339 value was accepted")
	}
	if value, err := nullableRFC3339(""); err != nil || value != nil {
		t.Fatalf("empty RFC3339 value=%v err=%v", value, err)
	}
}

func newMCPToolTestContext(t *testing.T) (*store.SQLiteStore, mcpActor, context.Context) {
	t.Helper()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "mcp-tools.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	data := repository.Bootstrap()
	actor := mcpActor{WorkspaceKey: data.Workspace.URLKey, User: data.Viewer, APIKey: domain.APIKey{Scopes: []string{"read", "write"}}}
	ctx := context.WithValue(context.Background(), authUserContextKey{}, actor.User)
	ctx = context.WithValue(ctx, apiKeyContextKey{}, actor.APIKey)
	ctx = context.WithValue(ctx, workspaceKeyContextKey{}, actor.WorkspaceKey)
	ctx = store.ContextWithActor(ctx, actor.User)
	return repository, actor, ctx
}
