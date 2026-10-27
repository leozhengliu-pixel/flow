package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
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

type mcpTestTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// registerAndAuthorizeMCPClient runs the real MCP OAuth flow as the signed-in
// client: dynamic client registration, consent and the PKCE code exchange.
func registerAndAuthorizeMCPClient(t *testing.T, host *httptest.Server, session *http.Client, workspace, name, redirectURI, scope string, registerStatus int) (string, mcpTestTokens) {
	t.Helper()
	registered := authRequest[domain.OAuthClient](t, session, http.MethodPost, host.URL+"/oauth/register", map[string]any{"client_name": name, "redirect_uris": []string{redirectURI}, "token_endpoint_auth_method": "none"}, "", registerStatus)
	if !strings.HasPrefix(registered.ClientID, dynamicOAuthClientPrefix) {
		t.Fatalf("registered client = %+v", registered)
	}
	verifier := strings.Repeat("m", 48)
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	decision := authRequest[map[string]string](t, session, http.MethodPost, host.URL+"/api/oauth/authorization-request", map[string]any{
		"clientId": registered.ClientID, "redirectUri": redirectURI, "responseType": "code", "scope": scope, "state": "mcp", "codeChallenge": challenge, "codeChallengeMethod": "S256", "resource": host.URL + "/mcp", "workspaceKey": workspace, "approve": true,
	}, "", http.StatusOK)
	redirect, err := url.Parse(decision["redirect"])
	if err != nil || redirect.Query().Get("code") == "" {
		t.Fatalf("authorization = %v, err=%v", decision, err)
	}
	tokens := postOAuthForm[mcpTestTokens](t, host.URL+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {redirect.Query().Get("code")}, "client_id": {registered.ClientID}, "redirect_uri": {redirectURI}, "code_verifier": {verifier}}, http.StatusOK)
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatal("token exchange returned no tokens")
	}
	return registered.ClientID, tokens
}

// mcpToolsListStatus calls tools/list on the MCP endpoint and returns the
// HTTP status (200 while the token is valid).
func mcpToolsListStatus(t *testing.T, host *httptest.Server, token string) int {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": map[string]any{}})
	request, _ := http.NewRequest(http.MethodPost, host.URL+"/mcp", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	return response.StatusCode
}

func mcpEntry(t *testing.T, listed []authorizedApplication) (authorizedApplication, bool) {
	t.Helper()
	var found []authorizedApplication
	for _, item := range listed {
		if item.Kind == "mcp" {
			found = append(found, item)
		}
		if strings.HasPrefix(item.ClientID, dynamicOAuthClientPrefix) {
			t.Fatalf("MCP client listed on its own: %+v", item)
		}
	}
	if len(found) > 1 {
		t.Fatalf("%d MCP entries listed", len(found))
	}
	if len(found) == 0 {
		return authorizedApplication{}, false
	}
	return found[0], true
}

type mcpApplicationFixture struct {
	s         *server
	host      *httptest.Server
	admin     *http.Client
	member    *http.Client
	data      domain.Bootstrap
	workspace string
	adminA    mcpTestTokens
	adminB    mcpTestTokens
	memberC   mcpTestTokens
	clients   []string
	app       domain.OAuthApplication
}

// newMCPApplicationFixture: an admin authorizes two MCP clients (Claude with
// read, Cursor with read and write), a member a third (ChatGPT with read), and
// the admin a registered OAuth application.
func newMCPApplicationFixture(t *testing.T) mcpApplicationFixture {
	t.Helper()
	s, host, admin, data := applicationHTTPFixture(t)
	f := mcpApplicationFixture{s: s, host: host, admin: admin, data: data, workspace: data.Workspace.URLKey}
	if err := s.store.UpdateMemberRole(t.Context(), data.Workspace.ID, "usr_member", "member"); err != nil {
		t.Fatal(err)
	}
	f.member = authClient(t)
	authRequest[map[string]any](t, f.member, "POST", host.URL+"/api/auth/login", map[string]string{"email": "member@example.test", "password": "test-password"}, "", 200)
	var id string
	id, f.adminA = registerAndAuthorizeMCPClient(t, host, admin, f.workspace, "Claude", "https://claude.ai/api/mcp/auth_callback", "read", http.StatusCreated)
	f.clients = append(f.clients, id)
	id, f.adminB = registerAndAuthorizeMCPClient(t, host, admin, f.workspace, "Cursor", "http://127.0.0.1:43120/oauth/callback", "read write", http.StatusCreated)
	f.clients = append(f.clients, id)
	id, f.memberC = registerAndAuthorizeMCPClient(t, host, f.member, f.workspace, "ChatGPT", "https://chatgpt.com/connector_platform_oauth_redirect", "read", http.StatusCreated)
	f.clients = append(f.clients, id)
	f.app = createConfidentialApp(t, host, admin, f.workspace, []string{"read"})
	decision := authRequest[map[string]string](t, admin, "POST", host.URL+"/api/oauth/authorization-request", map[string]any{"clientId": f.app.ClientID, "redirectUri": oauthTestRedirect, "responseType": "code", "scope": "read", "state": "app", "workspaceKey": f.workspace, "approve": true}, "", 200)
	if redirect, err := url.Parse(decision["redirect"]); err != nil || redirect.Query().Get("code") == "" {
		t.Fatalf("application authorization = %v", decision)
	}
	return f
}

// TestAuthorizedApplicationsFoldMCPClients: every dynamically registered MCP
// client is listed as one "MCP" application by Flow, with its distinct
// members, the union of granted scopes, each member's first authorization and
// last use, while a registered OAuth application keeps its own row.
func TestAuthorizedApplicationsFoldMCPClients(t *testing.T) {
	f := newMCPApplicationFixture(t)
	t.Setenv("FLOW_APP_URL", "https://flow.example.test/")
	if status := mcpToolsListStatus(t, f.host, f.adminB.AccessToken); status != http.StatusOK {
		t.Fatalf("MCP status = %d", status)
	}
	var listed []authorizedApplication
	deadline := time.Now().Add(5 * time.Second)
	for {
		listed = authRequest[[]authorizedApplication](t, f.admin, "GET", f.host.URL+"/api/oauth/authorized-applications", nil, f.workspace, 200)
		if entry, ok := mcpEntry(t, listed); ok && slices.ContainsFunc(entry.Members, func(member authorizedApplicationMember) bool { return member.LastUsedAt != nil }) || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	entry, ok := mcpEntry(t, listed)
	if !ok {
		t.Fatalf("no MCP entry: %+v", listed)
	}
	if entry.ClientID != mcpApplicationID || entry.Name != "MCP" || entry.DeveloperName != "Flow" || entry.DeveloperURL != "https://flow.example.test" || len(entry.Authorizations) != 3 {
		t.Fatalf("MCP entry = %+v", entry)
	}
	clients := slices.Clone(entry.ClientIDs)
	want := slices.Clone(f.clients)
	slices.Sort(clients)
	slices.Sort(want)
	if !slices.Equal(clients, want) {
		t.Fatalf("MCP clients = %v, want %v", clients, want)
	}
	if len(entry.Members) != 2 {
		t.Fatalf("MCP members = %+v", entry.Members)
	}
	adminIndex := slices.IndexFunc(entry.Members, func(member authorizedApplicationMember) bool { return member.UserID == f.data.Viewer.ID })
	memberIndex := slices.IndexFunc(entry.Members, func(member authorizedApplicationMember) bool { return member.UserID == "usr_member" })
	if adminIndex != 0 || memberIndex != 1 {
		t.Fatalf("members are not ordered by first authorization: %+v", entry.Members)
	}
	adminMember, member := entry.Members[adminIndex], entry.Members[memberIndex]
	first := slices.MinFunc(entry.Authorizations, func(a, b domain.OAuthAuthorization) int { return a.CreatedAt.Compare(b.CreatedAt) })
	if !adminMember.AuthorizedAt.Equal(first.CreatedAt) || adminMember.LastUsedAt == nil || !slices.Equal(adminMember.Scopes, []string{"read", "write"}) {
		t.Fatalf("admin member = %+v", adminMember)
	}
	if member.LastUsedAt != nil || !slices.Equal(member.Scopes, []string{"read"}) {
		t.Fatalf("member = %+v", member)
	}
	if !slices.Equal(entry.Permissions, []authorizedApplicationPermission{{Scope: "read", ActivelyUsed: true}, {Scope: "write", ActivelyUsed: true}}) {
		t.Fatalf("MCP permissions = %+v", entry.Permissions)
	}
	if index := slices.IndexFunc(listed, func(item authorizedApplication) bool { return item.ClientID == f.app.ClientID }); index < 0 || listed[index].Kind != "" || len(listed[index].Authorizations) != 1 {
		t.Fatalf("registered application row = %+v", listed)
	}
}

func TestMCPApplicationPermissionsActiveUse(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	recent, stale := now.Add(-time.Hour), now.Add(-mcpActiveUseWindow-time.Hour)
	entry := mcpAuthorizedApplication([]domain.OAuthAuthorization{
		{ID: "a", ClientID: "flow_mcp_a", UserID: "u1", Scopes: []string{"read"}, CreatedAt: now.Add(-48 * time.Hour), LastUsedAt: &recent},
		{ID: "b", ClientID: "flow_mcp_b", UserID: "u1", Scopes: []string{"read", "write"}, CreatedAt: now.Add(-72 * time.Hour), LastUsedAt: &stale},
		{ID: "c", ClientID: "flow_mcp_b", UserID: "u2", Scopes: []string{"read"}, CreatedAt: now.Add(-24 * time.Hour)},
	}, "https://flow.example.test", now)
	if !slices.Equal(entry.Permissions, []authorizedApplicationPermission{{Scope: "read", ActivelyUsed: true}, {Scope: "write"}}) {
		t.Fatalf("permissions = %+v", entry.Permissions)
	}
	if len(entry.Members) != 2 || entry.Members[0].UserID != "u1" || !entry.Members[0].AuthorizedAt.Equal(now.Add(-72*time.Hour)) || !entry.Members[0].LastUsedAt.Equal(recent) || entry.Members[1].LastUsedAt != nil {
		t.Fatalf("members = %+v", entry.Members)
	}
	if !slices.Equal(entry.ClientIDs, []string{"flow_mcp_a", "flow_mcp_b"}) {
		t.Fatalf("clients = %v", entry.ClientIDs)
	}
	installed := map[string]bool{"flow_mcp_installed": true}
	if mcpOAuthAuthorization(domain.OAuthAuthorization{ClientID: "flow_mcp_installed"}, installed) || mcpOAuthAuthorization(domain.OAuthAuthorization{ClientID: "flow_client_x"}, installed) || mcpOAuthAuthorization(domain.OAuthAuthorization{ClientID: "flow_mcp_a", Actor: "app"}, installed) || !mcpOAuthAuthorization(domain.OAuthAuthorization{ClientID: "flow_mcp_a"}, installed) {
		t.Fatal("MCP authorization classification is wrong")
	}
}

// TestRevokeMCPAccessPermissions: a member's (or guest's) "Revoke access"
// revokes only their own MCP authorizations; an admin's revokes every
// member's on every MCP client. Revoked access tokens are rejected by the MCP
// endpoint, refresh tokens stop working, and the registered OAuth
// application is untouched.
func TestRevokeMCPAccessPermissions(t *testing.T) {
	f := newMCPApplicationFixture(t)
	authRequest[map[string]any](t, f.member, "GET", f.host.URL+"/api/oauth/authorized-applications", nil, f.workspace, 403)

	revoked := authRequest[map[string]int](t, f.member, "DELETE", f.host.URL+"/api/oauth/authorized-applications/mcp", nil, f.workspace, 200)
	if revoked["revoked"] != 1 {
		t.Fatalf("member revoked %v", revoked)
	}
	if status := mcpToolsListStatus(t, f.host, f.memberC.AccessToken); status != http.StatusUnauthorized {
		t.Fatalf("member's revoked token MCP status = %d", status)
	}
	for _, token := range []string{f.adminA.AccessToken, f.adminB.AccessToken} {
		if status := mcpToolsListStatus(t, f.host, token); status != http.StatusOK {
			t.Fatalf("a member's revoke affected the admin's token: %d", status)
		}
	}

	// A guest with no MCP authorizations of their own revokes nothing.
	if err := f.s.store.UpdateMemberRole(t.Context(), f.data.Workspace.ID, "usr_member", "guest"); err != nil {
		t.Fatal(err)
	}
	if revoked := authRequest[map[string]int](t, f.member, "DELETE", f.host.URL+"/api/oauth/authorized-applications/mcp", nil, f.workspace, 200); revoked["revoked"] != 0 {
		t.Fatalf("guest revoked %v", revoked)
	}
	if status := mcpToolsListStatus(t, f.host, f.adminA.AccessToken); status != http.StatusOK {
		t.Fatalf("a guest's revoke affected the admin's token: %d", status)
	}

	// The member authorizes again; the admin's revoke covers every member and client.
	if err := f.s.store.UpdateMemberRole(t.Context(), f.data.Workspace.ID, "usr_member", "member"); err != nil {
		t.Fatal(err)
	}
	_, f.memberC = registerAndAuthorizeMCPClient(t, f.host, f.member, f.workspace, "ChatGPT", "https://chatgpt.com/connector_platform_oauth_redirect", "read", http.StatusOK)
	revoked = authRequest[map[string]int](t, f.admin, "DELETE", f.host.URL+"/api/oauth/authorized-applications/mcp", nil, f.workspace, 200)
	if revoked["revoked"] != 3 {
		t.Fatalf("admin revoked %v", revoked)
	}
	for _, tokens := range []mcpTestTokens{f.adminA, f.adminB, f.memberC} {
		if status := mcpToolsListStatus(t, f.host, tokens.AccessToken); status != http.StatusUnauthorized {
			t.Fatalf("revoked token MCP status = %d", status)
		}
	}
	postOAuthForm[map[string]any](t, f.host.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {f.adminB.RefreshToken}, "client_id": {f.clients[1]}}, http.StatusBadRequest)
	listed := authRequest[[]authorizedApplication](t, f.admin, "GET", f.host.URL+"/api/oauth/authorized-applications", nil, f.workspace, 200)
	if _, ok := mcpEntry(t, listed); ok {
		t.Fatalf("MCP still listed after revoking: %+v", listed)
	}
	if !slices.ContainsFunc(listed, func(item authorizedApplication) bool {
		return item.ClientID == f.app.ClientID && len(item.Authorizations) == 1
	}) {
		t.Fatalf("revoking MCP access touched the registered application: %+v", listed)
	}
}
