package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type chatTestTransport func(*http.Request) (*http.Response, error)

func (f chatTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func chatTestResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func chatTestServer(t *testing.T) (*server, http.Handler) {
	t.Helper()
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s", 32))))
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	s := &server{store: repo, authDisabled: true, allowedOrigin: "https://flow.example.com"}
	mux := http.NewServeMux()
	s.registerChatIntegrationRoutes(mux)
	return s, mux
}
func mockChatTransport(t *testing.T, transport chatTestTransport) {
	t.Helper()
	previous := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previous })
}

func TestChatApplicationSetupIdentityOAuthAndInternalGroupBindings(t *testing.T) {
	for _, provider := range []string{"feishu", "lark"} {
		t.Run(provider, func(t *testing.T) {
			s, handler := chatTestServer(t)
			tokenExchanges := 0
			mockChatTransport(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Scheme != "https" || !strings.HasSuffix(r.URL.Host, "feishu.cn") && !strings.HasSuffix(r.URL.Host, "larksuite.com") {
					t.Fatalf("untrusted host: %s", r.URL)
				}
				switch r.URL.Path {
				case "/open-apis/auth/v3/tenant_access_token/internal":
					var input map[string]string
					_ = json.NewDecoder(r.Body).Decode(&input)
					if input["app_secret"] != "app-secret" {
						t.Fatalf("wrong credentials: %#v", input)
					}
					return chatTestResponse(200, `{"code":0,"tenant_access_token":"tenant-token","expire":3600}`), nil
				case "/open-apis/bot/v3/info/":
					return chatTestResponse(200, `{"code":0,"bot":{"open_id":"ou_bot","app_name":"Flow bot"}}`), nil
				case "/open-apis/tenant/v2/tenant/query":
					// The app lacks 「获取企业信息」 (tenant:tenant:readonly).
					return chatTestResponse(400, `{"code":99991672,"msg":"Access denied. One of the following scopes is required: [tenant:tenant:readonly]"}`), nil
				case "/oauth/v3/token", "/open-apis/authen/v2/oauth/token":
					tokenExchanges++
					input := map[string]string{}
					if provider == "feishu" {
						if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
							t.Fatal("wrong Feishu OAuth encoding")
						}
						_ = r.ParseForm()
						for key := range r.Form {
							input[key] = r.Form.Get(key)
						}
					} else {
						if r.URL.Host != "open.larksuite.com" {
							t.Fatal("wrong Lark token host")
						}
						_ = json.NewDecoder(r.Body).Decode(&input)
					}
					if len(input["code_verifier"]) < 43 || input["client_id"] != "cli_test" || input["client_secret"] != "app-secret" || input["grant_type"] != "authorization_code" {
						t.Fatalf("missing OAuth protections: %#v", input)
					}
					return chatTestResponse(200, `{"code":0,"access_token":"user-token-not-persisted","expires_in":3600}`), nil
				case "/open-apis/authen/v1/user_info":
					if r.Header.Get("Authorization") != "Bearer user-token-not-persisted" {
						t.Fatal("user identity not verified with user token")
					}
					return chatTestResponse(200, `{"code":0,"data":{"open_id":"ou_alice","tenant_key":"tenant_a","name":"Alice"}}`), nil
				case "/open-apis/im/v1/chats/oc_internal/members/is_in_chat":
					return chatTestResponse(200, `{"code":0,"data":{"is_in_chat":true}}`), nil
				case "/open-apis/im/v1/chats":
					return chatTestResponse(200, `{"code":0,"data":{"has_more":true,"page_token":"next-page","items":[{"chat_id":"oc_internal","name":"Internal","tenant_key":"tenant_a","external":false},{"chat_id":"oc_external","name":"External","tenant_key":"tenant_a","external":true},{"chat_id":"oc_other","name":"Other tenant","tenant_key":"tenant_b","external":false},{"chat_id":"oc_unknown","name":"Unknown audience","tenant_key":"tenant_a"}]}}`), nil
				case "/open-apis/im/v1/chats/oc_internal":
					return chatTestResponse(200, `{"code":0,"data":{"name":"Internal","tenant_key":"tenant_a","external":false,"chat_mode":"group"}}`), nil
				case "/open-apis/im/v1/chats/oc_external":
					return chatTestResponse(200, `{"code":0,"data":{"tenant_key":"tenant_a","external":true,"chat_mode":"group"}}`), nil
				}
				return chatTestResponse(404, `{"code":404}`), nil
			})
			path := "/api/integrations/" + provider + "/chat"
			scope := "?workspace=test-workspace"
			settings := requestJSON[map[string]any](t, handler, "PUT", path+scope, map[string]string{"appId": "cli_test", "appSecret": "app-secret", "encryptKey": "encrypt-key", "verificationToken": "verify-token"}, 200)
			encoded, _ := json.Marshal(settings)
			if strings.Contains(string(encoded), "app-secret") || strings.Contains(string(encoded), "encrypt-key") || strings.Contains(string(encoded), "verify-token") {
				t.Fatal("secret leaked in settings")
			}
			// Saving obtained a tenant token and read the bot: the tenant is
			// authorized automatically. The app may not read the tenant
			// information, so the page asks for that permission, and the
			// tenant key falls back to the first platform answer naming it.
			// The bot's groups name two tenants, so it waits for a more
			// certain source.
			tenant := settings["permissions"].(map[string]any)["tenant"].(map[string]any)
			if settings["credentialsConfigured"] != true || settings["connection"].(map[string]any)["status"] != "connected" || tenant["status"] != "authorized" || tenant["tenantKey"] != nil || tenant["tenantInfoErrorCode"] != "chat_tenant_info_permission_missing" || settings["identity"] != nil {
				t.Fatalf("tenant not authorized automatically: %#v", settings)
			}
			// A member links their own account; it names the tenant.
			started := requestJSON[map[string]string](t, handler, "POST", path+"/identity/start"+scope, nil, 200)
			authorization, err := url.Parse(started["authorizeUrl"])
			if err != nil {
				t.Fatal(err)
			}
			query := authorization.Query()
			if query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" || strings.Contains(query.Get("scope"), "offline_access") {
				t.Fatalf("unsafe identity authorization: %s", authorization)
			}
			callback := path + "/identity/oauth/callback?state=" + url.QueryEscape(query.Get("state")) + "&code=accepted"
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest("GET", callback, nil))
			if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "/settings/account/connections?chat_auth=success") {
				t.Fatalf("identity callback: %d %s", rec.Code, rec.Header().Get("Location"))
			}
			requestJSON[map[string]any](t, handler, "GET", callback, nil, 400)
			if tokenExchanges != 1 {
				t.Fatal("OAuth state was reused")
			}
			settings = requestJSON[map[string]any](t, handler, "GET", path+scope, nil, 200)
			tenant = settings["permissions"].(map[string]any)["tenant"].(map[string]any)
			if settings["identity"].(map[string]any)["openId"] != "ou_alice" || tenant["tenantKey"] != "tenant_a" || tenant["tenantKeySource"] != "observed" || tenant["tenantInfoErrorCode"] != "chat_tenant_info_permission_missing" {
				t.Fatalf("identity not linked: %#v", settings)
			}
			groups := requestJSON[struct {
				Items     []chatGroup `json:"items"`
				HasMore   bool        `json:"hasMore"`
				PageToken string      `json:"pageToken"`
			}](t, handler, "GET", path+"/chats"+scope, nil, 200)
			if len(groups.Items) != 1 || groups.Items[0].ID != "oc_internal" || groups.PageToken != "next-page" || !groups.HasMore {
				t.Fatalf("unsafe group list or lost cursor: %#v", groups)
			}
			data, _ := s.store.WorkspaceMetadata("test-workspace")
			teamID := ""
			for _, team := range data.Teams {
				if chatPublicTeam(data, team.ID) {
					teamID = team.ID
					break
				}
			}
			if teamID == "" {
				t.Fatal("fixture missing public team")
			}
			binding := domain.ChatBinding{ChatID: "oc_internal", ChatName: "Internal", TeamID: teamID, Events: []string{"issue.created"}, Enabled: true, AllowCreate: true, AllowUpdate: true}
			if refused := requestJSON[map[string]any](t, handler, "POST", path+"/bindings"+scope, binding, 400); refused["code"] != "chat_binding_authorization_required" || refused["permissionDomain"] != "team" {
				t.Fatalf("binding saved without explicit authorization: %#v", refused)
			}
			saved := requestJSON[domain.ChatBinding](t, handler, "POST", path+"/bindings"+scope, chatAuthorizedBinding(binding), 200)
			if saved.CreatedAt.IsZero() || saved.ConnectionID == "" || saved.Authorization == nil || saved.Authorization.AuthorizedAt.IsZero() || saved.Authorization.PrivateTeam {
				t.Fatal("binding lacked lifecycle or authorization fields")
			}
			// Request settings: a reaction emoji type, a priority from 0 to
			// 4 and a template the binding may use.
			binding.ID = saved.ID
			priority := 2
			for _, refused := range []domain.ChatBindingRequests{{Enabled: true, Emoji: "OK!"}, {Enabled: true, Emoji: "OK", Priority: new(7)}, {Enabled: true, TemplateID: "template_missing"}} {
				binding.Requests = &refused
				requestJSON[map[string]any](t, handler, "POST", path+"/bindings"+scope, chatAuthorizedBinding(binding), 400)
			}
			binding.Requests = &domain.ChatBindingRequests{Enabled: true, EveryMention: true, Emoji: "OK", Priority: &priority}
			saved = requestJSON[domain.ChatBinding](t, handler, "POST", path+"/bindings"+scope, chatAuthorizedBinding(binding), 200)
			if !saved.RequestsEnabled() || !saved.Requests.EveryMention || saved.Requests.Emoji != "OK" || saved.Requests.Priority == nil || *saved.Requests.Priority != 2 {
				t.Fatalf("request settings not saved: %+v", saved.Requests)
			}
			audit, _ := s.store.WorkspaceMetadataFields("test-workspace", "auditLog")
			if len(audit.AuditLog) == 0 || audit.AuditLog[0].Action != "chat_binding.updated" || fmt.Sprint(audit.AuditLog[0].Metadata["requests"]) != fmt.Sprint(map[string]any{"enabled": true, "everyMention": true, "emoji": "OK", "templateId": "", "priority": 2}) {
				t.Fatalf("request settings not audited: %+v", audit.AuditLog[:min(len(audit.AuditLog), 1)])
			}
			binding.ID, binding.Requests = "", nil
			binding.ChatID = "oc_external"
			if refused := requestJSON[map[string]any](t, handler, "POST", path+"/bindings"+scope, chatAuthorizedBinding(binding), 400); refused["code"] != "chat_group_external" {
				t.Fatalf("external group accepted: %#v", refused)
			}
			requestJSON[map[string]any](t, handler, "PUT", path+scope, map[string]string{"appId": "cli_test", "name": "Updated name"}, 200)
			record, err := s.store.ChatIntegration(t.Context(), "test-workspace", provider)
			if err != nil {
				t.Fatal(err)
			}
			credential, err := s.chatCredentials(t.Context(), record)
			if err != nil || credential.AppSecret != "app-secret" || credential.EncryptKey != "encrypt-key" {
				t.Fatalf("blank update erased credentials: %#v %v", credential, err)
			}
			requestJSON[map[string]any](t, handler, "DELETE", path+scope, nil, 204)
			if _, err = s.chatCredentials(t.Context(), record); err == nil {
				t.Fatal("disconnect retained credentials")
			}
		})
	}
}

func TestChatProviderRefreshesRejectedTokenAndClassifiesFailures(t *testing.T) {
	s, _ := chatTestServer(t)
	record := domain.ChatIntegrationRecord{WorkspaceKey: "test-workspace", Provider: "feishu", ConnectionID: "refresh_conn", AppID: "cli_refresh", TenantKey: "tenant_refresh", Connected: true}
	if err := s.store.PutChatIntegration(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(chatCredentials{AppSecret: "refresh-secret"})
	if err := s.store.PutConnectorSecret(t.Context(), chatCredentialID(record), raw, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	tokens, calls := 0, 0
	mockChatTransport(t, func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "tenant_access_token") {
			tokens++
			return chatTestResponse(200, fmt.Sprintf(`{"code":0,"tenant_access_token":"token-%d","expire":3600}`, tokens)), nil
		}
		calls++
		if calls == 1 {
			return chatTestResponse(200, `{"code":99991663}`), nil
		}
		if r.Header.Get("Authorization") != "Bearer token-2" {
			t.Fatalf("stale token reused: %s", r.Header.Get("Authorization"))
		}
		return chatTestResponse(200, `{"code":0,"data":{}}`), nil
	})
	if err := s.chatPlatformRequest(t.Context(), record, "GET", "/open-apis/im/v1/chats", nil, nil); err != nil {
		t.Fatal(err)
	}
	if tokens != 2 || calls != 2 {
		t.Fatalf("unexpected refresh attempts: %d %d", tokens, calls)
	}
	if err := s.chatPlatformRequest(t.Context(), record, "GET", "https://untrusted.example.com", nil, nil); err == nil {
		t.Fatal("arbitrary endpoint accepted")
	}
	if err := chatRequest(t.Context(), "https://open.feishu.cn.evil.example.com/open-apis/im/v1/chats", "GET", "", nil, nil); err == nil {
		t.Fatal("spoofed provider domain accepted")
	}
}

func TestChatBindingRejectsPrivateScopeAndFormTemplates(t *testing.T) {
	data := domain.Bootstrap{Teams: []domain.Team{{ID: "public"}, {ID: "private", Private: true}}, TeamSettings: map[string]domain.TeamSettings{}, Projects: []domain.Project{{ID: "mixed", TeamIDs: []string{"public", "private"}}}, IssueTemplates: []domain.IssueTemplate{{ID: "form", TeamID: "public", TemplateType: "form"}}}
	if chatBindingScopePermitted(data, domain.ChatBinding{TeamID: "private"}) || chatBindingScopePermitted(data, domain.ChatBinding{TeamID: "public", ProjectID: "mixed"}) || chatBindingScopePermitted(data, domain.ChatBinding{TeamID: "public", TemplateID: "form"}) {
		t.Fatal("private scope or unsupported form accepted")
	}
	data.TeamSettings["public"] = domain.TeamSettings{TeamID: "public", Access: "restricted"}
	if chatBindingScopePermitted(data, domain.ChatBinding{TeamID: "public"}) {
		t.Fatal("restricted team settings ignored")
	}
}

func TestChatIdentityBindingRequiresOwnerSessionAndCurrentTenant(t *testing.T) {
	s, handler := chatTestServer(t)
	s.authDisabled = false
	session, token, err := s.store.Login(t.Context(), "admin@example.test", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	_, otherToken, err := s.store.Login(t.Context(), "admin@example.test", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.store.SetSessionAuthentication(t.Context(), token, "password", "", false); err != nil {
		t.Fatal(err)
	}
	record := domain.ChatIntegrationRecord{WorkspaceKey: "test-workspace", Provider: "feishu", ConnectionID: "ownership_conn", AppID: "cli_ownership", TenantKey: "tenant_a", Connected: true}
	if err = s.store.PutChatIntegration(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	credential, _ := json.Marshal(chatCredentials{AppSecret: "session-secret"})
	if err = s.store.PutConnectorSecret(t.Context(), chatCredentialID(record), credential, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	state := "chat_oauth_owner_session"
	pending := chatOAuthState{WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, Provider: record.Provider, AppID: record.AppID, UserID: session.User.ID, SessionHash: secretHash(token), Verifier: strings.Repeat("v", 43), Authentication: store.SessionAuthentication{Provider: "password"}, RedirectURI: s.chatCallbackURL(record.Provider)}
	raw, _ := json.Marshal(pending)
	if err = s.store.PutConnectorSecret(t.Context(), state, raw, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = s.store.PutChatAction(t.Context(), domain.ChatAction{ID: state, WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, UserID: session.User.ID, Payload: json.RawMessage(`{"kind":"oauth"}`), ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	callback := "/api/integrations/feishu/chat/identity/oauth/callback?state=" + state + "&code=accepted"
	for _, cookie := range []string{"", otherToken} {
		req := httptest.NewRequest("GET", callback, nil)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != 403 {
			t.Fatalf("wrong browser accepted: %d %s", rec.Code, rec.Body.String())
		}
	}
	browser := httptest.NewRequest("GET", callback, nil)
	browser.Header.Set("Accept", "text/html")
	browser.AddCookie(&http.Cookie{Name: sessionCookieName, Value: otherToken})
	redirect := httptest.NewRecorder()
	handler.ServeHTTP(redirect, browser)
	if location := redirect.Header().Get("Location"); redirect.Code != http.StatusSeeOther || !strings.Contains(location, "chat_auth_error=chat_authorization_other_session") || strings.Contains(location, "browser") {
		t.Fatalf("browser redirect does not carry only the reason code: %d %s", redirect.Code, location)
	}
	if _, err = s.store.ReadConnectorSecret(t.Context(), state, false); err != nil {
		t.Fatal("wrong browser consumed another session's OAuth state")
	}
	mockChatTransport(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/oauth/v3/token" {
			return chatTestResponse(200, `{"access_token":"cross-tenant-token"}`), nil
		}
		return chatTestResponse(200, `{"code":0,"data":{"open_id":"ou_wrong_tenant","tenant_key":"tenant_b"}}`), nil
	})
	req := httptest.NewRequest("GET", callback, nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "another tenant") {
		t.Fatalf("foreign tenant accepted: %d %s", rec.Code, rec.Body.String())
	}
	if _, err = s.store.ChatIdentityForUser(t.Context(), record.WorkspaceKey, record.ConnectionID, session.User.ID); err == nil {
		t.Fatal("foreign tenant identity persisted")
	}
}

func TestChatMembersCanBindWithoutManagingApplicationAndEnforcedSSOApplies(t *testing.T) {
	s, handler := chatTestServer(t)
	s.authDisabled = false
	session, token, err := s.store.LoginExternal(t.Context(), "chat-member@example.test", "Chat member", "", true)
	if err != nil {
		t.Fatal(err)
	}
	metadata, _ := s.store.WorkspaceMetadata("test-workspace")
	if err = s.store.EnsureWorkspaceMembership(t.Context(), metadata.Workspace.ID, session.User.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.store.UpdateMemberRole(t.Context(), metadata.Workspace.ID, session.User.ID, "member"); err != nil {
		t.Fatal(err)
	}
	if err = s.store.SetSessionAuthentication(t.Context(), token, "password", "", false); err != nil {
		t.Fatal(err)
	}
	record := domain.ChatIntegrationRecord{WorkspaceKey: "test-workspace", Provider: "feishu", ConnectionID: "member_conn", AppID: "cli_member", TenantKey: "tenant_a", Connected: true}
	if err = s.store.PutChatIntegration(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(chatCredentials{AppSecret: "member-secret"})
	if err = s.store.PutConnectorSecret(t.Context(), chatCredentialID(record), raw, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, bearer bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req = req.WithContext(context.WithValue(req.Context(), authUserContextKey{}, session.User))
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		if bearer {
			req.Header.Set("Authorization", "Bearer rejected-key")
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	path := "/api/integrations/feishu/chat?workspace=test-workspace"
	response := request("GET", path, false)
	if response.Code != 200 {
		t.Fatalf("member settings: %d %s", response.Code, response.Body.String())
	}
	var settings map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &settings)
	if settings["canManage"] != false || settings["credentialsConfigured"] != true {
		t.Fatalf("member cannot bind configured app: %#v", settings)
	}
	if response = request("POST", "/api/integrations/feishu/chat/identity/start?workspace=test-workspace", false); response.Code != 200 {
		t.Fatalf("member start: %d %s", response.Code, response.Body.String())
	}
	if response = request("DELETE", path, false); response.Code != 403 {
		t.Fatalf("member changed application: %d", response.Code)
	}
	if response = request("GET", path, true); response.Code != 403 {
		t.Fatalf("bearer key was elevated by ambient cookie: %d", response.Code)
	}
	if err = s.store.MutateWorkspace(t.Context(), record.WorkspaceKey, "identity_provider.updated", "required-idp", nil, func(data *domain.Bootstrap) error {
		data.IdentityProviders = append(data.IdentityProviders, domain.IdentityProvider{ID: "required-idp", Enabled: true, Enforced: true, Issuer: "https://sso.example.com"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if response = request("POST", "/api/integrations/feishu/chat/identity/start?workspace=test-workspace", false); response.Code != 403 {
		t.Fatalf("new enforced SSO was ignored: %d %s", response.Code, response.Body.String())
	}
}

func TestChatSettingsRecoverPartialSetupAndDisconnectStaleMetadata(t *testing.T) {
	s, handler := chatTestServer(t)
	record := domain.ChatIntegrationRecord{WorkspaceKey: "test-workspace", Provider: "lark", ConnectionID: "partial_conn", AppID: "cli_partial", TenantKey: "tenant_a", Name: "Partial application", Connected: true}
	if err := s.store.PutChatIntegration(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(chatCredentials{AppSecret: "partial-secret"})
	if err := s.store.PutConnectorSecret(t.Context(), chatCredentialID(record), raw, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	path := "/api/integrations/lark/chat?workspace=test-workspace"
	settings := requestJSON[map[string]any](t, handler, "GET", path, nil, 200)
	if settings["connection"] == nil || settings["connection"].(map[string]any)["id"] != record.ConnectionID {
		t.Fatal("authoritative app record hidden after metadata sync failure")
	}
	workspace := s.store.Bootstrap()
	bindable, _ := settings["bindableTeamIds"].([]any)
	all := []string{}
	for _, team := range workspace.Teams {
		all = append(all, team.ID)
	}
	expected := s.chatBindableTeamIDs(record.WorkspaceKey, all)
	if len(bindable) != len(expected) || len(expected) == 0 {
		t.Fatalf("bindable teams not reported: %v, expected %v", bindable, expected)
	}
	for _, team := range workspace.Teams {
		probe := domain.ChatBinding{TeamID: team.ID}
		if _, private := chatTeamState(workspace, team.ID); private {
			probe.Authorization = &domain.ChatBindingAuthorization{PrivateTeam: true}
		}
		if chatBindingScopePermitted(workspace, probe) != slices.Contains(expected, team.ID) {
			t.Fatalf("bindable team %s disagrees with the binding rule", team.ID)
		}
	}
	if err := s.syncChatConnection(t.Context(), record, "setup_admin", "connected", "success", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.store.DeleteChatIntegration(t.Context(), record.WorkspaceKey, record.ConnectionID); err != nil {
		t.Fatal(err)
	}
	settings = requestJSON[map[string]any](t, handler, "GET", path, nil, 200)
	if settings["connection"] == nil || settings["connection"].(map[string]any)["status"] != "error" {
		t.Fatal("stale metadata did not expose recovery/disconnect")
	}
	if settings["lastErrorCode"] != "chat_setup_incomplete" {
		t.Fatalf("connection error has no stable code: %v", settings["lastErrorCode"])
	}
	failure := requestJSON[map[string]any](t, handler, "POST", "/api/integrations/lark/chat/test?workspace=test-workspace", nil, 404)
	if failure["code"] != "chat_not_configured" {
		t.Fatalf("chat error has no stable code: %v", failure)
	}
	requestJSON[map[string]any](t, handler, "DELETE", path, nil, 204)
	requestJSON[map[string]any](t, handler, "DELETE", path, nil, 204)
	if _, err := s.chatCredentials(t.Context(), record); err == nil {
		t.Fatal("stale credentials survived idempotent disconnect")
	}
	data, _ := s.store.WorkspaceMetadataFields(record.WorkspaceKey, "integrationConnections")
	if len(data.IntegrationConnections) != 0 {
		t.Fatal("stale application metadata survived disconnect")
	}
}
