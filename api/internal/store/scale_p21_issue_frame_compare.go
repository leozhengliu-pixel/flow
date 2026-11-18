package store

import (
	"context"
	"io"
)

func legacyIssueStoredJSON(ctx context.Context, owner *ResourceRead, frames *metadataLogicalBuilder, node metadataLogicalNode) (_ *RecordSpool, err error) {
	stored, err := frames.writeAggregateNode(legacyIssueStoredRecordNode{node}, 0)
	if err != nil {
		return nil, err
	}
	encoded, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return nil, err
	}
	defer encoded.Close()
	if err := legacyProjectStreamNode(ctx, owner, stored, encoded); err != nil {
		return nil, err
	}
	result, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			result.Close()
		}
	}()
	if err := legacyProjectCopyStoredJSON(ctx, result, io.NewSectionReader(encoded, 0, encoded.bytes)); err != nil {
		return nil, err
	}
	return result, nil
}

// Callers supply the exact captured SQL preimage. All body comparisons use
// complete encoded bytes, retaining the literal normalization and reference
// refresh order without decoding the Issue's bodies into a native graph.
func legacyIssueRecordFrameEqual(ctx context.Context, s *SQLiteStore, frames *metadataLogicalBuilder, previous legacySessionJSONPart, next metadataLogicalNode, source *metadataLogicalSource) (bool, error) {
	if s == nil || frames == nil || next.typ == nil || previous.size < 0 {
		return false, ErrMetadataRecordQuery
	}
	if previous.size == 0 {
		return false, ctx.Err()
	}
	owner := &ResourceRead{store: s}
	defer owner.Close()
	if err := owner.reserve(ctx, logicalJSONStreamWork); err != nil {
		return false, err
	}
	after, err := legacyIssueStoredJSON(ctx, owner, frames, next)
	if err != nil {
		return false, err
	}
	defer after.Close()
	equal, err := legacySessionRawEqual(ctx, previous, legacySessionSpoolPart(after))
	if err != nil || equal {
		return equal, err
	}
	before, err := legacyIssueCanonicalRecord(ctx, s, frames, previous)
	if err != nil {
		return false, err
	}
	normalized, err := legacyIssueStoredJSON(ctx, owner, frames, before)
	if err != nil {
		return false, err
	}
	equal, err = legacySessionRawEqual(ctx, legacySessionSpoolPart(normalized), legacySessionSpoolPart(after))
	normalized.Close()
	after.Close()
	if err != nil || equal {
		return equal, err
	}
	leftState, err := issueHotField(ctx, before, "State")
	if err != nil {
		return false, err
	}
	leftType, err := issueHotField(ctx, leftState, "Type")
	if err != nil {
		return false, err
	}
	rightState, err := issueHotField(ctx, next, "State")
	if err != nil {
		return false, err
	}
	rightType, err := issueHotField(ctx, rightState, "Type")
	if err != nil {
		return false, err
	}
	sameType, err := metadataAggregateStringsEqual(ctx,
		metadataAggregateStoredNode{leftType}, metadataAggregateStoredNode{rightType})
	if err != nil || !sameType {
		return false, err
	}
	if source == nil {
		return false, ErrMetadataRecordQuery
	}
	var refreshed [2]*RecordSpool
	defer func() {
		for _, spool := range refreshed {
			if spool != nil {
				spool.Close()
			}
		}
	}()
	for index, node := range []metadataLogicalNode{before, next} {
		value, err := frames.writeAggregateNode(metadataLogicalIssueRecord{
			source: source, base: metadataAggregateStoredNode{node},
		}, 0)
		if err != nil {
			return false, err
		}
		refreshed[index], err = legacyIssueStoredJSON(ctx, owner, frames, value)
		if err != nil {
			return false, err
		}
	}
	return legacySessionRawEqual(ctx, legacySessionSpoolPart(refreshed[0]), legacySessionSpoolPart(refreshed[1]))
}
