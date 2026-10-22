package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A connector's isError result gives the model the connector's own text, as a worker run's model reads it through
// /mcp; any other failure keeps its error.
func TestAgentToolFailureTextIsTheConnectorsOwnText(t *testing.T) {
	result := []byte(`{"content":[{"type":"text","text":"search backend unavailable"}],"isError":true}`)
	if got := agentToolFailureText(result, errConnectorReportedFailure); got != "search backend unavailable" {
		t.Fatalf("connector failure text = %q", got)
	}
	if got := agentToolFailureText([]byte(`{"content":[],"isError":true}`), errConnectorReportedFailure); got != errConnectorReportedFailure.Error() {
		t.Fatalf("a flagged result without text = %q", got)
	}
	if got := agentToolFailureText(nil, errors.New("MCP connection access was revoked")); got != "MCP connection access was revoked" {
		t.Fatalf("other failure = %q", got)
	}
}

// The in-process chat agent's model reads a failing connector's own text (the tool result stays an error).
func TestInProcessChatModelReadsConnectorFailureText(t *testing.T) {
	var providerCalls, connectorCalls atomic.Int32
	var failing atomic.Bool
	failing.Store(true)
	upstream := fakeRuntimeConnector(t, &connectorCalls, &failing)
	external := connectorToolName("code", "search_code")
	followUp := make(chan string, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := providerCalls.Add(1)
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		if call == 1 {
			_, _ = w.Write([]byte("event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"item_search\",\"call_id\":\"call_search\",\"type\":\"function_call\",\"name\":\"" + external + "\",\"arguments\":\"{}\"}}\n\n" +
				"event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"item_search\",\"arguments\":\"{\\\"query\\\":\\\"roundTotal\\\"}\"}\n\n" +
				"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
			return
		}
		select {
		case followUp <- string(body):
		default:
		}
		_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Search is down.\"}\n\n" +
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
	_, _ = io.ReadAll(response.Body)
	var body string
	select {
	case body = <-followUp:
	default:
		t.Fatalf("the model was not called after the connector call (provider calls %d, connector calls %d)", providerCalls.Load(), connectorCalls.Load())
	}
	if !strings.Contains(body, "search backend unavailable") || strings.Contains(body, "MCP tool reported a failure") {
		t.Fatalf("the model did not read the connector's own failure text: %s", body)
	}
}

// A worker loop run records a selected connector that discovery skipped as a run notice, as an in-process loop
// run does: in the payload and on the loop run record, with a notice event.
func TestWorkerLoopRunRecordsSkippedConnectorNotice(t *testing.T) {
	f := newAgentAttributionFixture(t)
	f.s.agent.WorkerSecret = testWorkerSecret
	var calls atomic.Int32
	var failing atomic.Bool
	upstream := fakeRuntimeConnector(t, &calls, &failing)
	setTestConnectors(t, f.s.store, applicationPolicy{ID: "one", Kind: "mcp", Name: "One", URL: upstream.URL, Status: "approved", Shared: true})
	workspace := f.data.Workspace.URLKey
	record := domain.LoopRun{ID: "loop_run_notice", LoopID: "loop_notice", Status: "running", StartedAt: time.Now().UTC()}
	if err := f.s.store.CreateLoopRun(t.Context(), workspace, record, store.LoopRunLease{Owner: "worker", ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	run, token := startWorkerTestRun(t, f.s, f.handler, store.AgentRunInput{WorkspaceKey: workspace, SessionID: record.ID, Kind: store.AgentRunKindLoop}, agentRunPrincipal{Mode: agentRunModeUser, UserID: f.data.Viewer.ID, Connectors: []string{"one", "gone"}})

	payload := runtimeJSON[struct {
		Notices []string `json:"notices"`
	}](t, f.handler, runtimeCall{method: http.MethodGet, path: "/internal/agent-runtime/runs/" + run.ID + "/payload", worker: "w1"}, http.StatusOK)
	want := fmt.Sprintf("%s %s", loopConnectorSkippedNotice, "gone: the connection no longer exists")
	if len(payload.Notices) != 1 || payload.Notices[0] != want {
		t.Fatalf("payload notices = %q, want %q", payload.Notices, want)
	}
	// The other selected connector still works.
	if tools := listedRunTools(t, f.handler, token); tools[connectorToolName("one", "search_code")] == nil {
		t.Fatalf("the working connector is not offered: %v", tools)
	}
	updated, err := f.s.store.LoopRun(t.Context(), workspace, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Notices) != 1 || updated.Notices[0] != want {
		t.Fatalf("loop run notices = %q", updated.Notices)
	}
	events, err := f.s.store.LoopRunEvents(t.Context(), workspace, record.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	notices := 0
	for _, event := range events {
		if event.Kind == "notice" && strings.Contains(string(event.Data), "gone: the connection no longer exists") {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("notice events = %d (%+v)", notices, events)
	}
	// Asking again records nothing twice.
	runtimeJSON[map[string]any](t, f.handler, runtimeCall{method: http.MethodGet, path: "/internal/agent-runtime/runs/" + run.ID + "/payload", worker: "w1"}, http.StatusOK)
	if again, _ := f.s.store.LoopRun(t.Context(), workspace, record.ID); len(again.Notices) != 1 {
		t.Fatalf("notices after a second payload = %q", again.Notices)
	}
}

// elicitingConnector is an MCP server whose confirm tool asks the person for a region (MCP elicitation) and
// answers with what it got; a client that does not offer elicitation gets an error result.
func elicitingConnector(t *testing.T) *httptest.Server {
	t.Helper()
	remote := mcp.NewServer(&mcp.Implementation{Name: "Deploy", Version: "1"}, nil)
	mcp.AddTool(remote, &mcp.Tool{Name: "confirm", Description: "Confirm a deployment"}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
		result, err := req.Session.Elicit(ctx, &mcp.ElicitParams{Mode: "form", Message: "Choose a region", RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{"region": map[string]any{"type": "string", "enum": []string{"eu", "us"}}}, "required": []string{"region"}}})
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "no confirmation: " + err.Error()}}}, nil, nil
		}
		region, _ := result.Content["region"].(string)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: result.Action + ":" + region}}}, nil, nil
	})
	host := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, nil))
	t.Cleanup(host.Close)
	return host
}

type runElicitations struct {
	Supported bool                      `json:"supported"`
	Version   int64                     `json:"version"`
	Items     []agentRunElicitationItem `json:"items"`
}

// A worker chat run's connector can ask its person mid-call, as in the in-process chat: the worker sees the
// question through its long poll, only the chat's own user answers it through the chat's elicitation route, and
// the answer reaches the connector (not the stored conversation).
func TestWorkerChatRunConnectorElicitation(t *testing.T) {
	f := newAgentAttributionFixture(t)
	f.s.agent.WorkerSecret = testWorkerSecret
	upstream := elicitingConnector(t)
	setTestConnectors(t, f.s.store, applicationPolicy{ID: "deploy", Kind: "mcp", Name: "Deploy", URL: upstream.URL, Status: "approved", Shared: true})
	workspace, viewer := f.data.Workspace.URLKey, f.data.Viewer.ID
	sessionID := "agent_session_elicit"
	if err := f.s.store.MutateWorkspace(t.Context(), workspace, "agent.session_created", sessionID, nil, func(data *domain.Bootstrap) error {
		data.AgentSessions = append(data.AgentSessions, domain.AgentSession{ID: sessionID, UserID: viewer})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	run, token := startWorkerTestRun(t, f.s, f.handler, store.AgentRunInput{WorkspaceKey: workspace, SessionID: sessionID, Kind: store.AgentRunKindChat}, agentRunPrincipal{Mode: agentRunModeUser, UserID: viewer})
	name := connectorToolName("deploy", "confirm")
	if tools := listedRunTools(t, f.handler, token); tools[name] == nil {
		t.Fatalf("the eliciting connector is not offered: %v", tools)
	}
	poll := func(after int64, waitMs int) runElicitations {
		return runtimeJSON[runElicitations](t, f.handler, runtimeCall{method: http.MethodGet, path: fmt.Sprintf("/internal/agent-runtime/runs/%s/elicitations?after=%d&waitMs=%d", run.ID, after, waitMs), worker: "w1"}, http.StatusOK)
	}
	if first := poll(0, 0); !first.Supported || len(first.Items) != 0 {
		t.Fatalf("elicitations before the call = %+v", first)
	}
	done := make(chan map[string]any, 1)
	go func() { done <- callRunTool(t, f.handler, token, name, map[string]any{}, nil) }()

	asked := poll(0, 5000)
	if len(asked.Items) != 1 || asked.Items[0].Status != "pending" || asked.Items[0].Message != "Choose a region" || asked.Items[0].ConnectorName != "Deploy" || asked.Items[0].SessionID != sessionID {
		t.Fatalf("the worker does not see the question: %+v", asked)
	}
	item := asked.Items[0]
	if item.AnswerPath != "/api/agent/sessions/"+sessionID+"/elicitations/"+item.ID {
		t.Fatalf("answer path = %q", item.AnswerPath)
	}
	// A session the person does not own (or a wrong session id) cannot answer it.
	requestJSON[map[string]any](t, f.handler, "POST", "/api/agent/sessions/another_session/elicitations/"+item.ID+"?workspace="+workspace, map[string]any{"action": "accept", "content": map[string]any{"region": "eu"}}, http.StatusNotFound)
	requestJSON[map[string]any](t, f.handler, "POST", item.AnswerPath+"?workspace="+workspace, map[string]any{"action": "accept", "content": map[string]any{"region": "eu"}}, http.StatusOK)

	select {
	case result := <-done:
		if result["isError"] == true || runToolText(result) != "accept:eu" {
			t.Fatalf("connector result after the answer = %v", result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the connector call did not resume after the answer")
	}
	resolved := poll(asked.Version, 0)
	if len(resolved.Items) != 1 || resolved.Items[0].Status != "completed" || resolved.Items[0].Action != "accept" {
		t.Fatalf("resolved question = %+v", resolved)
	}
	if encoded, _ := json.Marshal(resolved.Items[0]); strings.Contains(string(encoded), `"content"`) || strings.Contains(string(encoded), `"region":"eu"`) {
		t.Fatalf("the answer leaked into the question log: %s", encoded)
	}
	// The run's end drops its questions.
	runtimeJSON[map[string]any](t, f.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/complete", worker: "w1", body: map[string]any{"status": "done"}}, http.StatusOK)
	if _, ok := agentRunElicitationLogs.Load(run.ID); ok {
		t.Fatal("a finished run keeps its questions")
	}
}

// A delegated worker run has no person to ask mid-call, like the in-process thread agent: its connectors are not
// offered elicitation, so the connector proceeds without asking (here: reports it could not confirm).
func TestWorkerThreadRunConnectorGetsNoElicitation(t *testing.T) {
	f := newAgentAttributionFixture(t)
	f.s.agent.WorkerSecret = testWorkerSecret
	upstream := elicitingConnector(t)
	setTestConnectors(t, f.s.store, applicationPolicy{ID: "deploy", Kind: "mcp", Name: "Deploy", URL: upstream.URL, Status: "approved", Shared: true})
	team := f.data.Teams[0].ID
	delegated := requestJSON[domain.Issue](t, f.handler, "POST", "/api/issues", map[string]any{"title": "Ship the release", "teamId": team, "delegateId": f.app.UserID}, 201)
	principal := agentRunPrincipal{Mode: agentRunModeApp, UserID: f.data.Viewer.ID, AppUserID: f.app.UserID, TeamID: team}
	run, token := startWorkerTestRun(t, f.s, f.handler, store.AgentRunInput{WorkspaceKey: f.data.Workspace.URLKey, SessionID: delegated.AgentSessionID, Kind: store.AgentRunKindDelegation}, principal)

	result := callRunTool(t, f.handler, token, connectorToolName("deploy", "confirm"), map[string]any{}, nil)
	if result["isError"] != true || !strings.HasPrefix(runToolText(result), "no confirmation:") {
		t.Fatalf("a delegated run's eliciting connector = %v", result)
	}
	log := runtimeJSON[runElicitations](t, f.handler, runtimeCall{method: http.MethodGet, path: "/internal/agent-runtime/runs/" + run.ID + "/elicitations?waitMs=0", worker: "w1"}, http.StatusOK)
	if log.Supported || len(log.Items) != 0 {
		t.Fatalf("a delegated run's question log = %+v", log)
	}
}
