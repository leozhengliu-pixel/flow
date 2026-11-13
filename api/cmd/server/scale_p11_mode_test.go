package main

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP11OffPreservesLegacyCoordination(t *testing.T) {
	for _, test := range []struct {
		name, peerMode, journalAtOpen, journalMode string
	}{
		{"unset-journal-unset", "", "", ""},
		{"unset", "", "on", "on"},
		{"unknown", "unknown", "on", "on"},
		{"off", "off", "on", "on"},
		{"shadow-journal-unset", "shadow", "", ""},
		{"shadow-journal-off", "shadow", "off", "off"},
		{"on-journal-unset", "on", "", ""},
		{"on-journal-off", "on", "off", "off"},
		{"shadow-journal-not-ready", "shadow", "off", "on"},
		{"on-journal-not-ready", "on", "off", "on"},
		{"shadow-journal-disabled", "shadow", "on", "off"},
		{"on-journal-disabled", "on", "on", "off"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_PEER_SYNC", "off")
			t.Setenv("FLOW_RECORD_JOURNAL", test.journalAtOpen)
			f := scaleP11NewFixture(t, 1)
			t.Setenv("FLOW_RECORD_JOURNAL", test.journalMode)
			t.Setenv("FLOW_RECORD_PEER_SYNC", test.peerMode)
			for key, value := range map[string]string{"FLOW_RECORD_PEER_SYNC": test.peerMode, "FLOW_RECORD_JOURNAL": test.journalMode} {
				if value == "" {
					if err := os.Unsetenv(key); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode := f.service.recordPeerSyncMode(); mode != "off" {
				t.Fatalf("effective peer mode = %q, want off", mode)
			}
			queries := 0
			ctx := store.WithSQLStatementObserver(t.Context(), func(string) { queries++ })
			for range 3 {
				if err := f.service.ensureCurrentWorkspace(ctx, f.workspace); err != nil {
					t.Fatal(err)
				}
			}
			if queries != 0 {
				t.Fatalf("unchanged workspace freshness performed %d SQL statements", queries)
			}
			pollCtx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			f.service.pollPeerChanges(pollCtx)
			if err := pollCtx.Err(); err != nil {
				t.Fatalf("disabled peer poller waited for cancellation: %v", err)
			}

			writer := &server{store: f.writer, coordinator: f.service.coordinator, realtime: newRealtimeHub()}
			local, stopLocal := writer.realtime.subscribe(f.workspace)
			defer stopLocal()
			event := scaleP11Rename(t, f, "legacy freshness")
			writer.publishRealtime(f.workspace, event)
			writer.publishRealtime(f.workspace, event)
			if got := drainRealtime(local); len(got) != 2 || got[0].ID != event.ID || got[1].ID != event.ID {
				t.Fatal("off changed the legacy local publish path")
			}
			if version, err := f.service.sharedWorkspaceVersion(t.Context(), f.workspace); err != nil || version != 2 {
				t.Fatalf("legacy Redis version = %d, want 2: %v", version, err)
			}
			if err := f.service.ensureCurrentWorkspace(t.Context(), f.workspace); err != nil || scaleP11Name(t, f) != "legacy freshness" {
				t.Fatalf("legacy Redis freshness did not install the change: %v", err)
			}

			remote, stopRemote := f.service.realtime.subscribe(f.workspace)
			defer stopRemote()
			event = scaleP11Rename(t, f, "legacy peer reload")
			event.ClientID = "legacy-client"
			writer.publishRealtime(f.workspace, event)
			f.service.receiveRemoteEvent(coordination.EventEnvelope{Workspace: f.workspace, Event: event})
			select {
			case got := <-remote:
				if got.ID != event.ID || got.Seq != event.Seq || got.ClientID != event.ClientID || !bytes.Equal(got.Payload, event.Payload) || scaleP11Name(t, f) != "legacy peer reload" {
					t.Fatal("off lost the legacy envelope or published before reloading")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("legacy peer event was not published")
			}
			f.service.remoteEvents.mu.Lock()
			consumers := len(f.service.remoteEvents.consumers)
			f.service.remoteEvents.mu.Unlock()
			if consumers != 0 {
				t.Fatal("off started a SQL peer consumer")
			}
		})
	}
}

func TestScaleP11OffKeepsLegacyQueue(t *testing.T) {
	t.Setenv("FLOW_RECORD_PEER_SYNC", "")
	var queue remoteEventQueue
	event := domain.RealtimeEvent{ID: "legacy", Seq: 1, Type: "project.updated", Payload: bytes.Repeat([]byte("p"), remoteEventQueueMaxBytes)}
	for range remoteEventQueueMaxEvents + 1 {
		queue.push("w", event, recordPeerSyncMode() != "off", func(string) {})
	}
	batch := queue.take("w")
	if len(batch) != remoteEventQueueMaxEvents+1 {
		t.Fatalf("legacy queue kept %d events, want %d", len(batch), remoteEventQueueMaxEvents+1)
	}
	for _, got := range batch {
		if got.ID != event.ID || got.Seq != event.Seq || !bytes.Equal(got.Payload, event.Payload) {
			t.Fatal("legacy queue changed the event envelope")
		}
	}
}

func TestScaleP11OffFreshnessDuringRedisOutage(t *testing.T) {
	t.Setenv("FLOW_RECORD_PEER_SYNC", "")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	f := scaleP11NewFixture(t, 1)
	f.redis.Close()
	queries := 0
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	ctx = store.WithSQLStatementObserver(ctx, func(string) { queries++ })
	if err := f.service.ensureCurrentWorkspace(ctx, f.workspace); err != nil {
		t.Fatalf("legacy Redis outage freshness failed: %v", err)
	}
	if queries != 0 {
		t.Fatalf("Redis outage added %d SQL statements to an unchanged workspace", queries)
	}
}

func TestScaleP11UnavailableJournalLogsOnce(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	s := &server{}
	for _, mode := range []string{"shadow", "shadow", "on", "on"} {
		t.Setenv("FLOW_RECORD_PEER_SYNC", mode)
		if got := s.recordPeerSyncMode(); got != "off" {
			t.Fatalf("unready journal mode = %q, want off", got)
		}
	}
	if count := strings.Count(output.String(), "Peer sync disabled:"); count != 1 {
		t.Fatalf("journal fallback logged %d times, want 1", count)
	}
}
