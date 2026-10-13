package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Run tokens spend their own budgets on /mcp — one per run and one per workspace — instead of the unauthenticated
// per-address bucket every run of a sidecar shared; personal API keys keep their own budget.
func TestAgentRunTokensHaveRunAndWorkspaceRateLimits(t *testing.T) {
	config := defaultAPIRateLimitConfig()
	config.Budgets[apiRateUnauthenticated] = apiRateBudget{Requests: 1, Complexity: 1000}
	config.Budgets[apiRateAgentRun] = apiRateBudget{Requests: 3}
	config.Budgets[apiRateAgentWorkspace] = apiRateBudget{Requests: 5}
	f := newPlatformFixture(t, newAPIRateLimiter(config, nil))
	f.service.agent.Enabled, f.service.agent.ToolsEnabled, f.service.agent.WorkerSecret = true, true, testWorkerSecret
	handler := f.host.Config.Handler
	principal := agentRunPrincipal{Mode: agentRunModeUser, UserID: f.data.Viewer.ID}
	workspace := f.data.Workspace.URLKey
	_, first := startWorkerTestRun(t, f.service, handler, store.AgentRunInput{WorkspaceKey: workspace, SessionID: "rate_one", Kind: store.AgentRunKindChat}, principal)
	_, second := startWorkerTestRun(t, f.service, handler, store.AgentRunInput{WorkspaceKey: workspace, SessionID: "rate_two", Kind: store.AgentRunKindChat}, principal)

	list := func(token string) (int, http.Header) {
		t.Helper()
		status, _, header := runtimeDo(t, handler, runtimeCall{method: http.MethodPost, path: "/mcp", bearer: token, body: map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "list_teams", "arguments": map[string]any{}}}})
		return status, header
	}
	// Far more calls than the unauthenticated budget (1): run tokens no longer spend it.
	for call := 1; call <= 3; call++ {
		status, header := list(first)
		if status != http.StatusOK || header.Get("X-RateLimit-Requests-Limit") != "3" || header.Get("X-RateLimit-Workspace-Requests-Limit") != "5" {
			t.Fatalf("run call %d = %d %v", call, status, header)
		}
	}
	status, header := list(first)
	if status != http.StatusTooManyRequests || header.Get("Retry-After") == "" || header.Get("X-RateLimit-Requests-Remaining") != "0" {
		t.Fatalf("run over its budget = %d %v", status, header)
	}
	// The other run has its own bucket but shares the workspace's: 3 workspace requests are spent already.
	for call := 1; call <= 2; call++ {
		if status, header := list(second); status != http.StatusOK {
			t.Fatalf("second run call %d = %d %v", call, status, header)
		}
	}
	status, header = list(second)
	if status != http.StatusTooManyRequests || header.Get("X-RateLimit-Workspace-Requests-Remaining") != "0" {
		t.Fatalf("workspace over its budget = %d %v", status, header)
	}

	// Personal API keys keep the apiKey category and budget on /mcp.
	key := f.apiKey(t, []string{"read"}, nil)
	response, raw := bearer(t, http.MethodPost, f.host.URL+"/mcp", key, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "list_teams", "arguments": map[string]any{}}}, http.StatusOK)
	if response.Header.Get("X-RateLimit-Requests-Limit") != "2500" || response.Header.Get("X-RateLimit-Workspace-Requests-Limit") != "" {
		t.Fatalf("API key headers = %v %s", response.Header, raw)
	}

	// A forged run token spends the address's unauthenticated budget (1 request), then gets 429.
	if status, _ := list(agentRunTokenPrefix + "forged"); status != http.StatusUnauthorized {
		t.Fatalf("forged token = %d", status)
	}
	if status, _ := list(agentRunTokenPrefix + "forged"); status != http.StatusTooManyRequests {
		t.Fatalf("forged token over the address budget = %d", status)
	}
}

func TestAgentWorkspaceRateOverrides(t *testing.T) {
	t.Setenv("FLOW_API_RATE_LIMIT_AGENT_RUN_REQUESTS", "50")
	t.Setenv("FLOW_API_RATE_LIMIT_AGENT_WORKSPACE_REQUESTS", "900")
	t.Setenv("FLOW_API_RATE_LIMIT_AGENT_WORKSPACE_OVERRIDES", "acme=100000, trial=20,broken,=5,neg=-1")
	config := apiRateLimitConfigFromEnv()
	if config.Budgets[apiRateAgentRun].Requests != 50 || config.Budgets[apiRateAgentWorkspace].Requests != 900 {
		t.Fatalf("budgets = %+v", config.Budgets)
	}
	if len(config.AgentWorkspaceRequests) != 2 || config.AgentWorkspaceRequests["acme"] != 100000 || config.AgentWorkspaceRequests["trial"] != 20 {
		t.Fatalf("overrides = %v", config.AgentWorkspaceRequests)
	}
	defaults := defaultAPIRateLimitConfig()
	if defaults.Budgets[apiRateAgentRun].Requests != 1200 || defaults.Budgets[apiRateAgentWorkspace].Requests != 36000 {
		t.Fatalf("defaults = %+v", defaults.Budgets)
	}
}

// Schema failures of a run token's tool call come back as a tool error the model can read and correct.
func TestAgentRunTokenInvalidArgumentsAreToolErrors(t *testing.T) {
	f := newAgentAttributionFixture(t)
	f.s.agent.WorkerSecret = testWorkerSecret
	_, token := startWorkerTestRun(t, f.s, f.handler, store.AgentRunInput{WorkspaceKey: f.data.Workspace.URLKey, SessionID: "agent_session_args", Kind: store.AgentRunKindChat}, agentRunPrincipal{Mode: agentRunModeUser, UserID: f.data.Viewer.ID})
	_, response := mcpWithToken(t, f.handler, token, "tools/call", map[string]any{"name": "list_issues", "arguments": map[string]any{"fields": []string{"identifier"}}}, nil)
	message := mcpToolErrorText(t, response)
	if !strings.HasPrefix(message, "Invalid arguments for list_issues: ") || !strings.Contains(message, "fields") {
		t.Fatalf("tool error = %q", message)
	}
	// A request that is not valid JSON-RPC stays a protocol error.
	_, response = mcpWithToken(t, f.handler, token, "tools/call", "not an object", nil)
	if failure, _ := response["error"].(map[string]any); failure == nil || failure["code"] != float64(-32602) {
		t.Fatalf("malformed params = %v", response)
	}
}

// The cancel long-poll answers as soon as a held run should stop (a requested cancel or a stopped session),
// re-checks the database for stops made elsewhere, and skips runs the worker already stops.
func TestAgentRuntimeCancelLongPoll(t *testing.T) {
	f := newAgentAttributionFixture(t)
	f.s.agent.WorkerSecret = testWorkerSecret
	issue := requestJSON[domain.Issue](t, f.handler, "POST", "/api/issues", map[string]any{"title": "Stopped quickly", "teamId": f.data.Teams[0].ID, "delegateId": f.app.UserID}, 201)
	task, err := f.s.store.AgentTask(t.Context(), f.data.Workspace.URLKey, issue.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	principal := agentRunPrincipal{Mode: agentRunModeApp, UserID: f.data.Viewer.ID, AppUserID: f.app.UserID, TeamID: f.data.Teams[0].ID}
	thread, token := startWorkerTestRun(t, f.s, f.handler, store.AgentRunInput{WorkspaceKey: f.data.Workspace.URLKey, SessionID: task.ID, Kind: store.AgentRunKindDelegation}, principal)
	chat, _ := startWorkerTestRun(t, f.s, f.handler, store.AgentRunInput{WorkspaceKey: f.data.Workspace.URLKey, SessionID: "chat_session", Kind: store.AgentRunKindChat}, agentRunPrincipal{Mode: agentRunModeUser, UserID: f.data.Viewer.ID})
	type answer struct {
		Cancel []string `json:"cancel"`
	}
	poll := func(body map[string]any) (answer, time.Duration) {
		t.Helper()
		started := time.Now()
		return runtimeJSON[answer](t, f.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/cancels", worker: "w1", body: body}, 200), time.Since(started)
	}
	if got, _ := poll(map[string]any{"waitMs": 0}); got.Cancel == nil || len(got.Cancel) != 0 {
		t.Fatalf("nothing to stop = %+v", got)
	}

	// A requested cancel wakes the poll at once.
	go func() {
		time.Sleep(100 * time.Millisecond)
		_, _ = f.s.store.CancelAgentRun(context.Background(), chat.ID)
	}()
	got, took := poll(map[string]any{"waitMs": 5000})
	if len(got.Cancel) != 1 || got.Cancel[0] != chat.ID || took > 700*time.Millisecond {
		t.Fatalf("cancel = %+v after %s", got, took)
	}
	t.Logf("cancel reached the poll %s after it was requested", took-100*time.Millisecond)

	// A person's stop signal in the thread wakes it too; the run being stopped already is ignored.
	go func() {
		time.Sleep(100 * time.Millisecond)
		current, _ := f.s.store.AgentTask(context.Background(), f.data.Workspace.URLKey, task.ID)
		_, _ = f.s.store.AppendAgentActivity(context.Background(), current.WorkspaceKey, current.ID, current.Version, domain.AgentActivity{ActorID: f.data.Viewer.ID, Type: "prompt", Signal: "stop"})
	}()
	got, took = poll(map[string]any{"waitMs": 5000, "ignore": []string{chat.ID}})
	if len(got.Cancel) != 1 || got.Cancel[0] != thread.ID || took > 700*time.Millisecond {
		t.Fatalf("session stop = %+v after %s", got, took)
	}
	t.Logf("session stop reached the poll %s after the stop signal", took-100*time.Millisecond)

	// Nothing new: the poll waits out its time.
	got, took = poll(map[string]any{"waitMs": 300, "ignore": []string{chat.ID, thread.ID}})
	if len(got.Cancel) != 0 || took < 250*time.Millisecond {
		t.Fatalf("idle poll = %+v after %s", got, took)
	}
	// Only the worker (secret) may poll; a run token may not.
	if status, _, _ := runtimeDo(t, f.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/cancels", bearer: token, body: map[string]any{}}); status != http.StatusForbidden {
		t.Fatalf("run token poll = %d", status)
	}
	// Another worker holds neither run.
	other := runtimeJSON[answer](t, f.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/cancels", worker: "w2", body: map[string]any{}}, 200)
	if len(other.Cancel) != 0 {
		t.Fatalf("other worker = %+v", other)
	}
}

// Tool calls in flight are bounded (each holds a private workspace copy); nested calls do not wait again and a
// caller that gives up stops waiting.
func TestToolCallLimiterBoundsCallsInFlight(t *testing.T) {
	limiter := newToolCallLimiter(3)
	var inFlight, peak atomic.Int32
	var group sync.WaitGroup
	for range 20 {
		group.Go(func() {
			ctx, release, err := limiter.acquire(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			defer release()
			now := inFlight.Add(1)
			for {
				old := peak.Load()
				if now <= old || peak.CompareAndSwap(old, now) {
					break
				}
			}
			_, nestedRelease, err := limiter.acquire(ctx) // a tool calling a tool
			if err != nil {
				t.Error(err)
			}
			nestedRelease()
			time.Sleep(5 * time.Millisecond)
			inFlight.Add(-1)
		})
	}
	group.Wait()
	if peak.Load() != 3 {
		t.Fatalf("peak calls in flight = %d, want 3", peak.Load())
	}
	held := []func(){}
	for range 3 {
		_, release, _ := limiter.acquire(context.Background())
		held = append(held, release)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := limiter.acquire(ctx); err == nil {
		t.Fatal("a full limiter admitted a call")
	}
	for _, release := range held {
		release()
	}
	if _, release, err := newToolCallLimiter(0).acquire(context.Background()); err != nil {
		t.Fatal(err)
	} else {
		release()
	}
	raw, _ := json.Marshal(toolCallConcurrencyFromEnv())
	if string(raw) == "0" {
		t.Fatal("default tool call concurrency is unbounded")
	}
}
