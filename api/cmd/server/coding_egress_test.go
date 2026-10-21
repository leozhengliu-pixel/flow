package main

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/codehost"
	"flow/api/internal/coding"
	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
)

type fakeEgressStore struct {
	grants       map[string]coding.SandboxGrant
	sessions     map[string]domain.CodingSession
	environments map[string]domain.CodingEnvironment
	snapshots    map[string]domain.CodingEnvironmentSnapshot
	failAuth     error
}

func (f fakeEgressStore) AuthenticateSandboxToken(_ context.Context, token string) (coding.SandboxGrant, error) {
	if f.failAuth != nil {
		return coding.SandboxGrant{}, f.failAuth
	}
	grant, ok := f.grants[token]
	if !ok {
		return coding.SandboxGrant{}, coding.ErrTokenInvalid
	}
	return grant, nil
}

func (f fakeEgressStore) CodingSession(_ context.Context, _, id string) (domain.CodingSession, error) {
	session, ok := f.sessions[id]
	if !ok {
		return domain.CodingSession{}, coding.ErrNotFound
	}
	return session, nil
}

func (f fakeEgressStore) CodingEnvironment(_ context.Context, _, id string) (domain.CodingEnvironment, error) {
	environment, ok := f.environments[id]
	if !ok {
		return domain.CodingEnvironment{}, coding.ErrNotFound
	}
	return environment, nil
}

func (f fakeEgressStore) CodingSnapshot(_ context.Context, _, id string) (domain.CodingEnvironmentSnapshot, error) {
	snapshot, ok := f.snapshots[id]
	if !ok {
		return domain.CodingEnvironmentSnapshot{}, coding.ErrNotFound
	}
	return snapshot, nil
}

type fakeEgressHost struct {
	codehost.Stub
	hosts []string
	err   error
}

func (f fakeEgressHost) EgressHosts(context.Context, string) ([]string, error) { return f.hosts, f.err }

func testEgressDecider() egressDecider {
	store := fakeEgressStore{
		grants: map[string]coding.SandboxGrant{
			"fcs_default":  {WorkspaceKey: "ws", SandboxID: "sb-default", SessionID: "s-default", Kind: sandbox.KindSession},
			"fcs_extended": {WorkspaceKey: "ws", SandboxID: "sb-extended", SessionID: "s-extended", Kind: sandbox.KindSession},
			"fcs_open":     {WorkspaceKey: "ws", SandboxID: "sb-open", EnvironmentID: "env-open", Kind: sandbox.KindDebug},
			"fcs_none":     {WorkspaceKey: "ws", SandboxID: "sb-none", SnapshotID: "snap-none", Kind: sandbox.KindSnapshot},
			"fcs_warm":     {WorkspaceKey: "ws", SandboxID: "sb-warm", Kind: sandbox.KindWarm},
			"fcs_bound":    {WorkspaceKey: "ws", SandboxID: "sb-bound", SessionID: "s-default", Kind: sandbox.KindWarm},
			"fcs_orphan":   {WorkspaceKey: "ws", SandboxID: "sb-orphan", SessionID: "s-missing", Kind: sandbox.KindSession},
		},
		sessions: map[string]domain.CodingSession{
			"s-default":  {ID: "s-default", EnvironmentID: "env-default"},
			"s-extended": {ID: "s-extended", EnvironmentID: "env-extended"},
		},
		environments: map[string]domain.CodingEnvironment{
			"env-default":  {ID: "env-default", CodeHost: domain.CodeHostGitHub, HostName: "github.com"},
			"env-extended": {ID: "env-extended", CodeHost: domain.CodeHostGitHub, HostName: "github.com", Network: domain.CodingNetworkExtended, AllowedDomains: []string{".example.org", "api.stripe.com"}},
			"env-open":     {ID: "env-open", CodeHost: domain.CodeHostGitLab, HostName: "gitlab.example.com", Network: domain.CodingNetworkOpen},
			"env-none":     {ID: "env-none", CodeHost: domain.CodeHostGitLab, HostName: "gitlab.example.com", Network: domain.CodingNetworkNone},
		},
		snapshots: map[string]domain.CodingEnvironmentSnapshot{"snap-none": {ID: "snap-none", EnvironmentID: "env-none"}},
	}
	config := appconfig.CodingConfig{
		Enabled:    true,
		RuntimeURL: "https://flow.example.com",
		Egress:     appconfig.CodingEgressConfig{DefaultAllowlist: true, Allowlist: []string{".corp-mirror.example.net"}, ACLSecret: "acl-secret"},
		GitHubApp:  appconfig.GitHubAppConfig{AppID: "1", PrivateKey: "pem", WebhookSecret: "hook", Host: "ghe.example.com", APIURL: "https://ghe.example.com/api/v3"},
	}
	return egressDecider{
		config: config,
		store:  store,
		hosts: map[codehost.Kind]codehost.Host{
			domain.CodeHostGitHub: fakeEgressHost{Stub: codehost.Stub{HostKind: domain.CodeHostGitHub}},
			domain.CodeHostGitLab: fakeEgressHost{Stub: codehost.Stub{HostKind: domain.CodeHostGitLab}, hosts: []string{"gitlab.example.com", "gitlab-registry.example.com"}},
		},
	}
}

func TestEgressDecide(t *testing.T) {
	decider := testEgressDecider()
	for _, test := range []struct {
		name   string
		req    EgressCheckRequest
		allow  bool
		reason string
		mode   string
	}{
		{"auth only", EgressCheckRequest{SandboxID: "sb-default", SandboxToken: "fcs_default"}, true, egressReasonAuthenticated, "default"},
		{"auth unknown token", EgressCheckRequest{SandboxID: "sb-default", SandboxToken: "fcs_nope"}, false, egressReasonInvalidToken, ""},
		{"auth empty token", EgressCheckRequest{SandboxID: "sb-default"}, false, egressReasonInvalidToken, ""},
		{"sandbox mismatch", EgressCheckRequest{SandboxID: "sb-other", SandboxToken: "fcs_default", Host: "github.com", Port: 443}, false, egressReasonSandboxMismatch, ""},
		{"another sandbox's token under its own id", EgressCheckRequest{SandboxID: "sb-extended", SandboxToken: "fcs_default", Host: "github.com", Port: 443}, false, egressReasonSandboxMismatch, ""},
		{"missing identity", EgressCheckRequest{SandboxToken: "fcs_default", Host: "github.com", Port: 443}, false, egressReasonSandboxMismatch, ""},
		{"auth missing identity", EgressCheckRequest{SandboxToken: "fcs_default"}, false, egressReasonSandboxMismatch, ""},
		{"warm unbound", EgressCheckRequest{SandboxID: "sb-warm", SandboxToken: "fcs_warm", Host: "github.com", Port: 443}, false, egressReasonWarmUnbound, ""},
		{"warm bound", EgressCheckRequest{SandboxID: "sb-bound", SandboxToken: "fcs_bound", Host: "github.com", Port: 443}, true, egressReasonAllowed, "default"},
		{"default registry", EgressCheckRequest{SandboxID: "sb-default", SandboxToken: "fcs_default", Host: "registry.npmjs.org", Port: 443}, true, egressReasonAllowed, "default"},
		{"default upper case and dot", EgressCheckRequest{SandboxID: "sb-default", SandboxToken: "fcs_default", Host: "PyPI.org.", Port: 443}, true, egressReasonAllowed, "default"},
		{"default extra allowlist subdomain", EgressCheckRequest{SandboxID: "sb-default", SandboxToken: "fcs_default", Host: "npm.corp-mirror.example.net", Port: 443}, true, egressReasonAllowed, "default"},
		{"default flow runtime", EgressCheckRequest{SandboxID: "sb-default", SandboxToken: "fcs_default", Host: "flow.example.com", Port: 443}, true, egressReasonAllowed, "default"},
		{"default github app host", EgressCheckRequest{SandboxID: "sb-default", SandboxToken: "fcs_default", Host: "ghe.example.com", Port: 443}, true, egressReasonAllowed, "default"},
		{"default other gitlab host", EgressCheckRequest{SandboxID: "sb-default", SandboxToken: "fcs_default", Host: "gitlab-registry.example.com", Port: 443}, true, egressReasonAllowed, "default"},
		{"default model api blocked", EgressCheckRequest{SandboxID: "sb-default", SandboxToken: "fcs_default", Host: "api.anthropic.com", Port: 443}, false, egressReasonNotAllowlisted, "default"},
		{"default subdomain not wildcard", EgressCheckRequest{SandboxID: "sb-default", SandboxToken: "fcs_default", Host: "evil.pypi.org", Port: 443}, false, egressReasonNotAllowlisted, "default"},
		{"default port", EgressCheckRequest{SandboxID: "sb-default", SandboxToken: "fcs_default", Host: "github.com", Port: 22}, false, egressReasonPort, "default"},
		{"default metadata ip", EgressCheckRequest{SandboxID: "sb-default", SandboxToken: "fcs_default", Host: "169.254.169.254", Port: 80}, false, egressReasonPrivate, "default"},
		{"default metadata name", EgressCheckRequest{SandboxID: "sb-default", SandboxToken: "fcs_default", Host: "metadata.google.internal", Port: 80}, false, egressReasonPrivate, "default"},
		{"default kubernetes service", EgressCheckRequest{SandboxID: "sb-default", SandboxToken: "fcs_default", Host: "kubernetes.default.svc", Port: 443}, false, egressReasonPrivate, "default"},
		{"default invalid host", EgressCheckRequest{SandboxID: "sb-default", SandboxToken: "fcs_default", Host: "exa mple.com", Port: 443}, false, egressReasonInvalidHost, "default"},
		{"extended allowed domain", EgressCheckRequest{SandboxID: "sb-extended", SandboxToken: "fcs_extended", Host: "cdn.example.org", Port: 443}, true, egressReasonAllowed, "extended"},
		{"extended exact domain", EgressCheckRequest{SandboxID: "sb-extended", SandboxToken: "fcs_extended", Host: "api.stripe.com", Port: 443}, true, egressReasonAllowed, "extended"},
		{"extended keeps default", EgressCheckRequest{SandboxID: "sb-extended", SandboxToken: "fcs_extended", Host: "crates.io", Port: 443}, true, egressReasonAllowed, "extended"},
		{"default lacks extended domains", EgressCheckRequest{SandboxID: "sb-default", SandboxToken: "fcs_default", Host: "api.stripe.com", Port: 443}, false, egressReasonNotAllowlisted, "default"},
		{"open public host", EgressCheckRequest{SandboxID: "sb-open", SandboxToken: "fcs_open", Host: "example.com", Port: 443}, true, egressReasonAllowed, "open"},
		{"open public ip literal", EgressCheckRequest{SandboxID: "sb-open", SandboxToken: "fcs_open", Host: "93.184.216.34", Port: 443}, false, egressReasonNotAllowlisted, "open"},
		{"open private ip", EgressCheckRequest{SandboxID: "sb-open", SandboxToken: "fcs_open", Host: "10.0.0.5", Port: 443}, false, egressReasonPrivate, "open"},
		{"open mapped private ip", EgressCheckRequest{SandboxID: "sb-open", SandboxToken: "fcs_open", Host: "[::ffff:127.0.0.1]", Port: 443}, false, egressReasonPrivate, "open"},
		{"open single label", EgressCheckRequest{SandboxID: "sb-open", SandboxToken: "fcs_open", Host: "redis", Port: 443}, false, egressReasonPrivate, "open"},
		{"none code host", EgressCheckRequest{SandboxID: "sb-none", SandboxToken: "fcs_none", Host: "gitlab.example.com", Port: 443}, true, egressReasonAllowed, "none"},
		{"none code host egress hosts", EgressCheckRequest{SandboxID: "sb-none", SandboxToken: "fcs_none", Host: "gitlab-registry.example.com", Port: 443}, true, egressReasonAllowed, "none"},
		{"none flow", EgressCheckRequest{SandboxID: "sb-none", SandboxToken: "fcs_none", Host: "flow.example.com", Port: 443}, true, egressReasonAllowed, "none"},
		{"none registry blocked", EgressCheckRequest{SandboxID: "sb-none", SandboxToken: "fcs_none", Host: "registry.npmjs.org", Port: 443}, false, egressReasonNotAllowlisted, "none"},
		{"missing session uses default", EgressCheckRequest{SandboxID: "sb-orphan", SandboxToken: "fcs_orphan", Host: "proxy.golang.org", Port: 443}, true, egressReasonAllowed, "default"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := decider.Decide(context.Background(), test.req)
			if err != nil {
				t.Fatal(err)
			}
			if got.Allow != test.allow || got.Reason != test.reason || got.Mode != test.mode || got.TTLSeconds != 60 {
				t.Fatalf("got %+v, want allow=%t reason=%s mode=%s", got, test.allow, test.reason, test.mode)
			}
		})
	}
}

func TestEgressDecideDefaultAllowlistOff(t *testing.T) {
	decider := testEgressDecider()
	decider.config.Egress.DefaultAllowlist = false
	request := EgressCheckRequest{SandboxID: "sb-default", SandboxToken: "fcs_default", Port: 443}
	for host, want := range map[string]bool{"registry.npmjs.org": false, "github.com": true, "x.corp-mirror.example.net": true} {
		request.Host = host
		got, err := decider.Decide(context.Background(), request)
		if err != nil || got.Allow != want {
			t.Errorf("%s: got %+v %v, want allow=%t", host, got, err, want)
		}
	}
}

func TestEgressDecideErrors(t *testing.T) {
	decider := testEgressDecider()
	store := decider.store.(fakeEgressStore)
	store.failAuth = errors.New("database down")
	decider.store = store
	if _, err := decider.Decide(context.Background(), EgressCheckRequest{SandboxToken: "fcs_default", Host: "github.com"}); err == nil {
		t.Fatal("store failure must be an error, not a decision")
	}
	decider = testEgressDecider()
	decider.hosts[domain.CodeHostGitLab] = fakeEgressHost{Stub: codehost.Stub{HostKind: domain.CodeHostGitLab}, err: errors.New("gitlab down")}
	if _, err := decider.Decide(context.Background(), EgressCheckRequest{SandboxID: "sb-default", SandboxToken: "fcs_default", Host: "github.com", Port: 443}); err == nil {
		t.Fatal("code host failure must be an error")
	}
}

func TestNormalizeEgressEntry(t *testing.T) {
	for raw, want := range map[string]string{
		"Example.COM":               "example.com",
		"*.example.com":             ".example.com",
		".example.com":              ".example.com",
		"https://flow.example.com/": "flow.example.com",
		"registry.local:5000":       "registry.local",
		"":                          "",
		"bad host":                  "",
	} {
		if got := normalizeEgressEntry(raw); got != want {
			t.Errorf("normalizeEgressEntry(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestCodingEgressCheckHandler(t *testing.T) {
	s := &server{}
	s.coding = &codingServices{config: appconfig.CodingConfig{Enabled: true}}
	call := func(auth, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/internal/coding-runtime/egress-check", strings.NewReader(body))
		if auth != "" {
			request.Header.Set("Authorization", auth)
		}
		recorder := httptest.NewRecorder()
		s.codingEgressCheck(recorder, request)
		return recorder
	}
	if got := call("Bearer x", `{}`).Code; got != http.StatusNotFound {
		t.Fatalf("no secret configured: got %d", got)
	}
	s.coding.config.Egress.ACLSecret = "acl-secret"
	for _, auth := range []string{"", "Bearer wrong", "Basic acl-secret", "Bearer "} {
		if got := call(auth, `{}`).Code; got != http.StatusUnauthorized {
			t.Fatalf("auth %q: got %d", auth, got)
		}
	}
	if got := call("Bearer acl-secret", `{`).Code; got != http.StatusBadRequest {
		t.Fatalf("bad body: got %d", got)
	}
	s.coding.config.Enabled = false
	if got := call("Bearer acl-secret", `{}`).Code; got != http.StatusNotFound {
		t.Fatalf("coding disabled: got %d", got)
	}
}

// The built-in allowlist is rendered by the Helm chart and baked into the
// egress proxy image; all three copies must match.
func TestDefaultEgressAllowlistMatchesDeployFiles(t *testing.T) {
	want := defaultEgressAllowlist()
	for _, path := range []string{
		filepath.Join("..", "..", "..", "deploy", "egress-proxy", "allowlist.default.txt"),
		filepath.Join("..", "..", "..", "deploy", "helm", "flow", "files", "allowlist-default.txt"),
	} {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" && !strings.HasPrefix(line, "#") {
				got = append(got, line)
			}
		}
		file.Close()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s differs from defaultEgressAllowlist():\n got %v\nwant %v", path, got, want)
		}
	}
}
