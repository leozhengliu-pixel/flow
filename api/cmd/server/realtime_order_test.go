package main

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func drainRealtime(channel <-chan domain.RealtimeEvent) []domain.RealtimeEvent {
	var events []domain.RealtimeEvent
	for len(channel) > 0 {
		events = append(events, <-channel)
	}
	return events
}

// A client resuming inside the 256-event window gets every event it missed,
// however many: the subscriber channel used to hold 64, so any longer replay
// became a resync followed by a partial tail.
func TestRealtimeReplayCoversTheWholeWindow(t *testing.T) {
	hub := newRealtimeHub()
	for i := 1; i <= 300; i++ {
		hub.publish("w", domain.RealtimeEvent{ID: fmt.Sprintf("evt_%d", i), Seq: int64(i), Type: "issue.updated"})
	}
	// The window keeps events 45..300: a cursor at 45 missed 255 events.
	channel, stop := hub.subscribeSince("w", "evt_45")
	defer stop()
	events := drainRealtime(channel)
	if len(events) != 255 || events[0].ID != "evt_46" || events[254].ID != "evt_300" {
		t.Fatalf("replay = %d events (%v .. %v), want evt_46..evt_300", len(events), events[0].ID, events[len(events)-1].ID)
	}
	for _, event := range events {
		if event.Type == "workspace.resync_required" {
			t.Fatal("a replay inside the window must not resync")
		}
	}
	// One event further back is outside the window: only a resync.
	evicted, stopEvicted := hub.subscribeSince("w", "evt_44")
	defer stopEvicted()
	if events := drainRealtime(evicted); len(events) != 1 || events[0].Type != "workspace.resync_required" {
		t.Fatalf("evicted cursor = %v, want one resync", events)
	}
	// Live events still fit after a full replay.
	hub.publish("w", domain.RealtimeEvent{ID: "evt_301", Seq: 301, Type: "issue.updated"})
	if events := drainRealtime(channel); len(events) != 1 || events[0].ID != "evt_301" {
		t.Fatalf("live after replay = %v", events)
	}
}

// With several instances, a peer's change reaches this one after the peer
// commits, so this instance's own next change can be published first. The
// hub delivers changes in Seq order; one that can no longer be put in order
// becomes a resync rather than older state applied over newer.
func TestRealtimeHubDeliversChangesInSeqOrder(t *testing.T) {
	hub := newRealtimeHub()
	hub.ordered = true
	channel, stop := hub.subscribe("w")
	defer stop()
	hub.publish("w", domain.RealtimeEvent{ID: "evt_1", Seq: 1, Type: "issue.updated"})
	hub.publish("w", domain.RealtimeEvent{ID: "evt_3", Seq: 3, Type: "issue.updated"}) // a peer's 2 is on its way
	hub.publish("w", domain.RealtimeEvent{ID: "presence_1", Type: "presence.updated"}) // not in the log: never waits
	hub.publish("w", domain.RealtimeEvent{ID: "evt_2", Seq: 2, Type: "issue.updated"})
	got := []string{}
	for _, event := range drainRealtime(channel) {
		got = append(got, event.ID)
	}
	if strings.Join(got, ",") != "evt_1,presence_1,evt_2,evt_3" {
		t.Fatalf("delivery = %v", got)
	}

	// A change that never comes holds the next ones only for a while.
	hub.publish("w", domain.RealtimeEvent{ID: "evt_5", Seq: 5, Type: "issue.updated"})
	if events := drainRealtime(channel); len(events) != 0 {
		t.Fatalf("evt_5 went out before the gap's wait: %v", events)
	}
	deadline := time.Now().Add(3 * realtimeReorderWait)
	for len(channel) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if events := drainRealtime(channel); len(events) != 1 || events[0].ID != "evt_5" {
		t.Fatalf("after the wait = %v, want evt_5", events)
	}
	// Arriving after 5 went out, 4 cannot be put back in order.
	hub.publish("w", domain.RealtimeEvent{ID: "evt_4", Seq: 4, Type: "issue.updated"})
	if events := drainRealtime(channel); len(events) != 1 || events[0].Type != "workspace.resync_required" {
		t.Fatalf("late change = %v, want a resync", events)
	}
	// The replay window holds the same order, so a resume replays it too.
	resumed, stopResumed := hub.subscribeSince("w", "evt_1")
	defer stopResumed()
	got = got[:0]
	for _, event := range drainRealtime(resumed) {
		got = append(got, event.ID)
	}
	if !strings.HasPrefix(strings.Join(got, ","), "presence_1,evt_2,evt_3,evt_5,resync_") {
		t.Fatalf("replay = %v", got)
	}
}

// A gap filled while a later one is still open gives the later gap its own
// full wait instead of the remainder of the first one's.
func TestRealtimeHubWaitsForEachGap(t *testing.T) {
	hub := newRealtimeHub()
	hub.ordered = true
	channel, stop := hub.subscribe("w")
	defer stop()
	hub.publish("w", domain.RealtimeEvent{ID: "evt_1", Seq: 1, Type: "issue.updated"})
	hub.publish("w", domain.RealtimeEvent{ID: "evt_3", Seq: 3, Type: "issue.updated"})
	time.Sleep(realtimeReorderWait * 3 / 4)
	hub.publish("w", domain.RealtimeEvent{ID: "evt_5", Seq: 5, Type: "issue.updated"})
	hub.publish("w", domain.RealtimeEvent{ID: "evt_2", Seq: 2, Type: "issue.updated"})
	time.Sleep(realtimeReorderWait / 2) // past the first gap's deadline
	hub.publish("w", domain.RealtimeEvent{ID: "evt_4", Seq: 4, Type: "issue.updated"})
	got := []string{}
	for _, event := range drainRealtime(channel) {
		got = append(got, event.ID)
	}
	if strings.Join(got, ",") != "evt_1,evt_2,evt_3,evt_4,evt_5" {
		t.Fatalf("delivery = %v", got)
	}
}

// Each instance's replay window is in its own arrival order, so a cursor
// written by another instance cannot be resumed: the stream resyncs.
func TestRealtimeCursorFromAnotherInstanceResyncs(t *testing.T) {
	hub := newRealtimeHub()
	hub.node = "aaaa"
	hub.publish("w", domain.RealtimeEvent{ID: "evt_1", Type: "issue.updated"})
	hub.publish("w", domain.RealtimeEvent{ID: "evt_2", Type: "issue.updated"})
	recorder := httptest.NewRecorder()
	writeSSE(recorder, domain.RealtimeEvent{ID: "evt_1", Type: "issue.updated"}, hub.node)
	if !strings.HasPrefix(recorder.Body.String(), "id: evt_1~aaaa\n") {
		t.Fatalf("stream id = %q", recorder.Body.String())
	}
	own, stopOwn := hub.subscribeSince("w", hub.localCursor("evt_1~aaaa"))
	defer stopOwn()
	if events := drainRealtime(own); len(events) != 1 || events[0].ID != "evt_2" {
		t.Fatalf("own cursor = %v", events)
	}
	foreign, stopForeign := hub.subscribeSince("w", hub.localCursor("evt_1~bbbb"))
	defer stopForeign()
	if events := drainRealtime(foreign); len(events) != 1 || events[0].Type != "workspace.resync_required" {
		t.Fatalf("foreign cursor = %v, want a resync", events)
	}
	if got := (&realtimeHub{}).localCursor("evt_1~aaaa"); got != "evt_1~aaaa" {
		t.Fatalf("an instance that does not tag resolved a tagged cursor to %q", got)
	}
}

// After the Redis subscription comes back, peer events published meanwhile
// are gone: every workspace's subscribers, and clients resuming from before,
// resync. Changes waiting for a gap go out first.
func TestRealtimeResyncAllAfterMissedPeerEvents(t *testing.T) {
	hub := newRealtimeHub()
	hub.ordered = true
	channel, stop := hub.subscribe("w")
	defer stop()
	hub.publish("other", domain.RealtimeEvent{ID: "evt_o", Type: "issue.updated"})
	hub.publish("w", domain.RealtimeEvent{ID: "evt_1", Seq: 1, Type: "issue.updated"})
	hub.publish("w", domain.RealtimeEvent{ID: "evt_3", Seq: 3, Type: "issue.updated"})
	hub.resyncAll()
	got := []string{}
	for _, event := range drainRealtime(channel) {
		got = append(got, event.Type+":"+event.ID)
	}
	if len(got) != 3 || got[1] != "issue.updated:evt_3" || !strings.HasPrefix(got[2], "workspace.resync_required:") {
		t.Fatalf("delivery = %v", got)
	}
	resumed, stopResumed := hub.subscribeSince("other", "evt_o")
	defer stopResumed()
	if events := drainRealtime(resumed); len(events) != 1 || events[0].Type != "workspace.resync_required" {
		t.Fatalf("resume across the gap = %v, want a resync", events)
	}
	// Delivery order starts over: the missed seq 2 is never waited for.
	hub.publish("w", domain.RealtimeEvent{ID: "evt_4", Seq: 4, Type: "issue.updated"})
	if events := drainRealtime(channel); len(events) != 1 || events[0].ID != "evt_4" {
		t.Fatalf("after resync = %v", events)
	}
}

// Peer events queue per workspace: one worker applies them in order, and a
// burst that arrives while it is busy shares one batch (one reload).
func TestRemoteEventQueueBatchesBursts(t *testing.T) {
	var queue remoteEventQueue
	release := make(chan struct{})
	batches := make(chan []string, 4)
	var workers sync.WaitGroup
	apply := func(workspace string) {
		defer workers.Done()
		for {
			<-release
			batch := queue.take(workspace)
			if len(batch) == 0 {
				return
			}
			ids := []string{}
			for _, event := range batch {
				ids = append(ids, event.ID)
			}
			batches <- ids
		}
	}
	workers.Add(1)
	for i := 1; i <= 3; i++ {
		queue.push("w", domain.RealtimeEvent{ID: fmt.Sprintf("evt_%d", i)}, apply)
	}
	release <- struct{}{}
	if got := <-batches; strings.Join(got, ",") != "evt_1,evt_2,evt_3" {
		t.Fatalf("first batch = %v", got)
	}
	queue.push("w", domain.RealtimeEvent{ID: "evt_4"}, apply) // the worker is still running: no second one
	release <- struct{}{}
	if got := <-batches; strings.Join(got, ",") != "evt_4" {
		t.Fatalf("second batch = %v", got)
	}
	release <- struct{}{}
	workers.Wait()
	workers.Add(1)
	queue.push("w", domain.RealtimeEvent{ID: "evt_5"}, apply) // a stopped worker is started again
	release <- struct{}{}
	if got := <-batches; strings.Join(got, ",") != "evt_5" {
		t.Fatalf("after restart = %v", got)
	}
	release <- struct{}{}
	workers.Wait()
}
