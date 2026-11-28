package store

import (
	"context"
	"reflect"
	"sync"
)

// MetadataJSONRead owns encoding, buffered bytes and a decoded JSON result.
// Close belongs to the containing operation, including response-derived errors.
type MetadataJSONRead struct {
	store       *SQLiteStore
	mu          sync.Mutex
	releases    []func()
	children    []func()
	output      *metadataOutputOwner
	encodedWork int
	err         error
	closed      bool
}

const (
	metadataJSONEncodedPage = 64 << 10
	// Each admitted page can add a lease, context, release callback and
	// a share of the release slice's growth.
	metadataJSONBufferPageWork = metadataJSONBufferGrowthMultiplier*metadataJSONEncodedPage + 512
)

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
	if cost == 0 || cost > uint64(metadataWorkBytes()) {
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
	if cost > metadataWorkBytes()-(64<<10) {
		return r.fail(ErrMetadataWorkBudget)
	}
	return r.reserve(ctx, uint64(cost)+(64<<10))
}

// ReserveEncoded admits the next absolute size of an append-only bytes.Buffer.
// Capacity at least doubles at each growth, so total capacity is less than
// twice the final capacity, which is at most twice the rounded length.
// The build-specific multiplier accounts for any temporary growth slices.
// The decoder has a separate allowance.
func (r *MetadataJSONRead) ReserveEncoded(ctx context.Context, size int) error {
	return r.reserveEncoded(ctx, size, metadataJSONBufferPageWork)
}

// ReserveEncodedCopies uses the same output allowance to admit a fixed-size
// payload and its transport copies at four rounded widths.
func (r *MetadataJSONRead) ReserveEncodedCopies(ctx context.Context, size int) error {
	return r.reserveEncoded(ctx, size, 4*metadataJSONEncodedPage)
}

func (r *MetadataJSONRead) reserveEncoded(ctx context.Context, size, pageWork int) error {
	if r == nil {
		return ErrBoundedMetadataCoverage
	}
	if size < 0 {
		return r.fail(ErrMetadataWorkBudget)
	}
	pages := size / metadataJSONEncodedPage
	if size%metadataJSONEncodedPage != 0 {
		pages++
	}
	if pages > metadataWorkBytes()/pageWork {
		return r.fail(ErrMetadataWorkBudget)
	}
	work := pages * pageWork
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
	if work <= r.encodedWork {
		return nil
	}
	_, release, err := r.store.tryAdmitAdditionalMetadataWork(ctx, work-r.encodedWork)
	if err != nil {
		r.err = err
		return err
	}
	r.encodedWork = work
	r.releases = append(r.releases, release)
	return nil
}

// MetadataJSONDecodeWork estimates one encoding/json Unmarshal into a fresh value.
func MetadataJSONDecodeWork(ctx context.Context, raw []byte) (int, error) {
	cost, err := metadataAggregatePayloadJSONWork(ctx, raw)
	if err != nil {
		return 0, err
	}
	const scratch = 64 << 10
	if cost > uint64(metadataWorkBytes()-scratch) {
		return 0, ErrMetadataWorkBudget
	}
	return int(cost) + scratch, nil
}

// ReserveDecode precedes Unmarshal into the original destination. Its existing
// graph is retained too, since absent JSON fields and map members can survive.
func (r *MetadataJSONRead) ReserveDecode(ctx context.Context, raw []byte, previous any) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	cost, err := MetadataJSONDecodeWork(ctx, raw)
	if err != nil {
		return r.fail(err)
	}
	if previous != nil {
		retained, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(previous), 0)
		if err != nil {
			return r.fail(err)
		}
		if retained > uint64(metadataWorkBytes()-cost) {
			return r.fail(ErrMetadataWorkBudget)
		}
		cost += int(retained)
	}
	return r.reserve(ctx, uint64(cost))
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
