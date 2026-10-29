package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// chatShortcutPlatform fakes the platform calls of the message shortcut on
// top of the workflow fixture: sign-in codes, message reads and JSAPI tickets.
type chatShortcutPlatform struct {
	codes    map[string]string // sign-in code -> open_id
	messages map[string]string // message id -> chat id (missing: the bot cannot read it)
	text     string
	paths    []string
}

func newChatShortcutPlatform(t *testing.T, f *chatWorkflowFixture) *chatShortcutPlatform {
	t.Helper()
	f.s.allowedOrigin = "https://flow.example.test"
	platform := &chatShortcutPlatform{codes: map[string]string{}, messages: map[string]string{}}
	previous := http.DefaultTransport
	respond := func(r *http.Request, status int, body string) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}
	http.DefaultTransport = chatWorkflowTransport(func(r *http.Request) (*http.Response, error) {
		platform.paths = append(platform.paths, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/open-apis/authen/v2/oauth/token":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if platform.codes[body["code"]] == "" || body["client_secret"] != f.credentials.AppSecret {
				return respond(r, http.StatusBadRequest, `{"code":20003,"error":"invalid_grant"}`)
			}
			return respond(r, http.StatusOK, `{"code":0,"access_token":"user_token_`+body["code"]+`"}`)
		case r.URL.Path == "/open-apis/authen/v1/user_info":
			code := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer user_token_")
			tenant := f.record.TenantKey
			if code == "code_other_tenant" {
				tenant = "tenant_other"
			}
			return respond(r, http.StatusOK, fmt.Sprintf(`{"code":0,"data":{"open_id":%q,"tenant_key":%q}}`, platform.codes[code], tenant))
		case r.URL.Path == "/open-apis/jssdk/ticket/get":
			return respond(r, http.StatusOK, `{"code":0,"data":{"ticket":"ticket_test","expire_in":7200}}`)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/open-apis/im/v1/messages/"):
			id := strings.TrimPrefix(r.URL.Path, "/open-apis/im/v1/messages/")
			chatID := platform.messages[id]
			if chatID == "" {
				return respond(r, http.StatusBadRequest, `{"code":230027,"msg":"Lack of necessary permissions"}`)
			}
			content, _ := json.Marshal(map[string]string{"text": platform.text})
			body, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"items": []map[string]any{{"message_id": id, "chat_id": chatID, "msg_type": "text", "body": map[string]string{"content": string(content)}}}}})
			return respond(r, http.StatusOK, string(body))
		}
		return previous.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	return platform
}

func (f *chatWorkflowFixture) shortcut(t *testing.T, body map[string]string) (int, map[string]string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/api/integrations/feishu/"+f.record.ConnectionID+"/chat/message-action", bytes.NewReader(raw))
	r.SetPathValue("provider", f.record.Provider)
	r.SetPathValue("id", f.record.ConnectionID)
	w := httptest.NewRecorder()
	f.s.submitChatMessageAction(w, r)
	var result map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	return w.Code, result
}

// submitForm submits a private form card with the given values.
func (f *chatWorkflowFixture) submitForm(t *testing.T, actionID string, values map[string]string) {
	t.Helper()
	stored, err := f.s.store.GetChatAction(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, actionID)
	if err != nil {
		t.Fatal(err)
	}
	var action chatExtendedAction
	_ = json.Unmarshal(stored.Payload, &action)
	envelope := f.messageEnvelope()
	envelope.Header.EventType, envelope.Header.EventID = "card.action.trigger", "evt_submit_"+actionID
	envelope.Event.Operator.TenantKey, envelope.Event.Operator.OpenID = f.record.TenantKey, f.identity.OpenID
	envelope.Event.Context.ChatID, envelope.Event.Context.MessageID = action.PersonalChatID, action.PersonalMessage
	envelope.Event.Action.Value = map[string]json.RawMessage{"actionId": json.RawMessage(strconv.Quote(actionID))}
	envelope.Event.Action.FormValue = map[string]json.RawMessage{}
	for key, value := range values {
		envelope.Event.Action.FormValue[key] = json.RawMessage(strconv.Quote(value))
	}
	command, dedup, accepted, err := f.s.chatCommandForEnvelope(t.Context(), f.record, envelope)
	if err != nil || !accepted {
		t.Fatalf("form submit refused: %v", err)
	}
	job := domain.ChatJob{ID: "chat_job_" + dedup, WorkspaceKey: f.record.WorkspaceKey, ConnectionID: f.record.ConnectionID, Kind: "inbound", DedupKey: dedup}
	job.Payload, _ = json.Marshal(command)
	if err := f.s.processChatJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
}

func (f *chatWorkflowFixture) runChatJobs(t *testing.T) {
	t.Helper()
	if err := f.s.processChatJobs(t.Context(), f.record.WorkspaceKey); err != nil {
		t.Fatal(err)
	}
}

// formAction returns the action ID of the last private form card.
func (f *chatWorkflowFixture) formAction(t *testing.T) (string, chatExtendedAction, string) {
	t.Helper()
	for index := len(f.sent) - 1; index >= 0; index-- {
		content, _ := f.sent[index]["content"].(string)
		if f.sent[index]["msg_type"] != "interactive" || !strings.Contains(content, `"action_type":"form_submit"`) {
			continue
		}
		match := chatActionIDPattern.FindStringSubmatch(content)
		stored, err := f.s.store.GetChatAction(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, match[1])
		if err != nil {
			t.Fatal(err)
		}
		var action chatExtendedAction
		_ = json.Unmarshal(stored.Payload, &action)
		return match[1], action, content
	}
	t.Fatalf("no form card sent: %#v", f.sent)
	return "", chatExtendedAction{}, ""
}

func TestChatMessageShortcutCreatesIssueAsActorAndRepliesInBoundGroup(t *testing.T) {
	f := newChatWorkflowFixture(t)
	platform := newChatShortcutPlatform(t, f)
	platform.codes["code_actor"] = f.identity.OpenID
	platform.messages["om_source_message"] = f.binding.ChatID
	platform.text = "@_user_1 Export fails on Safari\nOpen the report and export it."
	status, result := f.shortcut(t, map[string]string{"code": "code_actor", "messageId": "om_source_message", "chatId": f.binding.ChatID, "messageType": "text", "content": `{"text":"client copy"}`})
	if status != http.StatusAccepted || result["status"] != "sent" {
		t.Fatalf("shortcut: %d %#v", status, result)
	}
	f.runChatJobs(t)
	actionID, action, content := f.formAction(t)
	if f.sent[0]["receive_id"] != f.identity.OpenID || !slices.Equal(action.TeamIDs, []string{f.binding.TeamID}) || action.BindingID != f.binding.ID || !action.Verified || action.SourceMessageID != "om_source_message" {
		t.Fatalf("form scope: %#v %+v", f.sent[0], action)
	}
	for _, want := range []string{`"name":"teamId"`, `"name":"title"`, `"name":"description"`, `"name":"assigneeId"`, `"name":"priority"`, "Export fails on Safari", "applink.feishu.cn/client/chat/open?openChatId=" + f.binding.ChatID} {
		if !strings.Contains(content, want) {
			t.Fatalf("form lacks %s: %s", want, content)
		}
	}
	if strings.Contains(content, "client copy") || strings.Contains(content, "@_user_1") {
		t.Fatalf("form used the unverified client copy or mention placeholder: %s", content)
	}
	// A team outside the group's scope is refused.
	f.submitForm(t, actionID, map[string]string{"teamId": "team_outside_scope", "title": "Outside"})
	if text := f.lastText(t); !strings.Contains(text, "chat_form_invalid") {
		t.Fatalf("out-of-scope team: %s", text)
	}
	// A second tap within the minute opens no second form; the member then
	// submits a valid form.
	status, _ = f.shortcut(t, map[string]string{"code": "code_actor", "messageId": "om_source_message", "chatId": f.binding.ChatID})
	if status != http.StatusAccepted {
		t.Fatal(status)
	}
	jobs, _ := f.s.store.ClaimChatJobs(t.Context(), f.record.WorkspaceKey, "inbound", 10, time.Now())
	if len(jobs) != 0 {
		t.Fatal("a second tap within the minute queued another form")
	}
	stored, _ := f.s.store.GetChatAction(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, actionID)
	stored.ID = actionID + "_retry"
	if err := f.s.store.PutChatAction(t.Context(), stored); err != nil {
		t.Fatal(err)
	}
	f.submitForm(t, actionID+"_retry", map[string]string{"teamId": f.binding.TeamID, "title": "Export fails on Safari", "description": "Steps\n\nSource message: " + chatChatLink("feishu", f.binding.ChatID), "assigneeId": f.identity.UserID, "priority": "2"})
	issueID := "issue_chat_" + chatKey(f.record.ConnectionID, actionID+"_retry")
	issue, err := f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, issueID)
	if err != nil || issue.Creator.ID != f.identity.UserID || issue.Team.ID != f.binding.TeamID || issue.Priority != 2 || issue.Assignee == nil || issue.Assignee.ID != f.identity.UserID || !strings.Contains(issue.Description, "openChatId=") {
		t.Fatalf("issue not created as the actor with the form's values: %+v %v", issue, err)
	}
	before := len(f.sent)
	f.runChatJobs(t)
	reply := false
	for _, payload := range f.sent[before:] {
		if payload["reply_in_thread"] == true && payload["msg_type"] == "interactive" && strings.Contains(payload["content"].(string), issue.Identifier) {
			reply = true
		}
	}
	if !reply || !slices.Contains(platform.paths, "POST /open-apis/im/v1/messages/om_source_message/reply") {
		t.Fatalf("no issue card reply under the source message: %#v %v", f.sent[before:], platform.paths)
	}
}

func TestChatMessageShortcutRespectsBindingFlagLinkingAndUnreadableMessages(t *testing.T) {
	f := newChatWorkflowFixture(t)
	platform := newChatShortcutPlatform(t, f)
	platform.codes["code_actor"] = f.identity.OpenID
	platform.codes["code_stranger"] = "ou_stranger"
	platform.codes["code_other_tenant"] = "ou_other_tenant"
	platform.text = "Broken login"

	// Another tenant's account and an invalid code are refused.
	if status, result := f.shortcut(t, map[string]string{"code": "code_other_tenant", "messageId": "om_a"}); status != http.StatusForbidden || result["code"] != "chat_other_tenant" {
		t.Fatalf("other tenant: %d %#v", status, result)
	}
	if status, _ := f.shortcut(t, map[string]string{"code": "forged", "messageId": "om_a"}); status != http.StatusUnauthorized {
		t.Fatalf("forged code: %d", status)
	}
	// Unlinked: the page links to Connected accounts, and a link card arrives.
	status, result := f.shortcut(t, map[string]string{"code": "code_stranger", "messageId": "om_stranger", "chatId": f.binding.ChatID})
	if status != http.StatusAccepted || result["status"] != "not_linked" || !strings.Contains(result["linkUrl"], "/settings/account/connections") {
		t.Fatalf("unlinked: %d %#v", status, result)
	}
	f.runChatJobs(t)
	if len(f.sent) != 1 || f.sent[0]["receive_id"] != "ou_stranger" || f.sent[0]["msg_type"] != "interactive" || !strings.Contains(f.sent[0]["content"].(string), "Connect account") {
		t.Fatalf("link-account card: %#v", f.sent)
	}

	// The bound group does not allow creating issues.
	platform.messages["om_flag"] = f.binding.ChatID
	binding := f.binding
	binding.AllowCreate = false
	if err := f.s.store.PutChatBinding(t.Context(), f.record.WorkspaceKey, binding); err != nil {
		t.Fatal(err)
	}
	f.shortcut(t, map[string]string{"code": "code_actor", "messageId": "om_flag", "chatId": f.binding.ChatID})
	f.runChatJobs(t)
	if text := f.lastText(t); !strings.Contains(text, "chat_binding_create_disabled") {
		t.Fatalf("create flag ignored: %s", text)
	}
	// Even when the bot cannot read the message, the claimed group's flag holds.
	f.shortcut(t, map[string]string{"code": "code_actor", "messageId": "om_unreadable_flag", "chatId": f.binding.ChatID, "messageType": "text", "content": `{"text":"x"}`})
	f.runChatJobs(t)
	if text := f.lastText(t); !strings.Contains(text, "chat_binding_create_disabled") {
		t.Fatalf("claimed group flag ignored: %s", text)
	}

	// A message the bot cannot read: the form opens with the member's teams,
	// and the result goes to their bot conversation only.
	f.shortcut(t, map[string]string{"code": "code_actor", "messageId": "om_private_chat", "chatId": "oc_private_chat", "messageType": "post", "content": `{"title":"Crash","content":[[{"tag":"text","text":"on start"}]]}`})
	f.runChatJobs(t)
	actionID, action, content := f.formAction(t)
	if action.Verified || action.BindingID != "" || len(action.TeamIDs) == 0 || !strings.Contains(content, "Crash") {
		t.Fatalf("unverified form: %+v %s", action, content)
	}
	f.submitForm(t, actionID, map[string]string{"teamId": action.TeamIDs[0], "title": "Crash on start", "assigneeId": "__unassigned", "priority": "0"})
	before := len(f.sent)
	f.runChatJobs(t)
	if len(f.sent) != before+1 || f.sent[before]["receive_id"] != f.identity.OpenID || f.sent[before]["reply_in_thread"] != nil || slices.ContainsFunc(platform.paths, func(path string) bool { return strings.HasSuffix(path, "/om_private_chat/reply") }) {
		t.Fatalf("unverified message got a group reply: %#v", f.sent[before:])
	}

	// A readable message in an unbound group: a public team's issue is
	// replied under the message.
	platform.messages["om_unbound"] = "oc_unbound_group"
	f.shortcut(t, map[string]string{"code": "code_actor", "messageId": "om_unbound", "chatId": "oc_unbound_group"})
	f.runChatJobs(t)
	actionID, action, _ = f.formAction(t)
	if !action.Verified || action.BindingID != "" {
		t.Fatalf("unbound verified form: %+v", action)
	}
	teamID := f.s.store.Bootstrap().Issues[0].Team.ID
	f.submitForm(t, actionID, map[string]string{"teamId": teamID, "title": "Broken login"})
	f.runChatJobs(t)
	if !slices.Contains(platform.paths, "POST /open-apis/im/v1/messages/om_unbound/reply") {
		t.Fatalf("public issue not replied in the unbound group: %v", platform.paths)
	}
}

func TestChatMessageShortcutPageAndSignature(t *testing.T) {
	f := newChatWorkflowFixture(t)
	newChatShortcutPlatform(t, f)
	r := httptest.NewRequest(http.MethodGet, "/api/integrations/feishu/"+f.record.ConnectionID+"/chat/message-action", nil)
	r.SetPathValue("provider", f.record.Provider)
	r.SetPathValue("id", f.record.ConnectionID)
	w := httptest.NewRecorder()
	f.s.chatMessageActionPage(w, r)
	digest := sha256.Sum256([]byte(chatMessageActionScript))
	if w.Code != 200 || !strings.Contains(w.Body.String(), chatMessageActionScript) || !strings.Contains(w.Header().Get("Content-Security-Policy"), "'sha256-"+base64.StdEncoding.EncodeToString(digest[:])+"'") || !strings.Contains(w.Body.String(), chatH5SDKURL) {
		t.Fatalf("shortcut page: %d %s", w.Code, w.Header().Get("Content-Security-Policy"))
	}
	if !chatMessageActionRoute("/api/integrations/feishu/"+f.record.ConnectionID+"/chat/message-action") || !publicAuthPath("/api/integrations/feishu/"+f.record.ConnectionID+"/chat/message-action/config") {
		t.Fatal("shortcut routes need the platform sign-in, not a Flow session")
	}
	page := f.s.chatMessageActionURL(f.record) + "?bdp_launch_query=%7B%7D"
	for _, tc := range []struct {
		url    string
		status int
	}{{page, 200}, {"https://evil.example/page", 400}, {page + "#x", 400}} {
		r = httptest.NewRequest(http.MethodGet, "/config?url="+strings.ReplaceAll(strings.ReplaceAll(tc.url, "%", "%25"), "#", "%23"), nil)
		r.SetPathValue("provider", f.record.Provider)
		r.SetPathValue("id", f.record.ConnectionID)
		w = httptest.NewRecorder()
		f.s.chatMessageActionConfig(w, r)
		if w.Code != tc.status {
			t.Fatalf("config %s: %d %s", tc.url, w.Code, w.Body.String())
		}
		if tc.status == 200 {
			var config struct {
				AppID     string `json:"appId"`
				Timestamp int64  `json:"timestamp"`
				Nonce     string `json:"nonceStr"`
				Signature string `json:"signature"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &config)
			if config.AppID != f.record.AppID || config.Signature != chatJSAPISignature("ticket_test", config.Nonce, config.Timestamp, tc.url) {
				t.Fatalf("signature: %+v", config)
			}
		}
	}
	if got := chatMessageText("text", `{"text":"@_user_1 hi"}`); got != "hi" {
		t.Fatalf("text: %q", got)
	}
}
