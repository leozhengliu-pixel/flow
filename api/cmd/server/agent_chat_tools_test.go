package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Every Flow tool must be reachable from chat (core, a group, or deliberately hidden), so a new tool can't silently
// disappear from the chat agent.
func TestAgentChatToolsCoverTheInventory(t *testing.T) {
	var inventory []flowMCPTool
	if err := json.Unmarshal(flowMCPToolInventory, &inventory); err != nil {
		t.Fatal(err)
	}
	placed := map[string]string{}
	place := func(name, where string) {
		if previous, ok := placed[name]; ok {
			t.Errorf("%s is in both %s and %s", name, previous, where)
		}
		placed[name] = where
	}
	for _, name := range agentChatCoreTools {
		place(name, "core")
	}
	for _, group := range agentToolGroups {
		for _, name := range group.Tools {
			place(name, group.Name)
		}
	}
	for name := range agentChatHiddenTools {
		place(name, "hidden")
	}
	names := map[string]bool{agentProgressTool: true}
	for _, item := range inventory {
		name := strings.TrimPrefix(item.Name, "mcp__flow.")
		names[name] = true
		if _, ok := placed[name]; !ok {
			t.Errorf("tool %s is not offered in chat: add it to agentChatCoreTools, a group, or agentChatHiddenTools", name)
		}
	}
	for name, where := range placed {
		if !names[name] {
			t.Errorf("%s (%s) is not a Flow tool", name, where)
		}
	}
}

func chatToolNames(tools []agentProviderTool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

func TestAgentChatToolsetPicksToolsForTheRequest(t *testing.T) {
	s := &server{agent: appconfig.AgentConfig{ToolsEnabled: true, WriteTools: true}}
	available, err := s.agentToolDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	full, _ := json.Marshal(available)
	session := func(message string, mutate ...func(*domain.AgentSession)) domain.AgentSession {
		value := domain.AgentSession{Messages: []domain.AgentMessage{{Role: "user", Content: message}}}
		for _, apply := range mutate {
			apply(&value)
		}
		return value
	}

	question := newAgentChatToolset(session("用一句话介绍 DEV-16")).tools(available)
	names := chatToolNames(question)
	if slices.Contains(names, agentProgressTool) || slices.Contains(names, "save_issue") || !slices.Contains(names, "get_issue") || !slices.Contains(names, agentLoadToolsTool) {
		t.Fatalf("question tools = %v", names)
	}
	if slices.Index(names, "search_issues") != 0 || names[len(names)-1] != agentLoadToolsTool {
		t.Fatalf("core tools must come first and load_tools last for a stable cache prefix: %v", names)
	}
	compact, _ := json.Marshal(question)
	if len(compact)*3 > len(full) {
		t.Fatalf("chat tools are %d bytes, want well under a third of the full %d", len(compact), len(full))
	}

	change := chatToolNames(newAgentChatToolset(session("把 DEV-16 的优先级改成高")).tools(available))
	if !slices.Contains(change, "save_issue") || !slices.Contains(change, "save_comment") || slices.Contains(change, "save_project") {
		t.Fatalf("change tools = %v", change)
	}
	if english := chatToolNames(newAgentChatToolset(session("Assign ENG-4 to me")).tools(available)); !slices.Contains(english, "save_issue") {
		t.Fatalf("english change tools = %v", english)
	}
	if review := chatToolNames(newAgentChatToolset(session("summarize open pull requests")).tools(available)); !slices.Contains(review, "list_diffs") || slices.Contains(review, "save_issue") {
		t.Fatalf("review tools = %v", review)
	}
	// An issue panel chat can change its issue; a document panel chat can edit its document.
	if panel := chatToolNames(newAgentChatToolset(session("这个怎么样", func(value *domain.AgentSession) { value.IssueIDs = []string{"issue_1"} })).tools(available)); !slices.Contains(panel, "save_issue") {
		t.Fatalf("issue panel tools = %v", panel)
	}
	if panel := chatToolNames(newAgentChatToolset(session("tighten this", func(value *domain.AgentSession) { value.DocumentIDs = []string{"doc_1"} })).tools(available)); !slices.Contains(panel, "save_document") {
		t.Fatalf("document panel tools = %v", panel)
	}
	// Tools an earlier turn used stay available for follow-ups.
	followUp := session("ok", func(value *domain.AgentSession) {
		value.Messages = append([]domain.AgentMessage{{Role: "assistant", Parts: []domain.AgentMessagePart{{ToolCall: &domain.AgentToolCall{Name: "save_release"}}}}}, value.Messages...)
	})
	if names := chatToolNames(newAgentChatToolset(followUp).tools(available)); !slices.Contains(names, "save_release") {
		t.Fatalf("follow-up tools = %v", names)
	}

	toolset := newAgentChatToolset(session("hello"))
	if result, isError := toolset.load(json.RawMessage(`{"groups":["issue_changes"]}`), available); isError || !strings.Contains(result, "save_issue") {
		t.Fatalf("load = %s %v", result, isError)
	}
	if !slices.Contains(chatToolNames(toolset.tools(available)), "save_issue") {
		t.Fatal("loaded group missing from the next turn")
	}
	if _, isError := toolset.load(json.RawMessage(`{"groups":["nope"]}`), available); !isError {
		t.Fatal("unknown group accepted")
	}

	readOnly := &server{agent: appconfig.AgentConfig{ToolsEnabled: true}}
	readTools, _ := readOnly.agentToolDefinitions()
	if names := chatToolNames(newAgentChatToolset(session("把 DEV-16 的优先级改成高")).tools(readTools)); slices.Contains(names, "save_issue") {
		t.Fatalf("write tools offered while disabled: %v", names)
	}
}

func TestCompactAgentToolSchemaKeepsPropertiesNamedLikeKeywords(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"default":{"type":"string","maxLength":10,"description":"kept"},"tags":{"type":"array","minItems":1,"items":{"type":"string","minLength":1}}},"default":{}}`)
	var got map[string]any
	if err := json.Unmarshal(compactAgentToolSchema(raw), &got); err != nil {
		t.Fatal(err)
	}
	want := `{"properties":{"default":{"description":"kept","type":"string"},"tags":{"items":{"type":"string"},"type":"array"}},"type":"object"}`
	if encoded, _ := json.Marshal(got); string(encoded) != want {
		t.Fatalf("compacted = %s", encoded)
	}
}

func TestFindAgentMessageReferences(t *testing.T) {
	data := domain.Bootstrap{
		Teams:     []domain.Team{{Key: "DEV"}, {Key: "ENG"}},
		Projects:  []domain.Project{{ID: "project_1", SlugID: "compare-test-abc", Name: "Compare Test"}, {ID: "project_2", SlugID: "api-1", Name: "API"}},
		Documents: []domain.Document{{ID: "document_1", SlugID: "qa-doc-123", Title: "QA doc"}},
	}
	refs := findAgentMessageReferences(data, "介绍DEV-16和 eng-4，还有 [DEV-16](http://x/ws/issue/DEV-16)。GPT-5 UTF-8 XDEV-1 DEV-2a v1-2 Compare Test 的 API 文档 http://x/ws/document/qa-doc-123")
	if strings.Join(refs.IssueIdentifiers, ",") != "DEV-16,ENG-4" {
		t.Fatalf("identifiers = %v", refs.IssueIdentifiers)
	}
	if len(refs.Projects) != 1 || refs.Projects[0].ID != "project_1" {
		t.Fatalf("projects = %#v (short names like API must not match)", refs.Projects)
	}
	if len(refs.Documents) != 1 || refs.Documents[0].ID != "document_1" {
		t.Fatalf("documents = %#v", refs.Documents)
	}
	if refs := findAgentMessageReferences(data, "see /ws/project/api-1/overview"); len(refs.Projects) != 1 || refs.Projects[0].ID != "project_2" {
		t.Fatalf("project link = %#v", refs.Projects)
	}
}

// One chat reply end to end: the issue named in the request is in the first call's instructions, the request
// carries a prompt cache key and a small tool list without report_progress, and load_tools adds write tools for
// the next call.
func TestAgentChatLoadsReferencedIssuesAndToolsOnDemand(t *testing.T) {
	var mu sync.Mutex
	var payloads []map[string]any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if payload["instructions"] == agentTitleSystemPrompt {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
			return
		}
		mu.Lock()
		payloads = append(payloads, payload)
		call := len(payloads)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if call == 1 {
			_, _ = fmt.Fprint(w, "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"item_1\",\"call_id\":\"call_1\",\"type\":\"function_call\",\"name\":\"load_tools\",\"arguments\":\"\"}}\n\n"+
				"event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"item_1\",\"arguments\":\"{\\\"groups\\\":[\\\"issue_changes\\\"]}\"}\n\n"+
				"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
			return
		}
		_, _ = fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Done.\"}\n\n"+
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer provider.Close()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: provider.URL, Model: "flow-test", MaxOutputTokens: 256, ToolsEnabled: true, WriteTools: true}, agentClient: provider.Client()})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/sessions/stream", strings.NewReader(`{"message":"Tell me about tst-1","location":"page"}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || len(payloads) != 2 || !strings.Contains(recorder.Body.String(), "event: session.completed") {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, len(payloads), recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "load_tools") {
		t.Fatal("load_tools should not be streamed to the page as a tool row")
	}
	toolNames := func(payload map[string]any) []string {
		names := []string{}
		tools, _ := payload["tools"].([]any)
		for _, tool := range tools {
			names = append(names, tool.(map[string]any)["name"].(string))
		}
		return names
	}
	first, second := payloads[0], payloads[1]
	instructions, _ := first["instructions"].(string)
	if !strings.Contains(instructions, "- TST-1: ") || !strings.Contains(instructions, "load_tools") || strings.Contains(instructions, "report_progress") {
		t.Fatalf("first call instructions lack the referenced issue or the chat tool rule:\n%s", instructions)
	}
	if strings.Index(instructions, "Resource links:") > strings.Index(instructions, "Today: ") {
		t.Fatal("static instructions must come before the per-request context so they can be cached")
	}
	if key, _ := first["prompt_cache_key"].(string); !strings.HasPrefix(key, "flow-chat-") || second["prompt_cache_key"] != key {
		t.Fatalf("prompt cache keys = %v, %v", first["prompt_cache_key"], second["prompt_cache_key"])
	}
	if names := toolNames(first); slices.Contains(names, agentProgressTool) || slices.Contains(names, "save_issue") || !slices.Contains(names, agentLoadToolsTool) {
		t.Fatalf("first call tools = %v", names)
	}
	if names := toolNames(second); !slices.Contains(names, "save_issue") {
		t.Fatalf("second call tools after load_tools = %v", names)
	}
	sessions := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK)
	for _, part := range sessions[0].Messages[1].Parts {
		if part.ToolCall != nil && part.ToolCall.Name == agentLoadToolsTool {
			t.Fatalf("load_tools saved as a visible tool row: %#v", sessions[0].Messages[1].Parts)
		}
	}
}

// A gateway that rejects prompt_cache_key keeps working: the field is dropped and the request repeated.
func TestAgentPromptCacheKeyFallsBackWhenRejected(t *testing.T) {
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if _, ok := payload["prompt_cache_key"]; ok {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unrecognized request argument supplied: prompt_cache_key"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n")
	}))
	defer provider.Close()
	s := &server{agent: appconfig.AgentConfig{Protocol: "openai-responses", BaseURL: provider.URL, Model: "model", MaxOutputTokens: 100}, agentClient: provider.Client()}
	ctx := withAgentPromptCacheKeyForTest("flow-chat-test")
	for attempt := 0; attempt < 2; attempt++ {
		turn, err := s.requestAgentTurn(ctx, []agentProviderMessage{{Role: "user", Content: "hi"}}, nil)
		if err != nil || turn.Text != "ok" {
			t.Fatalf("turn=%#v err=%v", turn, err)
		}
	}
	if calls != 3 {
		t.Fatalf("provider calls = %d, want the rejected call, its retry, and one call without the key", calls)
	}
}

func withAgentPromptCacheKeyForTest(key string) context.Context {
	return context.WithValue(context.Background(), agentPromptCacheKey{}, key)
}
