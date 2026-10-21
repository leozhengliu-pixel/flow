package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"flow/api/internal/coding"
	appconfig "flow/api/internal/config"
	"flow/api/internal/sandbox"
)

// fakeEgressProxy models the Squid configuration that deploy/egress-proxy
// renders in helper mode, with the real `flow-api egress-acl` helper logic:
//
//   - no or undecodable Proxy-Authorization, or an auth helper answer other
//     than OK (ERR, or BH when Flow cannot decide): 407
//   - the check helper denies the destination: 403
//   - the destination cannot be reached (Squid ERR_CONNECT_FAIL): 503
//   - otherwise the upstream's own status is passed through
//
// Helper requests alternate between the given Flow replicas, which share one
// token store, as the replicas behind FLOW_EGRESS_FLOW_URL do.
type fakeEgressProxy struct {
	auth, check []*egressACLHelper
	routes      map[string]string // destination host -> test address
	next        atomic.Int32
}

// squidEscape escapes a helper field the way Squid does (no spaces).
func squidEscape(value string) string { return url.PathEscape(value) }

func (p *fakeEgressProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	replica := int(p.next.Add(1)) % len(p.auth)
	header := r.Header.Get("Proxy-Authorization")
	user, password, ok := parseProxyBasicAuth(header)
	if !ok || !strings.HasPrefix(p.auth[replica].answer(ctx, squidEscape(user)+" "+squidEscape(password)), "OK") {
		w.Header().Set("Proxy-Authenticate", `Basic realm="flow-egress"`)
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	host, port := r.URL.Hostname(), r.URL.Port()
	if port == "" {
		port = "80"
	}
	if answer := p.check[replica].answer(ctx, squidEscape(header)+" "+host+" "+port+" "+r.Method); !strings.HasPrefix(answer, "OK") {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}
	target, ok := p.routes[host]
	if !ok {
		http.Error(w, "connect failed", http.StatusServiceUnavailable)
		return
	}
	upstream, err := http.Get("http://" + target + r.URL.Path)
	if err != nil {
		http.Error(w, "connect failed", http.StatusServiceUnavailable)
		return
	}
	defer upstream.Body.Close()
	w.WriteHeader(upstream.StatusCode)
	_, _ = io.Copy(w, upstream.Body)
}

// egressFlowReplica serves the egress-check endpoint with the decider.
func egressFlowReplica(t *testing.T, decider egressDecider, calls *atomic.Int32) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !egressSecretMatches(r, "acl-secret") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var request EgressCheckRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		response, err := decider.Decide(r.Context(), request)
		if err != nil {
			http.Error(w, "egress check failed", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, response)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func closedAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	return addr
}

// TestEgressProxySandboxIdentity is the deterministic regression for
// dependency downloads answering 407: the proxy URL in the container
// environment has no identity, the per-sandbox identity the runner adds is
// accepted, and identities cannot be reused across sandboxes.
func TestEgressProxySandboxIdentity(t *testing.T) {
	decider := testEgressDecider()
	store := decider.store.(fakeEgressStore)
	// Tokens are opaque to the proxy; this one needs every kind of escaping.
	const special = "fcs_p@ss:w/rd%41 +?#&=é"
	const rotated = "fcs_rotated"
	store.grants[special] = coding.SandboxGrant{WorkspaceKey: "ws", SandboxID: "sb-special", SessionID: "s-default", Kind: sandbox.KindSession}
	store.grants[rotated] = coding.SandboxGrant{WorkspaceKey: "ws", SandboxID: "sb-special", SessionID: "s-default", Kind: sandbox.KindSession}

	var replicaA, replicaB atomic.Int32
	flows := []string{egressFlowReplica(t, decider, &replicaA), egressFlowReplica(t, decider, &replicaB)}
	proxy := &fakeEgressProxy{routes: map[string]string{}}
	for _, flow := range flows {
		env := egressEnv(map[string]string{"FLOW_EGRESS_FLOW_URL": flow, "FLOW_CODING_EGRESS_ACL_SECRET": "acl-secret"})
		auth, err := newEgressACLHelper([]string{"auth"}, env)
		if err != nil {
			t.Fatal(err)
		}
		check, err := newEgressACLHelper([]string{"check"}, env)
		if err != nil {
			t.Fatal(err)
		}
		proxy.auth, proxy.check = append(proxy.auth, auth), append(proxy.check, check)
	}
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "tarball") }))
	t.Cleanup(registry.Close)
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	t.Cleanup(broken.Close)
	proxy.routes["registry.npmjs.org"] = strings.TrimPrefix(registry.URL, "http://")
	proxy.routes["proxy.golang.org"] = strings.TrimPrefix(broken.URL, "http://")
	proxy.routes["pypi.org"] = closedAddr(t)
	proxyServer := httptest.NewServer(proxy)
	t.Cleanup(proxyServer.Close)

	// The container environment, as every provider renders it.
	runtimeConfig := appconfig.CodingConfig{RuntimeURL: "http://flow:8090"}
	baseProxy := ""
	for _, item := range sandbox.RunnerEnv(runtimeConfig, sandbox.SandboxSpec{ID: "sb-special", Kind: sandbox.KindSession, Token: special, ProxyURL: proxyServer.URL}, "session") {
		if item.Name == "HTTPS_PROXY" {
			baseProxy = item.Value
		}
	}
	if baseProxy != proxyServer.URL {
		t.Fatalf("HTTPS_PROXY = %q", baseProxy)
	}
	// withIdentity is the runner's encoding (structured URL API, never
	// string concatenation): sandbox-runner/src/proxy-env.ts.
	withIdentity := func(id, token string) *url.URL {
		parsed, err := url.Parse(baseProxy)
		if err != nil {
			t.Fatal(err)
		}
		if id != "" {
			parsed.User = url.UserPassword(id, token)
		}
		return parsed
	}
	get := func(proxyURL *url.URL, target string) int {
		t.Helper()
		client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
		response, err := client.Get(target)
		if err != nil {
			t.Fatalf("GET %s: %v", target, err)
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, response.Body)
		return response.StatusCode
	}
	const pkg = "http://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz"
	for _, test := range []struct {
		name      string
		id, token string
		target    string
		want      int
	}{
		{"no identity (the 407 regression)", "", "", pkg, http.StatusProxyAuthRequired},
		{"sandbox identity with escaped token", "sb-special", special, pkg, http.StatusOK},
		{"same request on the other replica", "sb-special", special, pkg, http.StatusOK},
		{"rotated token for the same sandbox", "sb-special", rotated, pkg, http.StatusOK},
		{"token reused under another sandbox", "sb-default", special, pkg, http.StatusProxyAuthRequired},
		{"another sandbox's token under this identity", "sb-special", "fcs_default", pkg, http.StatusProxyAuthRequired},
		{"unknown token", "sb-special", "fcs_unknown", pkg, http.StatusProxyAuthRequired},
		{"unbound warm sandbox", "sb-warm", "fcs_warm", pkg, http.StatusProxyAuthRequired},
		{"debug sandbox", "sb-open", "fcs_open", pkg, http.StatusOK},
		{"snapshot sandbox in none mode", "sb-none", "fcs_none", pkg, http.StatusForbidden},
		{"destination not allowlisted", "sb-special", special, "http://api.anthropic.com/v1/messages", http.StatusForbidden},
		{"destination unreachable (egress 503)", "sb-special", special, "http://pypi.org/simple/requests/", http.StatusServiceUnavailable},
		{"upstream failure passes through", "sb-special", special, "http://proxy.golang.org/x/mod/@v/list", http.StatusBadGateway},
	} {
		if got := get(withIdentity(test.id, test.token), test.target); got != test.want {
			t.Errorf("%s: status %d, want %d", test.name, got, test.want)
		}
	}
	if replicaA.Load() == 0 || replicaB.Load() == 0 {
		t.Fatalf("both replicas must answer: %d %d", replicaA.Load(), replicaB.Load())
	}

	// Warm-pool binding: the same sandbox and token become usable once bound.
	store.grants["fcs_warm"] = coding.SandboxGrant{WorkspaceKey: "ws", SandboxID: "sb-warm", SessionID: "s-default", Kind: sandbox.KindWarm}
	if got := get(withIdentity("sb-warm", "fcs_warm"), pkg); got != http.StatusOK {
		t.Errorf("bound warm sandbox: status %d", got)
	}
	// Rotation: once the old token expires (or the sandbox stops and its
	// tokens are revoked) only the new one works.
	delete(store.grants, special)
	if got := get(withIdentity("sb-special", special), pkg); got != http.StatusProxyAuthRequired {
		t.Errorf("expired token: status %d", got)
	}
	if got := get(withIdentity("sb-special", rotated), pkg); got != http.StatusOK {
		t.Errorf("current token after rotation: status %d", got)
	}
}

// Squid hands the basic-auth helper URL-escaped fields; the helper must
// recover exactly the identity the runner encoded.
func TestEgressACLAuthDecodesEscapedIdentity(t *testing.T) {
	var got EgressCheckRequest
	flow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		writeJSON(w, http.StatusOK, EgressCheckResponse{Allow: true, TTLSeconds: 60})
	}))
	t.Cleanup(flow.Close)
	helper, err := newEgressACLHelper([]string{"auth"}, egressEnv(map[string]string{"FLOW_EGRESS_FLOW_URL": flow.URL, "FLOW_CODING_EGRESS_ACL_SECRET": "acl-secret"}))
	if err != nil {
		t.Fatal(err)
	}
	const token = "fcs_a b%2F:@/é"
	if answer := helper.answer(context.Background(), squidEscape("01jabc")+" "+squidEscape(token)); answer != "OK" {
		t.Fatalf("answer %q", answer)
	}
	if got.SandboxID != "01jabc" || got.SandboxToken != token {
		t.Fatalf("decoded identity mismatch (lengths %d/%d)", len(got.SandboxID), len(got.SandboxToken))
	}
}
