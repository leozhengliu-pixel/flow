package main

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// The projection gate bounds the resource requests that build the viewer's
// whole workspace projection (PagedWorkspaceMetadata: a deep copy of the
// workspace metadata, tens of MiB in a large workspace): a resource route's
// authorization (resourceAllowed), the public API's records (publicMetadata)
// and handlers that read the projection themselves (workspaceData). Without a
// bound, a burst of project or document reads on a busy server piles up one
// projection per waiting request until the replica runs out of memory.
//
// It is a separate metadataReadGate instance from the bootstrap's, with its
// own settings, so page loads and resource reads never starve each other. The
// slot is taken lazily, right before the first projection a request builds,
// and held until the request ends (the handler keeps reading the projection);
// a request that builds none never waits. Reads (GET, HEAD) are refused with
// 503 + Retry-After when the queue is full or after the wait, and the web
// client retries them; writes wait for a slot (or until their client leaves),
// since a write is not retried.

// projectionGateFromEnv reads FLOW_RESOURCE_READ_CONCURRENCY (default 6),
// FLOW_RESOURCE_READ_QUEUE_LIMIT (default 128) and
// FLOW_RESOURCE_READ_QUEUE_TIMEOUT (default 15s).
func projectionGateFromEnv() *metadataReadGate {
	return newMetadataReadGate(envInt("FLOW_RESOURCE_READ_CONCURRENCY", 6), envInt("FLOW_RESOURCE_READ_QUEUE_LIMIT", 128), envDuration("FLOW_RESOURCE_READ_QUEUE_TIMEOUT", 15*time.Second), 0)
}

type projectionHoldKey struct{}

// projectionHold is one request's claim on the projection gate.
type projectionHold struct {
	gate *metadataReadGate
	mu   sync.Mutex
	held bool
	// retryAfter is set when a read was refused (the response is a 503).
	retryAfter time.Duration
}

// holdProjections scopes the gate to each request: whatever slot the request
// takes is released when it ends.
func (g *metadataReadGate) holdProjections(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hold := &projectionHold{gate: g}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), projectionHoldKey{}, hold)))
		hold.mu.Lock()
		held := hold.held
		hold.held = false
		hold.mu.Unlock()
		if held {
			<-g.slots
		}
	})
}

// acquireProjection takes the request's slot before it builds a projection.
// It is false when the request must not build one: a read the gate refused
// (projectionRefused tells the caller to answer 503) or a client that left.
// Requests outside an HTTP request scope (chat and MCP workflows) are not
// gated here.
func acquireProjection(r *http.Request) bool {
	return acquireProjectionSlot(r, r.Method == http.MethodGet || r.Method == http.MethodHead)
}

// acquireProjectionWaiting is acquireProjection for a caller that cannot
// answer 503 (it returns data, not a response): it waits for the slot, or
// until the client leaves.
func acquireProjectionWaiting(r *http.Request) {
	acquireProjectionSlot(r, false)
}

func acquireProjectionSlot(r *http.Request, bounded bool) bool {
	hold, ok := r.Context().Value(projectionHoldKey{}).(*projectionHold)
	if !ok || hold.gate == nil {
		return true
	}
	hold.mu.Lock()
	defer hold.mu.Unlock()
	if hold.held {
		return true
	}
	if hold.retryAfter > 0 && bounded {
		return false
	}
	admission, retryAfter := hold.gate.admit(r, "resource read", bounded)
	switch admission {
	case gateAdmitted:
		hold.held = true
		hold.gate.served.Add(1)
		return true
	case gateBusy:
		hold.retryAfter = retryAfter
	}
	return false
}

// projectionRefused reports whether the gate refused this request a slot,
// and how long the client should wait before retrying.
func projectionRefused(r *http.Request) (time.Duration, bool) {
	hold, ok := r.Context().Value(projectionHoldKey{}).(*projectionHold)
	if !ok {
		return 0, false
	}
	hold.mu.Lock()
	defer hold.mu.Unlock()
	return hold.retryAfter, hold.retryAfter > 0
}

// writeProjectionBusy answers a refused read: 503 with Retry-After.
func writeProjectionBusy(w http.ResponseWriter, retryAfter time.Duration) {
	(&metadataReadGate{}).busy(w, retryAfter)
}
