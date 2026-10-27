package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/oauth2"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// These tests walk the response security headers across the surfaces a
// browser or a client really reaches (sign-in, editor, attachments, realtime,
// OAuth and MCP, integration callbacks), on normal and on error responses,
// with the Content Security Policy in report-only and in enforced mode. They
// go through newHandler, so the whole middleware chain is in play.

// surfaceKind is which Content-Security-Policy a response must carry.
type surfaceKind int

const (
	// kindAPI: the locked-down policy of API and callback responses.
	kindAPI surfaceKind = iota
	// kindApp: the web app document policy (the SPA and its static files).
	kindApp
	// kindFile: a user's file; the handler's sandbox policy plus frame-ancestors.
	kindFile
	// kindOwn: a server-rendered page with its own policy (unsubscribe).
	kindOwn
	// kindNoFrame: the web app document policy, with framing denied outright.
	kindNoFrame
)

const (
	apiPolicyText  = "default-src 'none'; base-uri 'none'; form-action 'none'"
	filePolicyText = "sandbox; default-src 'none'"
)

// surfaceEnv is a full server (auth on, web app served) in one CSP mode.
type surfaceEnv struct {
	t         *testing.T
	mode      string
	server    *httptest.Server
	static    string
	admin     *http.Client
	anon      *http.Client
	workspace string
	issueID   string
	service   *server
}

var surfaceModes = []string{appconfig.CSPReportOnly, appconfig.CSPEnforce}

func noRedirect(client *http.Client) *http.Client {
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client
}

func newSurfaceEnv(t *testing.T, mode string, configure func(*server, *appconfig.SecurityHeadersConfig)) *surfaceEnv {
	t.Helper()
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	config := appconfig.DefaultSecurityHeaders()
	config.CSP = mode
	service := &server{store: repository, uploadPath: t.TempDir(), staticPath: securityStaticDir(t), securityHeaders: &config}
	if configure != nil {
		configure(service, &config)
	}
	httpServer := httptest.NewServer(newHandler(service))
	t.Cleanup(httpServer.Close)
	env := &surfaceEnv{t: t, mode: mode, server: httpServer, static: service.staticPath, admin: noRedirect(authClient(t)), anon: noRedirect(&http.Client{}), workspace: "test-workspace", service: service}
	authRequest[domain.AuthSession](t, env.admin, http.MethodPost, httpServer.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	bootstrap := authRequest[domain.Bootstrap](t, env.admin, http.MethodGet, httpServer.URL+"/api/bootstrap", nil, env.workspace, http.StatusOK)
	if len(bootstrap.Issues) == 0 {
		t.Fatal("the test fixture has no issue")
	}
	env.issueID = bootstrap.Issues[0].ID
	return env
}

type surfaceRequest struct {
	method  string
	path    string
	header  http.Header
	body    []byte
	client  *http.Client
	timeout time.Duration
}

// do sends the request and returns the response with its body read (up to
// 64 KiB; streams are cut off by the context deadline).
func (e *surfaceEnv) do(request surfaceRequest) (*http.Response, []byte) {
	e.t.Helper()
	client := request.client
	if client == nil {
		client = e.anon
	}
	timeout := request.timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	method := request.method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, e.server.URL+request.path, bytes.NewReader(request.body))
	if err != nil {
		e.t.Fatal(err)
	}
	for name, values := range request.header {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	response, err := client.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, request.path, err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	return response, body
}

func (e *surfaceEnv) admins(method, path string, body []byte, header http.Header) (*http.Response, []byte) {
	if header == nil {
		header = http.Header{}
	}
	header.Set("X-Workspace-Key", e.workspace)
	return e.do(surfaceRequest{method: method, path: path, body: body, header: header, client: e.admin})
}

// checkHeaders asserts the security headers of one response.
func (e *surfaceEnv) checkHeaders(label string, response *http.Response, kind surfaceKind) {
	e.t.Helper()
	header := response.Header
	fail := func(format string, args ...any) {
		e.t.Helper()
		e.t.Errorf("%s [%s, %d]: "+format+"\nheaders: %v", append([]any{label, e.mode, response.StatusCode}, append(args, header)...)...)
	}
	if header.Get("X-Content-Type-Options") != "nosniff" {
		fail("X-Content-Type-Options %q", header.Get("X-Content-Type-Options"))
	}
	if header.Get("Referrer-Policy") != "strict-origin-when-cross-origin" {
		fail("Referrer-Policy %q", header.Get("Referrer-Policy"))
	}
	if permissions := header.Get("Permissions-Policy"); !strings.Contains(permissions, "camera=()") || !strings.Contains(permissions, "microphone=()") || !strings.Contains(permissions, "publickey-credentials-get=(self)") {
		fail("Permissions-Policy %q", permissions)
	}
	frameOptions, ancestors := "SAMEORIGIN", "frame-ancestors 'self'"
	if kind == kindNoFrame {
		frameOptions, ancestors = "DENY", "frame-ancestors 'none'"
	}
	if header.Get("X-Frame-Options") != frameOptions {
		fail("X-Frame-Options %q, want %q", header.Get("X-Frame-Options"), frameOptions)
	}
	enforced, reportOnly := header.Values("Content-Security-Policy"), header.Values("Content-Security-Policy-Report-Only")
	if len(enforced) != 1 {
		fail("want one enforced Content-Security-Policy, have %q", enforced)
		return
	}
	policy := ""
	switch kind {
	case kindAPI:
		policy = apiPolicyText
	case kindApp, kindNoFrame:
		policy = "default-src 'self'; script-src 'self' " + scriptHash(testIndexScript)
	case kindFile:
		policy = filePolicyText
	case kindOwn:
		policy = "default-src 'none'"
	}
	if kind == kindFile || kind == kindOwn {
		// The handler owns its policy; the server adds only frame-ancestors,
		// and never a second, report-only copy.
		if !strings.HasPrefix(enforced[0], policy) || !strings.HasSuffix(enforced[0], "; "+ancestors) || len(reportOnly) != 0 {
			fail("handler-owned policy %q (report-only %q), want %q + %q", enforced, reportOnly, policy, ancestors)
		}
		return
	}
	switch e.mode {
	case appconfig.CSPEnforce:
		if !strings.HasPrefix(enforced[0], policy) || !strings.HasSuffix(enforced[0], "; "+ancestors) || len(reportOnly) != 0 {
			fail("enforced policy %q (report-only %q), want prefix %q", enforced, reportOnly, policy)
		}
	case appconfig.CSPReportOnly:
		// frame-ancestors is enforced alone, because browsers ignore it in a report-only policy.
		if enforced[0] != ancestors || len(reportOnly) != 1 || !strings.HasPrefix(reportOnly[0], policy) || strings.Contains(reportOnly[0], "frame-ancestors") {
			fail("report-only policy %q / %q, want %q and a report-only policy starting %q", enforced, reportOnly, ancestors, policy)
		}
	}
	for _, banned := range []string{"'unsafe-eval'", "script-src 'self' 'unsafe-inline'"} {
		for _, value := range append(append([]string{}, enforced...), reportOnly...) {
			if strings.Contains(value, banned) {
				fail("policy contains %s", banned)
			}
		}
	}
}

func (e *surfaceEnv) expect(label string, response *http.Response, status int, kind surfaceKind) {
	e.t.Helper()
	if response.StatusCode != status {
		e.t.Errorf("%s [%s]: status %d, want %d", label, e.mode, response.StatusCode, status)
	}
	e.checkHeaders(label, response, kind)
}

func forEachSurfaceMode(t *testing.T, configure func(*server, *appconfig.SecurityHeadersConfig), run func(*testing.T, *surfaceEnv)) {
	t.Helper()
	for _, mode := range surfaceModes {
		t.Run(mode, func(t *testing.T) { run(t, newSurfaceEnv(t, mode, configure)) })
	}
}

func jsonHeader() http.Header { return http.Header{"Content-Type": {"application/json"}} }

// Sign-in: the SPA pages a visitor lands on, the credential endpoints with
// their 401 and 429, the OIDC redirect and callback, the passkey endpoints,
// the email link pages and the unsubscribe page.
func TestSecuritySurfaceSignIn(t *testing.T) {
	forEachSurfaceMode(t, func(s *server, _ *appconfig.SecurityHeadersConfig) {
		s.externalAuth = &externalAuth{
			appURL: "http://app.example.test",
			config: appconfig.AuthConfig{EmailEnabled: true},
			providers: map[string]*oidcClient{"oidc": {name: "Company SSO", oauth: oauth2.Config{
				ClientID: "client", RedirectURL: "http://app.example.test/api/auth/oidc/callback", Scopes: []string{"openid"},
				Endpoint: oauth2.Endpoint{AuthURL: "https://idp.example.test/authorize", TokenURL: "https://idp.example.test/token"},
			}}},
		}
	}, func(t *testing.T, e *surfaceEnv) {
		// The pages: password sign-in, sign-up, e-mail links, SSO and passkey
		// return pages, and the invite pages are all the web app document.
		for _, path := range []string{"/login", "/signup", "/verify-email?token=abc", "/forgot-password", "/reset-password?token=abc", "/auth/token?email=a%40b.c&token=abc", "/auth/email", "/auth/error?error=denied", "/auth/oidc/callback?client_key=k", "/auth/google/callback", "/invite/abc", "/join/abc", "/mobile-auth"} {
			response, body := e.do(surfaceRequest{path: path})
			e.expect("page "+path, response, http.StatusOK, kindApp)
			if !strings.Contains(response.Header.Get("Content-Type"), "text/html") || !bytes.Contains(body, []byte("<script")) {
				t.Errorf("%s: not the app document: %s", path, response.Header.Get("Content-Type"))
			}
			if response.Header.Get("Cache-Control") != "no-cache" {
				t.Errorf("%s: document cache %q", path, response.Header.Get("Cache-Control"))
			}
		}
		// Password sign-in: JSON, a wrong password is 401, a good one sets the cookie.
		response, _ := e.do(surfaceRequest{path: "/api/auth/providers"})
		e.expect("providers", response, http.StatusOK, kindAPI)
		response, _ = e.do(surfaceRequest{method: http.MethodPost, path: "/api/auth/login", header: jsonHeader(), body: []byte(`{"email":"admin@example.test","password":"wrong-password"}`)})
		e.expect("login wrong password", response, http.StatusUnauthorized, kindAPI)
		response, _ = e.do(surfaceRequest{method: http.MethodPost, path: "/api/auth/login", header: jsonHeader(), body: []byte(`{"email":"admin@example.test","password":"test-password"}`)})
		e.expect("login", response, http.StatusOK, kindAPI)
		if cookies := response.Header.Values("Set-Cookie"); len(cookies) == 0 || !strings.Contains(strings.Join(cookies, ";"), "HttpOnly") {
			t.Errorf("session cookie: %q", cookies)
		}
		// 401 on a protected route without a session, 429 once the login limiter is spent.
		response, _ = e.do(surfaceRequest{path: "/api/bootstrap"})
		e.expect("bootstrap without session", response, http.StatusUnauthorized, kindAPI)
		limited := false
		for attempt := 0; attempt < 12 && !limited; attempt++ {
			response, _ = e.do(surfaceRequest{method: http.MethodPost, path: "/api/auth/login", header: jsonHeader(), body: []byte(`{"email":"nobody@example.test","password":"wrong-password"}`)})
			limited = response.StatusCode == http.StatusTooManyRequests
		}
		if !limited {
			t.Fatal("the login limiter never answered 429")
		}
		e.expect("login rate limit", response, http.StatusTooManyRequests, kindAPI)
		if response.Header.Get("Retry-After") == "" {
			t.Error("429 without Retry-After")
		}
		// E-mail link endpoints: token-login with a bad token, magic link, verify.
		response, _ = e.do(surfaceRequest{method: http.MethodPost, path: "/api/auth/token-login", header: jsonHeader(), body: []byte(`{"email":"admin@example.test","token":"bad"}`)})
		e.expect("token-login bad token", response, http.StatusUnauthorized, kindAPI)
		response, _ = e.do(surfaceRequest{method: http.MethodPost, path: "/api/auth/verify-email", header: jsonHeader(), body: []byte(`{"token":"bad"}`)})
		if response.StatusCode < 400 {
			t.Errorf("verify-email with a bad token: %d", response.StatusCode)
		}
		e.checkHeaders("verify-email bad token", response, kindAPI)
		// SSO: the redirect to the identity provider, and callback failures.
		response, _ = e.do(surfaceRequest{path: "/api/auth/oidc/start"})
		e.expect("oidc start redirect", response, http.StatusFound, kindAPI)
		if !strings.HasPrefix(response.Header.Get("Location"), "https://idp.example.test/authorize?") {
			t.Errorf("oidc start Location %q", response.Header.Get("Location"))
		}
		response, _ = e.do(surfaceRequest{path: "/api/auth/oidc/callback?state=forged&code=x"})
		e.expect("oidc callback with a forged state", response, http.StatusFound, kindAPI)
		if !strings.Contains(response.Header.Get("Location"), "/auth/error") {
			t.Errorf("oidc callback Location %q", response.Header.Get("Location"))
		}
		response, _ = e.do(surfaceRequest{path: "/api/auth/oidc/callback?state=forged&code=x", header: http.Header{"Accept": {"application/json"}}})
		e.expect("oidc callback error as JSON", response, http.StatusBadRequest, kindAPI)
		response, _ = e.do(surfaceRequest{path: "/api/auth/google/start"})
		e.expect("SSO provider that is not configured", response, http.StatusNotFound, kindAPI)
		response, _ = e.do(surfaceRequest{path: "/api/auth/saml/start"})
		e.expect("SAML not configured", response, http.StatusNotFound, kindAPI)
		// Passkeys (WebAuthn): the ceremony endpoints need a session; the app
		// document delegates the publickey-credentials features to itself.
		response, _ = e.do(surfaceRequest{method: http.MethodPost, path: "/api/account/passkeys/register/start", header: jsonHeader(), body: []byte(`{}`)})
		e.expect("passkey registration without a session", response, http.StatusUnauthorized, kindAPI)
		response, _ = e.admins(http.MethodPost, "/api/account/passkeys/register/start", []byte(`{}`), jsonHeader())
		e.checkHeaders("passkey registration start", response, kindAPI)
		response, _ = e.admins(http.MethodPost, "/api/account/passkeys/register/finish", []byte(`{}`), jsonHeader())
		if response.StatusCode < 400 {
			t.Errorf("passkey registration finish with an empty body: %d", response.StatusCode)
		}
		e.checkHeaders("passkey registration finish error", response, kindAPI)
		response, _ = e.admins(http.MethodGet, "/api/account/passkeys", nil, nil)
		e.expect("passkey list", response, http.StatusOK, kindAPI)
		// The e-mail unsubscribe link page has its own policy.
		key, err := e.service.unsubscribeKey(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		link := "/api/email/unsubscribe?list=" + onboardingList + "&user=usr_1&sig=" + unsubscribeSignature(key, onboardingList, "usr_1")
		response, _ = e.do(surfaceRequest{path: link})
		e.expect("unsubscribe page", response, http.StatusOK, kindOwn)
		response, _ = e.do(surfaceRequest{path: "/api/email/unsubscribe?list=" + onboardingList + "&user=usr_1&sig=bad"})
		if response.StatusCode < 400 {
			t.Errorf("unsubscribe with a bad signature: %d", response.StatusCode)
		}
		e.checkHeaders("unsubscribe bad signature", response, kindOwn)
	})
}

// The editor: its document, the real-time sockets it opens and the files it embeds.
func TestSecuritySurfaceEditor(t *testing.T) {
	forEachSurfaceMode(t, func(s *server, _ *appconfig.SecurityHeadersConfig) { s.allowedOrigin = "https://flow.example.com" }, func(t *testing.T, e *surfaceEnv) {
		response, _ := e.do(surfaceRequest{path: "/test-workspace/issue/TEST-1/editor", client: e.admin})
		e.expect("issue page", response, http.StatusOK, kindApp)
		policy := response.Header.Get("Content-Security-Policy") + response.Header.Get("Content-Security-Policy-Report-Only")
		for _, want := range []string{"connect-src 'self' wss://flow.example.com", "worker-src 'self'", "img-src 'self' data: blob: https:", "media-src 'self' blob:", "frame-src blob: https://www.youtube-nocookie.com", "style-src 'self' 'unsafe-inline'", "object-src 'none'"} {
			if !strings.Contains(policy, want) {
				t.Errorf("issue page policy lacks %q: %s", want, policy)
			}
		}
		// The collaboration WebSocket: upgrades with a session, and its refusals
		// are ordinary responses with the full set.
		wsURL := "ws" + strings.TrimPrefix(e.server.URL, "http")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		connection, handshake, err := websocket.Dial(ctx, wsURL+"/api/realtime/socket?workspace=test-workspace&clientId=surface-test", &websocket.DialOptions{HTTPClient: e.admin})
		if err != nil {
			t.Fatalf("collaboration socket: %v", err)
		}
		if handshake.StatusCode != http.StatusSwitchingProtocols {
			t.Errorf("collaboration socket handshake %d", handshake.StatusCode)
		}
		_ = connection.Close(websocket.StatusNormalClosure, "")
		_, handshake, err = websocket.Dial(ctx, wsURL+"/api/realtime/socket?workspace=test-workspace&clientId=surface-test", nil)
		if err == nil {
			t.Fatal("a socket without a session was accepted")
		}
		e.expect("collaboration socket without a session", handshake, http.StatusUnauthorized, kindAPI)
		_, handshake, err = websocket.Dial(ctx, wsURL+"/api/realtime/socket?workspace=test-workspace", &websocket.DialOptions{HTTPClient: e.admin})
		if err == nil {
			t.Fatal("a socket without a client id was accepted")
		}
		e.expect("collaboration socket without a client id", handshake, http.StatusBadRequest, kindAPI)
		_, handshake, err = websocket.Dial(ctx, wsURL+"/api/realtime/socket?workspace=nope&clientId=x", &websocket.DialOptions{HTTPClient: e.admin})
		if err == nil {
			t.Fatal("a socket for an unknown workspace was accepted")
		}
		if handshake.StatusCode < 400 {
			t.Errorf("unknown workspace socket: %d", handshake.StatusCode)
		}
		e.checkHeaders("collaboration socket, unknown workspace", handshake, kindAPI)
		// A foreign page cannot open the socket with the user's cookie.
		_, handshake, err = websocket.Dial(ctx, wsURL+"/api/realtime/socket?workspace=test-workspace&clientId=x", &websocket.DialOptions{HTTPClient: e.admin, HTTPHeader: http.Header{"Origin": {"https://evil.example.net"}}})
		if err == nil {
			t.Fatal("a socket from a foreign origin was accepted")
		}
		e.expect("collaboration socket from a foreign origin", handshake, http.StatusForbidden, kindAPI)
		// A 500 from the document API: panic recovery keeps the headers.
		response, _ = e.admins(http.MethodGet, "/api/documents/does-not-exist", nil, nil)
		if response.StatusCode < 400 {
			t.Errorf("missing document: %d", response.StatusCode)
		}
		e.checkHeaders("missing document", response, kindAPI)
	})
}
