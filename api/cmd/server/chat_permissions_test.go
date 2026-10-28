package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type chatAuthorizedBindingInput struct {
	domain.ChatBinding
	Authorize bool `json:"authorize"`
}

func chatAuthorizedBinding(binding domain.ChatBinding) chatAuthorizedBindingInput {
	return chatAuthorizedBindingInput{ChatBinding: binding, Authorize: true}
}

type chatPermissionActor struct {
	user  domain.User
	token string
}

// chatPermissionFixture: authentication on, a configured application whose
// tenant has not been checked yet, a private team with an owner and a plain
// member, and workspace members outside the team.
type chatPermissionFixture struct {
	t                                    *testing.T
	s                                    *server
	handler                              http.Handler
	workspaceID                          string
	admin, lead, member, outsider, owner chatPermissionActor
	privateTeam, publicTeam              string
	// platform state
	openIDs    map[string]string // session token -> platform open id
	userTenant map[string]string
	groups     map[string]string // chat id -> JSON of /im/v1/chats/{id}
	botIn      map[string]bool
	// tokenStatus and botStatus answer the token and bot info requests:
	// 0 is success; another HTTP status is a refusal.
	tokenStatus, botStatus int
	botMissing             bool
	userSignIns            int
	// outsideRange: open ids outside the app's availability range.
	outsideRange map[string]bool
}

func newChatPermissionFixture(t *testing.T, provider string) *chatPermissionFixture {
	t.Helper()
	s, handler := chatTestServer(t)
	s.authDisabled = false
	f := &chatPermissionFixture{t: t, s: s, handler: handler, openIDs: map[string]string{}, userTenant: map[string]string{}, groups: map[string]string{}, botIn: map[string]bool{}, outsideRange: map[string]bool{}}
	metadata, _ := s.store.WorkspaceMetadata("test-workspace")
	f.workspaceID = metadata.Workspace.ID
	session, token, err := s.store.Login(t.Context(), "admin@example.test", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	f.admin = f.activate(chatPermissionActor{session.User, token})
	f.lead = f.newMember("chat-lead@example.test", "Private lead")
	f.member = f.newMember("chat-member@example.test", "Private member")
	f.outsider = f.newMember("chat-outsider@example.test", "Outsider")
	f.owner = f.newMember("chat-tenant-owner@example.test", "Tenant owner")
	for _, team := range metadata.Teams {
		if chatPublicTeam(metadata, team.ID) {
			f.publicTeam = team.ID
			break
		}
	}
	f.privateTeam = "team_chat_private"
	now := time.Now().UTC()
	if err := s.store.MutateWorkspace(t.Context(), "test-workspace", "team.created", f.privateTeam, nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: f.privateTeam, Name: "Vault", Key: "VLT", Color: "#000000", Private: true, CreatedAt: &now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetTeamMembership(t.Context(), f.workspaceID, f.privateTeam, f.lead.user.ID, "owner", true); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetTeamMembership(t.Context(), f.workspaceID, f.privateTeam, f.member.user.ID, "member", true); err != nil {
		t.Fatal(err)
	}
	f.openIDs[f.admin.token], f.openIDs[f.owner.token], f.openIDs[f.lead.token], f.openIDs[f.outsider.token] = "ou_admin", "ou_owner", "ou_lead", "ou_outsider"
	f.groups["oc_internal"] = `{"code":0,"data":{"name":"Vault room","tenant_key":"tenant_a","external":false,"chat_mode":"group"}}`
	f.groups["oc_external"] = `{"code":0,"data":{"name":"Partners","tenant_key":"tenant_a","external":true,"chat_mode":"group"}}`
	f.groups["oc_foreign"] = `{"code":0,"data":{"name":"Other company","tenant_key":"tenant_b","external":false,"chat_mode":"group"}}`
	f.groups["oc_no_bot"] = `{"code":0,"data":{"name":"Quiet room","tenant_key":"tenant_a","external":false,"chat_mode":"group"}}`
	f.botIn["oc_internal"], f.botIn["oc_external"], f.botIn["oc_foreign"] = true, true, true
	mockChatTransport(t, f.platform)
	record := domain.ChatIntegrationRecord{WorkspaceKey: "test-workspace", Provider: provider, ConnectionID: "perm_conn_" + provider, AppID: "cli_perm", BotOpenID: "ou_bot"}
	if err := s.store.PutChatIntegration(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(chatCredentials{AppSecret: "perm-secret", EncryptKey: "perm-encrypt", VerificationToken: "perm-verify"})
	if err := s.store.PutConnectorSecret(t.Context(), chatCredentialID(record), raw, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *chatPermissionFixture) activate(actor chatPermissionActor) chatPermissionActor {
	if err := f.s.store.SetSessionAuthentication(f.t.Context(), actor.token, "password", "", false); err != nil {
		f.t.Fatal(err)
	}
	return actor
}

func (f *chatPermissionFixture) newMember(email, name string) chatPermissionActor {
	session, token, err := f.s.store.LoginExternal(f.t.Context(), email, name, "", true)
	if err != nil {
		f.t.Fatal(err)
	}
	if err = f.s.store.EnsureWorkspaceMembership(f.t.Context(), f.workspaceID, session.User.ID); err != nil {
		f.t.Fatal(err)
	}
	if err = f.s.store.UpdateMemberRole(f.t.Context(), f.workspaceID, session.User.ID, "member"); err != nil {
		f.t.Fatal(err)
	}
	return f.activate(chatPermissionActor{session.User, token})
}

// platform fakes the chat platform. The OAuth code names the session whose
// platform account signs in, so each Flow member has their own account.
func (f *chatPermissionFixture) platform(r *http.Request) (*http.Response, error) {
	path := r.URL.Path
	switch {
	case path == "/open-apis/auth/v3/tenant_access_token/internal":
		if f.tokenStatus != 0 {
			// The platform's answer while the app version is not approved,
			// or the credentials are wrong.
			return chatTestResponse(f.tokenStatus, `{"code":10003,"msg":"invalid param"}`), nil
		}
		return chatTestResponse(200, `{"code":0,"tenant_access_token":"tenant-token","expire":3600}`), nil
	case path == "/open-apis/bot/v3/info/":
		if f.botStatus != 0 {
			return chatTestResponse(f.botStatus, `{"code":99991672,"msg":"app not available"}`), nil
		}
		if f.botMissing {
			return chatTestResponse(200, `{"code":0,"bot":{}}`), nil
		}
		return chatTestResponse(200, `{"code":0,"bot":{"open_id":"ou_bot","app_name":"Flow bot"}}`), nil
	case path == "/oauth/v3/token" || path == "/open-apis/authen/v2/oauth/token":
		f.userSignIns++
		code := ""
		if strings.Contains(r.Header.Get("Content-Type"), "form") {
			_ = r.ParseForm()
			code = r.Form.Get("code")
		} else {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			code = body["code"]
		}
		return chatTestResponse(200, `{"code":0,"access_token":"user:`+code+`"}`), nil
	case path == "/open-apis/authen/v1/user_info":
		openID := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer user:")
		if f.outsideRange[openID] {
			return chatTestResponse(403, `{"code":20027,"msg":"the user is not in the app's availability range"}`), nil
		}
		tenant := f.userTenant[openID]
		if tenant == "" {
			tenant = "tenant_a"
		}
		return chatTestResponse(200, `{"code":0,"data":{"open_id":"`+openID+`","tenant_key":"`+tenant+`","name":"`+openID+`"}}`), nil
	case path == "/open-apis/im/v1/chats":
		return chatTestResponse(200, `{"code":0,"data":{"items":[{"chat_id":"oc_internal","name":"Vault room","tenant_key":"tenant_a","external":false}]}}`), nil
	case strings.HasSuffix(path, "/members/is_in_chat"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/open-apis/im/v1/chats/"), "/members/is_in_chat")
		if f.botIn[id] {
			return chatTestResponse(200, `{"code":0,"data":{"is_in_chat":true}}`), nil
		}
		return chatTestResponse(200, `{"code":0,"data":{"is_in_chat":false}}`), nil
	case strings.HasPrefix(path, "/open-apis/im/v1/chats/"):
		if body, ok := f.groups[strings.TrimPrefix(path, "/open-apis/im/v1/chats/")]; ok {
			return chatTestResponse(200, body), nil
		}
	}
	return chatTestResponse(404, `{"code":404}`), nil
}

func (f *chatPermissionFixture) do(actor chatPermissionActor, method, path string, input any) (int, map[string]any) {
	f.t.Helper()
	var body *bytes.Reader
	if input != nil {
		raw, _ := json.Marshal(input)
		body = bytes.NewReader(raw)
	} else {
		body = bytes.NewReader(nil)
	}
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	req := httptest.NewRequest(method, path+separator+"workspace=test-workspace", body)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), authUserContextKey{}, actor.user))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: actor.token})
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	result := map[string]any{}
	if rec.Code != http.StatusNoContent && rec.Code != http.StatusSeeOther {
		_ = json.Unmarshal(rec.Body.Bytes(), &result)
	}
	if rec.Code == http.StatusSeeOther {
		result["location"] = rec.Header().Get("Location")
	}
	return rec.Code, result
}

func (f *chatPermissionFixture) expect(actor chatPermissionActor, method, path string, input any, status int) map[string]any {
	f.t.Helper()
	code, body := f.do(actor, method, path, input)
	if code != status {
		f.t.Fatalf("%s %s as %s: %d, want %d: %v", method, path, actor.user.Email, code, status, body)
	}
	return body
}

func (f *chatPermissionFixture) refusal(actor chatPermissionActor, method, path string, input any, status int, code, domainName string) {
	f.t.Helper()
	body := f.expect(actor, method, path, input, status)
	if body["code"] != code || (domainName != "" && body["permissionDomain"] != domainName) {
		f.t.Fatalf("%s %s as %s: refusal %v, want code %s in domain %q", method, path, actor.user.Email, body, code, domainName)
	}
}

// link runs a personal account link started by actor; the platform account
// that signs in is the actor's.
func (f *chatPermissionFixture) link(actor chatPermissionActor, provider string) string {
	f.t.Helper()
	started := f.expect(actor, "POST", "/api/integrations/"+provider+"/chat/identity/start", nil, 200)
	authorize, _ := url.Parse(started["authorizeUrl"].(string))
	callback := "/api/integrations/" + provider + "/chat/identity/oauth/callback?state=" + url.QueryEscape(authorize.Query().Get("state")) + "&code=" + f.openIDs[actor.token]
	req := httptest.NewRequest("GET", callback, nil)
	req.Header.Set("Accept", "text/html")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: actor.token})
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		f.t.Fatalf("callback %d %s", rec.Code, rec.Body.String())
	}
	return rec.Header().Get("Location")
}

func (f *chatPermissionFixture) record(provider string) domain.ChatIntegrationRecord {
	record, err := f.s.store.ChatIntegration(f.t.Context(), "test-workspace", provider)
	if err != nil {
		f.t.Fatal(err)
	}
	return record
}

func chatTenantPermissions(settings map[string]any) map[string]any {
	return settings["permissions"].(map[string]any)["tenant"].(map[string]any)
}

func (f *chatPermissionFixture) audited(action string, match func(domain.AuditLogEntry) bool) bool {
	entries, _ := f.s.store.WorkspaceMetadataFields("test-workspace", "auditLog")
	return slices.ContainsFunc(entries.AuditLog, func(entry domain.AuditLogEntry) bool {
		return entry.Action == action && (match == nil || match(entry))
	})
}

// recheck runs the periodic tenant check now.
func (f *chatPermissionFixture) recheck(provider string) {
	f.t.Helper()
	if err := f.s.recheckChatTenant(f.t.Context(), "test-workspace", provider, func(domain.ChatIntegrationRecord) bool { return true }); err != nil {
		f.t.Fatal(err)
	}
}

// Acceptance (1), (2) and (6): the tenant is authorized automatically once the
// application's credentials obtain a tenant token and read the bot's info (the
// platform's own approval); nobody signs in for it, workspace administrators
// then list groups, and a workspace role never stands in for it.
func TestChatTenantAuthorizationIsAutomaticAndSeparateFromWorkspaceAdministration(t *testing.T) {
	for _, provider := range []string{"lark", "feishu"} {
		t.Run(provider, func(t *testing.T) {
			f := newChatPermissionFixture(t, provider)
			base := "/api/integrations/" + provider + "/chat"
			// Configured but not checked yet: each domain answers separately.
			settings := f.expect(f.admin, "GET", base, nil, 200)
			if tenant := chatTenantPermissions(settings); tenant["status"] != "pending" || tenant["canAuthorize"] != nil || tenant["designatedAuthorizerId"] != nil {
				t.Fatalf("admin tenant state: %v", tenant)
			}
			f.refusal(f.admin, "GET", base+"/chats", nil, 409, "chat_tenant_not_approved", "tenant")
			f.refusal(f.admin, "POST", base+"/identity/start", nil, 409, "chat_tenant_not_approved", "tenant")
			f.refusal(f.outsider, "GET", base+"/chats", nil, 403, "chat_team_binding_permission_required", "team")
			// The tenant administrator sign-in and the designated authorizer
			// are gone.
			f.expect(f.admin, "POST", base+"/tenant/start", nil, 404)
			f.expect(f.admin, "PUT", base+"/tenant/authorizer", map[string]string{"userId": f.owner.user.ID}, 404)

			// The app version is not approved yet: no tenant token.
			f.tokenStatus = 400
			f.refusal(f.admin, "POST", base+"/test", nil, 502, "chat_tenant_not_approved", "tenant")
			settings = f.expect(f.admin, "GET", base, nil, 200)
			if tenant := chatTenantPermissions(settings); tenant["status"] != "not_approved" || tenant["errorCode"] != "chat_tenant_not_approved" || tenant["checkedAt"] == nil || settings["lastErrorCode"] != "chat_tenant_not_approved" {
				t.Fatalf("not approved state: %v %v", tenant, settings["lastErrorCode"])
			}
			f.refusal(f.admin, "GET", base+"/chats", nil, 409, "chat_tenant_not_approved", "tenant")
			if f.audited("chat_tenant.authorized", nil) {
				t.Fatal("tenant authorized without a token")
			}

			// A token, but the bot's info is not readable (the version with
			// the bot is not live), or the bot is not enabled.
			f.tokenStatus, f.botStatus = 0, 403
			f.refusal(f.admin, "POST", base+"/test", nil, 502, "chat_tenant_not_approved", "tenant")
			f.botStatus, f.botMissing = 0, true
			f.refusal(f.admin, "POST", base+"/test", nil, 502, "chat_bot_unpublished", "tenant")
			if tenant := chatTenantPermissions(f.expect(f.admin, "GET", base, nil, 200)); tenant["status"] != "not_approved" || tenant["errorCode"] != "chat_bot_unpublished" {
				t.Fatalf("bot info failure state: %v", tenant)
			}
			if chatTenantAuthorized(f.record(provider)) {
				t.Fatal("tenant authorized without bot info")
			}

			// Approved and published: Test connection authorizes the tenant.
			f.botMissing = false
			settings = f.expect(f.admin, "POST", base+"/test", nil, 200)
			tenant := chatTenantPermissions(settings)
			authorization, _ := tenant["authorization"].(map[string]any)
			if tenant["status"] != "authorized" || tenant["tenantKey"] != "tenant_a" || tenant["errorCode"] != nil || authorization["configuredBy"] != f.admin.user.ID || authorization["authorizedAt"] == "" || authorization["method"] != nil || settings["connection"].(map[string]any)["status"] != "connected" {
				t.Fatalf("tenant not authorized automatically: %v", settings)
			}
			record := f.record(provider)
			if !record.Connected || record.TenantKey != "tenant_a" || record.TenantAuthorization == nil || record.TenantAuthorization.ConfiguredBy != f.admin.user.ID || record.TenantAuthorization.AuthorizedAt.IsZero() || record.TenantCheck == nil || record.TenantCheck.ErrorCode != "" {
				t.Fatalf("tenant authorization not recorded: %+v", record)
			}
			if !f.audited("chat_tenant.authorized", func(entry domain.AuditLogEntry) bool {
				return entry.Metadata["configuredBy"] == f.admin.user.ID && entry.Metadata["method"] == "automatic"
			}) || !f.audited("chat_tenant.key_recorded", func(entry domain.AuditLogEntry) bool { return entry.Metadata["tenantKey"] == "tenant_a" }) {
				t.Fatal("tenant authorization not audited")
			}

			// (1) The workspace administrator lists groups with no sign-in.
			groups := f.expect(f.admin, "GET", base+"/chats", nil, 200)
			if items, _ := groups["items"].([]any); len(items) != 1 || f.userSignIns != 0 {
				t.Fatalf("admin could not list groups without signing in: %v (%d sign-ins)", groups, f.userSignIns)
			}
			// Linking a personal account needs no tenant step and never
			// changes the tenant authorization.
			if location := f.link(f.outsider, provider); !strings.Contains(location, "chat_auth=success") || !strings.Contains(location, "/settings/account/connections") {
				t.Fatalf("member link: %s", location)
			}
			if identity, err := f.s.store.ChatIdentityForUser(t.Context(), "test-workspace", record.ConnectionID, f.outsider.user.ID); err != nil || identity.TenantKey != "tenant_a" {
				t.Fatalf("member link not saved: %+v %v", identity, err)
			}
			f.userTenant["ou_lead"] = "tenant_b"
			if location := f.link(f.lead, provider); !strings.Contains(location, "chat_auth_error=chat_other_tenant") {
				t.Fatalf("account of another tenant linked: %s", location)
			}
			// Approved in the tenant, but not available to this person.
			delete(f.userTenant, "ou_lead")
			f.outsideRange["ou_lead"] = true
			if location := f.link(f.lead, provider); !strings.Contains(location, "chat_auth_error=chat_app_unavailable_to_user") {
				t.Fatalf("account outside the availability range: %s", location)
			}
			if after := f.record(provider); after.TenantAuthorization.ConfiguredBy != f.admin.user.ID || after.TenantKey != "tenant_a" {
				t.Fatal("a personal link changed the tenant authorization")
			}
			// (6) Members see the tenant state but not the workspace permission.
			memberSettings := f.expect(f.outsider, "GET", base, nil, 200)
			if chatTenantPermissions(memberSettings)["status"] != "authorized" || memberSettings["permissions"].(map[string]any)["workspace"].(map[string]any)["canManage"] != false {
				t.Fatalf("member permission view: %v", memberSettings["permissions"])
			}
			f.refusal(f.outsider, "POST", base+"/test", nil, 403, "chat_workspace_permission_required", "workspace")

			// An unreachable platform never withdraws the authorization.
			f.tokenStatus = 503
			f.refusal(f.admin, "POST", base+"/test", nil, 502, "chat_platform_unreachable", "")
			if !chatTenantAuthorized(f.record(provider)) {
				t.Fatal("a transient failure withdrew the tenant authorization")
			}
			// The app is withdrawn in the admin console: the periodic check
			// says so; once approved again, it is authorized again.
			f.tokenStatus = 400
			f.recheck(provider)
			if record := f.record(provider); record.Connected || record.TenantCheck.ErrorCode != "chat_tenant_not_approved" || !f.audited("chat_tenant.not_approved", nil) {
				t.Fatalf("withdrawn app still authorized: %+v", record)
			}
			f.tokenStatus = 0
			f.recheck(provider)
			if record := f.record(provider); !record.Connected || record.TenantKey != "tenant_a" || record.TenantAuthorization.ConfiguredBy != f.admin.user.ID {
				t.Fatalf("approved app not authorized again: %+v", record)
			}
		})
	}
}

// A refused tenant token during delivery re-checks the tenant at once, and
// the periodic check waits for its interval.
func TestChatTenantRecheckRunsPeriodicallyAndAfterTokenFailure(t *testing.T) {
	provider := "lark"
	f := newChatPermissionFixture(t, provider)
	f.authorizeTenant(provider)
	record := f.record(provider)
	f.tokenStatus = 400
	if err := f.s.recheckChatTenants(t.Context(), "test-workspace"); err != nil {
		t.Fatal(err)
	}
	if !f.record(provider).Connected {
		t.Fatal("periodic check ran before its interval")
	}
	refused := chatTokenError{&chatPlatformError{Code: 10003, Status: 400}}
	f.s.recheckChatTenantAfterFailure(t.Context(), record, &chatPlatformError{Status: 400})
	if !f.record(provider).Connected {
		t.Fatal("a failure other than the token re-checked the tenant")
	}
	// Checked moments ago: a token failure waits a minute.
	f.s.recheckChatTenantAfterFailure(t.Context(), record, refused)
	if !f.record(provider).Connected {
		t.Fatal("token failure re-checked the tenant within a minute of the last check")
	}
	f.checkedAgo(provider, chatTenantFailureRecheckInterval+time.Second)
	f.s.recheckChatTenantAfterFailure(t.Context(), record, refused)
	if f.record(provider).Connected {
		t.Fatal("token failure did not re-check the tenant")
	}
	// Not approved: the periodic check retries sooner than for an
	// authorized tenant.
	f.tokenStatus = 0
	f.checkedAgo(provider, chatTenantPendingRecheckInterval-time.Minute)
	if err := f.s.recheckChatTenants(t.Context(), "test-workspace"); err != nil {
		t.Fatal(err)
	}
	if f.record(provider).Connected {
		t.Fatal("periodic check ran before its interval")
	}
	f.checkedAgo(provider, chatTenantPendingRecheckInterval+time.Minute)
	if err := f.s.recheckChatTenants(t.Context(), "test-workspace"); err != nil {
		t.Fatal(err)
	}
	if !f.record(provider).Connected {
		t.Fatal("periodic check did not authorize the approved app")
	}
}

// Connections stored while tenant authorization was a sign-in step: those
// authorized (by a tenant administrator, or completed before that step
// existed) stay authorized and become automatically authorized on their next
// successful check; those still waiting for the sign-in are authorized by it.
func TestChatTenantAuthorizationMigratesStoredStates(t *testing.T) {
	stored := map[string]string{
		"tenant_manager": `{"workspaceKey":"test-workspace","provider":"lark","connectionId":"perm_conn_lark","appId":"cli_perm","tenantKey":"tenant_a","botOpenId":"ou_bot","connected":true,"tenantAuthorization":{"userId":"user_owner","openId":"ou_owner","name":"Owner","tenantKey":"tenant_a","authorizedAt":"2026-10-01T00:00:00Z","method":"tenant_manager"},"tenantAuthorizerId":"user_owner"}`,
		"legacy":         `{"workspaceKey":"test-workspace","provider":"lark","connectionId":"perm_conn_lark","appId":"cli_perm","tenantKey":"tenant_a","botOpenId":"ou_bot","connected":true}`,
		"pending":        `{"workspaceKey":"test-workspace","provider":"lark","connectionId":"perm_conn_lark","appId":"cli_perm","tenantKey":"","botOpenId":"ou_bot","connected":false,"tenantAuthorizerId":"user_owner"}`,
	}
	for name, payload := range stored {
		t.Run(name, func(t *testing.T) {
			provider := "lark"
			f := newChatPermissionFixture(t, provider)
			var record domain.ChatIntegrationRecord
			if err := json.Unmarshal([]byte(payload), &record); err != nil {
				t.Fatal(err)
			}
			if err := f.s.store.PutChatIntegration(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			if raw, _ := json.Marshal(f.record(provider)); strings.Contains(string(raw), "tenantAuthorizerId") || strings.Contains(string(raw), "tenant_manager") || strings.Contains(string(raw), "openId") {
				t.Fatalf("old tenant sign-in fields kept: %s", raw)
			}
			base := "/api/integrations/" + provider + "/chat"
			tenant := chatTenantPermissions(f.expect(f.admin, "GET", base, nil, 200))
			authorization, _ := tenant["authorization"].(map[string]any)
			switch name {
			case "pending":
				if tenant["status"] != "pending" {
					t.Fatalf("pending state: %v", tenant)
				}
			default:
				if tenant["status"] != "authorized" || tenant["tenantKey"] != "tenant_a" || authorization["authorizedAt"] == nil || authorization["method"] != nil {
					t.Fatalf("authorized state: %v", tenant)
				}
				f.expect(f.admin, "GET", base+"/chats", nil, 200)
			}
			// Never checked: the periodic check is due at once.
			if err := f.s.recheckChatTenants(t.Context(), "test-workspace"); err != nil {
				t.Fatal(err)
			}
			after := f.record(provider)
			if !after.Connected || after.TenantKey != "tenant_a" || after.TenantAuthorization == nil || after.TenantCheck == nil || after.TenantCheck.ErrorCode != "" {
				t.Fatalf("not automatically authorized after a successful check: %+v", after)
			}
			if name == "tenant_manager" && !after.TenantAuthorization.AuthorizedAt.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
				t.Fatalf("original authorization time lost: %v", after.TenantAuthorization.AuthorizedAt)
			}
			if location := f.link(f.outsider, provider); !strings.Contains(location, "chat_auth=success") {
				t.Fatalf("member link after migration: %s", location)
			}
		})
	}
}

func (f *chatPermissionFixture) checkedAgo(provider string, age time.Duration) {
	f.t.Helper()
	record := f.record(provider)
	record.TenantCheck.CheckedAt = time.Now().Add(-age)
	if err := f.s.store.PutChatIntegration(f.t.Context(), record); err != nil {
		f.t.Fatal(err)
	}
}

func (f *chatPermissionFixture) authorizeTenant(provider string) {
	f.t.Helper()
	record := f.record(provider)
	now := time.Now().UTC()
	record.TenantKey, record.Connected = "tenant_a", true
	record.TenantAuthorization = &domain.ChatTenantAuthorization{TenantKey: "tenant_a", AuthorizedAt: now, ConfiguredBy: f.admin.user.ID}
	record.TenantCheck = &domain.ChatTenantCheck{CheckedAt: now}
	if err := f.s.store.PutChatIntegration(f.t.Context(), record); err != nil {
		f.t.Fatal(err)
	}
}

// Acceptance (3), (4), (5) and (6): a private team's owner binds the team to
// an internal group; nobody else in the team may change it; the binding adds
// no access to the team.
func TestChatPrivateTeamBindingNeedsTeamOwnerAuthorization(t *testing.T) {
	provider := "lark"
	f := newChatPermissionFixture(t, provider)
	f.authorizeTenant(provider)
	base := "/api/integrations/" + provider + "/chat"
	membersBefore, err := f.s.store.ListTeamMembers(t.Context(), f.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	binding := domain.ChatBinding{ChatID: "oc_internal", ChatName: "Vault room", TeamID: f.privateTeam, Events: []string{"issue.created", "comment.created"}, Enabled: true, AllowCreate: true, AllowUpdate: false}

	// (6) The lead sees team binding permission for the private team only.
	settings := f.expect(f.lead, "GET", base, nil, 200)
	teams := settings["permissions"].(map[string]any)["teams"].(map[string]any)
	if manageable, _ := teams["manageableTeamIds"].([]any); len(manageable) != 1 || manageable[0] != f.privateTeam {
		t.Fatalf("lead manageable teams: %v", teams)
	}
	if private, _ := teams["privateTeamIds"].([]any); len(private) != 1 || private[0] != f.privateTeam {
		t.Fatalf("private team not flagged: %v", teams)
	}
	if bindable, _ := settings["bindableTeamIds"].([]any); len(bindable) != 1 || bindable[0] != f.privateTeam {
		t.Fatalf("private team not bindable for its lead: %v", settings["bindableTeamIds"])
	}

	// Refusals: external, cross-tenant, bot not in group, unauthorized.
	for chatID, code := range map[string]string{"oc_external": "chat_group_external", "oc_foreign": "chat_group_other_tenant", "oc_no_bot": "chat_bot_not_in_group"} {
		refused := binding
		refused.ChatID = chatID
		f.refusal(f.lead, "POST", base+"/bindings", chatAuthorizedBinding(refused), 400, code, "")
	}
	f.refusal(f.lead, "POST", base+"/bindings", binding, 400, "chat_binding_authorization_required", "team")
	// (4) A plain member of the private team and an outsider cannot bind it.
	f.refusal(f.member, "POST", base+"/bindings", chatAuthorizedBinding(binding), 403, "chat_team_binding_permission_required", "team")
	f.refusal(f.outsider, "POST", base+"/bindings", chatAuthorizedBinding(binding), 403, "chat_team_binding_permission_required", "team")
	// The lead cannot bind a team they do not administer.
	public := binding
	public.TeamID = f.publicTeam
	f.refusal(f.lead, "POST", base+"/bindings", chatAuthorizedBinding(public), 403, "chat_team_binding_permission_required", "team")

	// (3) The lead's explicit authorization succeeds.
	saved := f.expect(f.lead, "POST", base+"/bindings", chatAuthorizedBinding(binding), 200)
	var created domain.ChatBinding
	raw, _ := json.Marshal(saved)
	_ = json.Unmarshal(raw, &created)
	if created.Authorization == nil || created.Authorization.UserID != f.lead.user.ID || created.Authorization.Role != domain.ChatBindingRoleTeamOwner || !created.Authorization.PrivateTeam || created.Authorization.AuthorizedAt.IsZero() || created.RevokedAt != nil || created.AllowUpdate || !created.AllowCreate {
		t.Fatalf("authorization not recorded: %+v", created)
	}

	// (4) Others cannot modify, revoke or delete it, nor see it.
	edit := created
	edit.Events = []string{"issue.created", "comment.created", "issue.status_changed"}
	f.refusal(f.member, "POST", base+"/bindings", chatAuthorizedBinding(edit), 403, "chat_team_binding_permission_required", "team")
	f.refusal(f.member, "POST", base+"/bindings/"+created.ID+"/revoke", nil, 403, "chat_team_binding_permission_required", "team")
	f.refusal(f.member, "DELETE", base+"/bindings/"+created.ID, nil, 403, "chat_team_binding_permission_required", "team")
	f.refusal(f.outsider, "DELETE", base+"/bindings/"+created.ID, nil, 403, "chat_team_binding_permission_required", "team")
	for _, viewer := range []chatPermissionActor{f.member, f.outsider} {
		if bindings, _ := f.expect(viewer, "GET", base, nil, 200)["bindings"].([]any); len(bindings) != 0 {
			t.Fatalf("%s sees the private team's binding: %v", viewer.user.Email, bindings)
		}
	}
	// Workspace administrators manage all bindings.
	if bindings, _ := f.expect(f.admin, "GET", base, nil, 200)["bindings"].([]any); len(bindings) != 1 {
		t.Fatalf("admin does not see all bindings: %v", bindings)
	}

	// (5) No membership, role or access was added for anyone.
	membersAfter, err := f.s.store.ListTeamMembers(t.Context(), f.workspaceID)
	if err != nil || len(membersAfter) != len(membersBefore) {
		t.Fatalf("binding changed team memberships: %d -> %d %v", len(membersBefore), len(membersAfter), err)
	}
	for _, actor := range []chatPermissionActor{f.outsider, f.owner} {
		if role, _ := f.s.store.TeamRole(t.Context(), f.workspaceID, f.privateTeam, actor.user.ID); role != "" {
			t.Fatalf("binding granted %s team role %q", actor.user.Email, role)
		}
		visible, err := f.s.store.PagedWorkspaceMetadata(t.Context(), "test-workspace", actor.user.ID)
		if err != nil {
			t.Fatal(err)
		}
		if slices.ContainsFunc(visible.Teams, func(team domain.Team) bool { return team.ID == f.privateTeam }) {
			t.Fatalf("binding made the private team visible to %s", actor.user.Email)
		}
	}

	// The lead edits the scope (re-authorized), then revokes the binding.
	updated := f.expect(f.lead, "POST", base+"/bindings", chatAuthorizedBinding(edit), 200)
	if events, _ := updated["events"].([]any); len(events) != 3 {
		t.Fatalf("lead could not update the binding: %v", updated)
	}
	revoked := f.expect(f.lead, "POST", base+"/bindings/"+created.ID+"/revoke", nil, 200)
	if revoked["revokedAt"] == nil || revoked["revokedBy"] != f.lead.user.ID || revoked["enabled"] != false {
		t.Fatalf("revocation not recorded: %v", revoked)
	}
	record := f.record(provider)
	if _, err := f.s.chatBindingByID(t.Context(), record, created.ID); err == nil {
		t.Fatal("revoked binding still delivers")
	}
	f.refusal(f.lead, "POST", base+"/bindings", chatAuthorizedBinding(edit), 409, "chat_binding_revoked", "")
	// A new binding of the same group replaces the revoked record.
	renewed := f.expect(f.lead, "POST", base+"/bindings", chatAuthorizedBinding(binding), 200)
	if renewed["id"] == created.ID {
		t.Fatal("revoked binding was reused")
	}

	entries, _ := f.s.store.WorkspaceMetadataFields("test-workspace", "auditLog")
	for _, action := range []string{"chat_binding.authorized", "chat_binding.updated", "chat_binding.revoked"} {
		if !slices.ContainsFunc(entries.AuditLog, func(entry domain.AuditLogEntry) bool {
			return entry.Action == action && entry.ResourceID == created.ID && entry.Metadata["authorizedBy"] == f.lead.user.ID && entry.Metadata["teamId"] == f.privateTeam && entry.Metadata["chatId"] == "oc_internal" && entry.Metadata["privateTeam"] == true
		}) {
			t.Fatalf("audit entry %s missing", action)
		}
	}

	// The binding follows its authorizer: once the lead no longer
	// administers the team, the private binding stops.
	bindings, _ := f.s.store.ChatBindings(t.Context(), "test-workspace", record.ConnectionID)
	active := bindings[slices.IndexFunc(bindings, func(item domain.ChatBinding) bool { return item.ID == renewed["id"] })]
	if _, err := f.s.chatBindingByID(t.Context(), record, active.ID); err != nil {
		t.Fatalf("authorized private binding inactive: %v", err)
	}
	if err := f.s.store.SetTeamMembership(t.Context(), f.workspaceID, f.privateTeam, f.owner.user.ID, "owner", true); err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.SetTeamMembership(t.Context(), f.workspaceID, f.privateTeam, f.lead.user.ID, "member", true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.chatBindingByID(t.Context(), record, active.ID); err == nil {
		t.Fatal("private binding kept delivering after its authorizer lost the team owner role")
	}
	// Workspace administrators may still revoke and delete it.
	f.expect(f.admin, "POST", base+"/bindings/"+active.ID+"/revoke", nil, 200)
	f.expect(f.admin, "DELETE", base+"/bindings/"+active.ID, nil, 204)
}

// Public teams keep working: workspace administrators bind them as before
// (now with a recorded authorization), and a public team's owner may too.
func TestChatPublicTeamBindingRecordsAuthorization(t *testing.T) {
	provider := "feishu"
	f := newChatPermissionFixture(t, provider)
	f.authorizeTenant(provider)
	base := "/api/integrations/" + provider + "/chat"
	binding := domain.ChatBinding{ChatID: "oc_internal", ChatName: "Vault room", TeamID: f.publicTeam, Events: []string{"issue.created"}, Enabled: true}
	saved := f.expect(f.admin, "POST", base+"/bindings", chatAuthorizedBinding(binding), 200)
	authorization := saved["authorization"].(map[string]any)
	if authorization["role"] != domain.ChatBindingRoleWorkspaceAdmin || authorization["privateTeam"] != false {
		t.Fatalf("public binding authorization: %v", authorization)
	}
	f.refusal(f.member, "POST", base+"/bindings/"+saved["id"].(string)+"/revoke", nil, 403, "chat_team_binding_permission_required", "team")
	if err := f.s.store.SetTeamMembership(t.Context(), f.workspaceID, f.publicTeam, f.member.user.ID, "owner", true); err != nil {
		t.Fatal(err)
	}
	f.expect(f.member, "POST", base+"/bindings/"+saved["id"].(string)+"/revoke", nil, 200)
}

// Notifications of a private team binding reach only the bound group, and
// only the team's own issues; inbound actions keep the binding's flags and the
// acting member's own permissions.
func TestChatPrivateTeamBindingDeliversOnlyToTheGroupAndKeepsActorPermissions(t *testing.T) {
	f := newChatWorkflowFixture(t)
	data := f.s.store.Bootstrap()
	privateTeam := "team_workflow_private"
	now := time.Now().UTC()
	if err := f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "team.created", privateTeam, nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: privateTeam, Name: "Vault", Key: "VLW", Color: "#000000", Private: true, CreatedAt: &now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.SetTeamMembership(t.Context(), data.Workspace.ID, privateTeam, data.Viewer.ID, "owner", true); err != nil {
		t.Fatal(err)
	}
	issue := data.Issues[0]
	issue.Team = domain.Team{ID: privateTeam, Private: true}
	unauthorized := f.binding
	unauthorized.TeamID = privateTeam
	if chatIssueMatchesBinding(issue, unauthorized) {
		t.Fatal("private issue matched a binding without private authorization")
	}
	authorized := unauthorized
	authorized.Authorization = &domain.ChatBindingAuthorization{UserID: data.Viewer.ID, Role: domain.ChatBindingRoleTeamOwner, PrivateTeam: true, AuthorizedAt: now}
	if !chatIssueMatchesBinding(issue, authorized) {
		t.Fatal("authorized private binding rejected its team's issue")
	}
	other := issue
	other.Team = domain.Team{ID: "another_private", Private: true}
	if chatIssueMatchesBinding(other, authorized) {
		t.Fatal("private binding matched another private team's issue")
	}
	revoked := authorized
	revoked.RevokedAt = &now
	if chatIssueMatchesBinding(issue, revoked) {
		t.Fatal("revoked binding matched")
	}
	current, _ := f.s.store.WorkspaceMetadataFields(f.record.WorkspaceKey, "teams", "teamSettings", "projects", "issueTemplates", "workspaceSettings")
	current.Projects = append(current.Projects, domain.Project{ID: "mixed", TeamIDs: []string{privateTeam, "another_private"}})
	current.Teams = append(current.Teams, domain.Team{ID: "another_private", Private: true})
	mixed := authorized
	mixed.ProjectID = "mixed"
	if !chatBindingScopePermitted(current, authorized) || chatBindingScopePermitted(current, mixed) || chatBindingScopePermitted(current, unauthorized) {
		t.Fatal("private scope rule wrong")
	}

	// An authorized private binding that disallows creating issues refuses
	// group create commands privately.
	authorized.ID, authorized.ChatID, authorized.AllowCreate = "binding_private_workflow", "oc_private_group", false
	if err := f.s.store.PutChatBinding(t.Context(), f.record.WorkspaceKey, authorized); err != nil {
		t.Fatal(err)
	}
	job := domain.ChatJob{ID: "private_form", WorkspaceKey: f.record.WorkspaceKey, ConnectionID: f.record.ConnectionID, Kind: "inbound", DedupKey: "private_form"}
	job.Payload, _ = json.Marshal(chatInboundCommand{Kind: "create_form", OpenID: f.identity.OpenID, ChatID: authorized.ChatID, MessageID: "om_private_source", Title: "Secret"})
	if err := f.s.processChatJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 1 || f.sent[0]["msg_type"] != "text" || !strings.Contains(f.sent[0]["content"].(string), "not enabled for creating") {
		t.Fatalf("create flag not enforced: %#v", f.sent)
	}
}

// Initiative updates reach the groups of the initiative's lead team, and only
// when no private contributing team would be disclosed.
func TestChatInitiativeUpdatesReachTheLeadTeamGroup(t *testing.T) {
	f := newChatWorkflowFixture(t)
	binding := f.binding
	binding.Events = append(binding.Events, "initiative.update_created")
	if err := f.s.store.PutChatBinding(t.Context(), f.record.WorkspaceKey, binding); err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "initiative.created", "initiative_chat", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: "team_initiative_private", Private: true})
		data.Initiatives = append(data.Initiatives,
			domain.Initiative{ID: "initiative_chat", Name: "Launch", SlugID: "launch-1", LeadTeamID: binding.TeamID, ContributingTeamIDs: []string{binding.TeamID}},
			domain.Initiative{ID: "initiative_mixed", Name: "Secret", SlugID: "secret-1", LeadTeamID: binding.TeamID, ContributingTeamIDs: []string{"team_initiative_private"}},
			domain.Initiative{ID: "initiative_workspace", Name: "Company", SlugID: "company-1"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"initiative_chat", "initiative_mixed", "initiative_workspace"} {
		event := domain.DomainEvent{ID: "evt_" + id, Type: "initiative.update_created", AggregateID: id, CreatedAt: time.Now()}
		if err := f.s.queueChatDomainEvent(t.Context(), f.record.WorkspaceKey, event); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := f.s.store.ClaimChatJobs(t.Context(), f.record.WorkspaceKey, "notification", 10, time.Now())
	if err != nil || len(jobs) != 1 {
		t.Fatalf("initiative updates queued: %d %v", len(jobs), err)
	}
	if err := f.s.processChatJob(t.Context(), jobs[0]); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 1 || f.sent[0]["receive_id"] != binding.ChatID || !strings.Contains(f.sent[0]["content"].(string), "/initiative/launch-1/overview") {
		t.Fatalf("initiative update not delivered to the group: %#v", f.sent)
	}
}
