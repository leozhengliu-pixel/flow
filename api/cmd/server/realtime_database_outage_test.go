package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"

	"github.com/coder/websocket"
)

func openOutageEventStream(t *testing.T, ctx context.Context, host *httptest.Server, workspace, session string) (*bufio.Reader, *http.Request) {
	t.Helper()
	events, err := http.NewRequestWithContext(ctx, http.MethodGet, host.URL+"/api/realtime/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	outageCookie(workspace, session)(events)
	stream, err := host.Client().Do(events)
	if err != nil {
		t.Fatal("could not open SSE stream")
	}
	t.Cleanup(func() { _ = stream.Body.Close() })
	if stream.StatusCode != http.StatusOK {
		t.Fatalf("healthy SSE returned %d", stream.StatusCode)
	}
	reader := bufio.NewReader(stream.Body)
	connected := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal("SSE handshake ended early")
		}
		if line == "\n" {
			break
		}
		connected = connected || strings.Contains(line, `"type":"connected"`)
	}
	if !connected {
		t.Fatal("SSE handshake did not send connected")
	}
	return reader, events
}

func assertRealtimeOutageRetry(t *testing.T, reader *bufio.Reader) {
	t.Helper()
	raw, err := io.ReadAll(reader)
	if err != nil || string(raw) != fmt.Sprintf("retry: %d\n\n", busyDeadlineRetryAfter.Milliseconds()) {
		t.Fatal("SSE did not close with a retry directive and an unchanged cursor")
	}
}

func exerciseRealtimeDatabaseOutage(t *testing.T, s *server, handler http.Handler, workspace, session string, disconnect func()) {
	t.Helper()
	host := httptest.NewServer(handler)
	defer host.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	reader, events := openOutageEventStream(t, ctx, host, workspace, session)
	socketURL := "ws" + strings.TrimPrefix(host.URL, "http") + "/api/realtime/socket?clientId=outage-test"
	socketOptions := &websocket.DialOptions{HTTPClient: host.Client(), HTTPHeader: events.Header.Clone()}
	socket, upgrade, err := websocket.Dial(ctx, socketURL, socketOptions)
	if err != nil {
		if upgrade != nil {
			t.Fatalf("healthy WebSocket upgrade returned %d", upgrade.StatusCode)
		}
		t.Fatal("could not open WebSocket")
	}
	defer socket.CloseNow()

	// Dial can return before the handler registers the upgraded socket.
	for len(s.realtime.socketAccessWork(workspace, socketAccessScope{all: true})) == 0 {
		select {
		case <-ctx.Done():
			t.Fatal("WebSocket did not register its access check")
		case <-time.After(5 * time.Millisecond):
		}
	}
	disconnect()
	// Trigger a live projection as well as the socket's background credential
	// check. Neither connection may send protected data during the outage.
	s.realtime.publish(workspace, domain.RealtimeEvent{ID: "outage-event", Type: "issue.updated", AggregateID: "outage-issue"})
	socketChecked := make(chan struct{})
	go func() {
		defer close(socketChecked)
		s.checkSocketAccess(workspace, socketAccessScope{all: true})
	}()
	_, _, err = socket.Read(ctx)
	<-socketChecked
	if code := websocket.CloseStatus(err); code != websocket.StatusTryAgainLater {
		t.Fatalf("WebSocket closed with code %d, want 1013", code)
	}
	assertRealtimeOutageRetry(t, reader)

	t.Run("outage/sse-connect", func(t *testing.T) {
		q := outageRequest{"GET", "/api/realtime/events", "", outageCookie(workspace, session)}
		assertDatabaseOutage(t, q.serve(t.Context(), handler))
	})
	t.Run("outage/websocket-connect", func(t *testing.T) {
		reconnected, response, err := websocket.Dial(ctx, socketURL, socketOptions)
		if reconnected != nil {
			reconnected.CloseNow()
		}
		if err == nil || response == nil {
			t.Fatal("unauthorized WebSocket upgrade during database outage")
		}
		defer response.Body.Close()
		retry := response.Header.Get("Retry-After")
		if response.StatusCode != http.StatusServiceUnavailable || retry == "" ||
			response.Header.Get("Set-Cookie") != "" || response.Header.Get("WWW-Authenticate") != "" {
			t.Fatalf("outage WebSocket upgrade returned %d, want retryable 503", response.StatusCode)
		}
	})
}

func TestRealtimeDatabaseOutageIdleSSE(t *testing.T) {
	repo := openOutageStore(t, store.DatabaseConfig{Driver: "sqlite", Path: filepath.Join(t.TempDir(), "idle.db")}, true)
	metadata, _ := repo.WorkspaceSettingsMetadata("")
	_, session := outageSession(t, repo)
	host := httptest.NewServer(newHandler(&server{store: repo, coordinationContext: t.Context()}))
	defer host.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	reader, _ := openOutageEventStream(t, ctx, host, metadata.Workspace.URLKey, session)
	if err := repo.CloseDatabasePoolForTesting(); err != nil {
		t.Fatal("could not close test database")
	}
	assertRealtimeOutageRetry(t, reader)
}
