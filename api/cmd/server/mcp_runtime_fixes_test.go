package main

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

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
	found := false
	for _, raw := range result["items"].([]any) {
		item := raw.(map[string]any)
		if item["id"] == parent.ID {
			found = true
			if item["subIssueCount"].(float64) != 0 || item["hasChildren"] != false {
				t.Fatalf("private child leaked through relationship fields: %v", item)
			}
		}
	}
	if !found {
		t.Fatalf("visible parent missing from relationship projection: %v", result)
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

func TestMCPCountIssuesFastPathAllowListsSchemaFilters(t *testing.T) {
	repository, actor, ctx := newMCPToolTestContext(t)
	service := &server{store: repository}
	data := repository.Bootstrap()
	team := data.Teams[0].ID

	var definitions []struct {
		Name        string `json:"name"`
		InputSchema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"input_schema"`
	}
	raw, err := os.ReadFile("flow_mcp_tools.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &definitions); err != nil {
		t.Fatal(err)
	}
	var properties map[string]json.RawMessage
	for _, definition := range definitions {
		if definition.Name == "mcp__flow.count_issues" {
			properties = definition.InputSchema.Properties
			break
		}
	}
	if len(properties) == 0 {
		t.Fatal("count_issues schema has no properties")
	}
	values := map[string]any{
		"assignee": "me", "createdAt": "2026-01-01", "cycle": data.Cycles[0].ID,
		"delegate": actor.User.ID, "hasAttachments": true, "hasCustomerRequests": true,
		"hasSubIssues": true, "label": data.Labels[0].ID, "parentId": data.Issues[0].ID,
		"priority": 2, "project": data.Projects[0].ID, "query": "no matching issue",
		"release": "no matching release", "state": data.States[0].ID, "updatedAt": "2026-01-01",
	}

	queryCount := func(args map[string]any) int64 {
		t.Helper()
		q, err := service.mcpIssueQuery(ctx, actor)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.applyMCPIssueFilters(ctx, &q, data, args, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		q.Limit, q.Summary, q.IncludeTotal = 1, true, true
		page, err := repository.QueryIssueRecords(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return page.Total
	}
	for property := range properties {
		if property == "team" || property == "includeArchived" {
			continue
		}
		t.Run(property, func(t *testing.T) {
			value, ok := values[property]
			if !ok {
				t.Fatalf("add a representative value for new schema property %q", property)
			}
			args := map[string]any{"team": team, property: value}
			if simpleMCPTeamCount(args) {
				t.Fatalf("schema filter %q unexpectedly takes the counter fast path", property)
			}
			result, err := service.countMCPIssues(ctx, actor, data, args)
			if err != nil {
				t.Fatalf("count_issues %s: %v", property, err)
			}
			got, want := result.(map[string]any)["count"].(int64), queryCount(args)
			if got != want {
				t.Fatalf("count_issues %s = %d, query path = %d", property, got, want)
			}
		})
	}
	for _, property := range []string{"statusType", "dueDate", "estimate", "customer", "subscriber", "completedAt", "triage", "filter", "futureFilter"} {
		if simpleMCPTeamCount(map[string]any{"team": team, property: map[string]any{"and": []any{}}}) {
			t.Fatalf("unrecognized filter %q unexpectedly takes the counter fast path", property)
		}
	}
}

func TestMCPCountIssuesVisibilityMatchesQueryForNonAdmin(t *testing.T) {
	f := newMCPContractFixture(t)
	if err := f.repository.UpdateMemberRole(t.Context(), f.data.Workspace.ID, "usr_member", "member"); err != nil {
		t.Fatal(err)
	}
	member := slices.IndexFunc(f.data.Users, func(user domain.User) bool { return user.ID == "usr_member" })
	if member < 0 {
		t.Fatal("fixture member missing")
	}
	actor := mcpActor{WorkspaceKey: f.data.Workspace.URLKey, User: f.data.Users[member], APIKey: domain.APIKey{Scopes: []string{"read", "write"}}}
	private := domain.Team{ID: "count-private", Name: "Count private", Key: "CPR", Private: true}
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "team.created", private.ID, nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, private)
		data.TeamSettings[private.ID] = domain.TeamSettings{TeamID: private.ID, Access: "private"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	shared := f.data.Issues[0]
	shared.ID, shared.Identifier = "count-shared", "CPR-1"
	shared.Team = private
	shared.Permissions = []domain.IssuePermission{{SubjectType: "user", SubjectID: actor.User.ID, Role: "viewer"}}
	hidden := shared
	hidden.ID, hidden.Identifier, hidden.Permissions = "count-hidden", "CPR-2", nil
	if err := f.repository.ImportIssues(t.Context(), f.data.Workspace.URLKey, []domain.Issue{shared, hidden}); err != nil {
		t.Fatal(err)
	}
	data, err := f.service.mcpWorkspaceDataForTool(t.Context(), actor, "count_issues", nil)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := f.service.pagedMCPIssues(t.Context(), actor, data, map[string]any{"limit": 250})
	if err != nil {
		t.Fatal(err)
	}
	items := listed.(map[string]any)["items"].([]domain.Issue)
	if !slices.ContainsFunc(items, func(issue domain.Issue) bool { return issue.ID == shared.ID }) {
		t.Fatal("shared issue was hidden from the non-admin viewer")
	}
	counted, err := f.service.countMCPIssues(t.Context(), actor, data, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	count := counted.(map[string]any)["count"].(int64)
	if count != int64(len(items)) {
		t.Fatalf("non-admin count = %d, list = %d", count, len(items))
	}
	if _, err := f.service.countMCPIssues(t.Context(), actor, data, map[string]any{"team": private.ID}); err == nil {
		t.Fatal("private team was countable by a non-admin viewer")
	}
	if err := f.repository.SetTeamMembership(t.Context(), f.data.Workspace.ID, private.ID, actor.User.ID, "member", true); err != nil {
		t.Fatal(err)
	}
	data, err = f.service.mcpWorkspaceDataForTool(t.Context(), actor, "count_issues", nil)
	if err != nil {
		t.Fatal(err)
	}
	memberCount, err := f.service.countMCPIssues(t.Context(), actor, data, map[string]any{"team": private.ID})
	if err != nil || memberCount.(map[string]any)["count"].(int64) != 2 {
		t.Fatalf("private team membership count = %v, err = %v", memberCount, err)
	}
	memberList, err := f.service.pagedMCPIssues(t.Context(), actor, data, map[string]any{"team": private.ID, "limit": 250})
	if err != nil || len(memberList.(map[string]any)["items"].([]domain.Issue)) != 2 {
		t.Fatalf("private team membership query = %v, err = %v", memberList, err)
	}

	archived := time.Now().UTC()
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "team.archived", private.ID, nil, func(data *domain.Bootstrap) error {
		for index := range data.Teams {
			if data.Teams[index].ID == private.ID {
				data.Teams[index].ArchivedAt = &archived
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	data, err = f.service.mcpWorkspaceDataForTool(t.Context(), actor, "count_issues", nil)
	if err != nil {
		t.Fatal(err)
	}
	listed, err = f.service.pagedMCPIssues(t.Context(), actor, data, map[string]any{"limit": 250})
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range listed.(map[string]any)["items"].([]domain.Issue) {
		if issue.ID == shared.ID {
			t.Fatal("archived team's shared issue remained visible")
		}
	}
	counted, err = f.service.countMCPIssues(t.Context(), actor, data, map[string]any{"includeArchived": true})
	if err != nil {
		t.Fatal(err)
	}
	if got := counted.(map[string]any)["count"].(int64); got != count-1 {
		t.Fatalf("archived team count = %d, want %d", got, count-1)
	}
	if _, err := f.service.countMCPIssues(t.Context(), actor, data, map[string]any{"team": private.ID, "includeArchived": true}); err == nil {
		t.Fatal("archived team was countable by a non-admin viewer")
	}
}

func TestMCPCountIssuesCounterArchiveAndTrashParity(t *testing.T) {
	f := newMCPContractFixture(t)
	team := domain.Team{ID: "count-archive", Name: "Count archive", Key: "CAR"}
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "team.created", team.ID, nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, team)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	archived := time.Now().UTC()
	issues := make([]domain.Issue, 4)
	for i, id := range []string{"count-active", "count-archived", "count-trash-active", "count-trash-archived"} {
		issue := f.data.Issues[0]
		issue.ID, issue.Identifier, issue.Team = id, "CAR-"+string(rune('1'+i)), team
		issue.ArchivedAt = nil
		if i%2 == 1 {
			issue.ArchivedAt = &archived
		}
		issues[i] = issue
	}
	if err := f.repository.ImportIssues(t.Context(), f.data.Workspace.URLKey, issues); err != nil {
		t.Fatal(err)
	}
	actor := mcpActor{WorkspaceKey: f.data.Workspace.URLKey, User: f.data.Viewer}
	check := func(active, total int64) {
		t.Helper()
		data, err := f.service.mcpWorkspaceDataForTool(t.Context(), actor, "count_issues", nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, includeArchived := range []bool{false, true} {
			args := map[string]any{"team": team.ID, "includeArchived": includeArchived}
			if !simpleMCPTeamCount(args) {
				t.Fatal("team/includeArchived did not take the fast path")
			}
			result, err := f.service.countMCPIssues(t.Context(), actor, data, args)
			if err != nil {
				t.Fatal(err)
			}
			query, err := f.service.mcpIssueQuery(t.Context(), actor)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.service.applyMCPIssueFilters(t.Context(), &query, data, args, archived); err != nil {
				t.Fatal(err)
			}
			query.Limit, query.IncludeTotal, query.Summary = 1, true, true
			page, err := f.repository.QueryIssueRecords(t.Context(), query)
			if err != nil {
				t.Fatal(err)
			}
			want := active
			if includeArchived {
				want = total
			}
			if got := result.(map[string]any)["count"].(int64); got != want || got != page.Total {
				t.Fatalf("includeArchived=%v: counter=%d query=%d want=%d", includeArchived, got, page.Total, want)
			}
		}
	}
	check(2, 4)
	for _, issue := range issues[2:] {
		mcpSuccess(t, f.call(t, "delete_issue", map[string]any{"id": issue.ID}))
	}
	check(1, 2)
	if err := f.repository.UpdateMemberRole(t.Context(), f.data.Workspace.ID, actor.User.ID, "member"); err != nil {
		t.Fatal(err)
	}
	check(1, 2)
	actor.APIKey = domain.APIKey{TeamRestriction: "selected", TeamIDs: []string{f.data.Teams[0].ID}}
	data, err := f.service.mcpWorkspaceDataForTool(t.Context(), actor, "count_issues", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.countMCPIssues(t.Context(), actor, data, map[string]any{"team": team.ID}); err == nil {
		t.Fatal("API key's disallowed team was countable")
	}
}
