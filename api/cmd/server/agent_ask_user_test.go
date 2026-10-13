package main

import (
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

// ask_user ends the reply on a question with options (persisted on the reply), and the user's next message — a
// picked option — answers it: the choice is saved on the question and handed to the model as the tool's result.
func TestAgentChatAskUserPausesAndResumesWithTheChoice(t *testing.T) {
	var mu sync.Mutex
	var payloads []map[string]any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.Header().Set("Content-Type", "text/event-stream")
		if payload["instructions"] == agentTitleSystemPrompt {
			_, _ = fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
			return
		}
		mu.Lock()
		payloads = append(payloads, payload)
		call := len(payloads)
		mu.Unlock()
		if call == 1 {
			arguments, _ := json.Marshal(`{"question":"Which priority should the issue have?","options":[{"label":"Urgent"},{"label":"High"},{"label":"Medium"}]}`)
			_, _ = fmt.Fprint(w, "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"item_1\",\"call_id\":\"call_1\",\"type\":\"function_call\",\"name\":\"ask_user\",\"arguments\":\"\"}}\n\n"+
				"event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"item_1\",\"arguments\":"+string(arguments)+"}\n\n"+
				"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
			return
		}
		_, _ = fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Created it with High priority.\"}\n\n"+
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer provider.Close()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: provider.URL, Model: "flow-test", MaxOutputTokens: 256, ToolsEnabled: true, WriteTools: true}, agentClient: provider.Client()})
	stream := func(path, body string) string {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "event: session.completed") {
			t.Fatalf("%s: status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
		return recorder.Body.String()
	}

	body := stream("/api/agent/sessions/stream", `{"message":"Create an issue, but ask me for the priority first","location":"page"}`)
	if len(payloads) != 1 {
		t.Fatalf("the run must stop on the question; provider calls = %d", len(payloads))
	}
	if names := agentPayloadToolNames(payloads[0]); !slices.Contains(names, askUserTool) {
		t.Fatalf("ask_user is not offered in chat: %v", names)
	}
	if !strings.Contains(body, `"type":"question"`) {
		t.Fatalf("the question part was not streamed: %s", body)
	}
	sessions := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK)
	session := sessions[0]
	reply := session.Messages[len(session.Messages)-1]
	question := agentQuestionPart(reply)
	if question == nil || question.Status != "pending" || question.Text != "Which priority should the issue have?" || len(question.Question.Options) != 3 || question.Question.Options[1].Value != "High" || !question.Question.AllowFreeText {
		t.Fatalf("persisted question = %#v", question)
	}
	if !strings.HasSuffix(reply.Content, "Which priority should the issue have?") {
		t.Fatalf("the reply text must end with the question: %q", reply.Content)
	}

	// An option that the question does not offer is refused.
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/agent/sessions/"+session.ID+"/messages", map[string]any{"message": "Low", "answer": map[string]string{"partId": question.ID, "value": "Low"}}, http.StatusBadRequest)

	stream("/api/agent/sessions/"+session.ID+"/messages/stream", fmt.Sprintf(`{"message":"High","answer":{"partId":%q,"value":"High"}}`, question.ID))
	if len(payloads) != 2 {
		t.Fatalf("answering must resume the run; provider calls = %d", len(payloads))
	}
	history, _ := json.Marshal(payloads[1]["input"])
	if !strings.Contains(string(history), `\"selected\":\"High\"`) || !strings.Contains(string(history), "call_1") {
		t.Fatalf("the choice must reach the model as the ask_user result: %s", history)
	}
	session = requestJSON[domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions/"+session.ID, nil, http.StatusOK)
	answered := agentQuestionPart(session.Messages[1])
	if answered == nil || answered.Status != "completed" || answered.Question.Selected != "High" {
		t.Fatalf("the chosen option must be saved on the question: %#v", answered)
	}
	if picked := session.Messages[2]; picked.Role != "user" || picked.Content != "High" || picked.AnswerTo != question.ID {
		t.Fatalf("the answer message = %#v", picked)
	}
	if last := session.Messages[len(session.Messages)-1]; last.Role != "assistant" || last.Content != "Created it with High priority." {
		t.Fatalf("resumed reply = %#v", last)
	}
	// Once answered, the question takes no more answers.
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/agent/sessions/"+session.ID+"/messages", map[string]any{"message": "Medium", "answer": map[string]string{"partId": question.ID, "value": "Medium"}}, http.StatusBadRequest)
}

// Typing instead of picking ("Something else…") answers the question with free text.
func TestAnswerAgentQuestionWithFreeText(t *testing.T) {
	session := domain.AgentSession{Messages: []domain.AgentMessage{{Role: "user", Content: "Do it"}, {Role: "assistant", Parts: []domain.AgentMessagePart{{ID: "q1", Type: "question", Status: "pending", ToolCall: &domain.AgentToolCall{ID: "call_1", Name: askUserTool, Status: "pending"}, Question: &domain.AgentQuestion{Options: []domain.AgentQuestionOption{{Label: "Approve", Value: "Approve"}, {Label: "Reject", Value: "Reject"}}, AllowFreeText: true}}}}}}
	answerTo, err := answerAgentQuestion(&session, "Change the health to At risk first", nil)
	part := session.Messages[1].Parts[0]
	if err != nil || answerTo != "" || part.Status != "completed" || part.Question.Answer != "Change the health to At risk first" || part.Question.Selected != "" || !strings.Contains(string(part.ToolCall.Result), "At risk") {
		t.Fatalf("free text answer: answerTo=%q err=%v part=%#v", answerTo, err, part)
	}
	if _, err := answerAgentQuestion(&session, "again", &agentQuestionAnswer{PartID: "q1", Value: "Approve"}); err == nil {
		t.Fatal("an answered question must refuse another choice")
	}
	if _, _, err := parseAskUser(domain.AgentToolCall{Arguments: json.RawMessage(`{"question":"Proceed?","options":[{"label":"Approve"}]}`)}); err == nil {
		t.Fatal("a question needs at least two options")
	}
}

func agentQuestionPart(message domain.AgentMessage) *domain.AgentMessagePart {
	for index := range message.Parts {
		if message.Parts[index].Type == "question" {
			return &message.Parts[index]
		}
	}
	return nil
}

func agentPayloadToolNames(payload map[string]any) []string {
	names := []string{}
	tools, _ := payload["tools"].([]any)
	for _, tool := range tools {
		if name, ok := tool.(map[string]any)["name"].(string); ok {
			names = append(names, name)
		}
	}
	return names
}
