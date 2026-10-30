package main

import (
	"encoding/json"
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

func TestMCPListCyclesAllAndIssueRelationshipProjections(t *testing.T) {
	f := newMCPContractFixture(t)
	team := f.data.Teams[0].ID
	cycles := mcpSuccess(t, f.call(t, "list_cycles", map[string]any{"teamId": team, "type": "all"}))
	if _, ok := cycles.(map[string]any)["items"].([]any); !ok {
		t.Fatalf("list_cycles all = %T, want structured items", cycles)
	}
	result := mcpSuccess(t, f.call(t, "list_issues", map[string]any{
		"limit":  1,
		"fields": []string{"title", "labels", "attachmentCount", "customerRequestCount", "subIssueCount", "hasChildren"},
	}))
	items := result.(map[string]any)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("list_issues projection items = %d, want 1", len(items))
	}
	item := items[0].(map[string]any)
	for _, field := range []string{"attachmentCount", "customerRequestCount", "subIssueCount", "hasChildren"} {
		if _, ok := item[field]; !ok {
			t.Errorf("list_issues projection missing %q: %v", field, item)
		}
	}
	for _, filter := range []string{"hasAttachments", "hasCustomerRequests", "hasSubIssues"} {
		if result := f.call(t, "list_issues", map[string]any{filter: true, "limit": 1}); result["result"].(map[string]any)["isError"] == true {
			t.Errorf("list_issues %s filter: %v", filter, result)
		}
	}
	parent := mcpSuccess(t, f.call(t, "save_issue", map[string]any{"title": "Relationship parent", "team": team})).(map[string]any)
	mcpSuccess(t, f.call(t, "save_issue", map[string]any{"title": "Relationship child", "parentId": parent["identifier"]}))
	filtered := mcpSuccess(t, f.call(t, "list_issues", map[string]any{"hasSubIssues": true, "fields": []string{"id", "subIssueCount", "hasChildren"}, "limit": 250})).(map[string]any)
	found := false
	for _, raw := range filtered["items"].([]any) {
		item := raw.(map[string]any)
		if item["id"] == parent["id"] {
			found = item["hasChildren"] == true && item["subIssueCount"].(float64) == 1
		}
	}
	if !found {
		t.Fatalf("hasSubIssues did not return parent: %v", filtered)
	}
}

func TestMCPIssueRelationshipsHidePrivateChildren(t *testing.T) {
	f := newMCPContractFixture(t)
	workspace := f.data.Workspace.URLKey
	if err := f.repository.MutateWorkspace(t.Context(), workspace, "team.created", "private-team", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: "private-team", Name: "Private team", Key: "PRV", Private: true})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	parent := f.data.Issues[0]
	parent.ID, parent.Identifier, parent.ParentID, parent.SubIssueIDs = "mcp-visible-parent", "TST-MCP-PARENT", nil, []string{"mcp-private-child"}
	child := parent
	child.ID, child.Identifier, child.Team = "mcp-private-child", "PRV-MCP-CHILD", domain.Team{ID: "private-team"}
	child.ParentID = &parent.ID
	if err := f.repository.ImportIssues(t.Context(), workspace, []domain.Issue{parent, child}); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"fields": []string{"id", "subIssueCount", "hasChildren"}, "limit": 250}
	result := mcpSuccess(t, f.call(t, "list_issues", args)).(map[string]any)
	for _, raw := range result["items"].([]any) {
		item := raw.(map[string]any)
		if item["id"] == parent.ID {
			if item["subIssueCount"].(float64) != 0 || item["hasChildren"] != false {
				t.Fatalf("private child leaked through relationship fields: %v", item)
			}
		}
	}
	filtered := mcpSuccess(t, f.call(t, "list_issues", map[string]any{"hasSubIssues": true, "fields": []string{"id"}, "limit": 250})).(map[string]any)
	for _, raw := range filtered["items"].([]any) {
		if raw.(map[string]any)["id"] == parent.ID {
			t.Fatal("parent with only a private child matched hasSubIssues=true")
		}
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
	if message := mcpToolErrorText(t, f.call(t, "delete_issue", map[string]any{"ids": []string{}})); !strings.Contains(message, "Invalid arguments for delete_issue") {
		t.Fatalf("empty ids: %s", message)
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
