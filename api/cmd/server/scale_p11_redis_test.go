package main

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/idgen"
)

func TestScaleP11MixedReaderEnvelope(t *testing.T) {
	t.Setenv("FLOW_RECORD_PEER_SYNC", "shadow")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP11NewFixture(t, 1)
	event := scaleP11Rename(t, f, "compact hint")
	event.ClientID = "p11-browser"
	config := coordination.Config{Mode: "standalone", Addrs: []string{f.redis.Addr()}, Prefix: idgen.New("p11-envelope-"), ConnectTimeout: time.Second}
	sender, err := coordination.Open(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sender.Close() })
	listener, err := coordination.Open(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	done, ready := make(chan struct{}), make(chan struct{})
	envelopes := make(chan coordination.EventEnvelope, 2)
	var subscribed sync.Once
	go func() {
		defer close(done)
		_ = listener.Listen(ctx, func(event coordination.EventEnvelope) { envelopes <- event }, func(bool) { subscribed.Do(func() { close(ready) }) })
	}()
	defer func() { cancel(); <-done }()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("Redis listener did not subscribe")
	}
	service := &server{store: f.writer, coordinator: sender, realtime: newRealtimeHub()}
	for _, mode := range []string{"shadow", "on"} {
		t.Setenv("FLOW_RECORD_PEER_SYNC", mode)
		service.publishRealtime(f.workspace, event)
		select {
		case envelope := <-envelopes:
			if envelope.Event.Type != event.Type || !bytes.Equal(envelope.Event.Payload, event.Payload) || envelope.Event.Seq != event.Seq || envelope.Event.ClientID != event.ClientID {
				t.Fatal("peer mode changed the mixed-reader business envelope")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("Redis did not deliver the peer envelope")
		}
	}
}
