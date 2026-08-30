package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestMCPOAuthPKCEAndToolLifecycle(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	server := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer server.Close()
	client := authClient(t)
	authRequest[domain.AuthSession](t, client, http.MethodPost, server.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)

	registered := authRequest[domain.OAuthClient](t, client, http.MethodPost, server.URL+"/oauth/register", map[string]any{
		"client_name": "Codex test", "redirect_uris": []string{"http://127.0.0.1:43119/callback"}, "token_endpoint_auth_method": "none",
	}, "", http.StatusCreated)
	if registered.ClientID == "" || registered.ClientName != "Codex test" {
		t.Fatalf("registered client = %#v", registered)
	}

	verifier := strings.Repeat("v", 48)
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	query := url.Values{"response_type": {"code"}, "client_id": {registered.ClientID}, "redirect_uri": {registered.RedirectURIs[0]}, "scope": {"read write openid email"}, "state": {"test-state"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	details := authRequest[map[string]any](t, client, http.MethodGet, server.URL+"/api/oauth/authorization-request?"+query.Encode(), nil, "", http.StatusOK)
	if details["redirectUri"] != registered.RedirectURIs[0] {
		t.Fatalf("authorization request = %#v", details)
	}
	decision := authRequest[map[string]string](t, client, http.MethodPost, server.URL+"/api/oauth/authorization-request", map[string]any{
		"clientId": registered.ClientID, "redirectUri": registered.RedirectURIs[0], "responseType": "code", "scope": "read write openid email", "state": "test-state", "codeChallenge": challenge, "codeChallengeMethod": "S256", "workspaceKey": "test-workspace", "approve": true,
	}, "", http.StatusOK)
	redirect, err := url.Parse(decision["redirect"])
	if err != nil || redirect.Query().Get("code") == "" || redirect.Query().Get("state") != "test-state" {
		t.Fatalf("authorization redirect = %#v, err=%v", decision, err)
	}

	tokens := postOAuthForm[struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Scope        string `json:"scope"`
	}](t, server.URL+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {redirect.Query().Get("code")}, "client_id": {registered.ClientID}, "redirect_uri": {registered.RedirectURIs[0]}, "code_verifier": {verifier}}, http.StatusOK)
	if tokens.AccessToken == "" || tokens.RefreshToken == "" || !strings.Contains(tokens.Scope, "write") {
		t.Fatalf("token response = %#v", tokens)
	}

	initialize := callMCP(t, server.URL+"/mcp", tokens.AccessToken, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25", "clientInfo": map[string]string{"name": "test", "version": "1"}, "capabilities": map[string]any{}}})
	if initialize.Error != nil {
		t.Fatalf("initialize error = %#v", initialize.Error)
	}
	listed := callMCP(t, server.URL+"/mcp", tokens.AccessToken, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{}})
	tools := listed.Result.(map[string]any)["tools"].([]any)
	if len(tools) != 48 {
		t.Fatalf("tools/list count = %d, want 48", len(tools))
	}
	teams := callMCP(t, server.URL+"/mcp", tokens.AccessToken, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "list_teams", "arguments": map[string]any{}}})
	if teams.Error != nil || teams.Result == nil {
		t.Fatalf("list_teams response = %#v", teams)
	}
	teamItems := teams.Result.(map[string]any)["structuredContent"].(map[string]any)["items"].([]any)
	teamID := teamItems[0].(map[string]any)["id"].(string)
	created := callMCP(t, server.URL+"/mcp", tokens.AccessToken, map[string]any{"jsonrpc": "2.0", "id": 31, "method": "tools/call", "params": map[string]any{"name": "save_issue", "arguments": map[string]any{"title": "MCP lifecycle issue", "team": teamID, "priority": 2}}})
	if created.Result.(map[string]any)["isError"] == true {
		t.Fatalf("save_issue response = %#v", created)
	}
	bootstrap, ok := repository.BootstrapFor("test-workspace")
	if !ok || !slices.ContainsFunc(bootstrap.Issues, func(item domain.Issue) bool { return item.Title == "MCP lifecycle issue" && item.Priority == 2 }) {
		t.Fatal("save_issue did not persist the created issue")
	}
	createdLabel := callMCP(t, server.URL+"/mcp", tokens.AccessToken, map[string]any{"jsonrpc": "2.0", "id": 32, "method": "tools/call", "params": map[string]any{"name": "create_initiative_label", "arguments": map[string]any{"name": "MCP initiative label", "color": "#5e6ad2"}}})
	if createdLabel.Result.(map[string]any)["isError"] == true {
		t.Fatalf("create_initiative_label response = %#v", createdLabel)
	}
	bootstrap, _ = repository.BootstrapFor("test-workspace")
	if !slices.ContainsFunc(bootstrap.Labels, func(item domain.IssueLabel) bool {
		return item.Name == "MCP initiative label" && item.ResourceType == "initiative" && labelScopeIsWorkspace(item.Scope) && item.GroupID == ""
	}) {
		t.Fatal("create_initiative_label did not use the initiative label contract")
	}
	groupedLabel := callMCP(t, server.URL+"/mcp", tokens.AccessToken, map[string]any{"jsonrpc": "2.0", "id": 33, "method": "tools/call", "params": map[string]any{"name": "create_initiative_label", "arguments": map[string]any{"name": "Invalid initiative group", "isGroup": true}}})
	if groupedLabel.Result.(map[string]any)["isError"] != true {
		t.Fatalf("initiative label group was accepted: %#v", groupedLabel)
	}

	readonly := callMCP(t, server.URL+"/mcp/readonly", tokens.AccessToken, map[string]any{"jsonrpc": "2.0", "id": 4, "method": "tools/list", "params": map[string]any{}})
	for _, raw := range readonly.Result.(map[string]any)["tools"].([]any) {
		if strings.HasPrefix(raw.(map[string]any)["name"].(string), "save_") {
			t.Fatalf("readonly endpoint exposed write tool %#v", raw)
		}
	}
	denied := callMCP(t, server.URL+"/mcp/readonly", tokens.AccessToken, map[string]any{"jsonrpc": "2.0", "id": 41, "method": "tools/call", "params": map[string]any{"name": "save_issue", "arguments": map[string]any{"title": "Denied", "team": teamID}}})
	if denied.Result.(map[string]any)["isError"] != true {
		t.Fatalf("readonly write was not denied: %#v", denied)
	}

	refreshed := postOAuthForm[map[string]any](t, server.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tokens.RefreshToken}, "client_id": {registered.ClientID}}, http.StatusOK)
	if refreshed["access_token"] == "" || refreshed["refresh_token"] == tokens.RefreshToken {
		t.Fatalf("refresh response = %#v", refreshed)
	}
}

func postOAuthForm[T any](t *testing.T, endpoint string, form url.Values, wantStatus int) T {
	t.Helper()
	response, err := http.Post(endpoint, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != wantStatus {
		t.Fatalf("POST %s status %d, want %d: %s", endpoint, response.StatusCode, wantStatus, raw)
	}
	var result T
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func callMCP(t *testing.T, endpoint, token string, payload any) mcpRPCResponse {
	t.Helper()
	raw, _ := json.Marshal(payload)
	request, _ := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("MCP status %d: %s", response.StatusCode, body)
	}
	var result mcpRPCResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
