package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Direct messages with the bot, played against a fake chat platform (sends,
// replies, card updates and message resources) and a scripted model.

const chatDMTestChat = "oc_dm_test"

type chatDMMessage struct {
	ID        string
	ReplyTo   string
	ReceiveID string
	Card      map[string]any
	Raw       string
}

type chatDMFixture struct {
	*chatWorkflowFixture
	mu        sync.Mutex
	messages  []chatDMMessage
	updates   map[string][]string
	resources map[string][]byte
	events    int
	provider  *httptest.Server
	calls     atomic.Int32
	inputs    []string
}

// newChatDMFixture: a connected Feishu app with the workspace viewer linked
// (ou_actor_test) and a model whose turns script answers.
func newChatDMFixture(t *testing.T, script func(call int, input string) string) *chatDMFixture {
	t.Helper()
	// Connector clients clone the default transport, so it stays an *http.Transport:
	// the fake platform answers its https scheme.
	original, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatal("the default transport was replaced")
	}
	base := newChatWorkflowFixture(t)
	f := &chatDMFixture{chatWorkflowFixture: base, updates: map[string][]string{}, resources: map[string][]byte{}}
	previousEvery, previousCheck := chatDMUpdateEvery, chatDMLinkCheckEvery
	chatDMUpdateEvery, chatDMLinkCheckEvery = 5*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { chatDMUpdateEvery, chatDMLinkCheckEvery = previousEvery, previousCheck })
	previous := http.DefaultTransport
	transport := original.Clone()
	transport.ForceAttemptHTTP2, transport.TLSNextProto = false, map[string]func(string, *tls.Conn) http.RoundTripper{}
	transport.RegisterProtocol("https", chatWorkflowTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() != "open.feishu.cn" && r.URL.Hostname() != "open.larksuite.com" {
			return nil, http.ErrSkipAltProtocol
		}
		respond := func(status int, contentType, body string) (*http.Response, error) {
			header := http.Header{}
			header.Set("Content-Type", contentType)
			return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		}
		path := r.URL.Path
		switch {
		case strings.Contains(path, "tenant_access_token"):
			return respond(200, "application/json", `{"code":0,"tenant_access_token":"dm_token","expire":7200}`)
		case strings.Contains(path, "/resources/") && r.Method == http.MethodGet:
			key := path[strings.LastIndex(path, "/")+1:]
			f.mu.Lock()
			content, ok := f.resources[key]
			f.mu.Unlock()
			if !ok {
				return respond(400, "application/json", `{"code":234003,"msg":"resource not found"}`)
			}
			return respond(200, "application/octet-stream", string(content))
		case strings.HasPrefix(path, "/open-apis/im/v1/messages/") && r.Method == http.MethodPatch:
			var payload struct {
				Content string `json:"content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			id := strings.TrimPrefix(path, "/open-apis/im/v1/messages/")
			f.mu.Lock()
			f.updates[id] = append(f.updates[id], payload.Content)
			f.mu.Unlock()
			return respond(200, "application/json", `{"code":0}`)
		case strings.HasPrefix(path, "/open-apis/im/v1/messages") && r.Method == http.MethodPost:
			raw, _ := io.ReadAll(r.Body)
			var payload struct {
				ReceiveID string `json:"receive_id"`
				MsgType   string `json:"msg_type"`
				Content   string `json:"content"`
				UUID      string `json:"uuid"`
			}
			_ = json.Unmarshal(raw, &payload)
			message := chatDMMessage{ReceiveID: payload.ReceiveID, Raw: payload.Content}
			if strings.HasSuffix(path, "/reply") {
				message.ReplyTo = strings.TrimSuffix(strings.TrimPrefix(path, "/open-apis/im/v1/messages/"), "/reply")
			}
			if payload.MsgType == "interactive" {
				if len(payload.Content) > 30<<10 {
					t.Errorf("card exceeds the platform limit: %d bytes", len(payload.Content))
				}
				_ = json.Unmarshal([]byte(payload.Content), &message.Card)
			}
			f.mu.Lock()
			// The platform answers a repeated uuid with the same message.
			for _, existing := range f.messages {
				if existing.ID == "om_dm_"+payload.UUID {
					f.mu.Unlock()
					return respond(200, "application/json", fmt.Sprintf(`{"code":0,"data":{"message_id":%q,"chat_id":%q}}`, existing.ID, chatDMTestChat))
				}
			}
			message.ID = "om_dm_" + payload.UUID
			f.messages = append(f.messages, message)
			f.mu.Unlock()
			return respond(200, "application/json", fmt.Sprintf(`{"code":0,"data":{"message_id":%q,"chat_id":%q}}`, message.ID, chatDMTestChat))
		}
		return respond(200, "application/json", `{"code":0}`)
	}))
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previous })
	if script != nil {
		f.provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			call := int(f.calls.Add(1))
			raw, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.inputs = append(f.inputs, string(raw))
			f.mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(script(call, string(raw))))
		}))
		t.Cleanup(f.provider.Close)
		f.s.agent = appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: f.provider.URL, Model: "flow-test", MaxOutputTokens: 256, ToolsEnabled: true, WriteTools: true}
		f.s.agentClient = f.provider.Client()
	}
	t.Cleanup(func() { chatDMReplies.Wait() })
	return f
}

func chatDMToolTurn(name, arguments string) string {
	args, _ := json.Marshal(arguments)
	return "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"item_1\",\"call_id\":\"call_1\",\"type\":\"function_call\",\"name\":\"" + name + "\",\"arguments\":\"\"}}\n\n" +
		"event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"item_1\",\"arguments\":" + string(args) + "}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
}

func chatDMTextTurn(text string) string {
	delta, _ := json.Marshal(text)
	return "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":" + string(delta) + "}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
}

// send delivers a direct message from openID through the signed webhook and
// processes the chat queue.
func (f *chatDMFixture) send(t *testing.T, openID, messageID, messageType, content, rootID, parentID string) {
	t.Helper()
	envelope := f.messageEnvelope()
	f.events++
	envelope.Header.EventID = fmt.Sprintf("evt_dm_%d", f.events)
	envelope.Event.Sender.SenderID.OpenID = openID
	envelope.Event.Message.MessageID = messageID
	envelope.Event.Message.ChatID = chatDMTestChat
	envelope.Event.Message.ChatType = "p2p"
	envelope.Event.Message.MessageType = messageType
	envelope.Event.Message.Content = content
	envelope.Event.Message.RootID = rootID
	envelope.Event.Message.ParentID = parentID
	envelope.Event.Message.Mentions = nil
	if got := f.callback(t, envelope, time.Now(), true); got.Code != http.StatusOK {
		t.Fatalf("direct message callback: %d %s", got.Code, got.Body.String())
	}
	f.process(t)
}

func (f *chatDMFixture) text(t *testing.T, messageID, text string) {
	t.Helper()
	content, _ := json.Marshal(map[string]string{"text": text})
	f.send(t, f.identity.OpenID, messageID, "text", string(content), "", "")
}

func (f *chatDMFixture) reply(t *testing.T, messageID, text, rootID, parentID string) {
	t.Helper()
	content, _ := json.Marshal(map[string]string{"text": text})
	f.send(t, f.identity.OpenID, messageID, "text", string(content), rootID, parentID)
}

// click presses a card button (or submits its form) as the member.
func (f *chatDMFixture) click(t *testing.T, message chatDMMessage, label string, form map[string]string) {
	t.Helper()
	actionID := chatDMButtonAction(message.Card, label)
	if actionID == "" {
		t.Fatalf("no %q button on card %s", label, message.Raw)
	}
	var envelope chatWebhookEnvelope
	envelope.Schema = "2.0"
	envelope.Header.AppID, envelope.Header.TenantKey, envelope.Header.Token = f.record.AppID, f.record.TenantKey, f.credentials.VerificationToken
	f.events++
	envelope.Header.EventID = fmt.Sprintf("evt_dm_%d", f.events)
	envelope.Header.EventType = "card.action.trigger"
	envelope.Event.Operator.TenantKey, envelope.Event.Operator.OpenID = f.record.TenantKey, f.identity.OpenID
	envelope.Event.Context.MessageID, envelope.Event.Context.ChatID = message.ID, chatDMTestChat
	raw, _ := json.Marshal(actionID)
	envelope.Event.Action.Value = map[string]json.RawMessage{"actionId": raw}
	envelope.Event.Action.FormValue = map[string]json.RawMessage{}
	for key, value := range form {
		raw, _ := json.Marshal(value)
		envelope.Event.Action.FormValue[key] = raw
	}
	if got := f.callback(t, envelope, time.Now(), true); got.Code != http.StatusOK || strings.Contains(got.Body.String(), "expired") {
		t.Fatalf("card callback: %d %s", got.Code, got.Body.String())
	}
	f.process(t)
}

func (f *chatDMFixture) process(t *testing.T) {
	t.Helper()
	if err := f.s.processChatJobs(t.Context(), f.record.WorkspaceKey); err != nil {
		t.Fatal(err)
	}
}

func chatDMButtonAction(value any, label string) string {
	switch value := value.(type) {
	case map[string]any:
		if value["tag"] == "button" {
			if text, _ := value["text"].(map[string]any); text != nil && text["content"] == label {
				if action, _ := value["value"].(map[string]any); action != nil {
					id, _ := action["actionId"].(string)
					return id
				}
			}
		}
		for _, nested := range value {
			if id := chatDMButtonAction(nested, label); id != "" {
				return id
			}
		}
	case []any:
		for _, nested := range value {
			if id := chatDMButtonAction(nested, label); id != "" {
				return id
			}
		}
	}
	return ""
}

func (f *chatDMFixture) sent() []chatDMMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.messages)
}

// lastUpdate is the latest content of a bot message (its last update, or as sent).
func (f *chatDMFixture) lastUpdate(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if updates := f.updates[id]; len(updates) > 0 {
		return updates[len(updates)-1]
	}
	for _, message := range f.messages {
		if message.ID == id {
			return message.Raw
		}
	}
	return ""
}

func (f *chatDMFixture) waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			f.mu.Lock()
			updates := fmt.Sprint(f.updates)
			f.mu.Unlock()
			t.Fatalf("timed out waiting for %s; sent %+v; updates %s", what, f.sent(), updates)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (f *chatDMFixture) sessions(t *testing.T) []domain.AgentSession {
	t.Helper()
	data, ok := f.s.store.BootstrapFor(f.record.WorkspaceKey)
	if !ok {
		t.Fatal("workspace missing")
	}
	return data.AgentSessions
}

func TestChatDirectMessageFromAnUnlinkedAccountGetsTheLinkCard(t *testing.T) {
	f := newChatDMFixture(t, func(int, string) string { return chatDMTextTurn("unexpected") })
	content, _ := json.Marshal(map[string]string{"text": "帮我看看我的事项"})
	f.send(t, "ou_stranger", "om_user_1", "text", string(content), "", "")
	sent := f.sent()
	if len(sent) != 1 || sent[0].ReplyTo != "om_user_1" || !strings.Contains(sent[0].Raw, "关联你的 Flow 账号") || !strings.Contains(sent[0].Raw, "/"+f.record.WorkspaceKey+"/settings/account/connections") || !strings.Contains(sent[0].Raw, "你自己的 Flow 权限") {
		t.Fatalf("link card: %+v", sent)
	}
	if f.calls.Load() != 0 || len(f.sessions(t)) != 0 {
		t.Fatalf("an unlinked account ran the agent: calls=%d sessions=%d", f.calls.Load(), len(f.sessions(t)))
	}
	// Group messages keep their own handling.
	envelope := f.messageEnvelope()
	if _, _, accepted, _ := f.s.chatCommandForEnvelope(t.Context(), f.record, envelope); !accepted {
		t.Fatal("a group command was not accepted")
	}
}

// A linked member's message is the Flow Agent as that member: an Agent chat
// marked as coming from the platform, answered from real tool calls into one
// card that is updated as it streams; replying to the card continues the chat.
func TestChatDirectMessageRunsTheAgentAndContinuesInTheThread(t *testing.T) {
	f := newChatDMFixture(t, func(call int, input string) string {
		switch {
		case call == 1:
			return chatDMToolTurn("list_issues", "{}")
		case call == 2:
			if !strings.Contains(input, "function_call_output") {
				t.Errorf("tool result missing from the continuation: %s", input)
			}
			return chatDMTextTurn("Found the issue.")
		case call == 3:
			if !strings.Contains(input, "Found the issue.") || !strings.Contains(input, "And the second one?") {
				t.Errorf("continuation lacks the conversation: %s", input)
			}
			return chatDMTextTurn("The second one is done.")
		default:
			return chatDMTextTurn("Something new.")
		}
	})
	f.text(t, "om_user_1", "Find my issues please")
	chatDMReplies.Wait()
	sessions := f.sessions(t)
	if len(sessions) != 1 || sessions[0].UserID != f.identity.UserID || sessions[0].Origin != "feishu" || len(sessions[0].Messages) != 2 {
		t.Fatalf("agent chat not saved as the member's: %+v", sessions)
	}
	answer := sessions[0].Messages[1]
	if answer.Content != "Found the issue." || len(answer.Parts) < 2 || answer.Parts[0].ToolCall == nil || answer.Parts[0].ToolCall.Status != "completed" {
		t.Fatalf("answer: %+v", answer)
	}
	sent := f.sent()
	if len(sent) != 1 || sent[0].ReplyTo != "om_user_1" {
		t.Fatalf("reply card: %+v", sent)
	}
	card := sent[0]
	final := f.lastUpdate(card.ID)
	if !strings.Contains(final, "Found the issue.") || !strings.Contains(final, "1 done") || !strings.Contains(final, "/agent/"+sessions[0].SlugID) || !strings.Contains(final, `"template":"green"`) {
		t.Fatalf("final card: %s", final)
	}
	if !strings.Contains(card.Raw, "Thinking") {
		t.Fatalf("first card: %s", card.Raw)
	}

	// Replying to the card continues the same chat.
	f.reply(t, "om_user_2", "And the second one?", "om_user_1", card.ID)
	chatDMReplies.Wait()
	sessions = f.sessions(t)
	if len(sessions) != 1 || len(sessions[0].Messages) != 4 || sessions[0].Messages[3].Content != "The second one is done." {
		t.Fatalf("the reply did not continue the chat: %+v", sessions)
	}
	sent = f.sent()
	if len(sent) != 2 || sent[1].ReplyTo != "om_user_2" || !strings.Contains(f.lastUpdate(sent[1].ID), "The second one is done.") {
		t.Fatalf("second reply card: %+v", sent)
	}
	// A message outside the thread starts a new chat.
	f.text(t, "om_user_3", "Something else")
	chatDMReplies.Wait()
	if sessions = f.sessions(t); len(sessions) != 2 {
		t.Fatalf("a new message did not start a new chat: %d", len(sessions))
	}
}

// A call that waits for approval gets a card; Approve resumes the reply.
func TestChatDirectMessageApprovalCardResumesTheReply(t *testing.T) {
	var connectorCalls, toolCount atomic.Int32
	toolCount.Store(3)
	upstream := fakeCodeMCP(t, &connectorCalls, &toolCount)
	defer upstream.Close()
	external := connectorToolName("code", "delete_branch")
	f := newChatDMFixture(t, func(call int, input string) string {
		if call == 1 {
			return chatDMToolTurn(external, `{"branch":"release","token":"sk-abcdefghijklmnopqrstuvwx"}`)
		}
		return chatDMTextTurn("Branch deleted.")
	})
	// Connectors on loopback test servers are reachable only in local development mode.
	f.s.authDisabled = true
	setTestConnectors(t, f.s.store, applicationPolicy{ID: "code", Kind: "mcp", Name: "Code search", URL: upstream.URL, Status: "approved", Shared: true, ToolApproval: connectorToolApprovalAlways})
	f.text(t, "om_user_1", "Delete the release branch")
	var approval chatDMMessage
	f.waitFor(t, "the approval card", func() bool {
		for _, message := range f.sent() {
			if strings.Contains(message.Raw, "Approval needed") {
				approval = message
				return true
			}
		}
		return false
	})
	if connectorCalls.Load() != 0 || approval.ReplyTo != "om_user_1" || strings.Contains(approval.Raw, "sk-abcdefghijklmnopqrstuvwx") || !strings.Contains(approval.Raw, "release") {
		t.Fatalf("approval card before the decision: calls=%d %+v", connectorCalls.Load(), approval)
	}
	reply := f.sent()[0]
	f.waitFor(t, "the waiting card", func() bool { return strings.Contains(f.lastUpdate(reply.ID), "waiting for approval") })
	f.click(t, approval, "Approve", nil)
	chatDMReplies.Wait()
	if connectorCalls.Load() != 1 {
		t.Fatalf("approved call did not run: %d", connectorCalls.Load())
	}
	if final := f.lastUpdate(reply.ID); !strings.Contains(final, "Branch deleted.") || !strings.Contains(final, "1 done") {
		t.Fatalf("final card: %s", final)
	}
	if card := f.lastUpdate(approval.ID); !strings.Contains(card, "Approved.") || strings.Contains(card, "actionId") {
		t.Fatalf("approval card after the decision: %s", card)
	}
	// The other button is spent.
	if id := chatDMButtonAction(approval.Card, "Reject"); id != "" {
		if _, err := f.s.store.GetChatAction(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, id); err == nil {
			t.Fatal("the reject action outlived the decision")
		}
	}
}

// Images and files of a message become the chat message's attachments, with
// the composer's limits.
func TestChatDirectMessageAttachmentsBecomeChatAttachments(t *testing.T) {
	f := newChatDMFixture(t, func(call int, input string) string { return chatDMTextTurn("I see a screenshot and notes.") })
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...)
	f.resources["img_key_1"] = png
	f.resources["file_key_1"] = []byte("release notes: ship on Friday")
	f.resources["file_key_big"] = bytes.Repeat([]byte("a"), agentAttachmentMaxBytes+1)
	post, _ := json.Marshal(map[string]any{"title": "", "content": [][]map[string]string{{{"tag": "text", "text": "What is in this screenshot?"}}, {{"tag": "img", "image_key": "img_key_1"}}}})
	f.send(t, f.identity.OpenID, "om_user_1", "post", string(post), "", "")
	chatDMReplies.Wait()
	sessions := f.sessions(t)
	if len(sessions) != 1 {
		t.Fatalf("sessions: %+v", sessions)
	}
	first := sessions[0].Messages[0]
	if first.Content != "What is in this screenshot?" || len(first.Attachments) != 1 || first.Attachments[0].ContentType != "image/png" || !strings.HasPrefix(first.Attachments[0].URL, "/uploads/agent_attachment_") {
		t.Fatalf("image attachment: %+v", first)
	}
	if !strings.Contains(strings.Join(f.inputs, ""), "data:image/png;base64") {
		t.Fatal("the image did not reach the model")
	}
	// A text file in the same thread.
	reply := f.sent()[0]
	file, _ := json.Marshal(map[string]string{"file_key": "file_key_1", "file_name": "notes.txt"})
	f.send(t, f.identity.OpenID, "om_user_2", "file", string(file), "om_user_1", reply.ID)
	chatDMReplies.Wait()
	sessions = f.sessions(t)
	if len(sessions) != 1 || len(sessions[0].Messages) != 4 || len(sessions[0].Messages[2].Attachments) != 1 || sessions[0].Messages[2].Attachments[0].Name != "notes.txt" {
		t.Fatalf("file attachment: %+v", sessions[0].Messages)
	}
	// Larger than 2 MB: refused like the composer refuses it.
	big, _ := json.Marshal(map[string]string{"file_key": "file_key_big", "file_name": "dump.log"})
	f.send(t, f.identity.OpenID, "om_user_3", "file", string(big), "", "")
	sent := f.sent()
	if last := sent[len(sent)-1]; !strings.Contains(last.Raw, "dump.log") || !strings.Contains(last.Raw, "2 MB") || len(f.sessions(t)) != 1 {
		t.Fatalf("oversized file: %+v", last)
	}
}

// A reply to a personal notification card comments on its issue, as the
// member.
func TestChatDirectMessageReplyToANotificationComments(t *testing.T) {
	f := newChatDMFixture(t, nil)
	data := f.s.store.Bootstrap()
	issue := data.Issues[0]
	chatPersonalPreferences(t, f.chatWorkflowFixture, data.Viewer.ID, &domain.NotificationChannelPreferences{Enabled: true})
	chatPersonalNotify(t, f.chatWorkflowFixture, data.Viewer.ID, "notification_dm_reply", "assignments", issue.ID)
	chatPersonalRun(t, f.chatWorkflowFixture)
	sent := f.sent()
	if len(sent) != 1 {
		t.Fatalf("notification card: %+v", sent)
	}
	f.reply(t, "om_user_reply", "Looks good, shipping today", sent[0].ID, sent[0].ID)
	comments := func() []domain.Comment {
		items, _, err := f.s.store.ResourceCommentsPage(t.Context(), f.record.WorkspaceKey, issue.ID, "", 100)
		if err != nil {
			t.Fatal(err)
		}
		mine := []domain.Comment{}
		for _, comment := range items {
			if comment.Body == "Looks good, shipping today" {
				mine = append(mine, comment)
			}
		}
		return mine
	}
	if found := comments(); len(found) != 1 || found[0].User.ID != data.Viewer.ID {
		t.Fatalf("no comment by the member on %s: %+v", issue.Identifier, found)
	}
	sent = f.sent()
	if last := sent[len(sent)-1]; last.ReplyTo != "om_user_reply" || !strings.Contains(last.Raw, "Comment added") || !strings.Contains(last.Raw, issue.Identifier) {
		t.Fatalf("comment confirmation: %+v", last)
	}
	// The same message delivered again does not comment twice.
	f.reply(t, "om_user_reply", "Looks good, shipping today", sent[0].ID, sent[0].ID)
	if count := len(comments()); count != 1 {
		t.Fatalf("comments: %d", count)
	}
}

func TestChatDirectMessageQuickCommands(t *testing.T) {
	f := newChatDMFixture(t, func(int, string) string { return chatDMTextTurn("agent") })
	data := f.s.store.Bootstrap()
	team := data.Teams[0]

	// 新建事项: a form that creates the issue as the member.
	f.text(t, "om_new", "新建事项 导出失败")
	form := f.sent()[0]
	if !strings.Contains(form.Raw, "新建事项") || !strings.Contains(form.Raw, `"name":"teamId"`) || !strings.Contains(form.Raw, `"name":"description"`) || !strings.Contains(form.Raw, `"name":"assigneeId"`) || !strings.Contains(form.Raw, `"name":"priority"`) || !strings.Contains(form.Raw, "导出失败") {
		t.Fatalf("new issue form: %s", form.Raw)
	}
	f.click(t, form, "创建事项", map[string]string{"teamId": team.ID, "title": "导出失败", "description": "CSV export times out", "assigneeId": data.Viewer.ID, "priority": "2"})
	after, _ := f.s.store.BootstrapFor(f.record.WorkspaceKey)
	index := slices.IndexFunc(after.Issues, func(issue domain.Issue) bool { return issue.Title == "导出失败" })
	if index < 0 {
		t.Fatal("the form did not create the issue")
	}
	created := after.Issues[index]
	if created.Team.ID != team.ID || created.Priority != 2 || created.Assignee == nil || created.Assignee.ID != data.Viewer.ID || created.Creator.ID != data.Viewer.ID {
		t.Fatalf("created issue: %+v", created)
	}
	if card := f.lastUpdate(form.ID); !strings.Contains(card, "已创建 "+created.Identifier) {
		t.Fatalf("form after creating: %s", card)
	}

	// my issues: the member's open assigned issues, linked.
	f.text(t, "om_mine", "my issues")
	mine := f.sent()[len(f.sent())-1]
	if !strings.Contains(mine.Raw, "My issues") || !strings.Contains(mine.Raw, created.Identifier) || !strings.Contains(mine.Raw, "/issue/"+created.Identifier) {
		t.Fatalf("my issues: %s", mine.Raw)
	}

	// 收件箱: the latest notifications.
	chatPersonalNotify(t, f.chatWorkflowFixture, data.Viewer.ID, "notification_inbox", "assignments", data.Issues[0].ID)
	f.text(t, "om_inbox", "收件箱")
	inbox := f.sent()[len(f.sent())-1]
	if !strings.Contains(inbox.Raw, "收件箱") || !strings.Contains(inbox.Raw, data.Issues[0].Identifier) {
		t.Fatalf("inbox: %s", inbox.Raw)
	}

	// A pasted Flow link: a preview of what the member can open.
	f.text(t, "om_link", f.s.chatAppOrigin()+"/"+f.record.WorkspaceKey+"/issue/"+created.Identifier)
	preview := f.sent()[len(f.sent())-1]
	if !strings.Contains(preview.Raw, created.Identifier+" 导出失败") || !strings.Contains(preview.Raw, "CSV export times out") {
		t.Fatalf("link preview: %s", preview.Raw)
	}
	f.text(t, "om_link_missing", f.s.chatAppOrigin()+"/"+f.record.WorkspaceKey+"/issue/NOPE-999")
	if missing := f.sent()[len(f.sent())-1]; !strings.Contains(missing.Raw, "can't open this link") {
		t.Fatalf("missing link: %s", missing.Raw)
	}
	if f.calls.Load() != 0 || len(f.sessions(t)) != 0 {
		t.Fatal("a quick command ran the agent")
	}
	// Anything else goes to the agent.
	f.text(t, "om_other", "summarize the week")
	chatDMReplies.Wait()
	if f.calls.Load() != 1 || len(f.sessions(t)) != 1 {
		t.Fatalf("other text did not reach the agent: %d", f.calls.Load())
	}
}

// A member linked in two workspaces of the tenant picks one on the first
// message (which then runs there) and can switch.
func TestChatDirectMessageWorkspaceChoiceAndSwitch(t *testing.T) {
	f := newChatDMFixture(t, func(int, string) string { return chatDMTextTurn("Answered.") })
	data := f.s.store.Bootstrap()
	ctx := store.ContextWithActor(t.Context(), data.Viewer)
	second, err := f.s.store.CreateWorkspace(ctx, "Second Space", "second-space", "us")
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
	// Another tenant's workspace is not offered.
	foreign := domain.ChatIntegrationRecord{WorkspaceKey: "foreign-space", Provider: "lark", ConnectionID: "chat_connection_foreign", AppID: "cli_foreign", TenantKey: "tenant_foreign", Connected: true}
	if _, err := f.s.store.CreateWorkspace(ctx, "Foreign", "foreign-space", "us"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.PutChatIntegration(t.Context(), foreign); err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.PutChatIdentity(t.Context(), foreign.WorkspaceKey, foreign.ConnectionID, domain.ChatIdentity{UserID: data.Viewer.ID, OpenID: "ou_actor_foreign", TenantKey: "tenant_foreign", AuthProvider: "password", AuthenticatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	f.text(t, "om_first", "What changed today?")
	choice := f.sent()[0]
	if !strings.Contains(choice.Raw, "Choose a workspace") || !strings.Contains(choice.Raw, "Second Space") || !strings.Contains(choice.Raw, data.Workspace.Name) || strings.Contains(choice.Raw, "Foreign") || f.calls.Load() != 0 {
		t.Fatalf("workspace choice: %s", choice.Raw)
	}
	f.click(t, choice, "Second Space", nil)
	chatDMReplies.Wait()
	if !strings.Contains(f.lastUpdate(choice.ID), "Second Space") || f.calls.Load() != 1 {
		t.Fatalf("after choosing: %s calls=%d", f.lastUpdate(choice.ID), f.calls.Load())
	}
	secondData, _ := f.s.store.BootstrapFor(second.Workspace.URLKey)
	if len(secondData.AgentSessions) != 1 || secondData.AgentSessions[0].Messages[0].Content != "What changed today?" || len(f.sessions(t)) != 0 {
		t.Fatalf("the pending message did not run in the chosen workspace: %+v", secondData.AgentSessions)
	}
	// Remembered: the next message runs there without asking.
	f.text(t, "om_second", "And yesterday?")
	chatDMReplies.Wait()
	if secondData, _ = f.s.store.BootstrapFor(second.Workspace.URLKey); len(secondData.AgentSessions) != 2 {
		t.Fatalf("the choice was not remembered: %d", len(secondData.AgentSessions))
	}
	// 切换工作区 asks again; the chosen workspace is used from then on.
	f.text(t, "om_switch", "切换工作区")
	switchCard := f.sent()[len(f.sent())-1]
	if !strings.Contains(switchCard.Raw, "选择工作区") {
		t.Fatalf("switch card: %s", switchCard.Raw)
	}
	f.click(t, switchCard, data.Workspace.Name, nil)
	f.text(t, "om_third", "Back home")
	chatDMReplies.Wait()
	if sessions := f.sessions(t); len(sessions) != 1 || sessions[0].Messages[0].Content != "Back home" {
		t.Fatalf("switch did not change the workspace: %+v", sessions)
	}
}

func TestChatDirectMessageRedactsSensitiveContent(t *testing.T) {
	for _, tc := range []struct{ in, gone string }{
		{"password: hunter22", "hunter22"},
		{"密码是 abc12345", "abc12345"},
		{"use sk-proj1234567890abcdefgh to call", "sk-proj1234567890abcdefgh"},
		{"token=flow_api_0123456789abcdef", "flow_api_0123456789abcdef"},
		{"Authorization: Bearer abcdefghijklmnop1234", "abcdefghijklmnop1234"},
		{"card 4111 1111 1111 1111 on file", "4111 1111 1111 1111"},
		{"ID 11010519491231002X", "11010519491231002X"},
		{"-----BEGIN RSA PRIVATE KEY-----\nMIIE\n-----END RSA PRIVATE KEY-----", "MIIE"},
	} {
		if got := chatDMRedact(tc.in, false); strings.Contains(got, tc.gone) || !strings.Contains(got, "[hidden]") {
			t.Errorf("redact %q = %q", tc.in, got)
		}
	}
	for _, keep := range []string{"FLOW-1234 is done", "order 1234 5678 9012 3456 shipped", "tokens: 1500"} {
		if got := chatDMRedact(keep, false); got != keep {
			t.Errorf("redact %q = %q", keep, got)
		}
	}
	// An answer with a secret reaches the card redacted; the chat keeps it.
	f := newChatDMFixture(t, func(int, string) string {
		return chatDMTextTurn("The deploy key is ghp_abcdefghijklmnopqrstuvwxyz0123 and password: s3cr3t!")
	})
	f.text(t, "om_user_1", "What's the deploy key?")
	chatDMReplies.Wait()
	card := f.lastUpdate(f.sent()[0].ID)
	if strings.Contains(card, "ghp_abcdefghijklmnopqrstuvwxyz0123") || strings.Contains(card, "s3cr3t") || !strings.Contains(card, "[hidden]") {
		t.Fatalf("card was not redacted: %s", card)
	}
	if sessions := f.sessions(t); !strings.Contains(sessions[0].Messages[1].Content, "ghp_abcdefghijklmnopqrstuvwxyz0123") {
		t.Fatal("the chat in Flow lost the answer")
	}
	// A workspace that keeps content out of chat apps answers nothing.
	if err := f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "workspace_settings.updated", "hipaa", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.HIPAACompliance = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.text(t, "om_user_2", "What's the deploy key again?")
	chatDMReplies.Wait()
	if last := f.sent()[len(f.sent())-1]; !strings.Contains(last.Raw, "keeps its content out of chat apps") || len(f.sessions(t)) != 1 {
		t.Fatalf("content left a protected workspace: %s", last.Raw)
	}
}

// Unlinking stops a running reply and every later message.
func TestChatDirectMessageUnlinkStopsEverything(t *testing.T) {
	var connectorCalls, toolCount atomic.Int32
	toolCount.Store(3)
	upstream := fakeCodeMCP(t, &connectorCalls, &toolCount)
	defer upstream.Close()
	external := connectorToolName("code", "delete_branch")
	f := newChatDMFixture(t, func(call int, input string) string {
		if call == 1 {
			return chatDMToolTurn(external, `{"branch":"main"}`)
		}
		return chatDMTextTurn("should not run")
	})
	// Connectors on loopback test servers are reachable only in local development mode.
	f.s.authDisabled = true
	setTestConnectors(t, f.s.store, applicationPolicy{ID: "code", Kind: "mcp", Name: "Code search", URL: upstream.URL, Status: "approved", Shared: true, ToolApproval: connectorToolApprovalAlways})
	f.text(t, "om_user_1", "Delete main")
	var approval chatDMMessage
	f.waitFor(t, "the approval card", func() bool {
		for _, message := range f.sent() {
			if strings.Contains(message.Raw, "Approval needed") {
				approval = message
				return true
			}
		}
		return false
	})
	if err := f.s.store.DeleteChatIdentity(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	chatDMReplies.Wait()
	reply := f.sent()[0]
	if final := f.lastUpdate(reply.ID); !strings.Contains(final, "no longer linked") {
		t.Fatalf("running reply after unlinking: %s", final)
	}
	if connectorCalls.Load() != 0 {
		t.Fatal("a held call ran after unlinking")
	}
	// The approval card's actions went with the account.
	if _, err := f.s.store.GetChatAction(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, chatDMButtonAction(approval.Card, "Approve")); err == nil {
		t.Fatal("the approval action outlived the link")
	}
	// Later messages get the link card.
	f.text(t, "om_user_2", "Are you there?")
	if last := f.sent()[len(f.sent())-1]; !strings.Contains(last.Raw, "Link your Flow account") || f.calls.Load() != 1 {
		t.Fatalf("message after unlinking: %s", last.Raw)
	}
}

// With FLOW_AGENT_RUNTIME=worker the reply runs on the agent worker, and the
// card follows it the same way.
func TestChatDirectMessageOnTheWorkerRuntime(t *testing.T) {
	f := newChatDMFixture(t, func(int, string) string { return chatDMTextTurn("in-process") })
	f.s.agent.WorkerSecret = testWorkerSecret
	f.s.agent.Runtime = appconfig.AgentRuntimeWorker
	handler := newHandler(f.s)
	t.Cleanup(func() { f.s.stopDeliveryScheduler(t.Context()) })
	f.text(t, "om_user_1", "How many issues are open?")
	claimed := runtimeJSON[struct {
		Runs []store.AgentRun `json:"runs"`
	}](t, handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/claim", worker: "w1", body: map[string]any{"free": map[string]int{"chat": 1}, "waitMs": 5000}}, http.StatusOK)
	if len(claimed.Runs) != 1 {
		t.Fatal("no worker run was queued")
	}
	run := claimed.Runs[0]
	post := func(activity map[string]any) {
		runtimeJSON[map[string]any](t, handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/activities", worker: "w1", body: activity}, http.StatusOK)
	}
	post(map[string]any{"type": "action", "activityId": "c1:call", "callId": "c1", "content": map[string]any{"action": "list_issues", "parameter": map[string]any{"limit": 5}}})
	post(map[string]any{"type": "action.result", "activityId": "c1:result", "callId": "c1", "content": map[string]any{"action": "list_issues", "result": `{"items":[]}`}})
	post(map[string]any{"type": "response", "activityId": "end", "body": "No issues are open."})
	runtimeJSON[map[string]any](t, handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/complete", worker: "w1", body: map[string]any{"status": "done"}}, http.StatusOK)
	chatDMReplies.Wait()
	sessions := f.sessions(t)
	if len(sessions) != 1 || sessions[0].Origin != "feishu" || run.SessionID != sessions[0].ID {
		t.Fatalf("worker chat: %+v run %+v", sessions, run)
	}
	if f.calls.Load() != 0 {
		t.Fatal("the reply ran in process")
	}
	if final := f.lastUpdate(f.sent()[0].ID); !strings.Contains(final, "No issues are open.") || !strings.Contains(final, "1 done") {
		t.Fatalf("worker reply card: %s", final)
	}
}

// What the card says it did comes from the tool calls: a failed write is
// counted, and the answer carries the write ledger's account.
func TestChatDirectMessageCardReportsTheWriteLedger(t *testing.T) {
	f := newChatDMFixture(t, func(call int, input string) string {
		if call == 1 {
			return chatDMToolTurn("save_issue", `{"id":"NOPE-404","title":"Renamed"}`)
		}
		return chatDMTextTurn("I renamed the issue.")
	})
	f.text(t, "om_user_1", "Rename NOPE-404 to Renamed")
	chatDMReplies.Wait()
	answer := f.sessions(t)[0].Messages[1]
	card := f.lastUpdate(f.sent()[0].ID)
	if answer.Content == "I renamed the issue." || !strings.Contains(answer.Content, agentSummaryHeading) {
		t.Fatalf("the ledger did not report the failed write: %q", answer.Content)
	}
	if !strings.Contains(card, "1 failed") || !strings.Contains(card, "0 succeeded, 1 failed") {
		t.Fatalf("card: %s", card)
	}
}

// The agent runs within the member's own request budget, and says so when
// the agent is not available.
func TestChatDirectMessageRateLimitAndAvailability(t *testing.T) {
	f := newChatDMFixture(t, func(int, string) string { return chatDMTextTurn("Answered.") })
	config := defaultAPIRateLimitConfig()
	config.Budgets[apiRateSession] = apiRateBudget{Requests: 1, Complexity: 1_000_000}
	f.s.apiRateLimiter = newAPIRateLimiter(config, nil)
	f.text(t, "om_user_1", "First question")
	chatDMReplies.Wait()
	f.text(t, "om_user_2", "Second question")
	chatDMReplies.Wait()
	if f.calls.Load() != 1 || len(f.sessions(t)) != 1 {
		t.Fatalf("the session budget was not applied: calls=%d", f.calls.Load())
	}
	if card := f.lastUpdate(f.sent()[1].ID); !strings.Contains(card, "faster than Flow allows") {
		t.Fatalf("rate limited card: %s", card)
	}
	f.s.apiRateLimiter = newAPIRateLimiter(defaultAPIRateLimitConfig(), nil)
	f.s.agent.Enabled = false
	f.text(t, "om_user_3", "Third question")
	chatDMReplies.Wait()
	if card := f.lastUpdate(f.sent()[2].ID); !strings.Contains(card, "isn't set up in this workspace") {
		t.Fatalf("agent unavailable card: %s", card)
	}
	// The app is not available to the account: recorded on its connected-account row.
	f.s.agent.Enabled = true
	previous := http.DefaultTransport
	http.DefaultTransport = chatWorkflowTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/reply") {
			return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"code":230013,"msg":"Bot has NO availability to this user."}`)), Request: r}, nil
		}
		return previous.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	f.text(t, "om_user_4", "Fourth question")
	if identity, _ := f.s.store.ChatIdentityForUser(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, f.identity.UserID); identity.PersonalDelivery != chatPersonalStateUnavailable {
		t.Fatalf("unavailable state not recorded: %+v", identity)
	}
}
