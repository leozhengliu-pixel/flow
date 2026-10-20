package outbound

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeResolver answers from a table; a host with several answer sets
// returns them in turn (DNS rebinding).
type fakeResolver struct {
	mu      sync.Mutex
	answers map[string][][]string
	calls   map[string]int
}

func (r *fakeResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sets := r.answers[host]
	if len(sets) == 0 {
		return nil, errors.New("no such host")
	}
	if r.calls == nil {
		r.calls = map[string]int{}
	}
	index := min(r.calls[host], len(sets)-1)
	r.calls[host]++
	out := []net.IPAddr{}
	for _, value := range sets[index] {
		out = append(out, net.IPAddr{IP: net.ParseIP(value)})
	}
	return out, nil
}

func resolver(table map[string][]string) *fakeResolver {
	answers := map[string][][]string{}
	for host, addrs := range table {
		answers[host] = [][]string{addrs}
	}
	return &fakeResolver{answers: answers}
}

func mustPolicy(t *testing.T, cfg Config) *Policy {
	t.Helper()
	policy, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

var sourcebotConfig = Config{TrustedHosts: []string{"hsb.haier.net", "*.corp.example.com"}, TrustedNetworks: []string{"10.218.0.0/16"}}

func TestPrivateEndpointNeedsHostAndNetwork(t *testing.T) {
	policy := mustPolicy(t, sourcebotConfig).WithResolver(resolver(map[string][]string{
		"hsb.haier.net":          {"10.218.161.13"},
		"other.haier.net":        {"10.218.161.14"},
		"far.corp.example.com":   {"10.1.2.3"},
		"git.corp.example.com":   {"10.218.9.9"},
		"public.example.org":     {"93.184.216.34"},
		"split.corp.example.com": {"10.218.1.1", "192.168.1.1"},
	}))
	ctx := context.Background()
	cases := []struct {
		url    string
		access Access
		ok     bool
		want   string
	}{
		{"https://hsb.haier.net/api/mcp", TrustedPrivate, true, ""},
		{"https://hsb.haier.net/api/mcp", PublicOnly, false, "only public HTTPS"},
		{"http://hsb.haier.net/api/mcp", TrustedPrivate, false, "https://"},
		{"https://user:pw@hsb.haier.net/api/mcp", TrustedPrivate, false, "https://"},
		{"https://other.haier.net/", TrustedPrivate, false, "FLOW_TRUSTED_PRIVATE_ENDPOINTS and FLOW_TRUSTED_PRIVATE_NETWORKS"},
		{"https://far.corp.example.com/", TrustedPrivate, false, "resolves outside FLOW_TRUSTED_PRIVATE_NETWORKS"},
		{"https://git.corp.example.com/", TrustedPrivate, true, ""},
		{"https://split.corp.example.com/", TrustedPrivate, false, "resolves outside"},
		{"https://public.example.org/", PublicOnly, true, ""},
		{"https://10.218.161.13/", TrustedPrivate, false, "FLOW_TRUSTED_PRIVATE_ENDPOINTS"},
	}
	for _, tc := range cases {
		_, err := policy.Check(ctx, tc.url, tc.access)
		if tc.ok != (err == nil) {
			t.Errorf("%s (access %d): err = %v, want ok=%t", tc.url, tc.access, err, tc.ok)
			continue
		}
		if err != nil {
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s: message %q does not contain %q", tc.url, err.Error(), tc.want)
			}
			if !strings.Contains(tc.url, "//10.") && (strings.Contains(err.Error(), "10.") || strings.Contains(err.Error(), "192.168")) {
				t.Errorf("%s: message leaks a resolved address: %q", tc.url, err.Error())
			}
		}
	}
	// Without configuration the production incident's URL is refused with
	// a message that says what to configure.
	_, err := Default().WithResolver(resolver(map[string][]string{"hsb.haier.net": {"10.218.161.13"}})).Check(ctx, "https://hsb.haier.net/api/mcp", TrustedPrivate)
	if err == nil || !strings.Contains(err.Error(), "an operator can allow it with FLOW_TRUSTED_PRIVATE_ENDPOINTS and FLOW_TRUSTED_PRIVATE_NETWORKS") {
		t.Fatalf("default policy: %v", err)
	}
	var refusal *Error
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Detail(), "10.218.161.13") {
		t.Fatalf("Detail should carry the resolved address for logs: %v", err)
	}
}

func TestMetadataAndLoopbackStayBlockedEvenWhenListed(t *testing.T) {
	policy := mustPolicy(t, Config{
		TrustedHosts:    []string{"evil.corp.example.com", "*.corp.example.com", "127.0.0.1", "169.254.169.254"},
		TrustedNetworks: []string{"127.0.0.0/8", "169.254.0.0/16", "100.100.100.200/32", "fd00:ec2::/32", "224.0.0.0/4", "0.0.0.0/8", "::1/128", "10.0.0.0/8"},
	}).WithResolver(resolver(map[string][]string{
		"evil.corp.example.com":   {"127.0.0.1"},
		"meta.corp.example.com":   {"169.254.169.254"},
		"ali.corp.example.com":    {"100.100.100.200"},
		"aws6.corp.example.com":   {"fd00:ec2::254"},
		"mc.corp.example.com":     {"224.0.0.1"},
		"zero.corp.example.com":   {"0.0.0.0"},
		"v6lo.corp.example.com":   {"::1"},
		"mixed.corp.example.com":  {"10.1.1.1", "169.254.169.254"},
		"bcast.corp.example.com":  {"255.255.255.255"},
		"mapped.corp.example.com": {"::ffff:127.0.0.1"},
	}))
	for _, host := range []string{"evil", "meta", "ali", "aws6", "mc", "zero", "v6lo", "mixed", "bcast", "mapped"} {
		_, err := policy.Check(context.Background(), "https://"+host+".corp.example.com/", TrustedPrivate)
		var refusal *Error
		if !errors.As(err, &refusal) || refusal.Reason != ReasonBlocked {
			t.Errorf("%s: expected blocked, got %v", host, err)
		}
	}
	for _, raw := range []string{"https://127.0.0.1/", "https://169.254.169.254/latest/meta-data", "https://[fd00:ec2::254]/", "https://metadata.google.internal/", "https://localhost/"} {
		if _, err := policy.Check(context.Background(), raw, TrustedPrivate); err == nil {
			t.Errorf("%s: expected blocked", raw)
		}
	}
	if _, err := New(Config{TrustedHosts: []string{"metadata.google.internal"}, TrustedNetworks: []string{"10.0.0.0/8"}}); err == nil {
		t.Error("a metadata host name must not be accepted as a trusted endpoint")
	}
}

func TestHostPatterns(t *testing.T) {
	policy := mustPolicy(t, Config{TrustedHosts: []string{"HSB.Haier.Net.", "*.corp.example.com"}, TrustedNetworks: []string{"10.0.0.0/8"}})
	for host, want := range map[string]bool{
		"hsb.haier.net":           true,
		"HSB.HAIER.NET.":          true,
		"x.hsb.haier.net":         false,
		"a.corp.example.com":      true,
		"a.b.corp.example.com":    true,
		"corp.example.com":        false,
		"evilcorp.example.com":    false,
		"a.corp.example.com.evil": false,
	} {
		if got := policy.HostTrusted(host); got != want {
			t.Errorf("HostTrusted(%q) = %t, want %t", host, got, want)
		}
	}
	for _, cfg := range []Config{
		{TrustedHosts: []string{"*"}, TrustedNetworks: []string{"10.0.0.0/8"}},
		{TrustedHosts: []string{"*.com"}, TrustedNetworks: []string{"10.0.0.0/8"}},
		{TrustedHosts: []string{"a.*.example.com"}, TrustedNetworks: []string{"10.0.0.0/8"}},
		{TrustedHosts: []string{"hsb.haier.net"}},
		{TrustedHosts: []string{"hsb.haier.net"}, TrustedNetworks: []string{"0.0.0.0/0"}},
		{TrustedHosts: []string{"hsb.haier.net"}, TrustedNetworks: []string{"not-a-cidr"}},
		{CAFile: filepath.Join(t.TempDir(), "missing.pem")},
	} {
		if err := Validate(cfg); err == nil {
			t.Errorf("Validate(%+v) accepted an invalid configuration", cfg)
		}
	}
	cfg := LoadConfigFrom(map[string]string{EnvTrustedEndpoints: "hsb.haier.net, *.corp.example.com", EnvTrustedNetworks: "10.218.0.0/16"})
	if len(cfg.TrustedHosts) != 2 || len(cfg.TrustedNetworks) != 1 {
		t.Fatalf("LoadConfig: %+v", cfg)
	}
}

// LoadConfigFrom is LoadConfig over a map (tests).
func LoadConfigFrom(env map[string]string) Config {
	return Config{TrustedHosts: splitList(env[EnvTrustedEndpoints]), TrustedNetworks: splitList(env[EnvTrustedNetworks]), CAFile: env[EnvTrustedCAFile]}
}

// routeDialer sends every dial for a checked address to target and records
// what the policy asked for.
type routeDialer struct {
	mu     sync.Mutex
	target string
	dialed []string
}

func (d *routeDialer) dial(ctx context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	d.dialed = append(d.dialed, address)
	d.mu.Unlock()
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, d.target)
}

func TestDNSRebindingIsRecheckedAtDial(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	// example.com: public at save time, the cloud metadata address when Flow connects.
	dns := &fakeResolver{answers: map[string][][]string{"example.com": {{"93.184.216.34"}, {"169.254.169.254"}}}}
	dialer := &routeDialer{target: server.Listener.Addr().String()}
	policy := Default().WithResolver(dns).WithDialer(dialer.dial).WithRoots(poolFor(t, server))
	if _, err := policy.Check(context.Background(), "https://example.com/hook", TrustedPrivate); err != nil {
		t.Fatalf("save-time check: %v", err)
	}
	client := policy.Client(5*time.Second, TrustedPrivate)
	_, err := client.Get("https://example.com/hook")
	if err == nil || !IsRefusal(err) {
		t.Fatalf("the rebound answer must be refused at dial time, got %v", err)
	}
	if len(dialer.dialed) != 0 {
		t.Fatalf("nothing may be dialed after a refused answer: %v", dialer.dialed)
	}
	// A later public answer is dialed by address, the one that was checked.
	dns.answers["example.com"] = [][]string{{"93.184.216.34"}}
	dns.calls = nil
	response, err := client.Get("https://example.com/hook")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(dialer.dialed) != 1 || dialer.dialed[0] != "93.184.216.34:443" {
		t.Fatalf("dial must be pinned to the checked address: %v", dialer.dialed)
	}
}

func poolFor(t *testing.T, server *httptest.Server) *x509.CertPool {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, err := LoadCAFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestTrustedEndpointWithCustomCA(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "example.com" {
			t.Errorf("Host header = %q, want the URL host", r.Host)
		}
		if r.TLS == nil || r.TLS.ServerName != "example.com" {
			t.Errorf("SNI must be the URL host, got %+v", r.TLS)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "enterprise-ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	dns := resolver(map[string][]string{"example.com": {"10.218.161.13"}})
	cfg := Config{TrustedHosts: []string{"example.com"}, TrustedNetworks: []string{"10.218.0.0/16"}}

	// Without the enterprise CA the certificate is not trusted: verification stays on.
	dialer := &routeDialer{target: server.Listener.Addr().String()}
	plain := mustPolicy(t, cfg).WithResolver(dns).WithDialer(dialer.dial)
	if _, err := plain.Client(5*time.Second, TrustedPrivate).Get("https://example.com/"); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("expected a certificate error without FLOW_TRUSTED_CA_FILE, got %v", err)
	}

	cfg.CAFile = path
	policy := mustPolicy(t, cfg).WithResolver(dns).WithDialer(dialer.dial)
	response, err := policy.Client(5*time.Second, TrustedPrivate).Get("https://example.com/")
	if err != nil {
		t.Fatalf("trusted endpoint with enterprise CA: %v", err)
	}
	response.Body.Close()
	if last := dialer.dialed[len(dialer.dialed)-1]; last != "10.218.161.13:443" {
		t.Fatalf("dialed %s, want the checked private address", last)
	}
	// PublicOnly callers never reach it, even with the CA.
	if _, err := policy.Client(5*time.Second, PublicOnly).Get("https://example.com/"); err == nil || !IsRefusal(err) {
		t.Fatalf("PublicOnly must refuse a trusted private endpoint, got %v", err)
	}
}

func TestRedirectToUnlistedHostIsRefused(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/to-unlisted":
			http.Redirect(w, r, "https://intranet.example.net/", http.StatusFound)
		case "/to-public":
			http.Redirect(w, r, "https://public.example.org/", http.StatusFound)
		case "/to-same":
			http.Redirect(w, r, "/done", http.StatusFound)
		default:
			_, _ = w.Write([]byte("done"))
		}
	}))
	defer server.Close()
	dns := resolver(map[string][]string{"example.com": {"10.218.161.13"}, "intranet.example.net": {"10.218.161.99"}, "public.example.org": {"93.184.216.34"}})
	dialer := &routeDialer{target: server.Listener.Addr().String()}
	policy := mustPolicy(t, Config{TrustedHosts: []string{"example.com"}, TrustedNetworks: []string{"10.218.0.0/16"}}).WithResolver(dns).WithDialer(dialer.dial).WithRoots(poolFor(t, server))
	client := policy.Client(5*time.Second, TrustedPrivate)
	for _, path := range []string{"/to-unlisted", "/to-public"} {
		if _, err := client.Get("https://example.com" + path); err == nil || !strings.Contains(err.Error(), "unsafe redirect") {
			t.Errorf("%s: expected the redirect to be refused, got %v", path, err)
		}
	}
	response, err := client.Get("https://example.com/to-same")
	if err != nil {
		t.Fatalf("a redirect within the trusted host is fine: %v", err)
	}
	response.Body.Close()
}

func TestPublicMatchesReservedRanges(t *testing.T) {
	for _, value := range []string{"10.0.0.1", "172.16.0.1", "192.168.0.1", "100.64.0.1", "127.0.0.1", "169.254.169.254", "fc00::1", "64:ff9b::7f00:1", "::"} {
		if Public(netip.MustParseAddr(value)) {
			t.Errorf("%s must not be public", value)
		}
	}
	for _, value := range []string{"93.184.216.34", "2606:4700::1111"} {
		if !Public(netip.MustParseAddr(value)) {
			t.Errorf("%s should be public", value)
		}
	}
}
