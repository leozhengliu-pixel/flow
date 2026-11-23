package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type outageRequest struct {
	method, path, body string
	headers            func(*http.Request)
}

func (q outageRequest) request(ctx context.Context) *http.Request {
	r := httptest.NewRequestWithContext(ctx, q.method, q.path, strings.NewReader(q.body))
	r.Header.Set("Content-Type", "application/json")
	if q.headers != nil {
		q.headers(r)
	}
	return r
}

func (q outageRequest) serve(ctx context.Context, handler http.Handler) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, q.request(ctx))
	return w
}

func outageResponse[T any](t *testing.T, handler http.Handler, q outageRequest, want int) T {
	t.Helper()
	w := q.serve(t.Context(), handler)
	var result T
	if w.Code != want || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatalf("fixture response: got status %d, want %d with a JSON body", w.Code, want)
	}
	return result
}

func assertDatabaseOutage(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	retry, _ := strconv.Atoi(w.Header().Get("Retry-After"))
	if w.Code != http.StatusServiceUnavailable || retry <= 0 {
		t.Fatalf("got status %d and Retry-After=%d, want retryable 503", w.Code, retry)
	}
	if w.Header().Get("Set-Cookie") != "" || w.Header().Get("WWW-Authenticate") != "" {
		t.Fatal("database outage changed the cookie or challenged the credential")
	}
	if !strings.Contains(w.Body.String(), databaseUnavailableMessage) {
		t.Fatal("response did not report a database outage")
	}
}

func openOutageStore(t *testing.T, config store.DatabaseConfig, bounded bool) *store.SQLiteStore {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_METADATA_CATALOG", "off")
	t.Setenv("FLOW_RECORD_PEER_SYNC", "off")
	config.FixtureProfile, config.FixturePassword = "test", "test-password"
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal("could not open test database")
	}
	if bounded {
		report, err := repo.PrepareBoundedMetadata(t.Context())
		_ = repo.Close()
		if err != nil || !report.Ready {
			t.Fatal("could not prepare bounded metadata")
		}
		t.Setenv("FLOW_BOUNDED_METADATA", "on")
		repo, err = store.OpenDatabase(config)
		if err != nil {
			t.Fatal("could not open bounded test database")
		}
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

func outageSession(t *testing.T, repo *store.SQLiteStore) (domain.User, string) {
	t.Helper()
	session, token, err := repo.Login(t.Context(), "admin@example.test", "test-password")
	if err != nil || token == "" {
		t.Fatal("could not create test session")
	}
	return session.User, token
}

func outageCookie(workspace, token string) func(*http.Request) {
	return func(r *http.Request) {
		r.Header.Set("X-Workspace-Key", workspace)
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	}
}

func createOutageAPIKey(t *testing.T, handler http.Handler, cookie func(*http.Request), name string) (id, secret string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"name": name, "scopes": []string{"read"}})
	created := outageResponse[struct {
		Key    domain.APIKey `json:"key"`
		Secret string        `json:"secret"`
	}](t, handler, outageRequest{"POST", "/api/api-keys", string(body), cookie}, http.StatusCreated)
	if created.Key.ID == "" || created.Secret == "" {
		t.Fatal("API key response was missing its ID or secret")
	}
	return created.Key.ID, created.Secret
}

func outageBearer(secret string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+secret) }
}

func outageOAuth(path string, form url.Values, client, secret string) outageRequest {
	return outageRequest{http.MethodPost, path, form.Encode(), func(r *http.Request) {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if secret != "" {
			r.SetBasicAuth(client, secret)
		}
	}}
}

func exerciseAuthDatabaseOutage(t *testing.T, repo *store.SQLiteStore, disconnect func()) {
	t.Helper()
	t.Setenv("FLOW_FILE_URL_SIGNING_KEY", "")
	s := &server{store: repo, uploadPath: t.TempDir(), coordinationContext: t.Context()}
	handler := newHandler(s)
	metadata, _ := repo.WorkspaceSettingsMetadata("")
	workspace := metadata.Workspace.URLKey
	user, session := outageSession(t, repo)
	cookie := outageCookie(workspace, session)
	keyID, keySecret := createOutageAPIKey(t, handler, cookie, "Outage key")
	loginToken, err := repo.RequestLoginToken(t.Context(), "admin@example.test")
	if err != nil || loginToken == "" {
		t.Fatal("could not create one-time login token")
	}
	tokenLoginBody, _ := json.Marshal(map[string]string{"email": "admin@example.test", "authToken": loginToken})
	app := outageResponse[domain.OAuthApplication](t, handler, outageRequest{"POST", "/api/oauth-applications",
		`{"name":"Outage client","scopes":["read"],"supportsClientCredentials":true,"redirectUris":["https://client.example.test/callback"]}`, cookie}, http.StatusCreated)
	clientCredentials := outageOAuth("/oauth/token", url.Values{"grant_type": {"client_credentials"}}, app.ClientID, app.ClientSecret)
	oauth := outageResponse[oauthTokenPair](t, handler, clientCredentials, http.StatusOK)

	// The PKCE cases exercise the grant store without a client-secret
	// lookup first. Both branches use real grants.
	codeExchange := func(pkce bool) outageRequest {
		t.Helper()
		request := map[string]any{"clientId": app.ClientID, "redirectUri": oauthTestRedirect,
			"responseType": "code", "scope": "read", "workspaceKey": workspace, "approve": true}
		verifier, challenge := pkcePair("p")
		if pkce {
			request["codeChallenge"], request["codeChallengeMethod"] = challenge, "S256"
		}
		input, _ := json.Marshal(request)
		decision := outageResponse[map[string]string](t, handler,
			outageRequest{"POST", "/api/oauth/authorization-request", string(input), cookie}, http.StatusOK)
		redirect, err := url.Parse(decision["redirect"])
		if err != nil || redirect.Query().Get("code") == "" {
			t.Fatal("authorization did not issue a code")
		}
		form := url.Values{"grant_type": {"authorization_code"}, "code": {redirect.Query().Get("code")}, "redirect_uri": {oauthTestRedirect}}
		if pkce {
			form.Set("client_id", app.ClientID)
			form.Set("code_verifier", verifier)
			return outageOAuth("/oauth/token", form, "", "")
		}
		return outageOAuth("/oauth/token", form, app.ClientID, app.ClientSecret)
	}
	userOAuth := outageResponse[oauthTokenPair](t, handler, codeExchange(false), http.StatusOK)
	exchange := codeExchange(false)
	pkceOAuth := outageResponse[oauthTokenPair](t, handler, codeExchange(true), http.StatusOK)
	pkceExchange := codeExchange(true)
	pkceRefresh := outageOAuth("/oauth/token", url.Values{"grant_type": {"refresh_token"},
		"refresh_token": {pkceOAuth.RefreshToken}, "client_id": {app.ClientID}}, "", "")
	refresh := func(token string) outageRequest {
		return outageOAuth("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {token}}, app.ClientID, app.ClientSecret)
	}
	rotated := outageResponse[oauthTokenPair](t, handler, refresh(userOAuth.RefreshToken), http.StatusOK)

	outageResponse[domain.WorkspaceSettings](t, handler,
		outageRequest{"PATCH", "/api/workspace/preferences", `{"scimEnabled":true}`, cookie}, http.StatusOK)
	scim, err := repo.CreateSCIMToken(t.Context(), metadata.Workspace.ID, "Outage SCIM")
	if err != nil {
		t.Fatal("could not create SCIM credential")
	}
	pipeline := outageResponse[domain.ReleasePipeline](t, handler,
		outageRequest{"POST", "/api/release-pipelines", `{"name":"Outage pipeline","type":"continuous"}`, cookie}, http.StatusCreated)
	ci := outageResponse[releasePipelineAccessKey](t, handler,
		outageRequest{"POST", "/api/release-pipelines/" + pipeline.ID + "/access-key", "", cookie}, http.StatusCreated)
	download := seedOutageSignedFile(t, s, handler, workspace, user.ID, keySecret)
	scimPath := "/scim/v2/" + workspace + "/ServiceProviderConfig"
	revoke := outageOAuth("/oauth/revoke", url.Values{"token": {rotated.AccessToken}}, app.ClientID, app.ClientSecret)
	cases := []struct {
		name    string
		request outageRequest
		healthy bool
	}{
		{"session", outageRequest{"GET", "/api/issue-records", "", cookie}, true},
		{"session-status", outageRequest{"GET", "/api/auth/session", "", cookie}, true},
		{"password-login", outageRequest{"POST", "/api/auth/login", `{"email":"admin@example.test","password":"test-password"}`, nil}, true},
		{"token-login", outageRequest{"POST", "/api/auth/token-login", string(tokenLoginBody), nil}, false},
		{"api-key", outageRequest{"GET", "/api/issue-records", "", func(r *http.Request) {
			cookie(r)
			outageBearer(keySecret)(r)
		}}, true},
		{"oauth-access-token", outageRequest{"GET", "/api/issue-records", "", outageBearer(oauth.AccessToken)}, true},
		{"oauth-confidential-client", exchange, false},
		{"oauth-refresh-token", refresh(rotated.RefreshToken), false},
		{"oauth-pkce-code", pkceExchange, false},
		{"oauth-pkce-refresh", pkceRefresh, false},
		{"oauth-client-credentials", clientCredentials, false},
		{"oauth-introspection", outageOAuth("/oauth/introspect", url.Values{"token": {oauth.AccessToken}}, app.ClientID, app.ClientSecret), true},
		{"oauth-revoke", revoke, false},
		{"mcp", outageRequest{"POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, outageBearer(oauth.AccessToken)}, true},
		{"scim", outageRequest{"GET", scimPath, "", outageBearer(scim.Secret)}, true},
		{"release-ci", outageRequest{"GET", "/api/release-ci/pipeline", "", outageBearer(ci.Secret)}, true},
		{"signed-file", outageRequest{"GET", download, "", nil}, true},
	}
	for _, tc := range cases {
		if tc.healthy {
			t.Run("healthy/"+tc.name, func(t *testing.T) {
				if w := tc.request.serve(t.Context(), handler); w.Code != http.StatusOK {
					t.Fatalf("valid credential returned status %d", w.Code)
				}
			})
		}
	}
	t.Run("healthy/actual-denials", func(t *testing.T) {
		// A valid session may not upgrade a supplied read-only bearer.
		scopeFailure := outageRequest{"POST", "/api/issue-records", `{"title":"Outside scope"}`, func(r *http.Request) {
			cookie(r)
			outageBearer(keySecret)(r)
		}}
		for _, q := range []outageRequest{
			{"GET", "/api/issue-records", "", nil},
			{"GET", "/api/release-ci/pipeline", "", outageBearer("flow_release_invalid")},
			scopeFailure,
		} {
			if w := q.serve(t.Context(), handler); w.Code != http.StatusUnauthorized || w.Header().Get("Retry-After") != "" {
				t.Fatalf("credential denial returned status %d", w.Code)
			}
		}
		revokedID, revokedSecret := createOutageAPIKey(t, handler, cookie, "Revoked key")
		if w := (outageRequest{"DELETE", "/api/api-keys/" + revokedID, "", cookie}).serve(t.Context(), handler); w.Code != http.StatusNoContent {
			t.Fatalf("could not revoke test key, got status %d", w.Code)
		}
		if w := (outageRequest{"GET", "/api/issue-records", "", outageBearer(revokedSecret)}).serve(t.Context(), handler); w.Code != http.StatusUnauthorized || w.Header().Get("Retry-After") != "" {
			t.Fatalf("revoked API key returned status %d", w.Code)
		}
		revoked, err := repo.CreateSCIMToken(t.Context(), metadata.Workspace.ID, "Revoked SCIM")
		if err != nil || repo.RevokeSCIMToken(t.Context(), metadata.Workspace.ID, revoked.ID) != nil {
			t.Fatal("could not revoke test credential")
		}
		w := (outageRequest{"GET", scimPath, "", outageBearer(revoked.Secret)}).serve(t.Context(), handler)
		if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") == "" || w.Header().Get("Retry-After") != "" {
			t.Fatalf("revoked SCIM credential returned status %d", w.Code)
		}
	})
	exerciseRealtimeDatabaseOutage(t, s, handler, workspace, session, disconnect)
	for _, tc := range cases {
		t.Run("outage/"+tc.name, func(t *testing.T) {
			assertDatabaseOutage(t, tc.request.serve(t.Context(), handler))
		})
	}
	t.Run("outage/missing-credential", func(t *testing.T) {
		w := (outageRequest{"GET", "/api/issue-records", "", nil}).serve(t.Context(), handler)
		if w.Code != http.StatusUnauthorized || w.Header().Get("Retry-After") != "" {
			t.Fatalf("missing credential returned status %d", w.Code)
		}
	})
	t.Run("outage/signed-file-configured-key", func(t *testing.T) {
		// With an environment signing key, download must still check live
		// file metadata and must not serve the stored bytes.
		const signingKey = "outage-test-signing-key"
		t.Setenv("FLOW_FILE_URL_SIGNING_KEY", signingKey)
		path := signedFileURLFor([]byte(signingKey), "", "outage.txt", time.Now().Add(time.Hour),
			fileSigner{Subject: user.ID, Workspace: workspace, KeyID: keyID})
		assertDatabaseOutage(t, (outageRequest{"GET", path, "", nil}).serve(t.Context(), handler))
	})
}

func seedOutageSignedFile(t *testing.T, s *server, handler http.Handler, workspace, userID, apiKey string) string {
	t.Helper()
	const name, content = "outage.txt", "protected bytes"
	now := time.Now().UTC()
	upload := store.FileUpload{ID: "outage-upload", WorkspaceKey: workspace, UserID: userID, ObjectKey: name,
		Filename: name, ContentType: "text/plain", ExpectedSize: int64(len(content)), Purpose: "asset", ExpiresAt: now.Add(time.Hour)}
	if err := s.store.CreateFileUpload(t.Context(), upload, store.FileUploadQuota{}, now); err != nil {
		t.Fatal("could not prepare file")
	}
	if _, err := s.store.BeginFileUpload(t.Context(), upload.ID, now); err != nil {
		t.Fatal("could not begin file upload")
	}
	if err := os.WriteFile(filepath.Join(s.uploadPath, name), []byte(content), 0600); err != nil {
		t.Fatal("could not write file")
	}
	if err := s.store.FinishFileUpload(t.Context(), upload.ID, true, int64(len(content)), now); err != nil {
		t.Fatal("could not finish file upload")
	}
	signed := outageResponse[struct{ Files []signedFile }](t, handler,
		outageRequest{"GET", "/api/files/sign?url=/uploads/" + name, "", outageBearer(apiKey)}, http.StatusOK)
	if len(signed.Files) != 1 {
		t.Fatal("file was not signed")
	}
	target, err := url.Parse(signed.Files[0].SignedURL)
	if err != nil {
		t.Fatal("signed URL is invalid")
	}
	return target.RequestURI()
}

func TestAuthDatabaseOutageSQLite(t *testing.T) {
	for _, bounded := range []bool{false, true} {
		t.Run("bounded="+strconv.FormatBool(bounded), func(t *testing.T) {
			repo := openOutageStore(t, store.DatabaseConfig{Driver: "sqlite", Path: filepath.Join(t.TempDir(), "auth.db")}, bounded)
			exerciseAuthDatabaseOutage(t, repo, func() {
				if err := repo.CloseDatabasePoolForTesting(); err != nil {
					t.Fatal("could not close test database")
				}
			})
		})
	}
}

func TestAuthDatabaseOutageDeadlineAndExpiredSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.db")
	repo := openOutageStore(t, store.DatabaseConfig{Driver: "sqlite", Path: path}, false)
	s := &server{store: repo}
	_, token := outageSession(t, repo)
	route := outageRequest{"GET", "/api/auth/session", "", outageCookie("", token)}
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	handler := http.HandlerFunc(s.authSession)
	assertDatabaseOutage(t, route.serve(ctx, handler))

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal("could not open session fixture")
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `UPDATE auth_sessions SET expires_at=? WHERE token_hash=?`,
		time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), secretHash(token)); err != nil {
		t.Fatal("could not expire test session")
	}
	w := route.serve(t.Context(), handler)
	if w.Code != http.StatusUnauthorized || w.Header().Get("Set-Cookie") == "" || w.Header().Get("Retry-After") != "" {
		t.Fatalf("expired session returned status %d", w.Code)
	}
}
