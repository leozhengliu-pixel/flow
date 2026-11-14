package store

import (
	"context"
	"slices"
	"sync"
	"time"
)

const metadataTimestampInputLimit = 64
const metadataTimestampInputBytes = metadataTimestampInputLimit * 512

// MetadataTimestampInputs owns only locations created by its RFC3339 parser.
// It is single-use and is active only inside its store's mutation callback.
type MetadataTimestampInputs struct {
	store     *SQLiteStore
	mu        sync.Mutex
	used      bool
	operation *metadataTimestampInputOperation
}

type metadataTimestampInputsKey struct{}
type metadataTimestampOperationKey struct{}

type metadataTimestampInputOperation struct {
	scope     *MetadataTimestampInputs
	ctx       context.Context
	lease     *metadataWorkLease
	bound     bool
	parsing   bool
	epoch     uint64
	calls     int
	locations []*time.Location
}

func (s *SQLiteStore) WithMetadataTimestampInputs(ctx context.Context) (context.Context, *MetadataTimestampInputs) {
	inputs := &MetadataTimestampInputs{store: s}
	return context.WithValue(ctx, metadataTimestampInputsKey{}, inputs), inputs
}

func (s *SQLiteStore) startMetadataTimestampInputs(ctx context.Context) (context.Context, func(), error) {
	inputs, _ := ctx.Value(metadataTimestampInputsKey{}).(*MetadataTimestampInputs)
	if inputs == nil {
		return ctx, func() {}, nil
	}
	inputs.mu.Lock()
	defer inputs.mu.Unlock()
	if inputs.store != s || inputs.used || inputs.operation != nil {
		return ctx, nil, ErrMetadataRecordQuery
	}
	inputs.used = true
	op := &metadataTimestampInputOperation{scope: inputs}
	inputs.operation = op
	finish := func() {
		inputs.mu.Lock()
		defer inputs.mu.Unlock()
		op.bound, op.ctx, op.lease, op.locations = false, nil, nil, nil
		inputs.operation = nil
	}
	return context.WithValue(ctx, metadataTimestampOperationKey{}, op), finish, nil
}

func metadataTimestampLeaseLive(inputs *MetadataTimestampInputs, lease *metadataWorkLease) bool {
	if inputs.store.metadataResidency == nil || lease == nil {
		return false
	}
	budget := inputs.store.metadataResidency.work
	budget.mu.Lock()
	defer budget.mu.Unlock()
	return lease.budget == budget && lease.live && lease.bytes >= metadataTimestampInputBytes
}

func (s *SQLiteStore) bindMetadataTimestampInputs(ctx context.Context) (func(), error) {
	op, _ := ctx.Value(metadataTimestampOperationKey{}).(*metadataTimestampInputOperation)
	if op == nil {
		return func() {}, nil
	}
	inputs := op.scope
	inputs.mu.Lock()
	defer inputs.mu.Unlock()
	lease, _ := ctx.Value(metadataWorkLeaseKey{}).(*metadataWorkLease)
	if inputs.store != s || inputs.operation != op || op.bound || !metadataTimestampLeaseLive(inputs, lease) {
		return nil, ErrMetadataRecordQuery
	}
	op.bound, op.parsing, op.ctx, op.lease = true, false, ctx, lease
	op.epoch++
	op.calls, op.locations = 0, nil
	unbind := func() {
		inputs.mu.Lock()
		defer inputs.mu.Unlock()
		op.bound, op.ctx, op.lease, op.locations = false, nil, nil, nil
	}
	return unbind, nil
}

func (inputs *MetadataTimestampInputs) ParseRFC3339(value string) (time.Time, error) {
	if inputs == nil {
		return time.Time{}, ErrMetadataRecordQuery
	}
	inputs.mu.Lock()
	op := inputs.operation
	if op == nil || !op.bound || op.parsing || !metadataTimestampLeaseLive(inputs, op.lease) {
		inputs.mu.Unlock()
		return time.Time{}, ErrMetadataRecordQuery
	}
	if op.calls >= metadataTimestampInputLimit {
		inputs.mu.Unlock()
		return time.Time{}, ErrMetadataWorkBudget
	}
	if err := op.ctx.Err(); err != nil {
		inputs.mu.Unlock()
		return time.Time{}, err
	}
	op.calls++
	op.parsing = true
	ctx, epoch := op.ctx, op.epoch
	inputs.mu.Unlock()
	parseLocal := time.Local
	parsed, err := time.Parse(time.RFC3339, value)
	inputs.mu.Lock()
	defer inputs.mu.Unlock()
	if op.epoch == epoch {
		op.parsing = false
	}
	if inputs.operation != op || !op.bound || op.epoch != epoch || !metadataTimestampLeaseLive(inputs, op.lease) {
		return time.Time{}, ErrMetadataRecordQuery
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return time.Time{}, ctxErr
	}
	if err != nil {
		return parsed, err
	}
	location, err := metadataTimestampParsedLocation(parsed, parseLocal)
	if err != nil {
		return time.Time{}, err
	}
	if location != nil && !slices.Contains(op.locations, location) {
		op.locations = append(op.locations, location)
	}
	return parsed, nil
}

func metadataTimestampParsedLocation(value time.Time, parseLocal *time.Location) (*time.Location, error) {
	location := value.Location()
	if location == time.UTC || location == metadataLogicalProcessLocal {
		return nil, nil
	}
	// time.Parse may reuse time.Local instead of constructing a fixed zone.
	// A replaced global is not provenance for an arbitrary caller-owned graph.
	if location == parseLocal {
		return nil, ErrBoundedMetadataCoverage
	}
	return location, nil
}

func (b *metadataLogicalBuilder) inheritTimestampInputs(ctx context.Context) error {
	op, _ := ctx.Value(metadataTimestampOperationKey{}).(*metadataTimestampInputOperation)
	if op == nil {
		return nil
	}
	inputs := op.scope
	inputs.mu.Lock()
	defer inputs.mu.Unlock()
	if inputs.operation != op || !op.bound || op.parsing || !metadataTimestampLeaseLive(inputs, op.lease) {
		return ErrMetadataRecordQuery
	}
	for _, location := range op.locations {
		if !slices.Contains(b.storage.locations, location) {
			if err := b.retainAuditedLocation(location); err != nil {
				return err
			}
		}
	}
	return nil
}
