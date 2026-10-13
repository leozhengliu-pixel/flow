package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// mcpResultText is content[0].text of a successful tools/call.
func mcpResultText(t *testing.T, response map[string]any) string {
	t.Helper()
	result, ok := response["result"].(map[string]any)
	if !ok || result["isError"] == true {
		t.Fatalf("MCP request failed: %v", response)
	}
	return result["content"].([]any)[0].(map[string]any)["text"].(string)
}

func mcpToolErrorText(t *testing.T, response map[string]any) string {
	t.Helper()
	result, ok := response["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Fatalf("MCP call succeeded, want a tool error: %v", response)
	}
	return result["content"].([]any)[0].(map[string]any)["text"].(string)
}

// List tools whose result is a JSON array answer with structuredContent {items: […]} (an object, as the MCP spec
// and strict clients require) and keep the array as their text content.
func TestMCPListResultsAreObjectStructuredContent(t *testing.T) {
	f := newMCPContractFixture(t)
	team := f.data.Teams[0].ID
	response := f.call(t, "list_issue_statuses", map[string]any{"team": team})
	var statuses []map[string]any
	if err := json.Unmarshal([]byte(mcpResultText(t, response)), &statuses); err != nil || len(statuses) == 0 {
		t.Fatalf("text content is no longer the status array: %v", err)
	}
	structured, ok := mcpSuccess(t, response).(map[string]any)
	if !ok {
		t.Fatalf("structuredContent is not an object: %v", response)
	}
	if items, _ := structured["items"].([]any); len(items) != len(statuses) {
		t.Fatalf("structuredContent.items = %v, want %d statuses", structured["items"], len(statuses))
	}
	response = f.call(t, "list_cycles", map[string]any{"teamId": team})
	if structured := response["result"].(map[string]any)["structuredContent"]; structured != nil {
		if _, ok := structured.(map[string]any); !ok {
			t.Fatalf("list_cycles structuredContent = %T", structured)
		}
	}
	if got := mcpStructuredContent(json.RawMessage(`[1,2]`), nil); !strings.Contains(string(mustJSON(t, got)), `"items":[1,2]`) {
		t.Fatalf("array wrap = %s", string(mustJSON(t, got)))
	}
	if got := mcpStructuredContent(json.RawMessage(`"done"`), nil); string(mustJSON(t, got)) != `{"value":"done"}` {
		t.Fatalf("scalar wrap = %s", string(mustJSON(t, got)))
	}
	if got := mcpStructuredContent(json.RawMessage(`{"id":"a"}`), map[string]any{"change": 1}); string(mustJSON(t, got)) != `{"change":1,"id":"a"}` {
		t.Fatalf("object extra = %s", string(mustJSON(t, got)))
	}
	if got := mcpStructuredContent(json.RawMessage(`null`), nil); got != nil {
		t.Fatalf("null result = %v", got)
	}
}

// A sub-issue created with parentId and no team goes to its parent's team (the spike's agents left team out).
func TestMCPSaveIssueSubIssueTakesTheParentsTeam(t *testing.T) {
	f := newMCPContractFixture(t)
	parentTeam := f.data.Teams[len(f.data.Teams)-1]
	parent := mcpSuccess(t, f.call(t, "save_issue", map[string]any{"title": "Parent", "team": parentTeam.ID})).(map[string]any)
	child := mcpSuccess(t, f.call(t, "save_issue", map[string]any{"title": "Child", "parentId": parent["identifier"]})).(map[string]any)
	issue, err := f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, child["id"].(string))
	if err != nil || issue.Team.ID != parentTeam.ID || issue.ParentID == nil || *issue.ParentID != parent["id"] {
		t.Fatalf("sub-issue = team %s parent %v (%v), want team %s", issue.Team.ID, issue.ParentID, err, parentTeam.ID)
	}
	if message := mcpToolErrorText(t, f.call(t, "save_issue", map[string]any{"title": "Orphan"})); !strings.Contains(message, "team is required") {
		t.Fatalf("issue without team or parent: %s", message)
	}
}

// Delete tools take ids[] so one call carries every target; id alone keeps working and both together are refused.
func TestMCPBatchDeleteTakesIDs(t *testing.T) {
	f := newMCPContractFixture(t)
	team := f.data.Teams[0].ID
	created := []string{}
	for _, title := range []string{"Batch one", "Batch two", "Batch three"} {
		issue := mcpSuccess(t, f.call(t, "save_issue", map[string]any{"title": title, "team": team})).(map[string]any)
		created = append(created, issue["identifier"].(string))
	}
	receipt := mcpSuccess(t, f.call(t, "delete_issue", map[string]any{"ids": []string{created[0], created[1], "NOPE-404"}})).(map[string]any)
	if receipt["deleted"] != float64(2) || len(receipt["items"].([]any)) != 2 || len(receipt["errors"].([]any)) != 1 {
		t.Fatalf("batch receipt = %v", receipt)
	}
	for _, identifier := range created[:2] {
		if _, err := f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, identifier); err == nil {
			t.Fatalf("%s survived the batch delete", identifier)
		}
	}
	if message := mcpToolErrorText(t, f.call(t, "delete_issue", map[string]any{"id": created[2], "ids": []string{created[2]}})); !strings.Contains(message, "either id or ids") {
		t.Fatalf("id and ids: %s", message)
	}
	if response := f.call(t, "delete_issue", map[string]any{"ids": []string{}}); response["error"] == nil {
		t.Fatalf("empty ids accepted: %v", response)
	}
	mcpSuccess(t, f.call(t, "delete_issue", map[string]any{"id": created[2]}))

	_, listed := f.raw(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	for _, raw := range listed["result"].(map[string]any)["tools"].([]any) {
		tool := raw.(map[string]any)
		name := tool["name"].(string)
		schema := tool["inputSchema"].(map[string]any)
		_, hasIDs := schema["properties"].(map[string]any)["ids"]
		if hasIDs != mcpBatchDeleteTools()[name] {
			t.Errorf("%s: ids in schema = %v", name, hasIDs)
		}
		if hasIDs && slices.Contains(schema["required"].([]any), any("id")) {
			t.Errorf("%s still requires id", name)
		}
	}
	for _, name := range []string{"delete_issue", "delete_document", "delete_project", "delete_initiative", "delete_comment", "delete_label", "delete_view", "delete_customer"} {
		if !mcpBatchDeleteTools()[name] {
			t.Errorf("%s does not take ids", name)
		}
	}
	// The in-process agent's tool definitions are unchanged: no ids, and no MCP-only tools.
	s := &server{}
	s.agent.ToolsEnabled, s.agent.WriteTools = true, true
	definitions, err := s.agentToolDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		if definition.Name == "count_issues" || strings.Contains(string(definition.Parameters), `"ids"`) && strings.HasPrefix(definition.Name, "delete_") {
			t.Errorf("in-process definition changed: %s %s", definition.Name, definition.Parameters)
		}
	}
}

// The bulk-delete approval counts the targets of one batch call, not just the calls.
func TestAgentApprovalCountsBatchDeleteTargets(t *testing.T) {
	f := newAgentAttributionFixture(t)
	team := f.data.Teams[0].ID
	one := f.issue(f.chat("save_issue", map[string]any{"team": team, "title": "Approval one"}))
	two := f.issue(f.chat("save_issue", map[string]any{"team": team, "title": "Approval two"}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
	request = request.WithContext(context.WithValue(request.Context(), authUserContextKey{}, f.data.Viewer))
	single := domain.AgentToolCall{ID: "call_1", Name: "delete_issue", Arguments: json.RawMessage(`{"ids":["` + one.Identifier + `"]}`)}
	if approval := f.s.agentApprovalForTurn(request, f.data, []domain.AgentToolCall{single}, "delete it"); approval != nil {
		t.Fatalf("one target needs no approval: %+v", approval)
	}
	batch := domain.AgentToolCall{ID: "call_2", Name: "delete_issue", Arguments: json.RawMessage(`{"ids":["` + one.Identifier + `","` + two.Identifier + `"]}`)}
	approval := f.s.agentApprovalForTurn(request, f.data, []domain.AgentToolCall{batch}, "delete both")
	if approval == nil || approval.Kind != "delete" || len(approval.Targets) != 2 || len(approval.Calls) != 1 {
		t.Fatalf("batch delete approval = %+v", approval)
	}
}

// count_issues counts across pages with list_issues' filters.
func TestMCPCountIssues(t *testing.T) {
	f := newMCPContractFixture(t)
	team := f.data.Teams[0].ID
	for index := range 3 {
		mcpSuccess(t, f.call(t, "save_issue", map[string]any{"title": "Countable needle " + string(rune('a'+index)), "team": team, "priority": 2}))
	}
	mcpSuccess(t, f.call(t, "save_issue", map[string]any{"title": "Countable needle other", "team": team, "priority": 4}))
	count := func(args map[string]any) float64 {
		t.Helper()
		return mcpSuccess(t, f.call(t, "count_issues", args)).(map[string]any)["count"].(float64)
	}
	if got := count(map[string]any{"query": "Countable needle", "priority": 2}); got != 3 {
		t.Fatalf("count by priority = %v, want 3", got)
	}
	if got := count(map[string]any{"query": "Countable needle"}); got != 4 {
		t.Fatalf("count by query = %v, want 4", got)
	}
	listed := mcpSuccess(t, f.call(t, "list_issues", map[string]any{"team": team, "limit": 250})).(map[string]any)
	if got := count(map[string]any{"team": team}); got != float64(len(listed["items"].([]any))) {
		t.Fatalf("count_issues = %v, list_issues = %d", got, len(listed["items"].([]any)))
	}
	if message := mcpToolErrorText(t, f.call(t, "count_issues", map[string]any{"team": "no-such-team"})); message == "" {
		t.Fatal("unknown team counted")
	}
}
