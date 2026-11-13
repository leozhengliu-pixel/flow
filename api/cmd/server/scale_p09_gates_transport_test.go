package main

import (
	"bytes"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP09GatesOnKeepsLegacyFreshnessAndBoundedQueue(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_PEER_SYNC", "on")
	f := scaleP11NewFixture(t, 1)
	event := scaleP11Rename(t, f, "mixed reader freshness")
	writer := &server{store: f.writer, coordinator: f.service.coordinator, realtime: newRealtimeHub()}
	before, err := f.service.sharedWorkspaceVersion(t.Context(), f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	writer.publishRealtime(f.workspace, event)
	after, err := f.service.sharedWorkspaceVersion(t.Context(), f.workspace)
	if err != nil || after != before+1 {
		t.Fatalf("on mode omitted legacy read-after-write invalidation: before=%d after=%d err=%v", before, after, err)
	}
	var queue remoteEventQueue
	payload := bytes.Repeat([]byte("p"), remoteEventQueueMaxBytes+1)
	for i := range 10000 {
		queued := domain.RealtimeEvent{ID: event.ID, ClientID: "mixed-reader", Type: event.Type, Seq: int64(i + 1), Payload: payload}
		queue.push(f.workspace, queued, true, func(string) {})
	}
	if len(queue.pending[f.workspace]) != 0 || queue.bytes[f.workspace] != 0 || queue.clientBytes[f.workspace] > peerClientHintMaxBytes {
		t.Fatal("SQL consumer retained legacy business bodies or exceeded its hint budget")
	}
	batch := queue.take(f.workspace)
	if len(batch) != 1 || batch[0].Type != peerHeadEventType || batch[0].Seq != 10000 || len(batch[0].Payload) != 0 {
		t.Fatal("SQL consumer did not coalesce business envelopes into one bounded head wakeup")
	}
}
