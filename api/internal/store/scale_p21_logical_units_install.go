package store

import (
	"context"
	"errors"
	"sync/atomic"
)

const (
	metadataLogicalUnitsFallbackDisk = iota
	metadataLogicalUnitsFallbackWork
	metadataLogicalUnitsFallbackCoverage
	metadataLogicalUnitsFallbackOther
)

// Fixed reason classes record optional optimization failures without retaining
// arbitrary errors, source bodies, workspace identities or filesystem paths.
var metadataLogicalUnitsFallbacks [4]atomic.Uint64

type metadataLogicalStreamedCollection struct{ metadataAggregateStoredNode }

func (n metadataLogicalStreamedCollection) logicalBase() metadataLogicalNode { return n.value }
func (n metadataLogicalStreamedCollection) logicalMapEntries(ctx context.Context, visit func(metadataAggregateNode, metadataAggregateNode) error) error {
	return n.value.MapEntries(ctx, func(key, value metadataLogicalNode) error {
		return visit(metadataAggregateStoredNode{key}, metadataAggregateStoredNode{value})
	})
}

// Installed sources prefer the point-indexed representation. Conversion is an
// optional optimization of an already validated source; retaining that source
// on failure preserves the already-supported bounded-memory behavior.
// The caller transfers its source reference only on success.
func metadataLogicalUnitsForInstalled(ctx context.Context, source *metadataLogicalSource) (*metadataLogicalSource, error) {
	if source.units != nil {
		return source, nil
	}
	packed, err := metadataLogicalUnitsImport(ctx, source)
	if err == nil {
		if err := ctx.Err(); err != nil {
			packed.Release()
			return nil, err
		}
		source.Release()
		return packed, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	reason := metadataLogicalUnitsFallbackOther
	switch {
	case errors.Is(err, ErrRecordSpoolBudget):
		reason = metadataLogicalUnitsFallbackDisk
	case errors.Is(err, ErrMetadataWorkBudget), errors.Is(err, ErrMetadataPageBudget):
		reason = metadataLogicalUnitsFallbackWork
	case errors.Is(err, ErrBoundedMetadataCoverage):
		reason = metadataLogicalUnitsFallbackCoverage
	}
	metadataLogicalUnitsFallbacks[reason].Add(1)
	return source, nil
}
