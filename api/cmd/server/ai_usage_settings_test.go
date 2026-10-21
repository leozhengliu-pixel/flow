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

func TestIncludedAICreditsMicros(t *testing.T) {
	for value, want := range map[string]int64{"": 0, "  ": 0, "abc": 0, "-5": 0, "0": 0, "NaN": 0, "Inf": 0, "1e12": 0, "10": 10_000_000, " 9.99 ": 9_990_000, "0.000001": 1} {
		t.Setenv("FLOW_AI_INCLUDED_CREDITS_USD", value)
		if got := includedAICreditsMicros(); got != want {
			t.Errorf("FLOW_AI_INCLUDED_CREDITS_USD=%q: %d, want %d", value, got, want)
		}
	}
	if got := aiCreditsView(5_000_000, 7_000_000); got.RemainingMicros != 0 || got.SpentMicros != 7_000_000 {
		t.Fatalf("remaining must not go below zero: %+v", got)
	}
}

// Remaining credits are the included amount minus all metered coding and loop spend, from the recorded usage.
func TestAIUsageCreditsFollowRecordedUsage(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	workspace := repository.Bootstrap().Workspace.URLKey

	t.Setenv("FLOW_AI_INCLUDED_CREDITS_USD", "")
	credits := requestJSON[aiCredits](t, handler, http.MethodGet, "/api/workspace/ai-credits", nil, http.StatusOK)
	if credits != (aiCredits{}) {
		t.Fatalf("no credits configured, nothing spent: %+v", credits)
	}

	t.Setenv("FLOW_AI_INCLUDED_CREDITS_USD", "10")
	credits = requestJSON[aiCredits](t, handler, http.MethodGet, "/api/workspace/ai-credits", nil, http.StatusOK)
	if credits.IncludedMicros != 10_000_000 || credits.SpentMicros != 0 || credits.RemainingMicros != 10_000_000 {
		t.Fatalf("unused credits: %+v", credits)
	}

	for index, micros := range []int64{260_000, 250_000} {
		if err := repository.RecordCodingUsage(t.Context(), domain.CodingUsageRecord{ID: "usage_" + string(rune('a'+index)), WorkspaceKey: workspace, Kind: "llm", CostMicros: micros}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.RecordCodingUsage(t.Context(), domain.CodingUsageRecord{ID: "usage_other", WorkspaceKey: "another-workspace", Kind: "llm", CostMicros: 9_000_000}); err != nil {
		t.Fatal(err)
	}
	credits = requestJSON[aiCredits](t, handler, http.MethodGet, "/api/workspace/ai-credits", nil, http.StatusOK)
	if credits.SpentMicros != 510_000 || credits.RemainingMicros != 9_490_000 {
		t.Fatalf("remaining must be $9.49 after $0.51 of usage: %+v", credits)
	}

	t.Setenv("FLOW_AI_INCLUDED_CREDITS_USD", "")
	credits = requestJSON[aiCredits](t, handler, http.MethodGet, "/api/workspace/ai-credits", nil, http.StatusOK)
	if credits.IncludedMicros != 0 || credits.RemainingMicros != 0 || credits.SpentMicros != 510_000 {
		t.Fatalf("no included credits: spend is reported, the balance is $0.00: %+v", credits)
	}
}

func usageFeedbackServer(t *testing.T) (*server, http.Handler, domain.AgentSession, domain.AgentMessage) {
	t.Helper()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": "Agent reply"}}}})
	}))
	t.Cleanup(provider.Close)
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	srv := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, BaseURL: provider.URL, Model: "flow-test"}, agentClient: provider.Client()}
	handler := newHandler(srv)
	requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	session := requestJSON[domain.AgentSession](t, handler, http.MethodPost, "/api/agent/sessions", map[string]any{"message": "Summarize this", "location": "page"}, http.StatusCreated)
	for _, message := range session.Messages {
		if message.Role == "assistant" {
			return srv, handler, session, message
		}
	}
	t.Fatalf("no reply: %#v", session.Messages)
	return nil, nil, session, domain.AgentMessage{}
}

// With "Enable usage feedback" off nothing new is recorded (ratings and notes are refused); taking a rating back works.
func TestUsageFeedbackSwitchStopsRecording(t *testing.T) {
	_, handler, session, reply := usageFeedbackServer(t)
	messagePath := "/api/agent/sessions/" + session.ID + "/messages/" + reply.ID + "/feedback"
	feedbackOf := func() *domain.AgentFeedback {
		current := requestJSON[domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions/"+session.ID, nil, http.StatusOK)
		for _, message := range current.Messages {
			if message.ID == reply.ID {
				return message.Feedback
			}
		}
		return nil
	}
	setSwitch := func(on bool) {
		requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/workspace/preferences", map[string]any{"featureFlags": map[string]bool{usageFeedbackFlag: on}}, http.StatusOK)
	}

	// On by default: ratings and conversation notes are recorded.
	requestJSON[domain.AgentSession](t, handler, http.MethodPut, messagePath, map[string]any{"rating": "positive"}, http.StatusOK)
	requestJSON[domain.AgentSession](t, handler, http.MethodPost, "/api/agent/sessions/"+session.ID+"/feedback", map[string]any{"comment": "Useful chat"}, http.StatusCreated)
	if got := feedbackOf(); got == nil || got.Rating != "positive" {
		t.Fatalf("rating on: %#v", got)
	}

	setSwitch(false)
	requestJSON[map[string]any](t, handler, http.MethodPut, messagePath, map[string]any{"rating": "negative", "comment": "Wrong"}, http.StatusForbidden)
	requestJSON[map[string]any](t, handler, http.MethodPut, messagePath, map[string]any{"comment": "A note"}, http.StatusForbidden)
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/agent/sessions/"+session.ID+"/feedback", map[string]any{"comment": "Another note"}, http.StatusForbidden)
	if got := feedbackOf(); got == nil || got.Rating != "positive" || got.Comment != "" {
		t.Fatalf("a refused request changed the stored feedback: %#v", got)
	}
	// Taking a rating back records nothing, so it still works.
	requestJSON[domain.AgentSession](t, handler, http.MethodPut, messagePath, map[string]any{"rating": ""}, http.StatusOK)
	if got := feedbackOf(); got != nil {
		t.Fatalf("rating was not removed: %#v", got)
	}

	setSwitch(true)
	requestJSON[domain.AgentSession](t, handler, http.MethodPut, messagePath, map[string]any{"rating": "negative", "comment": "Wrong"}, http.StatusOK)
	if got := feedbackOf(); got == nil || got.Rating != "negative" || got.Comment != "Wrong" {
		t.Fatalf("feedback after turning the switch back on: %#v", got)
	}
}

func TestAgentFeedbackExportForAdmins(t *testing.T) {
	_, handler, session, reply := usageFeedbackServer(t)
	requestJSON[domain.AgentSession](t, handler, http.MethodPut, "/api/agent/sessions/"+session.ID+"/messages/"+reply.ID+"/feedback", map[string]any{"rating": "negative", "comment": "=SUM(A1) wrong"}, http.StatusOK)
	requestJSON[domain.AgentSession](t, handler, http.MethodPost, "/api/agent/sessions/"+session.ID+"/feedback", map[string]any{"comment": "Overall fine"}, http.StatusCreated)

	entries := requestJSON[[]agentFeedbackEntry](t, handler, http.MethodGet, "/api/workspace/agent-feedback", nil, http.StatusOK)
	if len(entries) != 2 {
		t.Fatalf("entries: %#v", entries)
	}
	var reply1, conversation *agentFeedbackEntry
	for index := range entries {
		switch entries[index].Scope {
		case "reply":
			reply1 = &entries[index]
		case "conversation":
			conversation = &entries[index]
		}
	}
	if reply1 == nil || reply1.Rating != "negative" || reply1.MessageID != reply.ID || reply1.SessionID != session.ID || conversation == nil || conversation.Comment != "Overall fine" {
		t.Fatalf("entries: %#v", entries)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/workspace/agent-feedback?format=csv", nil))
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/csv") || !strings.HasPrefix(body, "updated_at,scope,rating,comment,") {
		t.Fatalf("csv: %d %q %q", recorder.Code, recorder.Header().Get("Content-Type"), body)
	}
	if !strings.Contains(body, "'=SUM(A1) wrong") || strings.Contains(body, ",=SUM(A1)") {
		t.Fatalf("csv must neutralise formulas: %q", body)
	}
	if strings.Contains(body, "Summarize this") || strings.Contains(body, "Agent reply") {
		t.Fatalf("the export must not carry conversation text: %q", body)
	}
}

// The guidance field's "Last edit by …" footer needs who changed it and when; saving the same text is not an edit.
func TestAgentGuidanceRecordsLastEdit(t *testing.T) {
	_, handler := newLoopTestServer(t, &fakeLoopProvider{})
	read := func() domain.WorkspaceSettings {
		return requestJSON[domain.WorkspaceSettings](t, handler, http.MethodGet, "/api/workspace/preferences", nil, http.StatusOK)
	}
	if settings := read(); settings.AgentInstructionsEditedAt != nil || settings.AgentInstructionsEditedByID != "" {
		t.Fatalf("never edited: %+v", settings)
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/workspace/agent-guidance", map[string]any{"instructions": "Be brief."}, http.StatusOK)
	first := read()
	if first.AgentInstructions != "Be brief." || first.AgentInstructionsEditedAt == nil || first.AgentInstructionsEditedByID == "" {
		t.Fatalf("edit not recorded: %+v", first)
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/workspace/agent-guidance", map[string]any{"instructions": " Be brief. "}, http.StatusOK)
	if again := read(); !again.AgentInstructionsEditedAt.Equal(*first.AgentInstructionsEditedAt) {
		t.Fatal("saving unchanged guidance counted as an edit")
	}
}
