package main

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// metadataReadGate bounds how many requests build and encode a viewer's
// workspace metadata projection at the same time on this server.
//
// One projection of a large workspace is tens of MiB of Go objects plus its
// JSON encoding. Without a bound, every concurrent page load (and every
// client retrying a slow one) adds another projection, and a replica runs out
// of memory under a burst it could have served a few at a time. Waiting
// requests hold no metadata; they leave the queue as soon as their client
// disconnects, and give up with 503 + Retry-After after a bounded wait or when
// the queue is full, so a burst degrades into retries instead of an OOM kill.
type metadataReadGate struct {
	slots chan struct{}
	wait  time.Duration
	// write bounds how long a response may take to reach the client, so a
	// stalled reader cannot hold a slot and its projection indefinitely.
	write    time.Duration
	maxQueue int64
	queued   atomic.Int64
	// Totals for logs and tests.
	served, rejected, cancelled atomic.Int64
}

func newMetadataReadGate(concurrency, maxQueue int, wait, write time.Duration) *metadataReadGate {
	return &metadataReadGate{slots: make(chan struct{}, max(1, concurrency)), wait: wait, write: write, maxQueue: int64(max(0, maxQueue))}
}

// metadataReadGateFromEnv reads FLOW_METADATA_READ_CONCURRENCY (default 4),
// FLOW_METADATA_READ_QUEUE_LIMIT (default 64), FLOW_METADATA_READ_QUEUE_TIMEOUT
// (default 20s) and FLOW_METADATA_READ_WRITE_TIMEOUT (default 2m).
func metadataReadGateFromEnv() *metadataReadGate {
	return newMetadataReadGate(envInt("FLOW_METADATA_READ_CONCURRENCY", 4), envInt("FLOW_METADATA_READ_QUEUE_LIMIT", 64), envDuration("FLOW_METADATA_READ_QUEUE_TIMEOUT", 20*time.Second), envDuration("FLOW_METADATA_READ_WRITE_TIMEOUT", 2*time.Minute))
}

func (g *metadataReadGate) busy(w http.ResponseWriter, retryAfter time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(max(1, int(retryAfter/time.Second))))
	writeError(w, http.StatusServiceUnavailable, "The workspace is busy loading for other people; retry in a moment")
}

// gateAdmission is the outcome of waiting for a slot.
type gateAdmission int

const (
	gateAdmitted gateAdmission = iota
	// gateBusy: the queue was full or the wait ran out; answer 503 with retryAfter.
	gateBusy
	// gateCancelled: the client left while waiting.
	gateCancelled
)

// admit waits for a slot. bounded requests are refused when the queue is
// full and give up after g.wait; unbounded ones (writes, whose clients do not
// retry) wait until they get one or their client leaves. Waiting requests
// hold no metadata either way. On gateAdmitted the caller releases the slot
// (<-g.slots) when its request ends.
func (g *metadataReadGate) admit(r *http.Request, name string, bounded bool) (gateAdmission, time.Duration) {
	started := time.Now()
	// queued counts waiting requests only; admitted ones hold a slot.
	if queued := g.queued.Add(1); bounded && queued > g.maxQueue && len(g.slots) == cap(g.slots) {
		g.queued.Add(-1)
		g.rejected.Add(1)
		return gateBusy, 2 * time.Second
	}
	var expired <-chan time.Time
	if bounded {
		timer := time.NewTimer(g.wait)
		defer timer.Stop()
		expired = timer.C
	}
	select {
	case g.slots <- struct{}{}:
		g.queued.Add(-1)
	case <-r.Context().Done():
		g.queued.Add(-1)
		g.cancelled.Add(1)
		return gateCancelled, 0
	case <-expired:
		g.queued.Add(-1)
		g.rejected.Add(1)
		log.Printf("%s gate: gave up after %s (%d slots busy, %d waiting) path=%s", name, g.wait, cap(g.slots), g.queued.Load(), r.URL.Path)
		return gateBusy, 5 * time.Second
	}
	if waited := time.Since(started); waited > time.Second {
		log.Printf("%s gate: waited %s (%d waiting) path=%s", name, waited.Round(time.Millisecond), g.queued.Load(), r.URL.Path)
	}
	return gateAdmitted, 0
}

func (g *metadataReadGate) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		switch admission, retryAfter := g.admit(r, "metadata read", true); admission {
		case gateBusy:
			g.busy(w, retryAfter)
			return
		case gateCancelled:
			// The client is gone; nothing reads a response.
			return
		}
		defer func() { <-g.slots }()
		waited := time.Since(started)
		w.Header().Add("Server-Timing", fmt.Sprintf("queue;dur=%.2f", float64(waited.Microseconds())/1000))
		if r.Context().Err() != nil {
			g.cancelled.Add(1)
			return
		}
		if g.write > 0 {
			controller := http.NewResponseController(w)
			_ = controller.SetWriteDeadline(time.Now().Add(g.write))
			defer controller.SetWriteDeadline(time.Time{})
		}
		g.served.Add(1)
		next.ServeHTTP(w, r)
	})
}
