package main

import (
	"bufio"
	"context"
	"encoding/json"
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
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// startAgentStreamUntil starts a new chat and reads its stream until stop returns true for an event, then drops the
// connection (the page reloads). It returns the chat id and the event that stopped it.
func startAgentStreamUntil(t *testing.T, base, message string, stop func(agentStreamEvent) bool) (string, agentStreamEvent) {
	t.Helper()
	request, _ := http.NewRequest(http.MethodPost, base+"/api/agent/sessions/stream", strings.NewReader(`{"message":"`+message+`","location":"page"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Workspace-Key", "test-workspace")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)
	id := ""
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue
		}
		var event agentStreamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "session.started" && event.Session != nil {
			id = event.Session.ID
		}
		if id != "" && stop(event) {
			return id, event
		}
	}
	t.Fatalf("stream ended before the awaited event (chat %q): %v", id, scanner.Err())
	return "", agentStreamEvent{}
}

// waitForAgentReply re-reads the chat (as a reloaded page does) until its last message satisfies done.
func waitForAgentReply(t *testing.T, base, id string, done func(domain.AgentMessage) bool) domain.AgentSession {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var session domain.AgentSession
	for time.Now().Before(deadline) {
		session = apiCall[domain.AgentSession](t, base, http.MethodGet, "/api/agent/sessions/"+id, http.StatusOK)
		if count := len(session.Messages); count > 0 && done(session.Messages[count-1]) {
			return session
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("chat %s never reached the expected state: %+v", id, session.Messages)
	return session
}

func postAgentJSON(t *testing.T, base, path, body string) (int, string) {
	t.Helper()
	request, _ := http.NewRequest(http.MethodPost, base+path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Workspace-Key", "test-workspace")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(raw)
}

// A reloaded page (or a second tab) reads the running reply from the stored chat: the partial text first, then the
// finished reply replaces it in place.
func TestAgentLiveReplyShowsPartialTextAfterReload(t *testing.T) {
	release := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello \"}\n\n"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
			_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"world\"}\n\n" +
				"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
		case <-r.Context().Done():
		}
	}))
	defer provider.Close()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	api := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: provider.URL, Model: "flow-test", MaxOutputTokens: 256}, agentClient: provider.Client()}))
	defer api.Close()

	id, _ := startAgentStreamUntil(t, api.URL, "hi", func(event agentStreamEvent) bool { return event.Type == "text.delta" })
	session := waitForAgentReply(t, api.URL, id, func(message domain.AgentMessage) bool {
		return message.Role == "assistant" && message.Status == agentMessageStreaming && strings.Contains(partsText(message.Parts), "Hello")
	})
	live := session.Messages[len(session.Messages)-1]
	if live.UpdatedAt == nil || live.Content != "Hello" {
		t.Fatalf("streaming reply = %+v", live)
	}
	close(release)
	session = waitForAgentReply(t, api.URL, id, func(message domain.AgentMessage) bool { return message.Role == "assistant" && message.Status == "" })
	if len(session.Messages) != 2 || session.Messages[1].ID != live.ID || session.Messages[1].Content != "Hello world" || session.Messages[1].UpdatedAt != nil {
		t.Fatalf("finished chat = %+v", session.Messages)
	}
}

// An approval the reply waits on is saved with it, so a reloaded page or another tab of the owner shows the card and
// answers it; the run continues and the finished reply replaces the streaming one.
func TestAgentLiveReplyApprovalAnsweredAfterReload(t *testing.T) {
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
		_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Write approved.\"}\n\n" +
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
	}))
	defer provider.Close()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	setTestConnectors(t, repository, applicationPolicy{ID: "code", Kind: "mcp", Name: "Code search", URL: upstream.URL, Status: "approved", Shared: true, ToolApproval: connectorToolApprovalAlways})
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: provider.URL, Model: "flow-test", MaxOutputTokens: 256, ToolsEnabled: true, WriteTools: true}, agentClient: provider.Client()}
	handler := newHandler(s)
	defer s.stopDeliveryScheduler(context.Background())
	api := httptest.NewServer(handler)
	defer api.Close()

	id, event := startAgentStreamUntil(t, api.URL, "Delete the branch", func(event agentStreamEvent) bool { return event.Type == "tool.approval_required" })
	approvalID := event.ApprovalID
	waitForAgentReply(t, api.URL, id, func(message domain.AgentMessage) bool {
		if message.Status != agentMessageStreaming {
			return false
		}
		for _, part := range message.Parts {
			if part.ToolCall != nil && part.ToolCall.ApprovalID == approvalID && part.Status == "pending" && part.ToolCall.Status == "pending" {
				return true
			}
		}
		return false
	})
	if connectorCalls.Load() != 0 {
		t.Fatal("tool ran before approval")
	}
	if status, body := postAgentJSON(t, api.URL, "/api/agent/sessions/"+id+"/approvals/"+approvalID, `{"decision":"approve"}`); status != http.StatusOK {
		t.Fatalf("approval from another tab: %d %s", status, body)
	}
	session := waitForAgentReply(t, api.URL, id, func(message domain.AgentMessage) bool { return message.Role == "assistant" && message.Status == "" })
	reply := session.Messages[len(session.Messages)-1]
	if len(session.Messages) != 2 || connectorCalls.Load() != 1 || !strings.Contains(reply.Content, "Write approved.") {
		t.Fatalf("finished chat = %+v (connector calls %d)", session.Messages, connectorCalls.Load())
	}
	for _, part := range reply.Parts {
		if part.ToolCall != nil && part.ToolCall.Status != "completed" {
			t.Fatalf("tool call after approval = %+v", part.ToolCall)
		}
	}
	// The answered approval is gone: answering again (a stale tab) is a clean 404.
	if status, _ := postAgentJSON(t, api.URL, "/api/agent/sessions/"+id+"/approvals/"+approvalID, `{"decision":"approve"}`); status != http.StatusNotFound {
		t.Fatalf("second answer status = %d", status)
	}
}

// A reply saved as streaming by a server that went away (a restart) is turned into an interrupted one: nothing stays
// "pending" forever, the waiting approval is no longer actionable and an error row offers Retry.
func TestAgentLiveReplyInterruptedByRestart(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: "http://127.0.0.1:1", Model: "flow-test"}}
	handler := newHandler(s)
	defer s.stopDeliveryScheduler(context.Background())
	viewer := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Viewer
	stamp := time.Now().UTC()
	session := domain.AgentSession{ID: "agent_session_restart", SlugID: "restart-1", UserID: viewer.ID, Title: "Restart", Location: "page", IssueIDs: []string{}, SkillIDs: []string{}, CreatedAt: stamp, UpdatedAt: stamp}
	streaming := domain.AgentMessage{ID: "agent_message_restart", Role: "assistant", Content: "Half", CreatedAt: stamp, Status: agentMessageStreaming, UpdatedAt: &stamp, Parts: []domain.AgentMessagePart{
		{ID: "text", Type: "text", Text: "Half", Status: "running"},
		{ID: "tool", Type: "toolCall", Status: "pending", ToolCall: &domain.AgentToolCall{ID: "call_1", Name: "external_code__delete_branch", Status: "pending", ApprovalID: "agent_approval_gone"}},
		{ID: "elicit_gone", Type: "elicitation", Status: "pending", Elicitation: &domain.AgentElicitation{ID: "elicit_gone", SessionID: session.ID, Mode: "form"}},
	}}
	session.Messages = []domain.AgentMessage{{ID: "agent_message_question", Role: "user", Content: "hello", CreatedAt: stamp}, streaming}
	ctx := store.ContextWithActor(context.Background(), viewer)
	if err := repository.MutateWorkspace(ctx, "test-workspace", "agent.session_created", session.ID, nil, func(data *domain.Bootstrap) error {
		data.AgentSessions = append(data.AgentSessions, session)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Without a run in this process the saved request cannot be answered.
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/sessions/"+session.ID+"/approvals/agent_approval_gone", strings.NewReader(`{"decision":"approve"}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("answer without a run = %d %s", recorder.Code, recorder.Body.String())
	}
	if swept := s.sweepAgentLiveReplies(context.Background(), time.Now()); swept != 1 {
		t.Fatalf("swept %d replies, want 1", swept)
	}
	stored := requestJSON[domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions/"+session.ID, nil, http.StatusOK)
	reply := stored.Messages[len(stored.Messages)-1]
	if reply.ID != streaming.ID || reply.Status != "" || reply.UpdatedAt != nil {
		t.Fatalf("interrupted reply = %+v", reply)
	}
	last := reply.Parts[len(reply.Parts)-1]
	if last.Type != "error" || last.Text != agentReplyInterrupted {
		t.Fatalf("interrupted reply has no error row: %+v", reply.Parts)
	}
	for _, part := range reply.Parts {
		if part.Status == "pending" || part.Status == "running" || part.ToolCall != nil && (part.ToolCall.ApprovalID != "" || part.ToolCall.Status != "error" || len(part.ToolCall.Result) == 0) {
			t.Fatalf("part still waiting after the restart: %+v %+v", part, part.ToolCall)
		}
	}
	if swept := s.sweepAgentLiveReplies(context.Background(), time.Now()); swept != 0 {
		t.Fatalf("second sweep changed %d replies", swept)
	}
}

// With several servers a reply's heartbeat tells a live one (owned by a peer) from an abandoned one.
func TestAgentLiveReplyAbandonedRules(t *testing.T) {
	s := &server{}
	now := time.Now()
	fresh, stale := now.Add(-time.Minute), now.Add(-agentLiveStaleAfter-time.Second)
	if !s.agentLiveReplyAbandoned(domain.AgentMessage{ID: "a", Status: agentMessageStreaming, UpdatedAt: &fresh}, now) {
		t.Fatal("a single server owns every live reply: one it does not run is abandoned")
	}
	if s.agentLiveReplyAbandoned(domain.AgentMessage{ID: "b", UpdatedAt: &stale}, now) {
		t.Fatal("a finished reply is never abandoned")
	}
	s.agentLiveReplies.Store("c", &agentLiveReply{})
	if s.agentLiveReplyAbandoned(domain.AgentMessage{ID: "c", Status: agentMessageStreaming, UpdatedAt: &stale}, now) {
		t.Fatal("a reply this process writes is not abandoned")
	}
}

// A peer forwards answers over coordination; the process running the reply applies them with the same checks as a
// local answer.
func TestAgentForwardedAnswerResolvesLocalRun(t *testing.T) {
	s := &server{}
	approval := &agentApproval{WorkspaceKey: "ws", SessionID: "chat", UserID: "user", Decision: make(chan string, 1)}
	s.registerAgentApproval("approval_1", approval)
	forward := func(answer agentForwardedAnswer) {
		payload, _ := json.Marshal(answer)
		s.receiveForwardedAgentAnswer("ws", domain.RealtimeEvent{Type: agentAnswerEventType, Payload: payload})
	}
	forward(agentForwardedAnswer{Kind: "approval", ID: "approval_1", SessionID: "chat", UserID: "someone-else", Decision: "approved"})
	select {
	case decision := <-approval.Decision:
		t.Fatalf("another user's answer resolved the approval: %s", decision)
	default:
	}
	forward(agentForwardedAnswer{Kind: "approval", ID: "approval_1", SessionID: "chat", UserID: "user", Decision: "approved"})
	select {
	case decision := <-approval.Decision:
		if decision != "approved" {
			t.Fatalf("decision = %s", decision)
		}
	default:
		t.Fatal("forwarded approval was not applied")
	}

	elicitation := &agentElicitation{Workspace: "ws", SessionID: "chat", UserID: "user", Mode: "form", Schema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`), Decision: make(chan mcp.ElicitResult, 1)}
	s.agentElicitations = map[string]*agentElicitation{"elicit_1": elicitation}
	forward(agentForwardedAnswer{Kind: "elicitation", ID: "elicit_1", SessionID: "chat", UserID: "user", Elicitation: &mcp.ElicitResult{Action: "accept", Content: map[string]any{}}})
	if len(elicitation.Decision) != 0 {
		t.Fatal("a forwarded answer that fails the form was applied")
	}
	forward(agentForwardedAnswer{Kind: "elicitation", ID: "elicit_1", SessionID: "chat", UserID: "user", Elicitation: &mcp.ElicitResult{Action: "accept", Content: map[string]any{"name": "Ada"}}})
	if result := <-elicitation.Decision; result.Action != "accept" || result.Content["name"] != "Ada" {
		t.Fatalf("forwarded elicitation = %+v", result)
	}
}

// Stopping a reply that waits on a connector question ends the question at once (the connector's call gets a
// cancel answer), instead of leaving the reply running until the question times out.
func TestStoppingAReplyCancelsItsWaitingConnectorQuestion(t *testing.T) {
	s := &server{authDisabled: true}
	run, stop := context.WithCancel(context.Background())
	defer stop()
	parts := make(chan domain.AgentMessagePart, 2)
	done := make(chan *mcp.ElicitResult, 1)
	go func() {
		// The SDK's handler context belongs to the connection and outlives the reply's run.
		ctx, cancel := agentElicitationContext(context.Background(), run)
		defer cancel()
		result, err := s.requestAgentElicitation(ctx, "ws", "chat", "user", applicationPolicy{Name: "Local tools", URL: "http://127.0.0.1:9/mcp"}, &mcp.ElicitParams{Mode: "form", Message: "Which branch?", RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{"branch": map[string]any{"type": "string"}}}}, func(part domain.AgentMessagePart, _ string) error {
			parts <- part
			return nil
		})
		if err != nil {
			t.Error(err)
		}
		done <- result
	}()
	select {
	case part := <-parts:
		if part.Status != "pending" {
			t.Fatalf("question status = %q, want pending", part.Status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the question was not asked")
	}
	stop()
	select {
	case result := <-done:
		if result == nil || result.Action != "cancel" {
			t.Fatalf("stopped question answered %+v, want cancel", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stopping the reply left its question waiting")
	}
	if final := <-parts; final.Status != "error" || final.Elicitation.Action != "cancel" {
		t.Fatalf("stopped question saved as %q / %q, want error / cancel", final.Status, final.Elicitation.Action)
	}
	s.agentApprovalsMu.Lock()
	defer s.agentApprovalsMu.Unlock()
	if len(s.agentElicitations) != 0 {
		t.Fatal("the stopped question can still be answered")
	}
}
