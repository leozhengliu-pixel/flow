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
// metadataWorkDemand records what an attempt holds and how much it would
// have needed at a refusal. An attempt whose live admissions plus the
// refused allowance fit the budget gets a contention refusal
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
	mu           sync.Mutex
	active, need int
	disk         *atomic.Int64
}

// metadataWorkRetryWait bounds a writer's wait for room in the budget, as
// metadataWorkBudget.acquire bounds a waiting admission.
var metadataWorkRetryWait atomic.Int64

func init() { metadataWorkRetryWait.Store(int64(5 * time.Second)) }

func withMetadataWorkDemand(ctx context.Context) (context.Context, *metadataWorkDemand) {
	demand := &metadataWorkDemand{}
	demand.disk = metadataLogicalDiskDemand(ctx)
	if demand.disk == nil {
		demand.disk = new(atomic.Int64)
	}
	return context.WithValue(ctx, metadataWorkDemandKey{}, demand), demand
}

func trackMetadataWorkDemand(ctx context.Context) context.Context {
	if metadataWorkDemandFrom(ctx) == nil {
		ctx, _ = withMetadataWorkDemand(ctx)
	}
	return ctx
}

func metadataWorkDemandFrom(ctx context.Context) *metadataWorkDemand {
	demand, _ := ctx.Value(metadataWorkDemandKey{}).(*metadataWorkDemand)
	return demand
}

func (d *metadataWorkDemand) fits(bytes, limit int) bool {
	if d == nil {
		return bytes <= limit
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return bytes <= limit-d.active
}

func (d *metadataWorkDemand) charge(bytes int) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.active += bytes
	d.mu.Unlock()
}

func (d *metadataWorkDemand) refuse(bytes int) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.need = max(d.need, d.active+bytes)
	d.mu.Unlock()
}

// awaitMetadataWork waits until the budget has room for the failed attempt
// again (everything it held at refusal plus the refused allowance) and
// reports whether it may run once more. It answers false at once when the
// attempt was refused no allowance for want of room, or needs more than the
// whole budget, and when ctx ends or deadline passes first.
func (s *SQLiteStore) awaitMetadataWork(ctx context.Context, demand *metadataWorkDemand, deadline time.Time) bool {
	if s == nil || s.metadataResidency == nil || demand == nil {
		return false
	}
	demand.mu.Lock()
	need := demand.need
	demand.mu.Unlock()
	budget := s.metadataResidency.work
	if need <= 0 {
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

// retryMetadataWriteWork waits after the attempt releases its transaction and
// write lock. The attempt stops retries before running a callback that can
// leave results in caller-owned memory.
func (s *SQLiteStore) retryMetadataWriteWork(ctx context.Context, attempt func(context.Context, func()) error) error {
	if s.metadataResidency == nil {
		return attempt(ctx, func() {})
	}
	deadline := time.Now().Add(time.Duration(metadataWorkRetryWait.Load()))
	for {
		attemptCtx, demand := withMetadataWorkDemand(ctx)
		allowRetry := true
		err := attempt(attemptCtx, func() { allowRetry = false })
		if !allowRetry || !MetadataWorkContended(err) || !s.awaitMetadataWork(ctx, demand, deadline) {
			return err
		}
		traceMark(ctx, "work-wait")
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

// SetMetadataWorkRetryWaitForTesting replaces the admission and retry wait
// for room in the work budget until the returned restore runs.
func SetMetadataWorkRetryWaitForTesting(wait time.Duration) func() {
	if !testing.Testing() {
		return func() {}
	}
	previous := metadataWorkRetryWait.Swap(int64(wait))
	return func() { metadataWorkRetryWait.Store(previous) }
}
