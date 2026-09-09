package main

import (
	"bytes"
	"context"
	"flow/api/internal/domain"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestJSONBudgetRejectsBeforeHandler(t *testing.T) {
	called := false
	handler := requestBodyBudget(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	r := httptest.NewRequest("POST", "/api/auth/register", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	r.ContentLength = maxJSONRequestBytes + 1
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if called || w.Code != 413 {
		t.Fatalf("handler=%v status=%d", called, w.Code)
	}
}

func TestImportStopsAtRowBoundary(t *testing.T) {
	for _, fixture := range []struct{ name, body string }{{"rows.csv", "title\n" + strings.Repeat("short\n", 5001)}, {"rows.json", "[" + strings.Repeat(`{"title":"short"},`, 5000) + `{"title":"last"}]`}} {
		if _, _, _, err := parseImportFile(fixture.name, strings.NewReader(fixture.body)); err == nil {
			t.Fatalf("%s accepted too many rows", fixture.name)
		}
	}
}

func TestSocketQueueBudgetRejectsBeforeCopyingLargeFrames(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &realtimeSocketClient{send: make(chan realtimeSocketMessage, 256), cancel: cancel}
	message := realtimeSocketMessage{binary: true, data: make([]byte, 2<<20)}
	for i := 0; i < 4; i++ {
		if !client.enqueue(message, true) {
			t.Fatal("valid queue rejected")
		}
	}
	if client.enqueue(message, true) {
		t.Fatal("unbounded socket queue")
	}
	if client.queuedBytes.Load() != 8<<20 || ctx.Err() == nil {
		t.Fatal("overflow did not close slow socket")
	}
}

func TestRealtimeReplayBytesAreBounded(t *testing.T) {
	hub := newRealtimeHub()
	payload := bytes.Repeat([]byte("x"), 128<<10)
	for workspace := 0; workspace < 12; workspace++ {
		for i := 0; i < 70; i++ {
			hub.publish(fmt.Sprint(workspace), domain.RealtimeEvent{ID: fmt.Sprintf("%d-%d", workspace, i), Payload: payload, CreatedAt: time.Now()})
		}
	}
	if hub.historyTotal > 64<<20 {
		t.Fatal("global replay budget exceeded")
	}
	for _, size := range hub.historyBytes {
		if size > 8<<20 {
			t.Fatal("workspace replay budget exceeded")
		}
	}
	hub.publish("large", domain.RealtimeEvent{ID: "huge", Type: "issue.updated", Payload: make([]byte, 1<<20)})
	if hub.history["large"][0].Type != "workspace.resync_required" {
		t.Fatal("oversized entity retained")
	}
}
