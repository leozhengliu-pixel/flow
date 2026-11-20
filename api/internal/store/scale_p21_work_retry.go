package store

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A bounded writer admits its work while it holds the write lock and a SQL
// transaction, so it never waits for the process work budget there: an
// admission other work has no room for fails at once with
// ErrMetadataWorkBudget. Such a refusal says nothing about the write itself,
// only that concurrent reads and writes held the budget at that moment.
//
// metadataWorkDemand records one write attempt's admissions: what it was
// granted and the allowance it was refused for want of room. A refusal of an
// attempt whose admissions together fit the budget is a contention refusal
// (errMetadataWorkContended): the writer, having committed nothing, waits
// outside its lock until the budget has room for the whole attempt again and
// runs it once more (awaitMetadataWork); one that still finds no room when
// the wait ends is answered 503 + Retry-After. A refusal of an attempt that
// would not fit even an idle budget stays the plain ErrMetadataWorkBudget.

// errMetadataWorkContended is the refusal of an admission the budget had no
// room for while other work held it. It is ErrMetadataWorkBudget (errors.Is)
// with the same text; MetadataWorkContended tells it from a refusal of work
// no budget admits (a size or allowance bound), which retrying cannot cure.
var errMetadataWorkContended error = metadataWorkContendedError{}

type metadataWorkContendedError struct{}

func (metadataWorkContendedError) Error() string { return ErrMetadataWorkBudget.Error() }
func (metadataWorkContendedError) Unwrap() error { return ErrMetadataWorkBudget }

// MetadataWorkContended reports whether err is a work budget refusal for
// want of room while concurrent work held the budget: the request may be
// retried (503 + Retry-After).
func MetadataWorkContended(err error) bool {
	return errors.Is(err, errMetadataWorkContended)
}

type metadataWorkDemandKey struct{}

type metadataWorkDemand struct {
	mu                sync.Mutex
	admitted, refused int
}

// metadataWorkRetryWait bounds a writer's wait for room in the budget, as
// metadataWorkBudget.acquire bounds a waiting admission.
var metadataWorkRetryWait atomic.Int64

func init() { metadataWorkRetryWait.Store(int64(5 * time.Second)) }

func withMetadataWorkDemand(ctx context.Context) (context.Context, *metadataWorkDemand) {
	demand := &metadataWorkDemand{}
	return context.WithValue(ctx, metadataWorkDemandKey{}, demand), demand
}

// metadataWorkDemandFits reports whether ctx belongs to a write attempt
// whose admissions so far and bytes more fit the whole budget: a refusal of
// bytes is then for want of room other work holds, not of the attempt's own
// size.
func metadataWorkDemandFits(ctx context.Context, bytes, limit int) bool {
	demand, ok := ctx.Value(metadataWorkDemandKey{}).(*metadataWorkDemand)
	if !ok || demand == nil {
		return false
	}
	demand.mu.Lock()
	defer demand.mu.Unlock()
	return bytes <= limit-demand.admitted
}

// noteMetadataWorkDemand records an admission (admitted) or a refusal for
// want of room (refused) on the attempt ctx belongs to.
func noteMetadataWorkDemand(ctx context.Context, admitted, refused int) {
	demand, ok := ctx.Value(metadataWorkDemandKey{}).(*metadataWorkDemand)
	if !ok || demand == nil {
		return
	}
	demand.mu.Lock()
	demand.admitted += admitted
	demand.refused = max(demand.refused, refused)
	demand.mu.Unlock()
}

// awaitMetadataWork waits until the budget has room for the failed attempt
// again (everything it was granted plus the allowance it was refused) and
// reports whether it may run once more. It answers false at once when the
// attempt was refused no allowance for want of room, or needs more than the
// whole budget, and when ctx ends or deadline passes first.
func (s *SQLiteStore) awaitMetadataWork(ctx context.Context, demand *metadataWorkDemand, deadline time.Time) bool {
	if s == nil || s.metadataResidency == nil || demand == nil {
		return false
	}
	demand.mu.Lock()
	need, refused := demand.admitted+demand.refused, demand.refused
	demand.mu.Unlock()
	budget := s.metadataResidency.work
	if refused <= 0 || need <= 0 {
		return false
	}
	wait := time.Until(deadline)
	if wait <= 0 {
		return false
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		budget.mu.Lock()
		if need > budget.limit {
			budget.mu.Unlock()
			return false
		}
		if need <= budget.limit-budget.used {
			budget.mu.Unlock()
			return true
		}
		changed := budget.changed
		budget.mu.Unlock()
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return false
		case <-changed:
		}
	}
}

// HoldMetadataWorkForTesting admits bytes of metadata work, as concurrent
// reads and writes would, until the returned release runs.
func (s *SQLiteStore) HoldMetadataWorkForTesting(bytes int) (func(), error) {
	if !testing.Testing() || s == nil || s.metadataResidency == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	_, release, err := s.tryAdmitAdditionalMetadataWork(context.Background(), bytes)
	return release, err
}

// MetadataWorkLimitForTesting reports the process work budget.
func (s *SQLiteStore) MetadataWorkLimitForTesting() int {
	if !testing.Testing() || s == nil || s.metadataResidency == nil {
		return 0
	}
	work := s.metadataResidency.work
	work.mu.Lock()
	defer work.mu.Unlock()
	return work.limit
}

// SetMetadataWorkRetryWaitForTesting replaces how long a bounded writer
// waits for room in the work budget until the returned restore runs.
func SetMetadataWorkRetryWaitForTesting(wait time.Duration) func() {
	if !testing.Testing() {
		return func() {}
	}
	previous := metadataWorkRetryWait.Swap(int64(wait))
	return func() { metadataWorkRetryWait.Store(previous) }
}
