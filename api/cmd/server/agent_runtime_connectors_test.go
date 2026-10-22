package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// fakeRuntimeConnector is a streamable-HTTP MCP server with one tool, search_code(query); its result is plain
// text, flagged isError while failing is set.
func fakeRuntimeConnector(t *testing.T, calls *atomic.Int32, failing *atomic.Bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&request) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "code", "version": "1"}}
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "search_code", "description": "Search the repository", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []string{"query"}}}}}
		case "tools/call":
			calls.Add(1)
			if failing.Load() {
				result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "search backend unavailable"}}, "isError": true}
			} else {
				result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "acme/api@main src/billing/invoice.go:42"}}, "isError": false}
			}
		default:
			result = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	t.Cleanup(server.Close)
	return server
}

func listedRunTools(t *testing.T, handler http.Handler, token string) map[string]map[string]any {
	t.Helper()
	_, listed := mcpWithToken(t, handler, token, "tools/list", map[string]any{}, nil)
	tools := map[string]map[string]any{}
	for _, raw := range listed["result"].(map[string]any)["tools"].([]any) {
		tool := raw.(map[string]any)
		tools[tool["name"].(string)] = tool
	}
	return tools
}

func callRunTool(t *testing.T, handler http.Handler, token, name string, args, meta map[string]any) map[string]any {
	t.Helper()
	params := map[string]any{"name": name, "arguments": args}
	if meta != nil {
		params["_meta"] = meta
	}
	_, response := mcpWithToken(t, handler, token, "tools/call", params, nil)
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("%s: %v", name, response)
	}
	return result
}

func runToolText(result map[string]any) string {
	content, _ := result["content"].([]any)
	parts := []string{}
	for _, item := range content {
		if text, ok := item.(map[string]any)["text"].(string); ok {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

// A worker run is offered, through /mcp with its run token, the connector tools its acting user may use — their
// own and the workspace's shared ones, not another person's, a rejected one, or any once the workspace turns
// connectors off — and every call re-checks that access.
func TestWorkerRunConnectorToolsForTheActingUserOnly(t *testing.T) {
	f := newAgentAttributionFixture(t)
	f.s.agent.WorkerSecret = testWorkerSecret
	var calls atomic.Int32
	var failing atomic.Bool
	upstream := fakeRuntimeConnector(t, &calls, &failing)
	viewer := f.data.Viewer.ID
	setTestConnectors(t, f.s.store,
		applicationPolicy{ID: "shared", Kind: "mcp", Name: "Shared code", URL: upstream.URL + "/shared", Status: "approved", Shared: true},
		applicationPolicy{ID: "mine", Kind: "mcp", Name: "My code", URL: upstream.URL + "/mine", Status: "pending", OwnerID: viewer},
		applicationPolicy{ID: "theirs", Kind: "mcp", Name: "Their code", URL: upstream.URL + "/theirs", Status: "approved", OwnerID: "someone-else"},
		applicationPolicy{ID: "rejected", Kind: "mcp", Name: "Rejected code", URL: upstream.URL + "/rejected", Status: "rejected", Shared: true},
	)
	_, token := startWorkerTestRun(t, f.s, f.handler, store.AgentRunInput{WorkspaceKey: f.data.Workspace.URLKey, SessionID: "agent_session_connectors", Kind: store.AgentRunKindChat}, agentRunPrincipal{Mode: agentRunModeUser, UserID: viewer})

	tools := listedRunTools(t, f.handler, token)
	shared, mine := connectorToolName("shared", "search_code"), connectorToolName("mine", "search_code")
	if tools[shared] == nil || tools[mine] == nil || tools["save_issue"] == nil {
		t.Fatalf("the run is not offered the user's connector tools: %v", slices.Collect(func(yield func(string) bool) {
			for name := range tools {
				if !yield(name) {
					return
				}
			}
		}))
	}
	for _, hidden := range []string{connectorToolName("theirs", "search_code"), connectorToolName("rejected", "search_code")} {
		if tools[hidden] != nil {
			t.Fatalf("a connector the user may not use is offered: %v", tools[hidden])
		}
		if result := callRunTool(t, f.handler, token, hidden, map[string]any{"query": "x"}, nil); result["isError"] != true || !strings.Contains(runToolText(result), "no longer available") {
			t.Fatalf("calling a hidden connector tool: %v", result)
		}
	}
	if meta, _ := tools[shared]["_meta"].(map[string]any); meta[agentRunApprovalMeta] != nil {
		t.Fatalf("a connector that runs automatically is marked for approval: %v", meta)
	}

	result := callRunTool(t, f.handler, token, shared, map[string]any{"query": "roundTotal"}, nil)
	if result["isError"] == true || !strings.Contains(runToolText(result), "invoice.go:42") || calls.Load() != 1 {
		t.Fatalf("connector call = %v (calls %d)", result, calls.Load())
	}
	// Arguments the tool's schema rejects are refused before the connector is contacted.
	if result := callRunTool(t, f.handler, token, shared, map[string]any{"q": "roundTotal"}, nil); result["isError"] != true || calls.Load() != 1 {
		t.Fatalf("invalid arguments = %v (calls %d)", result, calls.Load())
	}
	// A run's allowlist without connector tools offers none.
	_, narrow := startWorkerTestRun(t, f.s, f.handler, store.AgentRunInput{WorkspaceKey: f.data.Workspace.URLKey, SessionID: "agent_session_connectors_narrow", Kind: store.AgentRunKindChat}, agentRunPrincipal{Mode: agentRunModeUser, UserID: viewer, Tools: []string{"get_workspace"}})
	if tools := listedRunTools(t, f.handler, narrow); len(tools) != 1 {
		t.Fatalf("allowlisted run tools = %v", tools)
	}

	// Connectors turned off for the workspace: the next call is refused.
	if err := f.s.store.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "settings.updated", "mcp", nil, func(data *domain.Bootstrap) error {
		off := false
		data.WorkspaceSettings.MCPConnectors = &off
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if result := callRunTool(t, f.handler, token, shared, map[string]any{"query": "roundTotal"}, nil); result["isError"] != true || calls.Load() != 1 {
		t.Fatalf("a call after connectors were turned off = %v (calls %d)", result, calls.Load())
	}
}

// A loop run contacts only the connectors it selects: none when it selects none.
func TestWorkerLoopRunOffersOnlySelectedConnectors(t *testing.T) {
	f := newAgentAttributionFixture(t)
	f.s.agent.WorkerSecret = testWorkerSecret
	var calls atomic.Int32
	var failing atomic.Bool
	upstream := fakeRuntimeConnector(t, &calls, &failing)
	setTestConnectors(t, f.s.store,
		applicationPolicy{ID: "one", Kind: "mcp", Name: "One", URL: upstream.URL + "/one", Status: "approved", Shared: true},
		applicationPolicy{ID: "two", Kind: "mcp", Name: "Two", URL: upstream.URL + "/two", Status: "approved", Shared: true},
	)
	viewer := f.data.Viewer.ID
	_, none := startWorkerTestRun(t, f.s, f.handler, store.AgentRunInput{WorkspaceKey: f.data.Workspace.URLKey, SessionID: "loop_none", Kind: store.AgentRunKindLoop}, agentRunPrincipal{Mode: agentRunModeUser, UserID: viewer})
	for name := range listedRunTools(t, f.handler, none) {
		if strings.HasPrefix(name, "external_") {
			t.Fatalf("a loop that selects no connector is offered %s", name)
		}
	}
	_, selected := startWorkerTestRun(t, f.s, f.handler, store.AgentRunInput{WorkspaceKey: f.data.Workspace.URLKey, SessionID: "loop_two", Kind: store.AgentRunKindLoop}, agentRunPrincipal{Mode: agentRunModeUser, UserID: viewer, Connectors: []string{"two"}})
	tools := listedRunTools(t, f.handler, selected)
	if tools[connectorToolName("two", "search_code")] == nil || tools[connectorToolName("one", "search_code")] != nil {
		t.Fatalf("a loop selecting two is offered %v", tools)
	}
}

// A connector set to always ask is marked for approval, and a call without the person's approval is held (not
// run); the approved call runs.
func TestWorkerRunConnectorApprovalHolds(t *testing.T) {
	f := newAgentAttributionFixture(t)
	f.s.agent.WorkerSecret = testWorkerSecret
	var calls atomic.Int32
	var failing atomic.Bool
	upstream := fakeRuntimeConnector(t, &calls, &failing)
	setTestConnectors(t, f.s.store, applicationPolicy{ID: "ask", Kind: "mcp", Name: "Asking code", URL: upstream.URL, Status: "approved", Shared: true, ToolApproval: connectorToolApprovalAlways})
	_, token := startWorkerTestRun(t, f.s, f.handler, store.AgentRunInput{WorkspaceKey: f.data.Workspace.URLKey, SessionID: "agent_session_ask", Kind: store.AgentRunKindChat}, agentRunPrincipal{Mode: agentRunModeUser, UserID: f.data.Viewer.ID})
	name := connectorToolName("ask", "search_code")
	tools := listedRunTools(t, f.handler, token)
	if meta, _ := tools[name]["_meta"].(map[string]any); meta[agentRunApprovalMeta] != connectorToolApprovalAlways {
		t.Fatalf("an always-ask connector tool is not marked: %v", tools[name])
	}
	held := callRunTool(t, f.handler, token, name, map[string]any{"query": "x"}, nil)
	if held["isError"] != true || !strings.Contains(runToolText(held), "approval") || calls.Load() != 0 {
		t.Fatalf("an unapproved call = %v (calls %d)", held, calls.Load())
	}
	approved := callRunTool(t, f.handler, token, name, map[string]any{"query": "x"}, map[string]any{agentRunApprovedMeta: true})
	if approved["isError"] == true || calls.Load() != 1 {
		t.Fatalf("an approved call = %v (calls %d)", approved, calls.Load())
	}
}

// A delegated worker run's connector call is recorded like an in-process one: the thread's action activity (the
// connector, the arguments and the result), the audit log, and — for an isError result with plain text — a
// failed call for the run guard.
func TestWorkerRunConnectorCallRecordsActivityAndIsError(t *testing.T) {
	f := newAgentAttributionFixture(t)
	f.s.agent.WorkerSecret = testWorkerSecret
	var calls atomic.Int32
	var failing atomic.Bool
	upstream := fakeRuntimeConnector(t, &calls, &failing)
	setTestConnectors(t, f.s.store, applicationPolicy{ID: "code", Kind: "mcp", Name: "Code search", URL: upstream.URL, Status: "approved", Shared: true})
	team := f.data.Teams[0].ID
	delegated := requestJSON[domain.Issue](t, f.handler, "POST", "/api/issues", map[string]any{"title": "Look into the invoice rounding", "teamId": team, "delegateId": f.app.UserID}, 201)
	task, err := f.s.store.AgentTask(t.Context(), f.data.Workspace.URLKey, delegated.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	principal := agentRunPrincipal{Mode: agentRunModeApp, UserID: f.data.Viewer.ID, AppUserID: f.app.UserID, TeamID: team}
	run, token := startWorkerTestRun(t, f.s, f.handler, store.AgentRunInput{WorkspaceKey: f.data.Workspace.URLKey, SessionID: task.ID, Kind: store.AgentRunKindDelegation}, principal)
	guard := func(body map[string]any) workerRunGuardResponse {
		return runtimeJSON[workerRunGuardResponse](t, f.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/guard", worker: "w1", body: body}, http.StatusOK)
	}
	if !guard(map[string]any{"op": "start"}).Enabled {
		t.Fatal("the delegated run has no guard")
	}
	noProgress := func() int {
		raw, _, _ := f.s.store.AgentRunKey(t.Context(), run.ID, workerRunGuardKey)
		var state workerRunGuardState
		_ = json.Unmarshal(raw, &state)
		return state.NoProgress
	}

	name := connectorToolName("code", "search_code")
	ok := callRunTool(t, f.handler, token, name, map[string]any{"query": "roundTotal"}, nil)
	if ok["isError"] == true {
		t.Fatalf("connector call = %v", ok)
	}
	guard(map[string]any{"op": "after", "tool": name, "arguments": `{"query":"roundTotal"}`, "content": runToolText(ok), "failed": false})
	if noProgress() != 0 {
		t.Fatal("a successful call counted as no progress")
	}
	// An identical repeat is answered by the guard and shows on the thread as not run.
	if !guard(map[string]any{"op": "before", "tool": name, "arguments": `{"query":"roundTotal"}`}).Handled {
		t.Fatal("an identical repeat was not answered by the guard")
	}

	before := noProgress() // the answered repeat counts as a call without progress
	failing.Store(true)
	failed := callRunTool(t, f.handler, token, name, map[string]any{"query": "invoice"}, nil)
	if failed["isError"] != true || runToolText(failed) != "search backend unavailable" {
		t.Fatalf("an isError result does not pass through flagged: %v", failed)
	}
	// The worker reports the plain text unflagged (an older worker): Flow saw isError and counts the failure.
	guard(map[string]any{"op": "after", "tool": name, "arguments": `{"query": "invoice"}`, "content": "search backend unavailable", "failed": false})
	if noProgress() != before+1 {
		t.Fatalf("an isError result was not counted as failed: no progress %d", noProgress())
	}

	activities, err := f.s.store.AgentActivities(t.Context(), f.data.Workspace.URLKey, task.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	actions := []domain.AgentActivity{}
	for _, activity := range activities {
		if activity.Type == "action" && activity.Metadata["connector"] == "Code search" {
			actions = append(actions, activity)
		}
	}
	if len(actions) != 3 {
		t.Fatalf("connector action activities = %+v", actions)
	}
	first, repeat, second := actions[0], actions[1], actions[2]
	if result, _ := repeat.Metadata["result"].(string); !strings.HasPrefix(result, "Not run: ") {
		t.Fatalf("repeat action = %+v", repeat)
	}
	if first.Body != "search_code" || first.ActorID != f.app.UserID || first.Metadata["parameter"] != `Code search {"query":"roundTotal"}` || !strings.Contains(first.Metadata["result"].(string), "invoice.go:42") {
		t.Fatalf("first action = %+v", first)
	}
	if result, _ := second.Metadata["result"].(string); !strings.HasPrefix(result, "Error: ") || !strings.Contains(result, "search backend unavailable") {
		t.Fatalf("failed action = %+v", second)
	}
	metadata, _ := f.s.store.WorkspaceMetadata(f.data.Workspace.URLKey)
	audited := 0
	for _, entry := range metadata.AuditLog {
		if entry.Action == "agent_run.mcp_tool_call" && entry.ResourceID == run.ID {
			audited++
			if entry.Metadata["outcome"] != "ok" && entry.Metadata["outcome"] != "toolError" {
				t.Fatalf("audit entry = %+v", entry)
			}
		}
	}
	if audited != 2 {
		t.Fatalf("audited connector calls = %d", audited)
	}
}
