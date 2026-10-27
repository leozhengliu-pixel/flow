package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	appconfig "flow/api/internal/config"
	"flow/api/internal/store"
)

// Realtime: the server-sent event streams and the change feed. The headers
// are on the stream's response, which is written before the first event.
func TestSecuritySurfaceRealtime(t *testing.T) {
	forEachSurfaceMode(t, nil, func(t *testing.T, e *surfaceEnv) {
		for _, path := range []string{"/api/realtime/events?workspace=test-workspace"} {
			response, _ := e.do(surfaceRequest{path: path, client: e.admin, header: http.Header{"X-Workspace-Key": {e.workspace}}, timeout: 800 * time.Millisecond})
			e.expect("event stream "+path, response, http.StatusOK, kindAPI)
			if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") || !strings.Contains(response.Header.Get("Cache-Control"), "no-cache") {
				t.Errorf("%s: stream headers %v", path, response.Header)
			}
			if response.Header.Get("Content-Encoding") != "" {
				t.Errorf("%s: a stream must not be compressed: %q", path, response.Header.Get("Content-Encoding"))
			}
			response, _ = e.do(surfaceRequest{path: path, timeout: time.Second})
			e.expect("event stream without a session "+path, response, http.StatusUnauthorized, kindAPI)
		}
		response, _ := e.do(surfaceRequest{path: "/api/realtime/events?workspace=missing", client: e.admin, header: http.Header{"X-Workspace-Key": {"missing"}}, timeout: time.Second})
		if response.StatusCode < 400 {
			t.Errorf("event stream of an unknown workspace: %d", response.StatusCode)
		}
		e.checkHeaders("event stream of an unknown workspace", response, kindAPI)
		response, _ = e.admins(http.MethodGet, "/api/realtime/changes?since=0", nil, nil)
		e.expect("change feed", response, http.StatusOK, kindAPI)
		response, _ = e.admins(http.MethodGet, "/api/realtime/changes?since=not-a-cursor", nil, nil)
		e.checkHeaders("change feed with a bad cursor", response, kindAPI)
		response, _ = e.do(surfaceRequest{path: "/api/realtime/changes?since=0"})
		e.expect("change feed without a session", response, http.StatusUnauthorized, kindAPI)
		response, _ = e.admins(http.MethodPost, "/api/realtime/presence", []byte(`{"issueId":"`+e.issueID+`"}`), jsonHeader())
		e.checkHeaders("presence", response, kindAPI)
	})
}

// Behind a gateway that serves Flow under another public host (FLOW_APP_URL
// differs from the Host header, TLS ends at the proxy): the document's policy
// names the public host for sockets, the stream and socket still answer, and
// the socket's own origin check accepts the browser's Origin.
func TestSecuritySurfaceRealtimeBehindPublicHost(t *testing.T) {
	for _, mode := range surfaceModes {
		t.Run(mode, func(t *testing.T) {
			repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer repository.Close()
			config := appconfig.DefaultSecurityHeaders()
			config.CSP, config.TrustProxyHeaders = mode, true
			service := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true, staticPath: securityStaticDir(t), allowedOrigin: "https://flow.example.com", securityHeaders: &config}
			upstream := httptest.NewServer(newHandler(service))
			defer upstream.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// The document: sockets and streams are allowed to the public host.
			request, _ := http.NewRequest(http.MethodGet, upstream.URL+"/test-workspace/issue/TEST-1", nil)
			request.Host = "flow.example.com"
			request.Header.Set("X-Forwarded-Proto", "https")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			policy := response.Header.Get("Content-Security-Policy") + response.Header.Get("Content-Security-Policy-Report-Only")
			if !strings.Contains(policy, "connect-src 'self' wss://flow.example.com") || response.Header.Get("Strict-Transport-Security") != "max-age=31536000" {
				t.Errorf("document behind the gateway: %v", response.Header)
			}
			// The socket as the browser opens it: the gateway passes the public Host through.
			dial := func(host, origin string) (*http.Response, error) {
				header := http.Header{"Host": {host}, "Origin": {origin}, "X-Forwarded-Proto": {"https"}, "X-Forwarded-Host": {host}}
				connection, handshake, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(upstream.URL, "http")+"/api/realtime/socket?workspace=test-workspace&clientId=gateway-test", &websocket.DialOptions{HTTPHeader: header, Host: host})
				if err == nil {
					_ = connection.Close(websocket.StatusNormalClosure, "")
				}
				return handshake, err
			}
			if handshake, err := dial("flow.example.com", "https://flow.example.com"); err != nil {
				t.Errorf("socket with the public Host and Origin: %v (%v)", err, handshake)
			}
			// A gateway that rewrites Host to the upstream still forwards the public host.
			if handshake, err := dial(strings.TrimPrefix(upstream.URL, "http://"), "https://flow.example.com"); err != nil {
				t.Errorf("socket through a gateway that rewrites Host: %v (%v)", err, handshake)
			}
			if _, err := dial("flow.example.com", "https://evil.example.net"); err == nil {
				t.Error("a socket from a foreign origin was accepted")
			}
			// The stream.
			stream, _ := http.NewRequestWithContext(ctx, http.MethodGet, upstream.URL+"/api/realtime/events?workspace=test-workspace", nil)
			stream.Host = "flow.example.com"
			stream.Header.Set("X-Forwarded-Proto", "https")
			streamResponse, err := http.DefaultClient.Do(stream)
			if err != nil {
				t.Fatal(err)
			}
			_ = streamResponse.Body.Close()
			if streamResponse.StatusCode != http.StatusOK || streamResponse.Header.Get("X-Content-Type-Options") != "nosniff" || streamResponse.Header.Get("Strict-Transport-Security") == "" {
				t.Errorf("stream behind the gateway: %d %v", streamResponse.StatusCode, streamResponse.Header)
			}
		})
	}
}

func (e *surfaceEnv) post(path string, header http.Header, body string) (*http.Response, []byte) {
	return e.do(surfaceRequest{method: http.MethodPost, path: path, header: header, body: []byte(body)})
}

func formHeader() http.Header {
	return http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}
}

// The OAuth authorization server and the MCP endpoints: discovery documents,
// registration, the consent page and its API, the token endpoints and MCP
// itself. JSON responses keep nosniff and the API policy; the consent page
// cannot be framed by anyone, whatever the framing allow list says.
func TestSecuritySurfaceOAuthServerAndMCP(t *testing.T) {
	forEachSurfaceMode(t, nil, func(t *testing.T, e *surfaceEnv) {
		apiResponse := func(label string, response *http.Response, status int) {
			t.Helper()
			e.expect(label, response, status, kindAPI)
		}
		for _, path := range []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-authorization-server?actor=app", "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource/mcp/readonly"} {
			response, body := e.do(surfaceRequest{path: path})
			apiResponse("discovery "+path, response, http.StatusOK)
			if !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") || !json.Valid(body) {
				t.Errorf("%s: %s %s", path, response.Header.Get("Content-Type"), body)
			}
			if got := response.Header.Get("Content-Security-Policy") + response.Header.Get("Content-Security-Policy-Report-Only"); !strings.Contains(got, "default-src 'none'") {
				t.Errorf("%s: JSON served under %q", path, got)
			}
		}
		response, _ := e.do(surfaceRequest{path: "/.well-known/oauth-authorization-server", method: http.MethodPost})
		apiResponse("discovery POST", response, http.StatusMethodNotAllowed)
		response, _ = e.do(surfaceRequest{path: "/.well-known/oauth-unknown"})
		apiResponse("unknown well-known document", response, http.StatusNotFound)
		if !strings.Contains(response.Header.Get("Content-Security-Policy")+response.Header.Get("Content-Security-Policy-Report-Only"), "default-src 'none'") {
			t.Errorf("unknown well-known document is not an API response: %v", response.Header)
		}

		// Registration: created, then refused.
		response, body := e.post("/oauth/register", jsonHeader(), `{"client_name":"Headers test","redirect_uris":["http://127.0.0.1:43119/callback"],"token_endpoint_auth_method":"none"}`)
		apiResponse("register client", response, http.StatusCreated)
		var registered struct {
			ClientID string `json:"client_id"`
		}
		if err := json.Unmarshal(body, &registered); err != nil || registered.ClientID == "" {
			t.Fatalf("registered client: %v %s", err, body)
		}
		response, _ = e.post("/oauth/register", jsonHeader(), `{"redirect_uris":["javascript:alert(1)"]}`)
		apiResponse("register client with a bad redirect", response, http.StatusBadRequest)

		// The consent page itself is the web app document with framing denied outright.
		redirectURI := "http://127.0.0.1:43119/callback"
		verifier := strings.Repeat("v", 48)
		digest := sha256.Sum256([]byte(verifier))
		challenge := base64.RawURLEncoding.EncodeToString(digest[:])
		query := url.Values{"response_type": {"code"}, "client_id": {registered.ClientID}, "redirect_uri": {redirectURI}, "scope": {"read"}, "state": {"s"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
		response, _ = e.do(surfaceRequest{path: "/oauth/authorize?" + query.Encode()})
		e.expect("consent page", response, http.StatusOK, kindNoFrame)
		response, _ = e.do(surfaceRequest{path: "/oauth/authorize"})
		e.expect("consent page without a request", response, http.StatusOK, kindNoFrame)
		response, _ = e.do(surfaceRequest{path: "/oauth/authorize", method: http.MethodHead})
		e.expect("consent page, HEAD", response, http.StatusOK, kindNoFrame)
		// Its API: the details, the decision, and the refusals.
		response, _ = e.do(surfaceRequest{path: "/api/oauth/authorization-request?" + query.Encode()})
		apiResponse("authorization request without a session", response, http.StatusUnauthorized)
		response, body = e.admins(http.MethodGet, "/api/oauth/authorization-request?"+query.Encode(), nil, nil)
		apiResponse("authorization request details", response, http.StatusOK)
		response, _ = e.admins(http.MethodGet, "/api/oauth/authorization-request?client_id=nope&redirect_uri=http://127.0.0.1/x&response_type=code", nil, nil)
		if response.StatusCode < 400 {
			t.Errorf("authorization request of an unknown client: %d", response.StatusCode)
		}
		apiResponse("authorization request of an unknown client", response, response.StatusCode)
		decision := `{"clientId":"` + registered.ClientID + `","redirectUri":"` + redirectURI + `","responseType":"code","scope":"read","state":"s","codeChallenge":"` + challenge + `","codeChallengeMethod":"S256","workspaceKey":"test-workspace","approve":true}`
		response, body = e.admins(http.MethodPost, "/api/oauth/authorization-request", []byte(decision), jsonHeader())
		apiResponse("authorization decision", response, http.StatusOK)
		var approved struct {
			Redirect string `json:"redirect"`
		}
		if err := json.Unmarshal(body, &approved); err != nil {
			t.Fatal(err)
		}
		redirect, err := url.Parse(approved.Redirect)
		if err != nil || redirect.Query().Get("code") == "" {
			t.Fatalf("authorization redirect %q: %v", approved.Redirect, err)
		}

		// Tokens: exchange, a refused exchange, revocation, introspection.
		response, body = e.post("/oauth/token", formHeader(), url.Values{"grant_type": {"authorization_code"}, "code": {redirect.Query().Get("code")}, "client_id": {registered.ClientID}, "redirect_uri": {redirectURI}, "code_verifier": {verifier}}.Encode())
		apiResponse("token exchange", response, http.StatusOK)
		if response.Header.Get("Cache-Control") != "no-store" || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
			t.Errorf("token response must not be cached: %v", response.Header)
		}
		var tokens struct {
			AccessToken string `json:"access_token"`
		}
		if err := json.Unmarshal(body, &tokens); err != nil || tokens.AccessToken == "" {
			t.Fatalf("tokens: %v %s", err, body)
		}
		response, body = e.post("/oauth/token", formHeader(), url.Values{"grant_type": {"authorization_code"}, "code": {"used-or-forged"}, "client_id": {registered.ClientID}, "redirect_uri": {redirectURI}, "code_verifier": {verifier}}.Encode())
		apiResponse("token exchange with a bad code", response, http.StatusBadRequest)
		if !strings.Contains(string(body), "invalid_grant") {
			t.Errorf("bad code: %s", body)
		}
		// Token endpoints are never cached, on error responses either (RFC 6749 section 5.1).
		for label, uncached := range map[string]*http.Response{
			"token error":         response,
			"registration":        first(e.post("/oauth/register", jsonHeader(), `{"client_name":"x","redirect_uris":["http://127.0.0.1:1/cb"],"token_endpoint_auth_method":"none"}`)),
			"app token endpoint":  first(e.post("/api/oauth/token", formHeader(), "grant_type=client_credentials&client_id=nope&client_secret=nope")),
			"revocation":          first(e.post("/oauth/revoke", formHeader(), "token=nope")),
			"unsupported grant":   first(e.post("/oauth/token", formHeader(), "grant_type=password")),
			"introspection error": first(e.post("/oauth/introspect", formHeader(), "token=nope")),
		} {
			if uncached.Header.Get("Cache-Control") != "no-store" || uncached.Header.Get("Pragma") != "no-cache" {
				t.Errorf("%s [%s]: Cache-Control %q, Pragma %q", label, e.mode, uncached.Header.Get("Cache-Control"), uncached.Header.Get("Pragma"))
			}
		}
		response, _ = e.post("/oauth/token", formHeader(), "grant_type=authorization_code&client_id=unknown")
		if response.StatusCode < 400 {
			t.Errorf("token exchange for an unknown client: %d", response.StatusCode)
		}
		apiResponse("token exchange for an unknown client", response, response.StatusCode)
		response, _ = e.do(surfaceRequest{path: "/oauth/token"})
		apiResponse("token endpoint GET", response, http.StatusMethodNotAllowed)
		// Every OAuth endpoint is an API route: a GET is a 405, never the app document.
		for _, path := range []string{"/oauth/register", "/oauth/revoke", "/oauth/introspect"} {
			response, _ = e.do(surfaceRequest{path: path})
			apiResponse("GET "+path, response, http.StatusMethodNotAllowed)
		}
		response, _ = e.post("/oauth/introspect", formHeader(), "token="+tokens.AccessToken)
		if response.StatusCode < 400 {
			t.Errorf("introspection without client credentials: %d", response.StatusCode)
		}
		apiResponse("introspection without client credentials", response, response.StatusCode)
		if !strings.Contains(response.Header.Get("Content-Security-Policy")+response.Header.Get("Content-Security-Policy-Report-Only"), "default-src 'none'") {
			t.Errorf("introspection is not an API response: %v", response.Header)
		}

		// MCP: initialize with the token, the 401 challenge, a bad method, a malformed body.
		initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"name":"test","version":"1"},"capabilities":{}}}`
		for _, path := range []string{"/mcp", "/mcp/readonly"} {
			response, body = e.do(surfaceRequest{method: http.MethodPost, path: path, body: []byte(initialize), header: http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json, text/event-stream"}, "Authorization": {"Bearer " + tokens.AccessToken}}})
			apiResponse("mcp initialize "+path, response, http.StatusOK)
			if !json.Valid(body) && !strings.Contains(string(body), "data:") {
				t.Errorf("%s: %s", path, body)
			}
			response, _ = e.do(surfaceRequest{method: http.MethodPost, path: path, body: []byte(initialize), header: jsonHeader()})
			apiResponse("mcp without a token "+path, response, http.StatusUnauthorized)
			if !strings.Contains(response.Header.Get("WWW-Authenticate"), "resource_metadata") {
				t.Errorf("%s: 401 without the discovery challenge: %v", path, response.Header)
			}
			response, _ = e.do(surfaceRequest{method: http.MethodPost, path: path, body: []byte(initialize), header: http.Header{"Authorization": {"Bearer flow_invalid"}, "Content-Type": {"application/json"}}})
			apiResponse("mcp with an invalid token "+path, response, http.StatusUnauthorized)
			response, _ = e.do(surfaceRequest{method: http.MethodPost, path: path, body: []byte(`{not json`), header: http.Header{"Authorization": {"Bearer " + tokens.AccessToken}, "Content-Type": {"application/json"}}})
			if response.StatusCode < 200 {
				t.Errorf("%s malformed: %d", path, response.StatusCode)
			}
			e.checkHeaders("mcp malformed body "+path, response, kindAPI)
			response, _ = e.do(surfaceRequest{method: http.MethodDelete, path: path, header: http.Header{"Authorization": {"Bearer " + tokens.AccessToken}}})
			e.checkHeaders("mcp DELETE "+path, response, kindAPI)
		}
		response, _ = e.post("/oauth/revoke", formHeader(), "token="+tokens.AccessToken+"&client_id="+registered.ClientID)
		apiResponse("revoke", response, http.StatusOK)
		response, _ = e.do(surfaceRequest{method: http.MethodPost, path: "/mcp", body: []byte(initialize), header: http.Header{"Authorization": {"Bearer " + tokens.AccessToken}, "Content-Type": {"application/json"}}})
		apiResponse("mcp with a revoked token", response, http.StatusUnauthorized)
	})
}

// 429 from the API limiter on the OAuth and MCP endpoints.
func TestSecuritySurfaceRateLimitedProtocols(t *testing.T) {
	forEachSurfaceMode(t, func(s *server, _ *appconfig.SecurityHeadersConfig) {
		config := defaultAPIRateLimitConfig()
		config.Budgets[apiRateUnauthenticated] = apiRateBudget{Requests: 2, Complexity: 1000}
		s.apiRateLimiter = newAPIRateLimiter(config, nil)
	}, func(t *testing.T, e *surfaceEnv) {
		var response *http.Response
		for attempt := 0; attempt < 6 && (response == nil || response.StatusCode != http.StatusTooManyRequests); attempt++ {
			response, _ = e.post("/oauth/revoke", formHeader(), "token=x")
		}
		e.expect("oauth revoke over budget", response, http.StatusTooManyRequests, kindAPI)
		if response.Header.Get("Retry-After") == "" || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
			t.Errorf("429 headers: %v", response.Header)
		}
		response, _ = e.post("/mcp", jsonHeader(), `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
		e.expect("mcp over budget", response, http.StatusTooManyRequests, kindAPI)
		response, _ = e.post("/oauth/token", formHeader(), "grant_type=client_credentials")
		e.expect("oauth token over budget", response, http.StatusTooManyRequests, kindAPI)
		// Invalid bearer tokens spend the same budget; the 429 for them is an ordinary API response too.
		response, _ = e.do(surfaceRequest{path: "/api/viewer", header: http.Header{"Authorization": {"Bearer flow_api_invalid"}}})
		if response.StatusCode != http.StatusTooManyRequests && response.StatusCode != http.StatusUnauthorized {
			t.Errorf("invalid bearer: %d", response.StatusCode)
		}
		e.checkHeaders("invalid bearer token", response, kindAPI)
	})
}

// panickingLimiter makes the sign-in handlers panic: it is the one
// collaborator of the real routes a test can replace.
type panickingLimiter struct{ armed *atomic.Bool }

func (l panickingLimiter) Allow(context.Context, string, int, time.Duration) (bool, time.Duration, error) {
	if l.armed.Load() {
		panic("surface test: limiter failure")
	}
	return true, 0, nil
}

// A handler that panics: the 500 is the standard envelope and keeps the
// whole header set, through the real handler chain.
func TestSecuritySurfacePanicRecovery(t *testing.T) {
	var armed atomic.Bool
	forEachSurfaceMode(t, func(s *server, _ *appconfig.SecurityHeadersConfig) { s.authLimiter = panickingLimiter{armed: &armed} }, func(t *testing.T, e *surfaceEnv) {
		armed.Store(true)
		defer armed.Store(false)
		for _, path := range []string{"/api/auth/login", "/api/auth/magic-link", "/api/auth/register"} {
			response, body := e.post(path, jsonHeader(), `{"email":"admin@example.test","password":"test-password"}`)
			e.expect("panic on "+path, response, http.StatusInternalServerError, kindAPI)
			if !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") || response.Header.Get("Cache-Control") != "no-store" || strings.Contains(string(body), "surface test") {
				t.Errorf("%s: 500 headers %v body %s", path, response.Header, body)
			}
		}
	})
}

// An operator who lets a portal frame Flow (FLOW_SECURITY_FRAME_ANCESTORS)
// does not let it frame the OAuth consent page: that page is the one a
// clickjacking attempt would target, and nothing embeds it.
func TestSecuritySurfaceConsentPageIgnoresFramingAllowList(t *testing.T) {
	forEachSurfaceMode(t, func(s *server, config *appconfig.SecurityHeadersConfig) {
		config.FrameAncestors = []string{"'self'", "https://portal.example.com"}
	}, func(t *testing.T, e *surfaceEnv) {
		response, _ := e.do(surfaceRequest{path: "/oauth/authorize?response_type=code&client_id=x"})
		e.expect("consent page", response, http.StatusOK, kindNoFrame)
		if !strings.Contains(response.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") || strings.Contains(response.Header.Get("Content-Security-Policy")+response.Header.Get("Content-Security-Policy-Report-Only"), "portal.example.com") {
			t.Errorf("consent page policy: %v", response.Header)
		}
		// The rest of the app follows the allow list: the portal may frame it, X-Frame-Options is not sent.
		response, _ = e.do(surfaceRequest{path: "/login"})
		if response.Header.Get("X-Frame-Options") != "" || !strings.Contains(response.Header.Get("Content-Security-Policy"), "frame-ancestors 'self' https://portal.example.com") {
			t.Errorf("sign-in page framing: %v", response.Header)
		}
	})
}
