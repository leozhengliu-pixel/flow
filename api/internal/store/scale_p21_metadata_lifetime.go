package store

import (
	"context"
	"reflect"
	"sync"

	"flow/api/internal/domain"
)

const metadataLifetimeDescriptorWork = 16 << 10
const metadataLifetimeOwners = 64

type metadataLifetimeKey struct{}

// MetadataLifetime owns explicitly transferred resources through one finite
// request or job batch. It never pins a source or replaces ordinary reads.
type MetadataLifetime struct {
	mu      sync.Mutex
	store   *SQLiteStore
	owners  map[*metadataLifetimeOwner]struct{}
	release func()
	closed  bool
}

type metadataLifetimeOwner struct {
	mu       sync.Mutex
	lifetime *MetadataLifetime
	close    func()
	closed   bool
}

// BeginMetadataLifetime returns the literal input context off mode. Bounded
// callers close the returned owner after all synchronous output processing.
func (s *SQLiteStore) BeginMetadataLifetime(ctx context.Context) (context.Context, *MetadataLifetime, error) {
	if !s.BoundedMetadataEnabled() {
		return ctx, nil, nil
	}
	clean := context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	_, release, err := s.tryAdmitAdditionalMetadataWork(clean, metadataLifetimeDescriptorWork)
	if err != nil {
		return ctx, nil, err
	}
	lifetime := &MetadataLifetime{
		store: s, release: release,
		owners: make(map[*metadataLifetimeOwner]struct{}, metadataLifetimeOwners),
	}
	return context.WithValue(clean, metadataLifetimeKey{}, lifetime), lifetime, nil
}

// MetadataLifetime requires this exact store's live caller owner. A missing
// owner is an explicit coverage failure, never a prematurely closed result.
func (s *SQLiteStore) MetadataLifetime(ctx context.Context) (*MetadataLifetime, error) {
	lifetime, ok := ctx.Value(metadataLifetimeKey{}).(*MetadataLifetime)
	if !ok || lifetime == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	if lifetime.store != s {
		return nil, ErrMetadataRecordQuery
	}
	if _, err := lifetime.context(ctx); err != nil {
		return nil, err
	}
	return lifetime, nil
}

func (l *MetadataLifetime) context(ctx context.Context) (context.Context, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, errMetadataLogicalClosed
	}
	clean := context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	return context.WithValue(clean, metadataLifetimeKey{}, l), nil
}

// Hold takes cleanup ownership even on error. release closes that owner early
// and removes its descriptor; otherwise the lifetime closes it.
func (l *MetadataLifetime) Hold(close func()) (release func(), err error) {
	if close == nil {
		return nil, ErrMetadataRecordQuery
	}
	if l == nil {
		close()
		return nil, ErrBoundedMetadataCoverage
	}
	l.mu.Lock()
	if l.closed || len(l.owners) >= metadataLifetimeOwners {
		err = ErrMetadataWorkBudget
		if l.closed {
			err = errMetadataLogicalClosed
		}
		l.mu.Unlock()
		close()
		return nil, err
	}
	owner := &metadataLifetimeOwner{lifetime: l, close: close}
	l.owners[owner] = struct{}{}
	l.mu.Unlock()
	return owner.Close, nil
}

func (o *metadataLifetimeOwner) Close() {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return
	}
	o.closed = true
	lifetime, close := o.lifetime, o.close
	o.lifetime, o.close = nil, nil
	o.mu.Unlock()
	if lifetime != nil {
		lifetime.mu.Lock()
		delete(lifetime.owners, o)
		lifetime.mu.Unlock()
	}
	if close != nil {
		close()
	}
}

func (l *MetadataLifetime) Close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	owners, release := l.owners, l.release
	l.owners, l.release = nil, nil
	l.mu.Unlock()
	var panicValue any
	for owner := range owners {
		func() {
			defer func() {
				if recovered := recover(); recovered != nil && panicValue == nil {
					panicValue = recovered
				}
			}()
			owner.Close()
		}()
	}
	if release != nil {
		release()
	}
	if panicValue != nil {
		panic(panicValue)
	}
}

// MetadataResultSlot owns one retry-replaceable result and its complete
// processing/encoding allowance until Close or its caller lifetime closes.
type MetadataResultSlot struct {
	Data       any
	mu         sync.Mutex
	lifetime   *MetadataLifetime
	workBytes  int
	release    func()
	unregister func()
	closed     bool
}

// NewResultSlot reserves before callbacks allocate/capture their output.
func (l *MetadataLifetime) NewResultSlot(ctx context.Context, workBytes int) (*MetadataResultSlot, error) {
	if workBytes < metadataOwnedReductionMinimumWork || workBytes > metadataWorkBytes {
		return nil, ErrMetadataWorkBudget
	}
	bound, err := l.context(ctx)
	if err != nil {
		return nil, err
	}
	_, release, err := l.store.tryAdmitAdditionalMetadataWork(bound, workBytes)
	if err != nil {
		return nil, err
	}
	slot := &MetadataResultSlot{lifetime: l, workBytes: workBytes, release: release}
	unregister, err := l.Hold(slot.Close)
	if err != nil {
		return nil, err
	}
	slot.mu.Lock()
	closed := slot.closed
	if !closed {
		slot.unregister = unregister
	}
	slot.mu.Unlock()
	if closed {
		unregister()
		return nil, errMetadataLogicalClosed
	}
	if err := ctx.Err(); err != nil {
		slot.Close()
		return nil, err
	}
	return slot, nil
}

// Capture must run before mutation persistence and propagate errors to the
// transaction. A retry replaces the candidate within the same allowance.
func (s *MetadataResultSlot) Capture(ctx context.Context, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errMetadataLogicalClosed
	}
	s.Data = nil
	if _, err := s.lifetime.context(ctx); err != nil {
		return err
	}
	work, err := metadataLifetimeValueWork(ctx, value)
	if err != nil {
		return err
	}
	if work > s.workBytes {
		return ErrMetadataWorkBudget
	}
	s.Data = value
	return nil
}

// RetainValue adopts an already-owned value before processing or encoding.
// New allocation-producing callbacks use NewResultSlot before execution.
func (l *MetadataLifetime) RetainValue(ctx context.Context, value any) (*MetadataResultSlot, error) {
	if _, err := l.context(ctx); err != nil {
		return nil, err
	}
	work, err := metadataLifetimeValueWork(ctx, value)
	if err != nil {
		return nil, err
	}
	slot, err := l.NewResultSlot(ctx, work)
	if err != nil {
		return nil, err
	}
	if err := slot.Capture(ctx, value); err != nil {
		slot.Close()
		return nil, err
	}
	return slot, nil
}

func metadataLifetimeValueWork(ctx context.Context, value any) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	native := reflect.ValueOf(value)
	if !native.IsValid() {
		return metadataOwnedReductionMinimumWork, nil
	}
	if err := metadataLifetimeResultType(ctx, native); err != nil {
		return 0, err
	}
	retained, err := metadataInstalledRetainedCost(ctx, native, 0)
	if err != nil {
		return 0, err
	}
	encoding, err := metadataAggregatePayloadEncodingWork(ctx, native, 0)
	if err != nil {
		return 0, err
	}
	if retained > metadataWorkBytes-metadataOwnedReductionHandleWork ||
		uint64(encoding) > metadataWorkBytes-metadataOwnedReductionHandleWork-retained {
		return 0, ErrMetadataWorkBudget
	}
	return max(metadataOwnedReductionMinimumWork, int(retained)+encoding+metadataOwnedReductionHandleWork), nil
}

func metadataLifetimeResultType(ctx context.Context, value reflect.Value) error {
	if value.Type() == reflect.TypeFor[*domain.Bootstrap]() && !value.IsNil() {
		return metadataLifetimeResultType(ctx, value.Elem())
	}
	if value.Type() != reflect.TypeFor[domain.Bootstrap]() {
		return metadataInstalledResultType(ctx, value, 0)
	}
	// Bootstrap's ignored callbacks are nil outside their write-reader
	// lifetime. Nil carries no closure; a nonnil callback remains unsupported.
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		part := value.Field(i)
		if field.Tag.Get("json") == "-" && part.Kind() == reflect.Func && part.IsNil() {
			continue
		}
		if err := metadataInstalledResultType(ctx, part, 1); err != nil {
			return err
		}
	}
	return nil
}

func (s *MetadataResultSlot) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	release, unregister := s.release, s.unregister
	s.Data, s.lifetime, s.release, s.unregister = nil, nil, nil, nil
	s.mu.Unlock()
	if release != nil {
		release()
	}
	if unregister != nil {
		unregister()
	}
}
