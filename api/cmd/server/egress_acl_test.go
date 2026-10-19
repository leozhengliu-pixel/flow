package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func egressTestFlow(t *testing.T, failFirst int32) (*httptest.Server, *[]EgressCheckRequest, *atomic.Int32) {
	t.Helper()
	var requests []EgressCheckRequest
	var calls atomic.Int32
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.URL.Path != "/internal/coding-runtime/egress-check" || r.Header.Get("Authorization") != "Bearer acl-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if n <= failFirst {
			http.Error(w, "boom", http.StatusServiceUnavailable)
			return
		}
		var request EgressCheckRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		mu.Lock()
		requests = append(requests, request)
		mu.Unlock()
		allow := request.SandboxToken == "fcs_good" && (request.Host == "" || request.Host == "github.com")
		reason := "allowed"
		if !allow {
			reason = "not_allowlisted"
		}
		writeJSON(w, http.StatusOK, EgressCheckResponse{Allow: allow, Reason: reason, TTLSeconds: 60})
	}))
	t.Cleanup(server.Close)
	return server, &requests, &calls
}

func egressEnv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func basicHeader(user, password string) string {
	return "Basic%20" + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
}

func TestEgressACLAuthMode(t *testing.T) {
	flow, requests, _ := egressTestFlow(t, 0)
	var stdout, stderr bytes.Buffer
	input := "sb1 fcs_good\nsb1 fcs_bad\nmalformed\n"
	code := runEgressACL([]string{"auth"}, strings.NewReader(input), &stdout, &stderr,
		egressEnv(map[string]string{"FLOW_EGRESS_FLOW_URL": flow.URL + "/", "FLOW_CODING_EGRESS_ACL_SECRET": "acl-secret"}))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	want := "OK\nERR message=\"not_allowlisted\"\nERR message=\"malformed request\"\n"
	if stdout.String() != want {
		t.Fatalf("stdout %q, want %q", stdout.String(), want)
	}
	if len(*requests) != 2 || (*requests)[0].SandboxID != "sb1" || (*requests)[0].Host != "" {
		t.Fatalf("requests %+v", *requests)
	}
}

func TestEgressACLCheckMode(t *testing.T) {
	flow, requests, _ := egressTestFlow(t, 0)
	var stdout, stderr bytes.Buffer
	input := strings.Join([]string{
		basicHeader("sb1", "fcs_good") + " github.com 443 CONNECT",
		basicHeader("sb1", "fcs_good") + " evil.example 443",
		"- github.com 443",
		basicHeader("sb1", "fcs_good") + " github.com notaport",
		"Bearer%20abc github.com 443",
	}, "\n") + "\n"
	code := runEgressACL([]string{"check"}, strings.NewReader(input), &stdout, &stderr,
		egressEnv(map[string]string{"FLOW_CODING_RUNTIME_URL": flow.URL, "FLOW_CODING_EGRESS_ACL_SECRET": "acl-secret"}))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	want := strings.Join([]string{
		"OK",
		`ERR message="not_allowlisted"`,
		`ERR message="missing proxy credentials"`,
		`ERR message="invalid port"`,
		`ERR message="missing proxy credentials"`,
	}, "\n") + "\n"
	if stdout.String() != want {
		t.Fatalf("stdout %q, want %q", stdout.String(), want)
	}
	if len(*requests) != 2 || (*requests)[0].Method != "CONNECT" || (*requests)[0].Port != 443 || (*requests)[0].SandboxToken != "fcs_good" {
		t.Fatalf("requests %+v", *requests)
	}
}

func TestEgressACLConcurrencyChannels(t *testing.T) {
	flow, _, _ := egressTestFlow(t, 0)
	var stdout, stderr bytes.Buffer
	input := "0 " + basicHeader("sb", "fcs_good") + " github.com 443\n1 " + basicHeader("sb", "fcs_good") + " other.example 443\n"
	code := runEgressACL([]string{"check", "--concurrency"}, strings.NewReader(input), &stdout, &stderr,
		egressEnv(map[string]string{"FLOW_APP_URL": flow.URL, "FLOW_CODING_EGRESS_ACL_SECRET": "acl-secret"}))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	sort.Strings(lines)
	if len(lines) != 2 || lines[0] != "0 OK" || lines[1] != `1 ERR message="not_allowlisted"` {
		t.Fatalf("lines %q", lines)
	}
}

func TestEgressACLRetriesThenBrokenHelper(t *testing.T) {
	flow, _, calls := egressTestFlow(t, 1)
	var stdout, stderr bytes.Buffer
	env := egressEnv(map[string]string{"FLOW_EGRESS_FLOW_URL": flow.URL, "FLOW_CODING_EGRESS_ACL_SECRET": "acl-secret"})
	if code := runEgressACL([]string{"auth"}, strings.NewReader("sb fcs_good\n"), &stdout, &stderr, env); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if stdout.String() != "OK\n" || calls.Load() != 2 {
		t.Fatalf("one retry after a 503: stdout %q calls %d", stdout.String(), calls.Load())
	}

	down, _, _ := egressTestFlow(t, 100)
	stdout.Reset()
	env = egressEnv(map[string]string{"FLOW_EGRESS_FLOW_URL": down.URL, "FLOW_CODING_EGRESS_ACL_SECRET": "acl-secret"})
	runEgressACL([]string{"auth"}, strings.NewReader("sb fcs_good\n"), &stdout, &stderr, env)
	if !strings.HasPrefix(stdout.String(), "BH ") {
		t.Fatalf("Flow down must answer BH, got %q", stdout.String())
	}

	stdout.Reset()
	env = egressEnv(map[string]string{"FLOW_EGRESS_FLOW_URL": flow.URL, "FLOW_CODING_EGRESS_ACL_SECRET": "wrong"})
	runEgressACL([]string{"auth"}, strings.NewReader("sb fcs_good\n"), &stdout, &stderr, env)
	if stdout.String() != "BH message=\"flow answered 401\"\n" {
		t.Fatalf("wrong secret: got %q", stdout.String())
	}
}

func TestEgressACLConfiguration(t *testing.T) {
	secretFile := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretFile, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	helper, err := newEgressACLHelper([]string{"auth"}, egressEnv(map[string]string{
		"FLOW_EGRESS_FLOW_URL": "http://flow:8080", "FLOW_CODING_EGRESS_ACL_SECRET": "env", "FLOW_CODING_EGRESS_ACL_SECRET_FILE": secretFile, "FLOW_EGRESS_ACL_TIMEOUT": "2s",
	}))
	if err != nil || helper.secret != "from-file" || helper.endpoint != "http://flow:8080/internal/coding-runtime/egress-check" || helper.client.Timeout.String() != "2s" {
		t.Fatalf("helper %+v err %v", helper, err)
	}
	for name, test := range map[string]struct {
		args []string
		env  map[string]string
	}{
		"no mode":      {nil, map[string]string{"FLOW_EGRESS_FLOW_URL": "http://flow", "FLOW_CODING_EGRESS_ACL_SECRET": "x"}},
		"two modes":    {[]string{"auth", "check"}, map[string]string{"FLOW_EGRESS_FLOW_URL": "http://flow", "FLOW_CODING_EGRESS_ACL_SECRET": "x"}},
		"unknown flag": {[]string{"auth", "--nope"}, map[string]string{"FLOW_EGRESS_FLOW_URL": "http://flow", "FLOW_CODING_EGRESS_ACL_SECRET": "x"}},
		"no url":       {[]string{"auth"}, map[string]string{"FLOW_CODING_EGRESS_ACL_SECRET": "x"}},
		"no secret":    {[]string{"auth"}, map[string]string{"FLOW_EGRESS_FLOW_URL": "http://flow"}},
		"bad timeout":  {[]string{"auth"}, map[string]string{"FLOW_EGRESS_FLOW_URL": "http://flow", "FLOW_CODING_EGRESS_ACL_SECRET": "x", "FLOW_EGRESS_ACL_TIMEOUT": "soon"}},
		"missing file": {[]string{"auth"}, map[string]string{"FLOW_EGRESS_FLOW_URL": "http://flow", "FLOW_CODING_EGRESS_ACL_SECRET_FILE": "/nonexistent/secret"}},
	} {
		var stdout, stderr bytes.Buffer
		if code := runEgressACL(test.args, strings.NewReader(""), &stdout, &stderr, egressEnv(test.env)); code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
	}
}

func TestParseProxyBasicAuth(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("sb:fcs:with:colons"))
	if user, password, ok := parseProxyBasicAuth("basic " + encoded); !ok || user != "sb" || password != "fcs:with:colons" {
		t.Fatalf("got %q %q %t", user, password, ok)
	}
	for _, header := range []string{"", "Basic", "Basic !!!", "Basic " + base64.StdEncoding.EncodeToString([]byte("nopassword")), "Digest abc"} {
		if _, _, ok := parseProxyBasicAuth(header); ok {
			t.Errorf("%q must not parse", header)
		}
	}
	if got := egressHelperReply("ERR", "a \"quoted\"\nline"); got != `ERR message="a 'quoted' line"` {
		t.Fatalf("reply %q", got)
	}
}
