package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// responsesScript is a fake OpenAI Responses provider: each chat turn gets the next scripted SSE body (title
// requests are answered empty), and every request body is kept for inspection.
type responsesScript struct {
	mu       sync.Mutex
	bodies   []map[string]any
	turns    []func(w http.ResponseWriter, r *http.Request)
	fallback string
}

func (p *responsesScript) serve(w http.ResponseWriter, r *http.Request) {
	var payload map[string]any
	_ = json.NewDecoder(r.Body).Decode(&payload)
	w.Header().Set("Content-Type", "text/event-stream")
	if payload["instructions"] == agentTitleSystemPrompt {
		_, _ = io.WriteString(w, responsesCompleted)
		return
	}
	p.mu.Lock()
	p.bodies = append(p.bodies, payload)
	index := len(p.bodies) - 1
	var turn func(http.ResponseWriter, *http.Request)
	if index < len(p.turns) {
		turn = p.turns[index]
	}
	p.mu.Unlock()
	if turn != nil {
		turn(w, r)
		return
	}
	_, _ = io.WriteString(w, responsesText(p.fallback))
}

func (p *responsesScript) body(index int) map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index >= len(p.bodies) {
		return nil
	}
	return p.bodies[index]
}

const responsesCompleted = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"

func responsesText(text string) string {
	raw, _ := json.Marshal(text)
	return "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":" + string(raw) + "}\n\n" + responsesCompleted
}

func responsesCall(id, name string, arguments any) string {
	args, _ := json.Marshal(arguments)
	quoted, _ := json.Marshal(string(args))
	return fmt.Sprintf("event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"item_%s\",\"call_id\":%q,\"type\":\"function_call\",\"name\":%q,\"arguments\":\"\"}}\n\n", id, id, name) +
		fmt.Sprintf("event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"item_%s\",\"arguments\":%s}\n\n", id, quoted)
}

func newRuntimeTestServer(t *testing.T, provider *responsesScript) (*server, http.Handler) {
	t.Helper()
	host := httptest.NewServer(http.HandlerFunc(provider.serve))
	t.Cleanup(host.Close)
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	srv := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: host.URL, Model: "flow-test", MaxOutputTokens: 256, ToolsEnabled: true, WriteTools: true}, agentClient: host.Client()}
	return srv, newHandler(srv)
}

func streamRuntime(t *testing.T, handler http.Handler, path, body string) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s: status=%d body=%s", path, recorder.Code, recorder.Body.String())
	}
	return recorder.Body.String()
}

// responsesInputPairs checks the Responses input a provider received: every function_call is followed by its
// output before anything else, and no output lacks its call.
func responsesInputPairs(t *testing.T, payload map[string]any) {
	t.Helper()
	input, _ := payload["input"].([]any)
	pending := map[string]bool{}
	for _, raw := range input {
		item, _ := raw.(map[string]any)
		switch item["type"] {
		case "function_call":
			pending[item["call_id"].(string)] = true
		case "function_call_output":
			id := item["call_id"].(string)
			if !pending[id] {
				t.Fatalf("output without its call %s in %v", id, input)
			}
			delete(pending, id)
		default:
			if len(pending) > 0 {
				t.Fatalf("calls %v are not answered before the next item: %v", pending, input)
			}
		}
	}
	if len(pending) > 0 {
		t.Fatalf("calls %v have no output: %v", pending, input)
	}
}

// Stopping a reply while a tool call is running must not poison the chat: the saved reply gives the call a result,
// and the next message goes through (OpenAI Responses refused it with "No tool output found for function call").
func TestAgentChatContinuesAfterStopDuringToolCall(t *testing.T) {
	started := make(chan struct{})
	provider := &responsesScript{fallback: "Created the project."}
	provider.turns = []func(http.ResponseWriter, *http.Request){func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, responsesCall("fc_stop", "list_issues", map[string]any{"query": "export"}))
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done() // the stop cuts the stream while the call is still open
	}}
	_, handler := newRuntimeTestServer(t, provider)
	api := httptest.NewServer(handler)
	defer api.Close()
	done := make(chan string, 1)
	go func() {
		request, _ := http.NewRequest(http.MethodPost, api.URL+"/api/agent/sessions/stream", strings.NewReader(`{"message":"QA fix – long task","location":"page"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Workspace-Key", "test-workspace")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			done <- ""
			return
		}
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		done <- string(raw)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the tool call never started")
	}
	sessions := apiCall[[]domain.AgentSession](t, api.URL, http.MethodGet, "/api/agent/sessions", http.StatusOK)
	id := sessions[0].ID
	time.Sleep(50 * time.Millisecond)
	apiCall[any](t, api.URL, http.MethodPost, "/api/agent/sessions/"+id+"/stop", http.StatusNoContent)
	<-done
	stopped, ok := func() (domain.AgentMessage, bool) {
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			session := apiCall[domain.AgentSession](t, api.URL, http.MethodGet, "/api/agent/sessions/"+id, http.StatusOK)
			if last := session.Messages[len(session.Messages)-1]; last.Role == "assistant" {
				return last, true
			}
		}
		return domain.AgentMessage{}, false
	}()
	if !ok {
		t.Fatal("the stopped reply was not saved")
	}
	var interrupted *domain.AgentToolCall
	for _, part := range stopped.Parts {
		if part.ToolCall != nil && part.ToolCall.ID == "fc_stop" {
			interrupted = part.ToolCall
		}
	}
	if interrupted == nil || interrupted.Status != "error" || !strings.Contains(string(interrupted.Result), "Cancelled") {
		t.Fatalf("the interrupted call must be saved with a result: %+v", interrupted)
	}

	body := streamRuntime(t, handler, "/api/agent/sessions/"+id+"/messages/stream", `{"message":"QA fix – create a project instead"}`)
	if !strings.Contains(body, "event: session.completed") || strings.Contains(body, "event: error") {
		t.Fatalf("the next message failed: %s", body)
	}
	next := provider.body(1)
	responsesInputPairs(t, next)
	if raw, _ := json.Marshal(next["input"]); !strings.Contains(string(raw), "fc_stop") || !strings.Contains(string(raw), "Cancelled") {
		t.Fatalf("the interrupted call must reach the model with its cancelled result: %s", raw)
	}
}

// A long chat keeps its first messages: older replies reach the model as text only, the latest keep their tools.
func TestAgentProviderHistoryKeepsLongChatsAsTextBeyondRecentTools(t *testing.T) {
	session := domain.AgentSession{}
	for turn := 1; turn <= 25; turn++ {
		session.Messages = append(session.Messages,
			domain.AgentMessage{Role: "user", Content: fmt.Sprintf("QA agent – request %d", turn)},
			domain.AgentMessage{Role: "assistant", Content: fmt.Sprintf("Done %d", turn), Parts: []domain.AgentMessagePart{
				{Type: "toolCall", Status: "completed", ToolCall: &domain.AgentToolCall{ID: fmt.Sprintf("call_%d_a", turn), Name: "get_issue", Arguments: json.RawMessage(`{}`), Result: json.RawMessage(`{"ok":true}`), Status: "completed"}},
				{Type: "toolCall", Status: "completed", ToolCall: &domain.AgentToolCall{ID: fmt.Sprintf("call_%d_b", turn), Name: "save_issue", Arguments: json.RawMessage(`{}`), Result: json.RawMessage(`{"ok":true}`), Status: "completed"}},
			}})
	}
	messages := agentProviderHistory(session, "System", nil)
	if messages[1].Role != "user" || messages[1].Content != "QA agent – request 1" {
		t.Fatalf("the first request must stay in a 25-turn chat: %+v", messages[1])
	}
	withTools := 0
	for _, message := range messages {
		if len(message.ToolCalls) > 0 {
			withTools++
			if !strings.HasPrefix(message.Content, "Done 2") {
				t.Fatalf("only the latest replies keep tool calls, got %q", message.Content)
			}
		}
	}
	if withTools != agentHistoryToolReplies {
		t.Fatalf("replies with tools = %d, want %d", withTools, agentHistoryToolReplies)
	}
	if len(messages) != 1+50+agentHistoryToolReplies*2 {
		t.Fatalf("messages = %d", len(messages))
	}
}

// Stored chats from before the fix (calls saved without results, open questions) are repaired on the way out.
func TestAgentProviderHistoryRepairsUnansweredCalls(t *testing.T) {
	session := domain.AgentSession{Messages: []domain.AgentMessage{
		{Role: "user", Content: "Large CSV export best practices"},
		{Role: "assistant", Content: "", Parts: []domain.AgentMessagePart{
			{Type: "toolCall", Status: "error", ToolCall: &domain.AgentToolCall{ID: "fc_old", Name: "list_issues", Arguments: json.RawMessage(`{"query":"exp`), Status: "running"}},
			{Type: "toolCall", Status: "completed", ToolCall: &domain.AgentToolCall{ID: "fc_done", Name: "get_issue", Arguments: json.RawMessage(`{"id":"TST-1"}`), Result: json.RawMessage(`{"identifier":"TST-1"}`), Status: "completed"}},
			{Type: "toolCall", Status: "completed", ToolCall: &domain.AgentToolCall{ID: agentHostedToolPrefix + "ws_1", Name: webSearchToolName, Status: "completed", Result: json.RawMessage(`{"hosted":true}`)}},
			{Type: "error", Text: "Generation stopped"},
		}},
		{Role: "user", Content: "Try again"},
	}}
	messages := agentProviderHistory(session, "System", nil)
	var calls, outputs []string
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			calls = append(calls, call.ID)
			if !json.Valid(call.Arguments) {
				t.Fatalf("invalid arguments replayed: %s", call.Arguments)
			}
		}
		if message.ToolResult != nil {
			outputs = append(outputs, message.ToolResult.CallID+"="+message.ToolResult.Content)
		}
	}
	if strings.Join(calls, ",") != "fc_old,fc_done" || len(outputs) != 2 || !strings.Contains(outputs[0], "fc_old=") || !strings.Contains(outputs[0], "Cancelled") || !strings.HasPrefix(outputs[1], "fc_done=") {
		t.Fatalf("calls=%v outputs=%v", calls, outputs)
	}
	// Orphan outputs and outputs that do not follow their call are dropped.
	repaired := sanitizeAgentProviderMessages([]agentProviderMessage{
		{Role: "user", Content: "hi"},
		{Role: "tool", ToolResult: &agentProviderToolResult{CallID: "nobody", Content: "x"}},
		{Role: "assistant", ToolCalls: []domain.AgentToolCall{{ID: "a", Name: "get_issue"}, {ID: "b", Name: "get_issue"}}},
		{Role: "tool", ToolResult: &agentProviderToolResult{CallID: "b", Content: "B"}},
		{Role: "tool", ToolResult: &agentProviderToolResult{CallID: "b", Content: "again"}},
		{Role: "assistant", Content: ""},
		{Role: "user", Content: "next"},
	})
	var shape []string
	for _, message := range repaired {
		switch {
		case message.ToolResult != nil:
			shape = append(shape, "out:"+message.ToolResult.CallID)
		case len(message.ToolCalls) > 0:
			shape = append(shape, fmt.Sprintf("calls:%d", len(message.ToolCalls)))
		default:
			shape = append(shape, message.Role)
		}
	}
	if strings.Join(shape, " ") != "user calls:2 out:a out:b user" || repaired[2].ToolResult.Content != agentToolCancelledOutput || repaired[3].ToolResult.Content != "B" {
		t.Fatalf("repaired = %v", shape)
	}
}

// Every protocol gets a valid conversation from a history with an unanswered call and an orphan output.
func TestAgentProviderRequestsAreSanitizedForEveryProtocol(t *testing.T) {
	history := []agentProviderMessage{
		{Role: "system", Content: "System"},
		{Role: "user", Content: "Do it"},
		{Role: "assistant", ToolCalls: []domain.AgentToolCall{{ID: "call_open", Name: "list_issues", Arguments: json.RawMessage(`{}`)}, {ID: "call_done", Name: "get_issue", Arguments: json.RawMessage(`{"id":"TST-1"}`)}}},
		{Role: "tool", ToolResult: &agentProviderToolResult{CallID: "call_done", Content: `{"ok":true}`}},
		{Role: "tool", ToolResult: &agentProviderToolResult{CallID: "call_orphan", Content: `{"ok":true}`}},
		{Role: "user", Content: "Again"},
	}
	for _, test := range []struct {
		protocol, path, reply string
		check                 func(t *testing.T, body map[string]any)
	}{
		{"openai-responses", "/responses", `{"id":"r","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`, func(t *testing.T, body map[string]any) {
			responsesInputPairs(t, body)
		}},
		{"openai-chat-completions", "/chat/completions", `{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}]}`, func(t *testing.T, body map[string]any) {
			messages, _ := body["messages"].([]any)
			var roles []string
			for _, raw := range messages {
				message := raw.(map[string]any)
				role := message["role"].(string)
				if id, ok := message["tool_call_id"].(string); ok {
					role += ":" + id
				}
				roles = append(roles, role)
			}
			if strings.Join(roles, " ") != "system user assistant tool:call_open tool:call_done user" {
				t.Fatalf("roles = %v", roles)
			}
		}},
		{"anthropic-messages", "/messages", `{"id":"m","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}]}`, func(t *testing.T, body map[string]any) {
			messages, _ := body["messages"].([]any)
			if len(messages) != 4 {
				t.Fatalf("messages = %v", messages)
			}
			results := messages[2].(map[string]any)["content"].([]any)
			if len(results) != 2 || results[0].(map[string]any)["tool_use_id"] != "call_open" || results[1].(map[string]any)["tool_use_id"] != "call_done" || results[0].(map[string]any)["is_error"] != true {
				t.Fatalf("tool results must answer both calls in one user message: %v", results)
			}
		}},
	} {
		t.Run(test.protocol, func(t *testing.T) {
			var received map[string]any
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&received)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, test.reply)
			}))
			defer provider.Close()
			s := &server{agent: appconfig.AgentConfig{Enabled: true, Protocol: test.protocol, BaseURL: provider.URL, Model: "m", MaxOutputTokens: 64, AnthropicVersion: "2023-06-01"}, agentClient: provider.Client()}
			if _, err := s.requestAgentTurn(context.Background(), history, nil); err != nil {
				t.Fatal(err)
			}
			test.check(t, received)
		})
	}
}

func TestNormalizeAgentToolArgs(t *testing.T) {
	args := map[string]any{
		"id":              " DEV-42 QA agent – Export CSV fails for large workspaces",
		"parentId":        "DEV-42 QA agent – Export CSV fails…",
		"relatedTo":       []any{"[DEV-7 Fix login](/dev/issue/DEV-7/fix-login)", "http://127.0.0.1:5173/dev/issue/DEV-9/slug", "DEV-10"},
		"projectId":       "[Export revamp](/dev/project/export-revamp-1a2b3c/overview)",
		"duplicateOf":     `DEV-3 ("Old title")`,
		"title":           "DEV-42 follow-up: email a download link",
		"description":     "See [DEV-42](/dev/issue/DEV-42)",
		"team":            "Dev Workspace",
		"stateId":         "state_123",
		"cycleId":         "a1b2c3d4-1234-5678-9abc-def012345678",
		"milestoneId":     "q3-2026-plan",
		"__flowBaseURL":   "http://x/DEV-1 y",
		"assigneeId":      "@Dev User",
		"documentId":      "flow://document/roadmap-abc",
		"relatedIssueIds": []any{"dev-5: lowercase"},
	}
	normalizeAgentToolArgs("save_issue", args)
	want := map[string]any{
		"id": "DEV-42", "parentId": "DEV-42", "projectId": "export-revamp-1a2b3c", "duplicateOf": "DEV-3",
		"title": "DEV-42 follow-up: email a download link", "description": "See [DEV-42](/dev/issue/DEV-42)", "team": "Dev Workspace",
		"stateId": "state_123", "cycleId": "a1b2c3d4-1234-5678-9abc-def012345678", "milestoneId": "q3-2026-plan", "__flowBaseURL": "http://x/DEV-1 y",
		"assigneeId": "Dev User", "documentId": "roadmap-abc",
	}
	for key, value := range want {
		if args[key] != value {
			t.Errorf("%s = %q, want %q", key, args[key], value)
		}
	}
	if related := args["relatedTo"].([]any); related[0] != "DEV-7" || related[1] != "DEV-9" || related[2] != "DEV-10" {
		t.Errorf("relatedTo = %v", related)
	}
	if ids := args["relatedIssueIds"].([]any); ids[0] != "DEV-5" {
		t.Errorf("relatedIssueIds = %v", ids)
	}
	// Outside issue tools, an "id" keeps its text (a document title may start like an identifier).
	other := map[string]any{"id": "DEV-42 retro notes"}
	normalizeAgentToolArgs("delete_document", other)
	if other["id"] != "DEV-42 retro notes" {
		t.Errorf("document id = %q", other["id"])
	}
	if agentIssueReferenceKey("DEV-42") != "" || agentIssueReferenceKey("DEV-42 Title") != "DEV-42" {
		t.Error("agentIssueReferenceKey")
	}
	references := map[string]any{
		"team":     "https://flow.test/acme/team/eng/board",
		"project":  "[Roadmap](https://flow.test/acme/project/roadmap-1/overview)",
		"assignee": "@张伟",
	}
	normalizeAgentToolArgs("save_issue", references)
	if references["team"] != "eng" || references["project"] != "roadmap-1" || references["assignee"] != "张伟" {
		t.Fatalf("resource references = %#v", references)
	}
}

// Chips reach the model as copyable references, never as glued chip text.
func TestAgentModelUserTextSerializesChips(t *testing.T) {
	titles := func(identifier string) string {
		if identifier == "DEV-42" {
			return "QA agent – Export CSV fails"
		}
		return ""
	}
	text := agentModelUserText("@DEV-42 break this into 3 sub-issues, see @Export revamp and [DEV-7 Fix login](/dev/issue/DEV-7/fix-login)", []domain.AgentMention{{Type: "issue", ID: "issue_1", Label: "DEV-42"}, {Type: "project", ID: "project_1", Label: "Export revamp"}}, titles)
	want := `DEV-42 ("QA agent – Export CSV fails") break this into 3 sub-issues, see project "Export revamp" and DEV-7`
	if text != want {
		t.Fatalf("text = %q\nwant   %q", text, want)
	}
	session := domain.AgentSession{Messages: []domain.AgentMessage{{Role: "user", Content: "@DEV-42 hi", Mentions: []domain.AgentMention{{Type: "issue", ID: "issue_1", Label: "DEV-42"}}}}}
	if history := agentProviderHistory(session, "System", titles); history[1].Content != `DEV-42 ("QA agent – Export CSV fails") hi` {
		t.Fatalf("history user text = %q", history[1].Content)
	}
}

// The model copies chip text into id arguments ("DEV-1 Title"); the tools still find the issue and both writes
// succeed, so no sub-issue is silently skipped.
func TestAgentChatToolsAcceptChipTextIDs(t *testing.T) {
	provider := &responsesScript{fallback: "Created the sub-issue and set the priority."}
	srv, handler := newRuntimeTestServer(t, provider)
	data := srv.store.Bootstrap()
	parent := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "QA fix – Export CSV fails", "teamId": data.Teams[0].ID}, http.StatusCreated)
	chip := " " + parent.Identifier + " QA fix – Export CSV fails"
	provider.turns = []func(http.ResponseWriter, *http.Request){func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, responsesCall("call_child", "save_issue", map[string]any{"title": "QA fix – Backend streaming export job", "team": data.Teams[0].ID, "parentId": strings.TrimSpace(chip)})+
			responsesCall("call_parent", "save_issue", map[string]any{"id": chip, "priority": 1})+responsesCompleted)
	}}
	streamRuntime(t, handler, "/api/agent/sessions/stream", `{"message":"QA fix – break `+parent.Identifier+` down","location":"page"}`)
	sessions := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK)
	reply := sessions[0].Messages[len(sessions[0].Messages)-1]
	for _, part := range reply.Parts {
		if part.ToolCall != nil && part.ToolCall.Status != "completed" {
			t.Fatalf("%s failed: %s %s", part.ToolCall.Name, part.ToolCall.Error, part.ToolCall.Result)
		}
	}
	updated := requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+parent.ID, nil, http.StatusOK)
	if updated.Priority != 1 || len(updated.SubIssueIDs) != 1 {
		t.Fatalf("parent = priority %d, sub-issues %v", updated.Priority, updated.SubIssueIDs)
	}
}

// Raw provider reasoning is the model's scratchpad: it is neither streamed to the page nor saved on the reply.
func TestAgentChatDoesNotShowProviderReasoning(t *testing.T) {
	provider := &responsesScript{}
	provider.turns = []func(http.ResponseWriter, *http.Request){func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "event: response.reasoning_summary_text.delta\ndata: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"Figuring out query filters\"}\n\n"+responsesText("答案"))
	}}
	_, handler := newRuntimeTestServer(t, provider)
	body := streamRuntime(t, handler, "/api/agent/sessions/stream", `{"message":"QA fix – 你好","location":"page"}`)
	if strings.Contains(body, "Figuring out") || strings.Contains(body, "reasoning.delta") {
		t.Fatalf("reasoning streamed: %s", body)
	}
	sessions := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK)
	for _, part := range sessions[0].Messages[1].Parts {
		if part.Type == "reasoning" {
			t.Fatalf("reasoning saved: %+v", part)
		}
	}
}

// Without a search provider, chat searches with the provider's hosted web_search tool: the search shows as a step
// (never executed or replayed as a function call). A gateway that refuses the tool turns it off and the turn is
// asked again without it.
func TestAgentChatUsesHostedWebSearch(t *testing.T) {
	provider := &responsesScript{}
	provider.turns = []func(http.ResponseWriter, *http.Request){func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"ws_1\",\"type\":\"web_search_call\",\"status\":\"in_progress\"}}\n\n"+
			"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"ws_1\",\"type\":\"web_search_call\",\"status\":\"completed\",\"action\":{\"type\":\"search\",\"query\":\"node lts\"}}}\n\n"+
			responsesText("Node 24 is the LTS ([nodejs.org](https://nodejs.org/en/download))."))
	}}
	srv, handler := newRuntimeTestServer(t, provider)
	srv.agent.NativeWebSearch = true
	body := streamRuntime(t, handler, "/api/agent/sessions/stream", `{"message":"QA fix – latest Node LTS?","location":"page"}`)
	if !strings.Contains(body, "event: session.completed") {
		t.Fatalf("stream = %s", body)
	}
	tools, _ := json.Marshal(provider.body(0)["tools"])
	if !strings.Contains(string(tools), `{"type":"web_search"}`) || !strings.Contains(string(tools), `"fetch_url"`) {
		t.Fatalf("tools = %s", tools)
	}
	session := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK)[0]
	var step *domain.AgentToolCall
	for _, part := range session.Messages[1].Parts {
		if part.ToolCall != nil && part.ToolCall.Name == webSearchToolName {
			step = part.ToolCall
		}
	}
	if step == nil || step.Status != "completed" || !strings.Contains(string(step.Arguments), "node lts") {
		t.Fatalf("hosted search step = %+v", step)
	}
	if history := agentProviderHistory(session, "System", nil); len(history) != 3 || len(history[2].ToolCalls) != 0 {
		t.Fatalf("hosted search must not be replayed: %+v", history)
	}

	// A gateway without hosted search: 400 once, then the turn runs without it.
	refusals := 0
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), `"type":"web_search"`) {
			refusals++
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"Unsupported tool type: web_search"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, responsesText("ok"))
	}))
	defer fallback.Close()
	plain := &server{agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: fallback.URL, Model: "m", MaxOutputTokens: 64, ToolsEnabled: true, NativeWebSearch: true}, agentClient: fallback.Client()}
	ctx := withAgentWebTools(context.Background())
	for range 2 {
		if turn, err := plain.requestAgentTurn(ctx, []agentProviderMessage{{Role: "user", Content: "hi"}}, nil); err != nil || turn.Text != "ok" {
			t.Fatalf("turn = %+v, %v", turn, err)
		}
	}
	if refusals != 1 || plain.agentNativeWebSearch() {
		t.Fatalf("refusals = %d, native still on = %v", refusals, plain.agentNativeWebSearch())
	}
}

// A chat whose context issue was deleted (for example by the agent in that chat) keeps working: the next message
// leaves the missing issue out instead of failing with "one or more selected issues were not found".
func TestAgentChatContinuesAfterContextIssueIsDeleted(t *testing.T) {
	provider := &responsesScript{fallback: "Done."}
	srv, handler := newRuntimeTestServer(t, provider)
	data := srv.store.Bootstrap()
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "QA fix – context issue", "teamId": data.Teams[0].ID}, http.StatusCreated)
	streamRuntime(t, handler, "/api/agent/sessions/stream", `{"message":"QA fix – look at it","location":"page","issueIds":["`+issue.ID+`"]}`)
	sessions := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK)
	if len(sessions) != 1 || len(sessions[0].IssueIDs) != 1 {
		t.Fatalf("session = %+v", sessions)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/issues/"+issue.ID, nil))
	if recorder.Code >= 300 {
		t.Fatalf("delete issue: %d %s", recorder.Code, recorder.Body.String())
	}
	body := streamRuntime(t, handler, "/api/agent/sessions/"+sessions[0].ID+"/messages/stream", `{"message":"QA fix – and now?"}`)
	if strings.Contains(body, "selected issues were not found") || !strings.Contains(body, "session.completed") {
		t.Fatalf("follow-up after deleting the context issue failed: %s", body)
	}
}
