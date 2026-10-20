package main

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
	"flow/api/internal/outbound"
	"flow/api/internal/store"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// intranetDNS resolves test host names to fixed addresses.
type intranetDNS map[string]string

func (d intranetDNS) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	if value, ok := d[host]; ok {
		return []net.IPAddr{{IP: net.ParseIP(value)}}, nil
	}
	return nil, errors.New("no such host")
}

// useOutboundPolicy installs policy (with fake DNS, and dials routed to
// target when set) for the test.
func useOutboundPolicy(t *testing.T, cfg outbound.Config, dns intranetDNS, target *httptest.Server) {
	t.Helper()
	policy, err := outbound.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	policy = policy.WithResolver(dns)
	if target != nil {
		address := target.Listener.Addr().String()
		policy = policy.WithDialer(func(ctx context.Context, network, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, address)
		})
	}
	previous := outboundPolicy
	outboundPolicy = policy
	t.Cleanup(func() { outboundPolicy = previous })
}

func sourcebotPolicyRequest(url string) map[string]any {
	return map[string]any{"id": "mcp-sourcebot-production", "name": "Sourcebot", "kind": "mcp", "url": url, "shared": true, "status": "approved"}
}

// The production incident: an intranet MCP (10.218.161.13) is refused
// without configuration, with a message naming the operator settings, and
// accepted once the operator trusts both the host and its network.
func TestApplicationPolicyAcceptsIntranetMCPOnlyWithOperatorTrust(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if err := repo.MutateWorkspace(t.Context(), "test-workspace", "test", "settings", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.MCPConnectorsEnabled = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	handler := newHandler(&server{store: repo, authDisabled: true})
	dns := intranetDNS{"hsb.haier.net": "10.218.161.13", "metadata.corp.example.com": "169.254.169.254"}
	put := func(url string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(sourcebotPolicyRequest(url))
		request := httptest.NewRequest(http.MethodPut, "/api/application-policies?workspace=test-workspace", strings.NewReader(string(raw)))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}

	useOutboundPolicy(t, outbound.Config{}, dns, nil)
	rec := put("https://hsb.haier.net/api/mcp")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "FLOW_TRUSTED_PRIVATE_ENDPOINTS and FLOW_TRUSTED_PRIVATE_NETWORKS") || strings.Contains(rec.Body.String(), "10.218") {
		t.Fatalf("unconfigured: %d %s", rec.Code, rec.Body.String())
	}

	// Host listed but its network not: still refused.
	useOutboundPolicy(t, outbound.Config{TrustedHosts: []string{"hsb.haier.net"}, TrustedNetworks: []string{"10.1.0.0/16"}}, dns, nil)
	if rec := put("https://hsb.haier.net/api/mcp"); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "outside FLOW_TRUSTED_PRIVATE_NETWORKS") {
		t.Fatalf("network not trusted: %d %s", rec.Code, rec.Body.String())
	}

	useOutboundPolicy(t, outbound.Config{TrustedHosts: []string{"hsb.haier.net", "*.corp.example.com"}, TrustedNetworks: []string{"10.218.0.0/16", "169.254.0.0/16"}}, dns, nil)
	if rec := put("https://hsb.haier.net/api/mcp"); rec.Code != http.StatusOK {
		t.Fatalf("configured: %d %s", rec.Code, rec.Body.String())
	}
	// Plain HTTP and metadata addresses stay refused even when listed.
	if rec := put("http://hsb.haier.net/api/mcp"); rec.Code != http.StatusBadRequest {
		t.Fatalf("plain HTTP: %d %s", rec.Code, rec.Body.String())
	}
	if rec := put("https://metadata.corp.example.com/mcp"); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "never allowed") {
		t.Fatalf("metadata: %d %s", rec.Code, rec.Body.String())
	}
}

// fakeIntranetMCP is an MCP server reachable only as https://example.com
// (an "intranet" address in the tests) that requires its bearer token.
type fakeIntranetMCP struct {
	*httptest.Server
	mu    sync.Mutex
	auth  []string
	calls []string
}

func newFakeIntranetMCP(t *testing.T, token string) *fakeIntranetMCP {
	f := &fakeIntranetMCP{}
	remote := mcp.NewServer(&mcp.Implementation{Name: "Sourcebot", Version: "1"}, nil)
	mcp.AddTool(remote, &mcp.Tool{Name: "search_code", Description: "Search code across repositories"}, func(_ context.Context, _ *mcp.CallToolRequest, input struct {
		Query string `json:"query"`
	}) (*mcp.CallToolResult, any, error) {
		f.mu.Lock()
		f.calls = append(f.calls, input.Query)
		f.mu.Unlock()
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "found: " + input.Query}}}, nil, nil
	})
	// The listener is loopback; the Host is the intranet name.
	sdk := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, &mcp.StreamableHTTPOptions{DisableLocalhostProtection: true})
	f.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		sdk.ServeHTTP(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeIntranetMCP) caFile(t *testing.T) string {
	path := filepath.Join(t.TempDir(), "enterprise-ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mcpRPC(id int, method string, params any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
}

// A coding session's harness uses an approved intranet MCP server through
// Flow's gateway: the bootstrap lists it, calls are forwarded with the
// deployment's credential over the operator-trusted route and audited, and
// the gateway refuses unapproved servers, foreign tokens and ended sessions.
func TestCodingMCPGatewayForwardsWithServerSideCredential(t *testing.T) {
	e := newCodingE2E(t)
	ctx := context.Background()
	const endpoint = "https://example.com/api/mcp"
	upstream := newFakeIntranetMCP(t, "sb-server-secret")
	useOutboundPolicy(t, outbound.Config{TrustedHosts: []string{"example.com"}, TrustedNetworks: []string{"10.218.0.0/16"}, CAFile: upstream.caFile(t)}, intranetDNS{"example.com": "10.218.161.13"}, upstream.Server)
	t.Setenv("FLOW_MCP_CREDENTIALS", `{"mcp-sourcebot-production":{"url":"`+endpoint+`","token":"sb-server-secret"}}`)

	if err := mutateTestWorkspace(t, e.repo, func(data *domain.Bootstrap) {
		data.WorkspaceSettings.MCPConnectorsEnabled = true
		data.WorkspaceSettings.AllowedMCPConnectors = "approved"
	}); err != nil {
		t.Fatal(err)
	}
	policy := sourcebotPolicyRequest(endpoint)
	policy["codingSessions"] = true
	e.decode(e.do(http.MethodPut, "/api/application-policies", policy, "", nil), http.StatusOK, nil)
	// An approved connector that is not enabled for coding sessions, and a
	// pending one that is: neither reaches the sandbox.
	e.decode(e.do(http.MethodPut, "/api/application-policies", map[string]any{"id": "mcp-chat-only", "name": "Chat only", "kind": "mcp", "url": endpoint, "shared": true, "status": "approved"}, "", nil), http.StatusOK, nil)
	e.decode(e.do(http.MethodPut, "/api/application-policies", map[string]any{"id": "mcp-pending", "name": "Pending", "kind": "mcp", "url": endpoint, "shared": true, "status": "pending", "codingSessions": true}, "", nil), http.StatusOK, nil)

	e.installGitHubApp()
	if _, err := e.s.codingStore().CreateCodingEnvironment(ctx, domain.CodingEnvironment{WorkspaceKey: "test-workspace", Name: "Web", CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: "acme/web", DefaultBranch: "main", SandboxSize: domain.CodingSizeMedium}); err != nil {
		t.Fatal(err)
	}
	issue := e.createIssue("Find where checkout totals are computed")
	agentUser := e.s.builtinAgentUserID(ctx, "test-workspace")
	requestJSON[domain.Issue](t, e.handler, http.MethodPatch, "/api/issue-records/"+issue.ID, map[string]any{"delegateId": agentUser}, http.StatusOK)
	var started coding.TriggerResult
	e.decode(e.do(http.MethodPost, "/api/issues/"+issue.ID+"/coding-session", map[string]any{}, "", nil), http.StatusCreated, &started)
	session := e.drive(started.Session.ID, domain.CodingPhaseStarting)
	token := e.provider.token(session.SandboxID)

	var boot coding.BootstrapResponse
	e.decode(e.do(http.MethodGet, "/internal/coding-runtime/bootstrap", nil, token, nil), http.StatusOK, &boot)
	gateway := "http://flow.test/internal/coding-runtime/mcp/mcp-sourcebot-production"
	if len(boot.MCPServers) != 1 || boot.MCPServers[0] != (coding.BootstrapMCPServer{Name: "sourcebot", Title: "Sourcebot", URL: gateway}) {
		t.Fatalf("bootstrap MCP servers %+v", boot.MCPServers)
	}
	if !strings.Contains(boot.Guidance, "`sourcebot`: Sourcebot") {
		t.Fatalf("guidance does not mention the server: %s", boot.Guidance)
	}
	if raw, _ := json.Marshal(boot); strings.Contains(string(raw), "sb-server-secret") || strings.Contains(string(raw), "example.com") {
		t.Fatalf("the bootstrap must carry neither the credential nor the upstream URL: %s", raw)
	}

	path := "/internal/coding-runtime/mcp/mcp-sourcebot-production"
	var initialized struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	e.decode(e.do(http.MethodPost, path, mcpRPC(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "claude-code", "version": "2"}}), token, nil), http.StatusOK, &initialized)
	if initialized.Result.ProtocolVersion != "2025-06-18" {
		t.Fatalf("initialize: %+v", initialized)
	}
	if rec := e.do(http.MethodPost, path, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}, token, nil); rec.Code != http.StatusAccepted {
		t.Fatalf("notification: %d %s", rec.Code, rec.Body.String())
	}
	var listed struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	e.decode(e.do(http.MethodPost, path, mcpRPC(2, "tools/list", map[string]any{}), token, nil), http.StatusOK, &listed)
	if len(listed.Result.Tools) != 1 || listed.Result.Tools[0].Name != "search_code" {
		t.Fatalf("tools/list: %+v", listed)
	}
	call := e.do(http.MethodPost, path, mcpRPC(3, "tools/call", map[string]any{"name": "search_code", "arguments": map[string]any{"query": "checkoutTotal"}}), token, nil)
	if call.Code != http.StatusOK || !strings.Contains(call.Body.String(), "found: checkoutTotal") || strings.Contains(call.Body.String(), "sb-server-secret") {
		t.Fatalf("tools/call: %d %s", call.Code, call.Body.String())
	}
	upstream.mu.Lock()
	for _, auth := range upstream.auth {
		if auth != "Bearer sb-server-secret" {
			t.Errorf("upstream saw Authorization %q, want the server-side credential (never the sandbox token)", auth)
		}
	}
	calls := len(upstream.calls)
	upstream.mu.Unlock()
	if calls != 1 {
		t.Fatalf("upstream tool calls: %d", calls)
	}
	audit, _ := e.repo.WorkspaceMetadataFields("test-workspace", "auditLog")
	audited := false
	for _, entry := range audit.AuditLog {
		if entry.Action == "coding_session.mcp_tool_call" && entry.ResourceID == session.ID && entry.Metadata["tool"] == "search_code" && entry.Metadata["mcpServerId"] == "mcp-sourcebot-production" && entry.Metadata["outcome"] == "ok" {
			audited = true
		}
	}
	if !audited {
		t.Fatalf("the tool call was not audited: %+v", audit.AuditLog)
	}

	// Refusals: no or a forged token, a server not enabled for coding
	// sessions, a pending one, an unknown one, and a server streams request.
	for name, attempt := range map[string]struct {
		method, path, token string
		status              int
	}{
		"no token":       {http.MethodPost, path, "", http.StatusUnauthorized},
		"forged token":   {http.MethodPost, path, coding.SandboxTokenPrefix + "forged", http.StatusUnauthorized},
		"chat only":      {http.MethodPost, "/internal/coding-runtime/mcp/mcp-chat-only", token, http.StatusForbidden},
		"pending":        {http.MethodPost, "/internal/coding-runtime/mcp/mcp-pending", token, http.StatusForbidden},
		"unknown":        {http.MethodPost, "/internal/coding-runtime/mcp/mcp-elsewhere", token, http.StatusForbidden},
		"server streams": {http.MethodGet, path, token, http.StatusMethodNotAllowed},
	} {
		if rec := e.do(attempt.method, attempt.path, mcpRPC(9, "tools/list", map[string]any{}), attempt.token, nil); rec.Code != attempt.status {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	// Another workspace's sandbox cannot name this workspace's server.
	foreign := codingSandboxCaller{grant: coding.SandboxGrant{WorkspaceKey: "other-workspace", SessionID: "cs_other"}, session: domain.CodingSession{ID: "cs_other", CreatorID: session.CreatorID}}
	if _, ok := e.s.codingMCPGatewayPolicy(foreign, "mcp-sourcebot-production"); ok {
		t.Fatal("a sandbox of another workspace reached this workspace's MCP server")
	}

	// Revoking the approval applies to the next call.
	policy["codingSessions"] = false
	e.decode(e.do(http.MethodPut, "/api/application-policies", policy, "", nil), http.StatusOK, nil)
	if rec := e.do(http.MethodPost, path, mcpRPC(4, "tools/list", map[string]any{}), token, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("revoked approval: %d %s", rec.Code, rec.Body.String())
	}
	policy["codingSessions"] = true
	e.decode(e.do(http.MethodPut, "/api/application-policies", policy, "", nil), http.StatusOK, nil)
	if rec := e.do(http.MethodPost, path, mcpRPC(5, "tools/list", map[string]any{}), token, nil); rec.Code != http.StatusOK {
		t.Fatalf("re-approved: %d %s", rec.Code, rec.Body.String())
	}

	// The session ends: the gateway stops answering its sandbox.
	if _, err := e.s.codingStore().UpdateCodingSession(ctx, "test-workspace", session.ID, func(current *domain.CodingSession) error {
		now := time.Now()
		current.Phase, current.EndedAt = domain.CodingPhaseDone, &now
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(http.MethodPost, path, mcpRPC(6, "tools/call", map[string]any{"name": "search_code", "arguments": map[string]any{"query": "after end"}}), token, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("ended session: %d %s", rec.Code, rec.Body.String())
	}
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if len(upstream.calls) != 1 {
		t.Fatalf("calls after the session ended reached the server: %v", upstream.calls)
	}
}

func TestCodingMCPServerNames(t *testing.T) {
	for name, want := range map[string]string{"Sourcebot": "sourcebot", "Code Search (prod)": "code-search-prod", "flow": "", "中文": ""} {
		got := codingMCPServerName(applicationPolicy{ID: "mcp_1", Name: name})
		if want == "" {
			if !strings.HasPrefix(got, "mcp-") {
				t.Errorf("%q: %q", name, got)
			}
		} else if got != want {
			t.Errorf("%q: %q, want %q", name, got, want)
		}
	}
}
