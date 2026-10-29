package main

import (
	"net/http"
	"strings"
	"testing"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// An issue delegated to the built-in agent runs on the worker: the delivery queues a delegation run (the issue moved
// to its first started state when it was delegated, as in-process), the run gets Flow's thread prompt and budgets,
// and a "done" claim without Coding is turned by the guard into a real coding session in the environment of the
// repository the issue names, reported with its state.
func TestWorkerDelegationRunsOnTheWorkerAndEntersCoding(t *testing.T) {
	h := newDelegationHarness(t, "acme/api", "acme/web")
	h.s.agent.WorkerSecret, h.s.agent.Runtime = testWorkerSecret, appconfig.AgentRuntimeWorker
	issue := requestJSON[domain.Issue](t, h.handler, http.MethodPost, "/api/issues", map[string]any{"title": "Add the multiply endpoint", "description": "Implement the multiply endpoint in acme/web, add unit tests and open a draft MR.", "teamId": h.teamID, "delegateId": h.agentUser}, http.StatusCreated)
	task, err := h.s.store.AgentTask(t.Context(), h.workspace, issue.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	h.s.deliverApplicationTask(t.Context(), task)
	claimed := runtimeJSON[struct {
		Runs []store.AgentRun `json:"runs"`
	}](t, h.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/claim", worker: "w1", body: map[string]any{"free": map[string]int{"chat": 1, "thread": 1}, "max": 1}}, http.StatusOK)
	if len(claimed.Runs) != 1 || claimed.Runs[0].Kind != store.AgentRunKindDelegation || claimed.Runs[0].SessionID != task.ID {
		t.Fatalf("claimed = %+v", claimed.Runs)
	}
	run := claimed.Runs[0]
	payload := runtimeJSON[workerThreadPayload](t, h.handler, runtimeCall{method: http.MethodGet, path: "/internal/agent-runtime/runs/" + run.ID + "/payload", worker: "w1"}, http.StatusOK)
	if payload.Thread == nil || !strings.Contains(payload.Context.System+payload.Context.LatestPrompt, "multiply") {
		t.Fatalf("payload = %+v", payload.Thread)
	}
	guard := func(request map[string]any) workerRunGuardResponse {
		return runtimeJSON[workerRunGuardResponse](t, h.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/guard", worker: "w1", body: request}, http.StatusOK)
	}
	if start := guard(map[string]any{"op": "start"}); !start.Enabled || !start.CodingDue {
		t.Fatalf("start = %+v", start)
	}
	guard(map[string]any{"op": "turn", "round": 0, "maxTurns": 26, "connectors": false})
	finish := guard(map[string]any{"op": "finish", "phase": "reply", "text": "I have implemented the endpoint and opened a draft MR."})
	if !finish.Transferred || !strings.Contains(finish.Reply, "Started a coding session for "+issue.Identifier) || strings.Contains(finish.Reply, "I have implemented") {
		t.Fatalf("finish = %+v", finish)
	}
	runtimeJSON[map[string]any](t, h.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/activities", worker: "w1", body: map[string]any{"activityId": "1:1:response", "type": "response", "body": finish.Reply}}, http.StatusOK)
	runtimeJSON[map[string]any](t, h.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/complete", worker: "w1", body: map[string]any{"status": "done"}}, http.StatusOK)
	task, _ = h.s.store.AgentTask(t.Context(), h.workspace, task.ID)
	if task.CodingSessionID == "" {
		t.Fatal("the delegation did not enter Coding")
	}
	session, err := h.s.store.CodingSession(t.Context(), h.workspace, task.CodingSessionID)
	if err != nil || session.EnvironmentID != h.environments[1].ID {
		t.Fatalf("coding session = %+v %v", session, err)
	}
	activities, _ := h.s.store.AgentActivities(t.Context(), h.workspace, task.ID, "")
	if last := activities[len(activities)-1]; last.Type != "response" || last.Body != finish.Reply {
		t.Fatalf("last activity = %+v", last)
	}
}
