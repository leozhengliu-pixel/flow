package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// agentReplyActivityFor is the activity logged for a finished reply (agentReplyActivity), by its message id.
func agentReplyActivityFor(t *testing.T, base, sessionID, messageID string) domain.AgentActivity {
	t.Helper()
	page := apiCall[struct {
		Items []domain.AgentActivity `json:"nodes"`
	}](t, base, http.MethodGet, "/api/agent/activities?sessionId="+sessionID, http.StatusOK)
	var found []domain.AgentActivity
	for _, activity := range page.Items {
		if activity.Metadata["messageId"] == messageID {
			found = append(found, activity)
		}
	}
	if len(found) != 1 {
		t.Fatalf("activities for reply %s = %+v (all: %+v)", messageID, found, page.Items)
	}
	return found[0]
}

func checkAgentReplyOutcome(t *testing.T, name string, reply domain.AgentMessage, activity domain.AgentActivity, status, reason, activityType string) {
	t.Helper()
	if reply.Outcome == nil || reply.Outcome.Status != status || reply.Outcome.Reason != reason {
		t.Errorf("%s: reply outcome = %+v, want %s/%s", name, reply.Outcome, status, reason)
	}
	if activity.Type != activityType || activity.Status != status || activity.Metadata["outcome"] != status {
		t.Errorf("%s: activity = %s/%s %+v, want %s/%s", name, activity.Type, activity.Status, activity.Metadata, activityType, status)
	}
	if got, _ := activity.Metadata["reason"].(string); got != reason {
		t.Errorf("%s: activity reason = %q, want %q", name, got, reason)
	}
	_, failed := agentReplyErrorText(reply)
	if failed != (status != agentOutcomeCompleted) {
		t.Errorf("%s: error row %v for a %s reply: %+v", name, failed, status, reply.Parts)
	}
}

// Every way an in-process reply ends is saved with its outcome, and the activity logged with it agrees: a saved
// failure, empty response, cut-off answer or stop is not a "completed" response.
func TestAgentReplyOutcomeInProcess(t *testing.T) {
	var mode atomic.Value
	var calls atomic.Int32
	release := make(chan struct{})
	completed := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
	text := func(delta string) string {
		return "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"" + delta + "\"}\n\n"
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		calls.Add(1)
		switch mode.Load().(string) {
		case "fail":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"model not found"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch mode.Load().(string) {
		case "ok":
			_, _ = w.Write([]byte(text("Done.") + completed))
		case "empty":
			_, _ = w.Write([]byte(completed))
		case "truncated":
			_, _ = w.Write([]byte(text("The first half") + "event: response.incomplete\ndata: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n"))
		case "work":
			// The turn after the tool ran (its result is in the request) ends without text.
			if !strings.Contains(string(body), "call_teams") {
				_, _ = w.Write([]byte("event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"item_1\",\"call_id\":\"call_teams\",\"type\":\"function_call\",\"name\":\"list_teams\",\"arguments\":\"{}\"}}\n\n" +
					"event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"item_1\",\"arguments\":\"{}\"}\n\n" + completed))
				return
			}
			_, _ = w.Write([]byte(completed))
		case "block":
			_, _ = w.Write([]byte(text("Hello ")))
			w.(http.Flusher).Flush()
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
	}))
	defer provider.Close()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: provider.URL, Model: "flow-test", MaxOutputTokens: 256, ToolsEnabled: true}, agentClient: provider.Client()}
	api := httptest.NewServer(newHandler(s))
	defer api.Close()
	defer s.stopDeliveryScheduler(context.Background())

	finished := func(message domain.AgentMessage) bool { return message.Role == "assistant" && message.Status == "" }
	for _, test := range []struct {
		mode, status, reason, activityType string
	}{
		{"ok", agentOutcomeCompleted, "", "response"},
		{"fail", agentOutcomeFailed, agentReasonProviderError, "error"},
		{"empty", agentOutcomeCompleted, agentReasonEmptyResponse, "response"},
		{"truncated", agentOutcomeFailed, agentReasonOutputLimit, "error"},
		// The work ran and the model said nothing after it: completed, not an empty response.
		{"work", agentOutcomeCompleted, agentReasonNoReplyText, "response"},
	} {
		mode.Store(test.mode)
		calls.Store(0)
		id, _ := startAgentStreamUntil(t, api.URL, "reply "+test.mode, func(event agentStreamEvent) bool { return event.Type == "session.started" })
		session := waitForAgentReply(t, api.URL, id, finished)
		reply := session.Messages[len(session.Messages)-1]
		activity := agentReplyActivityFor(t, api.URL, id, reply.ID)
		checkAgentReplyOutcome(t, test.mode, reply, activity, test.status, test.reason, test.activityType)
		switch test.mode {
		case "ok":
			if activity.Body != "Done." || reply.Content != "Done." {
				t.Errorf("ok: activity body %q, reply %q", activity.Body, reply.Content)
			}
		case "fail":
			if text, _ := agentReplyErrorText(reply); activity.Body != text || text == "" {
				t.Errorf("%s: activity body %q, error row %q", test.mode, activity.Body, text)
			}
		case "truncated":
			if reply.Content != "The first half" || activity.Metadata["partialContent"] != true || activity.Body != agentOutputLimitError {
				t.Errorf("truncated: reply %q, activity %q %+v", reply.Content, activity.Body, activity.Metadata)
			}
		case "work":
			if reply.Content != "" || !agentReplyDidWork(reply.Parts) {
				t.Errorf("work: reply %q parts %+v", reply.Content, reply.Parts)
			}
		case "empty":
			// An empty turn is not a failure and shows nothing: no text, no error row (so no Retry).
			if reply.Content != "" || len(reply.Parts) != 0 {
				t.Errorf("empty: reply %q parts %+v", reply.Content, reply.Parts)
			}
		}
	}

	// The owner stops a reply: canceled, not failed or interrupted.
	mode.Store("block")
	id, _ := startAgentStreamUntil(t, api.URL, "reply stop", func(event agentStreamEvent) bool { return event.Type == "text.delta" })
	apiCall[any](t, api.URL, http.MethodPost, "/api/agent/sessions/"+id+"/stop", http.StatusNoContent)
	session := waitForAgentReply(t, api.URL, id, finished)
	reply := session.Messages[len(session.Messages)-1]
	activity := agentReplyActivityFor(t, api.URL, id, reply.ID)
	checkAgentReplyOutcome(t, "stop", reply, activity, agentOutcomeCanceled, agentReasonUserStop, "canceled")
	if text, _ := agentReplyErrorText(reply); text != "Generation stopped" || !strings.Contains(partsText(reply.Parts), "Hello") {
		t.Errorf("stop: reply %q error row %q", reply.Content, text)
	}
	close(release)
}

// A reply canceled by something other than its owner (a chat platform's lease that ran out) is interrupted.
func TestAgentReplyCanceledWithoutStopIsInterrupted(t *testing.T) {
	run, cancel := context.WithCancelCause(context.Background())
	stopped := withAgentRunContext(run)
	cancel(errAgentReplyStopped)
	if !agentReplyStoppedByOwner(context.WithoutCancel(stopped)) {
		t.Fatal("a stop is not seen through WithoutCancel")
	}
	other, cancelOther := context.WithCancelCause(context.Background())
	lease := withAgentRunContext(other)
	cancelOther(nil)
	if agentReplyStoppedByOwner(lease) || agentReplyStoppedByOwner(context.Background()) {
		t.Fatal("a cancel without a stop reads as a stop")
	}
}

// A reply whose server went away is interrupted by the sweeper, with an activity that says so.
func TestAgentReplyOutcomeInterruptedBySweeper(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: "http://127.0.0.1:1", Model: "flow-test"}}
	handler := newHandler(s)
	defer s.stopDeliveryScheduler(context.Background())
	api := httptest.NewServer(handler)
	defer api.Close()
	viewer := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Viewer
	stamp := time.Now().UTC()
	session := domain.AgentSession{ID: "agent_session_gone", SlugID: "gone-1", UserID: viewer.ID, Title: "Gone", Location: "page", IssueIDs: []string{}, SkillIDs: []string{}, CreatedAt: stamp, UpdatedAt: stamp}
	streaming := domain.AgentMessage{ID: "agent_message_gone", Role: "assistant", Content: "Half", CreatedAt: stamp, Status: agentMessageStreaming, UpdatedAt: &stamp, Parts: []domain.AgentMessagePart{{ID: "text", Type: "text", Text: "Half", Status: "running"}}}
	session.Messages = []domain.AgentMessage{{ID: "agent_message_ask", Role: "user", Content: "hello", CreatedAt: stamp}, streaming}
	if err := repository.MutateWorkspace(store.ContextWithActor(context.Background(), viewer), "test-workspace", "agent.session_created", session.ID, nil, func(data *domain.Bootstrap) error {
		data.AgentSessions = append(data.AgentSessions, session)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if swept := s.sweepAgentLiveReplies(context.Background(), time.Now()); swept != 1 {
		t.Fatalf("swept %d, want 1", swept)
	}
	stored := apiCall[domain.AgentSession](t, api.URL, http.MethodGet, "/api/agent/sessions/"+session.ID, http.StatusOK)
	reply := stored.Messages[len(stored.Messages)-1]
	activity := agentReplyActivityFor(t, api.URL, session.ID, reply.ID)
	checkAgentReplyOutcome(t, "sweeper", reply, activity, agentOutcomeInterrupted, agentReasonReplyAbandoned, "error")
	if activity.Body != agentReplyInterrupted {
		t.Errorf("interrupted activity body = %q", activity.Body)
	}
}

// Replies saved before outcomes were recorded read by their error row; they are never rewritten.
func TestAgentReplyOutcomeOfStoredReplies(t *testing.T) {
	for _, test := range []struct {
		error  string
		status string
	}{
		{"", agentOutcomeCompleted},
		{"Generation stopped", agentOutcomeCanceled},
		{agentReplyInterrupted, agentOutcomeInterrupted},
		{"Flow Agent provider returned status 500", agentOutcomeFailed},
	} {
		message := domain.AgentMessage{ID: "m", Role: "assistant", Content: "x"}
		if test.error != "" {
			message.Parts = []domain.AgentMessagePart{{ID: "m_error", Type: "error", Text: test.error}}
		}
		if got := agentReplyOutcomeOf(message).Status; got != test.status {
			t.Errorf("%q: outcome %s, want %s", test.error, got, test.status)
		}
		if activity := agentReplyActivity("s", message, nil); activity.Status != test.status {
			t.Errorf("%q: activity status %s, want %s", test.error, activity.Status, test.status)
		}
	}
}

// The agent worker's replies end the same way: each terminal state is saved with its outcome and an activity that
// agrees (and the activity is stored, not dropped by the reply's agentSessions-only save).
func TestAgentReplyOutcomeOnWorker(t *testing.T) {
	f := newWorkerChatFixture(t)
	ctx := context.Background()
	finished := func(message domain.AgentMessage) bool { return message.Role == "assistant" && message.Status == "" }
	type scenario struct {
		name, status, reason, activityType string
		play                               func(run store.AgentRun, sessionID string)
	}
	for _, test := range []scenario{
		{"success", agentOutcomeCompleted, "", "response", func(run store.AgentRun, _ string) {
			f.post(t, run, map[string]any{"type": "text.delta", "body": "Done."})
			f.post(t, run, map[string]any{"type": "response", "activityId": "end", "body": "Done."})
			f.complete(t, run, "done")
		}},
		{"failure", agentOutcomeFailed, agentReasonProviderError, "error", func(run store.AgentRun, _ string) {
			f.post(t, run, map[string]any{"type": "error", "activityId": "end", "body": "Flow Agent provider returned status 400: model not found"})
			f.complete(t, run, "failed")
		}},
		{"empty", agentOutcomeCompleted, agentReasonEmptyResponse, "response", func(run store.AgentRun, _ string) {
			f.post(t, run, map[string]any{"type": "response", "activityId": "end", "body": ""})
			f.complete(t, run, "done")
		}},
		// The tool ran and the model wrote nothing after it: completed, not an empty response.
		{"work without text", agentOutcomeCompleted, agentReasonNoReplyText, "response", func(run store.AgentRun, _ string) {
			f.post(t, run, map[string]any{"type": "action", "activityId": "c1:call", "callId": "c1", "content": map[string]any{"action": "list_issues", "parameter": map[string]any{}}})
			f.post(t, run, map[string]any{"type": "action.result", "activityId": "c1:result", "callId": "c1", "content": map[string]any{"action": "list_issues", "result": `{"items":[]}`}})
			f.post(t, run, map[string]any{"type": "response", "activityId": "end", "body": ""})
			f.complete(t, run, "done")
		}},
		{"stopped", agentOutcomeCanceled, agentReasonUserStop, "canceled", func(run store.AgentRun, sessionID string) {
			f.post(t, run, map[string]any{"type": "text.delta", "body": "Reading"})
			if status, body := postAgentJSON(t, f.api.URL, "/api/agent/sessions/"+sessionID+"/stop", `{}`); status != http.StatusNoContent {
				t.Fatalf("stop: %d %s", status, body)
			}
			f.post(t, run, map[string]any{"type": "error", "activityId": "end", "body": "Generation stopped"})
			f.complete(t, run, "canceled")
		}},
		{"interrupted", agentOutcomeInterrupted, agentReasonWorkerLost, "error", func(run store.AgentRun, _ string) {
			f.post(t, run, map[string]any{"type": "action", "activityId": "c1:call", "callId": "c1", "content": map[string]any{"action": "save_issue", "parameter": `{"id":"DEV-1"}`}})
			if err := f.s.store.MarkAgentRunWrote(ctx, run.ID); err != nil {
				t.Fatal(err)
			}
			if _, interrupted := f.s.reconcileAgentRuns(ctx, time.Now().Add(2*time.Minute)); interrupted != 1 {
				t.Fatalf("interrupted = %d", interrupted)
			}
		}},
	} {
		events := f.stream("/api/agent/sessions/stream", `{"message":"Reply `+test.name+`","location":"page"}`)
		sessionID := nextEvent(t, events, "session.started").Session.ID
		run := f.mustClaim(t)
		test.play(run, sessionID)
		reply := f.waitReply(t, sessionID, finished)
		activity := agentReplyActivityFor(t, f.api.URL, sessionID, reply.ID)
		checkAgentReplyOutcome(t, test.name, reply, activity, test.status, test.reason, test.activityType)
		if activity.Metadata["runId"] != run.ID {
			t.Errorf("%s: activity run = %v, want %s", test.name, activity.Metadata["runId"], run.ID)
		}
		rest := drain(t, events)
		if test.name == "interrupted" {
			// Like the reference app, an interrupted reply has no error copy: the stream ends with the saved chat.
			kinds := []string{}
			for _, event := range rest {
				kinds = append(kinds, event.Type)
			}
			if slices.Contains(kinds, "error") || !slices.Contains(kinds, "session.completed") {
				t.Errorf("interrupted reply stream events = %v", kinds)
			}
		}
	}
}
