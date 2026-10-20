package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// fakeCodeMCP is a streamable-HTTP MCP server with code tools; tools/list returns toolCount tools: search_code
// (no annotations), delete_branch (destructiveHint), list_branches (readOnlyHint and destructiveHint), then
// unannotated tool_N.
func fakeCodeMCP(t *testing.T, calls *atomic.Int32, toolCount *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			w.WriteHeader(200)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&request) != nil {
			w.WriteHeader(400)
			return
		}
		var result any
		switch request.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "code-session")
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "code-search", "title": "Code Search", "version": "1"}}
		case "notifications/initialized":
			w.WriteHeader(202)
			return
		case "tools/list":
			tools := []any{map[string]any{"name": "search_code", "description": "Search the repository", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}}}
			for index := int32(1); index < toolCount.Load(); index++ {
				tool := map[string]any{"name": fmt.Sprintf("tool_%d", index), "inputSchema": map[string]any{"type": "object"}}
				switch index {
				case 1:
					tool["name"], tool["annotations"] = "delete_branch", map[string]any{"destructiveHint": true}
				case 2:
					tool["name"], tool["annotations"] = "list_branches", map[string]any{"readOnlyHint": true, "destructiveHint": true}
				}
				tools = append(tools, tool)
			}
			result = map[string]any{"tools": tools}
		case "tools/call":
			calls.Add(1)
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "acme/api@main src/billing/invoice.go:42: func roundTotal(cents int64) int64"}}, "isError": false}
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32601, "message": "Method not found"}})
			return
		}
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
}

// connectorToolName is the external_ name an agent is offered for a connector's tool.
func connectorToolName(policyID, tool string) string {
	hash := sha256.Sum256([]byte(policyID + "\x00" + tool))
	return fmt.Sprintf("external_%x", hash[:16])
}

// setTestConnectors enables MCP connectors in the fixture workspace and replaces its connectors.
func setTestConnectors(t *testing.T, repository *store.SQLiteStore, policies ...applicationPolicy) {
	t.Helper()
	if err := repository.MutateWorkspace(t.Context(), repository.Bootstrap().Workspace.URLKey, "application_policy.updated", "code", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.MCPConnectorsEnabled = true
		setApplicationPolicies(data, policies)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Connector tools run without asking, whatever their annotations (destructive ones included), unless their
// connector is set to always ask.
func TestConnectorToolApprovalRule(t *testing.T) {
	var calls, toolCount atomic.Int32
	toolCount.Store(4)
	upstream := fakeCodeMCP(t, &calls, &toolCount)
	defer upstream.Close()
	s := &server{authDisabled: true}
	s.agent.ToolsEnabled, s.agent.WriteTools = true, true
	for _, approval := range []string{"", connectorToolApprovalAlways} {
		data := domain.Bootstrap{WorkspaceSettings: domain.WorkspaceSettings{MCPConnectorsEnabled: true}, Settings: map[string]any{}}
		setApplicationPolicies(&data, []applicationPolicy{{ID: "code", Kind: "mcp", Name: "Code search", URL: upstream.URL, Status: "approved", Shared: true, ToolApproval: approval}})
		ctx, tools, err := s.withConnectorTools(t.Context(), data, connectorRequestContext{UserID: "user"})
		if err != nil || len(tools) != 4 {
			t.Fatalf("discovery: %v %d", err, len(tools))
		}
		for _, name := range []string{"search_code", "delete_branch", "list_branches", "tool_3"} {
			held := approval == connectorToolApprovalAlways
			if got := agentToolNeedsApproval(ctx, connectorToolName("code", name)); got != held {
				t.Errorf("toolApproval=%q %s: needs approval %v, want %v", approval, name, got, held)
			}
		}
		if agentToolNeedsApproval(ctx, connectorToolName("gone", "search_code")) || agentToolNeedsApproval(ctx, "save_issue") {
			t.Fatal("a tool that is not an offered connector tool does not wait for approval")
		}
	}
}

// A connector's tool approval setting is saved as "always" or cleared; other values are not stored.
func TestApplicationPolicyToolApproval(t *testing.T) {
	s, handler, _, _ := applicationFixture(t)
	setTestConnectors(t, s.store)
	save := func(value string) applicationPolicy {
		return requestJSON[applicationPolicy](t, handler, "PUT", "/api/application-policies", map[string]any{"id": "code", "name": "Code search", "kind": "mcp", "url": "http://127.0.0.1:9/mcp", "status": "approved", "toolApproval": value}, 200)
	}
	if saved := save("always"); saved.ToolApproval != connectorToolApprovalAlways {
		t.Fatalf("always ask was not saved: %+v", saved)
	}
	if saved := save("sometimes"); saved.ToolApproval != "" {
		t.Fatalf("an unknown tool approval value was stored: %+v", saved)
	}
}

// Connecting records the connector's tools and takes the server's name for a connector named after its
// host; a display name renames it without losing the inventory, and a 401 asks for authorization.
func TestConnectConnectorRecordsToolsAndRenames(t *testing.T) {
	var calls, toolCount atomic.Int32
	toolCount.Store(3)
	upstream := fakeCodeMCP(t, &calls, &toolCount)
	defer upstream.Close()
	s, handler, _, _ := applicationFixture(t)
	setTestConnectors(t, s.store)
	created := requestJSON[applicationPolicy](t, handler, "PUT", "/api/application-policies", map[string]any{"name": "127.0.0.1", "kind": "mcp", "url": upstream.URL, "tools": []string{"forged"}}, 200)
	if len(created.Tools) != 0 || created.ConnectedAt != nil {
		t.Fatalf("clients cannot set the tool inventory: %+v", created)
	}
	connected := requestJSON[connectorConnectResult](t, handler, "POST", "/api/application-policies/"+created.ID+"/connect", map[string]any{}, 200)
	if !connected.Connected || connected.Policy == nil || strings.Join(connected.Policy.Tools, ",") != "search_code,delete_branch,list_branches" || connected.Policy.Name != "Code Search" || connected.Policy.ConnectedAt == nil {
		t.Fatalf("connect: %+v %+v", connected, connected.Policy)
	}
	renamed := requestJSON[applicationPolicy](t, handler, "PUT", "/api/application-policies", map[string]any{"id": created.ID, "name": "Code Search", "kind": "mcp", "url": upstream.URL, "displayName": "  Repo search  "}, 200)
	if renamed.DisplayName != "Repo search" || renamed.label() != "Repo search" || len(renamed.Tools) != 3 || renamed.ConnectedAt == nil {
		t.Fatalf("rename: %+v", renamed)
	}
	cleared := requestJSON[applicationPolicy](t, handler, "PUT", "/api/application-policies", map[string]any{"id": created.ID, "name": "Code Search", "kind": "mcp", "url": upstream.URL, "displayName": ""}, 200)
	if cleared.label() != "Code Search" {
		t.Fatalf("clearing the display name shows the default name: %+v", cleared)
	}
	moved := requestJSON[applicationPolicy](t, handler, "PUT", "/api/application-policies", map[string]any{"id": created.ID, "name": "Code Search", "kind": "mcp", "url": upstream.URL + "/other"}, 200)
	if len(moved.Tools) != 0 || moved.ConnectedAt != nil {
		t.Fatalf("a new endpoint drops the old inventory: %+v", moved)
	}

	locked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer locked.Close()
	private := requestJSON[applicationPolicy](t, handler, "PUT", "/api/application-policies", map[string]any{"name": "Locked", "kind": "mcp", "url": locked.URL}, 200)
	refused := requestJSON[connectorConnectResult](t, handler, "POST", "/api/application-policies/"+private.ID+"/connect", map[string]any{}, 200)
	if refused.Connected || !refused.AuthorizationRequired || refused.Error == "" {
		t.Fatalf("401 must ask for authorization: %+v", refused)
	}
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	down.Close()
	gone := requestJSON[applicationPolicy](t, handler, "PUT", "/api/application-policies", map[string]any{"name": "Gone", "kind": "mcp", "url": down.URL}, 200)
	if failed := requestJSON[connectorConnectResult](t, handler, "POST", "/api/application-policies/"+gone.ID+"/connect", map[string]any{}, 200); failed.Connected || failed.AuthorizationRequired || failed.Error == "" {
		t.Fatalf("an unreachable server is an error, not an authorization request: %+v", failed)
	}
}

// Chat runs a connector tool without annotations straight through and shows the call and its result.
func TestAgentSessionConnectorToolsRunWithoutApproval(t *testing.T) {
	var providerCalls, connectorCalls, toolCount atomic.Int32
	toolCount.Store(1)
	upstream := fakeCodeMCP(t, &connectorCalls, &toolCount)
	defer upstream.Close()
	external := connectorToolName("code", "search_code")
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := providerCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if call == 1 {
			_, _ = w.Write([]byte("event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"item_search\",\"call_id\":\"call_search\",\"type\":\"function_call\",\"name\":\"" + external + "\",\"arguments\":\"{}\"}}\n\n" +
				"event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"item_search\",\"arguments\":\"{\\\"query\\\":\\\"roundTotal\\\"}\"}\n\n" +
				"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
			return
		}
		_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Found it.\"}\n\n" +
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
	}))
	defer provider.Close()
	repository, err := store.OpenSQLiteTestFixture(t.TempDir() + "/flow.db")
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	setTestConnectors(t, repository, applicationPolicy{ID: "code", Kind: "mcp", Name: "Code search", URL: upstream.URL, Status: "approved", Shared: true})
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: provider.URL, Model: "flow-test", MaxOutputTokens: 256, ToolsEnabled: true, WriteTools: true}, agentClient: provider.Client()}
	handler := newHandler(s)
	defer s.stopDeliveryScheduler(context.Background())
	host := httptest.NewServer(handler)
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, host.URL+"/api/agent/sessions/stream", strings.NewReader(`{"message":"Where are totals rounded?","location":"page"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := host.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	stream := string(body)
	if strings.Contains(stream, "tool.approval_required") || connectorCalls.Load() != 1 || providerCalls.Load() != 2 || !strings.Contains(stream, "Found it.") || !strings.Contains(stream, "invoice.go:42") {
		t.Fatalf("an unannotated connector tool must run without approval calls=%d/%d: %s", providerCalls.Load(), connectorCalls.Load(), stream)
	}
}

// A delegated issue gets the delegating user's connector tools like chat: a tool without annotations runs at once
// and is logged with its result, so do tools marked destructive or read-only, a connector set to always ask holds
// every call, and a connector revoked before a held call is approved is refused and no longer
// offered.
func TestBuiltinDelegationUsesConnectorTools(t *testing.T) {
	s, handler, data, _ := applicationFixture(t)
	var calls, toolCount atomic.Int32
	toolCount.Store(3)
	upstream := fakeCodeMCP(t, &calls, &toolCount)
	defer upstream.Close()
	external := connectorToolName("code", "search_code")

	var mu sync.Mutex
	var requests []string
	script := map[int]string{}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, string(raw))
		reply, ok := script[len(requests)-1]
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if !ok {
			reply = "data: {\"choices\":[{\"delta\":{\"content\":\"Found it in src/billing/invoice.go:42.\"}}]}\n\ndata: [DONE]\n\n"
		}
		_, _ = io.WriteString(w, reply)
	}))
	defer provider.Close()
	toolCall := func(id, tool string) string {
		return "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"" + id + "\",\"function\":{\"name\":\"" + connectorToolName("code", tool) + "\",\"arguments\":\"{\\\"query\\\":\\\"roundTotal\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
	}
	// next scripts the model's next request to call tool.
	next := func(id, tool string) {
		mu.Lock()
		script[len(requests)] = toolCall(id, tool)
		mu.Unlock()
	}
	s.agent = appconfig.AgentConfig{Enabled: true, ToolsEnabled: true, WriteTools: true, Protocol: "openai-chat-completions", BaseURL: provider.URL, Model: "test", APIKey: "test", Timeout: 5 * time.Second}
	s.agentClient = provider.Client()
	app, err := s.store.InstallApplication(t.Context(), data.Workspace.URLKey, domain.ApplicationInstallation{ClientID: "builtin-flow-agent", Name: "Flow Agent", Builtin: true, Active: true, InstalledBy: data.Viewer.ID, Scopes: []string{"read", "write", "app:assignable"}, TeamIDs: []string{data.Teams[0].ID}}, "")
	if err != nil {
		t.Fatal(err)
	}
	setConnectors := func(policies ...applicationPolicy) {
		t.Helper()
		if err := s.store.MutateWorkspace(t.Context(), data.Workspace.URLKey, "application_policy.updated", "code", nil, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.MCPConnectorsEnabled = true
			setApplicationPolicies(data, policies)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	codePolicy := applicationPolicy{ID: "code", Kind: "mcp", Name: "Code search", URL: upstream.URL, Status: "approved", OwnerID: data.Viewer.ID}
	// Another member's personal connector is never offered to this delegator.
	setConnectors(codePolicy, applicationPolicy{ID: "private", Kind: "mcp", Name: "Private", URL: upstream.URL, Status: "approved", OwnerID: "someone-else"})
	lastActivity := func(task domain.AgentTask) domain.AgentActivity {
		activities, _ := s.store.AgentActivities(t.Context(), data.Workspace.URLKey, task.ID, "")
		return activities[len(activities)-1]
	}
	prompt := func(task domain.AgentTask, body string) domain.AgentTask {
		task = requestJSON[domain.AgentTask](t, handler, "POST", "/api/agent-tasks/"+task.ID+"/activities", map[string]any{"expectedVersion": task.Version, "type": "prompt", "body": body}, 200)
		s.deliverApplicationTask(t.Context(), task)
		task, _ = s.store.AgentTask(t.Context(), data.Workspace.URLKey, task.ID)
		return task
	}

	// A tool without annotations runs without asking.
	next("c_1", "search_code")
	issue := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "Where are invoice totals rounded?", "teamId": data.Teams[0].ID, "delegateId": app.UserID}, 201)
	task, _ := s.store.AgentTask(t.Context(), data.Workspace.URLKey, issue.AgentSessionID)
	s.deliverApplicationTask(t.Context(), task)
	task, _ = s.store.AgentTask(t.Context(), data.Workspace.URLKey, task.ID)
	mu.Lock()
	first := requests[0]
	mu.Unlock()
	if !strings.Contains(first, external) || strings.Contains(first, connectorToolName("private", "search_code")) || !strings.Contains(first, "connected MCP servers (Code search)") {
		t.Fatalf("delegation must offer the delegator's connector tools: %s", first)
	}
	if task.Status != "complete" || task.PendingTool != nil || calls.Load() != 1 {
		t.Fatalf("an unannotated connector call runs without approval: status=%s pending=%+v calls=%d", task.Status, task.PendingTool, calls.Load())
	}
	activities, _ := s.store.AgentActivities(t.Context(), data.Workspace.URLKey, task.ID, "")
	logged := false
	for _, activity := range activities {
		if activity.Type == "action" && activity.Body == "search_code" && !activity.Ephemeral {
			result, _ := activity.Metadata["result"].(string)
			parameter, _ := activity.Metadata["parameter"].(string)
			logged = strings.Contains(result, "src/billing/invoice.go:42") && strings.Contains(parameter, "Code search") && strings.Contains(parameter, "roundTotal")
		}
		if strings.Contains(activity.Body, "Approved action") || strings.Contains(activity.Body, "Allow ") {
			t.Fatalf("an automatic connector call must not read as an approval: %+v", activity)
		}
	}
	if !logged {
		t.Fatalf("the connector call and its result are missing from the activity log: %+v", activities)
	}
	mu.Lock()
	if !strings.Contains(requests[len(requests)-1], "invoice.go:42") {
		t.Fatalf("the connector result must reach the model: %s", requests[len(requests)-1])
	}
	mu.Unlock()

	// A tool the server marks destructive runs without asking too, and a later run keeps earlier connector results.
	next("c_2", "delete_branch")
	task = prompt(task, "Delete the branch")
	if task.Status != "complete" || task.PendingTool != nil || calls.Load() != 2 {
		t.Fatalf("a destructive connector call runs without approval: status=%s pending=%+v calls=%d", task.Status, task.PendingTool, calls.Load())
	}
	mu.Lock()
	if followUp := requests[len(requests)-1]; !strings.Contains(followUp, "Earlier connector call search_code") || !strings.Contains(followUp, "invoice.go:42") {
		mu.Unlock()
		t.Fatalf("a later run keeps earlier connector results: %s", followUp)
	}
	mu.Unlock()

	// So does a tool marked both read-only and destructive.
	next("c_3", "list_branches")
	task = prompt(task, "List branches")
	if task.Status != "complete" || task.PendingTool != nil || calls.Load() != 3 {
		t.Fatalf("a read-only connector call runs without approval: status=%s calls=%d", task.Status, calls.Load())
	}

	// A connector set to always ask holds every call; one approved after the connector was disconnected is refused,
	// and the next turn is not offered it.
	codePolicy.ToolApproval = connectorToolApprovalAlways
	setConnectors(codePolicy)
	next("c_4", "search_code")
	task = prompt(task, "Search again")
	if task.PendingTool == nil || calls.Load() != 3 {
		t.Fatalf("a connector set to always ask must hold the call: %+v calls=%d", task, calls.Load())
	}
	if body := lastActivity(task).Body; !strings.Contains(body, "Allow Code search: search_code?") {
		t.Fatalf("the approval names the connector and its tool: %s", body)
	}
	setConnectors()
	mu.Lock()
	before := len(requests)
	mu.Unlock()
	prompt(task, agentApprovalApprove)
	mu.Lock()
	defer mu.Unlock()
	if calls.Load() != 3 {
		t.Fatalf("a revoked connector was still called (%d calls)", calls.Load())
	}
	resumed := requests[before]
	var payload struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	_ = json.Unmarshal([]byte(resumed), &payload)
	offered := false
	for _, tool := range payload.Tools {
		offered = offered || tool.Function.Name == external
	}
	if len(payload.Tools) == 0 || offered || !strings.Contains(resumed, "no longer available") {
		t.Fatalf("a revoked connector must not be offered and the held call must be refused: %s", resumed)
	}
}

// Connector discovery is bounded: too many tools fail discovery (a delegated run continues without them and says
// why), and logged results are cut to their text.
func TestConnectorToolBounds(t *testing.T) {
	var calls, toolCount atomic.Int32
	toolCount.Store(maxConnectorTools + 1)
	upstream := fakeCodeMCP(t, &calls, &toolCount)
	defer upstream.Close()
	s := &server{authDisabled: true}
	s.agent.ToolsEnabled, s.agent.WriteTools = true, true
	data := domain.Bootstrap{WorkspaceSettings: domain.WorkspaceSettings{MCPConnectorsEnabled: true}, Settings: map[string]any{}}
	setApplicationPolicies(&data, []applicationPolicy{{ID: "code", Kind: "mcp", Name: "Code search", URL: upstream.URL, Status: "approved", Shared: true}})
	if _, tools, err := s.withConnectorTools(t.Context(), data, connectorRequestContext{UserID: "user"}); err == nil || len(tools) != 0 {
		t.Fatalf("more than %d tools must fail discovery: %v %d", maxConnectorTools, err, len(tools))
	}
	toolCount.Store(maxConnectorTools)
	if _, tools, err := s.withConnectorTools(t.Context(), data, connectorRequestContext{UserID: "user"}); err != nil || len(tools) != maxConnectorTools {
		t.Fatalf("discovery up to the bound: %v %d", err, len(tools))
	}
	text := connectorResultText([]byte(`{"content":[{"type":"text","text":"`+strings.Repeat("é", 50)+`"}]}`), 11)
	if !strings.HasSuffix(text, "…") || len(text) > 11+len("…") || !strings.HasPrefix(text, "ééééé") {
		t.Fatalf("logged result is not cut to its text: %q", text)
	}
}
