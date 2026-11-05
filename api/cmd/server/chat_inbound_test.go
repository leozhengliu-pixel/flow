package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	larkcard "github.com/larksuite/oapi-sdk-go/v3/card"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
)

// Inbound callbacks in the shapes the platform sends, through the signed
// webhook: both card callback formats (and every check that refuses one),
// group messages (text, rich text, command words, thread replies) and the
// diagnostics each leaves.

// chatLogCapture collects the structured log records written while a test
// runs.
type chatLogCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *chatLogCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *chatLogCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func captureChatLogs(t *testing.T) *chatLogCapture {
	t.Helper()
	capture := &chatLogCapture{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(capture, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return capture
}

// records are the captured records with the given message.
func (c *chatLogCapture) records(t *testing.T, message string) []map[string]any {
	t.Helper()
	result := []map[string]any{}
	for _, line := range strings.Split(c.String(), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) == nil && record["msg"] == message {
			result = append(result, record)
		}
	}
	return result
}

// chatSignature: sha256 signs with the encrypt key (events and
// card.action.trigger), sha1 with the verification token (legacy card
// requests), both as the platform's SDK computes them.
type chatSignature int

const (
	chatSignNone chatSignature = iota
	chatSignSHA256
	chatSignSHA1
)

type chatRawCallback struct {
	body      []byte
	stamp     string
	nonce     string
	signature chatSignature
	// override replaces the computed signature.
	override string
}

func (f *chatWorkflowFixture) post(t *testing.T, callback chatRawCallback) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/integrations/feishu/"+f.record.ConnectionID+"/chat/webhook", bytes.NewReader(callback.body))
	r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, "req_inbound_test"))
	r.SetPathValue("provider", f.record.Provider)
	r.SetPathValue("id", f.record.ConnectionID)
	if callback.stamp == "" {
		callback.stamp = strconv.FormatInt(time.Now().Unix(), 10)
	}
	if callback.nonce == "" {
		callback.nonce = "nonce_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	r.Header.Set(chatCallbackTimestampHeader, callback.stamp)
	if callback.nonce != "-" {
		r.Header.Set(chatCallbackNonceHeader, callback.nonce)
	}
	nonce := r.Header.Get(chatCallbackNonceHeader)
	switch callback.signature {
	case chatSignSHA256:
		r.Header.Set(chatCallbackSignatureHeader, larkevent.Signature(callback.stamp, nonce, f.credentials.EncryptKey, string(callback.body)))
	case chatSignSHA1:
		r.Header.Set(chatCallbackSignatureHeader, larkcard.Signature(callback.stamp, nonce, f.credentials.VerificationToken, string(callback.body)))
	}
	if callback.override != "" {
		r.Header.Set(chatCallbackSignatureHeader, callback.override)
	}
	w := httptest.NewRecorder()
	started := time.Now()
	f.s.handleChatWebhook(w, r)
	// The platform waits 3 seconds for a card callback's answer.
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("callback answered after %s", elapsed)
	}
	return w
}

// cardTriggerEvent is a card.action.trigger callback (schema 2.0).
func (f *chatWorkflowFixture) cardTriggerEvent(openID, chatID, messageID, actionID, option, eventID string, form map[string]string) map[string]any {
	action := map[string]any{"tag": "button", "value": map[string]string{"actionId": actionID}, "timezone": "Asia/Shanghai"}
	if option != "" {
		action["tag"], action["option"] = "select_static", option
	}
	if form != nil {
		action["form_value"] = form
	}
	return map[string]any{
		"schema": "2.0",
		"header": map[string]string{"event_id": eventID, "token": f.credentials.VerificationToken, "create_time": strconv.FormatInt(time.Now().UnixMicro(), 10), "event_type": "card.action.trigger", "tenant_key": f.record.TenantKey, "app_id": f.record.AppID},
		"event": map[string]any{
			"operator": map[string]string{"tenant_key": f.record.TenantKey, "open_id": openID, "union_id": "on_union_test"},
			"token":    "c-update-token-v2",
			"action":   action,
			"host":     "im_message",
			"context":  map[string]string{"open_message_id": messageID, "open_chat_id": chatID},
		},
	}
}

// legacyCardRequest is a legacy card request (card request URL or
// card.action.trigger_v1): the click at the top level, token being the card's
// update token, and here without open_chat_id as the platform's example.
func (f *chatWorkflowFixture) legacyCardRequest(openID, messageID, actionID, option string) map[string]any {
	action := map[string]any{"tag": "button", "value": map[string]string{"actionId": actionID}, "timezone": "Asia/Shanghai"}
	if option != "" {
		action["tag"], action["option"] = "select_static", option
	}
	return map[string]any{"open_id": openID, "user_id": "u_legacy", "open_message_id": messageID, "tenant_key": f.record.TenantKey, "token": "c-update-token-legacy", "action": action}
}

func chatPlainBody(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// groupIssueCard sends an issue card with controls to the bound group and
// returns the issue, the card's message ID and its action IDs by kind.
func (f *chatWorkflowFixture) groupIssueCard(t *testing.T) (domain.Issue, string, map[string]string) {
	t.Helper()
	issue := f.s.store.Bootstrap().Issues[0]
	ctx := withChatIdentityAuthentication(t.Context(), f.identity)
	var unassigned domain.Issue
	if err := f.s.chatAuthorizedMutation(ctx, f.record.WorkspaceKey, f.identity.UserID, http.MethodPatch, "/api/issue-records/"+issue.ID, map[string]any{"assigneeId": "", "priority": 4, "expectedVersion": issue.Version}, &unassigned); err != nil {
		t.Fatal(err)
	}
	if err := f.s.chatSendIssueCard(t.Context(), f.record, f.binding, unassigned, "inbound_card", "", "issue.created"); err != nil {
		t.Fatal(err)
	}
	ids := chatCardActionIDs(t, f, f.sent[len(f.sent)-1]["content"].(string))
	messages, err := f.s.store.ChatMessagesForIssue(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, issue.ID)
	if err != nil || len(messages) != 1 {
		t.Fatalf("card message: %#v %v", messages, err)
	}
	return unassigned, messages[0].MessageID, ids
}

func (f *chatWorkflowFixture) runAllJobs(t *testing.T) {
	t.Helper()
	if err := f.s.processChatJobs(t.Context(), f.record.WorkspaceKey); err != nil {
		t.Fatal(err)
	}
}

// The four card controls work end to end in both callback formats: Assign to
// me, the status and priority selects, and Comment (its form arrives
// privately and its submission becomes the member's comment).
func TestChatInboundCardCallbacksInBothFormatsRunTheFourControls(t *testing.T) {
	f := newChatWorkflowFixture(t)
	logs := captureChatLogs(t)
	issue, messageID, ids := f.groupIssueCard(t)
	for _, kind := range []string{chatQuickAssign, chatQuickState, chatQuickPriority, "open_comment"} {
		if ids[kind] == "" {
			t.Fatalf("card lacks %s: %v", kind, ids)
		}
	}
	toast := func(w *httptest.ResponseRecorder, want string) {
		t.Helper()
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"toast"`) || !strings.Contains(w.Body.String(), want) {
			t.Fatalf("card answer: %d %s", w.Code, w.Body.String())
		}
	}

	// Assign to me: card.action.trigger, encrypted and signed with the
	// encrypt key.
	toast(f.post(t, chatRawCallback{body: chatEncryptedTestBody(t, f.cardTriggerEvent(f.identity.OpenID, f.binding.ChatID, messageID, ids[chatQuickAssign], "", "evt_v2_assign", nil), f.credentials.EncryptKey), signature: chatSignSHA256}), "正在更新")
	f.runAllJobs(t)
	after, _ := f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, issue.ID)
	if after.Assignee == nil || after.Assignee.ID != f.identity.UserID {
		t.Fatalf("assign to me: %+v", after.Assignee)
	}

	// Priority Low -> High: a legacy card request, plain JSON, signed with
	// the verification token, without the chat ID.
	toast(f.post(t, chatRawCallback{body: chatPlainBody(t, f.legacyCardRequest(f.identity.OpenID, messageID, ids[chatQuickPriority], "2")), signature: chatSignSHA1}), "正在更新")
	f.runAllJobs(t)
	after, _ = f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, issue.ID)
	if after.Priority != 2 {
		t.Fatalf("priority: %d", after.Priority)
	}

	// Status: a legacy card request, encrypted.
	target := chatOtherState(t, f, after)
	toast(f.post(t, chatRawCallback{body: chatEncryptedTestBody(t, f.legacyCardRequest(f.identity.OpenID, messageID, ids[chatQuickState], target), f.credentials.EncryptKey), signature: chatSignSHA1}), "正在更新")
	f.runAllJobs(t)
	after, _ = f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, issue.ID)
	if after.State.ID != target {
		t.Fatalf("status: %s want %s", after.State.ID, target)
	}
	if last := f.patched[len(f.patched)-1]["content"].(string); !strings.Contains(last, `"initial_option":"`+target+`"`) || !strings.Contains(last, `"initial_option":"2"`) {
		t.Fatalf("card not redrawn with the new status and priority: %s", last)
	}

	// Comment: card.action.trigger in plain JSON opens the private form.
	sentBefore := len(f.sent)
	toast(f.post(t, chatRawCallback{body: chatPlainBody(t, f.cardTriggerEvent(f.identity.OpenID, f.binding.ChatID, messageID, ids["open_comment"], "", "evt_v2_comment", nil)), signature: chatSignSHA256}), "Processing")
	f.runAllJobs(t)
	if len(f.sent) != sentBefore+1 || f.sent[sentBefore]["receive_id"] != f.identity.OpenID || f.sent[sentBefore]["msg_type"] != "interactive" || !strings.Contains(f.sent[sentBefore]["content"].(string), `"name":"comment"`) {
		t.Fatalf("comment form: %#v", f.sent[sentBefore:])
	}
	form := f.sent[sentBefore]
	formIDs := chatActionIDPattern.FindAllStringSubmatch(form["content"].(string), -1)
	formMessage := f.responseIDs[form["uuid"].(string)]
	if len(formIDs) == 0 || formMessage == "" {
		t.Fatalf("form identity: %v %q", formIDs, formMessage)
	}
	comments := len(f.s.store.Bootstrap().Comments[issue.ID])
	submit := f.cardTriggerEvent(f.identity.OpenID, "oc_dm_test", formMessage, formIDs[len(formIDs)-1][1], "", "evt_v2_comment_submit", map[string]string{"comment": "Reproduced on Safari"})
	toast(f.post(t, chatRawCallback{body: chatEncryptedTestBody(t, submit, f.credentials.EncryptKey), signature: chatSignSHA256}), "Processing")
	f.runAllJobs(t)
	latest := f.s.store.Bootstrap().Comments[issue.ID]
	if len(latest) != comments+1 || latest[len(latest)-1].Body != "Reproduced on Safari" || latest[len(latest)-1].User.ID != f.identity.UserID {
		t.Fatalf("comment from the form: %d -> %d", comments, len(latest))
	}

	queued := logs.records(t, "chat callback handled")
	formats := map[string]bool{}
	for _, record := range queued {
		if record["outcome"] == "queued" && record["reason"] == "card_action" && record["request_id"] == "req_inbound_test" {
			formats[record["format"].(string)+"/"+record["signature_scheme"].(string)] = true
		}
	}
	if !formats["event_v2/sha256_encrypt_key"] || !formats["legacy_card/sha1_verification_token"] {
		t.Fatalf("diagnostics of accepted clicks: %v", formats)
	}
}

// Every check that refuses a callback answers its status and leaves one
// record naming the check, with no token, key, signature or payload.
func TestChatInboundCallbackRejectionsNameTheFailedCheck(t *testing.T) {
	f := newChatWorkflowFixture(t)
	logs := captureChatLogs(t)
	_, messageID, ids := f.groupIssueCard(t)
	event := func(edit func(map[string]any)) []byte {
		value := f.cardTriggerEvent(f.identity.OpenID, f.binding.ChatID, messageID, ids[chatQuickAssign], "", "evt_reject", nil)
		if edit != nil {
			edit(value)
		}
		return chatEncryptedTestBody(t, value, f.credentials.EncryptKey)
	}
	header := func(edit func(map[string]string)) func(map[string]any) {
		return func(value map[string]any) { edit(value["header"].(map[string]string)) }
	}
	legacy := chatPlainBody(t, f.legacyCardRequest(f.identity.OpenID, messageID, ids[chatQuickAssign], ""))
	otherTenant := f.legacyCardRequest(f.identity.OpenID, messageID, ids[chatQuickAssign], "")
	otherTenant["tenant_key"] = "tenant_other"
	garbled := chatEncryptedTestBody(t, map[string]string{"schema": "2.0"}, "another_encrypt_key")
	v1 := chatPlainBody(t, map[string]any{"uuid": "u", "token": f.credentials.VerificationToken, "ts": "1", "type": "event_callback", "event": map[string]string{"type": "message"}})
	cases := []struct {
		name     string
		callback chatRawCallback
		status   int
		reason   string
	}{
		{"not json", chatRawCallback{body: []byte("encrypt=1"), signature: chatSignSHA256}, 401, chatRejectMalformed},
		{"wrong encrypt key", chatRawCallback{body: garbled, signature: chatSignSHA256}, 401, chatRejectDecrypt},
		{"stale timestamp", chatRawCallback{body: event(nil), stamp: strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10), signature: chatSignSHA256}, 401, chatRejectTimestamp},
		{"future timestamp in ms", chatRawCallback{body: event(nil), stamp: strconv.FormatInt(time.Now().Add(10*time.Minute).UnixMilli(), 10), signature: chatSignSHA256}, 401, chatRejectTimestamp},
		{"no nonce", chatRawCallback{body: event(nil), nonce: "-", signature: chatSignSHA256}, 401, chatRejectNonce},
		{"unsigned", chatRawCallback{body: event(nil)}, 401, chatRejectSignature},
		{"wrong signature", chatRawCallback{body: event(nil), signature: chatSignSHA256, override: strings.Repeat("0", 64)}, 401, chatRejectSignature},
		// A schema 2.0 event never authenticates with the legacy scheme.
		{"event signed the legacy way", chatRawCallback{body: event(nil), signature: chatSignSHA1}, 401, chatRejectSignature},
		{"wrong header token", chatRawCallback{body: event(header(func(h map[string]string) { h["token"] = "wrong" })), signature: chatSignSHA256}, 401, chatRejectToken},
		{"top-level token only", chatRawCallback{body: event(func(value map[string]any) {
			value["token"] = f.credentials.VerificationToken
			delete(value["header"].(map[string]string), "token")
		}), signature: chatSignSHA256}, 401, chatRejectToken},
		{"other app", chatRawCallback{body: event(header(func(h map[string]string) { h["app_id"] = "cli_other" })), signature: chatSignSHA256}, 403, chatRejectAppMismatch},
		{"other tenant", chatRawCallback{body: event(header(func(h map[string]string) { h["tenant_key"] = "tenant_other" })), signature: chatSignSHA256}, 403, chatRejectTenantMismatch},
		{"legacy unsigned", chatRawCallback{body: legacy}, 401, chatRejectSignature},
		{"legacy other tenant", chatRawCallback{body: chatPlainBody(t, otherTenant), signature: chatSignSHA1}, 403, chatRejectTenantMismatch},
		{"v1 event", chatRawCallback{body: v1, signature: chatSignSHA256}, 403, chatRejectUnsupportedFormat},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := len(logs.records(t, "chat callback rejected"))
			if got := f.post(t, tc.callback); got.Code != tc.status {
				t.Fatalf("status %d want %d: %s", got.Code, tc.status, got.Body.String())
			}
			records := logs.records(t, "chat callback rejected")
			if len(records) != before+1 || records[len(records)-1]["reason"] != tc.reason || records[len(records)-1]["connection_id"] != f.record.ConnectionID || records[len(records)-1]["request_id"] != "req_inbound_test" {
				t.Fatalf("rejection record: %v", records[before:])
			}
		})
	}
	// The pending connection still verifies its URL, encrypted or plain.
	f.record.Connected = false
	if err := f.s.store.PutChatIntegration(t.Context(), f.record); err != nil {
		t.Fatal(err)
	}
	challenge := map[string]string{"type": "url_verification", "token": f.credentials.VerificationToken, "challenge": "challenge_plain"}
	if got := f.post(t, chatRawCallback{body: chatPlainBody(t, challenge)}); got.Code != 200 || !strings.Contains(got.Body.String(), "challenge_plain") {
		t.Fatalf("plain challenge: %d %s", got.Code, got.Body.String())
	}
	challenge["token"] = "wrong"
	if got := f.post(t, chatRawCallback{body: chatPlainBody(t, challenge)}); got.Code != 401 {
		t.Fatalf("challenge with another token: %d", got.Code)
	}
	if got := f.post(t, chatRawCallback{body: event(nil), signature: chatSignSHA256}); got.Code != 403 {
		t.Fatalf("pending connection accepted a click: %d", got.Code)
	}
	if records := logs.records(t, "chat callback rejected"); records[len(records)-1]["reason"] != chatRejectNotConnected {
		t.Fatalf("not connected record: %v", records[len(records)-1])
	}
	for _, secret := range []string{f.credentials.EncryptKey, f.credentials.VerificationToken, f.credentials.AppSecret, "c-update-token", "Asia/Shanghai"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("diagnostics leaked %q", secret)
		}
	}
}

// A refused click is answered within the response time with an error toast,
// and the record says why.
func TestChatInboundCardCallbackRefusalsAreRecorded(t *testing.T) {
	f := newChatWorkflowFixture(t)
	logs := captureChatLogs(t)
	_, messageID, ids := f.groupIssueCard(t)
	for _, tc := range []struct {
		name   string
		body   map[string]any
		reason string
	}{
		{"unknown action", f.cardTriggerEvent(f.identity.OpenID, f.binding.ChatID, messageID, "chat_action_missing", "", "evt_unknown", nil), "card_action_unknown"},
		{"another card", f.cardTriggerEvent(f.identity.OpenID, f.binding.ChatID, "om_other_card", ids[chatQuickAssign], "", "evt_mismatch", nil), "card_action_mismatch"},
		{"no event id", f.cardTriggerEvent(f.identity.OpenID, f.binding.ChatID, messageID, ids[chatQuickAssign], "", "", nil), "card_event_id_missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := f.post(t, chatRawCallback{body: chatEncryptedTestBody(t, tc.body, f.credentials.EncryptKey), signature: chatSignSHA256})
			if got.Code != 200 || !strings.Contains(got.Body.String(), `"type":"error"`) || !strings.Contains(got.Body.String(), "此操作已过期或不可用") {
				t.Fatalf("refusal answer: %d %s", got.Code, got.Body.String())
			}
			records := logs.records(t, "chat callback handled")
			if last := records[len(records)-1]; last["outcome"] != "refused" || last["reason"] != tc.reason {
				t.Fatalf("refusal record: %v", last)
			}
		})
	}
}

// groupMessage is an im.message.receive_v1 group event from the linked
// member.
func (f *chatWorkflowFixture) groupMessage(messageID, messageType, content string, mentions ...map[string]any) chatWebhookEnvelope {
	envelope := f.messageEnvelope()
	envelope.Event.Message.MessageID, envelope.Event.Message.MessageType, envelope.Event.Message.Content = messageID, messageType, content
	raw, _ := json.Marshal(mentions)
	envelope.Event.Message.Mentions = nil
	_ = json.Unmarshal(raw, &envelope.Event.Message.Mentions)
	return envelope
}

func (f *chatWorkflowFixture) botMention() map[string]any {
	return map[string]any{"key": "@_user_1", "name": "Flow", "mentioned_type": "bot", "tenant_key": f.record.TenantKey, "id": map[string]string{"open_id": f.record.BotOpenID, "union_id": "on_bot", "user_id": ""}}
}

func chatTextContent(text string) string {
	raw, _ := json.Marshal(map[string]string{"text": text})
	return string(raw)
}

// chatPostContent is a received rich text message: mentions as at elements
// whose user_id is the mention's placeholder.
func chatPostContent(elements ...map[string]any) string {
	raw, _ := json.Marshal(map[string]any{"title": "", "content": [][]map[string]any{elements}})
	return string(raw)
}

// Create commands are recognized after a real mention in text and rich text,
// in Chinese and English, with or without spaces and full-width punctuation.
func TestChatInboundGroupCreateCommandShapes(t *testing.T) {
	f := newChatWorkflowFixture(t)
	at := map[string]any{"tag": "at", "user_id": "@_user_1", "user_name": "Flow"}
	for _, tc := range []struct {
		name, messageType, content, title string
	}{
		{"text", "text", chatTextContent("@_user_1 创建事项 修复导出"), "修复导出"},
		{"text, no space", "text", chatTextContent("@_user_1创建事项修复导出"), "修复导出"},
		{"text, full-width colon", "text", chatTextContent("@_user_1 创建事项：修复导出"), "修复导出"},
		{"text, full-width space", "text", chatTextContent("@_user_1　创建事项　修复导出"), "修复导出"},
		{"text, mention last", "text", chatTextContent("创建事项 修复导出 @_user_1"), "修复导出"},
		{"text, 新建事项", "text", chatTextContent("@_user_1 新建事项 修复导出"), "修复导出"},
		{"text, slash", "text", chatTextContent("@_user_1 /创建事项 修复导出"), "修复导出"},
		{"english", "text", chatTextContent("@_user_1 Create Issue: Fix export"), "Fix export"},
		{"english, extra spaces", "text", chatTextContent("@_user_1  create   issue  Fix export"), "Fix export"},
		{"english, new issue", "text", chatTextContent("@_user_1 new issue Fix export"), "Fix export"},
		{"no title", "text", chatTextContent("@_user_1 创建事项"), ""},
		{"post", "post", chatPostContent(at, map[string]any{"tag": "text", "text": " 创建事项 修复导出"}), "修复导出"},
		{"post, bold title", "post", chatPostContent(at, map[string]any{"tag": "text", "text": " 创建事项 "}, map[string]any{"tag": "text", "text": "修复导出", "style": []string{"bold"}}), "修复导出"},
		{"post in its locale", "post", func() string {
			raw, _ := json.Marshal(map[string]any{"zh_cn": map[string]any{"title": "", "content": [][]map[string]any{{at, {"tag": "text", "text": " create issue Fix export"}}}}})
			return string(raw)
		}(), "Fix export"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envelope := f.groupMessage("om_create_"+strconv.Itoa(len(tc.name)), tc.messageType, tc.content, f.botMention())
			command, _, ignored, err := f.s.chatCommandForEnvelopeReason(t.Context(), f.record, envelope)
			if err != nil || ignored != "" || command.Kind != "create_form" || command.Title != tc.title {
				t.Fatalf("got %+v ignored=%q err=%v", command, ignored, err)
			}
		})
	}
	// Words that only start like the command are help, not a create.
	for _, text := range []string{"@_user_1 create issues faster", "@_user_1 创建 一个想法"} {
		command, _, _, _ := f.s.chatCommandForEnvelopeReason(t.Context(), f.record, f.groupMessage("om_not_create", "text", chatTextContent(text), f.botMention()))
		if command.Kind != "group_help" {
			t.Fatalf("%q: %+v", text, command)
		}
	}
}

// Every ignored group message has its reason.
func TestChatInboundIgnoredGroupMessagesHaveReasons(t *testing.T) {
	f := newChatWorkflowFixture(t)
	other := map[string]any{"key": "@_user_1", "name": "Other bot", "mentioned_type": "bot", "id": map[string]string{"open_id": "ou_other_bot"}}
	member := map[string]any{"key": "@_user_1", "name": "Ada", "mentioned_type": "user", "id": map[string]string{"open_id": "ou_ada"}}
	for _, tc := range []struct {
		name   string
		edit   func(*chatWebhookEnvelope)
		reason string
	}{
		{"no mention", func(e *chatWebhookEnvelope) { e.Event.Message.Mentions = nil }, "bot_not_mentioned"},
		{"another member", func(e *chatWebhookEnvelope) {
			*e = f.groupMessage(e.Event.Message.MessageID, "text", chatTextContent("@_user_1 创建事项 x"), member)
		}, "bot_not_mentioned"},
		{"another bot", func(e *chatWebhookEnvelope) {
			*e = f.groupMessage(e.Event.Message.MessageID, "text", chatTextContent("@_user_1 创建事项 x"), other)
		}, "other_bot_mentioned"},
		{"bot sender", func(e *chatWebhookEnvelope) { e.Event.Sender.SenderType = "app" }, "sender_not_user"},
		{"own echo", func(e *chatWebhookEnvelope) { e.Event.Sender.SenderID.OpenID = f.record.BotOpenID }, "sender_is_bot"},
		{"external member", func(e *chatWebhookEnvelope) { e.Event.Sender.TenantKey = "tenant_other" }, "sender_other_tenant"},
		{"image", func(e *chatWebhookEnvelope) {
			e.Event.Message.MessageType, e.Event.Message.Content = "image", `{"image_key":"img_1"}`
		}, "message_type_unsupported"},
		{"unknown chat type", func(e *chatWebhookEnvelope) { e.Event.Message.ChatType = "topic_group" }, "chat_type_unsupported"},
		{"reply in another thread", func(e *chatWebhookEnvelope) {
			e.Event.Message.RootID, e.Event.Message.Mentions = "om_unmapped_root", nil
		}, chatThreadNotIssue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envelope := f.groupMessage("om_ignored", "text", chatTextContent("@_user_1 创建事项 Fix"), f.botMention())
			tc.edit(&envelope)
			_, _, ignored, err := f.s.chatCommandForEnvelopeReason(t.Context(), f.record, envelope)
			if err != nil || ignored != tc.reason {
				t.Fatalf("reason %q want %q (%v)", ignored, tc.reason, err)
			}
		})
	}
	// Through the webhook, an ignored message is answered 200 and recorded.
	logs := captureChatLogs(t)
	envelope := f.groupMessage("om_ignored_webhook", "text", chatTextContent("hello"))
	if got := f.callback(t, envelope, time.Now(), true); got.Code != 200 {
		t.Fatalf("ignored message: %d", got.Code)
	}
	records := logs.records(t, "chat callback handled")
	if len(records) != 1 || records[0]["outcome"] != "ignored" || records[0]["reason"] != "bot_not_mentioned" || records[0]["message_id"] != "om_ignored_webhook" || records[0]["event_type"] != "im.message.receive_v1" {
		t.Fatalf("ignored record: %v", records)
	}
	if strings.Contains(logs.String(), "hello") {
		t.Fatal("the record carries message text")
	}
}

// Thread replies under an issue card become comments without a mention, in
// text and rich text, in normal and topic groups; a recognized command that
// cannot proceed answers the member.
func TestChatInboundThreadRepliesAndCommandFeedback(t *testing.T) {
	f := newChatWorkflowFixture(t)
	issue := f.s.store.Bootstrap().Issues[0]
	f.notify(t, "issue.created", issue.ID, "thread_card")
	thread := f.thread(t, issue.ID)
	ada := map[string]any{"key": "@_user_2", "name": "Ada", "mentioned_type": "user", "id": map[string]string{"open_id": "ou_ada"}}
	for _, tc := range []struct {
		name, messageType, content, want string
		mentions                         []map[string]any
	}{
		{"text, no mention", "text", chatTextContent("Reproduced on staging"), "Reproduced on staging", nil},
		{"post, no mention", "post", chatPostContent(map[string]any{"tag": "text", "text": "Steps: "}, map[string]any{"tag": "a", "text": "log", "href": "https://example.test"}), "Steps: log", nil},
		{"text naming a member", "text", chatTextContent("@_user_2 please check"), "@Ada please check", []map[string]any{ada}},
		{"text with the bot", "text", chatTextContent("@_user_1 fixed in 1.2"), "fixed in 1.2", []map[string]any{f.botMention()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envelope := f.groupMessage("om_reply_"+strconv.Itoa(len(tc.name)), tc.messageType, tc.content, tc.mentions...)
			envelope.Event.Message.RootID, envelope.Event.Message.ParentID, envelope.Event.Message.ThreadID = thread.RootMessageID, thread.RootMessageID, "omt_thread_test"
			command, _, ignored, err := f.s.chatCommandForEnvelopeReason(t.Context(), f.record, envelope)
			if err != nil || ignored != "" || command.Kind != chatThreadReplyKind || command.Text != tc.want || command.RootMessageID != thread.RootMessageID {
				t.Fatalf("got %+v ignored=%q err=%v", command, ignored, err)
			}
		})
	}
	// A create command in the thread keeps its meaning.
	create := f.groupMessage("om_thread_create", "text", chatTextContent("@_user_1 创建事项 Follow-up"), f.botMention())
	create.Event.Message.RootID = thread.RootMessageID
	if command, _, _, _ := f.s.chatCommandForEnvelopeReason(t.Context(), f.record, create); command.Kind != "create_form" {
		t.Fatalf("create in a thread: %+v", command)
	}

	// The group does not allow creating: the member hears why.
	binding := f.binding
	binding.AllowCreate = false
	if err := f.s.store.PutChatBinding(t.Context(), f.record.WorkspaceKey, binding); err != nil {
		t.Fatal(err)
	}
	if got := f.callback(t, f.groupMessage("om_create_refused", "text", chatTextContent("@_user_1 创建事项 Fix export"), f.botMention()), time.Now(), true); got.Code != 200 {
		t.Fatalf("create command: %d", got.Code)
	}
	f.runAllJobs(t)
	if text := f.lastText(t); !strings.Contains(text, "not enabled for creating") || f.sent[len(f.sent)-1]["receive_id"] != f.identity.OpenID {
		t.Fatalf("binding refusal: %s", text)
	}
	// An unlinked member gets the link prompt.
	unlinked := f.groupMessage("om_create_unlinked", "text", chatTextContent("@_user_1 创建事项 Fix export"), f.botMention())
	unlinked.Event.Sender.SenderID.OpenID = "ou_unlinked_member"
	f.callback(t, unlinked, time.Now(), true)
	f.runAllJobs(t)
	if text := f.lastText(t); !strings.Contains(text, "Connect your Flow account") || f.sent[len(f.sent)-1]["receive_id"] != "ou_unlinked_member" {
		t.Fatalf("link prompt: %s", text)
	}
}
