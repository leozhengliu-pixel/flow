package store

import (
	"context"
	"strings"
)

// Retain creates an independent owner of this exact installed generation.
// Its descriptor, children and Close are independent of the original owner.
func (r *InstalledMetadataRead) Retain(ctx context.Context) (*InstalledMetadataRead, error) {
	if r == nil {
		return nil, ErrMetadataRecordQuery
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.closed || r.source == nil {
		r.mu.Unlock()
		return nil, errMetadataLogicalClosed
	}
	cost := 64<<10 + len(r.workspace)*8
	if cost > 2<<20 {
		r.mu.Unlock()
		return nil, ErrMetadataWorkBudget
	}
	_, release, err := r.store.tryAdmitAdditionalMetadataWork(ctx, cost)
	if err != nil {
		r.mu.Unlock()
		return nil, err
	}
	if err = r.source.Retain(); err != nil {
		r.mu.Unlock()
		release()
		return nil, err
	}
	retained := &InstalledMetadataRead{
		store: r.store, workspace: strings.Clone(r.workspace), source: r.source,
		release: release, selections: map[*OwnedMetadataSelection]struct{}{},
		reductions: map[*OwnedMetadataReduction]struct{}{},
	}
	r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		retained.Close()
		return nil, err
	}
	return retained, nil
}

// retainInstalledMetadataRead honors an incoming owner before consulting the
// store's current default or installed generation.
func (s *SQLiteStore) retainInstalledMetadataRead(ctx context.Context, workspace string) (*InstalledMetadataRead, bool, error) {
	incoming, found, err := installedMetadataReadFromContext(ctx, s, workspace)
	if err != nil {
		return nil, true, err
	}
	if found {
		retained, err := incoming.Retain(ctx)
		return retained, true, err
	}
	return s.BeginInstalledMetadataRead(ctx, workspace)
}
