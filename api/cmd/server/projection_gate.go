package main

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"flow/api/internal/store"
	"golang.org/x/sync/semaphore"
)

// The projection gate bounds the resource requests that build the viewer's
// whole workspace projection (PagedWorkspaceMetadata: a deep copy of the
// workspace metadata, tens of MiB in a large workspace): a resource route's
// authorization (resourceAllowed), the public API's records (publicMetadata)
// and handlers that read the projection themselves (workspaceData). Without a
// bound, a burst of project or document reads on a busy server piles up one
// projection per waiting request until the replica runs out of memory.
//
// Legacy issue bootstrap and resource reads keep separate concurrency gates
// and share the byte budget below. A resource slot is taken lazily, right
// before the first projection, and held until the request ends.
// A request that builds none never waits. Reads (GET, HEAD) are refused with
// 503 + Retry-After when the queue is full or after the wait, and the web
// client retries them; writes wait for a slot (or until their client leaves),
// since a write is not retried.

// projectionGateFromEnv reads FLOW_RESOURCE_READ_CONCURRENCY (default 6),
// FLOW_RESOURCE_READ_QUEUE_LIMIT (default 128) and
// FLOW_RESOURCE_READ_QUEUE_TIMEOUT (default 15s).
func projectionGateFromEnv() *metadataReadGate {
	return newMetadataReadGate(envInt("FLOW_RESOURCE_READ_CONCURRENCY", 6), envInt("FLOW_RESOURCE_READ_QUEUE_LIMIT", 128), envDuration("FLOW_RESOURCE_READ_QUEUE_TIMEOUT", 15*time.Second), 0)
}

type legacyProjectionBudget struct {
	estimate func(*http.Request) int64
	bytes    int64
	used     *semaphore.Weighted
}

func newLegacyProjectionBudget(estimate func(*http.Request) int64, bytes int64) *legacyProjectionBudget {
	return &legacyProjectionBudget{estimate: estimate, bytes: bytes, used: semaphore.NewWeighted(bytes)}
}

func legacyProjectionBudgetBytes() int64 {
	// Follow the configured metadata memory ceiling so large hosts keep their
	// concurrency. A snapshot larger than the budget can only run alone.
	return max(1, store.LegacyProjectionBudgetBytes())
}

type projectionHoldKey struct{}

// projectionHold is one request's claim on the projection gate.
type projectionHold struct {
	gate *metadataReadGate
	mu   sync.Mutex
	held bool
	free func()
	// retryAfter is set when a projection or authorization read is refused.
	retryAfter time.Duration
}

// holdProjections scopes the gate to each request: whatever slot the request
// takes is released when it ends.
func (g *metadataReadGate) holdProjections(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hold := &projectionHold{gate: g}
		// Released even when the handler panics (an aborted streamed
		// response does): a slot that is never returned would leave the
		// gate full and every later read answered 503.
		defer hold.release()
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), projectionHoldKey{}, hold)))
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
// until the client leaves. False means the client left: build nothing.
func acquireProjectionWaiting(r *http.Request) bool {
	return acquireProjectionSlot(r, false)
}

// releaseProjection returns a slot before the request ends. COLD writes
// release the authorization projection before entering their mutation because
// their handlers either rebuild the state around the write or run on a COLD
// facade. A later handler projection can acquire the slot again.
func releaseProjection(r *http.Request) {
	hold, ok := r.Context().Value(projectionHoldKey{}).(*projectionHold)
	if !ok || hold == nil || hold.gate == nil {
		return
	}
	hold.release()
}

func (hold *projectionHold) release() {
	hold.mu.Lock()
	defer hold.mu.Unlock()
	if !hold.held {
		return
	}
	hold.held = false
	if hold.free != nil {
		hold.free()
		hold.free = nil
	}
	hold.gate.active.Add(-1)
	<-hold.gate.slots
}

func acquireProjectionSlot(r *http.Request, bounded bool) bool {
	if store.InColdFacade(r.Context()) ||
		(store.InColdScope(r.Context()) && r.Method != http.MethodGet && r.Method != http.MethodHead) ||
		registeredColdRoute(r) {
		// A COLD write's facade or legacy-view lane already bounds its
		// authorization projection, so it must not wait for a second gate
		// before the COLD operation can be admitted. Registered COLD reads
		// make the same guarantee through their retained facade.
		return true
	}
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
	started := time.Now()
	admission, retryAfter := hold.gate.admit(r, "resource read", bounded)
	switch admission {
	case gateAdmitted:
		free, retryAfter, ok := hold.gate.reserveProjection(r, started, bounded)
		if !ok {
			<-hold.gate.slots
			hold.retryAfter = retryAfter
			return false
		}
		hold.free = free
		hold.held = true
		hold.gate.active.Add(1)
		hold.gate.served.Add(1)
		return true
	case gateBusy:
		hold.retryAfter = retryAfter
	}
	return false
}

func (g *metadataReadGate) reserveProjection(r *http.Request, started time.Time, bounded bool) (func(), time.Duration, bool) {
	budget := g.projectionBudget
	if budget == nil {
		return nil, 0, true
	}
	ctx := r.Context()
	if bounded {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, started.Add(g.wait))
		defer cancel()
	}
	bytes := min(budget.bytes, max(1, budget.estimate(r)))
	if ctx.Err() == nil {
		acquired := budget.used.TryAcquire(bytes)
		if !acquired && (!bounded || g.maxQueue > 0) {
			acquired = budget.used.Acquire(ctx, bytes) == nil
		}
		if acquired {
			return func() { budget.used.Release(bytes) }, 0, true
		}
	}
	if r.Context().Err() != nil {
		g.cancelled.Add(1)
		return nil, 0, false
	}
	g.rejected.Add(1)
	return nil, 5 * time.Second, false
}

func registeredColdRoute(r *http.Request) bool {
	if !store.InColdScope(r.Context()) {
		return false
	}
	_, selected := scaleColdAuthorizationRoute(r)
	return selected
}

// An authorization read can fail before it has made a permission decision.
// Preserve the retry delay for the middleware that consumes the bool result.
func noteAuthorizationBusy(r *http.Request, err error) {
	retryAfter := storeBusyRetryAfter(err)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, store.ErrResourceAccessChanged) {
		retryAfter = busyDeadlineRetryAfter
	}
	if retryAfter <= 0 {
		return
	}
	hold, _ := r.Context().Value(projectionHoldKey{}).(*projectionHold)
	if hold == nil {
		// Scoped authorization can run without a projection gate.
		*r = *r.WithContext(context.WithValue(r.Context(), projectionHoldKey{}, &projectionHold{retryAfter: retryAfter}))
		return
	}
	hold.mu.Lock()
	hold.retryAfter = max(hold.retryAfter, retryAfter)
	hold.mu.Unlock()
}

// projectionRefused reports how long to wait after a projection or
// authorization read was refused.
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

func writeProjectionRefusal(w http.ResponseWriter, r *http.Request) bool {
	if retryAfter, refused := projectionRefused(r); refused {
		writeProjectionBusy(w, retryAfter)
		return true
	}
	return false
}
