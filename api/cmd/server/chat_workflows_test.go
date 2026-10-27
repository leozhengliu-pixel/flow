package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
	"flow/api/internal/store"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
)

type chatWorkflowTransport func(*http.Request) (*http.Response, error)

func (f chatWorkflowTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type chatWorkflowFixture struct {
	s           *server
	record      domain.ChatIntegrationRecord
	binding     domain.ChatBinding
	identity    domain.ChatIdentity
	credentials chatCredentials
	sent        []map[string]any
	patched     []map[string]any
	responseIDs map[string]string
}

func newChatWorkflowFixture(t *testing.T) *chatWorkflowFixture {
	t.Helper()
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "chat-workflows.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	if err := repo.EnsureChatIntegrationsSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	data := repo.Bootstrap()
	f := &chatWorkflowFixture{s: &server{store: repo, uploadPath: t.TempDir()}, responseIDs: map[string]string{}}
	f.record = domain.ChatIntegrationRecord{WorkspaceKey: data.Workspace.URLKey, Provider: "feishu", ConnectionID: "chat_connection_test", AppID: "cli_workflow_test", TenantKey: "tenant_workflow_test", BotOpenID: "ou_bot_test", Connected: true}
	f.identity = domain.ChatIdentity{UserID: data.Viewer.ID, OpenID: "ou_actor_test", TenantKey: f.record.TenantKey, AuthProvider: "password"}
	f.credentials = chatCredentials{AppSecret: "app_secret_test", EncryptKey: "encryption_test", VerificationToken: "verify_test"}
	f.binding = domain.ChatBinding{ID: "binding_workflow_test", ConnectionID: f.record.ConnectionID, ChatID: "oc_group_test", TeamID: data.Teams[0].ID, Enabled: true, AllowCreate: true, AllowUpdate: true, Events: []string{"issue.created", "issue.status_changed", "comment.created"}, CreatedAt: time.Now().UTC().Add(-time.Hour)}
	if err := repo.PutChatIntegration(t.Context(), f.record); err != nil {
		t.Fatal(err)
	}
	if err := repo.PutChatIdentity(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, f.identity); err != nil {
		t.Fatal(err)
	}
	if err := repo.PutChatBinding(t.Context(), f.record.WorkspaceKey, f.binding); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(f.credentials)
	if err := repo.PutConnectorSecret(t.Context(), chatCredentialID(f.record), raw, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	transport := http.DefaultTransport
	http.DefaultTransport = chatWorkflowTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() != "open.feishu.cn" && r.URL.Hostname() != "open.larksuite.com" {
			return transport.RoundTrip(r)
		}
		body := `{"code":0}`
		switch {
		case strings.Contains(r.URL.Path, "tenant_access_token"):
			body = `{"code":0,"tenant_access_token":"workflow_token","expire":7200}`
		case strings.Contains(r.URL.Path, "/im/v1/chats/"):
			body = fmt.Sprintf(`{"code":0,"data":{"external":false,"tenant_key":%q,"chat_mode":"group"}}`, f.record.TenantKey)
		case strings.Contains(r.URL.Path, "/im/v1/messages/") && r.Method == http.MethodPatch:
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			f.patched = append(f.patched, payload)
		case strings.Contains(r.URL.Path, "/im/v1/messages") && r.Method == http.MethodPost:
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["msg_type"] == "interactive" {
				content, _ := payload["content"].(string)
				if len(content) > 30<<10 {
					t.Fatal("outgoing card exceeds platform30KiB limit")
				}
				var card any
				if err := json.Unmarshal([]byte(content), &card); err != nil {
					t.Fatal(err)
				}
				chatAssertInputSchema(t, card)
			}
			f.sent = append(f.sent, payload)
			key, _ := payload["uuid"].(string)
			id := f.responseIDs[key]
			if id == "" {
				id = fmt.Sprintf("om_workflow_%d", len(f.responseIDs)+1)
				f.responseIDs[key] = id
			}
			chatID := "oc_dm_test"
			if r.URL.Query().Get("receive_id_type") == "chat_id" || strings.HasSuffix(r.URL.Path, "/reply") {
				chatID = f.binding.ChatID
			}
			body = fmt.Sprintf(`{"code":0,"data":{"message_id":%q,"chat_id":%q}}`, id, chatID)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = transport })
	return f
}

func chatAssertInputSchema(t *testing.T, value any) {
	t.Helper()
	switch value := value.(type) {
	case map[string]any:
		if value["tag"] == "input" {
			maximum, ok := value["max_length"].(float64)
			if !ok || maximum < 1 || maximum > 1000 {
				t.Fatalf("invalid platform input max_length: %#v", value)
			}
			if initial, ok := value["default_value"].(string); ok && utf8.RuneCountInString(initial) > int(maximum) {
				t.Fatalf("input default exceeds max_length: %#v", value)
			}
		}
		for _, nested := range value {
			chatAssertInputSchema(t, nested)
		}
	case []any:
		for _, nested := range value {
			chatAssertInputSchema(t, nested)
		}
	}
}

func (f *chatWorkflowFixture) messageEnvelope() chatWebhookEnvelope {
	var envelope chatWebhookEnvelope
	envelope.Schema = "2.0"
	envelope.Header.AppID = f.record.AppID
	envelope.Header.TenantKey = f.record.TenantKey
	envelope.Header.Token = f.credentials.VerificationToken
	envelope.Header.EventID = "evt_first"
	envelope.Header.EventType = "im.message.receive_v1"
	envelope.Event.Sender.SenderType = "user"
	envelope.Event.Sender.TenantKey = f.record.TenantKey
	envelope.Event.Sender.SenderID.OpenID = f.identity.OpenID
	envelope.Event.Message.MessageID = "om_user_command"
	envelope.Event.Message.ChatID = f.binding.ChatID
	envelope.Event.Message.ChatType = "group"
	envelope.Event.Message.MessageType = "text"
	envelope.Event.Message.Content = `{"text":"@_user_1 创建事项 Fix export"}`
	raw, _ := json.Marshal([]map[string]any{{"key": "@_user_1", "id": map[string]string{"open_id": f.record.BotOpenID}}})
	_ = json.Unmarshal(raw, &envelope.Event.Message.Mentions)
	return envelope
}

func chatEncryptedTestBody(t *testing.T, value any, key string) []byte {
	t.Helper()
	plain, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(hash[:])
	if err != nil {
		t.Fatal(err)
	}
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	plain = append(plain, bytes.Repeat([]byte{byte(padding)}, padding)...)
	iv := bytes.Repeat([]byte{3}, aes.BlockSize)
	encrypted := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(encrypted, plain)
	raw, _ := json.Marshal(map[string]string{"encrypt": base64.StdEncoding.EncodeToString(append(iv, encrypted...))})
	return raw
}

func (f *chatWorkflowFixture) callback(t *testing.T, envelope any, timestamp time.Time, signature bool) *httptest.ResponseRecorder {
	t.Helper()
	body := chatEncryptedTestBody(t, envelope, f.credentials.EncryptKey)
	r := httptest.NewRequest(http.MethodPost, "/api/integrations/feishu/"+f.record.ConnectionID+"/chat/webhook", bytes.NewReader(body))
	r.SetPathValue("provider", f.record.Provider)
	r.SetPathValue("id", f.record.ConnectionID)
	stamp := strconv.FormatInt(timestamp.Unix(), 10)
	r.Header.Set("X-Lark-Request-Timestamp", stamp)
	r.Header.Set("X-Lark-Request-Nonce", "nonce_test")
	if signature {
		r.Header.Set("X-Lark-Signature", larkevent.Signature(stamp, "nonce_test", f.credentials.EncryptKey, string(body)))
	}
	w := httptest.NewRecorder()
	f.s.handleChatWebhook(w, r)
	return w
}

func TestChatWebhookAuthenticatesTenantReplayAndMessageDedup(t *testing.T) {
	f := newChatWorkflowFixture(t)
	envelope := f.messageEnvelope()
	if got := f.callback(t, envelope, time.Now(), true); got.Code != 200 {
		t.Fatalf("valid callback: %d %s", got.Code, got.Body.String())
	}
	envelope.Header.EventID = "evt_duplicate_with_different_id"
	if got := f.callback(t, envelope, time.Now(), true); got.Code != 200 {
		t.Fatalf("duplicate callback: %d", got.Code)
	}
	jobs, err := f.s.store.ClaimChatJobs(t.Context(), f.record.WorkspaceKey, "inbound", 10, time.Now())
	if err != nil || len(jobs) != 1 {
		t.Fatalf("message_id dedup failed: %#v %v", jobs, err)
	}
	for _, tc := range []struct {
		name   string
		edit   func(*chatWebhookEnvelope)
		stamp  time.Time
		sign   bool
		status int
	}{
		{"cross app", func(e *chatWebhookEnvelope) { e.Header.AppID = "cli_other" }, time.Now(), true, 403},
		{"cross tenant", func(e *chatWebhookEnvelope) { e.Header.TenantKey = "tenant_other" }, time.Now(), true, 403},
		{"wrong token", func(e *chatWebhookEnvelope) { e.Header.Token = "wrong" }, time.Now(), true, 401},
		{"no signature", func(e *chatWebhookEnvelope) {}, time.Now(), false, 401},
		{"old signature", func(e *chatWebhookEnvelope) {}, time.Now().Add(-10 * time.Minute), true, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := envelope
			tc.edit(&e)
			if got := f.callback(t, e, tc.stamp, tc.sign); got.Code != tc.status {
				t.Fatalf("status %d want %d body %s", got.Code, tc.status, got.Body.String())
			}
		})
	}
	bot := envelope
	bot.Event.Sender.SenderType = "app"
	bot.Event.Message.MessageID = "bot_echo"
	_, _, accepted, err := f.s.chatCommandForEnvelope(t.Context(), f.record, bot)
	if err != nil || accepted {
		t.Fatal("bot echo was accepted")
	}
}

func TestChatPendingConnectionAcceptsOnlyEncryptedChallenge(t *testing.T) {
	f := newChatWorkflowFixture(t)
	f.record.Connected = false
	if err := f.s.store.PutChatIntegration(t.Context(), f.record); err != nil {
		t.Fatal(err)
	}
	w := f.callback(t, map[string]string{"type": "url_verification", "token": f.credentials.VerificationToken, "challenge": "challenge_test"}, time.Now(), false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"challenge":"challenge_test"`) {
		t.Fatalf("pending challenge: %d %s", w.Code, w.Body.String())
	}
	if w := f.callback(t, f.messageEnvelope(), time.Now(), true); w.Code != 403 {
		t.Fatalf("pending connection accepted an ordinary event: %d", w.Code)
	}
}

func TestChatCreateUsesCustomWorkflowActorAndStableCommandKey(t *testing.T) {
	f := newChatWorkflowFixture(t)
	if err := f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "team.settings_updated", f.binding.TeamID, nil, func(data *domain.Bootstrap) error {
		data.States = append(data.States, domain.WorkflowState{ID: "state_chat_custom", TeamID: f.binding.TeamID, Name: "Ready", Type: "unstarted", Default: true})
		settings := data.TeamSettings[f.binding.TeamID]
		settings.DefaultStateID = "state_chat_custom"
		data.TeamSettings[f.binding.TeamID] = settings
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	formJob := domain.ChatJob{ID: "form_job", WorkspaceKey: f.record.WorkspaceKey, ConnectionID: f.record.ConnectionID, Kind: "inbound", DedupKey: "form_command"}
	formJob.Payload, _ = json.Marshal(chatInboundCommand{Kind: "create_form", OpenID: f.identity.OpenID, ChatID: f.binding.ChatID, MessageID: "om_source", Title: "Fix export"})
	if err := f.s.processChatJob(t.Context(), formJob); err != nil {
		t.Fatal(err)
	}
	actionID := "chat_action_" + chatKey(f.record.ConnectionID, formJob.DedupKey, "create")
	stored, err := f.s.store.GetChatAction(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, actionID)
	if err != nil {
		t.Fatal(err)
	}
	var action chatCardAction
	if err := json.Unmarshal(stored.Payload, &action); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(action.StateIDs, []string{"state_chat_custom"}) || action.PersonalMessage == "" || action.PersonalChatID == f.binding.ChatID {
		t.Fatalf("illegal global status or shared draft: %+v", action)
	}
	command := chatInboundCommand{Kind: "card_action", OpenID: f.identity.OpenID, ChatID: action.PersonalChatID, MessageID: action.PersonalMessage, ActionID: actionID, Values: map[string]json.RawMessage{"title": json.RawMessage(`"Fix export"`), "stateId": json.RawMessage(`"state_chat_custom"`), "assigneeId": json.RawMessage(`"__unassigned"`), "priority": json.RawMessage(`"2"`)}}
	job := domain.ChatJob{ID: "create_job", WorkspaceKey: f.record.WorkspaceKey, ConnectionID: f.record.ConnectionID, Kind: "inbound", DedupKey: "confirmed_create"}
	job.Payload, _ = json.Marshal(command)
	for attempt := range 2 {
		if attempt == 1 {
			command.Values["title"] = json.RawMessage(`"A second submitted title"`)
			job.Payload, _ = json.Marshal(command)
		}
		if err := f.s.processChatJob(t.Context(), job); err != nil {
			t.Fatal(err)
		}
	}
	issueID := "issue_chat_" + chatKey(f.record.ConnectionID, actionID)
	issue, err := f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, issueID)
	if err != nil || issue.Title != "Fix export" || issue.State.ID != "state_chat_custom" || issue.Creator.ID != f.identity.UserID || issue.Version != 1 {
		t.Fatalf("wrong workflow, actor, or duplicated mutation: %+v %v", issue, err)
	}
	results, err := f.s.store.ClaimChatJobs(t.Context(), f.record.WorkspaceKey, "card", 10, time.Now())
	if err != nil || len(results) != 1 {
		t.Fatalf("result dedup failed: %#v %v", results, err)
	}
	if chatActionMatches(stored, action, "ou_another", action.PersonalChatID, action.PersonalMessage) || chatActionMatches(stored, action, f.identity.OpenID, f.binding.ChatID, action.PersonalMessage) {
		t.Fatal("personal form accepted another operator or group replay")
	}
}

func TestChatWorkflowDefaultsRejectForeignTemplateAndSupportInheritance(t *testing.T) {
	f := newChatWorkflowFixture(t)
	data := f.s.store.Bootstrap()
	data.TeamSettings[f.binding.TeamID] = domain.TeamSettings{TeamID: f.binding.TeamID, InheritWorkflowStatuses: true, ParentTeamID: "parent_chat", DefaultStateID: "state_todo"}
	data.States = append(data.States, domain.WorkflowState{ID: "parent_ready", TeamID: "parent_chat", Name: "Parent ready", Type: "unstarted", Default: true})
	_, ids, defaultID := chatStateOptions(data, f.binding.TeamID)
	if !slices.Equal(ids, []string{"parent_ready"}) || defaultID != "parent_ready" {
		t.Fatalf("inherited workflow wrong: %v %s", ids, defaultID)
	}
	binding := f.binding
	binding.TemplateID = "template_chat"
	data.IssueTemplates = append(data.IssueTemplates, domain.IssueTemplate{ID: binding.TemplateID, TeamID: binding.TeamID, Name: "Invalid template", StateID: "state_todo"})
	if _, err := chatCreateDefaults(data, binding, "Issue"); err == nil {
		t.Fatal("template foreign global state accepted")
	}
	data.IssueTemplates[len(data.IssueTemplates)-1].StateID = "parent_ready"
	if _, err := chatCreateDefaults(data, binding, "Issue"); err != nil {
		t.Fatalf("valid inherited workflow template rejected: %v", err)
	}
}

func TestChatEventMappingAndQueueFiltersOldBindingsAndDuplicates(t *testing.T) {
	f := newChatWorkflowFixture(t)
	issue := f.s.store.Bootstrap().Issues[0]
	event := domain.DomainEvent{ID: "evt_notifications", Type: "issue.updated", AggregateID: issue.ID, CreatedAt: time.Now(), PreviousValues: json.RawMessage(`{"state":{"id":"old"},"assignee":null}`)}
	if got := chatEventKinds(event); !slices.Equal(got, []string{"refresh", "issue.status_changed", "issue.assignee_changed"}) {
		t.Fatalf("event mapping: %v", got)
	}
	for range 2 {
		if err := f.s.queueChatDomainEvent(t.Context(), f.record.WorkspaceKey, event); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := f.s.store.ClaimChatJobs(t.Context(), f.record.WorkspaceKey, "notification", 10, time.Now())
	if err != nil || len(jobs) != 2 {
		t.Fatalf("event queue duplicate or opt out ignored: %#v %v", jobs, err)
	}
	old := event
	old.ID = "evt_historical"
	old.CreatedAt = f.binding.CreatedAt.Add(-time.Minute)
	if err := f.s.queueChatDomainEvent(t.Context(), f.record.WorkspaceKey, old); err != nil {
		t.Fatal(err)
	}
	newJobs, err := f.s.store.ClaimChatJobs(t.Context(), f.record.WorkspaceKey, "notification", 10, time.Now())
	if err != nil || len(newJobs) != 0 {
		t.Fatal("new binding replayed historical events")
	}
	private := issue
	private.Team.Private = true
	if chatIssueMatchesBinding(private, f.binding) {
		t.Fatal("private issue allowed into group card")
	}
	other := issue
	other.Team.ID = "team_other"
	if chatIssueMatchesBinding(other, f.binding) {
		t.Fatal("cross-team issue allowed into group card")
	}
}

func TestChatPrivateUpdateFormPreservesConcurrentWebChanges(t *testing.T) {
	f := newChatWorkflowFixture(t)
	data := f.s.store.Bootstrap()
	issue := data.Issues[0]
	_, actions, err := f.s.chatIssueCard(t.Context(), f.record, f.binding, issue, "shared_card", "om_shared", "")
	if err != nil {
		t.Fatal(err)
	}
	command := chatInboundCommand{Kind: "card_action", OpenID: f.identity.OpenID, ChatID: f.binding.ChatID, MessageID: "om_shared", ActionID: actions[0].ID}
	job := domain.ChatJob{ID: "open_state_job", WorkspaceKey: f.record.WorkspaceKey, ConnectionID: f.record.ConnectionID, Kind: "inbound", DedupKey: "open_state"}
	job.Payload, _ = json.Marshal(command)
	if err := f.s.processChatJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	id := "chat_action_" + chatKey(f.record.ConnectionID, job.DedupKey, "state")
	stored, err := f.s.store.GetChatAction(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, id)
	if err != nil {
		t.Fatal(err)
	}
	var action chatCardAction
	_ = json.Unmarshal(stored.Payload, &action)
	if action.PersonalChatID == f.binding.ChatID || action.ExpectedVersion != issue.Version {
		t.Fatalf("form was shared or version omitted: %+v", action)
	}
	ctx := withChatIdentityAuthentication(t.Context(), f.identity)
	var changed domain.Issue
	if err := f.s.chatAuthorizedMutation(ctx, f.record.WorkspaceKey, f.identity.UserID, http.MethodPatch, "/api/issue-records/"+issue.ID, map[string]any{"title": "Changed on the web", "expectedVersion": issue.Version}, &changed); err != nil {
		t.Fatal(err)
	}
	stateID := action.StateIDs[len(action.StateIDs)-1]
	command = chatInboundCommand{Kind: "card_action", OpenID: f.identity.OpenID, ChatID: action.PersonalChatID, MessageID: action.PersonalMessage, ActionID: id, Values: map[string]json.RawMessage{"stateId": json.RawMessage(strconv.Quote(stateID))}}
	job.ID, job.DedupKey = "submit_state_job", "submit_state"
	job.Payload, _ = json.Marshal(command)
	if err := f.s.processChatJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	after, err := f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, issue.ID)
	if err != nil || after.Version != changed.Version || after.Title != changed.Title || after.State.ID != changed.State.ID {
		t.Fatalf("stale form overwrote a web change: %+v %v", after, err)
	}
	last := f.sent[len(f.sent)-1]
	if last["msg_type"] != "text" || !strings.Contains(last["content"].(string), "changed after") {
		t.Fatalf("missing conflict result: %#v", last)
	}
}

func TestChatCommentIsAttributedAndIdempotentAfterWorkerRestart(t *testing.T) {
	f := newChatWorkflowFixture(t)
	data := f.s.store.Bootstrap()
	issue := data.Issues[0]
	before := len(data.Comments[issue.ID])
	action := chatCardAction{Kind: "comment", BindingID: f.binding.ID, BindingScope: chatBindingFingerprint(f.binding), ChatID: f.binding.ChatID, PersonalChatID: "oc_dm_test", PersonalMessage: "om_private_comment", OpenID: f.identity.OpenID, IssueID: issue.ID, ExpectedVersion: issue.Version}
	raw, _ := json.Marshal(action)
	stored := domain.ChatAction{ID: "comment_form_test", WorkspaceKey: f.record.WorkspaceKey, ConnectionID: f.record.ConnectionID, UserID: f.identity.UserID, Payload: raw, ExpiresAt: time.Now().Add(time.Minute)}
	if err := f.s.store.PutChatAction(t.Context(), stored); err != nil {
		t.Fatal(err)
	}
	command := chatInboundCommand{Kind: "card_action", OpenID: f.identity.OpenID, ChatID: action.PersonalChatID, MessageID: action.PersonalMessage, ActionID: stored.ID, Values: map[string]json.RawMessage{"comment": json.RawMessage(`"Confirmed the fix"`)}}
	job := domain.ChatJob{ID: "comment_job", WorkspaceKey: f.record.WorkspaceKey, ConnectionID: f.record.ConnectionID, Kind: "inbound", DedupKey: "comment_command"}
	job.Payload, _ = json.Marshal(command)
	for attempt := range 2 {
		if attempt == 1 {
			command.Values["comment"] = json.RawMessage(`"A second submitted comment body"`)
			job.Payload, _ = json.Marshal(command)
		}
		if err := f.s.processChatJob(t.Context(), job); err != nil {
			t.Fatal(err)
		}
	}
	after, ok := f.s.store.BootstrapFor(f.record.WorkspaceKey)
	if !ok || len(after.Comments[issue.ID]) != before+1 {
		t.Fatalf("comment replay duplicated writes: %d want %d", len(after.Comments[issue.ID]), before+1)
	}
	comments := after.Comments[issue.ID]
	comment := comments[len(comments)-1]
	if comment.User.ID != f.identity.UserID || comment.Body != "Confirmed the fix" {
		t.Fatalf("comment lost actual operator: %+v", comment)
	}
	if err := f.s.store.DeleteChatIdentity(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.processChatJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	latest, _ := f.s.store.BootstrapFor(f.record.WorkspaceKey)
	if len(latest.Comments[issue.ID]) != len(comments) {
		t.Fatal("queued action wrote after identity unlinked")
	}
}

func TestChatPersonalCallbacksDedupAcrossDifferentEventIDsAndBodies(t *testing.T) {
	f := newChatWorkflowFixture(t)
	action := chatCardAction{Kind: "create", BindingID: f.binding.ID, ChatID: f.binding.ChatID, PersonalChatID: "oc_dm_test", PersonalMessage: "om_private", OpenID: f.identity.OpenID}
	raw, _ := json.Marshal(action)
	stored := domain.ChatAction{ID: "chat_personal_dedup", WorkspaceKey: f.record.WorkspaceKey, ConnectionID: f.record.ConnectionID, UserID: f.identity.UserID, Payload: raw, ExpiresAt: time.Now().Add(time.Minute)}
	if err := f.s.store.PutChatAction(t.Context(), stored); err != nil {
		t.Fatal(err)
	}
	envelope := f.messageEnvelope()
	envelope.Header.EventType = "card.action.trigger"
	envelope.Event.Operator.TenantKey = f.record.TenantKey
	envelope.Event.Operator.OpenID = f.identity.OpenID
	envelope.Event.Context.ChatID = action.PersonalChatID
	envelope.Event.Context.MessageID = action.PersonalMessage
	envelope.Event.Action.Value = map[string]json.RawMessage{"actionId": json.RawMessage(strconv.Quote(stored.ID))}
	envelope.Event.Action.FormValue = map[string]json.RawMessage{"title": json.RawMessage(`"Original title"`)}
	first, key, accepted, err := f.s.chatCommandForEnvelope(t.Context(), f.record, envelope)
	if err != nil || !accepted {
		t.Fatalf("first callback: %v %v", accepted, err)
	}
	envelope.Header.EventID = "a_new_callback_id"
	envelope.Event.Action.FormValue = map[string]json.RawMessage{"title": json.RawMessage(`"Another body"`)}
	second, secondKey, accepted, err := f.s.chatCommandForEnvelope(t.Context(), f.record, envelope)
	if err != nil || !accepted || key != secondKey {
		t.Fatalf("single-use form dedup used event ID: %q %q %v", key, secondKey, err)
	}
	for index, command := range []chatInboundCommand{first, second} {
		payload, _ := json.Marshal(command)
		inserted, err := f.s.store.EnqueueChatJob(t.Context(), domain.ChatJob{ID: fmt.Sprintf("personal_callback_%d", index), WorkspaceKey: f.record.WorkspaceKey, ConnectionID: f.record.ConnectionID, Kind: "inbound", DedupKey: key, Payload: payload})
		if err != nil || inserted != (index == 0) {
			t.Fatalf("second body entered single-use command queue: %v %v", inserted, err)
		}
	}
	jobs, err := f.s.store.ClaimChatJobs(t.Context(), f.record.WorkspaceKey, "inbound", 10, time.Now())
	if err != nil || len(jobs) != 1 {
		t.Fatalf("personal callback queue: %#v %v", jobs, err)
	}
	var persisted chatInboundCommand
	_ = json.Unmarshal(jobs[0].Payload, &persisted)
	if chatString(persisted.Values, "title") != "Original title" {
		t.Fatal("duplicate callback replaced confirmed form body")
	}
}

func TestChatSharedCardNeverGrantsViewerEditPermission(t *testing.T) {
	f := newChatWorkflowFixture(t)
	data := f.s.store.Bootstrap()
	user, verification, err := f.s.store.Register(t.Context(), "Chat viewer", "chat-viewer@example.test", "long-password-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.VerifyEmail(t.Context(), verification); err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.EnsureWorkspaceMembership(t.Context(), data.Workspace.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	viewer := domain.ChatIdentity{UserID: user.ID, OpenID: "ou_viewer_test", TenantKey: f.record.TenantKey, AuthProvider: "password"}
	if err := f.s.store.PutChatIdentity(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, viewer); err != nil {
		t.Fatal(err)
	}
	issue := data.Issues[0]
	_, actions, err := f.s.chatIssueCard(t.Context(), f.record, f.binding, issue, "viewer_card", "om_shared_viewer", "")
	if err != nil {
		t.Fatal(err)
	}
	job := domain.ChatJob{ID: "viewer_job", WorkspaceKey: f.record.WorkspaceKey, ConnectionID: f.record.ConnectionID, Kind: "inbound", DedupKey: "viewer_open"}
	job.Payload, _ = json.Marshal(chatInboundCommand{Kind: "card_action", OpenID: viewer.OpenID, ChatID: f.binding.ChatID, MessageID: "om_shared_viewer", ActionID: actions[0].ID})
	if err := f.s.processChatJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	personalID := "chat_action_" + chatKey(f.record.ConnectionID, job.DedupKey, "state")
	if _, err := f.s.store.GetChatAction(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, personalID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("viewer received an edit form: %v", err)
	}
	if len(f.sent) != 1 || f.sent[0]["msg_type"] != "text" || !strings.Contains(f.sent[0]["content"].(string), "permission") {
		t.Fatalf("viewer denial not private: %#v", f.sent)
	}
}

func TestChatProjectNotificationsDoNotDiscloseMixedPrivateTeams(t *testing.T) {
	f := newChatWorkflowFixture(t)
	data := f.s.store.Bootstrap()
	data.Teams = append(data.Teams, domain.Team{ID: "private_project_team", Private: true})
	project := domain.Project{ID: "mixed_project", Name: "Private roadmap", TeamIDs: []string{f.binding.TeamID, "private_project_team"}}
	data.Projects = append(data.Projects, project)
	if chatProjectMatchesBinding(data, f.binding, project.ID) {
		t.Fatal("project with a private associated team was allowed into public group")
	}
	data.Projects[len(data.Projects)-1].TeamIDs = []string{f.binding.TeamID}
	if !chatProjectMatchesBinding(data, f.binding, project.ID) {
		t.Fatal("public project rejected")
	}
}

func TestChatOpenFormAndQueuedNotificationRejectChangedBindingScope(t *testing.T) {
	f := newChatWorkflowFixture(t)
	job := domain.ChatJob{ID: "scope_form_job", WorkspaceKey: f.record.WorkspaceKey, ConnectionID: f.record.ConnectionID, Kind: "inbound", DedupKey: "scope_form"}
	job.Payload, _ = json.Marshal(chatInboundCommand{Kind: "create_form", OpenID: f.identity.OpenID, ChatID: f.binding.ChatID, MessageID: "om_scope_source", Title: "Keep the confirmed scope"})
	if err := f.s.processChatJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	actionID := "chat_action_" + chatKey(f.record.ConnectionID, job.DedupKey, "create")
	stored, err := f.s.store.GetChatAction(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, actionID)
	if err != nil {
		t.Fatal(err)
	}
	var action chatCardAction
	_ = json.Unmarshal(stored.Payload, &action)
	issue := f.s.store.Bootstrap().Issues[0]
	event := domain.DomainEvent{ID: "scope_notification", Type: "issue.created", AggregateID: issue.ID, CreatedAt: time.Now()}
	if err := f.s.queueChatDomainEvent(t.Context(), f.record.WorkspaceKey, event); err != nil {
		t.Fatal(err)
	}
	notifications, err := f.s.store.ClaimChatJobs(t.Context(), f.record.WorkspaceKey, "notification", 10, time.Now())
	if err != nil || len(notifications) != 1 {
		t.Fatalf("notification setup: %#v %v", notifications, err)
	}
	changed := f.binding
	changed.ChatID = "oc_a_different_group"
	if err := f.s.store.PutChatBinding(t.Context(), f.record.WorkspaceKey, changed); err != nil {
		t.Fatal(err)
	}
	job.ID, job.DedupKey = "scope_submit_job", "scope_submit"
	job.Payload, _ = json.Marshal(chatInboundCommand{Kind: "card_action", OpenID: f.identity.OpenID, ChatID: action.PersonalChatID, MessageID: action.PersonalMessage, ActionID: actionID, Values: map[string]json.RawMessage{"stateId": json.RawMessage(strconv.Quote(action.StateIDs[0]))}})
	if err := f.s.processChatJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	issueID := "issue_chat_" + chatKey(f.record.ConnectionID, actionID)
	if _, err := f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, issueID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("open form silently retargeted: %v", err)
	}
	before := len(f.sent)
	if err := f.s.processChatJob(t.Context(), notifications[0]); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != before {
		t.Fatal("old queued notification disclosed to new binding scope")
	}
	for _, change := range []func(*domain.ChatBinding){func(b *domain.ChatBinding) { b.TeamID = "different_team" }, func(b *domain.ChatBinding) { b.ProjectID = "different_project" }, func(b *domain.ChatBinding) { b.TemplateID = "different_template" }, func(b *domain.ChatBinding) { b.ChatID = "different_chat" }} {
		copy := f.binding
		change(&copy)
		if chatBindingFingerprint(copy) == chatBindingFingerprint(f.binding) {
			t.Fatal("binding fingerprint omitted a target dimension")
		}
	}
}

func TestChatNotificationPersistsMessageAndRefreshUsesCurrentVersion(t *testing.T) {
	f := newChatWorkflowFixture(t)
	issue := f.s.store.Bootstrap().Issues[0]
	job := domain.ChatJob{ID: "notification_job", WorkspaceKey: f.record.WorkspaceKey, ConnectionID: f.record.ConnectionID, Kind: "notification", DedupKey: "created_notification"}
	job.Payload, _ = json.Marshal(chatNotificationJob{BindingID: f.binding.ID, BindingScope: chatBindingFingerprint(f.binding), IssueID: issue.ID, Kind: "issue.created"})
	if err := f.s.processChatJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 1 || f.sent[0]["receive_id"] != f.binding.ChatID || f.sent[0]["msg_type"] != "interactive" {
		t.Fatalf("notification target or kind: %#v", f.sent)
	}
	messages, err := f.s.store.ChatMessagesForIssue(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, issue.ID)
	if err != nil || len(messages) != 1 || messages[0].CreatedAt.IsZero() {
		t.Fatalf("message mapping not durable: %#v %v", messages, err)
	}
	ctx := withChatIdentityAuthentication(t.Context(), f.identity)
	var latest domain.Issue
	if err := f.s.chatAuthorizedMutation(ctx, f.record.WorkspaceKey, f.identity.UserID, http.MethodPatch, "/api/issue-records/"+issue.ID, map[string]any{"title": "Latest committed title", "expectedVersion": issue.Version}, &latest); err != nil {
		t.Fatal(err)
	}
	job.ID, job.DedupKey = "refresh_job", "refresh_notification"
	job.Payload, _ = json.Marshal(chatNotificationJob{BindingID: f.binding.ID, BindingScope: chatBindingFingerprint(f.binding), IssueID: issue.ID, Kind: "refresh"})
	for range 2 {
		if err := f.s.processChatJob(t.Context(), job); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.patched) != 1 || !strings.Contains(f.patched[0]["content"].(string), latest.Title) {
		t.Fatalf("old event restored stale card or duplicate patch: %#v", f.patched)
	}
	updated, err := f.s.store.ChatMessage(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, messages[0].MessageID)
	if err != nil || updated.Version != strconv.FormatInt(latest.Version, 10) || !updated.CreatedAt.Equal(messages[0].CreatedAt) {
		t.Fatalf("card version/creation age lost: %+v %v", updated, err)
	}
}

func TestChatUpdateReceiptRecoversAfterCommitAndLaterWebEdit(t *testing.T) {
	f := newChatWorkflowFixture(t)
	data := f.s.store.Bootstrap()
	issue := data.Issues[0]
	states := statesForTeam(&data, issue.Team.ID)
	target := states[len(states)-1].ID
	if target == issue.State.ID {
		target = states[0].ID
	}
	actionID := "chat_update_receipt_action"
	action := chatCardAction{Kind: "state", BindingID: f.binding.ID, BindingScope: chatBindingFingerprint(f.binding), ChatID: f.binding.ChatID, PersonalChatID: "oc_dm_test", PersonalMessage: "om_receipt_form", OpenID: f.identity.OpenID, IssueID: issue.ID, ExpectedVersion: issue.Version, StateIDs: []string{target}}
	raw, _ := json.Marshal(action)
	if err := f.s.store.PutChatAction(t.Context(), domain.ChatAction{ID: actionID, WorkspaceKey: f.record.WorkspaceKey, ConnectionID: f.record.ConnectionID, UserID: f.identity.UserID, Payload: raw, ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	ctx := withChatIdentityAuthentication(t.Context(), f.identity)
	mutationCtx := store.WithChatMutationKey(ctx, chatKey(f.record.ConnectionID, actionID))
	var committed, webEdited domain.Issue
	if err := f.s.chatAuthorizedMutation(mutationCtx, f.record.WorkspaceKey, f.identity.UserID, http.MethodPatch, "/api/issue-records/"+issue.ID, domain.IssueUpdateInput{StateID: &target, ExpectedVersion: &issue.Version}, &committed); err != nil {
		t.Fatal(err)
	}
	if err := f.s.chatAuthorizedMutation(ctx, f.record.WorkspaceKey, f.identity.UserID, http.MethodPatch, "/api/issue-records/"+issue.ID, map[string]any{"title": "A later web edit must remain", "expectedVersion": committed.Version}, &webEdited); err != nil {
		t.Fatal(err)
	}
	// The worker crashed after the first PATCH committed, before it enqueued a
	// result. Its original confirmation must resume without replaying its patch.
	job := domain.ChatJob{ID: "receipt_recovery_job", WorkspaceKey: f.record.WorkspaceKey, ConnectionID: f.record.ConnectionID, Kind: "inbound", DedupKey: "receipt_recovery"}
	job.Payload, _ = json.Marshal(chatInboundCommand{Kind: "card_action", OpenID: f.identity.OpenID, ChatID: action.PersonalChatID, MessageID: action.PersonalMessage, ActionID: actionID, Values: map[string]json.RawMessage{"stateId": json.RawMessage(strconv.Quote(target))}})
	if err := f.s.processChatJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	after, err := f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, issue.ID)
	if err != nil || after.Version != webEdited.Version || after.Title != webEdited.Title || after.State.ID != target {
		t.Fatalf("receipt replay overwrote later mutation: %+v %v", after, err)
	}
	if len(f.sent) != 0 {
		t.Fatalf("successful replay was reported as conflict: %#v", f.sent)
	}
	results, err := f.s.store.ClaimChatJobs(t.Context(), f.record.WorkspaceKey, "card", 10, time.Now())
	if err != nil || len(results) != 1 {
		t.Fatalf("committed update result was lost after crash: %#v %v", results, err)
	}
}

func TestChatOperationFeedbackAvoidsDuplicateGroupCardsWithoutLosingOtherGroups(t *testing.T) {
	f := newChatWorkflowFixture(t)
	issue := f.s.store.Bootstrap().Issues[0]
	if err := f.s.chatSendIssueCard(t.Context(), f.record, f.binding, issue, "threaded_create", "om_source_command", "result"); err != nil {
		t.Fatal(err)
	}
	job := domain.ChatJob{ID: "created_after_result", WorkspaceKey: f.record.WorkspaceKey, ConnectionID: f.record.ConnectionID, Kind: "notification", DedupKey: "created_after_result"}
	job.Payload, _ = json.Marshal(chatNotificationJob{BindingID: f.binding.ID, BindingScope: chatBindingFingerprint(f.binding), IssueID: issue.ID, Kind: "issue.created"})
	if err := f.s.processChatJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 1 {
		t.Fatalf("creation source reply and notification duplicated the same group: %#v", f.sent)
	}
	job.ID, job.Kind, job.DedupKey = "update_result", "card", "update_result"
	job.Payload, _ = json.Marshal(chatNotificationJob{BindingID: f.binding.ID, BindingScope: chatBindingFingerprint(f.binding), IssueID: issue.ID, Kind: "result", Operation: "state", PersonalOpenID: f.identity.OpenID})
	if err := f.s.processChatJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 2 || f.sent[1]["msg_type"] != "text" || f.sent[1]["receive_id"] != f.identity.OpenID {
		t.Fatalf("update result sent another group card: %#v", f.sent)
	}
	other := f.binding
	other.ID, other.ChatID = "binding_another_group", "oc_another_group"
	if err := f.s.store.PutChatBinding(t.Context(), f.record.WorkspaceKey, other); err != nil {
		t.Fatal(err)
	}
	job.ID, job.Kind, job.DedupKey = "notify_other_group", "notification", "notify_other_group"
	job.Payload, _ = json.Marshal(chatNotificationJob{BindingID: other.ID, BindingScope: chatBindingFingerprint(other), IssueID: issue.ID, Kind: "issue.created"})
	if err := f.s.processChatJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 3 || f.sent[2]["receive_id"] != other.ChatID {
		t.Fatalf("source group suppression lost another group's subscription: %#v", f.sent)
	}
}
