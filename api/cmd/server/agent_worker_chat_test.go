package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// These tests play the agent worker against Flow's internal runtime API (a fake worker client: claim, payload,
// token, activities, state, complete), with FLOW_AGENT_RUNTIME=worker, and check the chat reply the person sees.

type workerChatFixture struct {
	agentAttributionFixture
	api *httptest.Server
}

func newWorkerChatFixture(t *testing.T) workerChatFixture {
	t.Helper()
	f := newAgentAttributionFixture(t)
	f.s.agent.WorkerSecret = testWorkerSecret
	f.s.agent.Runtime = appconfig.AgentRuntimeWorker
	if f.data.Workspace.URLKey != "test-workspace" {
		t.Fatalf("workspace key = %q", f.data.Workspace.URLKey)
	}
	api := httptest.NewServer(f.handler)
	// A stream still following a reply ends with its connection.
	t.Cleanup(func() {
		api.CloseClientConnections()
		api.Close()
	})
	return workerChatFixture{agentAttributionFixture: f, api: api}
}

// stream sends a chat message on the stream route and delivers its events until the stream ends (closed channel).
func (f workerChatFixture) stream(path, body string) <-chan agentStreamEvent {
	events := make(chan agentStreamEvent, 256)
	go func() {
		defer close(events)
		request, _ := http.NewRequest(http.MethodPost, f.api.URL+path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Workspace-Key", "test-workspace")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return
		}
		defer response.Body.Close()
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)
		for scanner.Scan() {
			if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
				var event agentStreamEvent
				if json.Unmarshal([]byte(data), &event) == nil {
					events <- event
				}
			}
		}
	}()
	return events
}

func nextEvent(t *testing.T, events <-chan agentStreamEvent, kind string) agentStreamEvent {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("the stream ended before %s", kind)
			}
			if event.Type == kind {
				return event
			}
		case <-timeout:
			t.Fatalf("no %s event", kind)
		}
	}
}

// drain collects the rest of a stream.
func drain(t *testing.T, events <-chan agentStreamEvent) []agentStreamEvent {
	t.Helper()
	var all []agentStreamEvent
	timeout := time.After(10 * time.Second)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return all
			}
			all = append(all, event)
		case <-timeout:
			t.Fatal("the stream did not end")
		}
	}
}

func (f workerChatFixture) claim(t *testing.T, waitMs int) (store.AgentRun, bool) {
	t.Helper()
	claimed := runtimeJSON[struct {
		Runs []store.AgentRun `json:"runs"`
	}](t, f.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/claim", worker: "w1", body: map[string]any{"free": map[string]int{"chat": 1}, "waitMs": waitMs}}, http.StatusOK)
	if len(claimed.Runs) == 0 {
		return store.AgentRun{}, false
	}
	return claimed.Runs[0], true
}

func (f workerChatFixture) mustClaim(t *testing.T) store.AgentRun {
	t.Helper()
	run, ok := f.claim(t, 5000)
	if !ok {
		t.Fatal("no chat run was queued")
	}
	return run
}

func (f workerChatFixture) post(t *testing.T, run store.AgentRun, activity map[string]any) map[string]any {
	t.Helper()
	return runtimeJSON[map[string]any](t, f.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/activities", worker: "w1", body: activity}, http.StatusOK)
}

func (f workerChatFixture) complete(t *testing.T, run store.AgentRun, status string) {
	t.Helper()
	runtimeJSON[map[string]any](t, f.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/complete", worker: "w1", body: map[string]any{"status": status}}, http.StatusOK)
}

func (f workerChatFixture) token(t *testing.T, run store.AgentRun) string {
	t.Helper()
	return runtimeJSON[struct {
		Token string `json:"token"`
	}](t, f.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/token", worker: "w1"}, http.StatusCreated).Token
}

type workerChatPayload struct {
	Chat    workerChatRunInfo     `json:"chat"`
	Context agentRunPromptContext `json:"context"`
}

func (f workerChatFixture) payload(t *testing.T, run store.AgentRun) workerChatPayload {
	t.Helper()
	return runtimeJSON[workerChatPayload](t, f.handler, runtimeCall{method: http.MethodGet, path: "/internal/agent-runtime/runs/" + run.ID + "/payload", worker: "w1"}, http.StatusOK)
}

func (f workerChatFixture) reply(t *testing.T, sessionID string) domain.AgentMessage {
	t.Helper()
	session := apiCall[domain.AgentSession](t, f.api.URL, http.MethodGet, "/api/agent/sessions/"+sessionID, http.StatusOK)
	if len(session.Messages) == 0 {
		t.Fatal("the chat has no messages")
	}
	return session.Messages[len(session.Messages)-1]
}

func (f workerChatFixture) waitReply(t *testing.T, sessionID string, done func(domain.AgentMessage) bool) domain.AgentMessage {
	t.Helper()
	return lastAgentMessage(waitForAgentReply(t, f.api.URL, sessionID, done))
}

func lastAgentMessage(session domain.AgentSession) domain.AgentMessage {
	return session.Messages[len(session.Messages)-1]
}

func partTypes(parts []domain.AgentMessagePart) []string {
	types := make([]string, 0, len(parts))
	for _, part := range parts {
		types = append(types, part.Type)
	}
	return types
}

func toolPart(parts []domain.AgentMessagePart, callID string) *domain.AgentMessagePart {
	for index := range parts {
		if parts[index].ToolCall != nil && parts[index].ToolCall.ID == callID {
			return &parts[index]
		}
	}
	return nil
}

// A chat message queues a worker run acting as its user; the worker reads the prompt the in-process reply would
// send, calls a Flow read tool with its run token and posts the reply's activities: the reply is saved while it is
// written (a reload shows it streaming), streamed to the request that asked, and saved in its final form.
func TestWorkerChatReplyIsWrittenFromTheWorkersActivities(t *testing.T) {
	f := newWorkerChatFixture(t)
	events := f.stream("/api/agent/sessions/stream", `{"message":"How many issues are in the backlog?","location":"page"}`)
	started := nextEvent(t, events, "session.started")
	sessionID, messageID := started.Session.ID, started.MessageID
	if !strings.HasPrefix(messageID, workerChatMessagePrefix) || slices.ContainsFunc(started.Session.Messages, func(message domain.AgentMessage) bool { return message.ID == messageID }) {
		t.Fatalf("session.started = %s %+v", messageID, started.Session.Messages)
	}
	if reply := f.reply(t, sessionID); reply.ID != messageID || reply.Status != agentMessageStreaming {
		t.Fatalf("the queued reply is not saved as streaming: %+v", reply)
	}

	run := f.mustClaim(t)
	stored, _ := f.s.store.AgentRun(t.Context(), run.ID)
	principal, _ := decodeAgentRunPrincipal(stored)
	if run.SessionID != sessionID || principal.Mode != agentRunModeUser || principal.UserID != f.data.Viewer.ID {
		t.Fatalf("run = %+v principal %+v", run, principal)
	}
	payload := f.payload(t, run)
	last := payload.Context.Messages[len(payload.Context.Messages)-1]
	if payload.Chat.MessageID != messageID || payload.Chat.MaxTurns != f.s.agentMaxToolTurns() || payload.Chat.MaxCalls != f.s.agentMaxToolCalls() || !strings.Contains(payload.Context.System, "Flow") || last.Role != "user" || last.Content != "How many issues are in the backlog?" {
		t.Fatalf("payload = %+v", payload)
	}
	token := f.token(t, run)
	if result := callRunTool(t, f.handler, token, "list_issues", map[string]any{"limit": 5}, nil); result["isError"] == true {
		t.Fatalf("list_issues with the run token: %v", result)
	}

	f.post(t, run, map[string]any{"type": "thought", "body": "Working…"})
	f.post(t, run, map[string]any{"type": "text.delta", "body": "Let me check the backlog. "})
	f.post(t, run, map[string]any{"type": "action", "activityId": "c1:call", "callId": "c1", "content": map[string]any{"action": "list_issues", "parameter": map[string]any{"limit": 5}}})
	// Posting the same activity again is a no-op.
	f.post(t, run, map[string]any{"type": "action", "activityId": "c1:call", "callId": "c1", "content": map[string]any{"action": "list_issues", "parameter": map[string]any{"limit": 5}}})
	live := f.reply(t, sessionID)
	if live.Status != agentMessageStreaming || !slices.Equal(partTypes(live.Parts), []string{"reasoning", "toolCall"}) || live.Parts[1].ToolCall.Status != "running" || live.Parts[0].Text != "Let me check the backlog." {
		t.Fatalf("reply after the tool call (reload) = %+v", live.Parts)
	}
	f.post(t, run, map[string]any{"type": "action.result", "activityId": "c1:result", "callId": "c1", "content": map[string]any{"action": "list_issues", "result": `{"items":[]}`}})
	f.post(t, run, map[string]any{"type": "text.delta", "body": "The backlog is "})
	f.post(t, run, map[string]any{"type": "text.delta", "body": "empty."})
	answer := f.post(t, run, map[string]any{"type": "response", "activityId": "end", "body": "The backlog is empty."})
	if answer["settled"] != true {
		t.Fatalf("response answer = %v", answer)
	}
	f.complete(t, run, "done")

	rest := drain(t, events)
	kinds := []string{}
	for _, event := range rest {
		kinds = append(kinds, event.Type)
	}
	for _, kind := range []string{"tool.started", "tool.completed", "text.delta", "text.replaced", "session.completed"} {
		if !slices.Contains(kinds, kind) {
			t.Fatalf("stream events %v lack %s", kinds, kind)
		}
	}
	completed := rest[len(rest)-1].Session
	final := lastAgentMessage(*completed)
	if final.ID != messageID || final.Status != "" || final.Content != "The backlog is empty." || !slices.Equal(partTypes(final.Parts), []string{"reasoning", "toolCall", "text"}) {
		t.Fatalf("finished reply = %+v", final)
	}
	if call := final.Parts[1].ToolCall; call.Status != "completed" || string(call.Result) != `{"items":[]}` {
		t.Fatalf("tool call = %+v", call)
	}
	// A reload reads the same saved reply.
	if reloaded := f.reply(t, sessionID); reloaded.Content != final.Content || reloaded.Status != "" || len(reloaded.Parts) != 3 {
		t.Fatalf("reloaded reply = %+v", reloaded)
	}
	if done, _ := f.s.store.AgentRun(t.Context(), run.ID); done.Status != store.AgentRunDone {
		t.Fatalf("run status = %s", done.Status)
	}
	// Activities after the end change nothing.
	runtimeDo(t, f.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/activities", worker: "w1", bearer: testWorkerSecret, body: map[string]any{"type": "text.delta", "body": "late"}})
	if reloaded := f.reply(t, sessionID); reloaded.Content != "The backlog is empty." {
		t.Fatalf("late activity changed the reply: %+v", reloaded)
	}
}

// Calls a connector set to always ask holds wait on the reply, each with its own approval; the run suspends (its
// state in Flow), and once both are answered (one before the run suspended) a resume run continues the same reply
// with the person's decisions. A rejected call stays declined whatever the resume reports for it.
func TestWorkerChatApprovalsResumeTheReply(t *testing.T) {
	f := newWorkerChatFixture(t)
	events := f.stream("/api/agent/sessions/stream", `{"message":"Deploy and tag the release","location":"page"}`)
	started := nextEvent(t, events, "session.started")
	sessionID := started.Session.ID
	run := f.mustClaim(t)
	f.post(t, run, map[string]any{"type": "elicitation", "activityId": "elicitation", "elicitation": map[string]any{"kind": "approval", "body": "Run tools?", "calls": []map[string]any{
		{"callId": "c1", "name": "external_deploy", "arguments": `{"env":"prod"}`},
		{"callId": "c2", "name": "external_tag", "arguments": `{}`},
	}}})
	first := nextEvent(t, events, "tool.approval_required")
	second := nextEvent(t, events, "tool.approval_required")
	runtimeDo(t, f.handler, runtimeCall{method: http.MethodPut, path: "/internal/agent-runtime/runs/" + run.ID + "/state", worker: "w1", bearer: testWorkerSecret, body: []byte(`{"state":"suspended-run"}`), headers: map[string]string{"Content-Type": "application/json"}})
	held := f.reply(t, sessionID)
	for _, part := range held.Parts {
		if part.ToolCall == nil || part.ToolCall.ApprovalID == "" || part.Status != "pending" {
			t.Fatalf("held calls = %+v", held.Parts)
		}
	}
	// The first answer arrives before the worker suspended the run: nothing resumes yet.
	if status, body := postAgentJSON(t, f.api.URL, "/api/agent/sessions/"+sessionID+"/approvals/"+first.ApprovalID, `{"decision":"approve"}`); status != http.StatusOK {
		t.Fatalf("approve: %d %s", status, body)
	}
	f.complete(t, run, "awaiting")
	if _, queued := f.claim(t, 0); queued {
		t.Fatal("a resume was queued while a call still waits")
	}
	if status, body := postAgentJSON(t, f.api.URL, "/api/agent/sessions/"+sessionID+"/approvals/"+second.ApprovalID, `{"decision":"reject"}`); status != http.StatusOK {
		t.Fatalf("reject: %d %s", status, body)
	}
	resume := f.mustClaim(t)
	payload := f.payload(t, resume)
	if resume.SessionID != sessionID || payload.Chat.MessageID != started.MessageID || payload.Chat.Resume == nil || payload.Chat.Resume.Decisions["c1"] != "approved" || payload.Chat.Resume.Decisions["c2"] != "rejected" {
		t.Fatalf("resume payload = %+v", payload.Chat)
	}
	if status, state, _ := runtimeDo(t, f.handler, runtimeCall{method: http.MethodGet, path: "/internal/agent-runtime/runs/" + resume.ID + "/state", worker: "w1", bearer: testWorkerSecret}); status != http.StatusOK || string(state) != `{"state":"suspended-run"}` {
		t.Fatalf("suspended state = %d %s", status, state)
	}
	f.post(t, resume, map[string]any{"type": "action.result", "activityId": "c1:result", "callId": "c1", "content": map[string]any{"action": "external_deploy", "result": "deployed"}})
	f.post(t, resume, map[string]any{"type": "action.result", "activityId": "c2:result", "callId": "c2", "content": map[string]any{"action": "external_tag", "result": "The user rejected this action"}})
	f.post(t, resume, map[string]any{"type": "response", "activityId": "end", "body": "Deployed; the tag was skipped."})
	f.complete(t, resume, "done")
	rest := drain(t, events)
	final := lastAgentMessage(*rest[len(rest)-1].Session)
	deploy, tag := toolPart(final.Parts, "c1"), toolPart(final.Parts, "c2")
	if final.ID != started.MessageID || final.Status != "" || deploy == nil || deploy.ToolCall.Status != "completed" || string(deploy.ToolCall.Result) != `"deployed"` || tag == nil || tag.ToolCall.Status != "error" || tag.ToolCall.Error != workerChatDeclined {
		t.Fatalf("finished reply = %+v", final.Parts)
	}
	if status, _ := postAgentJSON(t, f.api.URL, "/api/agent/sessions/"+sessionID+"/approvals/"+first.ApprovalID, `{"decision":"approve"}`); status != http.StatusNotFound {
		t.Fatalf("answering a finished reply's approval = %d", status)
	}
}

// Stop ends a worker reply wherever its run is: a held run is cancelled through the worker's cancel long-poll and
// the reply is saved as stopped when the worker finishes it; a queued or awaiting run is cancelled and the reply
// saved as stopped at once.
func TestWorkerChatStopEndsTheRun(t *testing.T) {
	f := newWorkerChatFixture(t)
	stopped := func(message domain.AgentMessage) bool {
		return message.Status == "" && len(message.Parts) > 0 && message.Parts[len(message.Parts)-1].Text == "Generation stopped"
	}

	// Held by the worker.
	events := f.stream("/api/agent/sessions/stream", `{"message":"Summarize the project","location":"page"}`)
	sessionID := nextEvent(t, events, "session.started").Session.ID
	run := f.mustClaim(t)
	f.post(t, run, map[string]any{"type": "text.delta", "body": "Reading the project"})
	if status, body := postAgentJSON(t, f.api.URL, "/api/agent/sessions/"+sessionID+"/stop", `{}`); status != http.StatusNoContent {
		t.Fatalf("stop: %d %s", status, body)
	}
	cancels := runtimeJSON[struct {
		Cancel []string `json:"cancel"`
	}](t, f.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/cancels", worker: "w1", body: map[string]any{"waitMs": 2000}}, http.StatusOK)
	if !slices.Contains(cancels.Cancel, run.ID) {
		t.Fatalf("cancel long-poll = %v", cancels.Cancel)
	}
	f.post(t, run, map[string]any{"type": "error", "activityId": "end", "body": "Generation stopped"})
	f.complete(t, run, "canceled")
	final := f.waitReply(t, sessionID, stopped)
	if final.Content != "Reading the project" {
		t.Fatalf("stopped reply = %+v", final)
	}
	for _, event := range drain(t, events) {
		if event.Type == "session.completed" || event.Type == "error" {
			t.Fatalf("a stopped stream ends quietly, got %s", event.Type)
		}
	}

	// Still queued: no worker took it.
	events = f.stream("/api/agent/sessions/stream", `{"message":"Another question","location":"page"}`)
	sessionID = nextEvent(t, events, "session.started").Session.ID
	if status, _ := postAgentJSON(t, f.api.URL, "/api/agent/sessions/"+sessionID+"/stop", `{}`); status != http.StatusNoContent {
		t.Fatal("stop failed")
	}
	f.waitReply(t, sessionID, stopped)
	if _, queued := f.claim(t, 0); queued {
		t.Fatal("a stopped reply's run can still be claimed")
	}
	drain(t, events)

	// Suspended on an approval.
	events = f.stream("/api/agent/sessions/stream", `{"message":"Deploy it","location":"page"}`)
	sessionID = nextEvent(t, events, "session.started").Session.ID
	run = f.mustClaim(t)
	f.post(t, run, map[string]any{"type": "elicitation", "activityId": "elicitation", "elicitation": map[string]any{"kind": "approval", "calls": []map[string]any{{"callId": "c1", "name": "external_deploy", "arguments": `{}`}}}})
	approval := nextEvent(t, events, "tool.approval_required").ApprovalID
	runtimeDo(t, f.handler, runtimeCall{method: http.MethodPut, path: "/internal/agent-runtime/runs/" + run.ID + "/state", worker: "w1", bearer: testWorkerSecret, body: []byte(`{"state":"s"}`)})
	f.complete(t, run, "awaiting")
	if status, _ := postAgentJSON(t, f.api.URL, "/api/agent/sessions/"+sessionID+"/stop", `{}`); status != http.StatusNoContent {
		t.Fatal("stop failed")
	}
	final = f.waitReply(t, sessionID, stopped)
	if part := toolPart(final.Parts, "c1"); part == nil || part.ToolCall.Status != "error" || part.ToolCall.ApprovalID != "" {
		t.Fatalf("held call after stop = %+v", final.Parts)
	}
	if current, _ := f.s.store.AgentRun(t.Context(), run.ID); current.Status != store.AgentRunCanceled {
		t.Fatalf("awaiting run after stop = %s", current.Status)
	}
	if _, err := f.s.store.AgentSessionStateFor(t.Context(), f.data.Workspace.URLKey, sessionID); err == nil {
		t.Fatal("a stopped reply keeps its suspended state")
	}
	if status, _ := postAgentJSON(t, f.api.URL, "/api/agent/sessions/"+sessionID+"/approvals/"+approval, `{"decision":"approve"}`); status != http.StatusNotFound {
		t.Fatalf("approving a stopped reply = %d", status)
	}
	drain(t, events)
}

// A worker that dies mid-reply leaves no reply hanging: a run that wrote is interrupted by the reconciler (the
// reply gets the interrupted error row), one that wrote nothing is queued again with the reply reset, and a run
// that ended without saving its reply is interrupted by the live-reply sweeper.
func TestWorkerChatCrashInterruptsOrRestartsTheReply(t *testing.T) {
	f := newWorkerChatFixture(t)
	ctx := context.Background()

	events := f.stream("/api/agent/sessions/stream", `{"message":"Close DEV-1","location":"page"}`)
	sessionID := nextEvent(t, events, "session.started").Session.ID
	run := f.mustClaim(t)
	f.post(t, run, map[string]any{"type": "action", "activityId": "c1:call", "callId": "c1", "content": map[string]any{"action": "save_issue", "parameter": `{"id":"DEV-1"}`}})
	if err := f.s.store.MarkAgentRunWrote(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, interrupted := f.s.reconcileAgentRuns(ctx, time.Now().Add(2*time.Minute)); interrupted != 1 {
		t.Fatalf("interrupted = %d", interrupted)
	}
	final := f.waitReply(t, sessionID, func(message domain.AgentMessage) bool { return message.Status == "" })
	if last := final.Parts[len(final.Parts)-1]; last.Type != "error" || last.Text != agentReplyInterrupted {
		t.Fatalf("interrupted reply = %+v", final.Parts)
	}
	if part := toolPart(final.Parts, "c1"); part == nil || part.ToolCall.Status != "error" || len(part.ToolCall.Result) == 0 {
		t.Fatalf("interrupted call = %+v", part)
	}
	drain(t, events)

	events = f.stream("/api/agent/sessions/stream", `{"message":"What changed today?","location":"page"}`)
	started := nextEvent(t, events, "session.started")
	sessionID = started.Session.ID
	run = f.mustClaim(t)
	f.post(t, run, map[string]any{"type": "text.delta", "body": "Looking"})
	f.post(t, run, map[string]any{"type": "action", "activityId": "c1:call", "callId": "c1", "content": map[string]any{"action": "list_issues", "parameter": map[string]any{}}})
	// The worker's last heartbeat was two minutes ago: its lease has expired.
	if _, err := f.s.store.HeartbeatAgentRun(ctx, run.ID, "w1", time.Minute, time.Now().Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if requeued, _ := f.s.reconcileAgentRuns(ctx, time.Now()); requeued != 1 {
		t.Fatalf("requeued = %d", requeued)
	}
	if reset := f.reply(t, sessionID); reset.Status != agentMessageStreaming || len(reset.Parts) != 0 {
		t.Fatalf("reply after requeue = %+v", reset)
	}
	again := f.mustClaim(t)
	if again.ID != run.ID || again.Attempts != 2 {
		t.Fatalf("second attempt = %+v", again)
	}
	f.post(t, again, map[string]any{"type": "response", "activityId": "end", "body": "Nothing changed."})
	f.complete(t, again, "done")
	if final := f.reply(t, sessionID); final.Content != "Nothing changed." || final.ID != started.MessageID {
		t.Fatalf("reply after the retried attempt = %+v", final)
	}
	drain(t, events)

	// The run ended (here: finished in the store directly) without its reply being saved.
	events = f.stream("/api/agent/sessions/stream", `{"message":"One more","location":"page"}`)
	sessionID = nextEvent(t, events, "session.started").Session.ID
	run = f.mustClaim(t)
	if _, err := f.s.store.FinishAgentRun(ctx, run.ID, "w1", store.AgentRunResult{Status: store.AgentRunFailed}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if swept := f.s.sweepWorkerChatReplies(ctx, time.Now()); swept != 0 {
		t.Fatalf("a reply whose run just ended was swept (%d)", swept)
	}
	if swept := f.s.sweepWorkerChatReplies(ctx, time.Now().Add(2*time.Minute)); swept != 1 {
		t.Fatalf("swept = %d", swept)
	}
	if final := f.reply(t, sessionID); final.Status != "" || final.Parts[len(final.Parts)-1].Text != agentReplyInterrupted {
		t.Fatalf("swept reply = %+v", final)
	}
	drain(t, events)
}

// ask_user ends a worker reply with its question, as in-process; the answer is the person's next message, a new
// run whose conversation carries the question's call and the answer.
func TestWorkerChatQuestionEndsTheReply(t *testing.T) {
	f := newWorkerChatFixture(t)
	events := f.stream("/api/agent/sessions/stream", `{"message":"Create an issue","location":"page"}`)
	sessionID := nextEvent(t, events, "session.started").Session.ID
	run := f.mustClaim(t)
	arguments := `{"question":"Which team?","options":[{"label":"Dev","value":"dev"},{"label":"Ops","value":"ops"}]}`
	f.post(t, run, map[string]any{"type": "text.delta", "body": "I can do that."})
	f.post(t, run, map[string]any{"type": "question.delta", "callId": "ask1", "body": "Which"})
	f.post(t, run, map[string]any{"type": "elicitation", "activityId": "elicitation", "elicitation": map[string]any{"kind": "select", "callId": "ask1", "arguments": arguments, "body": "Which team?"}})
	f.complete(t, run, "done")
	rest := drain(t, events)
	final := lastAgentMessage(*rest[len(rest)-1].Session)
	question := final.Parts[len(final.Parts)-1]
	if final.Status != "" || final.Content != "I can do that.\n\nWhich team?" || question.Type != "question" || question.Status != "pending" || len(question.Question.Options) != 2 || question.ToolCall == nil || question.ToolCall.Name != askUserTool {
		t.Fatalf("reply with a question = %+v", final)
	}
	events = f.stream("/api/agent/sessions/"+sessionID+"/messages/stream", fmt.Sprintf(`{"message":"Dev","answer":{"partId":%q,"value":"dev"}}`, question.ID))
	nextEvent(t, events, "session.started")
	next := f.mustClaim(t)
	payload := f.payload(t, next)
	messages := payload.Context.Messages
	asked := slices.IndexFunc(messages, func(message agentRunPromptMessage) bool {
		return message.Role == "assistant" && slices.ContainsFunc(message.ToolCalls, func(call agentRunPromptToolCall) bool { return call.ID == "ask1" && call.Name == askUserTool })
	})
	if asked < 0 || asked+1 >= len(messages) || messages[asked+1].Role != "tool" || !strings.Contains(messages[asked+1].Content, "dev") || messages[len(messages)-1].Content != "Dev" || payload.Chat.Resume != nil {
		t.Fatalf("follow-up conversation = %+v", messages)
	}
	f.post(t, next, map[string]any{"type": "response", "activityId": "end", "body": "Created it in Dev."})
	f.complete(t, next, "done")
	drain(t, events)
}

// A connector's question in a worker chat run shows on the reply, and Stop cancels it at once (the connector gets
// a cancel answer) instead of leaving the call waiting until the question times out.
func TestWorkerChatStopCancelsAConnectorQuestion(t *testing.T) {
	f := newWorkerChatFixture(t)
	upstream := elicitingConnector(t)
	setTestConnectors(t, f.s.store, applicationPolicy{ID: "deploy", Kind: "mcp", Name: "Deploy", URL: upstream.URL, Status: "approved", Shared: true})
	events := f.stream("/api/agent/sessions/stream", `{"message":"Confirm the deployment","location":"page"}`)
	sessionID := nextEvent(t, events, "session.started").Session.ID
	run := f.mustClaim(t)
	token := f.token(t, run)
	done := make(chan map[string]any, 1)
	go func() {
		done <- callRunTool(t, f.handler, token, connectorToolName("deploy", "confirm"), map[string]any{}, nil)
	}()
	asked := nextEvent(t, events, "elicitation.requested")
	if asked.Part == nil || asked.Part.Elicitation == nil || asked.Part.Elicitation.Message != "Choose a region" {
		t.Fatalf("question event = %+v", asked)
	}
	f.waitReply(t, sessionID, func(message domain.AgentMessage) bool {
		return slices.ContainsFunc(message.Parts, func(part domain.AgentMessagePart) bool { return part.Type == "elicitation" && part.Status == "pending" })
	})
	if status, _ := postAgentJSON(t, f.api.URL, "/api/agent/sessions/"+sessionID+"/stop", `{}`); status != http.StatusNoContent {
		t.Fatal("stop failed")
	}
	select {
	case result := <-done:
		if !strings.HasPrefix(runToolText(result), "cancel:") {
			t.Fatalf("connector result after stop = %v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop left the connector question waiting")
	}
	f.post(t, run, map[string]any{"type": "error", "activityId": "end", "body": "Generation stopped"})
	f.complete(t, run, "canceled")
	final := f.waitReply(t, sessionID, func(message domain.AgentMessage) bool { return message.Status == "" })
	index := slices.IndexFunc(final.Parts, func(part domain.AgentMessagePart) bool { return part.Type == "elicitation" })
	if index < 0 || final.Parts[index].Status != "error" || final.Parts[index].Elicitation.Action != "cancel" || final.Parts[len(final.Parts)-1].Text != "Generation stopped" {
		t.Fatalf("stopped reply = %+v", final.Parts)
	}
	drain(t, events)
}

// A worker chat reply has the in-process reply's tool budget (calls over it are not run, a spent budget withholds
// the tools for the last word, which ends with the budget notice) and its outcome check (an answer that reports
// change outcomes no write produced is sent back once, then becomes Flow's statement).
func TestWorkerChatBudgetAndOutcomeCheck(t *testing.T) {
	f := newWorkerChatFixture(t)
	f.s.agent.MaxToolCalls = 1
	events := f.stream("/api/agent/sessions/stream", `{"message":"List the backlog","location":"page"}`)
	sessionID := nextEvent(t, events, "session.started").Session.ID
	run := f.mustClaim(t)
	token := f.token(t, run)
	guard := func(request map[string]any) workerRunGuardResponse {
		return runtimeJSON[workerRunGuardResponse](t, f.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/guard", worker: "w1", body: request}, http.StatusOK)
	}
	if started := guard(map[string]any{"op": "start"}); !started.Enabled || started.DecisionRound != -1 || started.CodingDue {
		t.Fatalf("chat guard start = %+v", started)
	}
	if turn := guard(map[string]any{"op": "turn", "round": 0, "maxTurns": 10, "connectors": false}); turn.Withhold {
		t.Fatalf("first turn = %+v", turn)
	}
	if result := callRunTool(t, f.handler, token, "list_issues", map[string]any{"limit": 1}, nil); result["isError"] == true {
		t.Fatalf("first call: %v", result)
	}
	if result := callRunTool(t, f.handler, token, "list_projects", map[string]any{}, nil); result["isError"] != true || !strings.Contains(runToolText(result), "tool-call budget") {
		t.Fatalf("a call over the budget = %v", result)
	}
	turn := guard(map[string]any{"op": "turn", "round": 1, "maxTurns": 10, "connectors": false})
	if !turn.Withhold || !strings.Contains(turn.Nudge, "must stop now") {
		t.Fatalf("turn after the budget = %+v", turn)
	}
	if again := guard(map[string]any{"op": "turn", "round": 2, "maxTurns": 10, "connectors": false}); !again.Withhold || again.Nudge != "" {
		t.Fatalf("the nudge is sent once: %+v", again)
	}
	finish := guard(map[string]any{"op": "finish", "phase": "reply", "text": "Here is one issue."})
	f.post(t, run, map[string]any{"type": "response", "activityId": "end", "body": finish.Reply})
	f.complete(t, run, "done")
	final := f.reply(t, sessionID)
	if final.Parts[len(final.Parts)-1].Type != "event" || !strings.Contains(final.Parts[len(final.Parts)-1].Text, "tool-call limit") || !strings.Contains(final.Content, "Here is one issue.") {
		t.Fatalf("reply stopped by the budget = %+v", final)
	}
	drain(t, events)

	f.s.agent.MaxToolCalls = 0
	events = f.stream("/api/agent/sessions/stream", `{"message":"把 DEV-1 移到 Alpha 团队","location":"page"}`)
	sessionID = nextEvent(t, events, "session.started").Session.ID
	run = f.mustClaim(t)
	guard(map[string]any{"op": "start"})
	if first := guard(map[string]any{"op": "finish", "phase": "reply", "text": "移动失败：你没有权限。"}); first.Nudge != agentUngroundedOutcomeNudge || first.Reply != "" {
		t.Fatalf("first ungrounded answer = %+v", first)
	}
	second := guard(map[string]any{"op": "finish", "phase": "reply", "text": "移动失败：你没有权限。"})
	if second.Reply != agentNothingExecutedText(true) {
		t.Fatalf("second ungrounded answer = %+v", second)
	}
	f.post(t, run, map[string]any{"type": "response", "activityId": "end", "body": second.Reply})
	f.complete(t, run, "done")
	if final := f.reply(t, sessionID); final.Content != agentNothingExecutedText(true) {
		t.Fatalf("reply = %+v", final)
	}
	drain(t, events)
}

// With the default runtime nothing is queued: the reply runs in-process.
func TestChatRepliesStayInProcessByDefault(t *testing.T) {
	f := newAgentAttributionFixture(t)
	f.s.agent.WorkerSecret = testWorkerSecret
	if f.s.agentChatOnWorker(domain.AgentSession{}) {
		t.Fatal("chat runs on the worker without FLOW_AGENT_RUNTIME=worker")
	}
	f.s.agent.Runtime = appconfig.AgentRuntimeWorker
	if !f.s.agentChatOnWorker(domain.AgentSession{}) || f.s.agentChatOnWorker(domain.AgentSession{LoopIDs: []string{"loop"}}) || f.s.agentChatOnWorker(domain.AgentSession{Messages: []domain.AgentMessage{{Role: "user", Attachments: []domain.AgentMessageAttachment{{Name: "a.png"}}}}}) {
		t.Fatal("worker eligibility")
	}
}
