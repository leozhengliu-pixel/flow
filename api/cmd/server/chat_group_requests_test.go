package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// The Flow Agent files requests from a group conversation (chat_group_requests.go),
// played against the fake chat platform of the direct message tests and a
// scripted model (or a scripted worker).

// chatGroupRequestOffered is in a model request that offers file_request (its
// definition, not a call in the history).
const chatGroupRequestOffered = `"description":"File a request from this group conversation`

const chatGroupRequestArgs = `{"title":"Export fails on Safari","description":"Open the report in Safari and export it: nothing downloads."}`

// waitApproval waits for the approval card the bot posts.
func (f *chatDMFixture) waitApproval(t *testing.T) chatDMMessage {
	t.Helper()
	return f.waitApprovalSince(t, 0)
}

// waitApprovalSince waits for an approval card among the messages sent after
// the first count.
func (f *chatDMFixture) waitApprovalSince(t *testing.T, count int) chatDMMessage {
	t.Helper()
	var approval chatDMMessage
	f.waitFor(t, "the approval card", func() bool {
		for _, message := range f.sentSince(count) {
			if strings.Contains(message.Raw, "需要批准") {
				approval = message
				return true
			}
		}
		return false
	})
	return approval
}

// checkGroupRequest checks the request a group message became: the same
// request as “@Flow 提需求” makes, as the member, in the team's triage, with the
// group's link, the message's thread, and its card replied in that thread.
func (f *chatDMFixture) checkGroupRequest(t *testing.T, messageID string) domain.Issue {
	t.Helper()
	issueID := chatRequestIssueID(f.chatWorkflowFixture, messageID)
	issue, err := f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, issueID)
	if err != nil {
		t.Fatalf("request issue: %v", err)
	}
	data := f.s.store.Bootstrap()
	if issue.Creator.ID != f.identity.UserID || issue.Team.ID != f.binding.TeamID || issue.Title != "Export fails on Safari" || !isTriageIssue(&data, &issue) || !strings.Contains(issue.Description, "nothing downloads") || !strings.Contains(issue.Description, "openChatId="+f.binding.ChatID) {
		t.Fatalf("request issue: %+v", issue)
	}
	asks := f.asksFor(t, issueID)
	if len(asks) != 1 || asks[0].Source != "feishu" || asks[0].Requester.ID != f.identity.UserID || asks[0].Status != "approved" {
		t.Fatalf("request record: %+v", asks)
	}
	request, err := f.s.store.ChatIntakeRequestForMessage(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, messageID)
	if err != nil || request.Status != domain.ChatRequestCreated || request.IssueID != issueID || request.AskID != asks[0].ID || request.Source != domain.ChatRequestSourceMention || request.RequesterID != f.identity.UserID {
		t.Fatalf("intake record: %+v %v", request, err)
	}
	if thread := f.thread(t, issueID); thread.RootMessageID != messageID || thread.Origin != "request" {
		t.Fatalf("the message's thread is not the request's: %+v", thread)
	}
	// The confirmation card, as for “@Flow 提需求”, replies in the message's thread.
	f.process(t)
	if !slices.ContainsFunc(f.sent(), func(message chatDMMessage) bool {
		return message.ReplyTo == messageID && message.InThread && strings.Contains(message.Raw, issue.Identifier) && strings.Contains(message.Raw, "已收到需求")
	}) {
		t.Fatalf("no request card in the thread: %+v", f.sent())
	}
	return issue
}

// In a bound group that takes requests, the agent files a request when the
// member asks, after the member (and only they) approves it in the thread.
func TestChatGroupAgentFilesARequestAfterApproval(t *testing.T) {
	f := newChatDMFixture(t, func(call int, input string) string {
		if call == 1 {
			return chatDMToolTurn(chatGroupRequestTool, chatGroupRequestArgs)
		}
		return chatDMTextTurn("已提交需求。")
	})
	f.enableRequests(t, &domain.ChatBindingRequests{Enabled: true, Emoji: "OK"}, nil)
	f.useTriage(t)
	f.history["chat:"+f.binding.ChatID] = `[{"message_id":"om_earlier","msg_type":"text","sender":{"id":"ou_alice","sender_type":"user"},"body":{"content":"{\"text\":\"Safari export is broken\"}"}}]`
	other := f.linkMember(t, "chat-group-request-other@example.test", "ou_request_other", false)

	f.groupSend(t, f.identity.OpenID, f.binding.ChatID, "om_request_ask", "把 Safari 导出的问题提成需求", "", "", true)
	approval := f.waitApproval(t)
	first := f.input(1)
	for _, want := range []string{chatGroupRequestOffered, "call file_request", "[om_earlier] Member: Safari export is broken"} {
		if !strings.Contains(first, want) {
			t.Fatalf("the agent was not offered %q: %.3000s", want, first)
		}
	}
	if strings.Contains(first, "You cannot file a request yourself") {
		t.Fatalf("the agent was told it cannot file requests: %.3000s", first)
	}
	if approval.ReplyTo != "om_request_ask" || !approval.InThread || approval.ChatID != f.binding.ChatID || !strings.Contains(approval.Raw, "File request") || !strings.Contains(approval.Raw, "Export fails on Safari") {
		t.Fatalf("approval card: %+v", approval)
	}
	issueID := chatRequestIssueID(f.chatWorkflowFixture, "om_request_ask")
	if answer := f.clickAs(t, other.OpenID, approval, "批准", nil); !strings.Contains(answer, "可以批准或拒绝") {
		t.Fatalf("another member's click: %s", answer)
	}
	if _, err := f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, issueID); err == nil {
		t.Fatal("the request was filed before the member approved it")
	}
	f.click(t, approval, "批准", nil)
	chatDMReplies.Wait()
	issue := f.checkGroupRequest(t, "om_request_ask")
	if card := f.lastUpdate(approval.ID); !strings.Contains(card, "已批准") {
		t.Fatalf("approval card after the decision: %s", card)
	}
	if final := f.lastUpdate(f.sent()[0].ID); !strings.Contains(final, "已提交需求。") {
		t.Fatalf("final card: %s", final)
	}
	reply := f.sessions(t)[0].Messages[1]
	index := slices.IndexFunc(reply.Parts, func(part domain.AgentMessagePart) bool {
		return part.ToolCall != nil && part.ToolCall.Name == chatGroupRequestTool
	})
	if index < 0 || reply.Parts[index].ToolCall.Status != "completed" || reply.Parts[index].ToolCall.Title != "Filed request "+issue.Identifier {
		t.Fatalf("the reply's step: %+v", reply.Parts)
	}
}

// Asked again for the same message, the agent hears the request exists and
// files nothing; a rejected call files nothing.
func TestChatGroupAgentRequestRejectedOrExisting(t *testing.T) {
	f := newChatDMFixture(t, func(call int, input string) string {
		switch call {
		case 1, 3:
			return chatDMToolTurn(chatGroupRequestTool, chatGroupRequestArgs)
		default:
			return chatDMTextTurn("Done.")
		}
	})
	f.enableRequests(t, &domain.ChatBindingRequests{Enabled: true}, nil)
	f.groupSend(t, f.identity.OpenID, f.binding.ChatID, "om_request_reject", "file this as a request", "", "", true)
	f.click(t, f.waitApproval(t), "拒绝", nil)
	chatDMReplies.Wait()
	if _, err := f.s.store.ChatIntakeRequestForMessage(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, "om_request_reject"); err == nil {
		t.Fatal("a rejected call filed a request")
	}
	if second := f.input(2); !strings.Contains(second, "Action declined by user") {
		t.Fatalf("the model was not told the call was declined: %.2000s", second)
	}

	// The thread's root already carries a request.
	if _, _, err := f.s.store.ReserveChatIntakeRequest(t.Context(), f.record.WorkspaceKey, domain.ChatIntakeRequest{ConnectionID: f.record.ConnectionID, ChatID: f.binding.ChatID, MessageID: "om_request_twice", BindingID: f.binding.ID, IssueID: f.s.store.Bootstrap().Issues[0].ID, RequesterID: f.identity.UserID, RequesterOpenID: f.identity.OpenID, Source: domain.ChatRequestSourceReaction, Status: domain.ChatRequestCreated}); err != nil {
		t.Fatal(err)
	}
	count := len(f.sent())
	f.groupSend(t, f.identity.OpenID, f.binding.ChatID, "om_request_twice", "file this as a request", "", "", true)
	f.click(t, f.waitApprovalSince(t, count), "批准", nil)
	chatDMReplies.Wait()
	if fourth := f.input(4); !strings.Contains(fourth, "This message is already a request: "+f.s.store.Bootstrap().Issues[0].Identifier) {
		t.Fatalf("the model was not told the request exists: %.3000s", fourth)
	}
	if asks := f.asksFor(t, chatRequestIssueID(f.chatWorkflowFixture, "om_request_twice")); len(asks) != 0 {
		t.Fatalf("a second request was filed: %+v", asks)
	}
}

// The tool is offered only to a turn asked in a bound group that takes
// requests: not in a direct message, an unbound group or a group without
// request intake. A call where it is not offered is refused and files nothing.
func TestChatGroupAgentRequestToolScope(t *testing.T) {
	f := newChatDMFixture(t, func(call int, input string) string {
		if strings.Contains(input, "function_call_output") {
			return chatDMTextTurn("Answered.")
		}
		return chatDMToolTurn(chatGroupRequestTool, chatGroupRequestArgs)
	})
	offered := func(input string) bool { return strings.Contains(input, chatGroupRequestOffered) }

	f.text(t, "om_scope_dm", "file this as a request")
	chatDMReplies.Wait()
	f.groupSend(t, f.identity.OpenID, chatGroupTestUnbound, "om_scope_unbound", "file this as a request", "", "", true)
	chatDMReplies.Wait()
	f.groupSend(t, f.identity.OpenID, f.binding.ChatID, "om_scope_off", "file this as a request", "", "", true)
	chatDMReplies.Wait()
	for call := 1; call <= int(f.calls.Load()); call++ {
		if input := f.input(call); offered(input) {
			t.Fatalf("call %d offered file_request: %.2000s", call, input)
		}
	}
	for _, call := range []int{2, 4, 6} {
		if !strings.Contains(f.input(call), "file_request is available only") {
			t.Fatalf("a call where the tool is not offered was not refused: %.2000s", f.input(call))
		}
	}
	for _, message := range f.sent() {
		if strings.Contains(message.Raw, "需要批准") {
			t.Fatalf("an approval was asked where the tool is not offered: %+v", message)
		}
	}
	data, _ := f.s.store.BootstrapFor(f.record.WorkspaceKey)
	if slices.ContainsFunc(data.Asks, func(ask domain.Ask) bool { return ask.Source == "feishu" }) {
		t.Fatalf("a request was filed: %+v", data.Asks)
	}

	// With request intake on, the same group offers it; turned off again, not.
	f.enableRequests(t, &domain.ChatBindingRequests{Enabled: true}, nil)
	calls := int(f.calls.Load())
	f.groupSend(t, f.identity.OpenID, f.binding.ChatID, "om_scope_on", "file this as a request", "", "", true)
	f.click(t, f.waitApproval(t), "拒绝", nil)
	chatDMReplies.Wait()
	if !offered(f.input(calls + 1)) {
		t.Fatalf("not offered in a group that takes requests: %.2000s", f.input(calls+1))
	}
	// Write tools off: not offered.
	f.s.agent.WriteTools = false
	calls = int(f.calls.Load())
	f.groupSend(t, f.identity.OpenID, f.binding.ChatID, "om_scope_readonly", "what is open?", "", "", true)
	chatDMReplies.Wait()
	if offered(f.input(calls + 1)) {
		t.Fatalf("offered without write tools: %.2000s", f.input(calls+1))
	}
}

// The message a request is filed from: the thread's root by default, the
// member's own message, or another message of the same group the bot reads.
func TestChatGroupAgentRequestMessage(t *testing.T) {
	f := newChatWorkflowFixture(t)
	_, messages := f.requestPlatform(t)
	messages["om_draft_other"] = chatRequestMessage{chat: f.binding.ChatID, text: "Safari export", root: "om_draft_other_root"}
	messages["om_draft_elsewhere"] = chatRequestMessage{chat: "oc_somewhere_else", text: "Not here"}
	scope := chatGroupRequestScope{Provider: f.record.Provider, ConnectionID: f.record.ConnectionID, ChatID: f.binding.ChatID, OpenID: f.identity.OpenID, MessageID: "om_draft_ask", Root: "om_draft_root"}
	for _, tc := range []struct {
		messageID, source, root, code string
	}{
		{"", "om_draft_root", "", ""},
		{"om_draft_root", "om_draft_root", "", ""},
		{"om_draft_ask", "om_draft_ask", "om_draft_root", ""},
		{"om_draft_other", "om_draft_other", "om_draft_other_root", ""},
		{"om_draft_elsewhere", "", "", "chat_message_unavailable"},
		{"om_draft_unknown", "", "", "chat_message_unavailable"},
		{"DEV-1", "", "", "chat_message_unavailable"},
	} {
		draft, code, err := f.s.chatGroupRequestDraft(t.Context(), f.record, scope, tc.messageID)
		if err != nil || code != tc.code || draft.MessageID != tc.source || draft.RootID != tc.root || tc.code == "" && draft.ChatID != f.binding.ChatID {
			t.Fatalf("%q: %+v %q %v", tc.messageID, draft, code, err)
		}
	}
}

// On the worker runtime the run's info carries the group: /mcp lists
// file_request to the run as always asking, refuses an unapproved call, and
// files the request once the member approved it in the thread.
func TestChatGroupAgentFilesARequestOnTheWorkerRuntime(t *testing.T) {
	f := newChatDMFixture(t, func(int, string) string { return chatDMTextTurn("in-process") })
	f.s.agent.WorkerSecret = testWorkerSecret
	f.s.agent.Runtime = appconfig.AgentRuntimeWorker
	handler := newHandler(f.s)
	t.Cleanup(func() { f.s.stopDeliveryScheduler(t.Context()) })
	f.enableRequests(t, &domain.ChatBindingRequests{Enabled: true}, nil)
	f.useTriage(t)
	other := f.linkMember(t, "chat-group-worker-other@example.test", "ou_worker_other", false)
	claim := func() store.AgentRun {
		t.Helper()
		claimed := runtimeJSON[struct {
			Runs []store.AgentRun `json:"runs"`
		}](t, handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/claim", worker: "w1", body: map[string]any{"free": map[string]int{"chat": 1}, "waitMs": 5000}}, http.StatusOK)
		if len(claimed.Runs) != 1 {
			t.Fatal("no worker run was queued")
		}
		return claimed.Runs[0]
	}
	post := func(run store.AgentRun, activity map[string]any) {
		t.Helper()
		runtimeJSON[map[string]any](t, handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/activities", worker: "w1", body: activity}, http.StatusOK)
	}
	token := func(run store.AgentRun) string {
		t.Helper()
		return runtimeJSON[struct {
			Token string `json:"token"`
		}](t, handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/token", worker: "w1"}, http.StatusCreated).Token
	}
	call := func(run store.AgentRun, meta map[string]any) map[string]any {
		t.Helper()
		var arguments map[string]any
		_ = json.Unmarshal([]byte(chatGroupRequestArgs), &arguments)
		params := map[string]any{"name": chatGroupRequestTool, "arguments": arguments}
		if meta != nil {
			params["_meta"] = meta
		}
		_, response := mcpWithToken(t, handler, token(run), "tools/call", params, nil)
		result, _ := response["result"].(map[string]any)
		return result
	}

	f.groupSend(t, f.identity.OpenID, f.binding.ChatID, "om_worker_request", "file this as a request", "", "", true)
	run := claim()
	payload := runtimeJSON[workerChatPayload](t, handler, runtimeCall{method: http.MethodGet, path: "/internal/agent-runtime/runs/" + run.ID + "/payload", worker: "w1"}, http.StatusOK)
	if scope := payload.Chat.GroupRequests; scope == nil || scope.ChatID != f.binding.ChatID || scope.OpenID != f.identity.OpenID || scope.Root != "om_worker_request" {
		t.Fatalf("the run's info lacks the group: %+v", payload.Chat)
	}
	if !strings.Contains(payload.Context.System, "call file_request") {
		t.Fatalf("the worker's prompt does not offer filing: %.2000s", payload.Context.System)
	}
	_, listed := mcpWithToken(t, handler, token(run), "tools/list", map[string]any{}, nil)
	tools, _ := listed["result"].(map[string]any)["tools"].([]any)
	index := slices.IndexFunc(tools, func(item any) bool { return item.(map[string]any)["name"] == chatGroupRequestTool })
	if index < 0 {
		t.Fatalf("/mcp does not list file_request to the run: %+v", listed)
	}
	if meta, _ := tools[index].(map[string]any)["_meta"].(map[string]any); meta[agentRunApprovalMeta] != connectorToolApprovalAlways {
		t.Fatalf("file_request is not listed as always asking: %+v", tools[index])
	}
	if result := call(run, nil); result["isError"] != true {
		t.Fatalf("an unapproved call ran: %+v", result)
	}
	if _, err := f.s.store.ChatIntakeRequestForMessage(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, "om_worker_request"); err == nil {
		t.Fatal("an unapproved call filed a request")
	}

	// The worker holds the call: its approval card goes into the thread.
	post(run, map[string]any{"type": "elicitation", "activityId": "elicitation", "elicitation": map[string]any{"kind": "approval", "body": "Run file_request?", "calls": []map[string]any{{"callId": "c1", "name": chatGroupRequestTool, "arguments": chatGroupRequestArgs}}}})
	runtimeDo(t, handler, runtimeCall{method: http.MethodPut, path: "/internal/agent-runtime/runs/" + run.ID + "/state", worker: "w1", bearer: testWorkerSecret, body: []byte(`{"state":"suspended-run"}`), headers: map[string]string{"Content-Type": "application/json"}})
	runtimeJSON[map[string]any](t, handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/complete", worker: "w1", body: map[string]any{"status": "awaiting"}}, http.StatusOK)
	approval := f.waitApproval(t)
	if approval.ReplyTo != "om_worker_request" || !approval.InThread || !strings.Contains(approval.Raw, "Export fails on Safari") {
		t.Fatalf("approval card: %+v", approval)
	}
	if answer := f.clickAs(t, other.OpenID, approval, "批准", nil); !strings.Contains(answer, "可以批准或拒绝") {
		t.Fatalf("another member's click: %s", answer)
	}
	f.click(t, approval, "批准", nil)

	// The resumed run sends the approved call.
	resume := claim()
	resumed := runtimeJSON[workerChatPayload](t, handler, runtimeCall{method: http.MethodGet, path: "/internal/agent-runtime/runs/" + resume.ID + "/payload", worker: "w1"}, http.StatusOK)
	if resumed.Chat.Resume == nil || resumed.Chat.Resume.Decisions["c1"] != "approved" || resumed.Chat.GroupRequests == nil {
		t.Fatalf("resume payload: %+v", resumed.Chat)
	}
	result := call(resume, map[string]any{agentRunApprovedMeta: true})
	content, _ := json.Marshal(result)
	if result["isError"] == true || !strings.Contains(string(content), "identifier") {
		t.Fatalf("the approved call failed: %s", content)
	}
	post(resume, map[string]any{"type": "action.result", "activityId": "c1:result", "callId": "c1", "content": map[string]any{"action": chatGroupRequestTool, "result": result["content"].([]any)[0].(map[string]any)["text"]}})
	post(resume, map[string]any{"type": "response", "activityId": "end", "body": "Filed."})
	runtimeJSON[map[string]any](t, handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + resume.ID + "/complete", worker: "w1", body: map[string]any{"status": "done"}}, http.StatusOK)
	chatDMReplies.Wait()
	issue := f.checkGroupRequest(t, "om_worker_request")
	ledger := f.s.agentRunLedger(t.Context(), resume)
	if !slices.ContainsFunc(ledger.outcomes, func(outcome agentWriteOutcome) bool {
		return outcome.Tool == chatGroupRequestTool && outcome.Status == agentWriteSucceeded && outcome.Target == issue.Identifier
	}) {
		t.Fatalf("the write ledger lacks the request: %+v", ledger.outcomes)
	}
	if f.calls.Load() != 0 {
		t.Fatal("the reply ran in process")
	}
}
