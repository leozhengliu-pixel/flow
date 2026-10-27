package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
)

// capturePanicLogs routes slog to a buffer for the test.
func capturePanicLogs(t *testing.T) *syncBuffer {
	t.Helper()
	buffer := &syncBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buffer, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return buffer
}

// panicChain is the server's outer middleware order around a handler.
func panicChain(handler http.Handler) http.Handler {
	policy := newSecurityHeaderPolicy(appconfig.DefaultSecurityHeaders(), "https://flow.example.com", "")
	return withSecurityHeaders(policy, recoverPanics(requestLog(apiErrorEnvelope(handler))))
}

func TestPanicRecoveryReturnsEnvelopeWithSecurityHeaders(t *testing.T) {
	logs := capturePanicLogs(t)
	handler := panicChain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Length", "99")
		http.SetCookie(w, &http.Cookie{Name: "half", Value: "done"})
		panic("boom: index out of range")
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/issues?token=secret-query-value", strings.NewReader(`{"password":"secret-body-value"}`))
	request.Header.Set("Authorization", "Bearer secret-header-value")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", recorder.Code)
	}
	header := recorder.Header()
	if !strings.HasPrefix(header.Get("Content-Type"), "application/json") || header.Get("Content-Length") != "" || header.Get("Set-Cookie") != "" || header.Get("Cache-Control") != "no-store" {
		t.Fatalf("handler headers leaked into the 500: %v", header)
	}
	if header.Get("X-Content-Type-Options") != "nosniff" || header.Get("Content-Security-Policy") == "" || header.Get("Referrer-Policy") == "" {
		t.Fatalf("security headers missing on the 500: %v", header)
	}
	requestID := header.Get("X-Request-ID")
	var body apiErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", recorder.Body.String(), err)
	}
	if requestID == "" || body.RequestID != requestID || body.Type != apiErrorInternal || body.Code != apiErrorInternal || body.Error != "Internal server error" {
		t.Fatalf("envelope %+v, request id %q", body, requestID)
	}
	logged := logs.String()
	for _, want := range []string{`"msg":"http handler panic"`, `"request_id":"` + requestID + `"`, `"path":"/api/issues"`, `"panic":"boom: index out of range"`, "panic_recovery_test.go"} {
		if !strings.Contains(logged, want) {
			t.Fatalf("log missing %s: %s", want, logged)
		}
	}
	for _, secret := range []string{"secret-query-value", "secret-body-value", "secret-header-value"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("log contains %s: %s", secret, logged)
		}
	}
}

func TestPanicRecoveryKeepsCORSAndTruncatesPanicValue(t *testing.T) {
	logs := capturePanicLogs(t)
	handler := recoverPanics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "https://flow.example.com")
		panic(errors.New(strings.Repeat("x", 4000)))
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusInternalServerError || recorder.Header().Get("Access-Control-Allow-Origin") != "https://flow.example.com" {
		t.Fatalf("status %d header %v", recorder.Code, recorder.Header())
	}
	if strings.Contains(logs.String(), strings.Repeat("x", panicValueLimit+1)) {
		t.Fatal("panic value was not truncated")
	}
}

func recoveredValue(fn func()) (value any) {
	defer func() { value = recover() }()
	fn()
	return nil
}

func TestPanicRecoveryAbortsStartedResponses(t *testing.T) {
	logs := capturePanicLogs(t)
	handler := panicChain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("the recovery writer hides http.Flusher")
		}
		_, _ = io.WriteString(w, "event: hello\n\n")
		flusher.Flush()
		panic("stream broke")
	}))
	recorder := httptest.NewRecorder()
	value := recoveredValue(func() { handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/events", nil)) })
	if value != http.ErrAbortHandler {
		t.Fatalf("recovered %v, want http.ErrAbortHandler", value)
	}
	if body := recorder.Body.String(); body != "event: hello\n\n" || recorder.Code != http.StatusOK {
		t.Fatalf("stream got %d %q after the panic", recorder.Code, body)
	}
	if !strings.Contains(logs.String(), `"panic":"stream broke"`) {
		t.Fatalf("panic not logged: %s", logs.String())
	}
}

func TestPanicRecoveryRepanicsAbortHandler(t *testing.T) {
	logs := capturePanicLogs(t)
	handler := recoverPanics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic(http.ErrAbortHandler) }))
	recorder := httptest.NewRecorder()
	if value := recoveredValue(func() { handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil)) }); value != http.ErrAbortHandler {
		t.Fatalf("recovered %v", value)
	}
	if recorder.Body.Len() != 0 || strings.Contains(logs.String(), "http handler panic") {
		t.Fatalf("abort handled as a crash: body %q log %s", recorder.Body.String(), logs.String())
	}
}

func TestPanicRecoveryLeavesHijackedConnections(t *testing.T) {
	logs := capturePanicLogs(t)
	server := httptest.NewServer(panicChain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, buffered, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = buffered.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: test\r\nConnection: Upgrade\r\n\r\nowned")
		_ = buffered.Flush()
		panic("after hijack")
	})))
	defer server.Close()
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/realtime", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "test")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status %d", response.StatusCode)
	}
	rest, _ := io.ReadAll(bufio.NewReader(response.Body))
	if string(rest) != "owned" {
		t.Fatalf("recovery wrote into the hijacked connection: %q", rest)
	}
	// The handler's deferred Close ends the read before the recovery logs.
	for deadline := time.Now().Add(2 * time.Second); !strings.Contains(logs.String(), `"panic":"after hijack"`); {
		if time.Now().After(deadline) {
			t.Fatalf("panic not logged: %s", logs.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A real HTTP server: the client gets the JSON 500 and the connection stays
// usable for the next request.
func TestPanicRecoveryOverHTTP(t *testing.T) {
	capturePanicLogs(t)
	server := httptest.NewServer(panicChain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/panic" {
			var issues map[string]int
			issues["nil map"]++
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})))
	defer server.Close()
	for _, path := range []string{"/api/panic", "/api/health"} {
		response, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		want := http.StatusOK
		if path == "/api/panic" {
			want = http.StatusInternalServerError
			if !strings.Contains(string(body), `"type":"INTERNAL_ERROR"`) {
				t.Fatalf("body %s", body)
			}
		}
		if response.StatusCode != want {
			t.Fatalf("%s status %d", path, response.StatusCode)
		}
	}
}
