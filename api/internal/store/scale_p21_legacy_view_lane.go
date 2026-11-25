package store

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// The legacy-view lane (coldAccessorView) bounds the replica's concurrent
// (FLOW_COLD_VIEW_LANES, see accessorLane) whole-workspace decodes of the
// legacy accessors (WorkspaceMetadata,
// WorkspaceMetadataFields, BootstrapFor, BootstrapForUser, a COLD request's
// projection and authorization view).
//
// Only a live request's wait is bounded, like its wait for the COLD slot
// (coldSlotQueue): FIFO among waiters, at most FLOW_COLD_QUEUE_LIMIT of them
// (default 64), each for at most FLOW_COLD_QUEUE_TIMEOUT (default 15s). A
// request is a context the server marked (WithLegacyViewBusyRecorder, on
// every request with bounded metadata on) whose handler has not returned.
// A refused wait makes the accessor answer as it answers a cancelled one (no
// workspace; the signatures carry no error), and is recorded on that
// request's own marker, so the server answers 503 + Retry-After for exactly
// the request that was refused.
//
// Every other wait is bounded only by its own context, as the legacy store
// waited: background COLD work (WithColdWork, WorkerContext: loop runs, the
// application-agent worker, chat ticks, the review-import scheduler, trash
// purge), work a request left running after its handler returned, and the
// accessors that take no context (WorkspaceMetadata, WorkspaceMetadataFields,
// BootstrapFor). A busy lane delays them; it never turns into "not found".

type legacyViewLimits struct {
	waiting atomic.Int64
	limit   atomic.Int64
	timeout atomic.Int64
}

var legacyViewLaneLimits sync.Map // *metadataResidency -> *legacyViewLimits

func (r *metadataResidency) legacyViewLimits() *legacyViewLimits {
	if limits, ok := legacyViewLaneLimits.Load(r); ok {
		return limits.(*legacyViewLimits)
	}
	limits := &legacyViewLimits{}
	limits.limit.Store(int64(envPositiveInt("FLOW_COLD_QUEUE_LIMIT", 64)))
	limits.timeout.Store(int64(envPositiveDuration("FLOW_COLD_QUEUE_TIMEOUT", 15*time.Second)))
	actual, _ := legacyViewLaneLimits.LoadOrStore(r, limits)
	return actual.(*legacyViewLimits)
}

// SetLegacyViewQueueForTesting sets the legacy-view lane's queue limit and
// wait timeout.
func (s *SQLiteStore) SetLegacyViewQueueForTesting(limit int, timeout time.Duration) {
	limits := s.metadataResidency.legacyViewLimits()
	limits.limit.Store(int64(limit))
	limits.timeout.Store(int64(timeout))
}

// HoldLegacyViewLaneForTesting takes the legacy-view lane until the returned
// release is called.
func (s *SQLiteStore) HoldLegacyViewLaneForTesting() func() {
	lane := s.metadataResidency.accessorLane()
	for range cap(lane) {
		lane <- struct{}{}
	}
	return func() {
		for range cap(lane) {
			<-lane
		}
	}
}

// LegacyViewWaitingForTesting reports how many waiters the legacy-view lane has.
func (s *SQLiteStore) LegacyViewWaitingForTesting() int64 {
	return s.metadataResidency.legacyViewLimits().waiting.Load()
}

// waitLegacyViewLane takes a lane (a slot of a buffered channel) for
// accessor. A live request's wait (boundedColdWait) fails with
// ErrColdMetadataBusy when the queue is full or the wait times out,
// recording the refusal on the request; any wait fails with the context's
// error when the caller leaves.
func (r *metadataResidency) waitLegacyViewLane(ctx context.Context, lane chan struct{}, accessor string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case lane <- struct{}{}:
		return nil
	default:
	}
	limits := r.legacyViewLimits()
	request := requestColdWait(ctx)
	if limits.waiting.Add(1) > limits.limit.Load() && request != nil {
		limits.waiting.Add(-1)
		refuseLegacyView(request, accessor, "queue full", limits)
		return ErrColdMetadataBusy
	}
	defer limits.waiting.Add(-1)
	defer observeLegacyViewWait(time.Now())
	var expired <-chan time.Time
	if request != nil {
		timer := time.NewTimer(time.Duration(limits.timeout.Load()))
		defer timer.Stop()
		expired = timer.C
	}
	select {
	case lane <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-expired:
		refuseLegacyView(request, accessor, "wait timed out", limits)
		return ErrColdMetadataBusy
	}
}

type legacyViewBusyKey struct{}

// legacyViewBusy marks one request: refused records a refused wait, ended
// that its handler returned (work it left running waits unbounded).
type legacyViewBusy struct{ refused, ended atomic.Bool }

// requestColdWait returns the live request marker of ctx, or nil when its
// waits are bounded only by ctx: background COLD work, work a request left
// running, a context no request marked.
func requestColdWait(ctx context.Context) *legacyViewBusy {
	if _, work := ctx.Value(coldWorkKey{}).(string); work {
		return nil
	}
	busy, ok := ctx.Value(legacyViewBusyKey{}).(*legacyViewBusy)
	if !ok || busy == nil || busy.ended.Load() {
		return nil
	}
	return busy
}

// legacyViewRefusals counts every refused legacy-view wait.
var legacyViewRefusals atomic.Int64

// The waits for the legacy-view lane that found it held: how many, how long
// in total and at most (load measurement).
var legacyViewWaits, legacyViewWaitNanos, legacyViewWaitMax atomic.Int64

func observeLegacyViewWait(started time.Time) {
	waited := int64(time.Since(started))
	legacyViewWaits.Add(1)
	legacyViewWaitNanos.Add(waited)
	storeMaxInt64(&legacyViewWaitMax, waited)
}

func refuseLegacyView(busy *legacyViewBusy, accessor, why string, limits *legacyViewLimits) {
	legacyViewRefusals.Add(1)
	busy.refused.Store(true)
	slog.Warn("bounded metadata: legacy view wait refused", "accessor", accessor, "reason", why,
		"timeout", time.Duration(limits.timeout.Load()), "waiting", limits.waiting.Load())
}

// WithLegacyViewBusyRecorder marks ctx as one request's: its waits for the
// legacy-view lane and the COLD slot are bounded and a refused one is
// recorded on it. refused reports whether one was; end marks the request's
// handler returned, after which work it left running waits unbounded.
func WithLegacyViewBusyRecorder(ctx context.Context) (_ context.Context, refused func() bool, end func()) {
	busy := &legacyViewBusy{}
	return context.WithValue(ctx, legacyViewBusyKey{}, busy), busy.refused.Load, func() { busy.ended.Store(true) }
}

// LegacyViewRefusalsForTesting reports how many legacy-view waits were refused.
func LegacyViewRefusalsForTesting() int64 { return legacyViewRefusals.Load() }
