package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// A worker run's calls reach whichever API server the load balancer picks. These tests play the agent worker
// against two API servers sharing one database and Redis (newPeerServers), sending its /mcp calls, guard
// operations and activity posts to both — in turn, at once and repeated — and check that the run's ledger, its tool
// budget, its guard and its chat reply come out as with one server.

// newWorkerChatPeers is newWorkerChatFixture on two API servers sharing one database and Redis.
func newWorkerChatPeers(t *testing.T) [2]workerChatFixture {
	t.Helper()
	peers := newPeerServers(t)
	var fixtures [2]workerChatFixture
	for index, api := range []*httptest.Server{peers.first, peers.second} {
		s := peers.servers[index]
		s.authDisabled = true
		s.agent.Enabled, s.agent.ToolsEnabled, s.agent.WriteTools = true, true, true
		s.agent.WorkerSecret, s.agent.Runtime = testWorkerSecret, appconfig.AgentRuntimeWorker
		fixtures[index] = workerChatFixture{agentAttributionFixture: agentAttributionFixture{t: t, s: s, handler: api.Config.Handler, data: peers.stores[index].Bootstrap()}, api: api}
	}
	// Streams still following a reply end with their connections (before the servers close).
	t.Cleanup(func() {
		peers.first.CloseClientConnections()
		peers.second.CloseClientConnections()
	})
	return fixtures
}

// peerRuntimeCall is runtimeDo for goroutines (no t).
func peerRuntimeCall(handler http.Handler, call runtimeCall) (int, []byte) {
	raw, _ := json.Marshal(call.body)
	request := httptest.NewRequest(call.method, call.path, bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+firstNonEmpty(call.bearer, testWorkerSecret))
	if call.worker != "" {
		request.Header.Set(agentRuntimeWorkerHeader, call.worker)
	}
	for key, value := range call.headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.Bytes()
}

// peerTool calls a tool with a run token (key: its Idempotency-Key) and returns the tool result.
func peerTool(handler http.Handler, token, name string, args map[string]any, key string) (map[string]any, error) {
	headers := map[string]string{}
	if key != "" {
		headers["Idempotency-Key"] = key
	}
	status, raw := peerRuntimeCall(handler, runtimeCall{method: http.MethodPost, path: "/mcp", bearer: token, headers: headers, body: map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}}})
	var response struct {
		Result map[string]any `json:"result"`
	}
	if status != http.StatusOK || json.Unmarshal(raw, &response) != nil || response.Result == nil {
		return nil, fmt.Errorf("%s: %d %s", name, status, raw)
	}
	return response.Result, nil
}

// inParallel runs work for each index at once and fails on the first error.
func inParallel(t *testing.T, count int, work func(index int) error) {
	t.Helper()
	var group sync.WaitGroup
	errs := make([]error, count)
	for index := range count {
		group.Add(1)
		go func() {
			defer group.Done()
			errs[index] = work(index)
		}()
	}
	group.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

// A chat run's parallel /mcp calls, each turn's calls split between two API servers and sent at once: every write
// keeps its ledger entry (exact counts), the tool budget stops the run at its limit across both servers, a retried
// call (same Idempotency-Key) on the other server is answered from the first without a second entry or call, and
// the decision at the last word reads the same ledger on either server.
func TestWorkerRunLedgerAndBudgetHoldAcrossAPIServers(t *testing.T) {
	peers := newWorkerChatPeers(t)
	a, b := peers[0], peers[1]
	handlers := []http.Handler{a.handler, b.handler}
	ctx := t.Context()
	issues := make([]domain.Issue, 6)
	for index := range issues {
		issues[index] = requestJSON[domain.Issue](t, a.handler, http.MethodPost, "/api/issues", map[string]any{"title": fmt.Sprintf("Issue %d", index), "teamId": a.data.Teams[0].ID}, http.StatusCreated)
	}
	info := workerChatRunInfo{MessageID: workerChatMessagePrefix + "peer_ledger", MaxCalls: 16, MaxTurns: 10, Request: "Rename the issues"}
	payload, _ := json.Marshal(map[string]any{"chat": info})
	run, token := startWorkerTestRun(t, a.s, a.handler, store.AgentRunInput{WorkspaceKey: "test-workspace", SessionID: "agent_session_peer_ledger", Kind: store.AgentRunKindChat, Payload: payload}, agentRunPrincipal{Mode: agentRunModeUser, UserID: a.data.Viewer.ID})

	// Turn 1: six renames on the servers in turn (two SQLite stores on one file cannot commit issue writes at once;
	// MySQL and PostgreSQL can, and the ledger rows are the same), then six writes that fail, at once on both.
	for index, issue := range issues {
		if result, err := peerTool(handlers[index%2], token, "save_issue", map[string]any{"id": issue.Identifier, "title": fmt.Sprintf("Renamed %d", index)}, ""); err != nil || result["isError"] == true {
			t.Fatalf("save_issue %s: %v %v", issue.Identifier, result, err)
		}
	}
	inParallel(t, 6, func(index int) error {
		target := fmt.Sprintf("NOPE-%d", 900+index)
		result, err := peerTool(handlers[index%2], token, "save_issue", map[string]any{"id": target, "title": "Renamed"}, "")
		if err == nil && result["isError"] != true {
			err = fmt.Errorf("save_issue %s: %v", target, result)
		}
		return err
	})
	// A retried write: the same Idempotency-Key on the other server gets the first result.
	first, err := peerTool(a.handler, token, "save_issue", map[string]any{"id": issues[0].Identifier, "title": "Renamed again"}, run.ID+":retry")
	if err != nil || first["isError"] == true {
		t.Fatalf("keyed write: %v %v", first, err)
	}
	retried, err := peerTool(b.handler, token, "save_issue", map[string]any{"id": issues[0].Identifier, "title": "Renamed again"}, run.ID+":retry")
	if err != nil || runToolText(retried) != runToolText(first) {
		t.Fatalf("retried write: %v %v", retried, err)
	}
	// Turn 2: eight reads at once, three left in the budget of sixteen calls.
	var mu sync.Mutex
	refused := 0
	countRefused := func(result map[string]any) {
		if result["isError"] == true && strings.Contains(runToolText(result), "tool-call budget") {
			mu.Lock()
			refused++
			mu.Unlock()
		}
	}
	inParallel(t, 8, func(index int) error {
		result, err := peerTool(handlers[index%2], token, "list_issues", map[string]any{"limit": index + 1}, "")
		if err == nil {
			countRefused(result)
		}
		return err
	})
	if refused != 5 {
		t.Fatalf("reads refused by the budget = %d, want 5", refused)
	}
	// Turn 3: writes over the budget are not run, and the ledger says so.
	inParallel(t, 2, func(index int) error {
		result, err := peerTool(handlers[index%2], token, "save_issue", map[string]any{"id": issues[index+1].Identifier, "title": "Over budget"}, "")
		if err == nil {
			countRefused(result)
		}
		return err
	})
	if refused != 7 {
		t.Fatalf("calls refused by the budget = %d, want 7", refused)
	}
	for index, peer := range peers {
		if budget := peer.s.workerChatBudget(ctx, run); budget.calls != 16 || budget.stop != agentBudgetCalls {
			t.Fatalf("server %d: budget calls %d stop %q", index, budget.calls, budget.stop)
		}
		ledger := peer.s.agentRunLedger(ctx, run)
		succeeded, failed, notRun := ledger.counts()
		if len(ledger.outcomes) != 15 || succeeded != 7 || failed != 6 || notRun != 2 {
			t.Fatalf("server %d: ledger %d entries: %d succeeded, %d failed, %d not run: %+v", index, len(ledger.outcomes), succeeded, failed, notRun, ledger.outcomes)
		}
	}
	for index := range issues {
		stored := requestJSON[domain.Issue](t, b.handler, http.MethodGet, "/api/issue-records/"+issues[index].ID, nil, http.StatusOK)
		if want := fmt.Sprintf("Renamed %d", index); index > 0 && stored.Title != want || index == 0 && stored.Title != "Renamed again" {
			t.Fatalf("issue %d title = %q", index, stored.Title)
		}
	}
	// The last word, asked of either server, reads the whole ledger.
	finish := runtimeJSON[workerRunGuardResponse](t, b.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/guard", worker: "w1", body: map[string]any{"op": "finish", "phase": "reply", "text": "All issues were renamed."}}, http.StatusOK)
	if !strings.Contains(finish.Reply, "15 change(s) requested: 13 executed (7 succeeded, 6 failed), 2 not executed") {
		t.Fatalf("last word = %+v", finish)
	}
}

// A thread run's guard takes the before and after of parallel connector calls on both servers at once: every call
// is counted and its result kept for repeats.
func TestWorkerRunGuardCountsParallelCallsAcrossAPIServers(t *testing.T) {
	peers := newWorkerChatPeers(t)
	a, b := peers[0], peers[1]
	run, _ := startWorkerTestRun(t, a.s, a.handler, store.AgentRunInput{WorkspaceKey: "test-workspace", SessionID: "agent_session_peer_guard", Kind: store.AgentRunKindThread}, agentRunPrincipal{Mode: agentRunModeUser, UserID: a.data.Viewer.ID})
	raw, _ := json.Marshal(workerRunGuardState{DecisionRound: -1})
	if err := a.s.store.ReplaceAgentRunState(t.Context(), run, workerRunGuardKey, raw); err != nil {
		t.Fatal(err)
	}
	handlers := []http.Handler{a.handler, b.handler}
	guard := func(index int, body map[string]any) (workerRunGuardResponse, error) {
		status, raw := peerRuntimeCall(handlers[index%2], runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/guard", worker: "w1", body: body})
		var response workerRunGuardResponse
		if status != http.StatusOK || json.Unmarshal(raw, &response) != nil {
			return response, fmt.Errorf("guard: %d %s", status, raw)
		}
		return response, nil
	}
	inParallel(t, 12, func(index int) error {
		_, err := guard(index, map[string]any{"op": "after", "tool": fmt.Sprintf("external_search_%d", index), "arguments": `{"q":1}`, "content": "", "failed": true})
		return err
	})
	state, found := b.s.workerRunGuardStateOf(t.Context(), run.ID)
	if !found || state.Calls != 12 || state.NoProgress != 12 || len(state.Results) != 12 {
		t.Fatalf("guard after parallel calls: calls %d, no progress %d, results %d", state.Calls, state.NoProgress, len(state.Results))
	}
	// Repeats of those calls, at once on both servers: each is answered from the kept result.
	inParallel(t, 6, func(index int) error {
		response, err := guard(index, map[string]any{"op": "before", "tool": fmt.Sprintf("external_search_%d", index), "arguments": `{"q":1}`})
		if err == nil && !response.Handled {
			err = fmt.Errorf("repeat %d was not answered from its result: %+v", index, response)
		}
		return err
	})
	if state, _ := a.s.workerRunGuardStateOf(t.Context(), run.ID); state.NoProgress != 18 || state.Calls != 12 {
		t.Fatalf("guard after repeats: calls %d, no progress %d", state.Calls, state.NoProgress)
	}
}

// workerChatScript is a chat run's activities in order (seq = index + 1).
var workerChatScript = []map[string]any{
	{"type": "thought", "body": "Working…"},
	{"type": "text.delta", "body": "Let me check "},
	{"type": "text.delta", "body": "the backlog."},
	{"type": "action", "callId": "c1", "content": map[string]any{"action": "list_issues", "parameter": map[string]any{"limit": 5}}},
	{"type": "action.result", "callId": "c1", "content": map[string]any{"action": "list_issues", "result": `{"items":[]}`}},
	{"type": "text.delta", "body": "Now the projects and cycles. "},
	{"type": "action", "callId": "c2", "content": map[string]any{"action": "list_projects", "parameter": map[string]any{}}},
	{"type": "action", "callId": "c3", "content": map[string]any{"action": "list_cycles", "parameter": map[string]any{}}},
	{"type": "action.result", "callId": "c3", "content": map[string]any{"action": "list_cycles", "result": `{"items":[]}`}},
	{"type": "action.result", "callId": "c2", "content": map[string]any{"action": "list_projects", "result": "no projects"}},
	{"type": "text.delta", "body": "The backlog "},
	{"type": "text.delta", "body": "is empty."},
	{"type": "response", "body": "The backlog is empty."},
}

func workerChatScriptPost(seq int) map[string]any {
	activity := map[string]any{"seq": seq}
	for key, value := range workerChatScript[seq-1] {
		activity[key] = value
	}
	return activity
}

// normalizedWorkerReply is a finished reply without what differs between two replies (ids, times).
func normalizedWorkerReply(message domain.AgentMessage) string {
	id := message.ID
	message.CreatedAt, message.UpdatedAt, message.DurationMS = time.Time{}, nil, 0
	raw, _ := json.Marshal(message)
	return strings.ReplaceAll(string(raw), id, "MESSAGE")
}

// startPeerReply sends a chat message on server f and claims its run there.
func startPeerReply(t *testing.T, f workerChatFixture, message string) (string, store.AgentRun) {
	t.Helper()
	events := f.stream("/api/agent/sessions/stream", fmt.Sprintf(`{"message":%q,"location":"page"}`, message))
	sessionID := nextEvent(t, events, "session.started").Session.ID
	return sessionID, f.mustClaim(t)
}

// A chat reply whose run posts to two API servers — in turn, two posts at once, repeated posts (a retry on the other
// server), a post ahead of the one before it — is the reply the same posts make on one server in order.
func TestWorkerChatReplyIsTheSameWhicheverServersTakeThePosts(t *testing.T) {
	peers := newWorkerChatPeers(t)
	a, b := peers[0], peers[1]
	post := func(f workerChatFixture, run store.AgentRun, seq int) error {
		status, raw := peerRuntimeCall(f.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/activities", worker: "w1", body: workerChatScriptPost(seq)})
		if status != http.StatusOK {
			return fmt.Errorf("post %d: %d %s", seq, status, raw)
		}
		return nil
	}
	mustPost := func(f workerChatFixture, run store.AgentRun, seq int) {
		t.Helper()
		if err := post(f, run, seq); err != nil {
			t.Fatal(err)
		}
	}

	// One server, in order.
	alone, aloneRun := startPeerReply(t, a, "How many issues are in the backlog?")
	for seq := 1; seq <= len(workerChatScript); seq++ {
		mustPost(a, aloneRun, seq)
	}
	a.complete(t, aloneRun, "done")
	want := a.waitReply(t, alone, func(message domain.AgentMessage) bool { return message.Status == "" })

	// Two servers.
	shared, run := startPeerReply(t, a, "How many issues are in the backlog?")
	mustPost(b, run, 1)
	inParallel(t, 2, func(index int) error { return post(peers[index], run, 2+index) }) // 2 and 3 at once
	mustPost(a, run, 2)                                                                 // a retry of 2 on the other server
	mustPost(b, run, 4)
	mustPost(a, run, 6) // ahead of 5: waits for it
	if live := b.reply(t, shared); live.WorkerCursor == nil || live.WorkerCursor.Seq != 4 || toolPart(live.Parts, "c1") == nil || toolPart(live.Parts, "c1").ToolCall.Status != "running" {
		t.Fatalf("reply with 6 ahead of 5 = cursor %+v parts %+v", live.WorkerCursor, live.Parts)
	}
	mustPost(b, run, 5)
	if live := a.reply(t, shared); live.WorkerCursor == nil || live.WorkerCursor.Seq != 6 || toolPart(live.Parts, "c1").ToolCall.Status != "completed" {
		t.Fatalf("reply once 5 arrived = cursor %+v parts %+v", live.WorkerCursor, live.Parts)
	}
	inParallel(t, 4, func(index int) error { return post(peers[index%2], run, 7+index) }) // 7–10 at once
	inParallel(t, 3, func(index int) error { return post(peers[(index+1)%2], run, []int{9, 11, 12}[index]) })
	// The response and its retry, at once on both servers.
	answers := make([]map[string]any, 2)
	inParallel(t, 2, func(index int) error {
		status, raw := peerRuntimeCall(peers[index].handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/activities", worker: "w1", body: workerChatScriptPost(13)})
		if status != http.StatusOK {
			return fmt.Errorf("response: %d %s", status, raw)
		}
		return json.Unmarshal(raw, &answers[index])
	})
	if answers[0]["settled"] != true || answers[1]["settled"] != true {
		t.Fatalf("response answers = %v", answers)
	}
	// Activities after the end change nothing, on either server.
	for index, peer := range peers {
		late := map[string]any{"seq": len(workerChatScript) + 1 + index, "type": "text.delta", "body": "late"}
		if answer := peer.post(t, run, late); answer["settled"] != true {
			t.Fatalf("late post answer = %v", answer)
		}
		mustPost(peer, run, 12)
	}
	b.complete(t, run, "done")
	got := b.waitReply(t, shared, func(message domain.AgentMessage) bool { return message.Status == "" })
	if normalizedWorkerReply(got) != normalizedWorkerReply(want) {
		t.Fatalf("reply on two servers:\n%s\nreply on one server:\n%s", normalizedWorkerReply(got), normalizedWorkerReply(want))
	}
	if got.Content != "The backlog is empty." || got.WorkerCursor != nil || len(got.Parts) != 6 {
		t.Fatalf("finished reply = %+v", got)
	}
	if reloaded := a.reply(t, shared); normalizedWorkerReply(reloaded) != normalizedWorkerReply(want) {
		t.Fatalf("the reply on the first server: %+v", reloaded)
	}
}

// Held calls posted on one server and posted again on the other wait under one approval each; the person's
// decisions, given through either server, resume the reply.
func TestWorkerChatHeldCallsAcrossAPIServers(t *testing.T) {
	peers := newWorkerChatPeers(t)
	a, b := peers[0], peers[1]
	sessionID, run := startPeerReply(t, a, "Deploy and tag the release")
	held := map[string]any{"type": "elicitation", "seq": 1, "elicitation": map[string]any{"kind": "approval", "body": "Run tools?", "calls": []map[string]any{
		{"callId": "c1", "name": "external_deploy", "arguments": `{"env":"prod"}`},
		{"callId": "c2", "name": "external_tag", "arguments": `{}`},
	}}}
	a.post(t, run, held)
	first := a.reply(t, sessionID)
	b.post(t, run, held)
	again := b.reply(t, sessionID)
	if normalizedWorkerReply(again) != normalizedWorkerReply(first) || toolPart(again.Parts, "c1") == nil || toolPart(again.Parts, "c2") == nil {
		t.Fatalf("held calls after the repeated post:\n%+v\nbefore:\n%+v", again.Parts, first.Parts)
	}
	deploy, tag := toolPart(again.Parts, "c1").ToolCall.ApprovalID, toolPart(again.Parts, "c2").ToolCall.ApprovalID
	if deploy == "" || tag == "" || deploy == tag {
		t.Fatalf("approvals = %q %q", deploy, tag)
	}
	runtimeDo(t, b.handler, runtimeCall{method: http.MethodPut, path: "/internal/agent-runtime/runs/" + run.ID + "/state", worker: "w1", bearer: testWorkerSecret, body: []byte(`{"state":"suspended-run"}`)})
	b.complete(t, run, "awaiting")
	if status, body := postAgentJSON(t, a.api.URL, "/api/agent/sessions/"+sessionID+"/approvals/"+deploy, `{"decision":"approve"}`); status != http.StatusOK {
		t.Fatalf("approve on the first server: %d %s", status, body)
	}
	if status, body := postAgentJSON(t, b.api.URL, "/api/agent/sessions/"+sessionID+"/approvals/"+tag, `{"decision":"reject"}`); status != http.StatusOK {
		t.Fatalf("reject on the second server: %d %s", status, body)
	}
	resume := b.mustClaim(t)
	payload := a.payload(t, resume)
	if payload.Chat.Resume == nil || payload.Chat.Resume.Decisions["c1"] != "approved" || payload.Chat.Resume.Decisions["c2"] != "rejected" {
		t.Fatalf("resume payload = %+v", payload.Chat)
	}
	a.post(t, resume, map[string]any{"type": "action.result", "seq": 1, "callId": "c1", "content": map[string]any{"action": "external_deploy", "result": "deployed"}})
	b.post(t, resume, map[string]any{"type": "action.result", "seq": 2, "callId": "c2", "content": map[string]any{"action": "external_tag", "result": "The user rejected this action"}})
	a.post(t, resume, map[string]any{"type": "response", "seq": 3, "body": "Deployed; the tag was skipped."})
	b.complete(t, resume, "done")
	final := b.waitReply(t, sessionID, func(message domain.AgentMessage) bool { return message.Status == "" })
	if call := toolPart(final.Parts, "c1"); call == nil || call.ToolCall.Status != "completed" || string(call.ToolCall.Result) != `"deployed"` {
		t.Fatalf("deploy = %+v", final.Parts)
	}
	if call := toolPart(final.Parts, "c2"); call == nil || call.ToolCall.Error != workerChatDeclined {
		t.Fatalf("tag = %+v", final.Parts)
	}
}
