package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP06Measurements(t *testing.T) {
	t.Run("heartbeat2000", func(t *testing.T) {
		t.Setenv("FLOW_PRESENCE_MINIMAL", "on")
		s := &server{store: pulseTestRepo(t), uploadPath: t.TempDir(), authDisabled: true}
		handler := newHandler(s)
		s.realtime.presence["test-workspace"] = make(map[string]domain.Presence, 2000)
		for i := range 2000 {
			id := fmt.Sprintf("client_%d", i)
			s.realtime.presence["test-workspace"][id] = domain.Presence{ClientID: id, User: domain.User{ID: fmt.Sprintf("user_%d", i), Name: "Presence user"}, Route: "/test-workspace/inbox", LastSeenAt: time.Now().UTC()}
		}
		request := func() int {
			r := httptest.NewRequest(http.MethodPost, "/api/realtime/presence?workspace=test-workspace", bytes.NewBufferString(`{"clientId":"client_0","route":"/test-workspace/inbox"}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-Flow-Presence", "minimal")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 200 && w.Code != 204 {
				t.Fatalf("heartbeat status %d: %s", w.Code, w.Body.String())
			}
			return w.Body.Len()
		}
		request()
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		wire := 0
		for range 100 {
			wire += request()
		}
		elapsed := time.Since(start)
		runtime.ReadMemStats(&after)
		t.Logf("100 heartbeats / 2000 roster: ns/op=%d alloc_bytes/op=%d allocs/op=%d response_bytes/op=%d", elapsed.Nanoseconds()/100, (after.TotalAlloc-before.TotalAlloc)/100, (after.Mallocs-before.Mallocs)/100, wire/100)
	})
	t.Run("slowSockets2000", func(t *testing.T) {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		hub := newRealtimeHub()
		stops := make([]context.CancelFunc, 0, 2000)
		for i := range 2000 {
			_, cancel := context.WithCancel(context.Background())
			stops = append(stops, cancel)
			client := hub.addSocket("w", fmt.Sprint(i), cancel)
			hub.joinDocument(client, "document")
		}
		frame := bytes.Repeat([]byte{1}, 16<<10)
		for range 8 {
			hub.broadcastDocument("w", "document", 0, realtimeSocketMessage{binary: true, data: frame})
		}
		runtime.GC()
		runtime.ReadMemStats(&after)
		t.Logf("2000 slow collaboration sockets / 8 x 16KiB: retained_heap_delta=%d bytes_per_connection=%d total_alloc_delta=%d", int64(after.HeapAlloc)-int64(before.HeapAlloc), (int64(after.HeapAlloc)-int64(before.HeapAlloc))/2000, after.TotalAlloc-before.TotalAlloc)
		runtime.KeepAlive(hub)
		for _, cancel := range stops {
			cancel()
		}
	})
}
