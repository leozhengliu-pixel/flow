package store

import (
	"context"
	"reflect"
	"slices"
	"sort"

	"flow/api/internal/domain"
)

// BeginTeamResourceAuthorizationRead owns the literal team-content decision
// and, when requested, the selected team's complete resource output. Ordinary
// reads use the installed native generation, including unknown revisions;
// caller workspace snapshots retain their original source precedence.
// Data.Teams carries only the canonical ID used by the existing handler.
// Resource values preserve JSON, not private time.Location identity, and must
// never become mutation preimages.
func (s *SQLiteStore) BeginTeamResourceAuthorizationRead(ctx context.Context, workspace, userID, ref string, output bool, key *domain.APIKey) (_ *ResourceRead, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	read := &ResourceRead{store: s}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	if err := read.reserve(ctx, 2<<20); err != nil {
		return nil, err
	}
	installed, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrAuthForbidden
	}
	selectedWorkspace := installed.workspace
	read.source, err = installed.retainSource(ctx, selectedWorkspace)
	installed.Close()
	if err != nil {
		return nil, err
	}
	access, err := s.ResolveAccess(ctx, selectedWorkspace, userID, key)
	if err != nil {
		return nil, err
	}
	// Keep the literal independent installed-directory lookup, including its
	// default-workspace choice and exact-ID-before-key precedence.
	teamID := s.ResolveTeamID(workspace, ref)
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == s.db &&
		(workspace == "" || workspace == snapshot.workspace) {
		source, err := read.sw307CallerSource(ctx, snapshot.metadata, teamID, output)
		if err != nil {
			return nil, err
		}
		read.source.Release()
		read.source = source
	}
	policy, err := read.installedResourcePolicy(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.validateMetadataLogicalOrigin(ctx, metadataLogicalOrigin{workspace: selectedWorkspace, stamp: read.source.Stamp()}, access.WorkspaceID); err != nil {
		return nil, err
	}
	policy.Viewer, policy.ViewerRole = access.policy.metadata.Viewer, access.policy.metadata.ViewerRole
	members := access.policy.metadata.TeamMembers
	memberSize := uint64(reflect.TypeFor[domain.TeamMember]().Size())
	if uint64(len(members)) > metadataWorkBytes/(memberSize*2) {
		return nil, ErrMetadataWorkBudget
	}
	if err := read.reserve(ctx, uint64(len(members))*memberSize*2+2048); err != nil {
		return nil, err
	}
	policy.TeamMembers = slices.Clone(members)
	visible := map[string]bool{}
	for _, team := range policy.Teams {
		if teamVisibleToUser(policy, team.ID, userID, policy.ViewerRole) {
			visible[team.ID] = true
		}
	}
	// Replay the policy portions of projectBootstrapForUser before the
	// server's later teamContentVisible check, including filtered ancestors.
	policy.Teams = slices.DeleteFunc(policy.Teams, func(team domain.Team) bool { return !visible[team.ID] })
	for id := range policy.TeamSettings {
		if !visible[id] {
			delete(policy.TeamSettings, id)
		}
	}
	policy.TeamMembers = slices.DeleteFunc(policy.TeamMembers, func(member domain.TeamMember) bool { return !visible[member.TeamID] })
	read.Data.Teams = []domain.Team{{ID: teamID}}
	read.Allowed = visible[teamID] && (!access.policy.selected() || slices.Contains(access.policy.key.TeamIDs, teamID))
	if read.Allowed && isWorkspaceAdminRole(policy.ViewerRole) {
		read.Allowed = TeamContentVisible(policy, teamID, userID, policy.ViewerRole)
	}
	if read.Allowed && output {
		read.Data.TeamResourceSections = []domain.TeamResourceSection{}
		read.Data.TeamPinnedResources = []domain.TeamPinnedResource{}
		for _, field := range []string{"teamResourceSections", "teamPinnedResources"} {
			err = read.visitNative(ctx, field, func(_ int, record metadataLogicalNode) error {
				node, err := metadataAggregateChild(ctx, metadataAggregateStoredNode{record}, "TeamID")
				if err != nil {
					return err
				}
				if node == nil {
					return ErrBoundedMetadataCoverage
				}
				match, err := node.(metadataAggregateStoredNode).value.StringEqual(ctx, teamID)
				if err != nil || !match {
					return err
				}
				// node owns the existing total processing/encoding allowance
				// for every retained value, not merely the largest row. These
				// rows are only sorted by Position and encoded as JSON.
				value, err := read.node(ctx, record, true)
				if err != nil {
					return err
				}
				if field == "teamResourceSections" {
					read.Data.TeamResourceSections = append(read.Data.TeamResourceSections, value.Interface().(domain.TeamResourceSection))
				} else {
					read.Data.TeamPinnedResources = append(read.Data.TeamPinnedResources, value.Interface().(domain.TeamPinnedResource))
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
		sort.SliceStable(read.Data.TeamResourceSections, func(i, j int) bool {
			return read.Data.TeamResourceSections[i].Position < read.Data.TeamResourceSections[j].Position
		})
		sort.SliceStable(read.Data.TeamPinnedResources, func(i, j int) bool {
			return read.Data.TeamPinnedResources[i].Position < read.Data.TeamPinnedResources[j].Position
		})
	}
	if err := s.ValidateAccess(ctx, access); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return read, nil
}

// A caller snapshot already owns its strings and timestamps. Frame only the
// policy headers and matching output, retaining inline order and duplicates.
func (r *ResourceRead) sw307CallerSource(ctx context.Context, data domain.Bootstrap, teamID string, output bool) (*metadataLogicalSource, error) {
	candidate := domain.Bootstrap{
		Workspace:    domain.Workspace{ID: data.Workspace.ID, URLKey: data.Workspace.URLKey},
		TeamSettings: map[string]domain.TeamSettings{},
	}
	for _, team := range data.Teams {
		if err := r.reserve(ctx, uint64(reflect.TypeFor[domain.Team]().Size())*2+2048); err != nil {
			return nil, err
		}
		candidate.Teams = append(candidate.Teams, domain.Team{
			ID: team.ID, Key: team.Key, ExternalSource: team.ExternalSource, Private: team.Private,
			ArchivedAt: team.ArchivedAt, RetiredAt: team.RetiredAt,
		})
	}
	var settings func(string) error
	settings = func(id string) error {
		if _, seen := candidate.TeamSettings[id]; seen {
			return nil
		}
		if err := r.reserve(ctx, uint64(len(id))*8+4096); err != nil {
			return err
		}
		value := data.TeamSettings[id]
		candidate.TeamSettings[id] = domain.TeamSettings{
			ParentTeamID: value.ParentTeamID, Access: value.Access, MembershipRestriction: value.MembershipRestriction,
		}
		if value.ParentTeamID != "" {
			return settings(value.ParentTeamID)
		}
		return nil
	}
	for _, team := range candidate.Teams {
		if err := settings(team.ID); err != nil {
			return nil, err
		}
	}
	if output {
		for _, section := range data.TeamResourceSections {
			if section.TeamID == teamID {
				if err := r.reserve(ctx, uint64(reflect.TypeFor[domain.TeamResourceSection]().Size())*2+2048); err != nil {
					return nil, err
				}
				candidate.TeamResourceSections = append(candidate.TeamResourceSections, section)
			}
		}
		for _, resource := range data.TeamPinnedResources {
			if resource.TeamID == teamID {
				if err := r.reserve(ctx, uint64(reflect.TypeFor[domain.TeamPinnedResource]().Size())*2+2048); err != nil {
					return nil, err
				}
				candidate.TeamPinnedResources = append(candidate.TeamPinnedResources, resource)
			}
		}
	}
	return captureMetadataLogicalSource(ctx, candidate, copyStamp{workspaceID: data.Workspace.ID, revision: -1})
}
