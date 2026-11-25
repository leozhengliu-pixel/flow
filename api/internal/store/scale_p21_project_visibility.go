package store

import (
	"context"
	"slices"

	"flow/api/internal/domain"
)

// BeginProjectVisibilityRead selects the first exact installed project ID.
// Its result contains only ID and TeamIDs, not a complete Project. The bool
// distinguishes an absent workspace from an absent project. This is trusted
// storage; callers retain the read through their existing visibility decision.
func (s *SQLiteStore) BeginProjectVisibilityRead(ctx context.Context, workspace, id string) (_ *ResourceRead, found bool, err error) {
	if !s.BoundedMetadataEnabled() {
		s.mu.RLock()
		defer s.mu.RUnlock()
		if workspace == "" {
			workspace = s.lastWorkspaceKey
		}
		data, found := s.workspaces[workspace]
		if !found {
			return nil, false, nil
		}
		read := &ResourceRead{}
		for _, project := range data.Projects {
			if project.ID == id {
				read.Data.Projects = []domain.Project{{ID: project.ID, TeamIDs: slices.Clone(project.TeamIDs)}}
				read.Allowed = true
				break
			}
		}
		return read, true, nil
	}
	// ProjectByID has no context pin: it always observes the current install.
	ctx = metadataLogicalUnpinnedContext(ctx)
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	installed, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return nil, found, err
	}
	defer installed.Close()
	source, err := installed.retainSource(ctx, installed.workspace)
	if err != nil {
		return nil, true, err
	}
	read := &ResourceRead{store: s, source: source}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	if err := read.reserve(ctx, 2<<20); err != nil {
		return nil, true, err
	}
	err = read.visitNative(ctx, "projects", func(_ int, record metadataLogicalNode) error {
		matches, err := read.nativeID(ctx, record, id)
		if err != nil || !matches {
			return err
		}
		value, err := read.projectNode(ctx, record, "ID", "TeamIDs")
		if err != nil {
			return err
		}
		read.Data.Projects = []domain.Project{value.Interface().(domain.Project)}
		read.Allowed = true
		return ErrMetadataIterationDone
	})
	if err != nil {
		return nil, true, err
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	return read, true, nil
}
