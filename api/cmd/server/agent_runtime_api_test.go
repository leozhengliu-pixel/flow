package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

const testWorkerSecret = "agent-worker-secret-for-tests-0123456789"

type runtimeCall struct {
	method, path, worker, bearer string
	body                         any
	headers                      map[string]string
}

func runtimeDo(t *testing.T, handler http.Handler, call runtimeCall) (int, []byte, http.Header) {
	t.Helper()
	var body *bytes.Reader
	switch value := call.body.(type) {
	case nil:
		body = bytes.NewReader(nil)
	case []byte:
		body = bytes.NewReader(value)
	default:
		raw, _ := json.Marshal(value)
		body = bytes.NewReader(raw)
	}
	request := httptest.NewRequest(call.method, call.path, body)
	if call.body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if call.bearer != "" {
		request.Header.Set("Authorization", "Bearer "+call.bearer)
	}
	if call.worker != "" {
		request.Header.Set(agentRuntimeWorkerHeader, call.worker)
	}
	for key, value := range call.headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.Bytes(), recorder.Header()
}

func runtimeJSON[T any](t *testing.T, handler http.Handler, call runtimeCall, want int) T {
	t.Helper()
	if call.bearer == "" {
		call.bearer = testWorkerSecret
	}
	status, raw, _ := runtimeDo(t, handler, call)
	if status != want {
		t.Fatalf("%s %s = %d, want %d: %s", call.method, call.path, status, want, raw)
	}
	var result T
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatalf("%s %s: %v %s", call.method, call.path, err, raw)
		}
	}
	return result
}

// mcpWithToken sends one JSON-RPC request to /mcp with a bearer token.
func mcpWithToken(t *testing.T, handler http.Handler, token string, method string, params any, headers map[string]string) (int, map[string]any) {
	t.Helper()
	status, raw, _ := runtimeDo(t, handler, runtimeCall{method: http.MethodPost, path: "/mcp", bearer: token, body: map[string]any{"jsonrpc": "2.0", "id": 7, "method": method, "params": params}, headers: headers})
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	return status, result
}

// startTestRun enqueues a run, claims it as worker w1 and mints its token.
func startWorkerTestRun(t *testing.T, s *server, handler http.Handler, input store.AgentRunInput, principal agentRunPrincipal) (store.AgentRun, string) {
	t.Helper()
	input.Principal, _ = json.Marshal(principal)
	run, created, err := s.store.EnqueueAgentRun(t.Context(), input)
	if err != nil || !created {
		t.Fatalf("enqueue: %v", err)
	}
	lane := map[string]string{store.AgentRunKindChat: "chat", store.AgentRunKindThread: "thread", store.AgentRunKindDelegation: "delegation", store.AgentRunKindLoop: "loop"}[input.Kind]
	claimed := runtimeJSON[struct {
		Runs []store.AgentRun `json:"runs"`
	}](t, handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/claim", worker: "w1", body: map[string]any{"free": map[string]int{lane: 1}}}, 200)
	if len(claimed.Runs) != 1 || claimed.Runs[0].ID != run.ID {
		t.Fatalf("claimed %+v, want %s", claimed.Runs, run.ID)
	}
	minted := runtimeJSON[struct {
		Token     string `json:"token"`
		ExpiresAt int64  `json:"expiresAt"`
	}](t, handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/token", worker: "w1"}, 201)
	if !strings.HasPrefix(minted.Token, agentRunTokenPrefix) || minted.ExpiresAt <= time.Now().UnixMilli() {
		t.Fatalf("token = %+v", minted)
	}
	return claimed.Runs[0], minted.Token
}

func TestAgentRuntimeAPIIsOffWithoutASecretAndAuthenticatesWorkers(t *testing.T) {
	f := newAgentAttributionFixture(t)
	call := runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/claim", worker: "w1", bearer: testWorkerSecret, body: map[string]any{"free": map[string]int{"chat": 1}}}
	if status, _, _ := runtimeDo(t, f.handler, call); status != http.StatusNotFound {
		t.Fatalf("internal API without a worker secret = %d", status)
	}
	f.s.agent.WorkerSecret = testWorkerSecret
	for _, test := range []struct {
		bearer, worker string
		want           int
	}{{"", "w1", 401}, {"wrong-secret-wrong-secret-wrong-secret", "w1", 401}, {testWorkerSecret, "", 400}, {testWorkerSecret, "bad worker", 400}, {testWorkerSecret, "w1", 200}} {
		call.bearer, call.worker = test.bearer, test.worker
		if status, raw, _ := runtimeDo(t, f.handler, call); status != test.want {
			t.Errorf("bearer %q worker %q = %d %s, want %d", test.bearer, test.worker, status, raw, test.want)
		}
	}
	call.bearer, call.worker, call.body = testWorkerSecret, "w1", map[string]any{"free": map[string]int{"coding": 1}}
	if status, _, _ := runtimeDo(t, f.handler, call); status != http.StatusBadRequest {
		t.Fatalf("unknown lane accepted: %d", status)
	}
}

// A chat (user-mode) run calls /mcp with its token: writes read "Flow on behalf of <user>" like the in-process chat
// agent, the change set is recorded and can be undone, and the token dies with the run.
func TestAgentRunTokenActsLikeTheInProcessChatAgent(t *testing.T) {
	f := newAgentAttributionFixture(t)
	f.s.agent.WorkerSecret = testWorkerSecret
	viewer, team := f.data.Viewer.ID, f.data.Teams[0].ID
	run, token := startWorkerTestRun(t, f.s, f.handler, store.AgentRunInput{WorkspaceKey: f.data.Workspace.URLKey, SessionID: "agent_session_chat", Kind: store.AgentRunKindChat}, agentRunPrincipal{Mode: agentRunModeUser, UserID: viewer})

	_, listed := mcpWithToken(t, f.handler, token, "tools/list", map[string]any{}, nil)
	names := map[string]bool{}
	for _, tool := range listed["result"].(map[string]any)["tools"].([]any) {
		names[tool.(map[string]any)["name"].(string)] = true
	}
	if !names["save_issue"] || !names["count_issues"] || !names["delete_issue"] {
		t.Fatalf("run token tools/list = %v", names)
	}

	call := func(name string, args map[string]any, headers map[string]string) map[string]any {
		t.Helper()
		_, response := mcpWithToken(t, f.handler, token, "tools/call", map[string]any{"name": name, "arguments": args}, headers)
		result, ok := response["result"].(map[string]any)
		if !ok || result["isError"] == true {
			t.Fatalf("%s: %v", name, response)
		}
		return result["structuredContent"].(map[string]any)
	}
	created := call("save_issue", map[string]any{"team": team, "title": "Run token issue"}, nil)
	change, _ := created["change"].(map[string]any)
	if change == nil || change["action"] != "created" || change["entityType"] != "issue" || change["undoable"] != true || change["id"] != run.ID+":1" {
		t.Fatalf("create change = %v", created)
	}
	issue := f.issue(mustJSON(t, created))
	f.assertAgent("run token issue creation", issue.BotActor, viewer, issue.Creator)
	updated := call("save_issue", map[string]any{"id": issue.ID, "title": "Run token issue, renamed"}, nil)
	if change, _ := updated["change"].(map[string]any); change == nil || change["action"] != "updated" || change["seq"] != float64(2) {
		t.Fatalf("update change = %v", updated)
	}
	for _, event := range issueHistory(f, issue.ID) {
		f.assertAgent("run token history "+event.Type, event.BotActor, viewer, event.Actor)
	}
	if current, _ := f.s.store.AgentRun(t.Context(), run.ID); !current.Wrote {
		t.Fatal("a write did not mark the run")
	}
	changes := runtimeJSON[struct {
		Items []struct {
			Ref agentRunChangeRef `json:"ref"`
		} `json:"items"`
	}](t, f.handler, runtimeCall{method: http.MethodGet, path: "/internal/agent-runtime/runs/" + run.ID + "/changes", worker: "w1"}, 200)
	if len(changes.Items) != 2 || changes.Items[1].Ref.Action != "updated" {
		t.Fatalf("changes = %+v", changes.Items)
	}

	// Undo the rename only, then everything (the create goes to the trash).
	// Workers never see the principal: read the run as stored.
	run, err := f.s.store.AgentRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	if applied, err := f.s.applyAgentRunChanges(request, run, []int64{2}, true); err != nil || len(applied) != 1 || applied[0].Change.State != "undone" {
		t.Fatalf("undo rename: %+v %v", applied, err)
	}
	if current, err := f.s.store.IssueRecord(t.Context(), f.data.Workspace.URLKey, issue.ID); err != nil || current.Title != "Run token issue" {
		t.Fatalf("title after undo = %q %v", current.Title, err)
	}
	if _, err := f.s.applyAgentRunChanges(request, run, nil, true); err != nil {
		t.Fatalf("undo all: %v", err)
	}
	if _, err := f.s.store.IssueRecord(t.Context(), f.data.Workspace.URLKey, issue.ID); err == nil {
		t.Fatal("undoing the create left the issue")
	}

	// A retried write with the same Idempotency-Key returns the first result and writes once.
	key := map[string]string{"Idempotency-Key": run.ID + ":toolu_1"}
	first := call("save_issue", map[string]any{"team": team, "title": "Idempotent issue"}, key)
	second := call("save_issue", map[string]any{"team": team, "title": "Idempotent issue"}, key)
	if first["id"] != second["id"] {
		t.Fatalf("retried write created a second issue: %v %v", first["id"], second["id"])
	}

	// Batch deletes record one undoable change per item.
	one := call("save_issue", map[string]any{"team": team, "title": "Batch run one"}, nil)
	two := call("save_issue", map[string]any{"team": team, "title": "Batch run two"}, nil)
	deleted := call("delete_issue", map[string]any{"ids": []string{one["id"].(string), two["id"].(string)}}, nil)
	if refs, _ := deleted["changes"].([]any); len(refs) != 2 || deleted["deleted"] != float64(2) {
		t.Fatalf("batch delete = %v", deleted)
	}

	// The token dies with the run.
	runtimeJSON[store.AgentRun](t, f.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/complete", worker: "w1", body: map[string]any{"status": "done", "usage": map[string]any{"inputTokens": 10}}}, 200)
	if status, _ := mcpWithToken(t, f.handler, token, "tools/list", map[string]any{}, nil); status != http.StatusUnauthorized {
		t.Fatalf("token of a finished run = %d", status)
	}
}

// A tool allowlist limits what the token can list and call; read-only /mcp refuses writes.
func TestAgentRunTokenToolAllowlist(t *testing.T) {
	f := newAgentAttributionFixture(t)
	f.s.agent.WorkerSecret = testWorkerSecret
	_, token := startWorkerTestRun(t, f.s, f.handler, store.AgentRunInput{WorkspaceKey: f.data.Workspace.URLKey, SessionID: "agent_session_allow", Kind: store.AgentRunKindChat}, agentRunPrincipal{Mode: agentRunModeUser, UserID: f.data.Viewer.ID, Tools: []string{"get_workspace", "save_issue"}})
	_, listed := mcpWithToken(t, f.handler, token, "tools/list", map[string]any{}, nil)
	if tools := listed["result"].(map[string]any)["tools"].([]any); len(tools) != 2 {
		t.Fatalf("allowlisted tools/list = %v", tools)
	}
	_, response := mcpWithToken(t, f.handler, token, "tools/call", map[string]any{"name": "list_teams", "arguments": map[string]any{}}, nil)
	if message := mcpToolErrorText(t, response); !strings.Contains(message, "not available to this agent run") {
		t.Fatalf("tool outside the allowlist: %s", message)
	}
	status, raw, _ := runtimeDo(t, f.handler, runtimeCall{method: http.MethodPost, path: "/mcp/readonly", bearer: token, body: map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "save_issue", "arguments": map[string]any{"team": f.data.Teams[0].ID, "title": "Readonly"}}}})
	if status != 200 || !strings.Contains(string(raw), "not available to this agent run") {
		t.Fatalf("readonly write = %d %s", status, raw)
	}
	if status, _ := mcpWithToken(t, f.handler, agentRunTokenPrefix+"forged", "tools/list", map[string]any{}, nil); status != http.StatusUnauthorized {
		t.Fatalf("forged token = %d", status)
	}
}

// A delegation (app-mode) run: the payload carries the rendered prompt context, activities land in the session like
// the in-process agent's, writes read "Flow on behalf of <invoker>" as the app user, and state round-trips.
func TestAgentRuntimeDelegationRunEndToEnd(t *testing.T) {
	f := newAgentAttributionFixture(t)
	f.s.agent.WorkerSecret = testWorkerSecret
	team := f.data.Teams[0].ID
	referenced := requestJSON[domain.Issue](t, f.handler, "POST", "/api/issues", map[string]any{"title": "Referenced background", "teamId": team}, 201)
	delegated := requestJSON[domain.Issue](t, f.handler, "POST", "/api/issues", map[string]any{"title": "Follow up on " + referenced.Identifier, "teamId": team, "delegateId": f.app.UserID}, 201)
	task, err := f.s.store.AgentTask(t.Context(), f.data.Workspace.URLKey, delegated.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	principal := agentRunPrincipal{Mode: agentRunModeApp, UserID: f.data.Viewer.ID, AppUserID: f.app.UserID, TeamID: team}
	run, token := startWorkerTestRun(t, f.s, f.handler, store.AgentRunInput{WorkspaceKey: f.data.Workspace.URLKey, SessionID: task.ID, Kind: store.AgentRunKindDelegation}, principal)
	base := "/internal/agent-runtime/runs/" + run.ID

	// Another worker cannot touch the run; a run token cannot mint tokens.
	if status, _, _ := runtimeDo(t, f.handler, runtimeCall{method: http.MethodGet, path: base + "/payload", worker: "w2", bearer: testWorkerSecret}); status != http.StatusConflict {
		t.Fatalf("foreign worker payload = %d", status)
	}
	if status, _, _ := runtimeDo(t, f.handler, runtimeCall{method: http.MethodPost, path: base + "/token", bearer: token}); status != http.StatusForbidden {
		t.Fatalf("token minting with a run token = %d", status)
	}

	payload := runtimeJSON[struct {
		Run       store.AgentRun         `json:"run"`
		Principal map[string]any         `json:"principal"`
		Session   domain.AgentTask       `json:"session"`
		Context   *agentRunPromptContext `json:"context"`
	}](t, f.handler, runtimeCall{method: http.MethodGet, path: base + "/payload", worker: "w1"}, 200)
	if payload.Context == nil || payload.Session.ID != task.ID || payload.Principal["mode"] != agentRunModeApp {
		t.Fatalf("payload = %+v", payload)
	}
	if !strings.Contains(payload.Context.System, "comment thread") || len(payload.Context.Messages) == 0 || !strings.Contains(payload.Context.Messages[0].Content, "delegated issue "+delegated.Identifier) {
		t.Fatalf("prompt context = %+v", payload.Context)
	}
	if len(payload.Context.ReferencedIssues) != 1 || payload.Context.ReferencedIssues[0] != referenced.Identifier || !strings.Contains(payload.Context.System, referenced.Identifier) {
		t.Fatalf("referenced issues = %v", payload.Context.ReferencedIssues)
	}

	beat := runtimeJSON[map[string]any](t, f.handler, runtimeCall{method: http.MethodPost, path: base + "/heartbeat", worker: "w1"}, 200)
	if beat["cancel"] != false {
		t.Fatalf("heartbeat = %v", beat)
	}

	// Activities: idempotent by activityId, posted as the app user, visible through the session API.
	thought := map[string]any{"activityId": "a1", "type": "thought", "body": "Looking at the issue", "ephemeral": true}
	first := runtimeJSON[map[string]any](t, f.handler, runtimeCall{method: http.MethodPost, path: base + "/activities", worker: "w1", body: thought}, 200)
	again := runtimeJSON[map[string]any](t, f.handler, runtimeCall{method: http.MethodPost, path: base + "/activities", worker: "w1", body: thought}, 200)
	if first["activityId"] == nil || first["activityId"] != again["activityId"] {
		t.Fatalf("activity replay = %v / %v", first, again)
	}
	runtimeJSON[map[string]any](t, f.handler, runtimeCall{method: http.MethodPost, path: base + "/activities", bearer: token, body: map[string]any{"type": "action", "body": "save_comment", "content": map[string]any{"action": "save_comment", "parameter": delegated.Identifier}}}, 200)
	if status, _, _ := runtimeDo(t, f.handler, runtimeCall{method: http.MethodPost, path: base + "/activities", worker: "w1", bearer: testWorkerSecret, body: map[string]any{"type": "prompt", "body": "impersonating"}}); status != http.StatusBadRequest {
		t.Fatalf("prompt activity from a worker = %d", status)
	}

	// The run's writes are the built-in app's, on behalf of the person who delegated.
	_, response := mcpWithToken(t, f.handler, token, "tools/call", map[string]any{"name": "save_issue", "arguments": map[string]any{"id": delegated.ID, "priority": 2}}, nil)
	if result := response["result"].(map[string]any); result["isError"] == true {
		t.Fatalf("app-mode write: %v", response)
	}
	for _, event := range issueHistory(f, delegated.ID) {
		if event.Type == "issue.updated" && event.BotActor != nil {
			f.assertAgent("app-mode run history", event.BotActor, f.app.UserID, event.Actor)
		}
	}

	// Session state for a suspended run.
	if status, _, _ := runtimeDo(t, f.handler, runtimeCall{method: http.MethodGet, path: base + "/state", worker: "w1", bearer: testWorkerSecret}); status != http.StatusNotFound {
		t.Fatalf("empty state = %d", status)
	}
	if status, raw, _ := runtimeDo(t, f.handler, runtimeCall{method: http.MethodPut, path: base + "/state", worker: "w1", bearer: testWorkerSecret, body: []byte(`{"runState":"serialized"}`), headers: map[string]string{"Content-Type": "application/json"}}); status != http.StatusNoContent {
		t.Fatalf("put state = %d %s", status, raw)
	}
	status, raw, headers := runtimeDo(t, f.handler, runtimeCall{method: http.MethodGet, path: base + "/state", worker: "w1", bearer: testWorkerSecret})
	if status != 200 || string(raw) != `{"runState":"serialized"}` || headers.Get("Content-Type") != "application/json" || headers.Get("Flow-State-Run-Id") != run.ID {
		t.Fatalf("get state = %d %s %v", status, raw, headers)
	}

	runtimeJSON[map[string]any](t, f.handler, runtimeCall{method: http.MethodPost, path: base + "/activities", worker: "w1", body: map[string]any{"type": "response", "body": "Done: set the priority."}}, 200)
	session := requestJSON[struct {
		Session    domain.AgentTask       `json:"session"`
		Activities []domain.AgentActivity `json:"activities"`
	}](t, f.handler, "GET", "/api/agent-tasks/"+task.ID, nil, 200)
	if session.Session.Status != "complete" || len(session.Activities) != 2 { // the ephemeral thought was replaced by the action
		t.Fatalf("session after the response = %+v", session)
	}
	activities, err := f.s.store.AgentActivities(t.Context(), f.data.Workspace.URLKey, task.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	last := activities[len(activities)-1]
	if last.Type != "response" || last.ActorID != f.app.UserID {
		t.Fatalf("last activity = %+v", last)
	}
	finished := runtimeJSON[store.AgentRun](t, f.handler, runtimeCall{method: http.MethodPost, path: base + "/complete", worker: "w1", body: map[string]any{"status": "done"}}, 200)
	if finished.Status != store.AgentRunDone {
		t.Fatalf("finished = %+v", finished)
	}
	if status, _, _ := runtimeDo(t, f.handler, runtimeCall{method: http.MethodPost, path: base + "/heartbeat", worker: "w1", bearer: testWorkerSecret}); status != http.StatusConflict {
		t.Fatalf("heartbeat after completion = %d", status)
	}
}

// A heartbeat reports a stop, and the reconciler interrupts a run whose worker vanished after writing.
func TestAgentRuntimeCancelAndReconcile(t *testing.T) {
	f := newAgentAttributionFixture(t)
	f.s.agent.WorkerSecret = testWorkerSecret
	issue := requestJSON[domain.Issue](t, f.handler, "POST", "/api/issues", map[string]any{"title": "Reconciled", "teamId": f.data.Teams[0].ID, "delegateId": f.app.UserID}, 201)
	task, err := f.s.store.AgentTask(t.Context(), f.data.Workspace.URLKey, issue.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	principal := agentRunPrincipal{Mode: agentRunModeApp, UserID: f.data.Viewer.ID, AppUserID: f.app.UserID, TeamID: f.data.Teams[0].ID}
	run, _ := startWorkerTestRun(t, f.s, f.handler, store.AgentRunInput{WorkspaceKey: f.data.Workspace.URLKey, SessionID: task.ID, Kind: store.AgentRunKindDelegation}, principal)
	if _, err := f.s.store.CancelAgentRun(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	beat := runtimeJSON[map[string]any](t, f.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/heartbeat", worker: "w1"}, 200)
	if beat["cancel"] != true {
		t.Fatalf("heartbeat after cancel = %v", beat)
	}

	second, _ := startWorkerTestRun(t, f.s, f.handler, store.AgentRunInput{WorkspaceKey: f.data.Workspace.URLKey, SessionID: task.ID, Kind: store.AgentRunKindDelegation, IdempotencyKey: "second"}, principal)
	if err := f.s.store.MarkAgentRunWrote(t.Context(), second.ID); err != nil {
		t.Fatal(err)
	}
	requeued, interrupted := f.s.reconcileAgentRuns(context.Background(), time.Now().Add(5*time.Minute))
	if requeued != 0 || interrupted != 1 {
		t.Fatalf("reconcile = %d requeued, %d interrupted", requeued, interrupted)
	}
	if current, _ := f.s.store.AgentRun(t.Context(), second.ID); current.Status != store.AgentRunInterrupted {
		t.Fatalf("second run = %+v", current)
	}
	if current, _ := f.s.store.AgentRun(t.Context(), run.ID); current.Status != store.AgentRunCanceled {
		t.Fatalf("canceled run = %+v", current)
	}
	session, err := f.s.store.AgentTask(t.Context(), f.data.Workspace.URLKey, task.ID)
	if err != nil || session.Status != "error" {
		t.Fatalf("session after interruption = %+v %v", session, err)
	}
}

// A claim waits for work and wakes when a run is enqueued.
func TestAgentRuntimeClaimLongPolls(t *testing.T) {
	f := newAgentAttributionFixture(t)
	f.s.agent.WorkerSecret = testWorkerSecret
	principal, _ := json.Marshal(agentRunPrincipal{Mode: agentRunModeUser, UserID: f.data.Viewer.ID})
	go func() {
		time.Sleep(100 * time.Millisecond)
		_, _, _ = f.s.store.EnqueueAgentRun(context.Background(), store.AgentRunInput{WorkspaceKey: f.data.Workspace.URLKey, SessionID: "late", Kind: store.AgentRunKindChat, Principal: principal})
	}()
	started := time.Now()
	claimed := runtimeJSON[struct {
		Runs []store.AgentRun `json:"runs"`
	}](t, f.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/claim", worker: "w1", body: map[string]any{"free": map[string]int{"chat": 1}, "waitMs": 5000}}, 200)
	if len(claimed.Runs) != 1 || claimed.Runs[0].SessionID != "late" || time.Since(started) > 4*time.Second {
		t.Fatalf("long poll = %+v after %s", claimed.Runs, time.Since(started))
	}
	empty := runtimeJSON[struct {
		Runs []store.AgentRun `json:"runs"`
	}](t, f.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/claim", worker: "w1", body: map[string]any{"free": map[string]int{"chat": 1}, "waitMs": 50}}, 200)
	if len(empty.Runs) != 0 {
		t.Fatalf("empty claim = %+v", empty.Runs)
	}
}
