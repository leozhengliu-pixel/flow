package store

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func captureSlog(t *testing.T) *lockedBuffer {
	t.Helper()
	buf := &lockedBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return buf
}

func TestColdMetricsDeltas(t *testing.T) {
	prev := ColdMetricsSnapshot()
	if coldMetricsDelta(prev, ColdMetricsSnapshot(), time.Minute) != nil {
		t.Fatal("an unchanged snapshot produced a line")
	}
	coldSlotWaits.Add(2)
	coldSlotWaitNanos.Add(int64(6 * time.Millisecond))
	coldSlotRefusals.Add(1)
	coldFacadeDecodes.Add(4)
	coldFacadeDecodeNanos.Add(int64(40 * time.Millisecond))
	t.Cleanup(func() {
		coldSlotWaits.Add(-2)
		coldSlotWaitNanos.Add(-int64(6 * time.Millisecond))
		coldSlotRefusals.Add(-1)
		coldFacadeDecodes.Add(-4)
		coldFacadeDecodeNanos.Add(-int64(40 * time.Millisecond))
	})
	cur := ColdMetricsSnapshot()
	attrs := coldMetricsDelta(prev, cur, time.Minute)
	got := map[string]any{}
	for i := 0; i < len(attrs); i += 2 {
		got[attrs[i].(string)] = attrs[i+1]
	}
	if got["slotWaits"] != int64(2) || got["slotRefusals"] != int64(1) || got["facadeDecodes"] != int64(4) {
		t.Fatalf("deltas = %v", got)
	}
	if got["slotWaitAvg"] != 3*time.Millisecond || got["facadeDecodeAvg"] != 10*time.Millisecond {
		t.Fatalf("averages = %v", got)
	}
	// The next interval sees only what happened after cur.
	if coldMetricsDelta(cur, ColdMetricsSnapshot(), time.Minute) != nil {
		t.Fatal("deltas were not relative to the last snapshot")
	}
}

func TestColdMetricsLogIntervalAndLifecycle(t *testing.T) {
	buf := captureSlog(t)
	// Interval 0 returns at once and logs nothing.
	done := make(chan struct{})
	go func() { RunColdMetricsLog(context.Background(), 0); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("interval 0 did not return")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() { RunColdMetricsLog(ctx, 10*time.Millisecond); close(done) }()
	time.Sleep(30 * time.Millisecond)
	if strings.Contains(buf.String(), "cold path") {
		t.Fatal("a line was logged without a change")
	}
	coldSlotRefusals.Add(1)
	t.Cleanup(func() { coldSlotRefusals.Add(-1) })
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(buf.String(), "bounded metadata: cold path") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if line := buf.String(); !strings.Contains(line, "slotRefusals=1") {
		t.Fatalf("log = %q", line)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the logger did not stop with its context")
	}
}

func TestBoundedMetadataStatsInterval(t *testing.T) {
	for raw, want := range map[string]time.Duration{"": time.Minute, "0": 0, "45": 45 * time.Second, "2m": 2 * time.Minute, "junk": time.Minute, "-3": time.Minute} {
		t.Setenv("FLOW_BOUNDED_METADATA_STATS_INTERVAL", raw)
		if got := BoundedMetadataStatsInterval(); got != want {
			t.Errorf("%q: %v want %v", raw, got, want)
		}
	}
}

func TestColdMetricsCatalogReport(t *testing.T) {
	s, workspace := scaleP05Database(t)
	scaleP05Put(t, s, workspace, "projects", "p1", 1, map[string]any{"id": "p1", "name": "P"})
	scaleP05Put(t, s, workspace, "labels", "l1", 1, map[string]any{"id": "l1", "name": "L"})
	scaleP05Finish(t, s, workspace, "projects")
	find := func() MetadataCatalogCompleteness {
		catalogs, err := s.MetadataCatalogCompleteness(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range catalogs {
			if c.Workspace == workspace {
				return c
			}
		}
		t.Fatal("workspace missing")
		return MetadataCatalogCompleteness{}
	}
	if c := find(); c.Collections != 2 || c.Incomplete != 1 {
		t.Fatalf("incomplete catalog = %+v", c)
	}
	// Flag off logs nothing.
	buf := captureSlog(t)
	s.LogMetadataCatalogCompleteness(t.Context())
	if buf.String() != "" {
		t.Fatalf("flag off logged %q", buf.String())
	}
	s.metadataResidency = &metadataResidency{}
	s.LogMetadataCatalogCompleteness(t.Context())
	if line := buf.String(); strings.Contains(line, workspace) || !strings.Contains(line, "complete=false") || !strings.Contains(line, "incompleteCollections=1") {
		t.Fatalf("incomplete line = %q", line)
	}
	scaleP05Finish(t, s, workspace, "labels")
	if c := find(); c.Collections != 2 || c.Incomplete != 0 {
		t.Fatalf("complete catalog = %+v", c)
	}
	s.LogMetadataCatalogCompleteness(t.Context())
	if line := buf.String(); !strings.Contains(line, "complete=true") {
		t.Fatalf("complete line = %q", line)
	}
}
