package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestAgentMessageAndSessionFeedback(t *testing.T) {
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
	requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)

	session := requestJSON[domain.AgentSession](t, handler, http.MethodPost, "/api/agent/sessions", map[string]any{"message": "Summarize this", "location": "page"}, http.StatusCreated)
	var reply, question domain.AgentMessage
	for _, message := range session.Messages {
		if message.Role == "assistant" {
			reply = message
		} else {
			question = message
		}
	}
	if reply.ID == "" || question.ID == "" {
		t.Fatalf("expected a question and a reply: %#v", session.Messages)
	}
	feedbackPath := "/api/agent/sessions/" + session.ID + "/messages/" + reply.ID + "/feedback"
	feedbackOf := func(session domain.AgentSession) *domain.AgentFeedback {
		for _, message := range session.Messages {
			if message.ID == reply.ID {
				return message.Feedback
			}
		}
		return nil
	}

	// Thumbs up rates the reply without touching the conversation's order or read state.
	updatedAt := session.UpdatedAt
	session = requestJSON[domain.AgentSession](t, handler, http.MethodPut, feedbackPath, map[string]any{"rating": "positive"}, http.StatusOK)
	if got := feedbackOf(session); got == nil || got.Rating != "positive" || !session.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("thumbs up: %#v (updatedAt %v -> %v)", got, updatedAt, session.UpdatedAt)
	}

	// "Leave feedback…" on the reply: a negative rating with a note.
	session = requestJSON[domain.AgentSession](t, handler, http.MethodPut, feedbackPath, map[string]any{"rating": "negative", "comment": "  The table was wrong  "}, http.StatusOK)
	if got := feedbackOf(session); got == nil || got.Rating != "negative" || got.Comment != "The table was wrong" {
		t.Fatalf("negative feedback: %#v", got)
	}

	// "Remove feedback" clears the rating and its note.
	session = requestJSON[domain.AgentSession](t, handler, http.MethodPut, feedbackPath, map[string]any{"rating": ""}, http.StatusOK)
	if got := feedbackOf(session); got != nil {
		t.Fatalf("removed feedback must clear the reply's rating: %#v", got)
	}

	// Persisted on the server.
	session = requestJSON[domain.AgentSession](t, handler, http.MethodPut, feedbackPath, map[string]any{"rating": "positive"}, http.StatusOK)
	session = requestJSON[domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions/"+session.ID, nil, http.StatusOK)
	if got := feedbackOf(session); got == nil || got.Rating != "positive" {
		t.Fatalf("feedback must persist: %#v", got)
	}

	// Only replies can be rated, ratings are validated, and unknown messages are not found.
	requestJSON[map[string]any](t, handler, http.MethodPut, "/api/agent/sessions/"+session.ID+"/messages/"+question.ID+"/feedback", map[string]any{"rating": "positive"}, http.StatusBadRequest)
	requestJSON[map[string]any](t, handler, http.MethodPut, feedbackPath, map[string]any{"rating": "meh"}, http.StatusBadRequest)
	requestJSON[map[string]any](t, handler, http.MethodPut, feedbackPath, map[string]any{"comment": strings.Repeat("x", maxAgentFeedbackComment+1)}, http.StatusBadRequest)
	requestJSON[map[string]any](t, handler, http.MethodPut, "/api/agent/sessions/"+session.ID+"/messages/missing/feedback", map[string]any{"rating": "positive"}, http.StatusNotFound)

	// The chat menu's "Leave feedback…" keeps notes about the whole conversation.
	session = requestJSON[domain.AgentSession](t, handler, http.MethodPost, "/api/agent/sessions/"+session.ID+"/feedback", map[string]any{"comment": "Great chat"}, http.StatusCreated)
	if len(session.Feedback) != 1 || session.Feedback[0].Comment != "Great chat" {
		t.Fatalf("session feedback: %#v", session.Feedback)
	}
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/agent/sessions/"+session.ID+"/feedback", map[string]any{"comment": "   "}, http.StatusBadRequest)
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/agent/sessions/missing/feedback", map[string]any{"comment": "Hi"}, http.StatusNotFound)
}
