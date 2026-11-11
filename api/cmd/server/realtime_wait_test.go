package main

import (
	"encoding/json"
	"testing"
	"time"
)

// waitForEvents waits until done reports that the expected realtime events
// arrived, then gives events that must not arrive a moment to show up.
func waitForEvents(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !done() {
		time.Sleep(20 * time.Millisecond)
	}
	if !done() {
		t.Fatal("realtime events did not arrive")
	}
	time.Sleep(100 * time.Millisecond)
}

// lastPayload decodes the payload of the stream's last eventType event for aggregate.
func lastPayload(t *testing.T, stream *sseCollector, eventType, aggregate string) map[string]any {
	t.Helper()
	var payload map[string]any
	found := false
	for _, event := range stream.find(eventType) {
		if event.AggregateID == aggregate {
			payload, found = map[string]any{}, true
			_ = json.Unmarshal(event.Payload, &payload)
		}
	}
	if !found {
		t.Fatalf("no %s for %s in %+v", eventType, aggregate, stream.events)
	}
	return payload
}
