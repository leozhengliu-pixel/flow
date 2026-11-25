package store

import (
	"context"

	"flow/api/internal/boundedcrdt"
)

const metadataAggregateTraversalFrame = uint64(4096)

// metadataAggregateTraversal owns recursive walk/encoder stack high-water.
// Its 4 KiB frame bound matches the metered decoder's proven stack bound;
// two child frames are funded before a recursive child prologue can run.
// Siblings reuse the high-water reservation. Graph storage is owned separately.
type metadataAggregateTraversal struct {
	ctx          context.Context
	admit        boundedcrdt.Admission
	active, high uint64
	releases     []func()
	closed       bool
}

func beginMetadataAggregateTraversal(ctx context.Context, admit boundedcrdt.Admission) (*metadataAggregateTraversal, error) {
	work := &metadataAggregateTraversal{ctx: ctx, admit: admit}
	if err := work.reserve(metadataAggregateTraversalFrame * 3); err != nil {
		work.Close()
		return nil, err
	}
	work.high = metadataAggregateTraversalFrame * 3
	return work, nil
}

func (w *metadataAggregateTraversal) reserve(bytes uint64) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	// Each independent allowance also owns its lease, closure and release-
	// slice growth, rather than hiding bookkeeping outside the stack bound.
	if bytes > metadataWorkBytes-256 {
		return ErrMetadataWorkBudget
	}
	release, err := w.admit(bytes + 256)
	if err == nil {
		w.releases = append(w.releases, release)
	}
	return err
}

func (w *metadataAggregateTraversal) enter() error {
	if w == nil || w.closed {
		return ErrBoundedMetadataCoverage
	}
	if err := w.ctx.Err(); err != nil {
		return err
	}
	next := w.active + metadataAggregateTraversalFrame
	ahead := next + 2*metadataAggregateTraversalFrame
	if ahead > metadataWorkBytes {
		return ErrMetadataWorkBudget
	}
	if ahead > w.high {
		if err := w.reserve(ahead - w.high); err != nil {
			return err
		}
		w.high = ahead
	}
	w.active = next
	return nil
}

func (w *metadataAggregateTraversal) leave() { w.active -= metadataAggregateTraversalFrame }

// Marshal sees only the already-normalized acyclic JSON graph. Sizing has
// already admitted its maximum depth. Beyond 1,000 containers encoding/json
// also keeps a depth-indexed cycle-detection map; admit its native ownership
// before Marshal can add entries, even though this graph contains no cycles.
func (w *metadataAggregateTraversal) encoding(depth int) error {
	if depth <= 1000 {
		return w.ctx.Err()
	}
	entries := uint64(depth - 1000 + 1)
	// The existing shallow-map bound covers larger string/interface slots;
	// it also covers these interface/empty-value slots and boxed slice keys.
	if entries > (metadataWorkBytes-1024)/512 {
		return ErrMetadataWorkBudget
	}
	return w.reserve(entries*512 + 1024)
}

func (w *metadataAggregateTraversal) Close() {
	if w == nil || w.closed {
		return
	}
	w.closed = true
	for _, release := range w.releases {
		release()
	}
	w.releases = nil
	w.admit, w.ctx = nil, nil
}
