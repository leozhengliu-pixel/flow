package opensandbox

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"flow/api/internal/config"
	"flow/api/internal/sandbox"
)

func TestBaseURLAndMetadataQuery(t *testing.T) {
	for raw, want := range map[string]string{
		"http://opensandbox-server.opensandbox-system.svc.cluster.local":     "http://opensandbox-server.opensandbox-system.svc.cluster.local/v1",
		"http://opensandbox-server.opensandbox-system.svc.cluster.local/v1/": "http://opensandbox-server.opensandbox-system.svc.cluster.local/v1",
		"https://gw.example/opensandbox":                                     "https://gw.example/opensandbox/v1",
	} {
		if got := BaseURL(raw); got != want {
			t.Errorf("BaseURL(%q) = %q, want %q", raw, got, want)
		}
	}
	inner := MetadataQuery(map[string]string{"flow.dev/sandbox-id": "abc", "note": "a b&c"})
	parsed, err := url.ParseQuery(inner)
	if err != nil || parsed.Get("flow.dev/sandbox-id") != "abc" || parsed.Get("note") != "a b&c" {
		t.Fatalf("metadata query %q: %v %v", inner, parsed, err)
	}
}

func TestClientRetries(t *testing.T) {
	cases := []struct {
		name     string
		failures []fakeError
		wantErr  bool
		attempts int
		retryAt  time.Duration
	}{
		{name: "503 then ok", failures: []fakeError{{Status: 503, Code: "UNAVAILABLE"}}, attempts: 2},
		{name: "429 honours Retry-After", failures: []fakeError{{Status: 429, Code: "RATE", RetryAfter: 7}}, attempts: 2, retryAt: 7 * time.Second},
		{name: "retries exhausted", failures: []fakeError{{Status: 502}, {Status: 502}, {Status: 502}}, attempts: 3, wantErr: true},
		{name: "400 is not retried", failures: []fakeError{{Status: 400, Code: "INVALID"}}, attempts: 1, wantErr: true},
		{name: "404 is not retried", failures: []fakeError{{Status: 404, Code: "NOT_FOUND"}}, attempts: 1, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, nil)
			h.server.failNext["GET /v1/sandboxes"] = tc.failures
			_, err := h.provider.client.List(context.Background(), nil)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v", err)
			}
			if n := h.server.exact("GET /v1/sandboxes"); n != tc.attempts {
				t.Fatalf("attempts = %d, want %d", n, tc.attempts)
			}
			if tc.retryAt > 0 && (len(h.sleeps) == 0 || h.sleeps[0] != tc.retryAt) {
				t.Fatalf("sleeps = %v", h.sleeps)
			}
		})
	}
}

func TestClientTimeoutIsTransient(t *testing.T) {
	h := newHarness(t, func(c *config.CodingConfig) { c.OpenSandbox.Retries = 1 })
	h.provider.client.requestTimeout = 50 * time.Millisecond
	h.server.slow = time.Second
	_, err := h.provider.Status(context.Background(), "sb1")
	if !errors.Is(err, sandbox.ErrTransient) {
		t.Fatalf("a timeout must be transient: %v", err)
	}
	if n := h.server.exact("GET /v1/sandboxes"); n != 2 {
		t.Fatalf("attempts = %d", n)
	}
	// A cancelled caller is not retried and not reported as transient.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.provider.client.Get(ctx, "x"); err == nil || sandbox.IsTransient(wrap("get", err)) {
		t.Fatalf("cancelled: %v", err)
	}
}

func TestErrorBodies(t *testing.T) {
	h := newHarness(t, nil)
	h.server.failNext["GET /v1/sandboxes/x"] = []fakeError{{Status: 404, Code: "KUBERNETES::SANDBOX_NOT_FOUND", Message: "sandbox x not found"}}
	_, err := h.provider.client.Get(context.Background(), "x")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "KUBERNETES::SANDBOX_NOT_FOUND" || apiErr.RequestID != "req-test" || !IsNotFound(err) {
		t.Fatalf("error: %#v", err)
	}
	if !errors.Is(wrap("get", err), sandbox.ErrNotFound) {
		t.Fatal("404 must map to ErrNotFound")
	}
}

func TestReadEvents(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"init","text":"cmd-1","timestamp":1}`,
		``,
		`{"type":"stdout","text":"hello"}`,
		``,
		`: comment`,
		`data: {"type":"stderr","text":"warn"}`,
		``,
		`data: plain text line`,
		``,
		`{"type":"error","error":{"ename":"CommandExecError","evalue":"3"}}`,
		``,
	}, "\n")
	events := []StreamEvent{}
	if err := ReadEvents(strings.NewReader(stream), func(e StreamEvent) error { events = append(events, e); return nil }); err != nil {
		t.Fatal(err)
	}
	types := []string{}
	for _, e := range events {
		types = append(types, e.Type+":"+e.Text)
	}
	if got := strings.Join(types, ","); got != "init:cmd-1,stdout:hello,stderr:warn,stdout:plain text line,error:" {
		t.Fatalf("events: %s", got)
	}
	if events[4].Error == nil || events[4].Error.EValue != "3" {
		t.Fatalf("error event: %+v", events[4])
	}
}

func TestExecdNotReadyIsRetried(t *testing.T) {
	h := newHarness(t, nil)
	// execd answers only after a few pings (it starts after the Pod is Ready).
	h.server.failNext["GET /v1/sandboxes/sbx-001/proxy/44772/ping"] = []fakeError{{Status: 503}, {Status: 503}, {Status: 503}, {Status: 503}}
	h.start(h.spec("sb1"))
	if status := h.status("sb1"); status.Phase != sandbox.PhaseRunning {
		t.Fatalf("status: %+v", status)
	}
	if n := h.server.exact("GET /v1/sandboxes/sbx-001/proxy/44772/ping"); n != 5 {
		t.Fatalf("pings = %d", n)
	}
}
