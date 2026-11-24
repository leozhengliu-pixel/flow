package store

import (
	"context"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

// ReadAgentTaskResource supplies the viewer's projected policy and matching
// anchor headers during inspect. The callback must not retain the headers.
// An empty userID selects the unprojected development view.
func (s *SQLiteStore) ReadAgentTaskResource(ctx context.Context, workspace, userID string, key *domain.APIKey, kind, ref string, inspect func(domain.Bootstrap)) error {
	switch kind {
	case "project", "document":
	default:
		return ErrMetadataRecordQuery
	}
	if s.BoundedMetadataEnabled() {
		return s.readInstalledAgentTaskResource(ctx, workspace, userID, key, kind, ref, inspect)
	}
	data, found := s.legacyAgentTaskResource(workspace, kind, ref)
	if !found {
		return ErrAuthForbidden
	}
	if userID != "" {
		var err error
		data.Viewer, data.ViewerRole, data.TeamMembers, err = s.resourceAccessPrincipal(ctx, data.Workspace.ID, userID)
		if err != nil {
			return err
		}
	}
	return projectAgentTaskResource(ctx, userID, data, inspect)
}

func (s *SQLiteStore) legacyAgentTaskResource(workspace, kind, ref string) (domain.Bootstrap, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	source, found := s.workspaces[workspace]
	if !found {
		return domain.Bootstrap{}, false
	}
	data := resourcePolicyMetadata(source)
	data.Viewer = domain.User{ID: source.Viewer.ID, App: source.Viewer.App}
	switch kind {
	case "project":
		for _, item := range source.Projects {
			if item.ID == ref {
				data.Projects = append(data.Projects, domain.Project{ID: item.ID, TeamIDs: slices.Clone(item.TeamIDs)})
			}
		}
	case "document":
		for _, item := range source.Documents {
			if documentReadRank(item, DocumentReadRequest{Ref: ref}) != 0 {
				data.Documents = append(data.Documents, domain.Document{
					ID: item.ID, SlugID: item.SlugID, PreviousSlugIDs: slices.Clone(item.PreviousSlugIDs),
					TeamIDs: slices.Clone(item.TeamIDs), InheritedTeamIDs: slices.Clone(item.InheritedTeamIDs),
					Creator: domain.User{ID: item.Creator.ID}, Permissions: slices.Clone(item.Permissions),
				})
			}
		}
	}
	return data, true
}

func (s *SQLiteStore) readInstalledAgentTaskResource(ctx context.Context, workspace, userID string, key *domain.APIKey, kind, ref string, inspect func(domain.Bootstrap)) error {
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	installed, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil {
		return err
	}
	if !found {
		return ErrAuthForbidden
	}
	defer installed.Close()
	source, err := installed.retainSource(ctx, installed.workspace)
	if err != nil {
		return err
	}
	owner := &ResourceRead{store: s, source: source}
	defer owner.Close()
	if err := owner.reserve(ctx, 64<<10); err != nil {
		return err
	}
	ctx = owner.metadataOutputContext(ctx)
	var access AccessContext
	if userID != "" {
		access, err = s.ResolveAccess(ctx, installed.workspace, userID, key)
		if err != nil {
			return err
		}
		if err := s.validateMetadataLogicalOrigin(ctx, metadataLogicalOrigin{workspace: installed.workspace, stamp: source.Stamp()}, access.WorkspaceID); err != nil {
			return err
		}
	}
	data, err := owner.installedResourcePolicy(ctx)
	if err != nil {
		return err
	}
	if userID == "" {
		index, _, _ := metadataLogicalField("viewer")
		viewer, err := owner.projectNode(ctx, source.fields[index], "ID", "App")
		if err != nil {
			return err
		}
		data.Viewer = viewer.Interface().(domain.User)
	} else {
		policy := access.policy.metadata
		data.Viewer, data.ViewerRole = policy.Viewer, policy.ViewerRole
		data.TeamMembers = slices.Clone(policy.TeamMembers)
	}
	err = owner.visitNative(ctx, kind+"s", func(_ int, node metadataLogicalNode) error {
		var match bool
		var err error
		if kind == "document" {
			match, err = agentTaskDocumentRefMatches(ctx, node, ref)
		} else {
			match, err = owner.nativeID(ctx, node, ref)
		}
		if err != nil || !match {
			return err
		}
		if kind == "project" {
			value, err := owner.projectNode(ctx, node, "ID", "TeamIDs")
			if err == nil {
				data.Projects = append(data.Projects, value.Interface().(domain.Project))
			}
			return err
		}
		value, err := owner.projectNode(ctx, node, "ID", "SlugID", "PreviousSlugIDs", "Creator", "Permissions", "TeamIDs", "InheritedTeamIDs")
		if err == nil {
			data.Documents = append(data.Documents, value.Interface().(domain.Document))
		}
		return err
	})
	if err != nil {
		return err
	}
	if userID != "" {
		if err := s.ValidateAccess(ctx, access); err != nil {
			return err
		}
	}
	return projectAgentTaskResource(ctx, userID, data, inspect)
}

func agentTaskDocumentRefMatches(ctx context.Context, document metadataLogicalNode, ref string) (matched bool, err error) {
	if ref == "" {
		return false, nil
	}
	equal := func(value metadataLogicalNode) error {
		match, err := value.StringEqual(ctx, ref)
		matched = matched || match
		return err
	}
	err = document.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error {
		switch key.String() {
		case "ID", "SlugID":
			return equal(value)
		case "PreviousSlugIDs":
			return value.Children(ctx, func(_ reflect.Value, item metadataLogicalNode) error { return equal(item) })
		}
		return nil
	})
	return matched, err
}

func projectAgentTaskResource(ctx context.Context, userID string, data domain.Bootstrap, inspect func(domain.Bootstrap)) error {
	if userID == "" {
		data.ViewerRole = "admin"
	} else {
		teams := make(map[string]bool, len(data.Teams))
		for _, team := range data.Teams {
			if teamVisibleToUser(data, team.ID, userID, data.ViewerRole) {
				teams[team.ID] = true
			}
		}
		scope := viewerTeamScope(data, teams, userID, data.ViewerRole)
		data.Teams = slices.DeleteFunc(data.Teams, func(team domain.Team) bool { return !teams[team.ID] })
		// The bootstrap checks links against the visible team rows and the
		// original settings, before narrowing those settings to the viewer.
		links := NewDocumentLinkScope(data.Teams, data.TeamSettings)
		data.Documents = slices.DeleteFunc(data.Documents, func(item domain.Document) bool {
			return !bootstrapDocumentVisible(&data, item, scope, links)
		})
		data.Projects = slices.DeleteFunc(data.Projects, func(item domain.Project) bool {
			return len(item.TeamIDs) > 0 && !slices.ContainsFunc(item.TeamIDs, func(id string) bool { return scope.Content[id] })
		})
		for i := range data.Projects {
			data.Projects[i].TeamIDs = slices.DeleteFunc(data.Projects[i].TeamIDs, func(id string) bool { return !scope.Content[id] })
		}
		data.TeamMembers = slices.DeleteFunc(data.TeamMembers, func(member domain.TeamMember) bool { return !teams[member.TeamID] })
		for id := range data.TeamSettings {
			if !teams[id] {
				delete(data.TeamSettings, id)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	inspect(data)
	return nil
}
