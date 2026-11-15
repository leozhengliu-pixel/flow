package store

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// CurrentCommentActionSettings preserves AccountCollections' current installed
// source and contextless timing. Its selected output belongs to this read.
func (r *IssueWriteAuthorizationRead) CurrentCommentActionSettings(ctx context.Context, userID string) (domain.UserSettings, bool, error) {
	if r == nil || r.owner == nil {
		return domain.UserSettings{}, false, errMetadataLogicalClosed
	}
	r.owner.mu.Lock()
	closed := r.owner.closed
	r.owner.mu.Unlock()
	if closed {
		return domain.UserSettings{}, false, errMetadataLogicalClosed
	}
	ctx = metadataLogicalUnpinnedContext(context.WithoutCancel(ctx))
	current, found, err := r.owner.store.BeginInstalledMetadataRead(ctx, r.query.Workspace)
	if err != nil || !found {
		return domain.UserSettings{}, false, err
	}
	defer current.Close()
	var settings domain.UserSettings
	saved := false
	index, _, _ := metadataLogicalField("userSettings")
	err = current.source.fields[index].MapEntries(ctx, func(key, record metadataLogicalNode) error {
		equal, err := key.StringEqual(ctx, userID)
		if err != nil || !equal {
			return err
		}
		value, err := r.owner.projectNode(ctx, record, "UserID", "AgentEnabled", "SuggestedActionsOnComments")
		if err == nil {
			settings, saved = value.Interface().(domain.UserSettings), true
		}
		return err
	})
	return settings, saved, err
}

func (r *IssueWriteAuthorizationRead) loadPolicy(ctx context.Context, userID string, key *domain.APIKey) error {
	s := r.owner.store
	workspace := r.query.Workspace
	ctx = r.owner.metadataOutputContext(ctx)
	var caller domain.Bootstrap
	tx, borrowed := (*sqlTx)(nil), false
	if existing, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && existing.database == s.db {
		if existing.workspace != workspace || existing.reader == nil {
			return ErrMetadataRecordQuery
		}
		tx, borrowed, caller = existing.reader, true, existing.metadata
		if caller.Workspace.ID == "" {
			return ErrAuthForbidden
		}
	} else {
		var err error
		tx, err = s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
		if err != nil {
			return err
		}
		defer tx.Rollback()
	}
	snapshot := context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: s.db, workspace: workspace, reader: tx})
	stamp, epoch, err := s.accessStampAndEpoch(snapshot, workspace)
	if err != nil {
		return err
	}
	data, err := r.issueAuthorizationPolicy(snapshot, caller, borrowed)
	if err != nil {
		return err
	}
	if err := r.trimIssueAuthorizationSettings(snapshot, &data, caller, borrowed); err != nil {
		return err
	}
	if borrowed {
		if len(caller.ViewerTeamIDs) > (metadataWorkBytes-4096)/32 {
			return ErrMetadataWorkBudget
		}
		cost := uint64(len(caller.ViewerTeamIDs))*32 + 4096
		for _, id := range caller.ViewerTeamIDs {
			if uint64(len(id)) > (metadataWorkBytes-cost)/8 {
				return ErrMetadataWorkBudget
			}
			cost += uint64(len(id)) * 8
		}
		if err := r.owner.reserve(snapshot, cost); err != nil {
			return err
		}
		data.ViewerTeamIDs = slices.Clone(caller.ViewerTeamIDs)
		for index, id := range data.ViewerTeamIDs {
			data.ViewerTeamIDs[index] = strings.Clone(id)
		}
	}
	if err := s.validateMetadataLogicalOrigin(snapshot, metadataLogicalOrigin{
		workspace: workspace, stamp: copyStamp{workspaceID: data.Workspace.ID},
	}, stamp.workspaceID); err != nil {
		return err
	}
	if userID == "" {
		// The development accessor bypasses viewer policy, but SQL still hides
		// archived teams using the unfiltered source's archive headers.
		if err := r.owner.reserve(snapshot, uint64(len(data.Teams))*32+4096); err != nil {
			return err
		}
		r.query.Access, r.query.AllowedTeamIDs = nil, nil
		if archived := ArchivedTeamIDs(data); len(archived) != 0 {
			r.query.Access = &IssueRecordAccess{Admin: true, ArchivedTeamIDs: archived}
		}
		viewerField, _, _ := metadataLogicalField("viewer")
		viewer, err := r.owner.node(snapshot, r.owner.source.fields[viewerField], false)
		if err != nil {
			return err
		}
		data.Viewer = viewer.Interface().(domain.User)
		data.ViewerRole = "admin"
		if err := r.loadCommentActionPolicy(snapshot, &data, caller, borrowed); err != nil {
			return err
		}
		r.Data = data
		return snapshot.Err()
	}
	principal := &DocumentRead{owner: r.owner}
	if err := principal.documentProjectionRows(snapshot,
		[]string{"u.id", "u.email", "u.name", "u.display_name", "u.avatar_url", "u.email_verified_at", "u.active", "u.deleted_at", "m.role", "m.status", "t.team_id", "t.role"},
		`FROM workspace_memberships m JOIN auth_users u ON u.id=m.user_id LEFT JOIN team_memberships t ON t.workspace_id=m.workspace_id AND t.user_id=m.user_id WHERE m.workspace_id=? AND m.user_id=?`,
		data.Workspace.ID, userID); err != nil {
		return err
	}
	user, role, members, err := s.resourceAccessPrincipal(snapshot, data.Workspace.ID, userID)
	if err != nil {
		return err
	}
	data.Viewer, data.ViewerRole, data.TeamMembers = user, role, members
	if err := r.loadCommentActionPolicy(snapshot, &data, caller, borrowed); err != nil {
		return err
	}
	// Cover the projection helpers' team maps and ancestry scratch.
	cost := uint64(len(data.Teams))*4096 + uint64(len(data.TeamSettings))*1024 + uint64(len(members))*1024 + 64<<10
	if err := r.owner.reserve(snapshot, cost); err != nil {
		return err
	}
	allowed := make(map[string]bool, len(data.Teams))
	for _, team := range data.Teams {
		if teamVisibleToUser(data, team.ID, userID, role) {
			allowed[team.ID] = true
		}
	}
	filterBootstrapTeams(&data, viewerTeamScope(data, allowed, userID, role), role == "guest")
	for id := range data.TeamSettings {
		if !allowed[id] {
			delete(data.TeamSettings, id)
		}
	}
	data.TeamMembers = slices.DeleteFunc(data.TeamMembers, func(member domain.TeamMember) bool { return !allowed[member.TeamID] })
	// Match filterBootstrapForAPIKey's team projection without replacing the
	// incoming query's independent SQL visibility or credential restrictions.
	if (&resourceAccessPolicy{key: key}).selected() {
		selected := func(id string) bool { return slices.Contains(key.TeamIDs, id) }
		data.Teams = slices.DeleteFunc(data.Teams, func(team domain.Team) bool { return !selected(team.ID) })
		data.TeamMembers = slices.DeleteFunc(data.TeamMembers, func(member domain.TeamMember) bool { return !selected(member.TeamID) })
		for id := range data.TeamSettings {
			if !selected(id) {
				delete(data.TeamSettings, id)
			}
		}
	}
	if err := metadataOutputError(snapshot); err != nil {
		return err
	}
	if !borrowed {
		if err := tx.Rollback(); err != nil {
			return err
		}
		if s.db.journalReady.Load() {
			current, err := s.AuthzEpoch(ctx, stamp.workspaceID)
			if err != nil {
				return err
			}
			if current != epoch {
				return errors.Join(ErrAuthForbidden, ErrResourceAccessChanged)
			}
		}
	}
	r.Data = data
	return ctx.Err()
}

// The shared policy loader synthesizes zero settings for missing teams.
// PagedWorkspaceMetadata preserves saved-key presence, including in dev mode.
func (r *IssueWriteAuthorizationRead) trimIssueAuthorizationSettings(ctx context.Context, data *domain.Bootstrap, caller domain.Bootstrap, borrowed bool) error {
	field, _, _ := metadataLogicalField("teamSettings")
	for id := range data.TeamSettings {
		_, found := caller.TeamSettings[id]
		if !borrowed {
			if err := r.owner.source.fields[field].MapEntries(ctx, func(key, _ metadataLogicalNode) error {
				equal, err := key.StringEqual(ctx, id)
				found = found || equal
				return err
			}); err != nil {
				return err
			}
		}
		if !found {
			delete(data.TeamSettings, id)
		}
	}
	return nil
}

func (r *IssueWriteAuthorizationRead) issueAuthorizationPolicy(ctx context.Context, caller domain.Bootstrap, borrowed bool) (domain.Bootstrap, error) {
	if !borrowed {
		return r.owner.installedResourcePolicy(ctx)
	}
	// Capture only caller-owned policy headers; the enclosing transaction
	// remains responsible for its final live authority revalidation.
	source, err := r.owner.installedResourceCallerSource(ctx, domain.Bootstrap{
		Workspace: caller.Workspace, Teams: caller.Teams, TeamSettings: caller.TeamSettings,
	}, "", "")
	if err != nil {
		return domain.Bootstrap{}, err
	}
	defer source.Release()
	installed := r.owner.source
	r.owner.source = source
	defer func() { r.owner.source = installed }()
	return r.owner.installedResourcePolicy(ctx)
}

func (r *IssueWriteAuthorizationRead) loadCommentActionPolicy(ctx context.Context, data *domain.Bootstrap, caller domain.Bootstrap, borrowed bool) error {
	var settings domain.UserSettings
	var saved bool
	if borrowed {
		data.WorkspaceSettings.HIPAACompliance = caller.WorkspaceSettings.HIPAACompliance
		data.WorkspaceSettings.PreventGuestAgents = caller.WorkspaceSettings.PreventGuestAgents
		for _, flag := range []string{"ai-agent", "ai"} {
			if enabled, found := caller.WorkspaceSettings.FeatureFlags[flag]; found {
				if err := r.owner.reserve(ctx, 4096); err != nil {
					return err
				}
				if data.WorkspaceSettings.FeatureFlags == nil {
					data.WorkspaceSettings.FeatureFlags = map[string]bool{}
				}
				data.WorkspaceSettings.FeatureFlags[flag] = enabled
			}
		}
		if original, found := caller.UserSettings[data.Viewer.ID]; found {
			if uint64(len(original.UserID)) > (metadataWorkBytes-8192)/8 {
				return ErrMetadataWorkBudget
			}
			if err := r.owner.reserve(ctx, uint64(len(original.UserID))*8+8192); err != nil {
				return err
			}
			settings = domain.UserSettings{UserID: strings.Clone(original.UserID), AgentEnabled: original.AgentEnabled}
			if original.SuggestedActionsOnComments != nil {
				enabled := *original.SuggestedActionsOnComments
				settings.SuggestedActionsOnComments = &enabled
			}
			saved = true
		}
	} else {
		index, _, _ := metadataLogicalField("workspaceSettings")
		node := r.owner.source.fields[index]
		value, err := r.owner.projectNode(ctx, node, "HIPAACompliance", "PreventGuestAgents")
		if err != nil {
			return err
		}
		data.WorkspaceSettings = value.Interface().(domain.WorkspaceSettings)
		if err := node.Children(ctx, func(key reflect.Value, flags metadataLogicalNode) error {
			if key.String() != "FeatureFlags" {
				return nil
			}
			return flags.MapEntries(ctx, func(key, value metadataLogicalNode) error {
				for _, flag := range []string{"ai-agent", "ai"} {
					equal, err := key.StringEqual(ctx, flag)
					if err != nil || !equal {
						if err != nil {
							return err
						}
						continue
					}
					if err := r.owner.reserve(ctx, 4096); err != nil {
						return err
					}
					enabled, err := r.owner.node(ctx, value, false)
					if err != nil {
						return err
					}
					if data.WorkspaceSettings.FeatureFlags == nil {
						data.WorkspaceSettings.FeatureFlags = map[string]bool{}
					}
					data.WorkspaceSettings.FeatureFlags[flag] = enabled.Bool()
					break
				}
				return nil
			})
		}); err != nil {
			return err
		}
		index, _, _ = metadataLogicalField("userSettings")
		if err := r.owner.source.fields[index].MapEntries(ctx, func(key, record metadataLogicalNode) error {
			equal, err := key.StringEqual(ctx, data.Viewer.ID)
			if err != nil || !equal {
				return err
			}
			value, err := r.owner.projectNode(ctx, record, "UserID", "AgentEnabled", "SuggestedActionsOnComments")
			if err == nil {
				settings, saved = value.Interface().(domain.UserSettings), true
			}
			return err
		}); err != nil {
			return err
		}
	}
	// An absent settings key differs from a saved zero-valued preference.
	if saved {
		if err := r.owner.reserve(ctx, 4096); err != nil {
			return err
		}
		data.UserSettings = map[string]domain.UserSettings{data.Viewer.ID: settings}
	}
	return nil
}
