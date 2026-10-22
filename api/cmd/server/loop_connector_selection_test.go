package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// countingMCP is a local streamable-HTTP MCP server that counts initialize
// and tools/list requests. It can answer slowly, fail with a status, or hold
// every request until the client gives up.
type countingMCP struct {
	*httptest.Server
	initialize, list atomic.Int32
}

type countingMCPMode struct {
	delay  time.Duration
	status int
	block  bool
}

func newCountingMCP(t *testing.T, tool string, mode countingMCPMode) *countingMCP {
	t.Helper()
	server := &countingMCP{}
	stop := make(chan struct{})
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		switch request.Method {
		case "initialize":
			server.initialize.Add(1)
		case "tools/list":
			server.list.Add(1)
		}
		if mode.block {
			select {
			case <-r.Context().Done():
			case <-stop:
			}
			return
		}
		if mode.delay > 0 {
			select {
			case <-time.After(mode.delay):
			case <-r.Context().Done():
				return
			case <-stop:
				return
			}
		}
		if mode.status != 0 {
			w.WriteHeader(mode.status)
			return
		}
		var result any
		switch request.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-"+tool)
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": tool, "version": "1"}}
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": tool, "description": "A test tool", "inputSchema": map[string]any{"type": "object"}}}}
		default:
			result = map[string]any{}
		}
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
	t.Cleanup(server.Close)
	// Runs first: held requests end before Close waits for them.
	t.Cleanup(func() { close(stop) })
	return server
}

func (server *countingMCP) requests() int32 {
	return server.initialize.Load() + server.list.Load()
}

func offeredExternalTools(provider *fakeLoopProvider) []string {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	names := []string{}
	if len(provider.tools) > 0 {
		for _, name := range provider.tools[0] {
			if strings.HasPrefix(name, "external_") {
				names = append(names, name)
			}
		}
	}
	return names
}

func TestLoopRunDiscoversOnlySelectedConnectors(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newReliableLoopServer(t, provider)
	code := newCountingMCP(t, "search_code", countingMCPMode{})
	docs := newCountingMCP(t, "search_docs", countingMCPMode{})
	slow := newCountingMCP(t, "slow_tool", countingMCPMode{delay: 3 * time.Second})
	broken := newCountingMCP(t, "broken_tool", countingMCPMode{status: http.StatusInternalServerError})
	expired := newCountingMCP(t, "expired_tool", countingMCPMode{status: http.StatusUnauthorized})
	hang := newCountingMCP(t, "hang_tool", countingMCPMode{block: true})
	setTestConnectors(t, srv.store,
		applicationPolicy{ID: "code", Kind: "mcp", Name: "Code", URL: code.URL, Status: "approved", Shared: true},
		applicationPolicy{ID: "docs", Kind: "mcp", Name: "Docs", URL: docs.URL, Status: "approved", Shared: true},
		applicationPolicy{ID: "slow", Kind: "mcp", Name: "Slow", URL: slow.URL, Status: "approved", Shared: true},
		applicationPolicy{ID: "broken", Kind: "mcp", Name: "Broken", URL: broken.URL, Status: "approved", Shared: true},
		applicationPolicy{ID: "expired", Kind: "mcp", Name: "Expired", URL: expired.URL, Status: "approved", Shared: true},
		applicationPolicy{ID: "hang", Kind: "mcp", Name: "Hang", URL: hang.URL, Status: "approved", Shared: true},
		applicationPolicy{ID: "private", Kind: "mcp", Name: "Private", URL: code.URL, Status: "approved", OwnerID: "someone-else"},
	)
	all := []*countingMCP{code, docs, slow, broken, expired, hang}
	counts := func() []int32 {
		values := []int32{}
		for _, server := range all {
			values = append(values, server.requests())
		}
		return values
	}
	reset := func() {
		for _, server := range all {
			server.initialize.Store(0)
			server.list.Store(0)
		}
	}
	run := func(name string, connectors []string) domain.LoopRun {
		t.Helper()
		reset()
		provider.script(`tool:finish_run {"status":"done","summary":"Done."}`)
		loop, started := startTestRun(t, handler, name, "Summarize open bugs.", map[string]any{"connectorIds": connectors})
		return waitForRun(t, handler, loop.ID, started.ID)
	}

	// No selection: no external request, and slow or failing connectors in
	// the workspace do not delay the run.
	got := run("Flow only", []string{})
	if got.Status != "completed" || got.FinishedAt.Sub(got.StartedAt) > 1500*time.Millisecond || len(offeredExternalTools(provider)) != 0 {
		t.Fatalf("flow-only run: %s in %s, external tools %v", got.Status, got.FinishedAt.Sub(got.StartedAt), offeredExternalTools(provider))
	}
	for index, count := range counts() {
		if count != 0 {
			t.Errorf("flow-only run contacted connector %d (%d requests)", index, count)
		}
	}

	// One selected connector: only it is contacted, and only its tools offered.
	got = run("Code only", []string{"code"})
	if got.Status != "completed" || code.initialize.Load() != 1 || code.list.Load() != 1 || docs.requests()+slow.requests()+broken.requests()+expired.requests()+hang.requests() != 0 {
		t.Fatalf("code-only run %s: counts %v", got.Status, counts())
	}
	if tools := offeredExternalTools(provider); len(tools) != 1 || tools[0] != connectorToolName("code", "search_code") {
		t.Fatalf("code-only tools = %v", tools)
	}
	// A different selection discovers its own connector, not the earlier one's.
	got = run("Docs only", []string{"docs"})
	if code.requests() != 0 || docs.initialize.Load() != 1 || len(offeredExternalTools(provider)) != 1 || offeredExternalTools(provider)[0] != connectorToolName("docs", "search_docs") {
		t.Fatalf("docs-only run: counts %v tools %v", counts(), offeredExternalTools(provider))
	}

	// Selected connectors that fail (server error, expired authorization) or
	// that the owner may not use are reported; the others keep their tools.
	got = run("Partial", []string{"code", "broken", "expired", "private"})
	notices := strings.Join(got.Notices, "\n")
	if got.Status != "completed" || len(offeredExternalTools(provider)) != 1 || !strings.Contains(notices, "Broken:") || !strings.Contains(notices, "Expired:") || !strings.Contains(notices, "Private: the connection is not available to this user") {
		t.Fatalf("partial run %s tools %v notices %q", got.Status, offeredExternalTools(provider), notices)
	}
	if !strings.Contains(notices, "authorization") {
		t.Errorf("the expired connector's notice does not say it needs authorization: %q", notices)
	}

	// The discovery budget bounds a slow selected connector.
	budget := loopConnectorDiscoveryBudget
	loopConnectorDiscoveryBudget = 300 * time.Millisecond
	got = run("Slow selected", []string{"code", "slow"})
	loopConnectorDiscoveryBudget = budget
	if got.Status != "completed" || got.FinishedAt.Sub(got.StartedAt) > 2*time.Second || len(offeredExternalTools(provider)) != 1 || !strings.Contains(strings.Join(got.Notices, "\n"), "Slow:") {
		t.Fatalf("slow selected run %s in %s tools %v notices %v", got.Status, got.FinishedAt.Sub(got.StartedAt), offeredExternalTools(provider), got.Notices)
	}

	// Required policy: a failing selected connector fails the run before the model runs.
	t.Setenv("FLOW_LOOP_CONNECTOR_POLICY", "required")
	got = run("Required", []string{"code", "broken"})
	if got.Status != "failed" || got.FailureReason != "unavailable" || !strings.Contains(got.Error, "Broken") || provider.calls() != 0 {
		t.Fatalf("required run %s %s %q after %d model calls", got.Status, got.FailureReason, got.Error, provider.calls())
	}
	t.Setenv("FLOW_LOOP_CONNECTOR_POLICY", "")

	// Cancelling a run while a selected connector hangs stops the discovery.
	reset()
	provider.script(`tool:finish_run {"status":"done","summary":"Done."}`)
	loop, started := startTestRun(t, handler, "Hanging", "Summarize open bugs.", map[string]any{"connectorIds": []string{"hang"}})
	deadline := time.Now().Add(5 * time.Second)
	for hang.initialize.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	began := time.Now()
	cancelled := requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs/"+started.ID+"/cancel", nil, http.StatusOK)
	if cancelled.Status != "cancelled" || time.Since(began) > 3*time.Second || provider.calls() != 0 {
		t.Fatalf("cancel during discovery: %s after %s, %d model calls", cancelled.Status, time.Since(began), provider.calls())
	}
}

// Chat keeps its contract: every connector the user may use is discovered.
func TestChatConnectorDiscoveryUnchanged(t *testing.T) {
	code := newCountingMCP(t, "search_code", countingMCPMode{})
	docs := newCountingMCP(t, "search_docs", countingMCPMode{})
	s := &server{authDisabled: true}
	s.agent.ToolsEnabled, s.agent.WriteTools = true, true
	data := domain.Bootstrap{WorkspaceSettings: domain.WorkspaceSettings{MCPConnectorsEnabled: true}, Settings: map[string]any{}}
	setApplicationPolicies(&data, []applicationPolicy{
		{ID: "code", Kind: "mcp", Name: "Code", URL: code.URL, Status: "approved", Shared: true},
		{ID: "docs", Kind: "mcp", Name: "Docs", URL: docs.URL, Status: "approved", Shared: true},
	})
	_, tools, err := s.withConnectorTools(t.Context(), data, connectorRequestContext{UserID: "user"})
	if err != nil || len(tools) != 2 || code.initialize.Load() != 1 || docs.initialize.Load() != 1 {
		t.Fatalf("chat discovery: %v %d tools, counts %d %d", err, len(tools), code.initialize.Load(), docs.initialize.Load())
	}
	// The selected discovery with no selection makes no request at all.
	ctx, tools, problems, err := s.withSelectedConnectorTools(t.Context(), data, connectorRequestContext{UserID: "user"}, connectorSelection{Partial: true})
	if err != nil || len(tools) != 0 || len(problems) != 0 || code.initialize.Load() != 1 || docs.initialize.Load() != 1 {
		t.Fatalf("empty selection: %v %d tools %v", err, len(tools), problems)
	}
	if offered, _ := ctx.Value(connectorToolsKey{}).([]connectorTool); offered == nil || len(offered) != 0 {
		t.Fatalf("empty selection context = %#v", ctx.Value(connectorToolsKey{}))
	}
}
