package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// workerLoopHarness plays the agent worker for loop runs against a loop test server with FLOW_AGENT_RUNTIME=worker.
type workerLoopHarness struct {
	s       *server
	handler http.Handler
}

func newWorkerLoopHarness(t *testing.T) workerLoopHarness {
	t.Helper()
	s, handler := newReliableLoopServer(t, &fakeLoopProvider{})
	s.agent.WorkerSecret, s.agent.Runtime = testWorkerSecret, appconfig.AgentRuntimeWorker
	return workerLoopHarness{s: s, handler: handler}
}

func (h workerLoopHarness) claim(t *testing.T) store.AgentRun {
	t.Helper()
	claimed := runtimeJSON[struct {
		Runs []store.AgentRun `json:"runs"`
	}](t, h.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/claim", worker: "w1", body: map[string]any{"free": map[string]int{"loop": 1}, "max": 1}}, http.StatusOK)
	if len(claimed.Runs) != 1 || claimed.Runs[0].Kind != store.AgentRunKindLoop {
		t.Fatalf("claimed %+v", claimed.Runs)
	}
	return claimed.Runs[0]
}

func (h workerLoopHarness) call(t *testing.T, run store.AgentRun, path string, body any, want int) map[string]any {
	t.Helper()
	return runtimeJSON[map[string]any](t, h.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + path, worker: "w1", body: body}, want)
}

func (h workerLoopHarness) guard(t *testing.T, run store.AgentRun, request map[string]any) workerRunGuardResponse {
	t.Helper()
	return runtimeJSON[workerRunGuardResponse](t, h.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/guard", worker: "w1", body: request}, http.StatusOK)
}

func (h workerLoopHarness) loopRun(t *testing.T, loopID, runID string) domain.LoopRun {
	t.Helper()
	return requestJSON[domain.LoopRun](t, h.handler, http.MethodGet, "/api/loops/"+loopID+"/runs/"+runID, nil, http.StatusOK)
}

// The reply-only loop on the worker, as TestLoopReplyOnlyRunCreatesNothing in-process: the run is queued for the
// worker with the in-process prompt, /mcp lists the loop's tools, the create is refused by the same contract gate
// and recorded as blocked, finish_run reports the run, and completing the agent run finishes the loop run with
// the same acceptance: completed, nothing produced, the refused-write notice, the report as output.
func TestWorkerLoopRunKeepsTheLoopsGates(t *testing.T) {
	h := newWorkerLoopHarness(t)
	bootstrap := requestJSON[domain.Bootstrap](t, h.handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	loop, started := startTestRun(t, h.handler, "团队日报", loopReplyOnlyDigestZH, nil)
	if started.Status != "running" {
		t.Fatalf("started = %+v", started)
	}
	run := h.claim(t)
	if run.SessionID != started.ID {
		t.Fatalf("agent run session = %s, want the loop run %s", run.SessionID, started.ID)
	}
	payload := runtimeJSON[struct {
		Loop    *workerLoopRunInfo    `json:"loop"`
		Context agentRunPromptContext `json:"context"`
	}](t, h.handler, runtimeCall{method: http.MethodGet, path: "/internal/agent-runtime/runs/" + run.ID + "/payload", worker: "w1"}, http.StatusOK)
	if payload.Loop == nil || payload.Loop.LoopRunID != started.ID || !strings.Contains(payload.Context.System, "This loop reads and reports") || !strings.Contains(payload.Context.System, "forbid every change") || len(payload.Context.Messages) != 1 {
		t.Fatalf("payload = %+v\n%s", payload.Loop, payload.Context.System)
	}
	token := h.call(t, run, "/token", map[string]any{}, http.StatusCreated)["token"].(string)
	tools := listedRunTools(t, h.handler, token)
	if tools[loopFinishToolName] == nil || tools["list_issues"] == nil || tools[askUserTool] != nil {
		t.Fatalf("loop tools: finish_run %v list_issues %v ask_user %v", tools[loopFinishToolName] != nil, tools["list_issues"] != nil, tools[askUserTool] != nil)
	}
	h.guard(t, run, map[string]any{"op": "start"})
	h.guard(t, run, map[string]any{"op": "turn", "round": 0, "maxTurns": 30})
	refused := callRunTool(t, h.handler, token, "save_issue", map[string]any{"title": "今日工作总结", "team": bootstrap.Teams[0].ID, "description": "今日完成 3 项"}, nil)
	if refused["isError"] != true || !strings.Contains(runToolText(refused), loopProhibitedCallPrefix) {
		t.Fatalf("save_issue in a reply-only loop = %v", refused)
	}
	if done := callRunTool(t, h.handler, token, loopFinishToolName, map[string]any{"status": "done", "summary": "今日完成 3 项，新增 2 个事项。", "done": []string{"创建了今日总结事项"}}, nil); done["isError"] == true {
		t.Fatalf("finish_run = %v", done)
	}
	if after := callRunTool(t, h.handler, token, "list_issues", map[string]any{"limit": 1}, nil); after["isError"] != true || !strings.Contains(runToolText(after), "already finished with finish_run") {
		t.Fatalf("a call after finish_run = %v", after)
	}
	finish := h.guard(t, run, map[string]any{"op": "finish", "phase": "reply", "text": `{"ok":true}`})
	if !strings.Contains(finish.Reply, "今日完成 3 项") || finish.Nudge != "" {
		t.Fatalf("finish = %+v", finish)
	}
	h.call(t, run, "/complete", map[string]any{"status": "done"}, http.StatusOK)

	record := waitForRun(t, h.handler, loop.ID, started.ID)
	call := loopRunCall(record, "save_issue", 0)
	if call.Status != "blocked" || !strings.HasPrefix(call.Error, loopProhibitedCallPrefix) {
		t.Fatalf("recorded save_issue = %s %q", call.Status, call.Error)
	}
	if record.Status != "completed" || len(record.ExpectedOutputs) != 0 || record.Produced["issue"] != 0 || record.Produced["change"] != 0 {
		t.Fatalf("run = %s %s %q expected %v produced %v", record.Status, record.FailureReason, record.Error, record.ExpectedOutputs, record.Produced)
	}
	if !strings.Contains(record.Output, "今日完成 3 项") || !slices.Contains(record.Notices, loopRefusedWriteNotice) || record.Summary == nil {
		t.Fatalf("output %q notices %v summary %+v", record.Output, record.Notices, record.Summary)
	}
}

// The turn rules of a loop run on the worker: at the turn budget the tools are withheld with the summary prompt; a
// reply without finish_run is sent back with the in-process completion nudge; the last word ends the run.
func TestWorkerLoopRunTurnRules(t *testing.T) {
	h := newWorkerLoopHarness(t)
	setLoopLimits(h.s, func(limits *loopRunLimits) { limits.MaxTurns = 2 })
	loop, started := startTestRun(t, h.handler, "Summary", "List the open issues and summarize them.", nil)
	run := h.claim(t)
	h.guard(t, run, map[string]any{"op": "start"})
	if turn := h.guard(t, run, map[string]any{"op": "turn", "round": 1}); turn.Withhold {
		t.Fatal("tools withheld before the turn budget")
	}
	first := h.guard(t, run, map[string]any{"op": "finish", "phase": "reply", "text": "There are 3 open issues."})
	if first.Nudge == "" || first.Reply != "" {
		t.Fatalf("a reply without finish_run = %+v", first)
	}
	turn := h.guard(t, run, map[string]any{"op": "turn", "round": 2})
	if !turn.Withhold || !strings.Contains(turn.Nudge, loopSummaryPrompt) {
		t.Fatalf("turn at the budget = %+v", turn)
	}
	last := h.guard(t, run, map[string]any{"op": "finish", "phase": "reply", "text": "Summary: 3 open issues."})
	if !strings.Contains(last.Reply, "Summary: 3 open issues.") || last.Nudge != "" {
		t.Fatalf("last word = %+v", last)
	}
	h.call(t, run, "/complete", map[string]any{"status": "done"}, http.StatusOK)
	record := waitForRun(t, h.handler, loop.ID, started.ID)
	if record.Status == "running" || record.FailureReason != "budget_exhausted" || !strings.Contains(record.Output, "3 open issues") {
		t.Fatalf("run = %s %s %q output %q", record.Status, record.FailureReason, record.Error, record.Output)
	}
}

// Cancel reaches a loop run on the worker at once (its agent run is cancelled); a cancelled run ends cancelled. A
// queued run no worker takes ends as unavailable; heartbeats keep the loop run's lease.
func TestWorkerLoopRunCancelAndLease(t *testing.T) {
	h := newWorkerLoopHarness(t)
	loop, started := startTestRun(t, h.handler, "Cancel me", "List the open issues and summarize them.", nil)
	run := h.claim(t)
	beat := h.call(t, run, "/heartbeat", map[string]any{}, http.StatusOK)
	if beat["cancel"] == true {
		t.Fatalf("heartbeat = %v", beat)
	}
	runtimeDo(t, h.handler, runtimeCall{method: http.MethodPost, path: "/api/loops/" + loop.ID + "/runs/" + started.ID + "/cancel"})
	if canceled, _ := h.s.store.AgentRun(t.Context(), run.ID); !canceled.CancelRequested {
		t.Fatalf("the agent run was not asked to stop: %+v", canceled)
	}
	h.call(t, run, "/complete", map[string]any{"status": "canceled"}, http.StatusOK)
	if record := waitForRun(t, h.handler, loop.ID, started.ID); record.Status != "cancelled" {
		t.Fatalf("cancelled run = %s %s", record.Status, record.FailureReason)
	}

	// No worker: the queued run ends unavailable once the claim timeout passed.
	h.s.agent.WorkerClaimTimeout = time.Nanosecond
	_, queued := startTestRunAgain(t, h, loop)
	h.s.reconcileLoopRuns(t.Context(), false)
	if record := waitForRun(t, h.handler, loop.ID, queued.ID); record.Status != "failed" || record.FailureReason != "unavailable" || record.Error != workerChatNoWorker {
		t.Fatalf("unclaimed run = %s %s %q", record.Status, record.FailureReason, record.Error)
	}
}

func startTestRunAgain(t *testing.T, h workerLoopHarness, loop domain.Loop) (domain.Loop, domain.LoopRun) {
	t.Helper()
	run := requestJSON[domain.LoopRun](t, h.handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusAccepted)
	return loop, run
}

// A loop run's worker calls reach both API servers, one at a time: the run's tool calls, its step and its report are
// recorded on the loop run in order whichever server takes them, and the run finishes on the other.
func TestWorkerLoopRunAcrossAPIServers(t *testing.T) {
	peers := newWorkerChatPeers(t)
	a, b := peers[0], peers[1]
	loop := requestJSON[domain.Loop](t, a.handler, http.MethodPost, "/api/loops", map[string]any{"name": "Peer loop", "instructions": "List the open issues and summarize them.", "status": "published"}, http.StatusCreated)
	started := requestJSON[domain.LoopRun](t, a.handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusAccepted)
	claimed := runtimeJSON[struct {
		Runs []store.AgentRun `json:"runs"`
	}](t, b.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/claim", worker: "w1", body: map[string]any{"free": map[string]int{"loop": 1}}}, http.StatusOK)
	if len(claimed.Runs) != 1 {
		t.Fatalf("claimed %+v", claimed.Runs)
	}
	run := claimed.Runs[0]
	runtimeJSON[map[string]any](t, a.handler, runtimeCall{method: http.MethodGet, path: "/internal/agent-runtime/runs/" + run.ID + "/payload", worker: "w1"}, http.StatusOK)
	token := runtimeJSON[struct {
		Token string `json:"token"`
	}](t, b.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/token", worker: "w1"}, http.StatusCreated).Token
	steps := []struct {
		handler http.Handler
		name    string
		args    map[string]any
	}{
		{a.handler, "list_issues", map[string]any{"limit": 2}},
		{b.handler, agentProgressTool, map[string]any{"title": "Listed the issues", "message": "2 open"}},
		{a.handler, "list_teams", map[string]any{}},
		{b.handler, loopFinishToolName, map[string]any{"status": "done", "summary": "Two open issues.", "done": []string{"Listed the open issues"}}},
	}
	for index, step := range steps {
		if result, err := peerTool(step.handler, token, step.name, step.args, ""); err != nil || result["isError"] == true {
			t.Fatalf("call %d %s: %v %v", index, step.name, result, err)
		}
	}
	runtimeJSON[workerRunGuardResponse](t, a.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/guard", worker: "w1", body: map[string]any{"op": "finish", "phase": "reply", "text": ""}}, http.StatusOK)
	runtimeJSON[map[string]any](t, b.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/complete", worker: "w1", body: map[string]any{"status": "done"}}, http.StatusOK)
	record := requestJSON[domain.LoopRun](t, a.handler, http.MethodGet, "/api/loops/"+loop.ID+"/runs/"+started.ID, nil, http.StatusOK)
	names := []string{}
	for _, call := range record.ToolCalls {
		names = append(names, call.Name)
	}
	if record.Status != "completed" || !slices.Equal(names, []string{"list_issues", "list_teams"}) || len(record.Steps) != 1 || record.Steps[0].Order > record.ToolCalls[1].Order || !strings.Contains(record.Output, "Two open issues.") {
		t.Fatalf("run = %s %s %q calls %v steps %+v output %q", record.Status, record.FailureReason, record.Error, names, record.Steps, record.Output)
	}
	var stored map[string]any
	raw, _ := json.Marshal(record.Summary)
	if json.Unmarshal(raw, &stored) != nil || stored["summary"] != "Two open issues." {
		t.Fatalf("summary = %s", raw)
	}
}
