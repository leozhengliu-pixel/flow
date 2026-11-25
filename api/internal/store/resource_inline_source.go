package store

import (
	"context"
)

// Collections the point resource readers' canonical SQL path consults row by
// row: the resource itself, its references and its display records.
var (
	resourceReadCanonicalFields          = []string{"projects", "initiatives", "initiativeRelations", "projectStatuses", "users", "labels"}
	resourceAuthorizationCanonicalFields = []string{"projects", "initiatives", "initiativeRelations"}
)

// inlineSnapshotSource prepares the canonical (non-current) point read of a
// workspace whose stored root keeps one of fields inline: duplicate, empty or
// over-long IDs leave such a collection without per-record rows, so a
// row-by-row read would miss its entries. It then replaces r.source with the
// logical source of the reader's own SQL snapshot, which holds inline and
// row-backed entries alike, for the installed (logical) readers to answer
// from. It reports false, changing nothing, when every field has rows.
func (r *ResourceRead) inlineSnapshotSource(ctx context.Context, tx *sqlTx, workspace string, fields []string) (bool, error) {
	s := r.store
	inline, err := s.inlineMetadataFields(ctx, tx, workspace, fields...)
	if err != nil || len(inline) == 0 {
		return false, err
	}
	// As the resident loader (reloadResidentWorkspace): the root parser
	// holds its own lease, and normalization admits its retained base.
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	parser, releaseParser, err := s.admitMetadataWork(ctx, 2<<20)
	if err != nil {
		return false, err
	}
	defer releaseParser()
	raw, err := s.readRawMetadataLogicalSourceValidated(parser, tx, workspace)
	if err != nil {
		return false, err
	}
	defer raw.Release()
	if err := validateMetadataLogicalRootIdentity(parser, raw); err != nil {
		return false, err
	}
	source, err := s.normalizeMetadataLogicalSource(ctx, raw)
	if err != nil {
		return false, err
	}
	if r.source != nil {
		r.source.Release()
	}
	r.source = source
	return true, nil
}
