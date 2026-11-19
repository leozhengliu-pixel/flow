package store

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// The legacy-view lane (coldAccessorView) serializes the replica's
// whole-workspace decodes of the legacy accessors (WorkspaceMetadata,
// WorkspaceMetadataFields, BootstrapFor, BootstrapForUser, a COLD request's
// projection and authorization view). Its wait is bounded like the COLD
// slot's queue (coldSlotQueue): FIFO among waiters, at most
// FLOW_COLD_QUEUE_LIMIT of them (default 64), each for at most
// FLOW_COLD_QUEUE_TIMEOUT (default 15s). Before, a waiter waited for as long
// as its context allowed, and the accessors that carry no context
// (WorkspaceMetadata, WorkspaceMetadataFields, BootstrapFor) waited forever.
//
// A refused wait makes the accessor answer as it answers a cancelled one
// (no workspace; the signatures carry no error). So the server can answer
// 503 + Retry-After instead of that answer, the refusal is recorded on the
// operation's context (WithLegacyViewBusyRecorder, which the server installs
// on every request with bounded metadata on), and a refusal with no recorder
// (a context-less accessor) is counted process-wide
// (UnrecordedLegacyViewRefusals).

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
	lane <- struct{}{}
	return func() { <-lane }
}

// LegacyViewWaitingForTesting reports how many waiters the legacy-view lane has.
func (s *SQLiteStore) LegacyViewWaitingForTesting() int64 {
	return s.metadataResidency.legacyViewLimits().waiting.Load()
}

// waitLegacyViewLane takes the lane (a buffered channel of one) for
// accessor. It fails with ErrColdMetadataBusy when the queue is full or the
// wait times out (recording the refusal), and with the context's error when
// the caller leaves.
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
	if limits.waiting.Add(1) > limits.limit.Load() {
		limits.waiting.Add(-1)
		refuseLegacyView(ctx, accessor, "queue full", limits)
		return ErrColdMetadataBusy
	}
	defer limits.waiting.Add(-1)
	timer := time.NewTimer(time.Duration(limits.timeout.Load()))
	defer timer.Stop()
	select {
	case lane <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		refuseLegacyView(ctx, accessor, "wait timed out", limits)
		return ErrColdMetadataBusy
	}
}

type legacyViewBusyKey struct{}

type legacyViewBusy struct{ refused atomic.Bool }

// legacyViewRefusals counts every refused legacy-view wait;
// unrecordedLegacyViewRefusals those whose context carried no recorder.
var legacyViewRefusals, unrecordedLegacyViewRefusals atomic.Int64

func refuseLegacyView(ctx context.Context, accessor, why string, limits *legacyViewLimits) {
	legacyViewRefusals.Add(1)
	if busy, ok := ctx.Value(legacyViewBusyKey{}).(*legacyViewBusy); ok {
		busy.refused.Store(true)
	} else {
		unrecordedLegacyViewRefusals.Add(1)
	}
	slog.Warn("bounded metadata: legacy view wait refused", "accessor", accessor, "reason", why,
		"timeout", time.Duration(limits.timeout.Load()), "waiting", limits.waiting.Load())
}

// WithLegacyViewBusyRecorder returns ctx carrying a recorder of refused
// legacy-view waits, and the report of whether an operation carrying it was
// refused.
func WithLegacyViewBusyRecorder(ctx context.Context) (context.Context, func() bool) {
	busy := &legacyViewBusy{}
	return context.WithValue(ctx, legacyViewBusyKey{}, busy), busy.refused.Load
}

// UnrecordedLegacyViewRefusals reports how many legacy-view waits were
// refused process-wide for an operation whose context carried no recorder.
func UnrecordedLegacyViewRefusals() int64 { return unrecordedLegacyViewRefusals.Load() }

// LegacyViewRefusalsForTesting reports how many legacy-view waits were refused.
func LegacyViewRefusalsForTesting() int64 { return legacyViewRefusals.Load() }
