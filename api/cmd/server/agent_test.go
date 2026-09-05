package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestAgentChatUsesSelectedIssueContext(t *testing.T) {
	var providerMessages []agentChatMessage
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Fatalf("unexpected provider request: %s %s", r.Method, r.URL.Path)
		}
		var payload struct {
			Model    string             `json:"model"`
			Messages []agentChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		providerMessages = payload.Messages
		writeJSON(w, http.StatusOK, map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": "The selected issue is in progress."}}}})
	}))
	defer provider.Close()

	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, BaseURL: provider.URL, APIKey: "test-secret", Model: "flow-test"}, agentClient: provider.Client()})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Issues) == 0 {
		t.Fatal("seed must include an issue")
	}
	issue := bootstrap.Issues[0]
	reply := requestJSON[map[string]string](t, handler, http.MethodPost, "/api/agent/chat", map[string]any{"message": "Summarize this", "issueIds": []string{issue.ID}}, http.StatusOK)
	if reply["message"] != "The selected issue is in progress." || reply["model"] != "flow-test" {
		t.Fatalf("unexpected reply: %#v", reply)
	}
	if len(providerMessages) != 2 || !strings.Contains(providerMessages[0].Content, issue.Identifier) || !strings.Contains(providerMessages[0].Content, issue.Title) || providerMessages[1].Content != "Summarize this" {
		t.Fatalf("selected issue context missing: %#v", providerMessages)
	}
}

func TestAgentChatRejectsUnconfiguredProvider(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	requestJSON[any](t, handler, http.MethodPost, "/api/agent/chat", map[string]any{"message": "Hello", "issueIds": []string{"issue"}}, http.StatusServiceUnavailable)
}

func TestAgentSessionAndSkillLifecycle(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": "Agent reply"}}}})
	}))
	defer provider.Close()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, BaseURL: provider.URL, Model: "flow-test"}, agentClient: provider.Client()})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	skill := requestJSON[domain.PersonalAgentSkill](t, handler, http.MethodPost, "/api/agent/skills", map[string]string{"name": "Release notes", "instructions": "Summarize shipped work."}, http.StatusCreated)
	if skill.Name != "Release notes" || skill.UserID != bootstrap.Viewer.ID {
		t.Fatalf("skill not persisted: %#v", skill)
	}
	session := requestJSON[domain.AgentSession](t, handler, http.MethodPost, "/api/agent/sessions", map[string]any{"message": "Summarize this", "issueIds": []string{bootstrap.Issues[0].ID}, "skillIds": []string{skill.ID}, "location": "page"}, http.StatusCreated)
	if session.SlugID == "" || len(session.Messages) != 2 || session.Messages[1].Content != "Agent reply" || len(session.SkillIDs) != 1 {
		t.Fatalf("session not completed: %#v", session)
	}
	session = requestJSON[domain.AgentSession](t, handler, http.MethodPost, "/api/agent/sessions/"+session.ID+"/messages", map[string]string{"message": "Continue"}, http.StatusOK)
	if len(session.Messages) != 4 {
		t.Fatalf("follow-up did not persist: %#v", session.Messages)
	}
	session = requestJSON[domain.AgentSession](t, handler, http.MethodPatch, "/api/agent/sessions/"+session.ID, map[string]any{"favorite": true, "location": "toolbar"}, http.StatusOK)
	if !session.Favorite || session.Location != "toolbar" {
		t.Fatalf("session update failed: %#v", session)
	}
	sessions := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK)
	if len(sessions) != 1 || sessions[0].ID != session.ID {
		t.Fatalf("session history failed: %#v", sessions)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/agent/sessions/"+session.ID, nil, http.StatusNoContent)
	requestJSON[any](t, handler, http.MethodDelete, "/api/agent/skills/"+skill.ID, nil, http.StatusNoContent)
	if remaining := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK); len(remaining) != 0 {
		t.Fatalf("session delete failed: %#v", remaining)
	}
}

func TestAgentSessionStreamsResponsesAndExecutesReadTools(t *testing.T) {
	providerCalls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls++
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if providerCalls == 1 {
			if tools, ok := payload["tools"].([]any); !ok || len(tools) == 0 {
				t.Fatalf("Flow tools missing from request: %#v", payload)
			}
			_, _ = w.Write([]byte("event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"item_1\",\"call_id\":\"call_1\",\"type\":\"function_call\",\"name\":\"list_issues\",\"arguments\":\"\"}}\n\n" +
				"event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"item_1\",\"arguments\":\"{}\"}\n\n" +
				"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
			return
		}
		raw, _ := json.Marshal(payload["input"])
		if !strings.Contains(string(raw), "function_call_output") || !strings.Contains(string(raw), "TST-1") {
			t.Fatalf("tool result missing from continuation: %s", raw)
		}
		_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Found the issue.\"}\n\n" +
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
	}))
	defer provider.Close()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: provider.URL, Model: "flow-test", MaxOutputTokens: 256, ToolsEnabled: true}, agentClient: provider.Client()})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/sessions/stream", strings.NewReader(`{"message":"Find issues","location":"page"}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || providerCalls != 2 {
		t.Fatalf("stream status=%d providerCalls=%d body=%s", recorder.Code, providerCalls, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, expected := range []string{"event: session.started", "event: tool.started", "event: tool.completed", "event: text.delta", "event: session.completed", "Found the issue."} {
		if !strings.Contains(body, expected) {
			t.Fatalf("stream missing %q: %s", expected, body)
		}
	}
	sessions := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK)
	if len(sessions) != 1 || len(sessions[0].Messages) != 2 || sessions[0].Messages[1].Content != "Found the issue." || len(sessions[0].Messages[1].Parts) < 2 || sessions[0].Messages[1].Parts[0].ToolCall == nil || sessions[0].Messages[1].Parts[0].ToolCall.Status != "completed" {
		t.Fatalf("streamed session not persisted: %#v", sessions)
	}
}

func TestAgentSessionWriteToolsRequireApproval(t *testing.T) {
	providerCalls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls++
		w.Header().Set("Content-Type", "text/event-stream")
		if providerCalls == 1 {
			_, _ = w.Write([]byte("event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"item_write\",\"call_id\":\"call_write\",\"type\":\"function_call\",\"name\":\"save_issue\",\"arguments\":\"{}\"}}\n\n" +
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
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: provider.URL, Model: "flow-test", MaxOutputTokens: 256, ToolsEnabled: true, WriteTools: true}, agentClient: provider.Client()})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/sessions/stream", strings.NewReader(`{"message":"Update the issue","location":"page"}`))
	request.Header.Set("Content-Type", "application/json")
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(recorder, request)
		close(done)
	}()
	var approvalID string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		body := recorder.Body.String()
		marker := `"approvalId":"agent_approval_`
		if index := strings.Index(body, marker); index >= 0 {
			start := index + len(`"approvalId":"`)
			if end := strings.IndexByte(body[start:], '"'); end >= 0 {
				approvalID = body[start : start+end]
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if approvalID == "" {
		t.Fatalf("approval event not emitted: %s", recorder.Body.String())
	}
	resolve := httptest.NewRecorder()
	approvalRequest := httptest.NewRequest(http.MethodPost, "/api/agent/sessions/agent_session_pending/approvals/"+approvalID, strings.NewReader(`{"decision":"approve"}`))
	approvalRequest.Header.Set("Content-Type", "application/json")
	// The session id is supplied by the session.started event. Resolve against
	// the actual persisted session rather than relying on an invented id.
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	sessions := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK)
	if len(sessions) != 1 || sessions[0].UserID != bootstrap.Viewer.ID {
		t.Fatalf("stream did not create a session: %#v", sessions)
	}
	approvalRequest = httptest.NewRequest(http.MethodPost, "/api/agent/sessions/"+sessions[0].ID+"/approvals/"+approvalID, strings.NewReader(`{"decision":"approve"}`))
	approvalRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(resolve, approvalRequest)
	if resolve.Code != http.StatusOK {
		t.Fatalf("approval response status=%d body=%s", resolve.Code, resolve.Body.String())
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not resume after approval")
	}
	if providerCalls != 2 || !strings.Contains(recorder.Body.String(), "tool.approval_required") || !strings.Contains(recorder.Body.String(), "tool.approval_resolved") || !strings.Contains(recorder.Body.String(), "Write approved.") {
		t.Fatalf("approval stream incomplete calls=%d body=%s", providerCalls, recorder.Body.String())
	}
}
