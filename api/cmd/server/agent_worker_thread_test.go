package main

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// workerThreadFixture plays the agent worker for built-in sessions (comment threads, delegation) against Flow's
// runtime API with FLOW_AGENT_RUNTIME=worker.
type workerThreadFixture struct {
	workerChatFixture
}

func newWorkerThreadFixture(t *testing.T) workerThreadFixture {
	return workerThreadFixture{newWorkerChatFixture(t)}
}

// deliver runs the application worker's delivery of the session (it queues the worker run) and returns the session.
func (f workerThreadFixture) deliver(t *testing.T, task domain.AgentTask) domain.AgentTask {
	t.Helper()
	current, err := f.s.store.AgentTask(t.Context(), task.WorkspaceKey, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.s.deliverApplicationTask(t.Context(), current)
	current, _ = f.s.store.AgentTask(t.Context(), task.WorkspaceKey, task.ID)
	return current
}

func (f workerThreadFixture) claimThread(t *testing.T) store.AgentRun {
	t.Helper()
	claimed := runtimeJSON[struct {
		Runs []store.AgentRun `json:"runs"`
	}](t, f.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/claim", worker: "w1", body: map[string]any{"free": map[string]int{"chat": 1, "thread": 1}, "max": 1}}, http.StatusOK)
	if len(claimed.Runs) != 1 {
		t.Fatalf("claimed %d runs, want 1", len(claimed.Runs))
	}
	return claimed.Runs[0]
}

func (f workerThreadFixture) guard(t *testing.T, run store.AgentRun, request map[string]any) workerRunGuardResponse {
	t.Helper()
	return runtimeJSON[workerRunGuardResponse](t, f.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/guard", worker: "w1", body: request}, http.StatusOK)
}

func (f workerThreadFixture) activities(t *testing.T, task domain.AgentTask) []domain.AgentActivity {
	t.Helper()
	activities, err := f.s.store.AgentActivities(t.Context(), task.WorkspaceKey, task.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	return activities
}

type workerThreadPayload struct {
	Thread  *workerThreadRunInfo  `json:"thread"`
	Context agentRunPromptContext `json:"context"`
}

// A mention in a comment thread runs on the worker: the delivery posts the first activity and queues a run acting
// as the built-in application for the person who asked; while it is queued or held the session is not delivered
// again; the worker's activities (a Flow tool call, streamed output, the response checked by the guard) are the
// session's; a question ends the run and the person's answer is the next run; held connector calls suspend the run
// and the person's decision resumes it.
func TestWorkerThreadMentionRunsOnTheWorker(t *testing.T) {
	f := newWorkerThreadFixture(t)
	issue := requestJSON[domain.Issue](t, f.handler, http.MethodPost, "/api/issues", map[string]any{"title": "Checkout fails on Safari", "teamId": f.data.Teams[0].ID}, http.StatusCreated)
	_, task := mentionAgentInComment(t, f.s, f.handler, f.data, f.app, issue.ID)
	task = f.deliver(t, task)
	if task.Status != "active" {
		t.Fatalf("session after delivery = %s", task.Status)
	}
	run := f.claimThread(t)
	stored, _ := f.s.store.AgentRun(t.Context(), run.ID)
	principal, _ := decodeAgentRunPrincipal(stored)
	if run.Kind != store.AgentRunKindThread || run.SessionID != task.ID || principal.Mode != agentRunModeApp || principal.AppUserID != f.app.UserID || principal.UserID != f.data.Viewer.ID {
		t.Fatalf("run = %+v principal %+v", run, principal)
	}
	// Held by a worker: delivering again neither queues another run nor interrupts the session.
	if again := f.deliver(t, task); again.Status != "active" {
		t.Fatalf("redelivery changed the session: %s", again.Status)
	}
	if runs, _ := f.s.store.SessionAgentRuns(t.Context(), task.WorkspaceKey, task.ID, 10); len(runs) != 1 {
		t.Fatalf("runs after redelivery = %d", len(runs))
	}
	payload := runtimeJSON[workerThreadPayload](t, f.handler, runtimeCall{method: http.MethodGet, path: "/internal/agent-runtime/runs/" + run.ID + "/payload", worker: "w1"}, http.StatusOK)
	if payload.Thread == nil || payload.Thread.MaxTurns != f.s.agentMaxToolTurns() || payload.Thread.Resume != nil || !strings.Contains(payload.Context.System, "comment thread") {
		t.Fatalf("payload = %+v", payload.Thread)
	}
	token := f.token(t, run)
	if view := f.guard(t, run, map[string]any{"op": "start"}); !view.Enabled {
		t.Fatal("the thread run has no guard")
	}
	f.guard(t, run, map[string]any{"op": "turn", "round": 0, "maxTurns": 99})
	post := func(activity map[string]any) {
		runtimeJSON[map[string]any](t, f.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/activities", worker: "w1", body: activity}, http.StatusOK)
	}
	post(map[string]any{"activityId": "1:1:action", "type": "action", "body": "get_issue"})
	if result := callRunTool(t, f.handler, token, "get_issue", map[string]any{"id": issue.Identifier}, nil); result["isError"] == true {
		t.Fatalf("get_issue = %v", result)
	}
	post(map[string]any{"activityId": "1:2:output", "type": "output", "body": "Looking at the checkout logs."})
	finish := f.guard(t, run, map[string]any{"op": "finish", "phase": "reply", "text": "The checkout fails because Safari blocks the payment iframe."})
	if finish.Reply == "" || finish.Nudge != "" {
		t.Fatalf("finish = %+v", finish)
	}
	post(map[string]any{"activityId": "1:3:response", "type": "response", "body": finish.Reply})
	f.complete(t, run, "done")
	task, _ = f.s.store.AgentTask(t.Context(), task.WorkspaceKey, task.ID)
	activities := f.activities(t, task)
	last := activities[len(activities)-1]
	if task.Status != "complete" || last.Type != "response" || !strings.Contains(last.Body, "Safari blocks the payment iframe") || last.ActorID != f.app.UserID {
		t.Fatalf("session = %s, last activity %+v", task.Status, last)
	}
	if !slices.ContainsFunc(activities, func(activity domain.AgentActivity) bool {
		return activity.Type == "action" && activity.Body == "get_issue" && !activity.Ephemeral
	}) {
		t.Fatal("the tool call is not in the session")
	}

	// A question with options ends the run; the answer is the next delivery.
	task = requestJSON[domain.AgentTask](t, f.handler, http.MethodPost, "/api/agent-tasks/"+task.ID+"/activities", map[string]any{"expectedVersion": task.Version, "type": "prompt", "body": "Can you fix it?"}, http.StatusOK)
	f.deliver(t, task)
	asking := f.claimThread(t)
	runtimeJSON[map[string]any](t, f.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + asking.ID + "/activities", worker: "w1", body: map[string]any{"activityId": "1:1:elicitation", "type": "elicitation", "body": "I can patch the iframe policy.", "elicitation": map[string]any{"kind": "select", "callId": "ask1", "arguments": `{"question":"Which browser versions?","options":[{"label":"All"},{"label":"Safari 17+"}]}`}}}, http.StatusOK)
	f.complete(t, asking, "done")
	task, _ = f.s.store.AgentTask(t.Context(), task.WorkspaceKey, task.ID)
	activities = f.activities(t, task)
	question := activities[len(activities)-1]
	options, _ := question.SignalMetadata["options"].([]any)
	if task.Status != "awaitingInput" || question.Type != "elicitation" || question.Signal != "select" || question.Body != "I can patch the iframe policy.\n\nWhich browser versions?" || len(options) != 2 || task.PendingTool != nil {
		t.Fatalf("question = %s %+v", task.Status, question)
	}

	// Held connector calls: one approval whose decision resumes the suspended run.
	task = requestJSON[domain.AgentTask](t, f.handler, http.MethodPost, "/api/agent-tasks/"+task.ID+"/activities", map[string]any{"expectedVersion": task.Version, "type": "prompt", "body": "Safari 17+"}, http.StatusOK)
	f.deliver(t, task)
	holding := f.claimThread(t)
	runtimeJSON[map[string]any](t, f.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + holding.ID + "/activities", worker: "w1", body: map[string]any{"activityId": "1:1:elicitation", "type": "elicitation", "body": "Deploying the fix.", "elicitation": map[string]any{"kind": "approval", "calls": []map[string]any{{"callId": "d1", "name": "external_deploy", "arguments": `{"env":"prod"}`}}}}}, http.StatusOK)
	runtimeDo(t, f.handler, runtimeCall{method: http.MethodPut, path: "/internal/agent-runtime/runs/" + holding.ID + "/state", worker: "w1", bearer: testWorkerSecret, body: []byte(`{"state":"suspended"}`)})
	f.complete(t, holding, "awaiting")
	task, _ = f.s.store.AgentTask(t.Context(), task.WorkspaceKey, task.ID)
	if task.Status != "awaitingInput" || task.PendingTool == nil || task.PendingTool.Name != agentApprovalBatchTool {
		t.Fatalf("held calls = %s %+v", task.Status, task.PendingTool)
	}
	if held := agentApprovalBatchCalls(*task.PendingTool); len(held) != 1 || held[0].ID != "d1" || held[0].Name != "external_deploy" {
		t.Fatalf("held = %+v", held)
	}
	activities = f.activities(t, task)
	if body := activities[len(activities)-1].Body; body != "Deploying the fix.\n\nAllow external_deploy?" {
		t.Fatalf("approval question = %q", body)
	}
	approve := true
	task = requestJSON[domain.AgentTask](t, f.handler, http.MethodPost, "/api/agent-tasks/"+task.ID+"/activities", map[string]any{"expectedVersion": task.Version, "type": "prompt", "body": "Approve", "approve": approve}, http.StatusOK)
	f.deliver(t, task)
	resumed := f.claimThread(t)
	again := runtimeJSON[workerThreadPayload](t, f.handler, runtimeCall{method: http.MethodGet, path: "/internal/agent-runtime/runs/" + resumed.ID + "/payload", worker: "w1"}, http.StatusOK)
	if again.Thread == nil || again.Thread.Resume == nil || again.Thread.Resume.Decisions["d1"] != "approved" {
		t.Fatalf("resume payload = %+v", again.Thread)
	}
	f.complete(t, resumed, "done")
}

// A thread run has the in-process run's tool budget: the turn budget withholds the tools with the nudge for the last
// word, which is the reply or Flow's summary; an empty reply is sent back once and then fails the run with the
// in-process error; a reply asking for approval in text is sent back once.
func TestWorkerThreadRunBudgetAndRetries(t *testing.T) {
	f := newWorkerThreadFixture(t)
	f.s.agent.MaxToolTurns = 2
	issue := requestJSON[domain.Issue](t, f.handler, http.MethodPost, "/api/issues", map[string]any{"title": "Budget", "teamId": f.data.Teams[0].ID}, http.StatusCreated)
	_, task := mentionAgentInComment(t, f.s, f.handler, f.data, f.app, issue.ID)
	f.deliver(t, task)
	run := f.claimThread(t)
	f.guard(t, run, map[string]any{"op": "start"})
	if turn := f.guard(t, run, map[string]any{"op": "turn", "round": 1, "maxTurns": 4}); turn.Withhold {
		t.Fatal("tools withheld before the turn budget")
	}
	turn := f.guard(t, run, map[string]any{"op": "turn", "round": 2, "maxTurns": 4})
	if !turn.Withhold || !strings.Contains(turn.Nudge, agentToolBudgetNudge(agentBudgetTurns, 2, f.s.agentMaxToolCalls())) {
		t.Fatalf("turn at the budget = %+v", turn)
	}
	finish := f.guard(t, run, map[string]any{"op": "finish", "phase": "reply", "text": ""})
	if !strings.Contains(finish.Reply, "2") || finish.Nudge != "" || finish.Error != "" {
		t.Fatalf("last word without text = %+v", finish)
	}

	_, other := mentionAgentInComment(t, f.s, f.handler, f.data, f.app, requestJSON[domain.Issue](t, f.handler, http.MethodPost, "/api/issues", map[string]any{"title": "Empty", "teamId": f.data.Teams[0].ID}, http.StatusCreated).ID)
	f.deliver(t, other)
	empty := f.claimThread(t)
	f.guard(t, empty, map[string]any{"op": "start"})
	if first := f.guard(t, empty, map[string]any{"op": "finish", "phase": "reply", "text": "  "}); first.Nudge != agentEmptyTurnNudge {
		t.Fatalf("first empty reply = %+v", first)
	}
	if second := f.guard(t, empty, map[string]any{"op": "finish", "phase": "reply", "text": ""}); second.Error != agentEmptyTurnError("") {
		t.Fatalf("second empty reply = %+v", second)
	}
	if first := f.guard(t, empty, map[string]any{"op": "finish", "phase": "reply", "text": "Allow save_issue?"}); first.Nudge != agentTextApprovalNudge {
		t.Fatalf("text approval = %+v", first)
	}
	if second := f.guard(t, empty, map[string]any{"op": "finish", "phase": "reply", "text": "Allow save_issue?"}); second.Error != agentTextApprovalError {
		t.Fatalf("second text approval = %+v", second)
	}
}

// A thread run no worker takes within the claim timeout ends the session with the no-worker error, as a chat reply.
func TestWorkerThreadUnclaimedRunEndsTheSession(t *testing.T) {
	f := newWorkerThreadFixture(t)
	f.s.agent.WorkerClaimTimeout = 1
	issue := requestJSON[domain.Issue](t, f.handler, http.MethodPost, "/api/issues", map[string]any{"title": "Nobody", "teamId": f.data.Teams[0].ID}, http.StatusCreated)
	_, task := mentionAgentInComment(t, f.s, f.handler, f.data, f.app, issue.ID)
	task = f.deliver(t, task)
	task = f.deliver(t, task)
	activities := f.activities(t, task)
	if last := activities[len(activities)-1]; task.Status != "error" || last.Body != workerChatNoWorker {
		t.Fatalf("session = %s, last %+v", task.Status, last)
	}
}
