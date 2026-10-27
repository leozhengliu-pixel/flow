package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
)

func forwardingTestPolicy(t *testing.T, trust bool, proxies string, appURL string) *forwardingPolicy {
	t.Helper()
	var prefixes []netip.Prefix
	if proxies != "" {
		parsed, err := appconfig.ParseTrustedProxies(strings.Split(proxies, ","))
		if err != nil {
			t.Fatal(err)
		}
		prefixes = parsed
	}
	return newForwardingPolicy(trust, prefixes, appURL)
}

func forwardedRequest(remote string, headers map[string][]string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "http://flow.internal:8080/api/health", nil)
	request.Host = "flow.internal:8080"
	request.RemoteAddr = remote
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	return request
}

func TestForwardingClientAddress(t *testing.T) {
	cases := []struct {
		name    string
		trust   bool
		proxies string
		remote  string
		xff     []string
		want    string
	}{
		{name: "untrusted: forged X-Forwarded-For is ignored", remote: "203.0.113.9:5123", xff: []string{"198.51.100.1"}, want: "203.0.113.9"},
		{name: "untrusted IPv6 peer", remote: "[2001:db8::9]:5123", xff: []string{"198.51.100.1"}, want: "2001:db8::9"},
		{name: "trusted proxy without the header", trust: true, remote: "10.0.0.5:5123", want: "10.0.0.5"},
		{name: "trusted proxy: the entry it appended wins over a client-sent one", trust: true, remote: "10.0.0.5:5123", xff: []string{"6.6.6.6, 198.51.100.7"}, want: "198.51.100.7"},
		{name: "trusted proxy: right-most across header lines", trust: true, remote: "10.0.0.5:5123", xff: []string{"6.6.6.6", "198.51.100.7"}, want: "198.51.100.7"},
		{name: "trusted proxy: blank items are skipped", trust: true, remote: "10.0.0.5:5123", xff: []string{"198.51.100.7, ", ""}, want: "198.51.100.7"},
		{name: "trusted proxy: IPv6 client with brackets and port", trust: true, remote: "10.0.0.5:5123", xff: []string{"[2001:db8::7]:443"}, want: "2001:db8::7"},
		{name: "trusted proxy: bare IPv6 client", trust: true, remote: "[fd00::5]:5123", xff: []string{"2001:db8::7"}, want: "2001:db8::7"},
		{name: "trusted proxy: IPv4-mapped IPv6 reads as IPv4", trust: true, remote: "10.0.0.5:5123", xff: []string{"::ffff:198.51.100.7"}, want: "198.51.100.7"},
		{name: "trusted proxy: garbage entry falls back to the peer", trust: true, remote: "10.0.0.5:5123", xff: []string{"1.2.3.4, not-an-ip"}, want: "10.0.0.5"},
		{name: "proxy list: chain of trusted hops is skipped", trust: true, proxies: "10.0.0.0/8, 2001:db8:ffff::/48", remote: "10.0.0.5:5123", xff: []string{"6.6.6.6, 198.51.100.7, 2001:db8:ffff::1, 10.1.2.3"}, want: "198.51.100.7"},
		{name: "proxy list: client forging a trusted address only moves past its own entries", trust: true, proxies: "10.0.0.0/8", remote: "10.0.0.5:5123", xff: []string{"10.9.9.9, 198.51.100.7"}, want: "198.51.100.7"},
		{name: "proxy list: every hop trusted gives the left-most", trust: true, proxies: "10.0.0.0/8", remote: "10.0.0.5:5123", xff: []string{"10.0.0.1, 10.0.0.2"}, want: "10.0.0.1"},
		{name: "proxy list: garbage after trusted hops stops at the last valid hop", trust: true, proxies: "10.0.0.0/8", remote: "10.0.0.5:5123", xff: []string{"bogus, 10.0.0.2"}, want: "10.0.0.2"},
		{name: "proxy list: peer outside the list is not a proxy", trust: true, proxies: "10.0.0.0/8", remote: "203.0.113.9:5123", xff: []string{"198.51.100.7"}, want: "203.0.113.9"},
		{name: "proxy list: IPv6 peer inside the list", trust: true, proxies: "fd00::/8", remote: "[fd00::5]:5123", xff: []string{"198.51.100.7"}, want: "198.51.100.7"},
		{name: "proxy list: single address entry", trust: true, proxies: "10.0.0.5", remote: "10.0.0.5:5123", xff: []string{"198.51.100.7"}, want: "198.51.100.7"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			policy := forwardingTestPolicy(t, test.trust, test.proxies, "")
			request := forwardedRequest(test.remote, map[string][]string{"X-Forwarded-For": test.xff})
			if got := policy.clientAddress(request); got != test.want {
				t.Fatalf("client address %q, want %q", got, test.want)
			}
		})
	}
}

func TestForwardingSchemeHostAndBaseURL(t *testing.T) {
	cases := []struct {
		name    string
		trust   bool
		proxies string
		appURL  string
		remote  string
		tls     bool
		host    string
		headers map[string][]string
		want    string
	}{
		{name: "untrusted: forged proto and host are ignored", remote: "203.0.113.9:1", host: "flow.internal:8080", headers: map[string][]string{"X-Forwarded-Proto": {"https"}, "X-Forwarded-Host": {"evil.example"}}, want: "http://flow.internal:8080"},
		{name: "untrusted: Forwarded is never read", trust: true, remote: "10.0.0.5:1", host: "flow.internal:8080", headers: map[string][]string{"Forwarded": {"proto=https;host=evil.example"}}, want: "http://flow.internal:8080"},
		{name: "TLS on the listener", remote: "203.0.113.9:1", tls: true, host: "flow.example.com", want: "https://flow.example.com"},
		{name: "untrusted: app URL scheme for the configured host", appURL: "https://flow.example.com", remote: "203.0.113.9:1", host: "flow.example.com", headers: map[string][]string{"X-Forwarded-Proto": {"http"}}, want: "https://flow.example.com"},
		{name: "untrusted: app URL scheme only for its host", appURL: "https://flow.example.com", remote: "203.0.113.9:1", host: "other.example.com", want: "http://other.example.com"},
		{name: "trusted proxy: proto and host", trust: true, remote: "10.0.0.5:1", host: "flow-api:8080", headers: map[string][]string{"X-Forwarded-Proto": {"https"}, "X-Forwarded-Host": {"flow.example.com"}}, want: "https://flow.example.com"},
		{name: "trusted proxy: nearest values win", trust: true, remote: "10.0.0.5:1", host: "flow-api:8080", headers: map[string][]string{"X-Forwarded-Proto": {"https, http"}, "X-Forwarded-Host": {"evil.example", "flow.example.com"}}, want: "http://flow.example.com"},
		{name: "trusted proxy: invalid forwarded host is ignored", trust: true, remote: "10.0.0.5:1", host: "flow.example.com", headers: map[string][]string{"X-Forwarded-Proto": {"https"}, "X-Forwarded-Host": {"evil.example/path@x"}}, want: "https://flow.example.com"},
		{name: "trusted proxy: IPv6 forwarded host", trust: true, remote: "[fd00::5]:1", host: "flow-api:8080", headers: map[string][]string{"X-Forwarded-Proto": {"https"}, "X-Forwarded-Host": {"[2001:db8::1]:8443"}}, want: "https://[2001:db8::1]:8443"},
		{name: "trusted proxy: unknown proto falls back to the app URL", trust: true, appURL: "https://flow.example.com", remote: "10.0.0.5:1", host: "flow.example.com", headers: map[string][]string{"X-Forwarded-Proto": {"gopher"}}, want: "https://flow.example.com"},
		{name: "proxy list: peer outside the list", trust: true, proxies: "10.0.0.0/8", remote: "203.0.113.9:1", host: "flow.internal:8080", headers: map[string][]string{"X-Forwarded-Proto": {"https"}, "X-Forwarded-Host": {"evil.example"}}, want: "http://flow.internal:8080"},
		{name: "proxy list: peer inside the list", trust: true, proxies: "10.0.0.0/8", remote: "10.3.0.1:1", host: "flow.internal:8080", headers: map[string][]string{"X-Forwarded-Proto": {"https"}, "X-Forwarded-Host": {"flow.example.com"}}, want: "https://flow.example.com"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			policy := forwardingTestPolicy(t, test.trust, test.proxies, test.appURL)
			request := forwardedRequest(test.remote, test.headers)
			request.Host = test.host
			if test.tls {
				request.TLS = &tls.ConnectionState{}
			}
			request = request.WithContext(context.WithValue(request.Context(), forwardingPolicyKey{}, policy))
			if got := externalBaseURL(request); got != test.want {
				t.Fatalf("base URL %q, want %q", got, test.want)
			}
			if got, want := secureCookie(request), strings.HasPrefix(test.want, "https://"); got != want {
				t.Fatalf("secure cookie %v, want %v", got, want)
			}
		})
	}
}

func TestForwardingFromEnvironment(t *testing.T) {
	t.Setenv("FLOW_APP_URL", "")
	t.Setenv("FLOW_COOKIE_SECURE", "")
	t.Setenv("FLOW_TRUSTED_PROXIES", "")
	t.Setenv("FLOW_TRUST_PROXY_HEADERS", "")
	request := forwardedRequest("203.0.113.9:1", map[string][]string{"X-Forwarded-For": {"198.51.100.7"}, "X-Forwarded-Proto": {"https"}, "X-Forwarded-Host": {"evil.example"}})
	if got := externalBaseURL(request); got != "http://flow.internal:8080" {
		t.Fatalf("base URL without trust %q", got)
	}
	if secureCookie(request) || authClientAddress(request) != "203.0.113.9" {
		t.Fatalf("forged headers honoured without trust: secure=%v address=%q", secureCookie(request), authClientAddress(request))
	}
	t.Setenv("FLOW_TRUST_PROXY_HEADERS", "true")
	t.Setenv("FLOW_TRUSTED_PROXIES", "10.0.0.0/8")
	if got := externalBaseURL(request); got != "http://flow.internal:8080" {
		t.Fatalf("peer outside FLOW_TRUSTED_PROXIES was trusted: %q", got)
	}
	request.RemoteAddr = "10.0.0.5:1"
	if got := externalBaseURL(request); got != "https://evil.example" || !secureCookie(request) || authClientAddress(request) != "198.51.100.7" {
		t.Fatalf("trusted proxy: base %q secure %v address %q", got, secureCookie(request), authClientAddress(request))
	}
}

// End to end through the server's handler chain: OAuth metadata, the
// session cookie and the rate limiter key follow the configured trust.
func TestForwardingThroughServerHandler(t *testing.T) {
	t.Setenv("FLOW_COOKIE_SECURE", "")
	for _, test := range []struct {
		name       string
		trust      bool
		proxies    []string
		remote     string
		wantBase   string
		wantClient string
	}{
		{name: "untrusted client forging headers", remote: "203.0.113.9:4000", wantBase: "https://flow.example.com", wantClient: "203.0.113.9"},
		{name: "trusted ingress", trust: true, remote: "10.0.0.5:4000", wantBase: "https://flow.example.com", wantClient: "198.51.100.7"},
		{name: "proxy list with a direct client", trust: true, proxies: []string{"10.0.0.0/8"}, remote: "203.0.113.9:4000", wantBase: "https://flow.example.com", wantClient: "203.0.113.9"},
	} {
		t.Run(test.name, func(t *testing.T) {
			limiter := &recordingAuthLimiter{}
			config := appconfig.DefaultSecurityHeaders()
			config.TrustProxyHeaders, config.TrustedProxies = test.trust, test.proxies
			handler := securityTestServer(t, func(s *server) {
				s.securityHeaders = &config
				s.allowedOrigin = "https://flow.example.com"
				s.authLimiter = limiter
			})
			request := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
			request.Host, request.RemoteAddr = "flow.example.com", test.remote
			request.Header.Set("X-Forwarded-For", "6.6.6.6, 198.51.100.7")
			request.Header.Set("X-Forwarded-Proto", "https")
			if !test.trust || len(test.proxies) > 0 {
				// A client talking to Flow directly tries to rewrite links.
				request.Header.Set("X-Forwarded-Host", "evil.example")
			}
			response := securityResponse(t, handler, request)
			var metadata map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &metadata); err != nil {
				t.Fatalf("metadata %d %s", response.Code, response.Body.String())
			}
			if metadata["issuer"] != test.wantBase || metadata["token_endpoint"] != test.wantBase+"/oauth/token" {
				t.Fatalf("issuer %v token %v, want %s", metadata["issuer"], metadata["token_endpoint"], test.wantBase)
			}

			limited := withForwardingPolicy(forwardingPolicyFromConfig(config, "https://flow.example.com"), (&server{authLimiter: limiter}).limitAuth("login", 5, time.Minute, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				setSessionCookie(w, r, "token", time.Now().Add(time.Hour))
			})))
			recorder := httptest.NewRecorder()
			limited.ServeHTTP(recorder, request)
			if len(limiter.keys) != 1 || limiter.keys[0] != "login:"+test.wantClient {
				t.Fatalf("rate limit keys %v, want login:%s", limiter.keys, test.wantClient)
			}
			cookies := recorder.Result().Cookies()
			if len(cookies) != 1 || !cookies[0].Secure {
				t.Fatalf("session cookie %+v, want Secure", cookies)
			}
		})
	}
}

type recordingAuthLimiter struct{ keys []string }

func (l *recordingAuthLimiter) Allow(_ context.Context, key string, _ int, _ time.Duration) (bool, time.Duration, error) {
	l.keys = append(l.keys, key)
	return true, 0, nil
}

func TestTrustedProxiesConfiguration(t *testing.T) {
	prefixes, err := appconfig.ParseTrustedProxies([]string{"10.0.0.0/8", " 192.0.2.7 ", "2001:db8::/32", "::ffff:10.0.0.0/104", "fd00::1"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.0/8", "192.0.2.7/32", "2001:db8::/32", "10.0.0.0/8", "fd00::1/128"}
	for index, prefix := range prefixes {
		if prefix.String() != want[index] {
			t.Fatalf("prefix %d = %s, want %s", index, prefix, want[index])
		}
	}
	for _, bad := range []string{"10.0.0.0/33", "example.com", "fe80::1%eth0", "10.0.0"} {
		if _, err := appconfig.ParseTrustedProxies([]string{bad}); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	config := appconfig.DefaultSecurityHeaders()
	config.TrustedProxies = []string{"10.0.0.0/8"}
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "FLOW_TRUST_PROXY_HEADERS") {
		t.Fatalf("trusted proxies without trust: %v", err)
	}
	config.TrustProxyHeaders = true
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	config.TrustedProxies = []string{"nope"}
	if err := config.Validate(); err == nil {
		t.Fatal("invalid proxy accepted")
	}
}
