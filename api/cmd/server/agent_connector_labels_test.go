package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestAnnotateConnectorCallNamesTheConnectorAndTool(t *testing.T) {
	ctx := context.WithValue(t.Context(), connectorToolsKey{}, []connectorTool{{Definition: agentProviderTool{Name: "external_abc"}, Policy: applicationPolicy{Name: "code", DisplayName: "Code search"}, RemoteName: "search_code"}})
	call := domain.AgentToolCall{ID: "call", Name: "external_abc"}
	annotateConnectorCall(ctx, &call)
	if call.Connector != "Code search" || call.RemoteTool != "search_code" {
		t.Fatalf("connector call not named: %#v", call)
	}
	for _, other := range []domain.AgentToolCall{{Name: "save_issue"}, {Name: "external_gone"}} {
		annotateConnectorCall(ctx, &other)
		if other.Connector != "" || other.RemoteTool != "" {
			t.Fatalf("call named by mistake: %#v", other)
		}
	}
	annotateConnectorCall(ctx, nil)
}

func TestAgentPhaseTitleNamesTheConnectorNotItsToolId(t *testing.T) {
	first := domain.AgentToolCall{ID: "a", Name: "external_13e42ac3", Connector: "Code search", RemoteTool: "search_code"}
	second := domain.AgentToolCall{ID: "b", Name: "external_99aa", Connector: "Code search", RemoteTool: "read_file"}
	if got := agentPhaseTitle([]domain.AgentToolCall{first, second}); got != "Using Code search" {
		t.Fatalf("phase title = %q", got)
	}
}

// A connector tool's chat row carries the connector and tool name from its first frame on, and keeps them in the saved reply.
func TestAgentChatConnectorToolRowsCarryTheToolName(t *testing.T) {
	var providerCalls, connectorCalls, toolCount atomic.Int32
	toolCount.Store(3)
	upstream := fakeCodeMCP(t, &connectorCalls, &toolCount)
	defer upstream.Close()
	external := connectorToolName("code", "delete_branch")
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := providerCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if call == 1 {
			_, _ = w.Write([]byte("event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"item_write\",\"call_id\":\"call_write\",\"type\":\"function_call\",\"name\":\"" + external + "\",\"arguments\":\"{}\"}}\n\n" +
				"event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"item_write\",\"arguments\":\"{}\"}\n\n" +
				"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
			return
		}
		_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Done.\"}\n\n" +
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
	}))
	defer provider.Close()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
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
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, host.URL+"/api/agent/sessions/stream", strings.NewReader(`{"message":"Delete the branch","location":"page"}`))
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
	const named = `"connector":"Code search","remoteTool":"delete_branch"`
	for _, kind := range []string{"event: tool.started", "event: tool.completed"} {
		_, after, found := strings.Cut(stream, kind)
		frame, _, _ := strings.Cut(after, "\n\n")
		if !found || !strings.Contains(frame, named) {
			t.Fatalf("%s frame does not name the connector tool: %s", kind, stream)
		}
	}
	if connectorCalls.Load() != 1 {
		t.Fatalf("connector tool did not run: %s", stream)
	}
	sessions := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK)
	if len(sessions) != 1 || len(sessions[0].Messages) != 2 {
		t.Fatalf("session not saved: %#v", sessions)
	}
	saved := false
	for _, part := range sessions[0].Messages[1].Parts {
		if part.ToolCall != nil && part.ToolCall.Name == external {
			saved = part.ToolCall.Connector == "Code search" && part.ToolCall.RemoteTool == "delete_branch"
		}
	}
	if !saved {
		t.Fatalf("saved tool row lost the connector tool name: %#v", sessions[0].Messages[1].Parts)
	}
}
