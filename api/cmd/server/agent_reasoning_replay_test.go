package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"flow/api/internal/domain"
)

// thinkingChatProvider fakes a Chat Completions thinking model (DeepSeek): an assistant tool-call message after the
// latest user message must carry reasoning_content unless the provider issued its call ids itself, or the request
// fails with 400, as DeepSeek answers.
type thinkingChatProvider struct {
	mu       sync.Mutex
	bodies   []map[string]any
	statuses []int
	issued   map[string]bool
	turns    []string
}

func (p *thinkingChatProvider) serve(w http.ResponseWriter, r *http.Request) {
	var payload map[string]any
	_ = json.NewDecoder(r.Body).Decode(&payload)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bodies = append(p.bodies, payload)
	messages, _ := payload["messages"].([]any)
	lastUser := -1
	for index, raw := range messages {
		if message, _ := raw.(map[string]any); message["role"] == "user" {
			lastUser = index
		}
	}
	for _, raw := range messages[lastUser+1:] {
		message, _ := raw.(map[string]any)
		calls, _ := message["tool_calls"].([]any)
		if message["role"] != "assistant" || len(calls) == 0 {
			continue
		}
		if _, ok := message["reasoning_content"]; ok {
			continue
		}
		for _, call := range calls {
			if id, _ := call.(map[string]any)["id"].(string); !p.issued[id] {
				p.statuses = append(p.statuses, http.StatusBadRequest)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"message":"The `+"`reasoning_content`"+` in the thinking mode must be passed back to the API.","type":"invalid_request_error"}}`)
				return
			}
		}
	}
	p.statuses = append(p.statuses, http.StatusOK)
	turn := chatText("Done.")
	if served := len(p.statuses) - 1 - countStatus(p.statuses, http.StatusBadRequest); served < len(p.turns) {
		turn = p.turns[served]
	}
	for _, id := range chatCallIDs(turn) {
		p.issued[id] = true
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, turn)
}

func (p *thinkingChatProvider) body(index int) map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.bodies[index]
}

func (p *thinkingChatProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.bodies)
}

func countStatus(statuses []int, status int) int {
	count := 0
	for _, value := range statuses {
		if value == status {
			count++
		}
	}
	return count
}

func chatText(text string) string {
	raw, _ := json.Marshal(text)
	return `data: {"choices":[{"delta":{"content":` + string(raw) + `},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"
}

// chatCalls streams reasoning, then the calls ([id, name, arguments]).
func chatCalls(reasoning string, calls ...[3]string) string {
	deltas := []string{}
	for index, call := range calls {
		arguments, _ := json.Marshal(call[2])
		deltas = append(deltas, fmt.Sprintf(`{"index":%d,"id":%q,"function":{"name":%q,"arguments":%s}}`, index, call[0], call[1], arguments))
	}
	raw, _ := json.Marshal(reasoning)
	return `data: {"choices":[{"delta":{"reasoning_content":` + string(raw) + `}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"tool_calls":[` + strings.Join(deltas, ",") + `]},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n"
}

func chatCallIDs(stream string) []string {
	ids := []string{}
	for _, part := range strings.Split(stream, `"id":"`)[1:] {
		ids = append(ids, part[:strings.Index(part, `"`)])
	}
	return ids
}

func newThinkingChatServer(t *testing.T, provider *thinkingChatProvider) (*server, http.Handler) {
	t.Helper()
	provider.issued = map[string]bool{}
	host := httptest.NewServer(http.HandlerFunc(provider.serve))
	t.Cleanup(host.Close)
	srv, handler := newRuntimeTestServer(t, &responsesScript{})
	srv.agent.Protocol, srv.agent.BaseURL = "openai-chat-completions", host.URL
	srv.agentClient = host.Client()
	return srv, handler
}

func chatAssistantToolTurns(payload map[string]any) []map[string]any {
	turns := []map[string]any{}
	messages, _ := payload["messages"].([]any)
	for _, raw := range messages {
		message, _ := raw.(map[string]any)
		if calls, _ := message["tool_calls"].([]any); message["role"] == "assistant" && len(calls) > 0 {
			turns = append(turns, message)
		}
	}
	return turns
}

// A bulk delete on a thinking model runs straight through (no approval): every tool turn of the run goes back with
// its reasoning, and the deletes remove exactly the sub-issues the model looked up, never a look-alike.
func TestAgentChatBulkDeleteReplaysReasoning(t *testing.T) {
	provider := &thinkingChatProvider{}
	srv, handler := newThinkingChatServer(t, provider)
	data := srv.store.Bootstrap()
	team := data.Teams[0].ID
	parent := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "QA del – CSV export fails", "teamId": team}, http.StatusCreated)
	lookalike := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "QA del – other CSV export fails", "teamId": team}, http.StatusCreated)
	first := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "QA del – streaming job", "teamId": team, "parentId": parent.ID}, http.StatusCreated)
	second := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "QA del – progress UI", "teamId": team, "parentId": parent.ID}, http.StatusCreated)
	provider.turns = []string{
		chatCalls("Look up the parent first.", [3]string{"call_get", "get_issue", `{"id":"` + parent.Identifier + `"}`}),
		chatCalls("Delete both sub-issues.", [3]string{"call_d1", "delete_issue", `{"id":"` + first.Identifier + `"}`}, [3]string{"call_d2", "delete_issue", `{"id":"` + second.Identifier + `"}`}),
		chatText("Deleted both sub-issues."),
	}
	streamRuntime(t, handler, "/api/agent/sessions/stream", `{"message":"Delete all sub-issues of `+parent.Identifier+`.","location":"page"}`)
	if provider.count() != 3 {
		t.Fatalf("a bulk delete must not stop for approval; provider calls = %d", provider.count())
	}
	turns := chatAssistantToolTurns(provider.body(2))
	if len(turns) != 2 || turns[0]["reasoning_content"] != "Look up the parent first." || turns[1]["reasoning_content"] != "Delete both sub-issues." {
		t.Fatalf("the run's tool turns must go back with their reasoning: %v", turns)
	}
	session := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK)[0]
	reply := session.Messages[len(session.Messages)-1]
	if reply.Content != "Deleted both sub-issues." || agentQuestionPart(reply) != nil {
		t.Fatalf("the reply must finish without a question: %q %+v", reply.Content, reply.Parts)
	}
	for _, issue := range []domain.Issue{first, second} {
		if status := handlerStatus(handler, http.MethodGet, "/api/issue-records/"+issue.ID); status == http.StatusOK {
			t.Fatalf("%s was not deleted", issue.Identifier)
		}
	}
	for _, issue := range []domain.Issue{parent, lookalike} {
		if status := handlerStatus(handler, http.MethodGet, "/api/issue-records/"+issue.ID); status != http.StatusOK {
			t.Fatalf("%s must not be deleted (status %d)", issue.Identifier, status)
		}
	}
}

// A model asked to create sub-issues that instead "updates" or deletes issues it guessed (the next identifiers
// after the parent, never shown to it) is refused: nothing is written, no approval is offered, and the model is
// told to create without id or look the issue up first.
func TestAgentChatRefusesWritesToUnseenIssues(t *testing.T) {
	provider := &thinkingChatProvider{}
	srv, handler := newThinkingChatServer(t, provider)
	data := srv.store.Bootstrap()
	team := data.Teams[0].ID
	parent := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "QA del – CSV export fails", "teamId": team}, http.StatusCreated)
	others := []domain.Issue{}
	for _, title := range []string{"QA del – other Export button misaligned", "QA del – other CSV export fails", "QA del – other CSV export times out"} {
		others = append(others, requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": title, "teamId": team}, http.StatusCreated))
	}
	provider.turns = []string{
		chatCalls("Create them as the next issues.",
			[3]string{"call_s1", "save_issue", `{"id":"` + others[0].Identifier + `","title":"QA del – streaming job","parentId":"` + parent.Identifier + `"}`},
			[3]string{"call_d1", "delete_issue", `{"id":"` + others[1].Identifier + `"}`},
			[3]string{"call_d2", "delete_issue", `{"id":"` + others[2].ID + `"}`}),
		chatText("I could not do that."),
	}
	streamRuntime(t, handler, "/api/agent/sessions/stream", `{"message":"Break `+parent.Identifier+` down into sub-issues.","location":"page"}`)
	if provider.count() != 2 {
		t.Fatalf("refused calls must go back to the model, not stop on an approval; provider calls = %d", provider.count())
	}
	results := 0
	messages, _ := provider.body(1)["messages"].([]any)
	for _, raw := range messages {
		if message, _ := raw.(map[string]any); message["role"] == "tool" && strings.Contains(fmt.Sprint(message["content"]), "has not appeared in this conversation") {
			results++
		}
	}
	if results != 3 {
		t.Fatalf("each guessed write must be refused with an explanation: %d %v", results, messages[len(messages)-4:])
	}
	for index, issue := range others {
		current := requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+issue.ID, nil, http.StatusOK)
		if current.Title != issue.Title || current.ParentID != nil && *current.ParentID != "" {
			t.Fatalf("unrelated issue %d was changed: %+v", index, current)
		}
	}
	session := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK)[0]
	if question := agentQuestionPart(session.Messages[1]); question != nil {
		t.Fatalf("no approval may be offered for refused calls: %+v", question)
	}
}

func TestAgentUngroundedIssueTarget(t *testing.T) {
	history := []agentProviderMessage{
		{Role: "system", Content: "Selected issues:\n- DEV-23: Export"},
		{Role: "user", Content: "Delete the sub-issues of DEV-23"},
		{Role: "assistant", Content: "Earlier I created DEV-40."},
	}
	historyEnd := len(history)
	messages := append(history,
		agentProviderMessage{Role: "assistant", Content: "I'll delete DEV-231 and DEV-232.", ToolCalls: []domain.AgentToolCall{{ID: "c1", Name: "get_issue"}}},
		agentProviderMessage{Role: "tool", ToolResult: &agentProviderToolResult{CallID: "c1", Content: `{"subIssues":[{"id":"issue_77","identifier":"DEV-77"}]}`}},
		agentProviderMessage{Role: "tool", ToolResult: &agentProviderToolResult{CallID: "c0", Content: `issue "DEV-99" not found`, IsError: true}},
	)
	for _, test := range []struct {
		call domain.AgentToolCall
		want string
	}{
		{agentTestCall("a", "delete_issue", map[string]any{"id": "DEV-77"}), ""},
		{agentTestCall("b", "delete_issue", map[string]any{"id": "issue_77"}), ""},
		{agentTestCall("c", "mcp__flow.delete_issue", map[string]any{"id": "dev-23"}), ""},
		{agentTestCall("d", "delete_issue", map[string]any{"id": "DEV-40"}), ""},
		{agentTestCall("e", "delete_issue", map[string]any{"id": "DEV-231"}), "DEV-231"},
		{agentTestCall("f", "delete_issue", map[string]any{"id": "DEV-2"}), "DEV-2"},
		{agentTestCall("g", "delete_issue", map[string]any{"id": "DEV-99"}), "DEV-99"},
		{agentTestCall("h", "save_issue", map[string]any{"id": "DEV-232 QA del – x", "title": "x"}), "DEV-232"},
		{agentTestCall("i", "save_issue", map[string]any{"title": "new", "parentId": "DEV-23"}), ""},
		{agentTestCall("j", "get_issue", map[string]any{"id": "DEV-500"}), ""},
	} {
		if got := agentUngroundedIssueTarget(test.call, messages, historyEnd); got != test.want {
			t.Errorf("%s %s: got %q, want %q", test.call.Name, test.call.Arguments, got, test.want)
		}
	}
}

// Chat Completions replays an assistant turn's reasoning; Responses and Anthropic Messages never carry it.
func TestAgentReasoningReplayPerProtocol(t *testing.T) {
	messages := []agentProviderMessage{
		{Role: "system", Content: "System"},
		{Role: "user", Content: "Hi"},
		{Role: "assistant", Content: "Looking", Reasoning: "secret plan", ToolCalls: []domain.AgentToolCall{{ID: "c1", Name: "get_issue", Arguments: json.RawMessage(`{"id":"DEV-1"}`)}}},
		{Role: "tool", ToolResult: &agentProviderToolResult{CallID: "c1", Content: "{}"}},
		{Role: "assistant", ToolCalls: []domain.AgentToolCall{{ID: "approved_c2", Name: "delete_issue", Arguments: json.RawMessage(`{"id":"DEV-1"}`)}}},
		{Role: "tool", ToolResult: &agentProviderToolResult{CallID: "approved_c2", Content: "{}"}},
		{Role: "assistant", Content: "Done."},
	}
	reasoning := func(encoded []any) []any {
		values := []any{}
		for _, raw := range encoded {
			if message, _ := raw.(map[string]any); message["role"] == "assistant" {
				value, ok := message["reasoning_content"]
				if !ok {
					value = nil
				}
				values = append(values, value)
			}
		}
		return values
	}
	if got := fmt.Sprint(reasoning(chatMessagesReplaying(messages, false))); got != "[secret plan <nil> <nil>]" {
		t.Fatalf("without replay only kept reasoning is sent: %s", got)
	}
	if got := fmt.Sprint(reasoning(chatMessagesReplaying(sanitizeAgentProviderMessages(messages), true))); got != "[secret plan  <nil>]" {
		t.Fatalf("with replay every tool turn carries reasoning_content: %s", got)
	}
	_, responses := responsesInput(messages)
	_, anthropic := anthropicMessages(messages)
	for name, encoded := range map[string]any{"responses": responses, "anthropic": anthropic} {
		if raw, _ := json.Marshal(encoded); strings.Contains(string(raw), "secret plan") || strings.Contains(string(raw), "reasoning_content") {
			t.Fatalf("%s must not carry chat reasoning: %s", name, raw)
		}
	}
}
