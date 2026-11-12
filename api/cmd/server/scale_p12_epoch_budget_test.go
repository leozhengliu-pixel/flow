package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP12CachedFrameDoesNotReserveAnotherEncoding(t *testing.T) {
	for _, repo := range scaleP03ServerTestStores(t) {
		f := newScaleP12Fixture(t, repo)
		event := domain.RealtimeEvent{ID: "p12-cache-budget", Type: "issue.updated", AggregateID: f.open.ID, Payload: mustJSON(t, map[string]string{"note": strings.Repeat("x", 8192)})}
		source, err := f.service.BuildEventSource(t.Context(), f.data.Workspace.URLKey, event)
		if err != nil {
			t.Fatal(err)
		}
		access, err := repo.ResolveEventAccess(t.Context(), f.data.Workspace.URLKey, f.data.Viewer.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		frame, err := ProjectEvent(source, access)
		if err != nil || !frame.Visible() {
			t.Fatalf("initial frame: %v", err)
		}
		frame.Close()
		source.budget = &realtimeBudget{limit: 32 << 10}
		frame, err = ProjectEvent(source, access)
		if err != nil || !frame.Visible() {
			t.Fatalf("cached frame reserved another encoding workload: %v", err)
		}
		frame.Close()
		if source.budget.used.Load() != 0 {
			t.Fatal("cached frame retained work or bytes after close")
		}
		source.frames = nil
		if frame, err = ProjectEvent(source, access); !errors.Is(err, store.ErrEventSourceBudget) {
			frame.Close()
			t.Fatalf("uncached encoding bypassed the work budget: %v", err)
		}
	}
}

func TestScaleP12SharedEncodingFailureIsNotAnInvisibleFrame(t *testing.T) {
	var cache realtimePayloadCache
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = cache.loadResult("p12-failed-encoding", func() (json.RawMessage, error) {
			close(entered)
			<-release
			return nil, store.ErrEventSourceBudget
		})
	}()
	<-entered
	cache.mu.Lock()
	entry := cache.entries["p12-failed-encoding"]
	cache.mu.Unlock()
	close(release)
	<-entry.ready
	<-done
	if !errors.Is(entry.err, store.ErrEventSourceBudget) {
		t.Fatal("shared encoding failure became a hidden event")
	}
	if _, err := cache.loadResult("p12-failed-encoding", func() (json.RawMessage, error) { return json.RawMessage(`{}`), nil }); err != nil {
		t.Fatalf("failed encoding poisoned the next delivery: %v", err)
	}
}
