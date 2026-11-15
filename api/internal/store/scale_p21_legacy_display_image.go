package store

import "context"

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
		_, changed := refreshed.(metadataAggregateRefreshCollection)
		return refreshed, changed, nil
	})
	if err != nil {
		return nil, err
	}
	defer display.Release()
	return refreshMetadataLogicalIssueReferences(ctx, display)
}
