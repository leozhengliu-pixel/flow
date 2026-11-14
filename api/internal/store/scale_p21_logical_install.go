package store

import (
	"context"
	"database/sql"
	"slices"
	"testing"
)

type metadataLogicalReadKey struct{}

type metadataLogicalRead struct {
	database  *sqlDatabase
	workspace string
	source    *metadataLogicalSource
	owner     *InstalledMetadataRead
}

func metadataLogicalUnpinnedContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, metadataLogicalReadKey{}, nil)
}

// The caller owns the returned reference independently of later installations.
func (s *SQLiteStore) retainMetadataLogicalSource(ctx context.Context, workspace string) (*metadataLogicalSource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	writing, _ := ctx.Value(scopedMetadataWriteKey{}).(bool)
	if !writing {
		if read, ok := ctx.Value(metadataLogicalReadKey{}).(metadataLogicalRead); ok {
			if workspace == "" {
				workspace = read.workspace
			}
			if read.database != s.db || read.workspace != workspace || read.owner == nil {
				return nil, ErrMetadataRecordQuery
			}
			return read.owner.retainSource(ctx, workspace)
		}
	}
	r := s.metadataResidency
	if r == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	source := r.logical[workspace]
	if source == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	if err := source.Retain(); err != nil {
		return nil, err
	}
	return source, nil
}

// Installation takes the candidate's reference. Prepare it before SQL commit;
// replacing the source afterward cannot allocate or fail.
func (s *SQLiteStore) installMetadataLogicalSource(workspace string, candidate *metadataLogicalSource, last ...bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.metadataResidency
	r.mu.Lock()
	if r.logical == nil {
		r.logical = map[string]*metadataLogicalSource{}
	}
	previous := r.logical[workspace]
	r.logical[workspace] = candidate
	if len(last) != 0 && last[0] {
		s.lastWorkspaceKey = workspace
	}
	r.mu.Unlock()
	if previous != nil {
		previous.Release()
	}
}

// Test views transfer the actual owner-installed logical generation. This is
// deliberately separate from cold SQL recovery, which normalizes its source.
func (s *SQLiteStore) captureLogicalTestView(ctx context.Context, owner *SQLiteStore, keys []string) error {
	owner.mu.Lock()
	if !slices.Contains(owner.logicalTestViews, s.metadataResidency) {
		owner.logicalTestViews = append(owner.logicalTestViews, s.metadataResidency)
	}
	owner.mu.Unlock()
	for _, workspace := range keys {
		owner.mu.RLock()
		data, found := owner.workspaces[workspace]
		if !found {
			owner.mu.RUnlock()
			return sql.ErrNoRows
		}
		stamp := copyStamp{workspaceID: data.Workspace.ID, revision: -1}
		if known, ok := owner.copyRevisionLocked(workspace); ok {
			stamp = copyStamp{workspaceID: known.workspaceID, revision: known.revision}
		}
		source, err := captureMetadataLogicalSource(ctx, data, stamp)
		owner.mu.RUnlock()
		if err != nil {
			return err
		}
		packed, err := metadataLogicalUnitsForInstalled(ctx, source)
		if err != nil {
			source.Release()
			return err
		}
		source = packed
		s.installMetadataLogicalSource(workspace, source)
	}
	return nil
}

// RefreshInstalledMetadataViewForTesting resets a differential test view to the
// literal owner's current installed preimage. It performs no SQL hydration and
// does not enable production coverage or override any caller-held source pin.
func (s *SQLiteStore) RefreshInstalledMetadataViewForTesting(ctx context.Context, owner *SQLiteStore, workspace string) error {
	if !testing.Testing() || owner == nil || s == owner || s.db != owner.db || s.metadataResidency == nil {
		return ErrBoundedMetadataCoverage
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx = metadataLogicalUnpinnedContext(ctx)
	owner.mu.RLock()
	if workspace == "" {
		workspace = owner.lastWorkspaceKey
	}
	owner.mu.RUnlock()
	if err := s.captureLogicalTestView(ctx, owner, []string{workspace}); err != nil {
		return err
	}
	source, err := s.retainMetadataLogicalSource(ctx, workspace)
	if err != nil {
		return err
	}
	defer source.Release()
	probe := &SQLiteStore{metadataResidency: newMetadataResidency()}
	policy, cost, err := probe.metadataLogicalPolicy(ctx, source)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.workspaces[workspace] = policy
	s.forgetCopyRevisionLocked(workspace)
	s.metadataResidency.mu.Lock()
	s.metadataResidency.policyBytes += cost - s.metadataResidency.policy[workspace]
	s.metadataResidency.policy[workspace] = cost
	s.metadataResidency.mu.Unlock()
	s.mu.Unlock()
	return nil
}

func (s *SQLiteStore) releaseMetadataLogicalSources() {
	s.mu.Lock()
	views := s.logicalTestViews
	s.logicalTestViews = nil
	if s.metadataResidency != nil {
		views = append(views, s.metadataResidency)
	}
	s.mu.Unlock()
	for _, residency := range views {
		residency.mu.Lock()
		sources := residency.logical
		residency.logical = nil
		residency.mu.Unlock()
		for _, source := range sources {
			source.Release()
		}
	}
}
