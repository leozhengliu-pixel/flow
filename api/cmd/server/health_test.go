package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/store"

	"github.com/alicebob/miniredis/v2"
)

// healthLog captures the default slog output for one test.
type healthLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *healthLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *healthLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func captureHealthLog(t *testing.T) *healthLog {
	t.Helper()
	captured := &healthLog{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(captured, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return captured
}

func newHealthServer(t *testing.T, coordinator *coordination.Redis) *httptest.Server {
	t.Helper()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "health.db"))
	if err != nil {
		t.Fatal(err)
	}
	if coordinator != nil {
		repository.SetWorkspaceCoordinator(coordinator)
	}
	ctx, cancel := context.WithCancel(context.Background())
	// Auth stays on: the probe is anonymous.
	instance := &server{store: repository, uploadPath: t.TempDir(), coordinator: coordinator, coordinationContext: ctx}
	httpServer := httptest.NewServer(newHandler(instance))
	t.Cleanup(func() {
		httpServer.Close()
		cancel()
		if coordinator != nil {
			_ = coordinator.Close()
		}
		_ = repository.Close()
	})
	return httpServer
}

// probeHealth sends an anonymous GET /api/health and returns the status code,
// the decoded body and the raw body.
func probeHealth(t *testing.T, baseURL string) (int, map[string]any, string) {
	t.Helper()
	response, err := http.Get(baseURL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q, want JSON", got)
	}
	if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("health body %q: %v", raw, err)
	}
	return response.StatusCode, body, string(raw)
}

// assertOnlyStatus checks the body carries the health status and nothing
// about the build or deployment. A 503 also carries the standard error
// envelope every 5xx response gets (code, error, type, requestId), which
// names no internal detail.
func assertOnlyStatus(t *testing.T, body map[string]any, raw, want string) {
	t.Helper()
	allowed := map[string]any{"status": want}
	if want == "degraded" {
		allowed = map[string]any{"status": want, "code": "UNAVAILABLE", "type": "UNAVAILABLE", "error": "Service Unavailable", "requestId": nil}
	}
	if body["status"] != want || len(body) != len(allowed) {
		t.Fatalf("health body = %s, want only the keys of %v", raw, allowed)
	}
	for key, value := range body {
		expected, ok := allowed[key]
		if !ok || expected != nil && value != expected {
			t.Fatalf("health body = %s: unexpected %s=%v", raw, key, value)
		}
	}
	for _, leaked := range []string{"version", "commit", "redis", "standalone", "dev", "unknown"} {
		if strings.Contains(raw, leaked) {
			t.Fatalf("health body %s exposes %q", raw, leaked)
		}
	}
}

func TestHealthWithoutCoordinatorReportsOnlyStatus(t *testing.T) {
	httpServer := newHealthServer(t, nil)
	code, body, raw := probeHealth(t, httpServer.URL)
	if code != http.StatusOK {
		t.Fatalf("health = %d %s, want 200", code, raw)
	}
	assertOnlyStatus(t, body, raw, "ok")
	t.Setenv("FLOW_HEALTHCHECK_URL", httpServer.URL+"/api/health")
	if exit := runHealthcheck(); exit != 0 {
		t.Fatalf("healthcheck exit = %d, want 0", exit)
	}
}

func TestHealthReportsCoordinatorOutageWithoutDetails(t *testing.T) {
	logs := captureHealthLog(t)
	redisServer := miniredis.RunT(t)
	address := redisServer.Addr()
	coordinator, err := coordination.Open(context.Background(), coordination.Config{Mode: "standalone", Addrs: []string{address}, Prefix: "health", ConnectTimeout: time.Second, LockTTL: 5 * time.Second, LockWait: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := newHealthServer(t, coordinator)
	t.Setenv("FLOW_HEALTHCHECK_URL", httpServer.URL+"/api/health")

	code, body, raw := probeHealth(t, httpServer.URL)
	if code != http.StatusOK {
		t.Fatalf("healthy health = %d %s, want 200", code, raw)
	}
	assertOnlyStatus(t, body, raw, "ok")

	redisServer.Close()
	for range 2 {
		code, body, raw = probeHealth(t, httpServer.URL)
		if code != http.StatusServiceUnavailable {
			t.Fatalf("degraded health = %d %s, want 503", code, raw)
		}
		assertOnlyStatus(t, body, raw, "degraded")
		_, port, _ := strings.Cut(address, ":")
		if strings.Contains(raw, address) || strings.Contains(raw, port) || strings.Contains(strings.ToLower(raw), "refused") || strings.Contains(strings.ToLower(raw), "dial") {
			t.Fatalf("degraded health body %s exposes the connection error", raw)
		}
	}
	if exit := runHealthcheck(); exit != 1 {
		t.Fatalf("healthcheck exit = %d while degraded, want 1", exit)
	}
	// Operators get the detail in the log, once per outage rather than per probe.
	if got := strings.Count(logs.String(), "health check degraded"); got != 1 {
		t.Fatalf("degraded log lines = %d, want 1:\n%s", got, logs.String())
	}
	if !strings.Contains(logs.String(), "redis_mode=standalone") {
		t.Fatalf("degraded log lacks the redis mode:\n%s", logs.String())
	}

	if err := redisServer.Restart(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, body, raw = probeHealth(t, httpServer.URL)
		if code == http.StatusOK || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if code != http.StatusOK {
		t.Fatalf("recovered health = %d %s, want 200", code, raw)
	}
	assertOnlyStatus(t, body, raw, "ok")
	if exit := runHealthcheck(); exit != 0 {
		t.Fatalf("healthcheck exit = %d after recovery, want 0", exit)
	}
	if got := strings.Count(logs.String(), "health check recovered"); got != 1 {
		t.Fatalf("recovered log lines = %d, want 1:\n%s", got, logs.String())
	}
}
