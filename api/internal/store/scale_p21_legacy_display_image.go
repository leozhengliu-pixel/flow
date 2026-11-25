package store

import (
	"context"
	"sync/atomic"
)

// metadataDisplayRefreshesKept counts display passes that kept a stored
// collection (tests).
var metadataDisplayRefreshesKept atomic.Int64

// metadataDisplayRefreshKeep enables keeping unchanged collections (tests turn
// it off for the reference pass).
var metadataDisplayRefreshKeep = func() *atomic.Bool { b := &atomic.Bool{}; b.Store(true); return b }()

// refreshLegacyDisplayImage composes the literal pre-callback display passes.
// It neither normalizes the source nor substitutes the request actor.
func (s *SQLiteStore) refreshLegacyDisplayImage(ctx context.Context, source *metadataLogicalSource) (*metadataLogicalSource, error) {
	if s == nil || !s.BoundedMetadataEnabled() || source == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	ctx = metadataLogicalSourceScratchContext(metadataLogicalUnpinnedContext(ctx), source)
	display, err := source.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		base := metadataAggregateStoredNode{value: original}
		refreshed := metadataAggregateRefreshField(source, name, base)
		collection, changed := refreshed.(metadataAggregateRefreshCollection)
		if changed && metadataDisplayRefreshKeep.Load() {
			// A collection whose users all equal the directory's is kept as
			// stored, not rewritten record by record.
			unchanged, err := collection.unchanged(ctx)
			if err != nil {
				return nil, false, err
			}
			if unchanged {
				metadataDisplayRefreshesKept.Add(1)
				return base, false, nil
			}
		}
		return refreshed, changed, nil
	})
	if err != nil {
		return nil, err
	}
	defer display.Release()
	return refreshMetadataLogicalIssueReferences(ctx, display)
}
