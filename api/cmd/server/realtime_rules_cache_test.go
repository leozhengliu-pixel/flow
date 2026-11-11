package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Streams share one project/initiative index per installed workspace copy; a
// write that changes the projects installs a new copy, and the next event
// builds a new index from it.
func TestPulseRulesAreSharedPerWorkspaceCopy(t *testing.T) {
	repo := pulseTestRepo(t)
	hub := newRealtimeHub()
	snapshot, _ := repo.PulseSources("test-workspace")
	first := hub.pulseRulesFor("test-workspace", snapshot)
	again, _ := repo.PulseSources("test-workspace")
	if hub.pulseRulesFor("test-workspace", again) != first {
		t.Fatal("an unchanged workspace copy built a second index")
	}
	if err := repo.MutateWorkspace(t.Context(), "test-workspace", "project.updated", "project_team", map[string]string{"name": "Renamed"}, func(data *domain.Bootstrap) error {
		for index := range data.Projects {
			if data.Projects[index].ID == "project_team" {
				data.Projects[index].Name = "Renamed"
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	changed, _ := repo.PulseSources("test-workspace")
	rules := hub.pulseRulesFor("test-workspace", changed)
	if rules == first || rules.projects["project_team"] == nil || rules.projects["project_team"].Name != "Renamed" {
		t.Fatalf("the index did not follow the write: %+v", rules.projects["project_team"])
	}
	if first.projects["project_team"].Name == "Renamed" {
		t.Fatal("the write changed the earlier copy in place")
	}
}

// Concurrent streams asking for one event's projection build it once.
func TestRealtimePayloadIsBuiltOncePerEvent(t *testing.T) {
	var cache realtimePayloadCache
	var builds atomic.Int32
	release := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]string, 50)
	for index := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[index] = string(cache.load("evt_1|document", func() json.RawMessage {
				builds.Add(1)
				<-release
				return json.RawMessage(`{"entity":1}`)
			}))
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if builds.Load() != 1 || strings.Join(results, "") != strings.Repeat(`{"entity":1}`, 50) {
		t.Fatalf("builds=%d results=%v", builds.Load(), results)
	}
}

// A stream that falls more than its byte budget behind gets one resync
// instead of a growing backlog of payloads.
func TestRealtimeSlowSubscriberResyncsPastItsByteBudget(t *testing.T) {
	hub := newRealtimeHub()
	channel, stop := hub.subscribe("w")
	defer stop()
	payload := json.RawMessage(`{"entity":"` + strings.Repeat("x", 200<<10) + `"}`)
	for index := range 8 {
		hub.publish("w", domain.RealtimeEvent{ID: "evt_" + string(rune('a'+index)), Type: "document.updated", Payload: payload})
	}
	got := []string{}
	for len(channel) > 0 {
		got = append(got, (<-channel).Type)
	}
	if len(got) == 0 || len(got) > 5 || got[0] != "workspace.resync_required" {
		t.Fatalf("slow stream queue = %v", got)
	}
	// A stream that keeps up gets every event.
	for index := range 8 {
		hub.publish("w", domain.RealtimeEvent{ID: "live_" + string(rune('a'+index)), Type: "document.updated", Payload: payload})
		if event := <-channel; event.ID != "live_"+string(rune('a'+index)) {
			t.Fatalf("event %d = %s", index, event.ID)
		}
	}
}

// The SSE frame splices the shared payload in as it is; it reads back as the event.
func TestWriteSSEKeepsThePayloadAsIs(t *testing.T) {
	created := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	for _, payload := range []string{`{"entity":{"id":"doc_1","content":"a \"quoted\" body"},"documentId":"doc_1"}`, "{\n  \"spaced\": true\n}", ""} {
		event := domain.RealtimeEvent{ID: "evt_1", Seq: 4, Type: "document.updated", AggregateID: "doc_1", Payload: json.RawMessage(payload), CreatedAt: created}
		recorder := httptest.NewRecorder()
		if !writeSSE(recorder, event, "node1") {
			t.Fatal("writeSSE failed")
		}
		frame := recorder.Body.String()
		lines := strings.Split(strings.TrimSuffix(frame, "\n\n"), "\n")
		if len(lines) != 2 || lines[0] != "id: evt_1~node1" || !strings.HasPrefix(lines[1], "data: ") {
			t.Fatalf("frame = %q", frame)
		}
		var decoded domain.RealtimeEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[1], "data: ")), &decoded); err != nil {
			t.Fatal(err)
		}
		want, _ := json.Marshal(event)
		got, _ := json.Marshal(decoded)
		if string(want) != string(got) {
			t.Fatalf("decoded %s, want %s", got, want)
		}
	}
}

// A heartbeat that changes nothing but the time reaches no stream.
func TestPresenceHeartbeatWithoutChangesIsNotPublished(t *testing.T) {
	s := &server{store: pulseTestRepo(t), realtime: newRealtimeHub()}
	s.realtime.presenceInterval = -1
	channel, stop := s.realtime.subscribe("w")
	defer stop()
	user := domain.User{ID: "usr_a", Name: "A"}
	roster := func(route string) []domain.Presence {
		return []domain.Presence{{ClientID: "tab_1", User: user, Route: route, LastSeenAt: time.Now()}}
	}
	s.publishPresence("w", roster("/w/inbox"))
	s.publishPresence("w", roster("/w/inbox"))
	s.publishPresence("w", roster("/w/projects"))
	if len(channel) != 2 {
		t.Fatalf("presence events = %d, want 2 (the first roster and the route change)", len(channel))
	}
}

// Rosters of a workspace go out at most once per interval: a burst of joins
// sends the first at once and the roster as it is at the end of the interval.
func TestPresenceBroadcastsAreCoalescedPerWorkspace(t *testing.T) {
	hub := newRealtimeHub()
	hub.presenceInterval = 150 * time.Millisecond
	channel, stop := hub.subscribe("w")
	defer stop()
	for index := range 20 {
		hub.updatePresence("w", "tab_"+string(rune('a'+index)), domain.User{ID: "usr_" + string(rune('a'+index))}, "", "/w/inbox")
	}
	if len(channel) != 1 {
		t.Fatalf("events during the interval = %d, want 1", len(channel))
	}
	time.Sleep(300 * time.Millisecond)
	if len(channel) != 2 {
		t.Fatalf("events after the interval = %d, want 2", len(channel))
	}
	<-channel
	last := <-channel
	var payload struct {
		Presence []domain.Presence `json:"presence"`
	}
	if err := json.Unmarshal(last.Payload, &payload); err != nil || len(payload.Presence) != 20 {
		t.Fatalf("the deferred roster has %d entries, want all 20 (%v)", len(payload.Presence), err)
	}
	if !hub.presenceCleanupDue("w", 15*time.Second) || hub.presenceCleanupDue("w", 15*time.Second) {
		t.Fatal("presence cleanup should run once per interval per workspace")
	}
}
