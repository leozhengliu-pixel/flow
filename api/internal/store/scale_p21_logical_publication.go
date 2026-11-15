package store

import (
	"context"

	"flow/api/internal/domain"
)

// metadataLogicalPublication is owned by one transaction attempt. Its candidate
// is prepared before commit; Install only transfers that reference afterward.
// Policy changes still require the resident-policy writer's precommit check.
type metadataLogicalPublication struct {
	store     *SQLiteStore
	workspace string
	candidate *metadataLogicalSource
	capture   *metadataCommitStamp
	exact     bool
	last      bool
}

// Ownership of candidate transfers only on success. The caller holds writeMu
// from preparation through installation and must Close on every return path.
func (s *SQLiteStore) prepareMetadataLogicalPublication(ctx context.Context, tx *sqlTx, workspace string, candidate *metadataLogicalSource, baseStamp copyStamp, exactSource, last bool) (*metadataLogicalPublication, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if candidate == nil || tx == nil || s.metadataResidency == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	// Check exclusive candidate ownership before SQL can commit. No other
	// reference is published until Install, so final stamping cannot fail.
	if err := candidate.setStamp(candidate.Stamp()); err != nil {
		return nil, err
	}
	return &metadataLogicalPublication{
		store: s, workspace: workspace, candidate: candidate,
		capture: tx.captureMetadataCommitStampFrom(workspace, baseStamp),
		exact:   exactSource, last: last,
	}, nil
}

// Point writers use their native callback pre/postimages and actual merged
// deltas. Unselected warm values and skipped/merged canonical rows survive.
func (s *SQLiteStore) prepareMetadataLogicalDeltaPublication(ctx context.Context, tx *sqlTx, workspace string, source *metadataLogicalSource, before, next domain.Bootstrap, scope MetadataRecordScope, changes []metadataRecordChange, appendOrders map[metadataRecordKey]int, baseStamp copyStamp, exactSource, last bool) (*metadataLogicalPublication, error) {
	if source == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	candidate, err := source.Fork(ctx, before, next, scope, source.Stamp(), appendOrders)
	if err != nil {
		return nil, err
	}
	merged, err := metadataLogicalMergedPostimage(ctx, candidate, changes)
	candidate.Release()
	if err != nil {
		return nil, err
	}
	publication, err := s.prepareMetadataLogicalPublication(ctx, tx, workspace, merged, baseStamp, exactSource, last)
	if err != nil {
		merged.Release()
	}
	return publication, err
}

func (p *metadataLogicalPublication) Close() {
	if p != nil && p.candidate != nil {
		p.candidate.Release()
		p.candidate = nil
	}
}

func (p *metadataLogicalPublication) Install() error {
	if p == nil || p.candidate == nil {
		return ErrBoundedMetadataCoverage
	}
	final, ready := p.capture.result()
	if !ready {
		return ErrBoundedMetadataCoverage
	}
	stamp := copyStamp{workspaceID: final.workspaceID, revision: -1}
	if p.exact {
		stamp = final
	}
	if err := p.candidate.setStamp(stamp); err != nil {
		return err
	}
	p.store.installMetadataLogicalSource(p.workspace, p.candidate, p.last)
	p.candidate = nil
	p.store.mu.Lock()
	p.store.forgetCopyRevisionLocked(p.workspace)
	p.store.mu.Unlock()
	return nil
}
