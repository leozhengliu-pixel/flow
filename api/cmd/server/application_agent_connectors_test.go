package main

import (
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
)

// fakeCodeMCP is a streamable-HTTP MCP server with read-only code tools; tools/list returns toolCount tools.
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
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}}
		case "notifications/initialized":
			w.WriteHeader(202)
			return
		case "tools/list":
			tools := []any{map[string]any{"name": "search_code", "description": "Search the repository", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}}}
			for index := int32(1); index < toolCount.Load(); index++ {
				tools = append(tools, map[string]any{"name": fmt.Sprintf("tool_%d", index), "inputSchema": map[string]any{"type": "object"}})
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

// A delegated issue gets the delegating user's connector tools like chat: the call waits for approval, the approval
// runs it through the delegation path with the call and its result in the activity log, and a connector revoked
// before a held call is approved is refused and no longer offered.
func TestBuiltinDelegationUsesConnectorTools(t *testing.T) {
	s, handler, data, _ := applicationFixture(t)
	var calls, toolCount atomic.Int32
	toolCount.Store(1)
	upstream := fakeCodeMCP(t, &calls, &toolCount)
	defer upstream.Close()
	hash := sha256.Sum256([]byte("code\x00search_code"))
	external := fmt.Sprintf("external_%x", hash[:16])

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
	searchCall := func(id string) string {
		return "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"" + id + "\",\"function\":{\"name\":\"" + external + "\",\"arguments\":\"{\\\"query\\\":\\\"roundTotal\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
	}
	s.agent = appconfig.AgentConfig{Enabled: true, ToolsEnabled: true, WriteTools: true, Protocol: "openai-chat-completions", BaseURL: provider.URL, Model: "test", APIKey: "test", Timeout: 5 * time.Second}
	s.agentClient = provider.Client()
	app, err := s.store.InstallApplication(t.Context(), data.Workspace.URLKey, domain.ApplicationInstallation{ClientID: "builtin-flow-agent", Name: "Flow Agent", Builtin: true, Active: true, InstalledBy: data.Viewer.ID, Scopes: []string{"read", "write", "app:assignable"}, TeamIDs: []string{data.Teams[0].ID}}, "")
	if err != nil {
		t.Fatal(err)
	}
	setConnectors := func(enabled bool, policies ...applicationPolicy) {
		t.Helper()
		if err := s.store.MutateWorkspace(t.Context(), data.Workspace.URLKey, "application_policy.updated", "code", nil, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.MCPConnectorsEnabled = enabled
			setApplicationPolicies(data, policies)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	codePolicy := applicationPolicy{ID: "code", Kind: "mcp", Name: "Code search", URL: upstream.URL, Status: "approved", OwnerID: data.Viewer.ID}
	// Another member's personal connector is never offered to this delegator.
	setConnectors(true, codePolicy, applicationPolicy{ID: "private", Kind: "mcp", Name: "Private", URL: upstream.URL, Status: "approved", OwnerID: "someone-else"})

	mu.Lock()
	script[0] = searchCall("c_1")
	mu.Unlock()
	issue := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "Where are invoice totals rounded?", "teamId": data.Teams[0].ID, "delegateId": app.UserID}, 201)
	task, _ := s.store.AgentTask(t.Context(), data.Workspace.URLKey, issue.AgentSessionID)
	s.deliverApplicationTask(t.Context(), task)
	task, _ = s.store.AgentTask(t.Context(), data.Workspace.URLKey, task.ID)
	mu.Lock()
	first := requests[0]
	mu.Unlock()
	privateHash := sha256.Sum256([]byte("private\x00search_code"))
	if !strings.Contains(first, external) || strings.Contains(first, fmt.Sprintf("%x", privateHash[:16])) || !strings.Contains(first, "connected MCP servers (Code search)") {
		t.Fatalf("delegation must offer the delegator's connector tools: %s", first)
	}
	if task.Status != "awaitingInput" || task.PendingTool == nil || calls.Load() != 0 {
		t.Fatalf("a connector call waits for approval: status=%s pending=%+v calls=%d", task.Status, task.PendingTool, calls.Load())
	}
	if activities, _ := s.store.AgentActivities(t.Context(), data.Workspace.URLKey, task.ID, ""); !strings.Contains(activities[len(activities)-1].Body, "Allow Code search: search_code?") {
		t.Fatalf("the approval names the connector and its tool: %+v", activities[len(activities)-1])
	}

	task = requestJSON[domain.AgentTask](t, handler, "POST", "/api/agent-tasks/"+task.ID+"/activities", map[string]any{"expectedVersion": task.Version, "type": "prompt", "body": agentApprovalApprove}, 200)
	s.deliverApplicationTask(t.Context(), task)
	task, _ = s.store.AgentTask(t.Context(), data.Workspace.URLKey, task.ID)
	if task.Status != "complete" || calls.Load() != 1 {
		t.Fatalf("the approved connector call must run: status=%s calls=%d", task.Status, calls.Load())
	}
	activities, _ := s.store.AgentActivities(t.Context(), data.Workspace.URLKey, task.ID, "")
	logged := false
	for _, activity := range activities {
		if activity.Type == "action" && activity.Body == "search_code" && !activity.Ephemeral {
			result, _ := activity.Metadata["result"].(string)
			parameter, _ := activity.Metadata["parameter"].(string)
			logged = strings.Contains(result, "src/billing/invoice.go:42") && strings.Contains(parameter, "Code search") && strings.Contains(parameter, "roundTotal")
		}
	}
	if !logged {
		t.Fatalf("the connector call and its result are missing from the activity log: %+v", activities)
	}
	mu.Lock()
	if !strings.Contains(requests[len(requests)-1], "invoice.go:42") {
		t.Fatalf("the connector result must reach the model: %s", requests[len(requests)-1])
	}
	script[len(requests)] = searchCall("c_2")
	mu.Unlock()

	// A held call approved after the connector was disconnected is refused, and the next turn is not offered it.
	task = requestJSON[domain.AgentTask](t, handler, "POST", "/api/agent-tasks/"+task.ID+"/activities", map[string]any{"expectedVersion": task.Version, "type": "prompt", "body": "Search again"}, 200)
	s.deliverApplicationTask(t.Context(), task)
	task, _ = s.store.AgentTask(t.Context(), data.Workspace.URLKey, task.ID)
	if task.PendingTool == nil {
		t.Fatalf("the second call should wait for approval: %+v", task)
	}
	mu.Lock()
	if followUp := requests[len(requests)-1]; !strings.Contains(followUp, "Earlier connector call search_code") || !strings.Contains(followUp, "invoice.go:42") {
		mu.Unlock()
		t.Fatalf("a later run keeps earlier connector results: %s", followUp)
	}
	mu.Unlock()
	setConnectors(true)
	mu.Lock()
	before := len(requests)
	mu.Unlock()
	task = requestJSON[domain.AgentTask](t, handler, "POST", "/api/agent-tasks/"+task.ID+"/activities", map[string]any{"expectedVersion": task.Version, "type": "prompt", "body": agentApprovalApprove}, 200)
	s.deliverApplicationTask(t.Context(), task)
	mu.Lock()
	defer mu.Unlock()
	if calls.Load() != 1 {
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
