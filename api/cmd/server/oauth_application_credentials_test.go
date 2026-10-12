package main

import (
	"context"
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

type applicationTokenResponse struct {
	Token   string `json:"access_token"`
	Actor   string `json:"actor"`
	Subject string `json:"subject_user_id"`
	Scope   string `json:"scope"`
	Expires int    `json:"expires_in"`
}

func applicationHTTPFixture(t *testing.T) (*server, *httptest.Server, *http.Client, domain.Bootstrap) {
	t.Helper()
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "apps.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	s := &server{store: repo, uploadPath: t.TempDir()}
	host := httptest.NewServer(newHandler(s))
	t.Cleanup(host.Close)
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", 200)
	return s, host, admin, repo.Bootstrap()
}

func applicationFormToken(t *testing.T, host *httptest.Server, app domain.OAuthApplication, scope string, status int) applicationTokenResponse {
	t.Helper()
	form := url.Values{"grant_type": {"client_credentials"}}
	if scope != "" {
		form.Set("scope", scope)
	}
	request, _ := http.NewRequest("POST", host.URL+"/oauth/token", strings.NewReader(form.Encode()))
	request.SetBasicAuth(app.ClientID, app.ClientSecret)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := host.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != status {
		t.Fatalf("token response %d: %s", response.StatusCode, body)
	}
	var result applicationTokenResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func applicationBearerRequest(t *testing.T, host *httptest.Server, token, method, path, body string, status int) []byte {
	t.Helper()
	request, _ := http.NewRequest(method, host.URL+path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := host.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != status {
		t.Fatalf("%s %s: %d %s", method, path, response.StatusCode, raw)
	}
	return raw
}

func TestApplicationMachineTokensUseAppIdentityAndPublicTeams(t *testing.T) {
	s, host, admin, data := applicationHTTPFixture(t)
	private := authRequest[domain.Team](t, admin, "POST", host.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Private", "key": "PRI", "private": true}, "", 201)
	app := authRequest[domain.OAuthApplication](t, admin, "POST", host.URL+"/api/oauth-applications", map[string]any{"name": "Automation", "scopes": []string{"read", "write", "app:assignable"}, "supportsClientCredentials": true}, data.Workspace.URLKey, 201)
	token := applicationFormToken(t, host, app, "read write app:assignable", 200)
	if token.Actor != "app" || token.Subject == data.Viewer.ID || token.Token == "" || token.Expires != 30*24*3600 {
		t.Fatalf("application credential: %+v", token)
	}
	raw := applicationBearerRequest(t, host, token.Token, "GET", "/api/bootstrap", "", 200)
	var visible domain.Bootstrap
	if err := json.Unmarshal(raw, &visible); err != nil {
		t.Fatal(err)
	}
	if !visible.Viewer.App || visible.Viewer.ID != token.Subject || slices.ContainsFunc(visible.Teams, func(team domain.Team) bool { return team.ID == private.ID }) {
		t.Fatalf("app access leaked private team: %+v", visible.Teams)
	}
	body := `{"title":"App-created issue","teamId":"` + data.Teams[0].ID + `"}`
	raw = applicationBearerRequest(t, host, token.Token, "POST", "/api/issues", body, 201)
	var issue domain.Issue
	if err := json.Unmarshal(raw, &issue); err != nil {
		t.Fatal(err)
	}
	if issue.Creator.ID != token.Subject {
		t.Fatal("issue was attributed to the installer")
	}
	applicationBearerRequest(t, host, token.Token, "POST", "/api/issues", `{"title":"Forbidden","teamId":"`+private.ID+`"}`, 403)
	applicationBearerRequest(t, host, token.Token, "GET", "/api/oauth-applications", "", 401)
	applicationBearerRequest(t, host, token.Token, "POST", "/api/api-keys", `{"name":"Escalate"}`, 403)
	items, err := s.store.ListApplications(t.Context(), data.Workspace.URLKey)
	if err != nil || len(items) != 1 || !items[0].AllPublicTeams {
		t.Fatal("no installed application", err)
	}
}

func TestApplicationMachineGrantOptInScopesAndCompatibility(t *testing.T) {
	_, host, admin, data := applicationHTTPFixture(t)
	app := authRequest[domain.OAuthApplication](t, admin, "POST", host.URL+"/api/oauth-applications", map[string]any{"name": "Internal", "scopes": []string{"read"}}, data.Workspace.URLKey, 201)
	applicationFormToken(t, host, app, "read", 400)
	authRequest[domain.OAuthApplication](t, admin, "PATCH", host.URL+"/api/oauth-applications/"+app.ID, map[string]bool{"supportsClientCredentials": true}, data.Workspace.URLKey, 200)
	applicationFormToken(t, host, app, "read admin", 400)
	wrong := app
	wrong.ClientSecret = "wrong"
	applicationFormToken(t, host, wrong, "read", 401)
	legacy := authRequest[applicationTokenResponse](t, authClient(t), "POST", host.URL+"/api/oauth/token", map[string]string{"grant_type": "client_credentials", "client_id": app.ClientID, "client_secret": app.ClientSecret}, data.Workspace.URLKey, 200)
	if legacy.Actor != "app" {
		t.Fatal("legacy endpoint still issues personal actor tokens")
	}
	applicationBearerRequest(t, host, legacy.Token, "POST", "/api/issues", `{"title":"No write scope","teamId":"`+data.Teams[0].ID+`"}`, 401)
}

func TestApplicationSecretRotationAndDeveloperTokens(t *testing.T) {
	_, host, admin, data := applicationHTTPFixture(t)
	app := authRequest[domain.OAuthApplication](t, admin, "POST", host.URL+"/api/oauth-applications", map[string]any{"name": "Internal", "scopes": []string{"read"}, "supportsClientCredentials": true}, data.Workspace.URLKey, 201)
	machine := applicationFormToken(t, host, app, "read", 200)
	developer := authRequest[applicationTokenResponse](t, admin, "POST", host.URL+"/api/oauth-applications/"+app.ID+"/developer-token", map[string]any{"actor": "app", "scopes": []string{"read"}}, data.Workspace.URLKey, 200)
	if developer.Subject != machine.Subject || developer.Expires != 3600 {
		t.Fatal("developer token has a different actor or lifetime")
	}
	personal := authRequest[applicationTokenResponse](t, admin, "POST", host.URL+"/api/oauth-applications/"+app.ID+"/developer-token", map[string]any{"actor": "user", "scopes": []string{"read"}}, data.Workspace.URLKey, 200)
	if personal.Subject != data.Viewer.ID || personal.Actor != "user" {
		t.Fatal("personal developer actor is incorrect")
	}
	rotated := authRequest[struct {
		Secret string `json:"clientSecret"`
	}](t, admin, "POST", host.URL+"/api/oauth-applications/"+app.ID+"/rotate-secret", nil, data.Workspace.URLKey, 200)
	if rotated.Secret == "" || rotated.Secret == app.ClientSecret {
		t.Fatal("client secret was not rotated")
	}
	applicationBearerRequest(t, host, machine.Token, "GET", "/api/bootstrap", "", 401)
	applicationBearerRequest(t, host, developer.Token, "GET", "/api/bootstrap", "", 200)
	applicationFormToken(t, host, app, "read", 401)
	app.ClientSecret = rotated.Secret
	applicationFormToken(t, host, app, "read", 200)
}

func TestBuiltinApplicationCannotReadPrivateResourcesOfAnotherUser(t *testing.T) {
	s, host, admin, data := applicationHTTPFixture(t)
	private := authRequest[domain.Team](t, admin, "POST", host.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Restricted", "key": "RES", "private": true}, "", 201)
	issue := authRequest[domain.Issue](t, admin, "POST", host.URL+"/api/issues", map[string]any{"title": "Restricted issue", "teamId": private.ID}, data.Workspace.URLKey, 201)
	member, _, err := s.store.Register(t.Context(), "Agent requester", "agent-requester@example.test", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.EnsureWorkspaceMembership(t.Context(), data.Workspace.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	app, err := s.store.InstallApplication(t.Context(), data.Workspace.URLKey, domain.ApplicationInstallation{ClientID: "builtin-flow-agent", Name: "Flow Agent", InstalledBy: data.Viewer.ID, Scopes: domain.ApplicationScopes(), TeamIDs: []string{private.ID, data.Teams[0].ID}, Active: true, Builtin: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	actor := mcpActor{WorkspaceKey: data.Workspace.URLKey, User: app.User(), AuthorizationUserID: member.ID, APIKey: domain.APIKey{Actor: "app", Scopes: app.Scopes, TeamRestriction: "selected", TeamIDs: app.AccessTeamIDs()}}
	ctx := context.WithValue(t.Context(), workspaceKeyContextKey{}, data.Workspace.URLKey)
	ctx = context.WithValue(ctx, authUserContextKey{}, app.User())
	ctx = context.WithValue(ctx, apiKeyContextKey{}, actor.APIKey)
	control := actor
	control.AuthorizationUserID = ""
	result, err := s.callFlowTool(ctx, control, "list_issues", map[string]any{"team": private.ID})
	raw, _ := json.Marshal(result)
	if err != nil || !strings.Contains(string(raw), issue.ID) {
		t.Fatalf("application control could not read its granted issue: %s %v", raw, err)
	}
	if _, err := s.callFlowTool(ctx, actor, "list_issues", map[string]any{"team": private.ID}); err == nil {
		t.Fatal("builtin agent exposed another user's private issue")
	}
	request, _ := http.NewRequestWithContext(ctx, "GET", "http://flow.internal", nil)
	if _, _, err := s.builtinApplicationContext(request, app, domain.AgentTask{WorkspaceKey: data.Workspace.URLKey, IssueID: issue.ID, CreatorID: member.ID}); err == nil {
		t.Fatal("builtin task continued after requester lost access")
	}
}
