package main

import (
	"net/http"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// The outcome ledger of chat replies (agent_action_outcomes.go) runs in application-agent threads and worker runs
// too: a text-only failure claim is sent back once, then replaced by Flow's statement; partial outcomes are counted
// from the real tool results, and a sentence claiming "all failed" against them is rewritten.

const outcomeInventedZH = "两个 issue 都迁移失败：你没有权限将 issue 移入私有团队 VLT。"

func lastUserMessage(request scriptedRequest) string {
	for index := len(request.Messages) - 1; index >= 0; index-- {
		if request.Messages[index].Role == "user" {
			text, _ := request.Messages[index].Content.(string)
			return text
		}
	}
	return ""
}

// A delegated thread run answers that every move failed for lack of permission without calling a tool: the model
// is told once that nothing ran, and its second such answer becomes Flow's statement.
func TestBuiltinThreadDoesNotPassOnFailuresNoToolReported(t *testing.T) {
	h := newDelegationHarness(t)
	run := h.delegate(t, "把 ENG-1 和 ENG-2 迁移到 VLT 团队", "", func(index int, _ domain.Issue) string {
		return chatTextSSE(outcomeInventedZH)
	})
	if len(run.requests) != 2 {
		t.Fatalf("the invented answer and one nudged retry: %d model calls", len(run.requests))
	}
	if nudge := lastUserMessage(run.requests[1]); nudge != agentUngroundedOutcomeNudge {
		t.Fatalf("the retry is told that nothing ran: %q", nudge)
	}
	if reply := run.response(); reply != agentNothingExecutedText(true) {
		t.Fatalf("the thread reply says nothing was executed: %q", reply)
	}
	if errors := run.errors(); len(errors) != 0 {
		t.Fatalf("the run ends without an error: %v", errors)
	}
}

// After the nudge the thread run makes the real call; the reply is the model's.
func TestBuiltinThreadNudgedOutcomeLeadsToTheRealCall(t *testing.T) {
	h := newDelegationHarness(t)
	target := requestJSON[domain.Issue](t, h.handler, http.MethodPost, "/api/issues", map[string]any{"title": "Old title", "teamId": h.teamID}, http.StatusCreated)
	run := h.delegate(t, "Rename "+target.Identifier+" to Renamed", "", func(index int, _ domain.Issue) string {
		switch index {
		case 0:
			return chatTextSSE("I couldn't update " + target.Identifier + ": you don't have permission.")
		case 1:
			return chatToolCallSSE("c_rename", "save_issue", map[string]any{"id": target.Identifier, "title": "Renamed"})
		}
		return chatTextSSE("Renamed " + target.Identifier + ".")
	})
	if len(run.requests) != 3 || lastUserMessage(run.requests[1]) != agentUngroundedOutcomeNudge {
		t.Fatalf("nudged once, then the call and the answer: %d model calls", len(run.requests))
	}
	if reply := run.response(); reply != "Renamed "+target.Identifier+"." {
		t.Fatalf("an all-success reply is the model's answer: %q", reply)
	}
	stored, err := h.s.store.IssueRecord(t.Context(), h.workspace, target.ID)
	if err != nil || stored.Title != "Renamed" {
		t.Fatalf("the rename landed: %q %v", stored.Title, err)
	}
}

// One update succeeds and one fails: the thread reply's counts are the tool results', and the model's "all failed"
// sentence is rewritten into them.
func TestBuiltinThreadReportsExactCountsForAPartialChange(t *testing.T) {
	h := newDelegationHarness(t)
	target := requestJSON[domain.Issue](t, h.handler, http.MethodPost, "/api/issues", map[string]any{"title": "Old title", "teamId": h.teamID}, http.StatusCreated)
	run := h.delegate(t, "Rename "+target.Identifier+" and NOPE-404 to Renamed", "", func(index int, _ domain.Issue) string {
		switch index {
		case 0:
			return chatToolCallSSE("c_one", "save_issue", map[string]any{"id": target.Identifier, "title": "Renamed"})
		case 1:
			return chatToolCallSSE("c_two", "save_issue", map[string]any{"id": "NOPE-404", "title": "Renamed"})
		}
		return chatTextSSE("All the updates failed because you do not have permission to edit these issues. Let me know if you need anything else.")
	})
	reply := run.response()
	for _, want := range []string{
		"Correction from Flow: of 2 change(s), 1 succeeded, 1 failed and 0 were not executed.",
		"**Result from Flow:** 2 change(s) requested: 2 executed (1 succeeded, 1 failed), 0 not executed.",
		"Failed: NOPE-404 (save_issue)",
		"Let me know if you need anything else.",
	} {
		if !strings.Contains(reply, want) {
			t.Fatalf("the reply reports %q: %q", want, reply)
		}
	}
	if strings.Contains(reply, "do not have permission") {
		t.Fatalf("the contrary sentence is rewritten: %q", reply)
	}
}

// workerOutcomeRun starts a worker run on a new issue delegated to the built-in agent.
func workerOutcomeRun(t *testing.T, h *delegationHarness, title string) (store.AgentRun, string, domain.AgentTask) {
	t.Helper()
	issue := requestJSON[domain.Issue](t, h.handler, http.MethodPost, "/api/issues", map[string]any{"title": title, "teamId": h.teamID, "delegateId": h.agentUser}, http.StatusCreated)
	task, err := h.s.store.AgentTask(t.Context(), h.workspace, issue.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	principal := agentRunPrincipal{Mode: agentRunModeApp, UserID: task.CreatorID, AppUserID: h.agentUser, TeamID: h.teamID}
	run, token := startWorkerTestRun(t, h.s, h.handler, store.AgentRunInput{WorkspaceKey: h.workspace, SessionID: task.ID, Kind: store.AgentRunKindDelegation}, principal)
	return run, token, task
}

func workerGuard(t *testing.T, h *delegationHarness, run store.AgentRun, request map[string]any) workerRunGuardResponse {
	t.Helper()
	return runtimeJSON[workerRunGuardResponse](t, h.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/guard", worker: "w1", body: request}, http.StatusOK)
}

func workerResponses(t *testing.T, h *delegationHarness, task domain.AgentTask) []string {
	t.Helper()
	activities, err := h.s.store.AgentActivities(t.Context(), h.workspace, task.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	bodies := []string{}
	for _, activity := range activities {
		if activity.Type == "response" {
			bodies = append(bodies, activity.Body)
		}
	}
	return bodies
}

// A worker run's last word that reports outcomes no write produced is answered with the nudge once, then with
// Flow's statement; the statement it posts is kept.
func TestWorkerRunDoesNotPassOnFailuresNoToolReported(t *testing.T) {
	h := newDelegationHarness(t)
	h.s.agent.WorkerSecret = testWorkerSecret
	run, _, task := workerOutcomeRun(t, h, "把 ENG-1 和 ENG-2 迁移到 VLT 团队")
	workerGuard(t, h, run, map[string]any{"op": "start"})
	first := workerGuard(t, h, run, map[string]any{"op": "finish", "phase": "reply", "text": outcomeInventedZH})
	if first.Nudge != agentUngroundedOutcomeNudge || first.Reply != "" || first.Error != "" {
		t.Fatalf("the first such answer is sent back: %+v", first)
	}
	if h.s.workerRunGuardFinished(t.Context(), run) {
		t.Fatal("a nudged run has not had its last word")
	}
	second := workerGuard(t, h, run, map[string]any{"op": "finish", "phase": "reply", "text": outcomeInventedZH})
	if second.Nudge != "" || second.Reply != agentNothingExecutedText(true) {
		t.Fatalf("the second such answer becomes Flow's statement: %+v", second)
	}
	runtimeJSON[map[string]any](t, h.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/activities", worker: "w1", body: map[string]any{"type": "response", "body": second.Reply}}, http.StatusOK)
	if bodies := workerResponses(t, h, task); len(bodies) != 1 || bodies[0] != agentNothingExecutedText(true) {
		t.Fatalf("the posted statement is kept: %q", bodies)
	}
}

// A worker run's writes go through /mcp: Flow counts them, rewrites an "all failed" claim, and the reply it gives is
// posted unchanged.
func TestWorkerRunReportsExactCountsForAPartialChange(t *testing.T) {
	h := newDelegationHarness(t)
	h.s.agent.WorkerSecret = testWorkerSecret
	target := requestJSON[domain.Issue](t, h.handler, http.MethodPost, "/api/issues", map[string]any{"title": "Old title", "teamId": h.teamID}, http.StatusCreated)
	run, token, task := workerOutcomeRun(t, h, "Rename "+target.Identifier+" and NOPE-404 to Renamed")
	workerGuard(t, h, run, map[string]any{"op": "start"})
	if result := callRunTool(t, h.handler, token, "save_issue", map[string]any{"id": target.Identifier, "title": "Renamed"}, nil); result["isError"] == true {
		t.Fatalf("the rename runs: %v", result)
	}
	if result := callRunTool(t, h.handler, token, "save_issue", map[string]any{"id": "NOPE-404", "title": "Renamed"}, nil); result["isError"] != true {
		t.Fatalf("the second rename fails: %v", result)
	}
	// A read is not a change.
	callRunTool(t, h.handler, token, "get_issue", map[string]any{"id": target.Identifier}, nil)
	answer := workerGuard(t, h, run, map[string]any{"op": "finish", "phase": "reply", "text": "Both updates failed: you do not have permission."})
	for _, want := range []string{
		"Correction from Flow: of 2 change(s), 1 succeeded, 1 failed and 0 were not executed.",
		"**Result from Flow:** 2 change(s) requested: 2 executed (1 succeeded, 1 failed), 0 not executed.",
		"Failed: NOPE-404 (save_issue)",
	} {
		if !strings.Contains(answer.Reply, want) {
			t.Fatalf("the reply reports %q: %q", want, answer.Reply)
		}
	}
	if answer.Nudge != "" || strings.Contains(answer.Reply, "do not have permission") {
		t.Fatalf("a run that wrote is not nudged, and the contrary sentence is rewritten: %+v", answer)
	}
	runtimeJSON[map[string]any](t, h.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/activities", worker: "w1", body: map[string]any{"type": "response", "body": answer.Reply}}, http.StatusOK)
	if bodies := workerResponses(t, h, task); len(bodies) != 1 || bodies[0] != answer.Reply {
		t.Fatalf("Flow's reply is posted as it is: %q", bodies)
	}
}

// A run that posts its response without asking for its last word gets the same checks when it posts it.
func TestWorkerRunResponseActivityIsChecked(t *testing.T) {
	h := newDelegationHarness(t)
	h.s.agent.WorkerSecret = testWorkerSecret
	target := requestJSON[domain.Issue](t, h.handler, http.MethodPost, "/api/issues", map[string]any{"title": "Old title", "teamId": h.teamID}, http.StatusCreated)

	run, token, task := workerOutcomeRun(t, h, "Rename "+target.Identifier+" and NOPE-404 to Renamed")
	callRunTool(t, h.handler, token, "save_issue", map[string]any{"id": target.Identifier, "title": "Renamed"}, nil)
	callRunTool(t, h.handler, token, "save_issue", map[string]any{"id": "NOPE-404", "title": "Renamed"}, nil)
	outcomes := h.s.agentRunLedger(t.Context(), run).outcomes
	if len(outcomes) != 2 || outcomes[0].Status != agentWriteSucceeded || outcomes[1].Status != agentWriteFailed || outcomes[1].Target != "NOPE-404" {
		t.Fatalf("the run's ledger: %+v", outcomes)
	}
	runtimeJSON[map[string]any](t, h.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/activities", worker: "w1", body: map[string]any{"type": "response", "body": "Both updates failed."}}, http.StatusOK)
	bodies := workerResponses(t, h, task)
	if len(bodies) != 1 || !strings.HasPrefix(bodies[0], "Correction from Flow: of 2 change(s), 1 succeeded, 1 failed") || !strings.Contains(bodies[0], "**Result from Flow:** 2 change(s) requested") {
		t.Fatalf("the posted response is corrected: %q", bodies)
	}

	idle, _, idleTask := workerOutcomeRun(t, h, "Move ENG-1 to the VLT team")
	runtimeJSON[map[string]any](t, h.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + idle.ID + "/activities", worker: "w1", body: map[string]any{"type": "response", "body": "I couldn't move ENG-1: you don't have permission."}}, http.StatusOK)
	if bodies := workerResponses(t, h, idleTask); len(bodies) != 1 || bodies[0] != agentNothingExecutedText(false) {
		t.Fatalf("a response reporting outcomes no write produced is Flow's statement: %q", bodies)
	}
}
