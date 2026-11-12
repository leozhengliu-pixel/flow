package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"runtime/pprof"
	"testing"
	"time"
)

func TestScaleP06SlowConnectionsLoad(t *testing.T) {
	if os.Getenv("FLOW_P06_LOAD") != "1" {
		t.Skip("set FLOW_P06_LOAD=1 for the two-minute transport load")
	}
	t.Setenv("FLOW_PRESENCE_MINIMAL", "on")
	s := &server{store: pulseTestRepo(t), uploadPath: t.TempDir(), authDisabled: true}
	handler := newHandler(s)
	heartbeat := func(index int) time.Duration {
		r := httptest.NewRequest(http.MethodPost, "/api/realtime/presence?workspace=test-workspace", bytes.NewBufferString(fmt.Sprintf(`{"clientId":"client_%d","route":"/test-workspace/inbox"}`, index)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Flow-Presence", "minimal")
		w := httptest.NewRecorder()
		start := time.Now()
		handler.ServeHTTP(w, r)
		elapsed := time.Since(start)
		if w.Code != 204 || w.Body.Len() != 0 {
			t.Fatalf("heartbeat status=%d bytes=%d", w.Code, w.Body.Len())
		}
		return elapsed
	}
	for i := range 2000 {
		heartbeat(i)
	}
	stops := make([]func(), 0, 2000)
	for i := range 1000 {
		_, stop := s.realtime.subscribeTransport("test-workspace", "", false)
		stops = append(stops, stop)
		client := s.realtime.addSocket("test-workspace", fmt.Sprint(i), func() {})
		if client == nil {
			t.Fatal("idle socket admission failed")
		}
		s.realtime.joinDocument(client, "document")
		stops = append(stops, func() { s.realtime.removeSocket(client) })
	}
	defer func() {
		for _, stop := range stops {
			stop()
		}
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	start := time.Now()
	count := 0
	var cost time.Duration
	var peakHeap uint64
	for time.Since(start) < 2*time.Minute {
		<-ticker.C
		cost += heartbeat(count % 2000)
		count++
		if count%100 == 0 {
			s.realtime.broadcastDocument("test-workspace", "document", 0, realtimeSocketMessage{binary: true, data: bytes.Repeat([]byte{byte(count)}, 64<<10)})
		}
		if count%1000 == 0 {
			runtime.GC()
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			peakHeap = max(peakHeap, stats.HeapAlloc)
		}
	}
	if count < 11800 {
		t.Fatalf("only %d heartbeats in two minutes", count)
	}
	if peakHeap > 128<<20 || s.realtime.budget.peak.Load() > realtimeTransportBytes {
		t.Fatalf("retained_heap=%d transport_peak=%d", peakHeap, s.realtime.budget.peak.Load())
	}
	t.Logf("duration=%s heartbeats=%d latency_ns/op=%d retained_heap_peak=%d transport_peak=%d", time.Since(start), count, cost.Nanoseconds()/int64(count), peakHeap, s.realtime.budget.peak.Load())
	if profile, err := os.Create("/tmp/codex-scale-p06/slow-connections.heap"); err == nil {
		defer profile.Close()
		if err := pprof.WriteHeapProfile(profile); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Fatal(err)
	}
}
