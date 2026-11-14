package main

import (
	"context"
)

// Member erasure needs only the workspace identity before its separate account
// and erasure operations. An empty principal keeps this installed-core lookup
// independent of membership SQL; no mutable bootstrap value escapes.
func (s *server) eraseMemberWorkspaceID(ctx context.Context, workspace string) (string, bool) {
	if !s.store.BoundedMetadataEnabled() {
		data, ok := s.store.WorkspaceMetadata(workspace)
		return data.Workspace.ID, ok
	}
	if workspace == "" {
		data, ok := s.store.WorkspaceMetadataFields(workspace, "workspace")
		return data.Workspace.ID, ok
	}
	_, _, _, data, ok := s.store.WorkspaceAccessStatus(ctx, workspace, "")
	return data.Workspace.ID, ok
}
