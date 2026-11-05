package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// lowerChatOperationLimit makes every workspace's bootstrap exceed the
// internal response limit, as a large production workspace's (~10 MiB) does.
func lowerChatOperationLimit(t *testing.T, limit int) {
	t.Helper()
	previous := chatOperationResponseLimit
	chatOperationResponseLimit = limit
	t.Cleanup(func() { chatOperationResponseLimit = previous })
}

// A direct message in a workspace whose bootstrap exceeds the internal
// response limit still reaches the Flow Agent: workspace metadata is read in
// process, never encoded.
func TestChatInboundDirectMessageRunsTheAgentInALargeWorkspace(t *testing.T) {
	f := newChatDMFixture(t, func(int, string) string { return chatDMTextTurn("你好！有什么可以帮你？") })
	logs := captureChatLogs(t)
	data, _ := f.s.store.BootstrapFor(f.record.WorkspaceKey)
	raw, _ := json.Marshal(data)
	lowerChatOperationLimit(t, 1024)
	if len(raw) <= chatOperationResponseLimit {
		t.Fatalf("the workspace's bootstrap (%d bytes) does not exceed the limit", len(raw))
	}
	f.text(t, "om_large_hello", "你好")
	chatDMReplies.Wait()
	sessions := f.sessions(t)
	if len(sessions) != 1 || sessions[0].Origin != "feishu" || len(sessions[0].Messages) != 2 || sessions[0].Messages[1].Content != "你好！有什么可以帮你？" {
		t.Fatalf("agent chat: %+v", sessions)
	}
	sent := f.sent()
	if len(sent) != 1 || !strings.Contains(f.lastUpdate(sent[0].ID), "你好！有什么可以帮你？") {
		t.Fatalf("reply card: %+v", sent)
	}
	outcomes := map[string]bool{}
	for _, record := range logs.records(t, "chat direct message") {
		outcomes[record["outcome"].(string)] = true
	}
	if !outcomes["dm_agent_queued"] || !outcomes["dm_agent_answered"] {
		t.Fatalf("direct message records: %v", outcomes)
	}
	if len(logs.records(t, "chat operation response exceeds limit")) != 0 {
		t.Fatal("the bootstrap went through the response limit")
	}
}

// An internal operation over the limit fails visibly: the member hears it,
// the job fails on its first attempt instead of backing off, the integration
// shows the error, and the log names the endpoint and size.
func TestChatInboundOverLimitOperationFailsOnceAndVisibly(t *testing.T) {
	f := newChatWorkflowFixture(t)
	logs := captureChatLogs(t)
	if err := f.s.syncChatConnection(t.Context(), f.record, f.identity.UserID, "connected", "", ""); err != nil {
		t.Fatal(err)
	}
	issue, messageID, ids := f.groupIssueCard(t)
	lowerChatOperationLimit(t, 64)
	if got := f.post(t, chatRawCallback{body: chatEncryptedTestBody(t, f.cardTriggerEvent(f.identity.OpenID, f.binding.ChatID, messageID, ids[chatQuickAssign], "", "evt_over_limit", nil), f.credentials.EncryptKey), signature: chatSignSHA256}); got.Code != 200 {
		t.Fatalf("click: %d", got.Code)
	}
	f.runAllJobs(t)
	if text := f.lastText(t); !strings.Contains(text, "chat_processing_failed") || f.sent[len(f.sent)-1]["receive_id"] != f.identity.OpenID {
		t.Fatalf("failure notice: %s", text)
	}
	if after, _ := f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, issue.ID); after.Assignee != nil {
		t.Fatalf("over-limit read still assigned: %+v", after.Assignee)
	}
	// Failed, not pending: nothing is claimable later.
	if jobs, err := f.s.store.ClaimChatJobs(t.Context(), f.record.WorkspaceKey, "inbound", 10, time.Now().Add(48*time.Hour)); err != nil || len(jobs) != 0 {
		t.Fatalf("the job is retried: %#v %v", jobs, err)
	}
	data, _ := f.s.store.BootstrapFor(f.record.WorkspaceKey)
	var connection domain.IntegrationConnection
	for _, item := range data.IntegrationConnections {
		if item.ID == f.record.ConnectionID {
			connection = item
		}
	}
	if !strings.HasPrefix(connection.LastError, chatProcessingErrorPrefix) || !strings.Contains(connection.LastError, "/api/issue-records/{id}: response_too_large") || chatErrorCode(connection.LastError) != "chat_processing_failed" {
		t.Fatalf("lastError: %q", connection.LastError)
	}
	records := logs.records(t, "chat operation response exceeds limit")
	if len(records) != 1 || records[0]["endpoint"] != "/api/issue-records/{id}" || records[0]["size_bytes_at_least"].(float64) <= 64 {
		t.Fatalf("limit record: %v", records)
	}
	if failed := logs.records(t, "chat job failed"); len(failed) != 1 || failed[0]["error_kind"] != "response_too_large" || failed[0]["attempts"].(float64) != 1 {
		t.Fatalf("job failure record: %v", failed)
	}
	// A later successful delivery clears it.
	if err := f.s.recordChatDeliveryResultLocked(t.Context(), f.record, nil); err != nil {
		t.Fatal(err)
	}
	data, _ = f.s.store.BootstrapFor(f.record.WorkspaceKey)
	for _, item := range data.IntegrationConnections {
		if item.ID == f.record.ConnectionID && item.LastError != "" {
			t.Fatalf("lastError not cleared: %q", item.LastError)
		}
	}
}

// A direct message that fails inside Flow tells the member, in their
// language, once the failure is final; a transient one retries quietly.
func TestChatInboundDirectMessageFailureNotice(t *testing.T) {
	f := newChatDMFixture(t, nil)
	identity, _, err := f.s.chatDMLinked(t.Context(), f.record, f.identity.OpenID)
	if err != nil {
		t.Fatal(err)
	}
	dm := chatDMRequest{record: f.record, identity: identity, command: chatInboundCommand{Kind: "dm_message", OpenID: f.identity.OpenID, MessageID: "om_dm_failure"}, job: domain.ChatJob{ID: "chat_job_dm_failure", DedupKey: "dm_failure", Attempts: 1}, zh: true}
	dm.target = chatDMTarget{Workspace: f.record.WorkspaceKey, Identity: identity}
	transient := &chatPlatformError{Retryable: true}
	if err := f.s.chatDMFailure(t.Context(), dm, transient); err != transient || len(f.sent()) != 0 {
		t.Fatalf("a transient failure answered: %v %+v", err, f.sent())
	}
	final := &chatOperationError{Path: "/api/issue-records/bootstrap", Reason: "response_invalid"}
	if err := f.s.chatDMFailure(t.Context(), dm, final); err != final {
		t.Fatalf("final failure: %v", err)
	}
	if sent := f.sent(); len(sent) != 1 || sent[0].ReplyTo != "om_dm_failure" || !strings.Contains(sent[0].Raw, "Flow 服务端出现问题") || !strings.Contains(sent[0].Raw, "chat_processing_failed") {
		t.Fatalf("failure notice: %+v", sent)
	}
	// The last attempt of a transient failure answers too.
	dm.job.Attempts, dm.command.MessageID, dm.job.DedupKey = 8, "om_dm_failure_last", "dm_failure_last"
	if err := f.s.chatDMFailure(t.Context(), dm, transient); err != transient || len(f.sent()) != 2 {
		t.Fatalf("last attempt: %v %+v", err, f.sent())
	}
}

// Quick commands answer only when the message is the command; questions that
// contain their words go to the agent.
func TestChatInboundDirectMessageQuickCommandsAreExact(t *testing.T) {
	for _, text := range []string{"我的事项有哪些？", "inbox zero tips", "新建事项怎么填模板？", "new issue?", "how do I create a new issue"} {
		if _, ok := chatDMNewIssueTitle(text); ok {
			t.Fatalf("%q opened the new issue form", text)
		}
		if chatDMCommandIs(text, "my issues", "我的事项") || chatDMCommandIs(text, "inbox", "收件箱") {
			t.Fatalf("%q is a quick command", text)
		}
	}
	for text, title := range map[string]string{"新建事项 修复导出": "修复导出", "new issue: Fix export": "Fix export", "新建事项": ""} {
		if got, ok := chatDMNewIssueTitle(text); !ok || got != title {
			t.Fatalf("%q: %q %v", text, got, ok)
		}
	}
}

// The tenant check reads which inbound message permissions the tenant has
// granted, so the settings page can name the missing ones.
func TestChatInboundScopeCheck(t *testing.T) {
	answer := `{"code":0,"msg":"success","data":{"scopes":[{"scope_name":"im:message.group_at_msg:readonly","grant_status":1},{"scope_name":"im:message.group_msg","grant_status":2},{"scope_name":"im:message.p2p_msg","grant_status":1}]}}`
	mockChatTransport(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/open-apis/application/v6/scopes" || r.Header.Get("Authorization") != "Bearer tenant-token" {
			return chatTestResponse(404, `{"code":404}`), nil
		}
		return chatTestResponse(200, answer), nil
	})
	missing, err := readChatMissingScopes(t.Context(), "lark", "tenant-token")
	if err != nil || len(missing) != 1 || missing[0] != "im:message.group_msg" {
		t.Fatalf("missing scopes: %v %v", missing, err)
	}
	record := domain.ChatIntegrationRecord{TenantKey: "tenant_a"}
	applyChatTenantCheck(&record, chatProbeResult{BotOpenID: "ou_bot", TenantKey: "tenant_a", MissingScopes: missing}, nil, "", time.Now())
	if permission := chatTenantPermission(record, true, ""); permission["status"] != "authorized" || len(permission["missingScopes"].([]string)) != 1 {
		t.Fatalf("tenant permission: %v", permission)
	}
	applyChatTenantCheck(&record, chatProbeResult{BotOpenID: "ou_bot", TenantKey: "tenant_a", ScopesErr: &chatPlatformError{Code: 99991672}}, nil, "", time.Now())
	if permission := chatTenantPermission(record, true, ""); permission["missingScopes"] != nil || permission["scopesUnchecked"] != true {
		t.Fatalf("unchecked scopes: %v", permission)
	}
}
