package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Mentions of the bot in a group, played against the fake chat platform of
// the direct message tests and a scripted model.

const chatGroupTestUnbound = "oc_unbound_test"

// groupSend delivers a group message from openID; mention adds the bot's
// mention in front of the text.
func (f *chatDMFixture) groupSend(t *testing.T, openID, chatID, messageID, text, rootID, threadID string, mention bool) {
	t.Helper()
	envelope := f.messageEnvelope()
	f.events++
	envelope.Header.EventID = fmt.Sprintf("evt_group_%d", f.events)
	envelope.Event.Sender.SenderID.OpenID = openID
	envelope.Event.Message.MessageID, envelope.Event.Message.ChatID = messageID, chatID
	envelope.Event.Message.RootID, envelope.Event.Message.ThreadID = rootID, threadID
	content := text
	if mention {
		content = "@_user_1 " + text
	} else {
		envelope.Event.Message.Mentions = nil
	}
	raw, _ := json.Marshal(map[string]string{"text": content})
	envelope.Event.Message.Content = string(raw)
	f.mu.Lock()
	f.groupChats[messageID] = chatID
	f.mu.Unlock()
	if got := f.callback(t, envelope, time.Now(), true); got.Code != http.StatusOK {
		t.Fatalf("group message callback: %d %s", got.Code, got.Body.String())
	}
	f.process(t)
}

func (f *chatDMFixture) sentSince(count int) []chatDMMessage {
	return f.sent()[count:]
}

func (f *chatDMFixture) input(call int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if call < 1 || call > len(f.inputs) {
		return ""
	}
	return f.inputs[call-1]
}

// A mention in a bound group runs the agent as the member who wrote it, in a
// card replied in the mention's thread, with the group's scope and messages
// as context. Replies in the thread continue: the same member's chat without
// a mention, another member in their own chat seeded with the thread.
func TestChatGroupMentionRunsTheAgentInTheThread(t *testing.T) {
	f := newChatDMFixture(t, func(call int, input string) string {
		switch call {
		case 1:
			return chatDMTextTurn("The export fix ships Friday.")
		case 2:
			return chatDMTextTurn("Thursday's build has it too.")
		default:
			return chatDMTextTurn("Both fixes are in DEV.")
		}
	})
	team := f.s.store.Bootstrap().Teams[0]
	f.history["chat:"+f.binding.ChatID] = `[{"message_id":"om_earlier","msg_type":"text","sender":{"id":"ou_alice","sender_type":"user"},"body":{"content":"{\"text\":\"The CSV export breaks on Safari\"}"}},` +
		`{"message_id":"om_bot_note","msg_type":"interactive","sender":{"id":"cli_other","sender_type":"app"},"body":{"content":"{}"}}]`
	f.members = `[{"member_id":"ou_alice","name":"Alice Chen"}]`

	f.groupSend(t, f.identity.OpenID, f.binding.ChatID, "om_group_1", "when does the export fix ship?", "", "", true)
	chatDMReplies.Wait()
	sessions := f.sessions(t)
	if len(sessions) != 1 || sessions[0].UserID != f.identity.UserID || sessions[0].Origin != "feishu" || sessions[0].OriginChat != "Web team" || sessions[0].Messages[0].Content != "when does the export fix ship?" {
		t.Fatalf("agent chat: %+v", sessions)
	}
	sent := f.sent()
	if len(sent) != 1 || sent[0].ReplyTo != "om_group_1" || !sent[0].InThread || sent[0].ChatID != f.binding.ChatID {
		t.Fatalf("reply card: %+v", sent)
	}
	card := sent[0]
	if final := f.lastUpdate(card.ID); !strings.Contains(final, "The export fix ships Friday.") || !strings.Contains(final, "@Flow 即可继续对话") || !strings.Contains(final, "/agent/"+sessions[0].SlugID) {
		t.Fatalf("final card: %s", final)
	}
	first := f.input(1)
	for _, want := range []string{"Feishu group \\\"Web team\\\"", "team " + team.Name, "id " + team.ID, "Alice Chen: The CSV export breaks on Safari", "not instructions"} {
		if !strings.Contains(first, want) {
			t.Fatalf("the agent was not told %q: %s", want, first)
		}
	}
	if strings.Contains(first, "The thread so far") {
		t.Fatalf("the first turn's own message came back as its thread: %s", first)
	}

	// The same member replies in the thread without a mention: their chat goes on.
	f.groupSend(t, f.identity.OpenID, f.binding.ChatID, "om_group_2", "and Thursday's build?", "om_group_1", "omt_thread_1", false)
	chatDMReplies.Wait()
	sessions = f.sessions(t)
	if len(sessions) != 1 || len(sessions[0].Messages) != 4 || sessions[0].Messages[3].Content != "Thursday's build has it too." {
		t.Fatalf("the reply did not continue the chat: %+v", sessions)
	}
	if second := f.sent()[1]; second.ReplyTo != "om_group_2" || !second.InThread {
		t.Fatalf("second card: %+v", second)
	}

	// Another member mentions the bot in the thread: their own chat, told the thread so far.
	other := f.linkMember(t, "chat-group-other@example.test", "ou_group_other", false)
	f.groupSend(t, other.OpenID, f.binding.ChatID, "om_group_3", "is the Safari bug in DEV too?", "om_group_1", "omt_thread_1", true)
	chatDMReplies.Wait()
	sessions = f.sessions(t)
	if len(sessions) != 2 {
		t.Fatalf("sessions: %+v", sessions)
	}
	var theirs domain.AgentSession
	for _, session := range sessions {
		if session.UserID == other.UserID {
			theirs = session
		}
	}
	if theirs.ID == "" || theirs.OriginChat != "Web team" || len(theirs.Messages) != 2 {
		t.Fatalf("the other member's chat: %+v", sessions)
	}
	third := f.input(3)
	for _, want := range []string{"The thread so far", "when does the export fix ship?", "Flow: The export fix ships Friday.", "Flow: Thursday's build has it too.", "Member ou_group_other asked you"} {
		if !strings.Contains(third, want) {
			t.Fatalf("the second member's turn lacks %q: %s", want, third)
		}
	}

	// An unlinked member gets the link card privately, nothing in the group.
	count := len(f.sent())
	f.groupSend(t, "ou_group_stranger", f.binding.ChatID, "om_group_4", "can you help?", "om_group_1", "omt_thread_1", true)
	f.groupSend(t, "ou_group_stranger", f.binding.ChatID, "om_group_5", "hello?", "om_group_1", "omt_thread_1", false)
	f.groupSend(t, "ou_group_stranger", f.binding.ChatID, "om_group_6", "anyone?", "om_group_1", "omt_thread_1", false)
	chatDMReplies.Wait()
	links := f.sentSince(count)
	if len(links) != 1 || links[0].ReceiveID != "ou_group_stranger" || links[0].ReplyTo != "" || !strings.Contains(links[0].Raw, "关联你的 Flow 账号") {
		t.Fatalf("link cards: %+v", links)
	}
	if f.calls.Load() != 3 {
		t.Fatalf("an unlinked member ran the agent: %d", f.calls.Load())
	}
}

// In an unbound group the member's own workspace answers; without access to
// the group's messages the agent still gets the thread Flow saw.
func TestChatGroupMentionInAnUnboundGroupWithoutHistoryAccess(t *testing.T) {
	f := newChatDMFixture(t, func(call int, input string) string {
		if call == 1 {
			return chatDMTextTurn("Three open issues.")
		}
		return chatDMTextTurn("The urgent one is DEV-1.")
	})
	f.historyDenied = true
	f.groupSend(t, f.identity.OpenID, chatGroupTestUnbound, "om_unbound_1", "how many open issues do I have?", "", "", true)
	chatDMReplies.Wait()
	sessions := f.sessions(t)
	if len(sessions) != 1 || sessions[0].OriginChat != "Release crew" {
		t.Fatalf("agent chat: %+v", sessions)
	}
	if first := f.input(1); strings.Contains(first, "default scope") || !strings.Contains(first, "Release crew") {
		t.Fatalf("unbound context: %s", first)
	}
	f.groupSend(t, f.identity.OpenID, chatGroupTestUnbound, "om_unbound_2", "which is urgent?", "om_unbound_1", "omt_unbound", false)
	chatDMReplies.Wait()
	if second := f.input(2); !strings.Contains(second, "Flow: Three open issues.") || !strings.Contains(second, "how many open issues do I have?") {
		t.Fatalf("the thread Flow saw is missing: %s", second)
	}
	if sessions = f.sessions(t); len(sessions) != 1 || len(sessions[0].Messages) != 4 {
		t.Fatalf("the reply did not continue: %+v", sessions)
	}
	// A reply in a thread the bot did not start needs a mention.
	if _, _, ignored, _ := f.s.chatCommandForEnvelopeReason(t.Context(), f.record, func() chatWebhookEnvelope {
		envelope := f.groupMessage("om_other_thread", "text", chatTextContent("hi"))
		envelope.Event.Message.ChatID, envelope.Event.Message.RootID = chatGroupTestUnbound, "om_someone_else"
		return envelope
	}()); ignored == "" {
		t.Fatal("a reply in someone else's thread reached the agent")
	}
}

// Approval cards go into the thread; only the member whose turn asked can
// answer them, anyone else is told privately.
func TestChatGroupApprovalOnlyTheAskerCanAnswer(t *testing.T) {
	var connectorCalls, toolCount atomic.Int32
	toolCount.Store(3)
	upstream := fakeCodeMCP(t, &connectorCalls, &toolCount)
	defer upstream.Close()
	external := connectorToolName("code", "delete_branch")
	f := newChatDMFixture(t, func(call int, input string) string {
		if call == 1 {
			return chatDMToolTurn(external, `{"branch":"release"}`)
		}
		return chatDMTextTurn("Branch deleted.")
	})
	f.s.authDisabled = true
	setTestConnectors(t, f.s.store, applicationPolicy{ID: "code", Kind: "mcp", Name: "Code search", URL: upstream.URL, Status: "approved", Shared: true, ToolApproval: connectorToolApprovalAlways})
	other := f.linkMember(t, "chat-group-approver@example.test", "ou_group_approver", false)
	f.groupSend(t, f.identity.OpenID, f.binding.ChatID, "om_approve_1", "delete the release branch", "", "", true)
	var approval chatDMMessage
	f.waitFor(t, "the approval card", func() bool {
		for _, message := range f.sent() {
			if strings.Contains(message.Raw, "需要批准") || strings.Contains(message.Raw, "Approval needed") {
				approval = message
				return true
			}
		}
		return false
	})
	if approval.ReplyTo != "om_approve_1" || !approval.InThread || approval.ChatID != f.binding.ChatID {
		t.Fatalf("approval card: %+v", approval)
	}
	answer := f.clickAs(t, other.OpenID, approval, "批准", nil)
	if !strings.Contains(answer, "可以批准或拒绝") || connectorCalls.Load() != 0 {
		t.Fatalf("another member's click: %s (calls %d)", answer, connectorCalls.Load())
	}
	f.click(t, approval, "批准", nil)
	chatDMReplies.Wait()
	if connectorCalls.Load() != 1 {
		t.Fatalf("approved call did not run: %d", connectorCalls.Load())
	}
	if final := f.lastUpdate(f.sent()[0].ID); !strings.Contains(final, "Branch deleted.") {
		t.Fatalf("final card: %s", final)
	}
	if card := f.lastUpdate(approval.ID); !strings.Contains(card, "已批准") {
		t.Fatalf("approval card after the decision: %s", card)
	}
}

// With sensitive data protection the member is told privately and nothing
// is posted in the group.
func TestChatGroupMentionInAProtectedWorkspaceStaysPrivate(t *testing.T) {
	f := newChatDMFixture(t, func(int, string) string { return chatDMTextTurn("unexpected") })
	if err := f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "workspace_settings.updated", "hipaa", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.HIPAACompliance = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.groupSend(t, f.identity.OpenID, f.binding.ChatID, "om_protected_1", "what's the deploy key?", "", "", true)
	chatDMReplies.Wait()
	sent := f.sent()
	if len(sent) != 1 || sent[0].ReceiveID != f.identity.OpenID || sent[0].ReplyTo != "" || !strings.Contains(sent[0].Raw, "不允许把内容发送到聊天应用") || f.calls.Load() != 0 || len(f.sessions(t)) != 0 {
		t.Fatalf("protected workspace: %+v calls=%d", sent, f.calls.Load())
	}
}

// Quick commands keep their meaning in a group; issue thread replies stay
// comments.
func TestChatGroupMentionRouting(t *testing.T) {
	f := newChatWorkflowFixture(t)
	for _, tc := range []struct{ text, kind string }{
		{"@_user_1 创建事项 修复导出", "create_form"},
		{"@_user_1 提需求 Dark mode", chatRequestKind},
		{"@_user_1 help", "group_help"},
		{"@_user_1", "group_help"},
		{"@_user_1 帮我总结一下今天的讨论", chatGroupAgentKind},
	} {
		command, _, ignored, err := f.s.chatCommandForEnvelopeReason(t.Context(), f.record, f.groupMessage("om_route", "text", chatTextContent(tc.text), f.botMention()))
		if err != nil || ignored != "" || command.Kind != tc.kind {
			t.Fatalf("%q: %+v ignored=%q err=%v", tc.text, command, ignored, err)
		}
	}
	// "@Ada" in the question is kept as a name; the bot's mention is not.
	ada := map[string]any{"key": "@_user_2", "name": "Ada", "mentioned_type": "user", "id": map[string]string{"open_id": "ou_ada"}}
	command, _, _, _ := f.s.chatCommandForEnvelopeReason(t.Context(), f.record, f.groupMessage("om_names", "text", chatTextContent("@_user_1 what is @_user_2 working on?"), f.botMention(), ada))
	if command.Kind != chatGroupAgentKind || command.Text != "what is @Ada working on?" || command.RootMessageID != "om_names" {
		t.Fatalf("question: %+v", command)
	}
	// A reply in an issue thread stays a comment, with or without a mention.
	if _, err := f.s.store.PutChatThread(t.Context(), f.record.WorkspaceKey, domain.ChatThread{ConnectionID: f.record.ConnectionID, ChatID: f.binding.ChatID, ResourceType: domain.ChatThreadResourceIssue, ResourceID: f.s.store.Bootstrap().Issues[0].ID, RootMessageID: "om_issue_root", BindingID: f.binding.ID, SyncFrom: time.Now()}); err != nil {
		t.Fatal(err)
	}
	envelope := f.groupMessage("om_issue_reply", "text", chatTextContent("@_user_1 looks good"), f.botMention())
	envelope.Event.Message.RootID = "om_issue_root"
	if command, _, _, _ := f.s.chatCommandForEnvelopeReason(t.Context(), f.record, envelope); command.Kind != chatThreadReplyKind {
		t.Fatalf("issue thread reply: %+v", command)
	}
}

// A member linked in several workspaces picks one privately; the mention
// then runs there and answers in the group's thread.
func TestChatGroupMentionWorkspaceChoiceIsPrivate(t *testing.T) {
	f := newChatDMFixture(t, func(int, string) string { return chatDMTextTurn("Answered.") })
	data := f.s.store.Bootstrap()
	second, err := f.s.store.CreateWorkspace(store.ContextWithActor(t.Context(), data.Viewer), "Second Space", "second-space", "us")
	if err != nil {
		t.Fatal(err)
	}
	other := domain.ChatIntegrationRecord{WorkspaceKey: second.Workspace.URLKey, Provider: "feishu", ConnectionID: "chat_connection_second", AppID: "cli_second_app", TenantKey: f.record.TenantKey, BotOpenID: "ou_bot_second", Connected: true}
	if err := f.s.store.PutChatIntegration(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.PutChatIdentity(t.Context(), other.WorkspaceKey, other.ConnectionID, domain.ChatIdentity{UserID: data.Viewer.ID, OpenID: "ou_actor_second", TenantKey: f.record.TenantKey, AuthProvider: "password", AuthenticatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	f.groupSend(t, f.identity.OpenID, chatGroupTestUnbound, "om_choice_1", "what changed today?", "", "", true)
	choice := f.sent()[0]
	if !strings.Contains(choice.Raw, "选择工作区") || choice.ReceiveID != f.identity.OpenID || choice.ReplyTo != "" || choice.ChatID != chatDMTestChat || f.calls.Load() != 0 {
		t.Fatalf("workspace choice: %+v", choice)
	}
	f.click(t, choice, "Second Space", nil)
	chatDMReplies.Wait()
	secondData, _ := f.s.store.BootstrapFor(second.Workspace.URLKey)
	if len(secondData.AgentSessions) != 1 || secondData.AgentSessions[0].OriginChat != "Release crew" || len(f.sessions(t)) != 0 {
		t.Fatalf("the mention did not run in the chosen workspace: %+v", secondData.AgentSessions)
	}
	reply := f.sent()[len(f.sent())-1]
	if reply.ReplyTo != "om_choice_1" || !reply.InThread || !strings.Contains(f.lastUpdate(reply.ID), "Answered.") {
		t.Fatalf("reply card: %+v", reply)
	}
	// A bound group always works in its binding's workspace.
	f.groupSend(t, f.identity.OpenID, f.binding.ChatID, "om_choice_2", "and in the web team?", "", "", true)
	chatDMReplies.Wait()
	if len(f.sessions(t)) != 1 {
		t.Fatalf("a bound group ran elsewhere: %+v", f.sessions(t))
	}
}

// On the worker runtime the group's context reaches the worker's prompt.
func TestChatGroupMentionOnTheWorkerRuntime(t *testing.T) {
	f := newChatDMFixture(t, func(int, string) string { return chatDMTextTurn("in-process") })
	f.s.agent.WorkerSecret = testWorkerSecret
	f.s.agent.Runtime = appconfig.AgentRuntimeWorker
	handler := newHandler(f.s)
	t.Cleanup(func() { f.s.stopDeliveryScheduler(t.Context()) })
	f.groupSend(t, f.identity.OpenID, f.binding.ChatID, "om_worker_1", "how many issues are open?", "", "", true)
	claimed := runtimeJSON[struct {
		Runs []store.AgentRun `json:"runs"`
	}](t, handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/claim", worker: "w1", body: map[string]any{"free": map[string]int{"chat": 1}, "waitMs": 5000}}, http.StatusOK)
	if len(claimed.Runs) != 1 {
		t.Fatal("no worker run was queued")
	}
	run := claimed.Runs[0]
	payload := runtimeJSON[map[string]any](t, handler, runtimeCall{method: http.MethodGet, path: "/internal/agent-runtime/runs/" + run.ID + "/payload", worker: "w1"}, http.StatusOK)
	if raw, _ := json.Marshal(payload); !strings.Contains(string(raw), `Feishu group \"Web team\"`) {
		t.Errorf("the worker's prompt lacks the group: %.2000s", raw)
	}
	runtimeJSON[map[string]any](t, handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/activities", worker: "w1", body: map[string]any{"type": "response", "activityId": "end", "body": "No issues are open."}}, http.StatusOK)
	runtimeJSON[map[string]any](t, handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/complete", worker: "w1", body: map[string]any{"status": "done"}}, http.StatusOK)
	chatDMReplies.Wait()
	if sessions := f.sessions(t); len(sessions) != 1 || sessions[0].OriginChat != "Web team" || f.calls.Load() != 0 {
		t.Fatalf("worker chat: %+v", sessions)
	}
	if reply := f.sent()[0]; reply.ReplyTo != "om_worker_1" || !reply.InThread || !strings.Contains(f.lastUpdate(reply.ID), "No issues are open.") {
		t.Fatalf("worker reply card: %+v", reply)
	}
}
