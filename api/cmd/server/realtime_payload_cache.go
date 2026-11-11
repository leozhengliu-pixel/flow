package main

import (
	"encoding/json"
	"sync"

	"flow/api/internal/domain"
)

// bareRealtimeEvent keeps only the event's identity and the given payload.
func bareRealtimeEvent(event domain.RealtimeEvent, payload map[string]any) domain.RealtimeEvent {
	bare := domain.RealtimeEvent{ID: event.ID, Seq: event.Seq, Type: event.Type, AggregateID: event.AggregateID, ClientID: event.ClientID, ActorID: event.ActorID, CreatedAt: event.CreatedAt}
	if len(payload) > 0 {
		bare.Payload, _ = json.Marshal(payload)
	}
	return bare
}

// realtimePayloadCache shares a value computed for one event between the
// streams that deliver it (the record an event's read rule checks), so each
// event is looked up once, not once per open stream. Bounded by entries and
// bytes; the oldest go first.
type realtimePayloadCache struct {
	mu      sync.Mutex
	entries map[string]*realtimePayloadEntry
	order   []string
	bytes   int
}

type realtimePayloadEntry struct {
	ready   chan struct{}
	payload json.RawMessage
}

const (
	realtimePayloadCacheEntries = 256
	realtimePayloadCacheBytes   = 32 << 20
)

// cachedPayload is load on the hub's cache; a server without a hub (tests) builds every time.
func (h *realtimeHub) cachedPayload(key string, build func() json.RawMessage) json.RawMessage {
	if h == nil {
		return build()
	}
	return h.payloads.load(key, build)
}

func (c *realtimePayloadCache) load(key string, build func() json.RawMessage) json.RawMessage {
	c.mu.Lock()
	if entry, ok := c.entries[key]; ok {
		c.mu.Unlock()
		<-entry.ready
		return entry.payload
	}
	if c.entries == nil {
		c.entries = map[string]*realtimePayloadEntry{}
	}
	entry := &realtimePayloadEntry{ready: make(chan struct{})}
	c.entries[key] = entry
	c.order = append(c.order, key)
	c.mu.Unlock()
	var payload json.RawMessage
	defer func() {
		// Also on a panic in build: the waiters get nil and build for themselves next time.
		c.mu.Lock()
		entry.payload = payload
		if c.entries[key] == entry {
			if payload == nil {
				delete(c.entries, key)
			}
			c.bytes += len(payload)
		}
		for len(c.order) > realtimePayloadCacheEntries || c.bytes > realtimePayloadCacheBytes {
			oldest := c.order[0]
			c.order = c.order[1:]
			if evicted, ok := c.entries[oldest]; ok {
				c.bytes -= len(evicted.payload)
				delete(c.entries, oldest)
			}
		}
		c.mu.Unlock()
		close(entry.ready)
	}()
	payload = build()
	return payload
}
