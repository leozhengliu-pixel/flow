package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"testing"
)

// TestOAuthAuthorizeRequestErrorsMatchReference checks the authorization
// request's error codes and wording against what the reference app's /oauth/authorize
// shows for the same mistakes.
func TestOAuthAuthorizeRequestErrorsMatchReference(t *testing.T) {
	_, host, admin, data := applicationHTTPFixture(t)
	app := createConfidentialApp(t, host, admin, data.Workspace.URLKey, []string{"read", "write", "app:assignable", "app:mentionable"})
	base := func() url.Values {
		return url.Values{"response_type": {"code"}, "client_id": {app.ClientID}, "redirect_uri": {oauthTestRedirect}, "scope": {"read"}}
	}
	cases := []struct {
		name        string
		edit        func(url.Values)
		code        string
		description string
	}{
		{"unknown client", func(q url.Values) { q.Set("client_id", "00000000000000000000000000000000") }, "invalid_client", "Could not find OAuth client with clientId 00000000000000000000000000000000. Please check that you are logged into the correct workspace."},
		{"missing redirect_uri", func(q url.Values) { q.Del("redirect_uri") }, "invalid_request", "Invalid parameters. Make sure to include `redirect_uri`"},
		{"missing response_type", func(q url.Values) { q.Del("response_type") }, "invalid_request", "Invalid or missing `response_type`. Expected `response_type=code`"},
		{"unregistered redirect_uri", func(q url.Values) { q.Set("redirect_uri", "https://evil.example.test/callback") }, "invalid_request", "Invalid redirect_uri parameter for the application."},
		{"unknown scope", func(q url.Values) { q.Set("scope", "read,bogus") }, "invalid_scope", "Invalid scope: bogus"},
		{"unknown app scope", func(q url.Values) { q.Set("scope", "read,bogus"); q.Set("actor", "app") }, "invalid_scope", "Invalid scope: bogus"},
		{"app scope without actor=app", func(q url.Values) { q.Set("scope", "read,app:assignable") }, "invalid_scope", "The scopes requested are not valid for this actor mode."},
	}
	for _, item := range cases {
		query := base()
		item.edit(query)
		body := authRequest[map[string]any](t, admin, "GET", host.URL+"/api/oauth/authorization-request?"+query.Encode(), nil, "", 400)
		if body["error"] != item.code || body["error_description"] != item.description {
			t.Errorf("%s = %v, want %s %q", item.name, body, item.code, item.description)
		}
	}

	// No scope parameter defaults to read, worded as the reference app words it.
	query := base()
	query.Del("scope")
	details := authRequest[map[string]any](t, admin, "GET", host.URL+"/api/oauth/authorization-request?"+query.Encode(), nil, "", 200)
	if labels, _ := details["scopeLabels"].([]any); len(labels) != 1 || labels[0] != "Read access to your workspace" {
		t.Fatalf("default scope labels = %v", details["scopeLabels"])
	}
	query = base()
	query.Set("scope", "read,write,app:assignable,app:mentionable")
	query.Set("actor", "app")
	details = authRequest[map[string]any](t, admin, "GET", host.URL+"/api/oauth/authorization-request?"+query.Encode(), nil, "", 200)
	want := []any{"Read access to your workspace", "Write access to your workspace", "Assign issues and projects to the app in teams it can access", "Mention app in issues, projects, and documents it can access"}
	if labels, _ := details["scopeLabels"].([]any); !slices.Equal(labels, want) {
		t.Fatalf("app scope labels = %v", details["scopeLabels"])
	}
}

// TestOAuthAuthorizeCancelRedirectsLikeReference: Cancel sends the browser back
// with error=access_denied and the state, without an error_description.
func TestOAuthAuthorizeCancelRedirectsLikeReference(t *testing.T) {
	_, host, admin, data := applicationHTTPFixture(t)
	app := createConfidentialApp(t, host, admin, data.Workspace.URLKey, []string{"read"})
	body := map[string]any{"clientId": app.ClientID, "redirectUri": oauthTestRedirect, "responseType": "code", "scope": "read", "state": "qa123", "workspaceKey": data.Workspace.URLKey, "approve": false}
	decision := authRequest[map[string]string](t, admin, "POST", host.URL+"/api/oauth/authorization-request", body, "", 200)
	redirect, err := url.Parse(decision["redirect"])
	if err != nil {
		t.Fatal(err)
	}
	if got := redirect.Query(); got.Get("error") != "access_denied" || got.Get("state") != "qa123" || got.Has("error_description") || got.Has("code") || len(got) != 2 {
		t.Fatalf("cancel redirect = %s", redirect)
	}
}

// TestOAuthApplicationClientSecretCopy: the settings page's copy button reads
// the current client secret (masked on the page).
func TestOAuthApplicationClientSecretCopy(t *testing.T) {
	_, host, admin, data := applicationHTTPFixture(t)
	app := createConfidentialApp(t, host, admin, data.Workspace.URLKey, []string{"read"})
	got := authRequest[map[string]string](t, admin, "GET", host.URL+"/api/oauth-applications/"+app.ID+"/client-secret", nil, data.Workspace.URLKey, 200)
	if got["clientSecret"] != app.ClientSecret {
		t.Fatal("client secret does not match the created application's")
	}
	rotated := authRequest[map[string]string](t, admin, "POST", host.URL+"/api/oauth-applications/"+app.ID+"/rotate-secret", nil, data.Workspace.URLKey, 200)
	if got = authRequest[map[string]string](t, admin, "GET", host.URL+"/api/oauth-applications/"+app.ID+"/client-secret", nil, data.Workspace.URLKey, 200); got["clientSecret"] != rotated["clientSecret"] || got["clientSecret"] == app.ClientSecret {
		t.Fatal("client secret was not rotated")
	}
	authRequest[map[string]any](t, admin, "GET", host.URL+"/api/oauth-applications/missing/client-secret", nil, data.Workspace.URLKey, 404)
	if !apiKeyRestrictedPersonalPath("/api/oauth-applications/" + app.ID + "/client-secret") {
		t.Fatal("API keys must not read client secrets")
	}
}

// TestAuthorizedApplicationsListAndAdminRevoke: Settings › Applications lists
// every member's authorizations for admins, and an admin can revoke another
// member's authorization (the reference app's "Revoke access").
func TestAuthorizedApplicationsListAndAdminRevoke(t *testing.T) {
	s, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	if err := s.store.UpdateMemberRole(t.Context(), data.Workspace.ID, "usr_member", "member"); err != nil {
		t.Fatal(err)
	}
	app := createConfidentialApp(t, host, admin, workspace, []string{"read", "write"})
	member := authClient(t)
	authRequest[map[string]any](t, member, "POST", host.URL+"/api/auth/login", map[string]string{"email": "member@example.test", "password": "test-password"}, "", 200)
	body := map[string]any{"clientId": app.ClientID, "redirectUri": oauthTestRedirect, "responseType": "code", "scope": "read,write", "state": "member", "workspaceKey": workspace, "approve": true}
	decision := authRequest[map[string]string](t, member, "POST", host.URL+"/api/oauth/authorization-request", body, "", 200)
	if redirect, err := url.Parse(decision["redirect"]); err != nil || redirect.Query().Get("code") == "" {
		t.Fatalf("member authorization = %v", decision)
	}

	authRequest[map[string]any](t, member, "GET", host.URL+"/api/oauth/authorized-applications", nil, workspace, 403)
	listed := authRequest[[]authorizedApplication](t, admin, "GET", host.URL+"/api/oauth/authorized-applications", nil, workspace, 200)
	index := slices.IndexFunc(listed, func(item authorizedApplication) bool { return item.ClientID == app.ClientID })
	if index < 0 || listed[index].Name != app.Name || len(listed[index].Authorizations) != 1 || listed[index].Authorizations[0].UserID == data.Viewer.ID {
		t.Fatalf("authorized applications = %+v", listed)
	}

	authRequest[map[string]any](t, admin, "DELETE", host.URL+"/api/oauth/authorizations/"+listed[index].Authorizations[0].ID, nil, workspace, 204)
	listed = authRequest[[]authorizedApplication](t, admin, "GET", host.URL+"/api/oauth/authorized-applications", nil, workspace, 200)
	if slices.ContainsFunc(listed, func(item authorizedApplication) bool { return item.ClientID == app.ClientID }) {
		t.Fatalf("revoked application still listed: %+v", listed)
	}
}

// TestOAuthApplicationIconUpload: the create page's "Application icon" picker
// uploads the image and the manifest accepts the uploaded icon URL.
func TestOAuthApplicationIconUpload(t *testing.T) {
	_, host, admin, data := applicationHTTPFixture(t)
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("file", "icon.png")
	_, _ = part.Write(append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...))
	_ = form.Close()
	request, _ := http.NewRequest(http.MethodPost, host.URL+"/api/oauth-applications/icon", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("X-Workspace-Key", data.Workspace.URLKey)
	response, err := admin.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var uploaded map[string]string
	_ = json.NewDecoder(response.Body).Decode(&uploaded)
	if response.StatusCode != http.StatusCreated || !uploadedOAuthAppIcon(uploaded["url"]) {
		t.Fatalf("icon upload = %d %v", response.StatusCode, uploaded)
	}
	if uploadedOAuthAppIcon("/uploads/workspace_logo_x.png") || uploadedOAuthAppIcon("https://evil.test/uploads/oauth_app_icon_1.png") {
		t.Fatal("only uploaded application icons are accepted")
	}
	manifest := map[string]any{"schemaVersion": "1.0.0", "developer": map[string]any{"name": "QA Developer"}, "display": map[string]any{"iconUrl": uploaded["url"]}, "oauth": map[string]any{"client_name": "Icon app", "redirect_uris": []string{oauthTestRedirect}, "scopes": []string{"read"}}}
	result := authRequest[map[string]any](t, admin, "POST", host.URL+"/api/oauth-applications/manifest/validate", manifest, data.Workspace.URLKey, 200)
	if result["valid"] != true {
		t.Fatalf("manifest with uploaded icon = %v", result)
	}
}
