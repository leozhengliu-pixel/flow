package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// This simulator enforces the documented v1 authorize + v2 JSON exchange
// contract, including the challenge captured from the actual start response.
type chatIdentityProtocol struct {
	f                       *chatPermissionFixture
	provider                string
	authorize               *url.URL
	exchanges, reads        int
	tokenStatus, userStatus int
	tokenBody, userBody     string
	verifier                string
}

func newChatIdentityProtocol(t *testing.T, provider string) *chatIdentityProtocol {
	f := newChatPermissionFixture(t, provider)
	f.authorizeTenant(provider)
	p := &chatIdentityProtocol{f: f, provider: provider, tokenStatus: 200, userStatus: 200,
		tokenBody: `{"code":0,"access_token":"protocol-user-token","token_type":"Bearer","expires_in":7200,"scope":""}`,
		userBody:  `{"code":0,"data":{"open_id":"ou_protocol","tenant_key":"tenant_a","name":"Member"}}`}
	mockChatTransport(t, p.roundTrip)
	p.start(f.member)
	return p
}

func (p *chatIdentityProtocol) start(actor chatPermissionActor) {
	p.f.t.Helper()
	started := p.f.expect(actor, "POST", "/api/integrations/"+p.provider+"/chat/identity/start", nil, 200)
	p.authorize, _ = url.Parse(started["authorizeUrl"].(string))
	q := p.authorize.Query()
	host := "accounts.feishu.cn"
	if p.provider == "lark" {
		host = "accounts.larksuite.com"
	}
	if p.authorize.Host != host || p.authorize.Path != "/open-apis/authen/v1/authorize" || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43 || q.Get("state") == "" || q.Get("client_id") != "cli_perm" || q.Get("scope") != "" {
		p.f.t.Fatal("invalid authorization request")
	}
}

func (p *chatIdentityProtocol) roundTrip(r *http.Request) (*http.Response, error) {
	t := p.f.t
	switch r.URL.Path {
	case "/open-apis/authen/v2/oauth/token":
		p.exchanges++
		if r.Method != "POST" || r.URL.Host != strings.TrimPrefix(chatBaseURL(p.provider), "https://") || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "" {
			t.Fatal("invalid token exchange endpoint, method or headers")
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal("token exchange was not JSON")
		}
		challenge := sha256.Sum256([]byte(body["code_verifier"]))
		p.verifier = body["code_verifier"]
		if body["grant_type"] != "authorization_code" || body["client_id"] != "cli_perm" || body["client_secret"] != "perm-secret" || body["code"] != "protocol-code" || body["redirect_uri"] != p.authorize.Query().Get("redirect_uri") || len(body["code_verifier"]) < 43 || base64.RawURLEncoding.EncodeToString(challenge[:]) != p.authorize.Query().Get("code_challenge") {
			t.Fatal("token exchange did not preserve application, redirect or PKCE")
		}
		return chatTestResponse(p.tokenStatus, p.tokenBody), nil
	case "/open-apis/authen/v1/user_info":
		p.reads++
		if r.Method != "GET" || r.URL.Host != strings.TrimPrefix(chatBaseURL(p.provider), "https://") || r.Header.Get("Authorization") != "Bearer protocol-user-token" || r.URL.RawQuery != "" {
			t.Fatal("user_info must use the exchanged user token as a bearer header")
		}
		return chatTestResponse(p.userStatus, p.userBody), nil
	case "/oauth/v3/token":
		t.Fatal("v1 authorization PKCE flow used the v3 token endpoint")
	}
	return p.f.platform(r)
}

func (p *chatIdentityProtocol) callback(actor chatPermissionActor, query, accept string) *httptest.ResponseRecorder {
	p.f.t.Helper()
	req := httptest.NewRequest("GET", "/api/integrations/"+p.provider+"/chat/identity/oauth/callback?state="+url.QueryEscape(p.authorize.Query().Get("state"))+"&"+query, nil)
	req.Header.Set("Accept", accept)
	if actor.token != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: actor.token})
	}
	rec := httptest.NewRecorder()
	p.f.handler.ServeHTTP(rec, req)
	return rec
}

func (p *chatIdentityProtocol) noLink() {
	p.f.t.Helper()
	if _, err := p.f.s.store.ChatIdentityForUser(p.f.t.Context(), "test-workspace", p.f.record(p.provider).ConnectionID, p.f.member.user.ID); err == nil {
		p.f.t.Fatal("refused callback created an identity")
	}
}

func TestChatIdentityProtocolLinksExistingMemberAndPersists(t *testing.T) {
	for _, provider := range []string{"feishu", "lark"} {
		t.Run(provider, func(t *testing.T) {
			p := newChatIdentityProtocol(t, provider)
			before, _ := p.f.s.store.ListMembers(t.Context(), p.f.workspaceID)
			rec := p.callback(p.f.member, "code=protocol-code", "text/html")
			if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "chat_auth=success") || p.exchanges != 1 || p.reads != 1 {
				t.Fatalf("link did not complete: status %d, exchanges %d, reads %d", rec.Code, p.exchanges, p.reads)
			}
			for _, actor := range []chatPermissionActor{p.f.member, p.f.signIn(p.f.member.user.Email)} {
				settings := p.f.expect(actor, "GET", "/api/integrations/"+provider+"/chat", nil, 200)
				identity, ok := settings["identity"].(map[string]any)
				if !ok || identity["userId"] != p.f.member.user.ID || identity["openId"] != "ou_protocol" {
					t.Fatal("refresh did not preserve the identity on the existing Flow user")
				}
			}
			after, _ := p.f.s.store.ListMembers(t.Context(), p.f.workspaceID)
			if len(after) != len(before) {
				t.Fatal("link created a workspace member")
			}
			if replay := p.callback(p.f.member, "code=protocol-code", "application/json"); replay.Code != 400 || p.exchanges != 1 {
				t.Fatal("callback replay exchanged the code again")
			}
			p.f.expect(p.f.member, "DELETE", "/api/integrations/"+provider+"/chat/identity", nil, 204)
			p.noLink()
		})
	}
}

func TestChatIdentityProtocolRejectsUnsafeCallbacks(t *testing.T) {
	for _, scenario := range []string{"missing session", "other user", "same user other session", "denied", "cross tenant", "app changed", "workspace access removed", "tenant withdrawn", "wrong provider", "tampered state"} {
		t.Run(scenario, func(t *testing.T) {
			p := newChatIdentityProtocol(t, "feishu")
			actor, query := p.f.member, "code=protocol-code"
			switch scenario {
			case "missing session":
				actor = chatPermissionActor{}
			case "other user":
				actor = p.f.lead
			case "same user other session":
				actor = p.f.signIn(p.f.member.user.Email)
			case "denied":
				query = "error=access_denied"
			case "cross tenant":
				p.userBody = `{"code":0,"data":{"open_id":"ou_protocol","tenant_key":"tenant_b"}}`
			case "workspace access removed":
				if err := p.f.s.store.RemoveMember(t.Context(), p.f.workspaceID, p.f.member.user.ID); err != nil {
					t.Fatal(err)
				}
			case "app changed", "tenant withdrawn":
				record := p.f.record("feishu")
				if scenario == "app changed" {
					record.AppID = "other-app"
				} else {
					record.Connected = false
					record.TenantAuthorization = nil
				}
				if err := p.f.s.store.PutChatIntegration(t.Context(), record); err != nil {
					t.Fatal(err)
				}
			case "wrong provider":
				p.provider = "lark"
			case "tampered state":
				q := p.authorize.Query()
				q.Set("state", q.Get("state")+"tampered")
				p.authorize.RawQuery = q.Encode()
			}
			rec := p.callback(actor, query, "application/json")
			if rec.Code < 400 {
				t.Fatal("unsafe callback accepted")
			}
			// Read the original connection, even if its workspace changed.
			if _, err := p.f.s.store.ChatIdentityForUser(t.Context(), "test-workspace", "perm_conn_feishu", p.f.member.user.ID); err == nil {
				t.Fatal("unsafe callback created a link")
			}
			if scenario != "cross tenant" && scenario != "tenant withdrawn" && p.exchanges != 0 {
				t.Fatal("unsafe callback reached token exchange")
			}
			if scenario == "denied" || scenario == "cross tenant" {
				before := p.exchanges
				if replay := p.callback(p.f.member, "code=protocol-code", "application/json"); replay.Code != 400 || p.exchanges != before {
					t.Fatal("refused callback state was reusable")
				}
			}
		})
	}
}

func TestChatIdentityDiagnosticsDistinguishStagesWithoutSecrets(t *testing.T) {
	for _, tc := range []struct {
		name, stage, body, code string
		status                  int
	}{
		{"code rejected", "code_exchange", `{"code":20049,"error":"invalid_grant","error_description":"SECRET_SENTINEL"}`, "chat_code_exchange_failed", 400},
		{"identity rejected", "identity_read", `{"code":20005,"msg":"SECRET_SENTINEL"}`, "chat_identity_read_failed", 200},
		{"permission missing", "identity_read", `{"code":99991679,"error":{"permission_violations":[{"subject":"SECRET_SENTINEL"}]}}`, "chat_oauth_permission_required", 403},
		{"token scope refused", "code_exchange", `{"code":20068,"error":"invalid_scope","error_description":"SECRET_SENTINEL"}`, "chat_oauth_permission_required", 400},
		{"missing token", "code_exchange", `{"code":0,"unexpected":"SECRET_SENTINEL"}`, "chat_code_exchange_failed", 200},
		{"missing identity", "identity_read", `{"code":0,"data":{"name":"SECRET_SENTINEL"}}`, "chat_identity_read_failed", 200},
		{"invalid JSON", "identity_read", `SECRET_SENTINEL`, "chat_identity_read_failed", 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newChatIdentityProtocol(t, "feishu")
			if tc.stage == "code_exchange" {
				p.tokenStatus, p.tokenBody = tc.status, tc.body
			} else {
				p.userStatus, p.userBody = tc.status, tc.body
			}
			var logs bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&logs)
			t.Cleanup(func() { log.SetOutput(previous) })
			rec := p.callback(p.f.member, "code=protocol-code&error_description=SECRET_SENTINEL", "application/json")
			var body map[string]string
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if rec.Code != 502 || body["code"] != tc.code || !strings.Contains(logs.String(), "stage="+tc.stage) || !strings.Contains(logs.String(), fmt.Sprintf("http_status=%d", tc.status)) {
				t.Fatalf("incorrect stage diagnostics: status %d code %s log %s", rec.Code, body["code"], logs.String())
			}
			var platform struct {
				Code int `json:"code"`
			}
			_ = json.Unmarshal([]byte(tc.body), &platform)
			if !strings.Contains(logs.String(), fmt.Sprintf("platform_code=%d", platform.Code)) {
				t.Fatal("diagnostics lost the platform error code")
			}
			combined := logs.String() + rec.Body.String() + rec.Header().Get("Location")
			for _, secret := range []string{"SECRET_SENTINEL", "perm-secret", "perm-encrypt", "perm-verify", "protocol-user-token", "protocol-code", p.authorize.Query().Get("state"), p.authorize.Query().Get("code_challenge"), p.verifier, p.f.member.token} {
				if strings.Contains(combined, secret) {
					t.Fatal("OAuth diagnostics or error response exposed secret material")
				}
			}
			p.noLink()
			// HTML callbacks carry only the stable code, too.
			p.start(p.f.member)
			rec = p.callback(p.f.member, "code=protocol-code", "text/html")
			if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "chat_auth_error="+tc.code) {
				t.Fatal("HTML callback lost the stage code")
			}
		})
	}
}

func TestChatAuthorizationGateExplicitRoutesAndWorkspaceCredentials(t *testing.T) {
	for _, provider := range []string{"feishu", "lark"} {
		t.Run(provider, func(t *testing.T) {
			f := newChatPermissionFixture(t, provider)
			f.authorizeTenant(provider)
			base := "/api/integrations/" + provider + "/chat"
			for _, actor := range []chatPermissionActor{f.member, f.lead} {
				for _, route := range []struct{ method, path string }{{"PUT", ""}, {"DELETE", ""}, {"POST", "/test"}, {"POST", "/identity/new-operation"}, {"POST", "/bindings/id/new-operation"}, {"PATCH", "/bindings/id"}, {"GET", "/future-route"}, {"GET", "/future/oauth/callback"}, {"POST", "/future/webhook"}} {
					f.refusal(actor, route.method, base+route.path, map[string]string{"appSecret": "replacement"}, 403, "chat_workspace_permission_required", "workspace")
				}
			}
			f.expect(f.admin, "PUT", base, map[string]string{"appId": "cli_perm", "appSecret": "perm-secret"}, 200)
			f.expect(f.admin, "DELETE", base, nil, 204)
		})
	}
}
