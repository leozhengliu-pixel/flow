package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

// Token responses, refresh and introspection report the reference app's scope names,
// sorted, while Flow's old names are still accepted on input.
func TestOAuthTokenResponsesUseFlowScopeNames(t *testing.T) {
	_, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	app := createConfidentialApp(t, host, admin, workspace, []string{"read", "write", "create_issues", "create_comments"})
	code := authorizeCode(t, host, admin, workspace, app.ClientID, "write,create_issues,comments:create", "")
	pair := oauthForm[oauthTokenPair](t, host.URL+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {oauthTestRedirect}}, app.ClientID, app.ClientSecret, 200)
	const want = "comments:create issues:create read write"
	if pair.Scope != want {
		t.Fatalf("token scope = %q, want %q", pair.Scope, want)
	}
	for _, token := range []string{pair.AccessToken, pair.RefreshToken} {
		introspection := oauthForm[map[string]any](t, host.URL+"/oauth/introspect", url.Values{"token": {token}}, app.ClientID, app.ClientSecret, 200)
		if introspection["active"] != true || introspection["scope"] != want {
			t.Fatalf("introspection = %v", introspection)
		}
	}
	refreshed := oauthForm[oauthTokenPair](t, host.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {pair.RefreshToken}}, app.ClientID, app.ClientSecret, 200)
	if refreshed.Scope != want {
		t.Fatalf("refreshed scope = %q", refreshed.Scope)
	}
	metadata := authRequest[map[string]any](t, admin, "GET", host.URL+"/.well-known/oauth-authorization-server?actor=app", nil, "", 200)
	advertised := fmt.Sprint(metadata["scopes_supported"])
	if strings.Contains(advertised, "create_issues") || !strings.Contains(advertised, "issues:create") || !strings.Contains(advertised, "comments:create") {
		t.Fatalf("scopes_supported = %s", advertised)
	}
}

// The reference app rejects customer:* and initiative:* (like app:*) for user
// authorizations of a registered application, and words them its way.
func TestOAuthUserAuthorizationRejectsAppOnlyResourceScopes(t *testing.T) {
	_, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	app := createConfidentialApp(t, host, admin, workspace, domain.ApplicationScopes())
	request := func(scope, actor string, status int) map[string]any {
		values := url.Values{"client_id": {app.ClientID}, "redirect_uri": {oauthTestRedirect}, "response_type": {"code"}, "scope": {scope}, "workspace": {workspace}}
		if actor != "" {
			values.Set("actor", actor)
		}
		return authRequest[map[string]any](t, admin, "GET", host.URL+"/api/oauth/authorization-request?"+values.Encode(), nil, "", status)
	}
	for _, scope := range []string{"customer:read", "customer:write", "initiative:read", "initiative:write"} {
		body := request("read,"+scope, "", 400)
		if body["error"] != "invalid_scope" || body["error_description"] != "The scopes requested are not valid for this actor mode." {
			t.Fatalf("user %s = %v", scope, body)
		}
	}
	granted := request("read,write,release:read", "", 200)
	if labels := fmt.Sprint(granted["scopeLabels"]); !strings.Contains(labels, "Read releases in the workspace") {
		t.Fatalf("release label = %s", labels)
	}
	app2 := request("read,customer:write,initiative:write,release:read,releaseStage:update", "app", 200)
	labels := fmt.Sprint(app2["scopeLabels"])
	for _, label := range []string{"Read and write data about customers in the workspace", "Read and write data about initiatives in the workspace", "Read releases in the workspace", "Update release stages in the workspace"} {
		if !strings.Contains(labels, label) {
			t.Fatalf("app labels %s lack %q", labels, label)
		}
	}
}

// The reference app's "Create & copy token": User creates a read/write token for the
// admin; App creates an app user token with the chosen scopes once, then
// copies that token again until it is revoked.
func TestApplicationDeveloperTokenUserAndExistingAppToken(t *testing.T) {
	_, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	app := authRequest[domain.OAuthApplication](t, admin, "POST", host.URL+"/api/oauth-applications", map[string]any{"name": "Dev tokens", "scopes": domain.ApplicationScopes()}, workspace, 201)
	path := host.URL + "/api/oauth-applications/" + app.ID + "/developer-token"
	type tokenBody struct {
		Token    string `json:"access_token"`
		Actor    string `json:"actor"`
		Scope    string `json:"scope"`
		Subject  string `json:"subject_user_id"`
		Existing bool   `json:"existing"`
	}
	user := authRequest[tokenBody](t, admin, "POST", path, map[string]any{"actor": "user"}, workspace, 200)
	if user.Actor != "user" || user.Scope != "read write" || user.Subject != data.Viewer.ID || user.Existing {
		t.Fatalf("user developer token = %+v", user)
	}
	if again := authRequest[tokenBody](t, admin, "POST", path, map[string]any{"actor": "user"}, workspace, 200); again.Token == user.Token {
		t.Fatal("a second user token reused the first")
	}
	authRequest[map[string]any](t, admin, "GET", path, nil, workspace, 404)
	created := authRequest[tokenBody](t, admin, "POST", path, map[string]any{"actor": "app", "scopes": []string{"read", "write", "initiative:read", "customer:read"}}, workspace, 200)
	if created.Actor != "app" || created.Scope != "customer:read initiative:read read write" || created.Existing {
		t.Fatalf("app developer token = %+v", created)
	}
	existing := authRequest[tokenBody](t, admin, "GET", path, nil, workspace, 200)
	if !existing.Existing || existing.Token != created.Token || existing.Scope != created.Scope {
		t.Fatalf("existing app token = %+v", existing)
	}
	// Asking for other scopes still copies the existing token.
	if reused := authRequest[tokenBody](t, admin, "POST", path, map[string]any{"actor": "app", "scopes": []string{"read"}}, workspace, 200); !reused.Existing || reused.Token != created.Token {
		t.Fatalf("app token was not reused: %+v", reused)
	}
	oauthForm[map[string]any](t, host.URL+"/oauth/revoke", url.Values{"token": {created.Token}}, "", "", 200)
	authRequest[map[string]any](t, admin, "GET", path, nil, workspace, 404)
	if fresh := authRequest[tokenBody](t, admin, "POST", path, map[string]any{"actor": "app", "scopes": []string{"read"}}, workspace, 200); fresh.Existing || fresh.Token == created.Token || fresh.Scope != "read" {
		t.Fatalf("token after revocation = %+v", fresh)
	}
}

// The reference app's "GitHub username": validated, unique per workspace, exported in
// the manifest and editable through PATCH.
func TestOAuthApplicationGitHubUsername(t *testing.T) {
	_, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	manifest := func(name, github string) map[string]any {
		value := map[string]any{"schemaVersion": "1.0.0", "developer": map[string]any{"name": "Acme"}, "oauth": map[string]any{"client_name": name, "redirect_uris": []string{oauthTestRedirect}}}
		if github != "" {
			value["github"] = map[string]any{"username": github}
		}
		return value
	}
	created := authRequest[domain.OAuthApplication](t, admin, "POST", host.URL+"/api/oauth-applications/manifest", manifest("Coder", "acme-coder[bot]"), workspace, 201)
	if created.GitHubUsername != "acme-coder[bot]" {
		t.Fatalf("github username = %q", created.GitHubUsername)
	}
	exported := authRequest[map[string]any](t, admin, "GET", host.URL+"/api/oauth-applications/"+created.ID+"/manifest", nil, workspace, 200)
	if fmt.Sprint(exported["github"]) != "map[username:acme-coder[bot]]" {
		t.Fatalf("exported github = %v", exported["github"])
	}
	invalid := authRequest[map[string]any](t, admin, "POST", host.URL+"/api/oauth-applications/manifest", manifest("Broken", "-bad name"), workspace, 400)
	if !strings.Contains(fmt.Sprint(invalid["issues"]), "Invalid GitHub username") {
		t.Fatalf("invalid username = %v", invalid)
	}
	taken := authRequest[map[string]string](t, admin, "POST", host.URL+"/api/oauth-applications/manifest", manifest("Twin", "ACME-coder[bot]"), workspace, 400)
	if taken["error"] != "There is already a user in Flow associated with this GitHub account." {
		t.Fatalf("duplicate username = %v", taken)
	}
	other := authRequest[domain.OAuthApplication](t, admin, "POST", host.URL+"/api/oauth-applications", map[string]any{"name": "Other", "githubUsername": "octocat"}, workspace, 201)
	authRequest[map[string]string](t, admin, "PATCH", host.URL+"/api/oauth-applications/"+other.ID, map[string]any{"githubUsername": "acme-coder[bot]"}, workspace, 400)
	authRequest[map[string]string](t, admin, "PATCH", host.URL+"/api/oauth-applications/"+other.ID, map[string]any{"githubUsername": "not valid"}, workspace, 400)
	// Saving the manifest without a github block clears the username.
	cleared := authRequest[domain.OAuthApplication](t, admin, "PUT", host.URL+"/api/oauth-applications/"+created.ID+"/manifest", manifest("Coder", ""), workspace, 200)
	if cleared.GitHubUsername != "" {
		t.Fatalf("username not cleared: %q", cleared.GitHubUsername)
	}
}

// The settings form generates the webhook signing secret and shows it before
// the first save; the server keeps that secret and returns it to admins.
func TestApplicationWebhookSecretFromForm(t *testing.T) {
	_, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	secret := "flow_wh_" + strings.Repeat("Ab3_", 8)
	body := map[string]any{"schemaVersion": "1.0.0", "developer": map[string]any{"name": "Acme"}, "oauth": map[string]any{"client_name": "Hooks", "redirect_uris": []string{oauthTestRedirect}}, "webhook": map[string]any{"url": "https://hooks.example.com/flow", "resourceTypes": []string{"Issue"}, "secret": secret}}
	created := authRequest[map[string]any](t, admin, "POST", host.URL+"/api/oauth-applications/manifest", body, workspace, 201)
	if created["webhookSecret"] != secret {
		t.Fatalf("created webhook secret = %v", created["webhookSecret"])
	}
	id := fmt.Sprint(created["id"])
	stored := authRequest[map[string]string](t, admin, "GET", host.URL+"/api/oauth-applications/"+id+"/webhook/secret", nil, workspace, 200)
	if stored["secret"] != secret {
		t.Fatal("stored secret differs from the form's secret")
	}
	// Once a secret exists, a different form secret does not replace it.
	body["webhook"].(map[string]any)["secret"] = "flow_wh_" + strings.Repeat("Zz9-", 8)
	authRequest[map[string]any](t, admin, "PUT", host.URL+"/api/oauth-applications/"+id+"/manifest", body, workspace, 200)
	if again := authRequest[map[string]string](t, admin, "GET", host.URL+"/api/oauth-applications/"+id+"/webhook/secret", nil, workspace, 200); again["secret"] != secret {
		t.Fatal("manifest save replaced the signing secret")
	}
	exported := authRequest[map[string]any](t, admin, "GET", host.URL+"/api/oauth-applications/"+id+"/manifest", nil, workspace, 200)
	if strings.Contains(fmt.Sprint(exported["webhook"]), "flow_wh_") {
		t.Fatal("manifest export leaked the signing secret")
	}
	body["webhook"].(map[string]any)["secret"] = "not-a-secret"
	authRequest[map[string]any](t, admin, "POST", host.URL+"/api/oauth-applications/manifest/validate", body, workspace, 200)
	if issues := authRequest[map[string]any](t, admin, "PUT", host.URL+"/api/oauth-applications/"+id+"/manifest", body, workspace, 400); !strings.Contains(fmt.Sprint(issues["issues"]), "webhook.secret") {
		t.Fatalf("invalid secret = %v", issues)
	}
	rotated := authRequest[map[string]any](t, admin, "POST", host.URL+"/api/oauth-applications/"+id+"/webhook/rotate-secret", nil, workspace, 200)
	if latest := authRequest[map[string]string](t, admin, "GET", host.URL+"/api/oauth-applications/"+id+"/webhook/secret", nil, workspace, 200); rotated["secret"] == secret || latest["secret"] != rotated["secret"] {
		t.Fatal("rotation did not replace the readable secret")
	}
	authRequest[map[string]string](t, admin, "GET", host.URL+"/api/oauth-applications/missing/webhook/secret", nil, workspace, 404)
}

// Pull requests opened by an application's GitHub username are attributed
// to its app user.
func TestGitHubWebhookAttributesApplicationGitHubUsername(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	handler := newHandler(s)
	requestJSON[domain.IntegrationConnection](t, handler, http.MethodPut, "/api/integrations/github?workspace=test-workspace", map[string]any{"name": "acme", "config": map[string]string{"organization": "acme", "webhookSecret": "secret"}}, http.StatusOK)
	app := requestJSON[domain.OAuthApplication](t, handler, http.MethodPost, "/api/oauth-applications?workspace=test-workspace", map[string]any{"name": "Coder", "githubUsername": "acme-coder[bot]"}, http.StatusCreated)
	seed := repository.Bootstrap()
	installation, err := repository.InstallApplication(t.Context(), "test-workspace", domain.ApplicationInstallation{ClientID: app.ClientID, Name: "Coder", Active: true, InstalledBy: seed.Viewer.ID, Scopes: []string{"read", "write"}, TeamIDs: []string{seed.Teams[0].ID}}, "")
	if err != nil {
		t.Fatal(err)
	}
	deliver := func(id int, login, delivery string) domain.CodeReview {
		payload := []byte(fmt.Sprintf(`{"action":"opened","number":%d,"pull_request":{"id":%d,"title":"Change %d","html_url":"https://github.com/acme/store/pull/%d","state":"open","user":{"login":%q},"base":{"ref":"main"},"head":{"ref":"x","sha":"abc"}},"sender":{"login":%q},"repository":{"full_name":"acme/store"}}`, id, 7000+id, id, id, login, login))
		mac := hmac.New(sha256.New, []byte("secret"))
		_, _ = mac.Write(payload)
		request := httptest.NewRequest(http.MethodPost, "/api/integrations/github/webhook?workspace=test-workspace", bytes.NewReader(payload))
		request.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		request.Header.Set("X-GitHub-Delivery", delivery)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("webhook = %d %s", recorder.Code, recorder.Body.String())
		}
		reviews := repository.Bootstrap().Reviews
		index := slices.IndexFunc(reviews, func(review domain.CodeReview) bool { return review.ExternalID == fmt.Sprint(7000+id) })
		if index < 0 {
			t.Fatalf("review %d missing", id)
		}
		return reviews[index]
	}
	bot := deliver(1, "Acme-Coder[bot]", "delivery-bot")
	if bot.Author.ID != installation.UserID || !bot.Author.App || bot.Events[len(bot.Events)-1].Actor.ID != installation.UserID {
		t.Fatalf("bot pull request author = %+v events = %+v", bot.Author, bot.Events)
	}
	human := deliver(2, "someone-else", "delivery-human")
	if human.Author.ID == installation.UserID {
		t.Fatal("another GitHub account was attributed to the app user")
	}
}
