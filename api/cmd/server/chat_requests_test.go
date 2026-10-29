package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// chatRequestMessage is a message the fake platform lets the bot read.
type chatRequestMessage struct{ chat, text, root string }

// requestPlatform fakes message reads on top of the thread platform: the
// messages it knows are readable, others are refused like a message the bot
// cannot see.
func (f *chatWorkflowFixture) requestPlatform(t *testing.T) (*chatThreadPlatform, map[string]chatRequestMessage) {
	t.Helper()
	platform := f.threadPlatform(t)
	messages := map[string]chatRequestMessage{}
	inner := http.DefaultTransport
	http.DefaultTransport = chatWorkflowTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/open-apis/im/v1/messages/") {
			id := strings.TrimPrefix(r.URL.Path, "/open-apis/im/v1/messages/")
			message, ok := messages[id]
			if !ok {
				return chatTestResponse(400, `{"code":230027,"msg":"Lack of necessary permissions"}`), nil
			}
			content, _ := json.Marshal(map[string]string{"text": message.text})
			body, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"items": []map[string]any{{"message_id": id, "chat_id": message.chat, "root_id": message.root, "msg_type": "text", "body": map[string]string{"content": string(content)}}}}})
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
		}
		return inner.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = inner })
	return platform, messages
}

func (f *chatWorkflowFixture) enableRequests(t *testing.T, requests *domain.ChatBindingRequests, change func(*domain.ChatBinding)) {
	t.Helper()
	f.binding.Requests = requests
	if change != nil {
		change(&f.binding)
	}
	if err := f.s.store.PutChatBinding(t.Context(), f.record.WorkspaceKey, f.binding); err != nil {
		t.Fatal(err)
	}
}

func (f *chatWorkflowFixture) useTriage(t *testing.T) {
	t.Helper()
	if err := f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "test.triage", f.binding.TeamID, nil, func(data *domain.Bootstrap) error {
		settings := data.TeamSettings[f.binding.TeamID]
		settings.TeamID, settings.TriageEnabled = f.binding.TeamID, true
		data.TeamSettings[f.binding.TeamID] = settings
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// mentionEnvelope is a group message mentioning the bot (@_user_1).
func (f *chatWorkflowFixture) mentionEnvelope(openID, messageID, text, parentID string) chatWebhookEnvelope {
	envelope := f.messageEnvelope()
	envelope.Header.EventID = "evt_" + messageID
	envelope.Event.Sender.SenderID.OpenID = openID
	envelope.Event.Message.MessageID = messageID
	envelope.Event.Message.ParentID = parentID
	envelope.Event.Message.Content = `{"text":` + string(must(json.Marshal("@_user_1 "+text))) + `}`
	return envelope
}

func (f *chatWorkflowFixture) reactionEnvelope(openID, messageID, emoji string) chatWebhookEnvelope {
	envelope := f.messageEnvelope()
	envelope.Header.EventType, envelope.Header.EventID = "im.message.reaction.created_v1", "evt_reaction_"+messageID+openID+emoji
	envelope.Event.Message = f.messageEnvelope().Event.Message
	envelope.Event.Message.MessageID, envelope.Event.Message.ChatID, envelope.Event.Message.Content, envelope.Event.Message.Mentions = "", "", "", nil
	envelope.Event.MessageID, envelope.Event.OperatorType = messageID, "user"
	envelope.Event.ReactionType.EmojiType, envelope.Event.UserID.OpenID = emoji, openID
	return envelope
}

// accept delivers an event through the webhook and reports whether it
// queued work.
func (f *chatWorkflowFixture) accept(t *testing.T, envelope chatWebhookEnvelope) bool {
	t.Helper()
	_, _, accepted, err := f.s.chatCommandForEnvelope(t.Context(), f.record, envelope)
	if err != nil {
		t.Fatal(err)
	}
	if response := f.callback(t, envelope, time.Now(), true); response.Code != http.StatusOK {
		t.Fatalf("callback: %d %s", response.Code, response.Body.String())
	}
	return accepted
}

func chatRequestIssueID(f *chatWorkflowFixture, messageID string) string {
	return "issue_chat_" + chatKey(f.record.ConnectionID, "request", messageID)
}

func (f *chatWorkflowFixture) asksFor(t *testing.T, issueID string) []domain.Ask {
	t.Helper()
	data, ok := f.s.store.BootstrapFor(f.record.WorkspaceKey)
	if !ok {
		t.Fatal("workspace missing")
	}
	return slices.DeleteFunc(slices.Clone(data.Asks), func(ask domain.Ask) bool { return ask.IssueID != issueID })
}

func (f *chatWorkflowFixture) linkMember(t *testing.T, email, openID string, guest bool) domain.ChatIdentity {
	t.Helper()
	data := f.s.store.Bootstrap()
	member, _, err := f.s.store.LoginExternal(t.Context(), email, "Member "+openID, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.EnsureWorkspaceMembership(t.Context(), data.Workspace.ID, member.User.ID); err != nil {
		t.Fatal(err)
	}
	if guest {
		if err := f.s.store.UpdateMemberRole(t.Context(), data.Workspace.ID, member.User.ID, "guest"); err != nil {
			t.Fatal(err)
		}
		// A guest outside the bound team.
		if err := f.s.store.SetTeamMembership(t.Context(), data.Workspace.ID, f.binding.TeamID, member.User.ID, "", false); err != nil {
			t.Fatal(err)
		}
	}
	identity := domain.ChatIdentity{UserID: member.User.ID, OpenID: openID, TenantKey: f.record.TenantKey, AuthProvider: "password", AuthenticatedAt: time.Now().UTC()}
	if err := f.s.store.PutChatIdentity(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, identity); err != nil {
		t.Fatal(err)
	}
	return identity
}

// "@Flow 提需求 …" lands in the team's triage as the member, as an Ask with
// its issue, and the confirmation replies in the message's thread.
func TestChatRequestFromAMentionLandsInTriageAndRepliesInTheThread(t *testing.T) {
	f := newChatWorkflowFixture(t)
	platform, messages := f.requestPlatform(t)
	priority := 1
	f.enableRequests(t, &domain.ChatBindingRequests{Enabled: true, Emoji: "OK", Priority: &priority}, nil)
	f.useTriage(t)

	if !f.accept(t, f.mentionEnvelope(f.identity.OpenID, "om_request_mention", "提需求 Export fails on Safari\nOpen the report and export it.", "")) {
		t.Fatal("request mention not accepted")
	}
	// Redelivered by the platform: one job.
	f.callback(t, f.mentionEnvelope(f.identity.OpenID, "om_request_mention", "提需求 Export fails on Safari", ""), time.Now(), true)
	f.runJobs(t, "inbound")
	issueID := chatRequestIssueID(f, "om_request_mention")
	issue, err := f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, issueID)
	if err != nil {
		t.Fatalf("request issue: %v", err)
	}
	data := f.s.store.Bootstrap()
	if issue.Creator.ID != f.identity.UserID || issue.Team.ID != f.binding.TeamID || issue.Title != "Export fails on Safari" || issue.Priority != 1 || !isTriageIssue(&data, &issue) || !strings.Contains(issue.Description, "Open the report") || !strings.Contains(issue.Description, "openChatId="+f.binding.ChatID) {
		t.Fatalf("request issue not in triage as the member: %+v", issue)
	}
	asks := f.asksFor(t, issueID)
	if len(asks) != 1 || asks[0].Source != "feishu" || asks[0].Requester.ID != f.identity.UserID || asks[0].TeamID != f.binding.TeamID || asks[0].Status != "approved" {
		t.Fatalf("request record: %+v", asks)
	}
	request, err := f.s.store.ChatIntakeRequestForMessage(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, "om_request_mention")
	if err != nil || request.Status != domain.ChatRequestCreated || request.IssueID != issueID || request.AskID != asks[0].ID || request.Source != domain.ChatRequestSourceMention {
		t.Fatalf("intake record: %+v %v", request, err)
	}
	thread := f.thread(t, issueID)
	if thread.RootMessageID != "om_request_mention" || thread.Origin != "request" || thread.BindingID != f.binding.ID {
		t.Fatalf("message thread not the request's: %+v", thread)
	}

	calls := len(platform.calls)
	f.runJobs(t, "card")
	confirmations := chatCallsMention(platform.since(calls), "已收到需求")
	if len(confirmations) != 1 || chatThreadReplyTo(confirmations[0]) != "om_request_mention" || confirmations[0].body["reply_in_thread"] != true || !strings.Contains(confirmations[0].body["content"].(string), issue.Identifier) {
		t.Fatalf("confirmation not in the message's thread: %#v", platform.since(calls))
	}
	// The issue.created notification does not post a second card.
	calls = len(platform.calls)
	f.deliver(t, domain.DomainEvent{ID: "evt_request_created", Type: "issue.created", AggregateID: issueID})
	if len(platform.since(calls)) != 0 {
		t.Fatalf("request announced twice: %#v", platform.since(calls))
	}

	// Asking again for the same message, another way and by another member,
	// makes no second request; an explicit ask hears about the first.
	other := f.linkMember(t, "chat-request-other@example.test", "ou_request_other", false)
	reply := f.mentionEnvelope(other.OpenID, "om_request_again", "提需求", "om_request_mention")
	if !f.accept(t, reply) {
		t.Fatal("bare request reply not accepted")
	}
	messages["om_request_mention"] = chatRequestMessage{chat: f.binding.ChatID, text: "Export fails on Safari"}
	f.runJobs(t, "inbound")
	if text := f.lastText(t); !strings.Contains(text, issue.Identifier) || f.sent[len(f.sent)-1]["receive_id"] != other.OpenID {
		t.Fatalf("second ask not answered privately with the request: %#v", f.sent[len(f.sent)-1])
	}
	if len(f.asksFor(t, issueID)) != 1 {
		t.Fatal("second request recorded for the same message")
	}

	// Any other mention becomes a request in a group that takes requests; a
	// create command keeps its meaning.
	command, _, accepted, err := f.s.chatCommandForEnvelope(t.Context(), f.record, f.mentionEnvelope(f.identity.OpenID, "om_implicit", "the login page is slow", ""))
	if err != nil || !accepted || command.Kind != chatRequestKind || chatString(command.Values, "text") != "the login page is slow" {
		t.Fatalf("implicit request: %+v %v %v", command, accepted, err)
	}
	command, _, accepted, _ = f.s.chatCommandForEnvelope(t.Context(), f.record, f.mentionEnvelope(f.identity.OpenID, "om_create", "创建事项 Fix export", ""))
	if !accepted || command.Kind != "create_form" {
		t.Fatalf("create command became a request: %+v", command)
	}
}

// The binding's reaction turns a message into a request; other reactions,
// the bot's and repeated ones do not. Unlinked members get the link card.
func TestChatRequestFromAReactionUsesTheConfiguredEmoji(t *testing.T) {
	f := newChatWorkflowFixture(t)
	platform, messages := f.requestPlatform(t)
	f.enableRequests(t, &domain.ChatBindingRequests{Enabled: true, Emoji: "DONE"}, nil)
	messages["om_reacted"] = chatRequestMessage{chat: f.binding.ChatID, text: "Login button does nothing on iPad", root: "om_discussion_root"}

	if f.accept(t, f.reactionEnvelope(f.identity.OpenID, "om_reacted", "OK")) {
		t.Fatal("an emoji the binding does not take was accepted")
	}
	if f.accept(t, f.reactionEnvelope(f.record.BotOpenID, "om_reacted", "DONE")) {
		t.Fatal("the bot's own reaction was accepted")
	}
	// An unlinked member's reaction: the link card, no request.
	if !f.accept(t, f.reactionEnvelope("ou_request_unlinked", "om_reacted", "DONE")) {
		t.Fatal("reaction not accepted")
	}
	f.runJobs(t, "inbound")
	if links := chatCallsMention(platform.calls, "Connect account"); len(links) != 1 || links[0].body["receive_id"] != "ou_request_unlinked" {
		t.Fatalf("link card: %#v", platform.calls)
	}
	if _, err := f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, chatRequestIssueID(f, "om_reacted")); err == nil {
		t.Fatal("unlinked reaction created a request")
	}

	if !f.accept(t, f.reactionEnvelope(f.identity.OpenID, "om_reacted", "DONE")) {
		t.Fatal("reaction not accepted")
	}
	f.runJobs(t, "inbound")
	issueID := chatRequestIssueID(f, "om_reacted")
	issue, err := f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, issueID)
	if err != nil || issue.Creator.ID != f.identity.UserID || issue.Title != "Login button does nothing on iPad" {
		t.Fatalf("reaction request: %+v %v", issue, err)
	}
	// The message is in a discussion thread: that thread carries the request.
	if thread := f.thread(t, issueID); thread.RootMessageID != "om_discussion_root" {
		t.Fatalf("request thread root: %+v", thread)
	}
	calls := len(platform.calls)
	f.runJobs(t, "card")
	if confirmations := chatCallsMention(platform.since(calls), "已收到需求"); len(confirmations) != 1 || chatThreadReplyTo(confirmations[0]) != "om_discussion_root" {
		t.Fatalf("confirmation: %#v", platform.since(calls))
	}

	// Another member's reaction on the same message: nothing new, silently.
	other := f.linkMember(t, "chat-reaction-other@example.test", "ou_reaction_other", false)
	calls = len(platform.calls)
	f.accept(t, f.reactionEnvelope(other.OpenID, "om_reacted", "DONE"))
	f.runJobs(t, "inbound")
	if len(platform.since(calls)) != 0 || len(f.asksFor(t, issueID)) != 1 {
		t.Fatalf("second reaction: %#v", platform.since(calls))
	}

	// A reaction to a message the bot cannot read does nothing.
	calls = len(platform.calls)
	f.accept(t, f.reactionEnvelope(f.identity.OpenID, "om_unreadable", "DONE"))
	f.runJobs(t, "inbound")
	if len(platform.since(calls)) != 0 {
		t.Fatalf("unreadable message: %#v", platform.since(calls))
	}
}

// Requests are off unless the binding takes them; members who cannot create
// issues in the team are told privately and the message stays free.
func TestChatRequestRefusalsFlagOffAndPermission(t *testing.T) {
	f := newChatWorkflowFixture(t)
	platform, messages := f.requestPlatform(t)
	messages["om_flag_off"] = chatRequestMessage{chat: f.binding.ChatID, text: "Something"}

	// Off: an explicit request is refused privately; a plain mention and a
	// reaction are not requests at all.
	if !f.accept(t, f.mentionEnvelope(f.identity.OpenID, "om_explicit_off", "提需求 Dark mode", "")) {
		t.Fatal("explicit request not accepted")
	}
	f.runJobs(t, "inbound")
	if text := f.lastText(t); !strings.Contains(text, "chat_requests_disabled") || f.sent[len(f.sent)-1]["receive_id"] != f.identity.OpenID {
		t.Fatalf("requests-off refusal: %s", text)
	}
	if f.accept(t, f.mentionEnvelope(f.identity.OpenID, "om_plain_off", "hello there", "")) || f.accept(t, f.reactionEnvelope(f.identity.OpenID, "om_flag_off", "OK")) {
		t.Fatal("a mention or reaction became a request with requests off")
	}
	// A binding that names an emoji but is disabled takes no reaction.
	f.enableRequests(t, &domain.ChatBindingRequests{Enabled: false, Emoji: "OK"}, nil)
	if f.accept(t, f.reactionEnvelope(f.identity.OpenID, "om_flag_off", "OK")) {
		t.Fatal("disabled requests accepted a reaction")
	}

	// On, but the member is a guest outside the team.
	f.enableRequests(t, &domain.ChatBindingRequests{Enabled: true, Emoji: "OK"}, nil)
	guest := f.linkMember(t, "chat-request-guest@example.test", "ou_request_guest", true)
	f.accept(t, f.mentionEnvelope(guest.OpenID, "om_guest_request", "提需求 Let me in", ""))
	f.runJobs(t, "inbound")
	if text := f.lastText(t); !strings.Contains(text, "chat_request_forbidden") || f.sent[len(f.sent)-1]["receive_id"] != guest.OpenID {
		t.Fatalf("permission refusal: %#v ", f.sent[len(f.sent)-1])
	}
	if _, err := f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, chatRequestIssueID(f, "om_guest_request")); err == nil {
		t.Fatal("a member without permission created a request")
	}
	if _, err := f.s.store.ChatIntakeRequestForMessage(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, "om_guest_request"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("refused request kept the message: %v", err)
	}
	// An unlinked member's mention gets the link card.
	calls := len(platform.calls)
	f.accept(t, f.mentionEnvelope("ou_mention_unlinked", "om_unlinked_request", "提需求 Help", ""))
	f.runJobs(t, "inbound")
	if links := chatCallsMention(platform.since(calls), "Connect account"); len(links) != 1 || links[0].body["receive_id"] != "ou_mention_unlinked" {
		t.Fatalf("unlinked mention: %#v", platform.since(calls))
	}
	// Sensitive data protection keeps the group out: an explicit request is
	// refused.
	if err := f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "test.hipaa", "", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.HIPAACompliance = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.accept(t, f.mentionEnvelope(f.identity.OpenID, "om_hipaa_request", "提需求 Secret", ""))
	f.runJobs(t, "inbound")
	if text := f.lastText(t); !strings.Contains(text, "chat_requests_disabled") {
		t.Fatalf("sensitive data protection: %s", text)
	}
	if _, err := f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, chatRequestIssueID(f, "om_hipaa_request")); err == nil {
		t.Fatal("request created under sensitive data protection")
	}
}

// The request's thread syncs both ways whatever the binding's notifications:
// replies become comments (when the binding allows updates), Flow comments
// are posted back, status changes post a note and closing replies with the
// outcome once.
func TestChatRequestThreadSyncsCommentsStatusAndOutcome(t *testing.T) {
	f := newChatWorkflowFixture(t)
	platform, _ := f.requestPlatform(t)
	f.enableRequests(t, &domain.ChatBindingRequests{Enabled: true}, func(binding *domain.ChatBinding) { binding.Events = []string{} })
	f.accept(t, f.mentionEnvelope(f.identity.OpenID, "om_sync_request", "提需求 Billing export is empty", ""))
	f.runJobs(t, "inbound")
	f.runJobs(t, "card")
	issueID := chatRequestIssueID(f, "om_sync_request")

	// A member's reply in the thread becomes their comment.
	f.callback(t, f.replyEnvelope(f.identity.OpenID, "om_sync_request", "om_sync_reply", "Also happens for March"), time.Now(), true)
	f.runJobs(t, "inbound")
	comments := f.comments(t, issueID)
	if len(comments) != 1 || comments[0].User.ID != f.identity.UserID || comments[0].Body != "Also happens for March" {
		t.Fatalf("reply not a comment: %+v", comments)
	}
	calls := len(platform.calls)
	f.deliver(t, domain.DomainEvent{ID: "evt_sync_imported", Type: "comment.created", AggregateID: issueID})
	if echoed := chatCallsMention(platform.since(calls), "Also happens for March"); len(echoed) != 0 {
		t.Fatalf("imported reply echoed: %#v", echoed)
	}
	// A Flow comment goes back to the thread.
	f.addComment(t, issueID, "Looking into it")
	calls = len(platform.calls)
	f.deliver(t, domain.DomainEvent{ID: "evt_sync_written", Type: "comment.created", AggregateID: issueID})
	if posted := chatCallsMention(platform.since(calls), "Looking into it"); len(posted) != 1 || chatThreadReplyTo(posted[0]) != "om_sync_request" {
		t.Fatalf("Flow comment not in the thread: %#v", platform.since(calls))
	}

	setState := func(stateType, eventID string) domain.Issue {
		t.Helper()
		data := f.s.store.Bootstrap()
		state := stateForTeamByType(&data, f.binding.TeamID, stateType)
		if state == nil {
			t.Fatalf("no %s state", stateType)
		}
		var issue domain.Issue
		ctx := withChatIdentityAuthentication(t.Context(), f.identity)
		if err := f.s.chatAuthorizedMutation(ctx, f.record.WorkspaceKey, f.identity.UserID, http.MethodPatch, "/api/issue-records/"+issueID, domain.IssueUpdateInput{StateID: &state.ID}, &issue); err != nil {
			t.Fatal(err)
		}
		f.deliver(t, domain.DomainEvent{ID: eventID, Type: "issue.updated", AggregateID: issueID, PreviousValues: json.RawMessage(`{"state":{}}`)})
		return issue
	}
	calls = len(platform.calls)
	started := setState("started", "evt_sync_started")
	notes := chatCallsMention(platform.since(calls), "状态已更新为："+started.State.Name)
	if len(notes) != 1 || chatThreadReplyTo(notes[0]) != "om_sync_request" {
		t.Fatalf("status note: %#v", platform.since(calls))
	}
	calls = len(platform.calls)
	completed := setState("completed", "evt_sync_completed")
	outcome := chatCallsMention(platform.since(calls), "需求已完成："+completed.Identifier)
	if len(outcome) != 1 || chatThreadReplyTo(outcome[0]) != "om_sync_request" || !strings.Contains(outcome[0].body["content"].(string), "/issue/"+completed.Identifier) {
		t.Fatalf("outcome reply: %#v", platform.since(calls))
	}
	if request, _ := f.s.store.ChatIntakeRequestForMessage(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, "om_sync_request"); request.Outcome != "completed" {
		t.Fatalf("outcome not recorded: %+v", request)
	}
	// The same outcome again (a replayed event) is not repeated; reopening
	// says so.
	calls = len(platform.calls)
	f.deliver(t, domain.DomainEvent{ID: "evt_sync_completed_again", Type: "issue.updated", AggregateID: issueID, PreviousValues: json.RawMessage(`{"state":{}}`)})
	if len(chatCallsMention(platform.since(calls), "需求已完成")) != 0 {
		t.Fatalf("outcome repeated: %#v", platform.since(calls))
	}
	calls = len(platform.calls)
	setState("started", "evt_sync_reopened")
	if len(chatCallsMention(platform.since(calls), "需求已重新打开")) != 1 {
		t.Fatalf("reopen note: %#v", platform.since(calls))
	}

	// Without the update option, replies stay in the group.
	f.enableRequests(t, f.binding.Requests, func(binding *domain.ChatBinding) { binding.AllowUpdate = false })
	f.callback(t, f.replyEnvelope(f.identity.OpenID, "om_sync_request", "om_sync_reply_off", "Not imported"), time.Now(), true)
	f.runJobs(t, "inbound")
	if got := f.comments(t, issueID); len(got) != 2 {
		t.Fatalf("reply imported without the update option: %+v", got)
	}
}

// The message shortcut offers Submit as request in a group that takes
// requests; the request follows the message's thread and the member hears
// back privately.
func TestChatRequestFromTheMessageShortcut(t *testing.T) {
	f := newChatWorkflowFixture(t)
	threads := f.threadPlatform(t)
	platform := newChatShortcutPlatform(t, f)
	platform.codes["code_actor"] = f.identity.OpenID
	platform.messages["om_shortcut_request"] = f.binding.ChatID
	platform.text = "Invoices show the wrong currency"
	f.enableRequests(t, &domain.ChatBindingRequests{Enabled: true}, nil)
	f.useTriage(t)

	f.shortcut(t, map[string]string{"code": "code_actor", "messageId": "om_shortcut_request", "chatId": f.binding.ChatID})
	f.runChatJobs(t)
	_, _, content := f.formAction(t)
	requestID := chatFormRequestAction(t, f, content)
	if !strings.Contains(content, "提交为需求") || !strings.Contains(content, `"name":"teamId"`) {
		t.Fatalf("form lacks the request choice: %s", content)
	}
	f.submitForm(t, requestID, map[string]string{"teamId": f.binding.TeamID, "title": "Invoices show the wrong currency", "description": "Seen on EU invoices", "priority": "0", "assigneeId": "__unassigned"})
	issueID := chatRequestIssueID(f, "om_shortcut_request")
	issue, err := f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, issueID)
	data := f.s.store.Bootstrap()
	if err != nil || issue.Creator.ID != f.identity.UserID || issue.Description != "Seen on EU invoices" || !isTriageIssue(&data, &issue) || len(f.asksFor(t, issueID)) != 1 {
		t.Fatalf("shortcut request: %+v %v", issue, err)
	}
	calls := len(threads.calls)
	f.runChatJobs(t)
	if confirmations := chatCallsMention(threads.since(calls), "已收到需求"); len(confirmations) != 1 || chatThreadReplyTo(confirmations[0]) != "om_shortcut_request" {
		t.Fatalf("thread confirmation: %#v", threads.since(calls))
	}
	// In the member's Flow language (English for the fixture's member).
	if text := f.lastText(t); !strings.Contains(text, "Request saved in Flow: "+issue.Identifier) {
		t.Fatalf("private confirmation: %s", text)
	}

	// The same message again: no second request.
	platform.codes["code_again"] = f.identity.OpenID
	f.shortcut(t, map[string]string{"code": "code_again", "messageId": "om_shortcut_request", "chatId": f.binding.ChatID, "content": "again"})
	jobs, _ := f.s.store.ClaimChatJobs(t.Context(), f.record.WorkspaceKey, "inbound", 10, time.Now().Add(time.Minute))
	for _, job := range jobs {
		_ = f.s.store.FinishChatJob(t.Context(), job, "completed", time.Time{}, "")
	}
	stored, _ := f.s.store.GetChatAction(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, requestID)
	stored.ID = requestID + "_again"
	if err := f.s.store.PutChatAction(t.Context(), stored); err != nil {
		t.Fatal(err)
	}
	f.submitForm(t, requestID+"_again", map[string]string{"title": "Again"})
	if text := f.lastText(t); !strings.Contains(text, "This message is already a request: "+issue.Identifier) {
		t.Fatalf("second request: %s", text)
	}

	// A group that takes requests but not issues: the form submits requests
	// only.
	f.enableRequests(t, f.binding.Requests, func(binding *domain.ChatBinding) { binding.AllowCreate = false })
	platform.messages["om_request_only"] = f.binding.ChatID
	platform.codes["code_only"] = f.identity.OpenID
	f.shortcut(t, map[string]string{"code": "code_only", "messageId": "om_request_only", "chatId": f.binding.ChatID})
	f.runChatJobs(t)
	_, action, content := f.formAction(t)
	if action.Kind != chatMessageRequestKind || strings.Contains(content, `"name":"teamId"`) || strings.Contains(content, "创建事项") || !strings.Contains(content, "提交 Flow 需求") {
		t.Fatalf("request-only form: %+v %s", action, content)
	}
}

// chatFormRequestAction finds the Submit as request action of a form card.
func chatFormRequestAction(t *testing.T, f *chatWorkflowFixture, content string) string {
	t.Helper()
	for _, match := range chatActionIDPattern.FindAllStringSubmatch(content, -1) {
		stored, err := f.s.store.GetChatAction(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, match[1])
		var action chatExtendedAction
		if err == nil && json.Unmarshal(stored.Payload, &action) == nil && action.Kind == chatMessageRequestKind {
			return match[1]
		}
	}
	t.Fatalf("form has no request action: %s", content)
	return ""
}

// An issue created with the message shortcut in a bound group keeps the
// message as its thread root: the creation reply and later notifications
// continue that thread, and issue.created posts no separate card.
func TestChatMessageShortcutIssueKeepsTheMessageThread(t *testing.T) {
	f := newChatWorkflowFixture(t)
	threads := f.threadPlatform(t)
	platform := newChatShortcutPlatform(t, f)
	platform.codes["code_actor"] = f.identity.OpenID
	platform.messages["om_thread_source"] = f.binding.ChatID
	platform.text = "Search ignores accents"
	f.shortcut(t, map[string]string{"code": "code_actor", "messageId": "om_thread_source", "chatId": f.binding.ChatID})
	f.runChatJobs(t)
	actionID, _, _ := f.formAction(t)
	f.submitForm(t, actionID, map[string]string{"teamId": f.binding.TeamID, "title": "Search ignores accents"})
	issueID := "issue_chat_" + chatKey(f.record.ConnectionID, actionID)
	thread := f.thread(t, issueID)
	if thread.RootMessageID != "om_thread_source" || thread.Origin != "message" {
		t.Fatalf("thread root not the message: %+v", thread)
	}
	// The issue.created notification may run before the creation reply: it
	// posts nothing of its own.
	calls := len(threads.calls)
	f.deliver(t, domain.DomainEvent{ID: "evt_shortcut_created", Type: "issue.created", AggregateID: issueID})
	if len(threads.since(calls)) != 0 {
		t.Fatalf("issue.created posted a separate card: %#v", threads.since(calls))
	}
	f.runChatJobs(t)
	f.notify(t, "issue.status_changed", issueID, "shortcut_state")
	for _, call := range threads.since(calls) {
		if call.body["receive_id"] == f.binding.ChatID || call.body["reply_in_thread"] == true && chatThreadReplyTo(call) != "om_thread_source" {
			t.Fatalf("message left the thread: %#v", call)
		}
	}
	if replies := slices.DeleteFunc(slices.Clone(threads.since(calls)), func(call chatThreadCall) bool { return chatThreadReplyTo(call) != "om_thread_source" }); len(replies) < 2 {
		t.Fatalf("creation reply and notification not in the thread: %#v", threads.since(calls))
	}
}

// Direct messages: a chat the member had in Lark is read on their Agent page,
// and a pasted link gets no bot card once the platform previews links.
func TestChatDirectMessageReadStateAndPlatformLinkPreview(t *testing.T) {
	f := newChatDMFixture(t, func(int, string) string { return chatDMTextTurn("Here you go.") })
	f.text(t, "om_read_1", "Summarize my week")
	chatDMReplies.Wait()
	sessions := f.sessions(t)
	if len(sessions) != 1 || len(sessions[0].Messages) != 2 || !agentSessionRead(sessions[0]) {
		t.Fatalf("chat from the DM is unread: %+v", sessions)
	}

	issue := f.s.store.Bootstrap().Issues[0]
	link := f.s.chatAppOrigin() + "/" + f.record.WorkspaceKey + "/issue/" + issue.Identifier
	preview := map[string]any{"schema": "2.0", "header": map[string]any{"app_id": f.record.AppID, "tenant_key": f.record.TenantKey, "token": f.credentials.VerificationToken, "event_id": "evt_preview", "event_type": "url.preview.get"}, "event": map[string]any{"operator": map[string]string{"tenant_key": f.record.TenantKey, "open_id": f.identity.OpenID}, "context": map[string]string{"url": link, "open_chat_id": "oc_dm_test"}}}
	if response := f.callback(t, preview, time.Now(), true); response.Code != http.StatusOK {
		t.Fatalf("preview callback: %d", response.Code)
	}
	if !f.s.chatLinkPreviewConfigured(t.Context(), f.record) {
		t.Fatal("link preview requests not recorded")
	}
	before := len(f.sent())
	f.text(t, "om_link_preview", link)
	if len(f.sent()) != before {
		t.Fatalf("bot preview card repeated the platform's preview: %+v", f.sent()[before:])
	}
}
