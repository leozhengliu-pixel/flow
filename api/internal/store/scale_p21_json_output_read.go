package store

import (
	"context"
	"reflect"
	"sync"
)

// MetadataJSONRead owns encoding, buffered bytes and a decoded JSON result.
// Close belongs to the containing operation, including response-derived errors.
type MetadataJSONRead struct {
	store    *SQLiteStore
	mu       sync.Mutex
	releases []func()
	children []func()
	output   *metadataOutputOwner
	encoded  int
	err      error
	closed   bool
}

func (s *SQLiteStore) BeginMetadataJSONRead(ctx context.Context) (*MetadataJSONRead, error) {
	if !s.BoundedMetadataEnabled() {
		return nil, ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	read := &MetadataJSONRead{store: s}
	read.output = &metadataOutputOwner{register: read.addChild}
	if err := read.reserve(ctx, 128<<10); err != nil {
		read.Close()
		return nil, err
	}
	return read, nil
}

// Context binds subordinate store projections to the same output lifetime.
func (r *MetadataJSONRead) Context(ctx context.Context) context.Context {
	if r == nil || r.output == nil {
		return ctx
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	return context.WithValue(ctx, metadataOutputOwnerKey{}, r.output)
}

func (r *MetadataJSONRead) addChild(close func()) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || close == nil {
		return false
	}
	r.children = append(r.children, close)
	return true
}

func (r *MetadataJSONRead) check(ctx context.Context) error {
	if r == nil {
		return ErrBoundedMetadataCoverage
	}
	if err := ctx.Err(); err != nil {
		return r.fail(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		if r.err == nil {
			r.err = ErrBoundedMetadataCoverage
		}
		return ErrBoundedMetadataCoverage
	}
	return r.err
}

func (r *MetadataJSONRead) fail(err error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err == nil {
		r.err = err
	}
	return err
}

func (r *MetadataJSONRead) reserve(ctx context.Context, cost uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		if r.err == nil {
			r.err = ErrBoundedMetadataCoverage
		}
		return ErrBoundedMetadataCoverage
	}
	if r.err != nil {
		return r.err
	}
	if cost == 0 || cost > metadataWorkBytes {
		r.err = ErrMetadataWorkBudget
		return r.err
	}
	_, release, err := r.store.tryAdmitAdditionalMetadataWork(ctx, int(cost))
	if err != nil {
		r.err = err
		return err
	}
	r.releases = append(r.releases, release)
	return nil
}

// ReserveEncoding runs before the JSON encoder allocates its output buffer.
func (r *MetadataJSONRead) ReserveEncoding(ctx context.Context, value any) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	cost, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(value), 0)
	if err != nil {
		return r.fail(err)
	}
	if cost > metadataWorkBytes-(64<<10) {
		return r.fail(ErrMetadataWorkBudget)
	}
	return r.reserve(ctx, uint64(cost)+(64<<10))
}

// ReserveEncoded grows a byte-buffer allowance before copying bytes. Four
// copies cover buffer capacity growth and transient copies; decode is separate.
func (r *MetadataJSONRead) ReserveEncoded(ctx context.Context, size int) error {
	if r == nil {
		return ErrBoundedMetadataCoverage
	}
	if size < 0 || size > metadataWorkBytes/4 {
		return r.fail(ErrMetadataWorkBudget)
	}
	const page = 64 << 10
	capacity := (size + page - 1) / page * page
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		if r.err == nil {
			r.err = ErrBoundedMetadataCoverage
		}
		return ErrBoundedMetadataCoverage
	}
	if r.err != nil {
		return r.err
	}
	if err := ctx.Err(); err != nil {
		r.err = err
		return err
	}
	if capacity <= r.encoded {
		return nil
	}
	_, release, err := r.store.tryAdmitAdditionalMetadataWork(ctx, (capacity-r.encoded)*4)
	if err != nil {
		r.err = err
		return err
	}
	r.encoded = capacity
	r.releases = append(r.releases, release)
	return nil
}

// ReserveDecode precedes Unmarshal into the original destination. Its existing
// graph is retained too, since absent JSON fields and map members can survive.
func (r *MetadataJSONRead) ReserveDecode(ctx context.Context, raw []byte, previous any) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	cost, err := metadataAggregatePayloadJSONWork(ctx, raw)
	if err != nil {
		return r.fail(err)
	}
	if previous != nil {
		retained, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(previous), 0)
		if err != nil {
			return r.fail(err)
		}
		if retained > metadataWorkBytes-cost {
			return r.fail(ErrMetadataWorkBudget)
		}
		cost += retained
	}
	if cost > metadataWorkBytes-(64<<10) {
		return r.fail(ErrMetadataWorkBudget)
	}
	return r.reserve(ctx, cost+(64<<10))
}

func (r *MetadataJSONRead) Err() error {
	if r == nil {
		return ErrBoundedMetadataCoverage
	}
	r.mu.Lock()
	err := r.err
	r.mu.Unlock()
	if err != nil {
		return err
	}
	if r.output == nil {
		return nil
	}
	r.output.mu.Lock()
	defer r.output.mu.Unlock()
	return r.output.err
}

func (r *MetadataJSONRead) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	releases, children := r.releases, r.children
	r.releases, r.children = nil, nil
	r.mu.Unlock()
	for i := len(children) - 1; i >= 0; i-- {
		children[i]()
	}
	for _, release := range releases {
		release()
	}
}
