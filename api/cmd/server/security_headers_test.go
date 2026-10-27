package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	appconfig "flow/api/internal/config"
	"flow/api/internal/store"
)

func securityTestServer(t *testing.T, configure func(*server)) http.Handler {
	t.Helper()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	if configure != nil {
		configure(s)
	}
	return newHandler(s)
}

func securityResponse(t *testing.T, handler http.Handler, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

const testIndexScript = "document.documentElement.dataset.theme = 'dark';"

func securityStaticDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	index := "<!doctype html><html><head><script>" + testIndexScript + "</script><script type=\"module\" src=\"/assets/index-AbCdEf12.js\"></script></head><body></body></html>"
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(index), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "index-AbCdEf12.js"), []byte("console.log(1)"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func scriptHash(script string) string {
	digest := sha256.Sum256([]byte(script))
	return "'sha256-" + base64.StdEncoding.EncodeToString(digest[:]) + "'"
}

func TestSecurityHeadersDefaultsOnAPIHealthAndErrors(t *testing.T) {
	handler := securityTestServer(t, nil)
	for _, path := range []string{"/api/health", "/api/no-such-route", "/uploads/missing.png", "/.well-known/oauth-authorization-server"} {
		response := securityResponse(t, handler, httptest.NewRequest(http.MethodGet, path, nil))
		header := response.Header()
		if header.Get("X-Content-Type-Options") != "nosniff" || header.Get("Referrer-Policy") != "strict-origin-when-cross-origin" || header.Get("X-Frame-Options") != "SAMEORIGIN" {
			t.Fatalf("%s (%d): baseline headers missing: %v", path, response.Code, header)
		}
		if permissions := header.Get("Permissions-Policy"); !strings.Contains(permissions, "camera=()") || !strings.Contains(permissions, "microphone=()") || !strings.Contains(permissions, "clipboard-write=(self)") {
			t.Fatalf("%s: Permissions-Policy %q", path, permissions)
		}
		// Report-only by default; framing is enforced on its own because
		// browsers ignore frame-ancestors in a report-only policy.
		if got := header.Values("Content-Security-Policy"); len(got) != 1 || got[0] != "frame-ancestors 'self'" {
			t.Fatalf("%s: enforced CSP %q", path, got)
		}
		if got := header.Get("Content-Security-Policy-Report-Only"); got != "default-src 'none'; base-uri 'none'; form-action 'none'" {
			t.Fatalf("%s: report-only CSP %q", path, got)
		}
		if header.Get("Strict-Transport-Security") != "" {
			t.Fatalf("%s: HSTS sent for a plain-HTTP app", path)
		}
	}
}

func TestSecurityHeadersWebAppPolicyHashesInlineScripts(t *testing.T) {
	static := securityStaticDir(t)
	enforce := appconfig.DefaultSecurityHeaders()
	enforce.CSP = appconfig.CSPEnforce
	handler := securityTestServer(t, func(s *server) {
		s.staticPath, s.allowedOrigin, s.securityHeaders = static, "https://flow.example.com", &enforce
	})
	document := securityResponse(t, handler, httptest.NewRequest(http.MethodGet, "/acme/issue/ACME-1", nil))
	policy := document.Header().Get("Content-Security-Policy")
	for _, want := range []string{
		"default-src 'self'", "script-src 'self' " + scriptHash(testIndexScript) + ";", "style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob: https:", "connect-src 'self' wss://flow.example.com https://api.github.com",
		"frame-src blob: https://www.youtube-nocookie.com", "worker-src 'self'", "object-src 'none'", "base-uri 'self'",
		"form-action 'self'", "frame-ancestors 'self'",
	} {
		if !strings.Contains(policy, want) {
			t.Fatalf("web app policy lacks %q: %s", want, policy)
		}
	}
	for _, banned := range []string{"'unsafe-eval'", " * ", "script-src 'self' 'unsafe-inline'", "https: 'unsafe"} {
		if strings.Contains(policy, banned) {
			t.Fatalf("web app policy contains %q: %s", banned, policy)
		}
	}
	if document.Header().Get("Content-Security-Policy-Report-Only") != "" {
		t.Fatal("enforce mode must not also send a report-only policy")
	}
	// Static assets (worker scripts take their policy from their own response)
	// and a missing chunk's 404 carry the same policy.
	for _, path := range []string{"/assets/index-AbCdEf12.js", "/assets/index-00000000.js"} {
		response := securityResponse(t, handler, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Header().Get("Content-Security-Policy") != policy || response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s (%d): policy %q", path, response.Code, response.Header().Get("Content-Security-Policy"))
		}
	}
	// API routes keep the locked-down policy even when the web app is served.
	api := securityResponse(t, handler, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if got := api.Header().Get("Content-Security-Policy"); got != "default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'" {
		t.Fatalf("API policy %q", got)
	}
	// A rebuilt index.html is picked up without a restart.
	rebuilt := "<!doctype html><script>window.rebuilt = true</script>"
	if err := os.WriteFile(filepath.Join(static, "index.html"), []byte(rebuilt), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(filepath.Join(static, "index.html"), later, later)
	refreshed := securityResponse(t, handler, httptest.NewRequest(http.MethodGet, "/", nil)).Header().Get("Content-Security-Policy")
	if !strings.Contains(refreshed, scriptHash("window.rebuilt = true")) || strings.Contains(refreshed, scriptHash(testIndexScript)) {
		t.Fatalf("policy did not follow the rebuilt index.html: %s", refreshed)
	}
}

func TestSecurityHeadersInlineScriptHashListSkipsExternalScripts(t *testing.T) {
	hashes := inlineScriptHashList([]byte(`<script>a()</script><SCRIPT type="module" src="/x.js"></SCRIPT><script data-src="y">b()</script><script></script><script>a()</script>`))
	if len(hashes) != 2 || hashes[0] != scriptHash("a()") || hashes[1] != scriptHash("b()") {
		t.Fatalf("hashes %v", hashes)
	}
}

func TestSecurityHeadersHandlerOwnedPolicyWins(t *testing.T) {
	policy := newSecurityHeaderPolicy(appconfig.DefaultSecurityHeaders(), "https://flow.example.com", "")
	upload := withSecurityHeaders(policy, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		_, _ = w.Write([]byte("file"))
	}))
	response := securityResponse(t, upload, httptest.NewRequest(http.MethodGet, "/uploads/a.svg", nil))
	if got := response.Header().Values("Content-Security-Policy"); len(got) != 1 || got[0] != "sandbox; default-src 'none'; frame-ancestors 'self'" {
		t.Fatalf("upload CSP %q", got)
	}
	if response.Header().Get("Content-Security-Policy-Report-Only") != "" || response.Header().Get("X-Frame-Options") != "DENY" || response.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("handler headers were overridden: %v", response.Header())
	}
	framed := withSecurityHeaders(policy, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none';")
		w.WriteHeader(http.StatusOK)
	}))
	if got := securityResponse(t, framed, httptest.NewRequest(http.MethodGet, "/api/x", nil)).Header().Values("Content-Security-Policy"); len(got) != 1 || got[0] != "default-src 'none'; frame-ancestors 'none';" {
		t.Fatalf("a handler's own frame-ancestors must stay: %q", got)
	}
	// The unsubscribe page has its own policy that allows only its stylesheet.
	var service *server
	handler := securityTestServer(t, func(s *server) { service = s })
	key, err := service.unsubscribeKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	page := securityResponse(t, handler, httptest.NewRequest(http.MethodGet, "/api/email/unsubscribe?list="+onboardingList+"&user=usr_1&sig="+unsubscribeSignature(key, onboardingList, "usr_1"), nil))
	csp := page.Header().Get("Content-Security-Policy")
	if !strings.HasPrefix(page.Header().Get("Content-Type"), "text/html") || !strings.Contains(csp, "style-src "+scriptHash(unsubscribeStyle)) || !strings.Contains(csp, "frame-ancestors 'self'") || page.Header().Get("Content-Security-Policy-Report-Only") != "" {
		t.Fatalf("unsubscribe page (%d) CSP %q", page.Code, csp)
	}
	if !strings.Contains(page.Body.String(), "<style>"+unsubscribeStyle+"</style>") {
		t.Fatal("the hashed style is not the one the page renders")
	}
}

func TestSecurityHeadersHSTSOnlyForConfirmedHTTPS(t *testing.T) {
	tlsState := &tls.ConnectionState{}
	cases := []struct {
		name      string
		appURL    string
		trust     bool
		tls       bool
		forwarded []string
		configure func(*appconfig.SecurityHeadersConfig)
		want      string
	}{
		{name: "TLS on the listener", appURL: "https://flow.example.com", tls: true, want: "max-age=31536000"},
		{name: "plain HTTP request", appURL: "https://flow.example.com"},
		{name: "forged X-Forwarded-Proto without a trusted proxy", appURL: "https://flow.example.com", forwarded: []string{"https"}},
		{name: "trusted proxy says https", appURL: "https://flow.example.com", trust: true, forwarded: []string{"https"}, want: "max-age=31536000"},
		{name: "trusted proxy says http", appURL: "https://flow.example.com", trust: true, forwarded: []string{"http"}},
		{name: "client-prepended https before the proxy's http", appURL: "https://flow.example.com", trust: true, forwarded: []string{"https, http"}},
		{name: "nearest proxy value wins across header lines", appURL: "https://flow.example.com", trust: true, forwarded: []string{"http", "https"}, want: "max-age=31536000"},
		{name: "trusted proxy without the header", appURL: "https://flow.example.com", trust: true},
		{name: "app URL is not https", appURL: "http://flow.example.com", tls: true},
		{name: "HSTS off", appURL: "https://flow.example.com", tls: true, configure: func(c *appconfig.SecurityHeadersConfig) { c.HSTS = appconfig.HSTSOff }},
		{name: "subdomains and preload", appURL: "https://flow.example.com", tls: true, configure: func(c *appconfig.SecurityHeadersConfig) {
			c.HSTSMaxAge, c.HSTSIncludeSubdomains, c.HSTSPreload = 63072000, true, true
		}, want: "max-age=63072000; includeSubDomains; preload"},
		{name: "gateway owns HSTS", appURL: "https://flow.example.com", tls: true, configure: func(c *appconfig.SecurityHeadersConfig) {
			c.Skip = []string{"Strict-Transport-Security"}
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			config := appconfig.DefaultSecurityHeaders()
			config.TrustProxyHeaders = test.trust
			if test.configure != nil {
				test.configure(&config)
			}
			handler := withSecurityHeaders(newSecurityHeaderPolicy(config, test.appURL, ""), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
			request := httptest.NewRequest(http.MethodGet, "/api/health", nil)
			if test.tls {
				request.TLS = tlsState
			}
			for _, value := range test.forwarded {
				request.Header.Add("X-Forwarded-Proto", value)
			}
			if got := securityResponse(t, handler, request).Header().Get("Strict-Transport-Security"); got != test.want {
				t.Fatalf("HSTS %q, want %q", got, test.want)
			}
		})
	}
}

func TestSecurityHeadersCSPModesAndReporting(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	config := appconfig.DefaultSecurityHeaders()
	config.CSPReportURI = "/csp-reports"
	header := securityResponse(t, withSecurityHeaders(newSecurityHeaderPolicy(config, "https://flow.example.com", ""), ok), httptest.NewRequest(http.MethodGet, "/api/health", nil)).Header()
	if header.Get("Content-Security-Policy") != "frame-ancestors 'self'" || header.Get("Content-Security-Policy-Report-Only") != "default-src 'none'; base-uri 'none'; form-action 'none'; report-uri https://flow.example.com/csp-reports; report-to csp" || header.Get("Reporting-Endpoints") != `csp="https://flow.example.com/csp-reports"` {
		t.Fatalf("report-only with reporting: %v", header)
	}
	config.CSP = appconfig.CSPEnforce
	header = securityResponse(t, withSecurityHeaders(newSecurityHeaderPolicy(config, "https://flow.example.com", ""), ok), httptest.NewRequest(http.MethodGet, "/api/health", nil)).Header()
	if got := header.Values("Content-Security-Policy"); len(got) != 1 || got[0] != "default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'; report-uri https://flow.example.com/csp-reports; report-to csp" || header.Get("Content-Security-Policy-Report-Only") != "" {
		t.Fatalf("enforce: %v", header)
	}
	config.CSP = appconfig.CSPOff
	header = securityResponse(t, withSecurityHeaders(newSecurityHeaderPolicy(config, "https://flow.example.com", ""), ok), httptest.NewRequest(http.MethodGet, "/api/health", nil)).Header()
	if header.Get("Content-Security-Policy") != "frame-ancestors 'self'" || header.Get("Content-Security-Policy-Report-Only") != "" || header.Get("Reporting-Endpoints") != "" {
		t.Fatalf("CSP off keeps only framing: %v", header)
	}
}

func TestSecurityHeadersFramingAllowListAndGatewayOverrides(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	serve := func(config appconfig.SecurityHeadersConfig) http.Header {
		return securityResponse(t, withSecurityHeaders(newSecurityHeaderPolicy(config, "https://flow.example.com", ""), ok), httptest.NewRequest(http.MethodGet, "/", nil)).Header()
	}
	config := appconfig.DefaultSecurityHeaders()
	config.FrameAncestors = []string{"'self'", "https://portal.example.com"}
	header := serve(config)
	if header.Get("X-Frame-Options") != "" || header.Get("Content-Security-Policy") != "frame-ancestors 'self' https://portal.example.com" {
		t.Fatalf("embedding allow list: %v", header)
	}
	config.FrameAncestors = []string{"'none'"}
	if header := serve(config); header.Get("X-Frame-Options") != "DENY" || header.Get("Content-Security-Policy") != "frame-ancestors 'none'" {
		t.Fatalf("no framing: %v", header)
	}
	config = appconfig.DefaultSecurityHeaders()
	config.Skip = []string{"Content-Security-Policy", "X-Frame-Options", "Permissions-Policy"}
	header = serve(config)
	if header.Get("Content-Security-Policy") != "" || header.Get("Content-Security-Policy-Report-Only") != "" || header.Get("X-Frame-Options") != "" || header.Get("Permissions-Policy") != "" || header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("skipped headers: %v", header)
	}
	config = appconfig.DefaultSecurityHeaders()
	config.Enabled = false
	if newSecurityHeaderPolicy(config, "https://flow.example.com", "") != nil {
		t.Fatal("FLOW_SECURITY_HEADERS=off must turn the policy off")
	}
	handler := securityTestServer(t, func(s *server) { s.securityHeaders = &config })
	if header := securityResponse(t, handler, httptest.NewRequest(http.MethodGet, "/api/health", nil)).Header(); header.Get("Permissions-Policy") != "" || header.Get("Content-Security-Policy") != "" || header.Get("X-Frame-Options") != "" {
		t.Fatalf("headers sent with the policy off: %v", header)
	}
}

func TestSecurityHeadersStreamingImplicitAndUpgradeResponses(t *testing.T) {
	policy := newSecurityHeaderPolicy(appconfig.DefaultSecurityHeaders(), "http://127.0.0.1", "")
	mux := http.NewServeMux()
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: one\n\n"))
		_ = http.NewResponseController(w).Flush()
	})
	mux.HandleFunc("/empty", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/socket", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = conn.Write(r.Context(), websocket.MessageText, []byte("hello"))
		_ = conn.Close(websocket.StatusNormalClosure, "")
	})
	server := httptest.NewServer(withSecurityHeaders(policy, mux))
	defer server.Close()
	for _, path := range []string{"/stream", "/empty"} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.Header.Get("X-Content-Type-Options") != "nosniff" || response.Header.Get("Content-Security-Policy") != "frame-ancestors 'self'" {
			t.Fatalf("%s: %v", path, response.Header)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, handshake, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/socket", nil)
	if err != nil {
		t.Fatalf("upgrade through the security header writer: %v", err)
	}
	defer conn.CloseNow()
	if handshake.StatusCode != http.StatusSwitchingProtocols || handshake.Header.Get("Content-Security-Policy") != "" {
		t.Fatalf("upgrade response: %d %v", handshake.StatusCode, handshake.Header)
	}
	if _, message, err := conn.Read(ctx); err != nil || string(message) != "hello" {
		t.Fatalf("message %q, %v", message, err)
	}
}

// The web app policy's frame-src and the Permissions-Policy delegations must
// cover every player the editor embeds.
func TestSecurityHeadersEmbedOriginsMatchWebProviders(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "src", "components", "editor", "embeds", "embed-providers.ts"))
	if err != nil {
		t.Skipf("web sources not available: %v", err)
	}
	matches := regexp.MustCompile("source\\('[a-z]+', `(https://[^/`$]+)/").FindAllStringSubmatch(string(source), -1)
	if len(matches) == 0 {
		t.Fatal("no embed origins found in embed-providers.ts")
	}
	frame := map[string]bool{}
	for _, match := range matches {
		frame[match[1]] = true
	}
	for origin := range frame {
		if !containsString(embedFrameOrigins, origin) {
			t.Errorf("embed origin %s is missing from embedFrameOrigins", origin)
		}
	}
	for _, origin := range embedFrameOrigins {
		if !frame[origin] {
			t.Errorf("embedFrameOrigins lists %s, which the editor no longer embeds", origin)
		}
	}
}
