package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"flow/api/internal/domain"
)

// appConsent approves an actor=app authorization request as the admin and
// redeems the code with the client secret, returning the token pair.
func appConsent(t *testing.T, host *httptest.Server, admin *http.Client, app domain.OAuthApplication, workspace, scope string, team map[string]any) oauthTokenPair {
	t.Helper()
	body := map[string]any{"actor": "app", "clientId": app.ClientID, "redirectUri": oauthTestRedirect, "responseType": "code", "scope": scope, "state": "s", "workspaceKey": workspace, "approve": true}
	for key, value := range team {
		body[key] = value
	}
	decision := authRequest[map[string]string](t, admin, "POST", host.URL+"/api/oauth/authorization-request", body, "", 200)
	redirect, err := url.Parse(decision["redirect"])
	if err != nil || redirect.Query().Get("code") == "" {
		t.Fatalf("app consent = %v (%v)", decision, err)
	}
	exchange := url.Values{"grant_type": {"authorization_code"}, "code": {redirect.Query().Get("code")}, "redirect_uri": {oauthTestRedirect}, "client_id": {app.ClientID}, "client_secret": {app.ClientSecret}}
	return oauthForm[oauthTokenPair](t, host.URL+"/oauth/token", exchange, "", "", 200)
}

type consentInstallationBody struct {
	Installation *consentInstallation `json:"installation"`
}

func visibleTeamIDs(t *testing.T, host *httptest.Server, token string) []string {
	t.Helper()
	var visible domain.Bootstrap
	if err := json.Unmarshal(applicationBearerRequest(t, host, token, "GET", "/api/bootstrap", "", 200), &visible); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, team := range visible.Teams {
		ids = append(ids, team.ID)
	}
	return ids
}

// TestOAuthAppReconsentMergesScopesAndKeepsTeamAccess follows what the reference app does
// when an installed application asks again: no team access is asked, the
// requested scopes are added to the installed ones (never removed), the app
// user and its team access stay, every code carries all installed scopes and
// the previous app token is revoked.
func TestOAuthAppReconsentMergesScopesAndKeepsTeamAccess(t *testing.T) {
	s, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	private := authRequest[domain.Team](t, admin, "POST", host.URL+"/api/workspaces/"+workspace+"/teams", map[string]any{"name": "Private", "key": "PRV", "private": true}, "", 201)
	hidden := authRequest[domain.Team](t, admin, "POST", host.URL+"/api/workspaces/"+workspace+"/teams", map[string]any{"name": "Hidden", "key": "HID", "private": true}, "", 201)
	app := createConfidentialApp(t, host, admin, workspace, []string{"read", "write", "app:assignable", "app:mentionable"})
	status := func() *consentInstallation {
		return authRequest[consentInstallationBody](t, admin, "GET", host.URL+"/api/oauth/installation?workspace="+workspace+"&client_id="+app.ClientID, nil, "", 200).Installation
	}
	if status() != nil {
		t.Fatal("installation reported before the app was installed")
	}

	// First install: "All public teams and select other teams…" with one private team.
	first := appConsent(t, host, admin, app, workspace, "read", map[string]any{"allPublicTeams": true, "teamIds": []string{private.ID}})
	if first.Actor != "app" || first.Scope != "read" {
		t.Fatalf("install token = %+v", first)
	}
	if teams := visibleTeamIDs(t, host, first.AccessToken); !slices.Contains(teams, private.ID) || !slices.Contains(teams, data.Teams[0].ID) || slices.Contains(teams, hidden.ID) {
		t.Fatalf("app user teams = %v (want public teams and %s, not %s)", teams, private.ID, hidden.ID)
	}
	installed := status()
	if installed == nil || !slices.Equal(installed.Scopes, []string{"read"}) || installed.InstalledBy != data.Viewer.ID || installed.InstallerName == "" || installed.Realtime {
		t.Fatalf("installation status = %+v", installed)
	}

	// "Update access": more scopes, no team access in the request.
	second := appConsent(t, host, admin, app, workspace, "read,write,app:assignable", nil)
	if second.Scope != "app:assignable read write" {
		t.Fatalf("update token scope = %q", second.Scope)
	}
	applicationBearerRequest(t, host, first.AccessToken, "GET", "/api/bootstrap", "", 401)
	refresh := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {first.RefreshToken}, "client_id": {app.ClientID}, "client_secret": {app.ClientSecret}}
	oauthForm[oauthTokenPair](t, host.URL+"/oauth/token", refresh, "", "", 400)

	// "Continue" with fewer scopes still issues every installed scope.
	third := appConsent(t, host, admin, app, workspace, "read", nil)
	if third.Scope != "app:assignable read write" {
		t.Fatalf("continue token scope = %q", third.Scope)
	}
	applicationBearerRequest(t, host, second.AccessToken, "GET", "/api/bootstrap", "", 401)
	items, err := s.store.ListApplications(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(items, func(item domain.ApplicationInstallation) bool { return item.ClientID == app.ClientID })
	if index < 0 || !items[index].AllPublicTeams || !slices.Equal(items[index].TeamIDs, []string{private.ID}) || !slices.Equal(items[index].Scopes, []string{"app:assignable", "read", "write"}) {
		t.Fatalf("installation after reconsent = %+v", items)
	}
	if teams := visibleTeamIDs(t, host, third.AccessToken); !slices.Contains(teams, private.ID) || slices.Contains(teams, hidden.ID) {
		t.Fatalf("team access changed by reconsent: %v", teams)
	}
	var viewer domain.Bootstrap
	if err := json.Unmarshal(applicationBearerRequest(t, host, third.AccessToken, "GET", "/api/bootstrap", "", 200), &viewer); err != nil || viewer.Viewer.ID != items[index].UserID || !viewer.Viewer.App {
		t.Fatalf("app user changed: %+v / %s (%v)", items[index], viewer.Viewer.ID, err)
	}
}

// TestOAuthAppRealtimePermission: an application with a webhook asks for
// "Receive realtime updates about your workspace". An installation made
// before the application had a webhook gets no deliveries until an admin
// authorizes it again, as the reference app requires.
func TestOAuthAppRealtimePermission(t *testing.T) {
	s, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	app := createConfidentialApp(t, host, admin, workspace, []string{"read", "write"})
	query := url.Values{"actor": {"app"}, "response_type": {"code"}, "client_id": {app.ClientID}, "redirect_uri": {oauthTestRedirect}, "scope": {"read"}}
	details := authRequest[map[string]any](t, admin, "GET", host.URL+"/api/oauth/authorization-request?"+query.Encode(), nil, "", 200)
	if details["realtime"] != false {
		t.Fatalf("realtime without a webhook = %v", details["realtime"])
	}
	appConsent(t, host, admin, app, workspace, "read", map[string]any{"allPublicTeams": true})

	err := s.store.MutateWorkspace(context.Background(), workspace, "test.app_webhook", "", nil, func(next *domain.Bootstrap) error {
		for index := range next.OAuthApplications {
			if next.OAuthApplications[index].ClientID == app.ClientID {
				next.OAuthApplications[index].WebhookEnabled = true
				next.OAuthApplications[index].WebhookURL = "https://hooks.example.test/flow"
				next.OAuthApplications[index].WebhookResourceTypes = []string{"Issue"}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	details = authRequest[map[string]any](t, admin, "GET", host.URL+"/api/oauth/authorization-request?"+query.Encode(), nil, "", 200)
	if details["realtime"] != true {
		t.Fatalf("realtime with a webhook = %v", details["realtime"])
	}
	listed := authRequest[[]authorizedApplication](t, admin, "GET", host.URL+"/api/oauth/authorized-applications", nil, workspace, 200)
	if index := slices.IndexFunc(listed, func(item authorizedApplication) bool { return item.ClientID == app.ClientID }); index < 0 || !listed[index].Realtime {
		t.Fatalf("authorized applications = %+v", listed)
	}
	installation := authRequest[consentInstallationBody](t, admin, "GET", host.URL+"/api/oauth/installation?workspace="+workspace+"&client_id="+app.ClientID, nil, "", 200).Installation
	if installation == nil || installation.Realtime {
		t.Fatalf("installation made before the webhook = %+v", installation)
	}
	s.reconcileApplicationWebhooks(context.Background(), workspace)
	if _, ok := managedWebhook(s, app.ClientID); ok {
		t.Fatal("installation without realtime consent got a webhook")
	}

	// "Update access" with the realtime permission.
	appConsent(t, host, admin, app, workspace, "read", nil)
	installation = authRequest[consentInstallationBody](t, admin, "GET", host.URL+"/api/oauth/installation?workspace="+workspace+"&client_id="+app.ClientID, nil, "", 200).Installation
	if installation == nil || !installation.Realtime {
		t.Fatalf("installation after update = %+v", installation)
	}
	s.reconcileApplicationWebhooks(context.Background(), workspace)
	if _, ok := managedWebhook(s, app.ClientID); !ok {
		t.Fatal("authorized installation has no webhook")
	}

	// Saving team access on the settings page keeps the consent.
	items, _ := s.store.ListApplications(t.Context(), workspace)
	index := slices.IndexFunc(items, func(item domain.ApplicationInstallation) bool { return item.ClientID == app.ClientID })
	saved := authRequest[struct {
		Application domain.ApplicationInstallation `json:"application"`
	}](t, admin, "PATCH", host.URL+"/api/application-installations/"+items[index].ID, map[string]any{"allPublicTeams": true, "teamIds": []string{}, "scopes": items[index].Scopes, "active": true, "withoutRealtime": true}, workspace, 200)
	if saved.Application.WithoutRealtime {
		t.Fatal("settings save dropped the realtime consent")
	}
}

// TestOAuthConsentInstallationAdminOnly: only admins (who can install) read
// the installation state on the consent screen.
func TestOAuthConsentInstallationAdminOnly(t *testing.T) {
	s, host, admin, data := applicationHTTPFixture(t)
	if err := s.store.UpdateMemberRole(t.Context(), data.Workspace.ID, "usr_member", "member"); err != nil {
		t.Fatal(err)
	}
	app := createConfidentialApp(t, host, admin, data.Workspace.URLKey, []string{"read"})
	member := authClient(t)
	authRequest[map[string]any](t, member, "POST", host.URL+"/api/auth/login", map[string]string{"email": "member@example.test", "password": "test-password"}, "", 200)
	authRequest[map[string]any](t, member, "GET", host.URL+"/api/oauth/installation?workspace="+data.Workspace.URLKey+"&client_id="+app.ClientID, nil, "", 403)
}
