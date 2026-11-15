package main

import (
	"bytes"
	"encoding/json"

	"flow/api/internal/domain"
)

// Keep shared legacy frames byte-identical to writeSSE's payload-splicing path.
func marshalLegacyRealtimeEvent(event domain.RealtimeEvent) ([]byte, error) {
	payload := event.Payload
	if len(payload) == 0 || bytes.ContainsAny(payload, "\r\n") || !json.Valid(payload) {
		return json.Marshal(event)
	}
	event.Payload = nil
	head, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	raw := make([]byte, 0, len(head)+len(payload)+11)
	raw = append(raw, head[:len(head)-1]...)
	raw = append(raw, `,"payload":`...)
	raw = append(raw, payload...)
	return append(raw, '}'), nil
}
