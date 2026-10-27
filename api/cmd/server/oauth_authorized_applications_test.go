package main

import (
	"net/url"
	"slices"
	"testing"
)

// TestAuthorizedApplicationsFoldDynamicClient: a dynamically registered MCP
// client (here one calling itself "Claude" with claude.ai's callback) is not
// listed on its own: it is folded into the single "MCP" entry.
func TestAuthorizedApplicationsFoldDynamicClient(t *testing.T) {
	_, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	redirects := []string{"https://claude.ai/api/mcp/auth_callback", "https://claude.com/api/mcp/auth_callback"}
	registered := authRequest[struct {
		ClientID     string   `json:"client_id"`
		RedirectURIs []string `json:"redirect_uris"`
	}](t, admin, "POST", host.URL+"/oauth/register", map[string]any{"client_name": "Claude", "redirect_uris": redirects, "token_endpoint_auth_method": "none"}, "", 201)
	body := map[string]any{"clientId": registered.ClientID, "redirectUri": redirects[0], "responseType": "code", "scope": "read", "state": "s", "codeChallenge": "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM", "codeChallengeMethod": "S256", "workspaceKey": workspace, "approve": true}
	decision := authRequest[map[string]string](t, admin, "POST", host.URL+"/api/oauth/authorization-request", body, "", 200)
	if redirect, err := url.Parse(decision["redirect"]); err != nil || redirect.Query().Get("code") == "" {
		t.Fatalf("authorization = %v", decision)
	}
	listed := authRequest[[]authorizedApplication](t, admin, "GET", host.URL+"/api/oauth/authorized-applications", nil, workspace, 200)
	if slices.ContainsFunc(listed, func(item authorizedApplication) bool { return item.ClientID == registered.ClientID }) {
		t.Fatalf("MCP client listed on its own: %+v", listed)
	}
	index := slices.IndexFunc(listed, func(item authorizedApplication) bool { return item.ClientID == mcpApplicationID })
	if index < 0 || listed[index].Name != "MCP" || !slices.Equal(listed[index].ClientIDs, []string{registered.ClientID}) || len(listed[index].Members) != 1 {
		t.Fatalf("authorized applications = %+v", listed)
	}
}
