package main

import (
	"net/url"
	"slices"
	"testing"
)

// TestAuthorizedApplicationsExposeDynamicClientRedirectURIs: a dynamically
// registered MCP client (here one calling itself "Claude" with claude.ai's
// callback) is listed with its registered redirect URIs, which the web app
// uses to recognise well-known clients and show their logo.
func TestAuthorizedApplicationsExposeDynamicClientRedirectURIs(t *testing.T) {
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
	index := slices.IndexFunc(listed, func(item authorizedApplication) bool { return item.ClientID == registered.ClientID })
	if index < 0 || listed[index].Name != "Claude" || !slices.Equal(listed[index].RedirectURIs, redirects) {
		t.Fatalf("authorized applications = %+v", listed)
	}
}
