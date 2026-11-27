package main

import (
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleActivationRealtimeDifferential(t *testing.T) {
	for _, mode := range []string{"off", "current", "full"} {
		t.Run(mode, func(t *testing.T) {
			peers, fixture, issue, _ := scaleActivationHTTPPeers(t, mode)
			workspace := fixture.data.Workspace.URLKey
			writer, reader := peers[0].service, peers[1].service
			// Drive the receiver's poll explicitly, between commit and the
			// arrival of the first Redis hint.
			receiver := &server{store: reader.store, coordinator: reader.coordinator, realtime: newTestRealtimeHub(t)}
			receiver.realtime.ordered = true
			if mode == "full" {
				if err := receiver.syncPeerWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			stream, stop := receiver.realtime.subscribe(workspace)
			defer stop()
			writes := make(chan domain.RealtimeEvent, 2)
			writer.store.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
				if event.Type == "issue.updated" {
					writes <- event
				}
			})
			write := func(priority int) domain.RealtimeEvent {
				t.Helper()
				authRequest[domain.Issue](t, fixture.admin, http.MethodPatch, peers[0].URL+"/api/issue-records/"+issue.ID,
					map[string]int{"priority": priority}, workspace, http.StatusOK)
				select {
				case event := <-writes:
					event.Seq = writer.store.DomainEventSeq(t.Context(), event.ID)
					event.ClientID = "activation-tab"
					if event.Seq == 0 {
						t.Fatal("write did not have a realtime cursor")
					}
					return event
				default:
					t.Fatal("write did not publish a realtime event")
					return domain.RealtimeEvent{}
				}
			}
			events := [2]domain.RealtimeEvent{write(1), write(2)}
			envelope := func(event domain.RealtimeEvent) {
				receiver.remoteEvents.push(workspace, event, mode == "full", func(string) {})
			}
			if mode == "full" {
				receiver.publishPeerEvent(workspace, events[1])
				if err := receiver.syncPeerWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
				receiver.realtime.mu.Lock()
				legacyTimer := receiver.realtime.order[workspace].timer != nil
				receiver.realtime.mu.Unlock()
				if len(drainRealtime(stream)) != 0 || legacyTimer {
					t.Fatal("polling with a delayed hint resynced or released the later event")
				}
				envelope(events[0])
			} else {
				envelope(events[0])
				envelope(events[1])
			}
			receiver.applyRemoteEvents(workspace)
			got := drainRealtime(stream)
			if len(got) != len(events) {
				t.Fatalf("delivered %d events, want %d", len(got), len(events))
			}
			for i, event := range got {
				if event.ID != events[i].ID || event.Seq != events[i].Seq || event.ClientID != events[i].ClientID {
					t.Fatal("realtime delivery reordered, duplicated or lost an event or its correlation")
				}
			}
			if mode != "full" {
				return
			}

			lost, later := write(3), write(4)
			envelope(later)
			receiver.applyRemoteEvents(workspace)
			if len(drainRealtime(stream)) != 0 {
				t.Fatal("a potentially delayed hint triggered fallback before the grace period")
			}
			receiver.remoteEvents.consumer(workspace).missingHintSince = time.Now().Add(-peerClientHintWait)
			if err := receiver.syncPeerWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			got = drainRealtime(stream)
			if len(got) != 2 || got[0].Type != "workspace.resync_required" || got[1].ID != later.ID || got[1].ClientID != later.ClientID {
				t.Fatal("confirmed hint loss did not signal recovery and deliver the later correlated event")
			}
			replay := authRequest[struct{ Events []domain.RealtimeEvent }](t, fixture.admin, http.MethodGet,
				peers[1].URL+"/api/realtime/changes?since="+strconv.FormatInt(lost.Seq-1, 10), nil, workspace, http.StatusOK)
			if !slices.ContainsFunc(replay.Events, func(event domain.RealtimeEvent) bool { return event.ID == lost.ID && event.Seq == lost.Seq }) {
				t.Fatal("recovery could not replay the event whose live hint was lost")
			}
			receiver.publishPeerEvent(workspace, lost)
			if len(drainRealtime(stream)) != 0 {
				t.Fatal("the old publication triggered another resync after recovery")
			}
		})
	}
}
