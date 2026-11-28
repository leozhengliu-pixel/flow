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
	ctx       context.Context
	workspace string
	candidate *metadataLogicalSource
	capture   *metadataCommitStamp
	exact     bool
	last      bool
	// reload: the installed generation lacks another server's commit
	// (installedGenerationBehind), so Install reloads the generation from SQL
	// instead of installing the candidate (staleCommitReload).
	reload bool
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
	publication := &metadataLogicalPublication{
		store: s, ctx: ctx, workspace: workspace, candidate: candidate,
		capture: tx.captureMetadataCommitStampFrom(workspace, baseStamp),
		exact:   exactSource, last: last,
	}
	publication.reload = !exactSource && s.installedGenerationBehind(workspace, baseStamp)
	return publication, nil
}

// installedGenerationBehind reports whether the installed logical generation
// of workspace provably lacks a commit of another server, given base, the
// stored stamp this write's transaction read before its own increment: the
// generation is exact at another revision, or a conflict refresh patched only
// the records a lost race named (metadataLogicalSource.behind). The caller
// holds writeMu, so the installed generation is the one the write derives
// from. A generation whose stamp is merely unknown (this server's own
// patches) is no evidence of another commit and keeps its candidate.
func (s *SQLiteStore) installedGenerationBehind(workspace string, base copyStamp) bool {
	r := s.metadataResidency
	r.mu.Lock()
	defer r.mu.Unlock()
	source := r.logical[workspace]
	if source == nil {
		return false
	}
	stamp := source.Stamp()
	return source.behind.Load() || stamp.revision >= 0 && stamp != base
}

// staleCommitReload: a candidate derived from a generation that lacks another
// server's commit is that generation plus this write's records and the stored
// rows the write merged. The rest of the other commit (a run it appended, a
// definition it deleted, a record it changed beside the ones a conflict
// refresh replaced) is missing from it, and installed with an unknown stamp
// it would serve that state until something reloads the workspace. After the
// SQL commit Install therefore reloads the generation from SQL as a peer
// event's ReloadWorkspace does: under the writeMu the caller holds, on the
// store's lifetime rather than the request's context (the write is committed
// even when the request is cancelled now, and none of the write's context
// state belongs to the load). It then drops the candidate. Should the reload
// fail, the candidate is installed with its unknown stamp, marked behind: the
// next publication reloads.
func (p *metadataLogicalPublication) staleCommitReload() bool {
	if !p.reload {
		return false
	}
	ctx := context.Background()
	if p.store.lifecycle != nil {
		ctx = p.store.lifecycle
	}
	if err := p.store.reloadResidentWorkspace(ctx, p.workspace); err != nil {
		p.candidate.behind.Store(true)
		return false
	}
	if p.last {
		p.store.mu.Lock()
		p.store.lastWorkspaceKey = p.workspace
		p.store.mu.Unlock()
	}
	p.candidate.Release()
	p.candidate = nil
	return true
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
	if p.staleCommitReload() {
		return nil
	}
	stamp := copyStamp{workspaceID: final.workspaceID, revision: -1}
	if p.exact {
		stamp = final
	}
	if err := p.candidate.setStamp(stamp); err != nil {
		return err
	}
	p.store.installMetadataLogicalSourceTimed(p.ctx, p.workspace, p.candidate, p.last)
	p.candidate = nil
	p.store.mu.Lock()
	p.store.forgetCopyRevisionLocked(p.workspace)
	p.store.mu.Unlock()
	return nil
}
