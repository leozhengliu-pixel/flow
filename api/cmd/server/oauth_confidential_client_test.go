package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

type oauthTokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	Actor        string `json:"actor"`
	Error        string `json:"error"`
}

const oauthTestRedirect = "https://client.example.test/callback"

// oauthForm posts a token-endpoint form, optionally with HTTP Basic client
// authentication, and decodes the JSON response.
func oauthForm[T any](t *testing.T, endpoint string, form url.Values, basicID, basicSecret string, status int) T {
	t.Helper()
	request, _ := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if basicID != "" {
		request.SetBasicAuth(basicID, basicSecret)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != status {
		t.Fatalf("POST %s = %d, want %d: %s", endpoint, response.StatusCode, status, raw)
	}
	var result T
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
	}
	return result
}

func pkcePair(seed string) (string, string) {
	verifier := strings.Repeat(seed, 48)[:48]
	digest := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(digest[:])
}

func createConfidentialApp(t *testing.T, host *httptest.Server, admin *http.Client, workspace string, scopes []string) domain.OAuthApplication {
	t.Helper()
	app := authRequest[domain.OAuthApplication](t, admin, "POST", host.URL+"/api/oauth-applications", map[string]any{"name": "Acme Sync", "redirectUris": []string{oauthTestRedirect}, "scopes": scopes, "logoUrl": "https://client.example.test/icon.png"}, workspace, 201)
	if app.ClientSecret == "" {
		t.Fatal("application has no client secret")
	}
	return app
}

// authorizeCode approves an authorization request as the admin and returns
// the code from the redirect (state must round-trip).
func authorizeCode(t *testing.T, host *httptest.Server, admin *http.Client, workspace, clientID, scope, challenge string) string {
	t.Helper()
	body := map[string]any{"clientId": clientID, "redirectUri": oauthTestRedirect, "responseType": "code", "scope": scope, "state": "state-123", "workspaceKey": workspace, "approve": true}
	if challenge != "" {
		body["codeChallenge"], body["codeChallengeMethod"] = challenge, "S256"
	}
	decision := authRequest[map[string]string](t, admin, "POST", host.URL+"/api/oauth/authorization-request", body, "", 200)
	redirect, err := url.Parse(decision["redirect"])
	if err != nil || redirect.Query().Get("code") == "" || redirect.Query().Get("state") != "state-123" {
		t.Fatalf("authorization redirect = %v (%v)", decision, err)
	}
	return redirect.Query().Get("code")
}

func TestOAuthConfidentialClientSecretVerification(t *testing.T) {
	_, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	app := createConfidentialApp(t, host, admin, workspace, []string{"read", "write"})
	token := host.URL + "/oauth/token"

	// client_secret_post without PKCE.
	code := authorizeCode(t, host, admin, workspace, app.ClientID, "read,write", "")
	exchange := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {oauthTestRedirect}, "client_id": {app.ClientID}}
	// Without PKCE a confidential client must authenticate; the failed
	// attempt does not consume the code.
	if denied := oauthForm[oauthTokenPair](t, token, exchange, "", "", 400); denied.Error != "invalid_grant" {
		t.Fatalf("secret-less exchange = %+v", denied)
	}
	wrong := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {oauthTestRedirect}, "client_id": {app.ClientID}, "client_secret": {"wrong"}}
	if denied := oauthForm[oauthTokenPair](t, token, wrong, "", "", 401); denied.Error != "invalid_client" {
		t.Fatalf("wrong secret = %+v", denied)
	}
	exchange.Set("client_secret", app.ClientSecret)
	pair := oauthForm[oauthTokenPair](t, token, exchange, "", "", 200)
	if pair.AccessToken == "" || pair.RefreshToken == "" || pair.Actor != "user" || pair.Scope != "read write" {
		t.Fatalf("client_secret_post tokens = %+v", pair)
	}

	// client_secret_basic, and both methods at once is rejected.
	code = authorizeCode(t, host, admin, workspace, app.ClientID, "read", "")
	basic := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {oauthTestRedirect}}
	oauthForm[oauthTokenPair](t, token, basic, app.ClientID, "nope", 401)
	both := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {oauthTestRedirect}, "client_secret": {app.ClientSecret}}
	if denied := oauthForm[oauthTokenPair](t, token, both, app.ClientID, app.ClientSecret, 400); denied.Error != "invalid_request" {
		t.Fatalf("two authentication methods = %+v", denied)
	}
	basicPair := oauthForm[oauthTokenPair](t, token, basic, app.ClientID, app.ClientSecret, 200)
	if basicPair.AccessToken == "" {
		t.Fatal("client_secret_basic exchange failed")
	}

	// Secret-only tokens need the secret again to refresh (Basic works).
	refresh := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {pair.RefreshToken}, "client_id": {app.ClientID}}
	if denied := oauthForm[oauthTokenPair](t, token, refresh, "", "", 400); denied.Error != "invalid_grant" {
		t.Fatalf("secret-less refresh = %+v", denied)
	}
	refreshed := oauthForm[oauthTokenPair](t, token, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {pair.RefreshToken}}, app.ClientID, app.ClientSecret, 200)
	if refreshed.RefreshToken == "" || refreshed.RefreshToken == pair.RefreshToken {
		t.Fatalf("refresh did not rotate: %+v", refreshed)
	}
}

func TestOAuthPKCERequiredForPublicOptionalForConfidential(t *testing.T) {
	_, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	app := createConfidentialApp(t, host, admin, workspace, []string{"read", "write"})
	public := authRequest[domain.OAuthClient](t, admin, "POST", host.URL+"/oauth/register", map[string]any{"client_name": "Public CLI", "redirect_uris": []string{"http://127.0.0.1:5000/callback"}}, "", 201)

	query := url.Values{"response_type": {"code"}, "client_id": {public.ClientID}, "redirect_uri": {"http://127.0.0.1:5000/callback"}, "scope": {"read"}}
	if body := authRequest[map[string]any](t, admin, "GET", host.URL+"/api/oauth/authorization-request?"+query.Encode(), nil, "", 400); !strings.Contains(body["error_description"].(string), "PKCE") {
		t.Fatalf("public client without PKCE = %v", body)
	}
	query.Set("code_challenge", "short")
	query.Set("code_challenge_method", "plain")
	authRequest[map[string]any](t, admin, "GET", host.URL+"/api/oauth/authorization-request?"+query.Encode(), nil, "", 400)

	confidential := url.Values{"response_type": {"code"}, "client_id": {app.ClientID}, "redirect_uri": {oauthTestRedirect}, "scope": {"read"}}
	details := authRequest[map[string]any](t, admin, "GET", host.URL+"/api/oauth/authorization-request?"+confidential.Encode(), nil, "", 200)
	if details["client"].(map[string]any)["token_endpoint_auth_method"] != "client_secret_basic" {
		t.Fatalf("registered application is not confidential: %v", details["client"])
	}

	// A confidential client using PKCE needs no secret, and its tokens
	// refresh with client_id alone (the reference app's PKCE behavior).
	verifier, challenge := pkcePair("p")
	code := authorizeCode(t, host, admin, workspace, app.ClientID, "read", challenge)
	token := host.URL + "/oauth/token"
	exchange := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {oauthTestRedirect}, "client_id": {app.ClientID}, "code_verifier": {"wrong-verifier-wrong-verifier-wrong-verifier-xx"}}
	oauthForm[oauthTokenPair](t, token, exchange, "", "", 400)
	exchange.Set("code_verifier", verifier)
	pair := oauthForm[oauthTokenPair](t, token, exchange, "", "", 200)
	oauthForm[oauthTokenPair](t, token, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {pair.RefreshToken}, "client_id": {app.ClientID}}, "", "", 200)

	// A public client cannot present a secret.
	oauthForm[oauthTokenPair](t, token, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"x"}, "client_id": {public.ClientID}, "client_secret": {"guess"}}, "", "", 401)
}

func TestOAuthRefreshRotationGraceReplay(t *testing.T) {
	_, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	app := createConfidentialApp(t, host, admin, workspace, []string{"read"})
	verifier, challenge := pkcePair("g")
	code := authorizeCode(t, host, admin, workspace, app.ClientID, "read", challenge)
	token := host.URL + "/oauth/token"
	first := oauthForm[oauthTokenPair](t, token, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {oauthTestRedirect}, "client_id": {app.ClientID}, "code_verifier": {verifier}}, "", "", 200)
	refresh := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {first.RefreshToken}, "client_id": {app.ClientID}}
	second := oauthForm[oauthTokenPair](t, token, refresh, "", "", 200)
	// The client lost the response and retries: same successor, new access token.
	replay := oauthForm[oauthTokenPair](t, token, refresh, "", "", 200)
	if replay.RefreshToken != second.RefreshToken || replay.AccessToken == second.AccessToken || replay.AccessToken == "" {
		t.Fatalf("replay = %+v, original = %+v", replay, second)
	}
	applicationBearerRequest(t, host, replay.AccessToken, "GET", "/api/viewer", "", 200)
	// Once the successor is used the old token is dead.
	oauthForm[oauthTokenPair](t, token, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {second.RefreshToken}, "client_id": {app.ClientID}}, "", "", 200)
	if denied := oauthForm[oauthTokenPair](t, token, refresh, "", "", 400); denied.Error != "invalid_grant" {
		t.Fatalf("replay after successor use = %+v", denied)
	}
	// Another client cannot replay.
	oauthForm[oauthTokenPair](t, token, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {first.RefreshToken}, "client_id": {"someone-else"}}, "", "", 400)
}

func TestOAuthTokenRevocationRFC7009(t *testing.T) {
	_, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	app := createConfidentialApp(t, host, admin, workspace, []string{"read"})
	other := createConfidentialApp(t, host, admin, workspace, []string{"read"})
	issue := func() oauthTokenPair {
		code := authorizeCode(t, host, admin, workspace, app.ClientID, "read", "")
		return oauthForm[oauthTokenPair](t, host.URL+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {oauthTestRedirect}}, app.ClientID, app.ClientSecret, 200)
	}
	revoke := host.URL + "/oauth/revoke"
	pair := issue()

	// Mixing token with legacy fields is rejected; unknown tokens are 200.
	oauthForm[map[string]string](t, revoke, url.Values{"token": {pair.AccessToken}, "access_token": {pair.AccessToken}}, "", "", 400)
	oauthForm[map[string]string](t, revoke, url.Values{"token": {"flow_oauth_unknown"}}, "", "", 200)
	oauthForm[map[string]string](t, revoke, url.Values{"token": {pair.AccessToken}, "token_type_hint": {"bogus"}}, "", "", 400)
	// Wrong client credentials, and another client's valid credentials.
	oauthForm[map[string]string](t, revoke, url.Values{"token": {pair.AccessToken}}, app.ClientID, "wrong", 401)
	oauthForm[map[string]string](t, revoke, url.Values{"token": {pair.AccessToken}}, other.ClientID, other.ClientSecret, 401)
	applicationBearerRequest(t, host, pair.AccessToken, "GET", "/api/viewer", "", 200)

	// Access token revoked with its client's credentials.
	oauthForm[map[string]string](t, revoke, url.Values{"token": {pair.AccessToken}, "token_type_hint": {"access_token"}}, app.ClientID, app.ClientSecret, 200)
	applicationBearerRequest(t, host, pair.AccessToken, "GET", "/api/viewer", "", 401)

	// Refresh token revoked: it can neither refresh nor be replayed.
	oauthForm[map[string]string](t, revoke, url.Values{"token": {pair.RefreshToken}, "token_type_hint": {"refresh_token"}, "client_id": {app.ClientID}}, "", "", 200)
	oauthForm[oauthTokenPair](t, host.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {pair.RefreshToken}}, app.ClientID, app.ClientSecret, 400)

	// A consumed refresh token revoked inside the grace window stops replays.
	next := issue()
	refreshForm := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {next.RefreshToken}}
	rotated := oauthForm[oauthTokenPair](t, host.URL+"/oauth/token", refreshForm, app.ClientID, app.ClientSecret, 200)
	sibling := issue()
	oauthForm[map[string]string](t, revoke, url.Values{"refresh_token": {next.RefreshToken}}, "", "", 200)
	oauthForm[oauthTokenPair](t, host.URL+"/oauth/token", refreshForm, app.ClientID, app.ClientSecret, 400)
	// The access tokens issued from that refresh chain are revoked with it;
	// another session of the same application keeps working.
	applicationBearerRequest(t, host, next.AccessToken, "GET", "/api/viewer", "", 401)
	applicationBearerRequest(t, host, rotated.AccessToken, "GET", "/api/viewer", "", 401)
	applicationBearerRequest(t, host, sibling.AccessToken, "GET", "/api/viewer", "", 200)
	oauthForm[oauthTokenPair](t, host.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rotated.RefreshToken}}, app.ClientID, app.ClientSecret, 400)
	oauthForm[oauthTokenPair](t, host.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {sibling.RefreshToken}}, app.ClientID, app.ClientSecret, 200)

	// Legacy: the token in the Authorization header revokes itself.
	legacy := issue()
	request, _ := http.NewRequest("POST", revoke, nil)
	request.Header.Set("Authorization", "Bearer "+legacy.AccessToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("legacy revoke = %v %v", response, err)
	}
	response.Body.Close()
	applicationBearerRequest(t, host, legacy.AccessToken, "GET", "/api/viewer", "", 401)
}

func TestOAuthPromptConsentAndPreviousGrant(t *testing.T) {
	_, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	app := createConfidentialApp(t, host, admin, workspace, []string{"read", "write", "create_issues"})
	query := url.Values{"response_type": {"code"}, "client_id": {app.ClientID}, "redirect_uri": {oauthTestRedirect}, "scope": {"read write"}, "state": {"abc"}}
	request := func(values url.Values) map[string]any {
		return authRequest[map[string]any](t, admin, "GET", host.URL+"/api/oauth/authorization-request?"+values.Encode(), nil, "", 200)
	}
	if got := request(query)["previouslyAuthorizedWorkspaceKey"]; got != "" {
		t.Fatalf("first request skipped consent: %v", got)
	}
	authorizeCode(t, host, admin, workspace, app.ClientID, "read write", "")
	if got := request(query)["previouslyAuthorizedWorkspaceKey"]; got != workspace {
		t.Fatalf("previous grant not detected: %v", got)
	}
	// Broader scopes need consent again.
	broader := url.Values{"response_type": {"code"}, "client_id": {app.ClientID}, "redirect_uri": {oauthTestRedirect}, "scope": {"read write"}}
	broader.Set("scope", "read write issues:create")
	if got := authRequest[map[string]any](t, admin, "GET", host.URL+"/api/oauth/authorization-request?"+broader.Encode(), nil, "", 200)["previouslyAuthorizedWorkspaceKey"]; got != "" {
		t.Fatalf("broader scopes skipped consent: %v", got)
	}
	consent := url.Values{}
	for key, values := range query {
		consent[key] = values
	}
	consent.Set("prompt", "consent")
	details := request(consent)
	if details["previouslyAuthorizedWorkspaceKey"] != "" || details["prompt"] != "consent" {
		t.Fatalf("prompt=consent did not force consent: %v", details)
	}
	consent.Set("prompt", "none")
	authRequest[map[string]any](t, admin, "GET", host.URL+"/api/oauth/authorization-request?"+consent.Encode(), nil, "", 400)
}

func TestOAuthScopeValidation(t *testing.T) {
	_, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	app := createConfidentialApp(t, host, admin, workspace, []string{"read", "write", "create_issues", "admin", "app:assignable"})
	if !slices.Contains(app.Scopes, "admin") {
		t.Fatalf("app config dropped admin: %v", app.Scopes)
	}
	base := url.Values{"response_type": {"code"}, "client_id": {app.ClientID}, "redirect_uri": {oauthTestRedirect}}
	check := func(scope, actor string, status int, code string) map[string]any {
		t.Helper()
		values := url.Values{}
		for key, value := range base {
			values[key] = value
		}
		values.Set("scope", scope)
		if actor != "" {
			values.Set("actor", actor)
		}
		body := authRequest[map[string]any](t, admin, "GET", host.URL+"/api/oauth/authorization-request?"+values.Encode(), nil, "", status)
		if code != "" && body["error"] != code {
			t.Fatalf("scope %q actor %q: %v", scope, actor, body)
		}
		return body
	}
	check("read,admin", "app", 400, "invalid_scope")
	check("read,bogus", "", 400, "invalid_scope")
	granted := check("write,issues:create,admin", "", 200, "")
	scopes := granted["scopes"].([]any)
	if !slices.Contains(scopes, any("read")) || !slices.Contains(scopes, any("create_issues")) || !slices.Contains(scopes, any("admin")) {
		t.Fatalf("user scopes = %v", scopes)
	}
	// admin needs a registered application configured with it.
	plain := createConfidentialApp(t, host, admin, workspace, []string{"read", "write"})
	base.Set("client_id", plain.ClientID)
	check("read,admin", "", 400, "invalid_scope")

	// Client credentials can never carry admin; default tokens drop it.
	authRequest[domain.OAuthApplication](t, admin, "PATCH", host.URL+"/api/oauth-applications/"+app.ID, map[string]bool{"supportsClientCredentials": true}, workspace, 200)
	applicationFormToken(t, host, app, "read admin", 400)
	machine := applicationFormToken(t, host, app, "", 200)
	if strings.Contains(machine.Scope, "admin") || machine.Actor != "app" {
		t.Fatalf("client credentials token = %+v", machine)
	}

	// An authorized admin-scope user token is usable and reports its scopes.
	code := authorizeCode(t, host, admin, workspace, app.ClientID, "read admin", "")
	pair := oauthForm[oauthTokenPair](t, host.URL+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {oauthTestRedirect}}, app.ClientID, app.ClientSecret, 200)
	// Like the reference app, the scope is reported sorted.
	if pair.Scope != "admin read" {
		t.Fatalf("admin token scope = %q", pair.Scope)
	}
}

func TestOAuthViewerAndIntrospection(t *testing.T) {
	_, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	app := createConfidentialApp(t, host, admin, workspace, []string{"read", "write"})
	code := authorizeCode(t, host, admin, workspace, app.ClientID, "read", "")
	pair := oauthForm[oauthTokenPair](t, host.URL+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {oauthTestRedirect}}, app.ClientID, app.ClientSecret, 200)
	var viewer struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		Actor          string   `json:"actor"`
		Authentication string   `json:"authentication"`
		Scopes         []string `json:"scopes"`
		Application    struct {
			ClientID string `json:"clientId"`
		} `json:"application"`
	}
	if err := json.Unmarshal(applicationBearerRequest(t, host, pair.AccessToken, "GET", "/api/viewer", "", 200), &viewer); err != nil {
		t.Fatal(err)
	}
	if viewer.User.ID != data.Viewer.ID || viewer.Actor != "user" || viewer.Authentication != "oauth" || viewer.Application.ClientID != app.ClientID || !slices.Equal(viewer.Scopes, []string{"read"}) {
		t.Fatalf("viewer = %+v", viewer)
	}

	introspect := host.URL + "/oauth/introspect"
	active := oauthForm[map[string]any](t, introspect, url.Values{"token": {pair.AccessToken}}, app.ClientID, app.ClientSecret, 200)
	if active["active"] != true || active["actor"] != "user" || active["client_id"] != app.ClientID || active["sub"] != data.Viewer.ID {
		t.Fatalf("introspection = %v", active)
	}
	refresh := oauthForm[map[string]any](t, introspect, url.Values{"token": {pair.RefreshToken}, "token_type_hint": {"refresh_token"}}, app.ClientID, app.ClientSecret, 200)
	if refresh["active"] != true || refresh["token_type"] != "refresh_token" {
		t.Fatalf("refresh introspection = %v", refresh)
	}
	other := createConfidentialApp(t, host, admin, workspace, []string{"read"})
	if hidden := oauthForm[map[string]any](t, introspect, url.Values{"token": {pair.AccessToken}}, other.ClientID, other.ClientSecret, 200); hidden["active"] != false {
		t.Fatalf("another client introspected the token: %v", hidden)
	}
	oauthForm[map[string]any](t, introspect, url.Values{"token": {pair.AccessToken}, "client_id": {app.ClientID}}, "", "", 401)

	// App actor tokens report actor=app.
	authRequest[domain.OAuthApplication](t, admin, "PATCH", host.URL+"/api/oauth-applications/"+app.ID, map[string]bool{"supportsClientCredentials": true}, workspace, 200)
	machine := applicationFormToken(t, host, app, "read", 200)
	var appViewer map[string]any
	_ = json.Unmarshal(applicationBearerRequest(t, host, machine.Token, "GET", "/api/viewer", "", 200), &appViewer)
	if appViewer["actor"] != "app" || appViewer["user"].(map[string]any)["id"] != machine.Subject {
		t.Fatalf("app viewer = %v", appViewer)
	}
}
