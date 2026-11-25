package store

import (
	"context"
	"reflect"
	"time"

	"flow/api/internal/domain"
)

// BeginProjectedInstalledResourcePointRead preserves PagedWorkspaceMetadata's
// installed or caller-snapshot source without canonical actor/default refresh.
// Existing resource policy and projection rules still intersect live authority.
func (s *SQLiteStore) BeginProjectedInstalledResourcePointRead(ctx context.Context, access AccessContext, kind, ref string) (_ *ResourceRead, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, ErrBoundedMetadataCoverage
	}
	if err := access.checkBinding(s); err != nil {
		return nil, err
	}
	if kind != "projects" && kind != "initiatives" {
		return nil, ErrMetadataRecordQuery
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
	p := *access.policy
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == s.db && snapshot.workspace == p.workspace {
		if snapshot.metadata.Workspace.ID == "" {
			return nil, ErrAuthForbidden
		}
		read.source, err = read.installedResourceCallerSource(ctx, snapshot.metadata, kind, ref)
	} else {
		var installed *InstalledMetadataRead
		var found bool
		installed, found, err = s.BeginInstalledMetadataRead(ctx, p.workspace)
		if err == nil && !found {
			err = ErrAuthForbidden
		}
		if err == nil {
			read.source, err = installed.retainSource(ctx, installed.workspace)
			installed.Close()
		}
	}
	if err != nil {
		return nil, err
	}
	if err := s.validateMetadataLogicalOrigin(ctx, metadataLogicalOrigin{workspace: p.workspace, stamp: read.source.Stamp()}, access.WorkspaceID); err != nil {
		return nil, err
	}
	native, err := read.installedResourcePolicy(ctx)
	if err != nil {
		return nil, err
	}
	native.Viewer, native.ViewerRole, native.TeamMembers = p.metadata.Viewer, p.metadata.ViewerRole, p.metadata.TeamMembers
	p.metadata = native
	allowed := map[string]bool{}
	for _, team := range native.Teams {
		if teamVisibleToUser(native, team.ID, p.principal, native.ViewerRole) {
			allowed[team.ID] = true
		}
	}
	p.teams = viewerTeamScope(native, allowed, p.principal, native.ViewerRole)
	data, err := read.installed(ctx, &p, kind, ref)
	if err == nil {
		read.Data, read.Allowed, err = resourceReadMetadata(&p, data, kind, ref)
	}
	if err == nil && len(read.Data.Initiatives) == 1 && !read.Data.Initiatives[0].Favorite {
		var favorite int
		workspace := native.Workspace.URLKey
		err = s.db.QueryRowContext(ctx, `SELECT CASE WHEN EXISTS (
			SELECT 1 FROM workspace_metadata_records WHERE workspace_key=? AND field='favorites' AND `+s.jsonText("data", "userId")+`=? AND `+s.jsonText("data", "resourceType")+`='initiative' AND `+s.jsonText("data", "resourceId")+`=?
		) OR EXISTS (
			SELECT 1 FROM team_default_favorites d JOIN team_memberships tm ON tm.team_id=d.team_id AND tm.workspace_id=? AND tm.user_id=?
			WHERE d.workspace_key=? AND d.resource_type='initiative' AND d.resource_id=? AND NOT EXISTS (
			SELECT 1 FROM team_default_favorite_overrides o WHERE o.workspace_key=d.workspace_key AND o.user_id=tm.user_id AND o.resource_type=d.resource_type AND o.resource_id=d.resource_id AND o.hidden=1)
		) THEN 1 ELSE 0 END`, workspace, access.PrincipalID, read.Data.Initiatives[0].ID, access.WorkspaceID, access.PrincipalID, workspace, read.Data.Initiatives[0].ID).Scan(&favorite)
		read.Data.Initiatives[0].Favorite = favorite == 1
	}
	if err == nil {
		err = s.ValidateAccess(ctx, access)
	}
	if err != nil {
		return nil, err
	}
	return read, ctx.Err()
}

func (r *ResourceRead) installedResourcePolicy(ctx context.Context) (domain.Bootstrap, error) {
	data := domain.Bootstrap{TeamSettings: map[string]domain.TeamSettings{}}
	workspaceField, _, _ := metadataLogicalField("workspace")
	workspace, err := r.projectNode(ctx, r.source.fields[workspaceField], "ID", "URLKey")
	if err != nil {
		return data, err
	}
	data.Workspace = workspace.Interface().(domain.Workspace)
	err = r.visitNative(ctx, "teams", func(_ int, record metadataLogicalNode) error {
		value, err := r.projectNode(ctx, record, "ID", "Key", "ExternalSource", "Private")
		if err != nil {
			return err
		}
		team := value.Interface().(domain.Team)
		if err := record.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
			if key.String() != "ArchivedAt" && key.String() != "RetiredAt" {
				return nil
			}
			return node.Children(ctx, func(_ reflect.Value, _ metadataLogicalNode) error {
				reflect.ValueOf(&team).Elem().FieldByName(key.String()).Set(reflect.ValueOf(new(time.Time)))
				return nil
			})
		}); err != nil {
			return err
		}
		data.Teams = append(data.Teams, team)
		return nil
	})
	if err != nil {
		return data, err
	}
	settingsField, _, _ := metadataLogicalField("teamSettings")
	var load func(string) error
	load = func(id string) error {
		if _, found := data.TeamSettings[id]; found {
			return nil
		}
		if err := r.reserve(ctx, uint64(len(id))*8+4096); err != nil {
			return err
		}
		data.TeamSettings[id] = domain.TeamSettings{}
		policy := domain.TeamSettings{}
		if err := r.source.fields[settingsField].MapEntries(ctx, func(key, record metadataLogicalNode) error {
			equal, err := key.StringEqual(ctx, id)
			if err != nil || !equal {
				return err
			}
			value, err := r.projectNode(ctx, record, "ParentTeamID", "Access", "MembershipRestriction")
			if err == nil {
				policy = value.Interface().(domain.TeamSettings)
			}
			return err
		}); err != nil {
			return err
		}
		data.TeamSettings[id] = policy
		if policy.ParentTeamID == "" {
			return nil
		}
		return load(policy.ParentTeamID)
	}
	for _, team := range data.Teams {
		if err := load(team.ID); err != nil {
			return data, err
		}
	}
	return data, nil
}

// Caller snapshots already own their complete graph. Frame matching records
// and reference/policy headers only, preserving their order without copying
// unrelated bodies into a new logical source.
func (r *ResourceRead) installedResourceCallerSource(ctx context.Context, data domain.Bootstrap, kind, ref string) (*metadataLogicalSource, error) {
	candidate := domain.Bootstrap{Workspace: domain.Workspace{ID: data.Workspace.ID, URLKey: data.Workspace.URLKey}, TeamSettings: map[string]domain.TeamSettings{}}
	for _, team := range data.Teams {
		if err := r.reserve(ctx, uint64(reflect.TypeFor[domain.Team]().Size())*2+2048); err != nil {
			return nil, err
		}
		candidate.Teams = append(candidate.Teams, domain.Team{ID: team.ID, Key: team.Key, ExternalSource: team.ExternalSource, Private: team.Private, ArchivedAt: team.ArchivedAt, RetiredAt: team.RetiredAt})
	}
	var load func(string) error
	load = func(id string) error {
		if _, found := candidate.TeamSettings[id]; found {
			return nil
		}
		if err := r.reserve(ctx, uint64(len(id))*8+4096); err != nil {
			return err
		}
		policy := data.TeamSettings[id]
		candidate.TeamSettings[id] = domain.TeamSettings{ParentTeamID: policy.ParentTeamID, Access: policy.Access, MembershipRestriction: policy.MembershipRestriction}
		if policy.ParentTeamID == "" {
			return nil
		}
		return load(policy.ParentTeamID)
	}
	for _, team := range candidate.Teams {
		if err := load(team.ID); err != nil {
			return nil, err
		}
	}
	for _, item := range data.Projects {
		if err := r.reserve(ctx, uint64(reflect.TypeFor[domain.Project]().Size())*2+2048); err != nil {
			return nil, err
		}
		project := domain.Project{ID: item.ID, TeamIDs: item.TeamIDs}
		if kind == "projects" && domain.MatchSlugRef(ref, item.ID, item.SlugID, item.PreviousSlugIDs) != domain.SlugNoMatch {
			project = item
		}
		candidate.Projects = append(candidate.Projects, project)
	}
	for _, item := range data.Initiatives {
		if err := r.reserve(ctx, uint64(reflect.TypeFor[domain.Initiative]().Size())*2+2048); err != nil {
			return nil, err
		}
		initiative := domain.Initiative{ID: item.ID, LeadTeamID: item.LeadTeamID, ContributingTeamIDs: item.ContributingTeamIDs, ProjectIDs: item.ProjectIDs}
		if kind == "initiatives" && domain.MatchSlugRef(ref, item.ID, item.SlugID, item.PreviousSlugIDs) != domain.SlugNoMatch {
			initiative = item
		}
		candidate.Initiatives = append(candidate.Initiatives, initiative)
	}
	for _, relation := range data.InitiativeRelations {
		if err := r.reserve(ctx, uint64(reflect.TypeFor[domain.InitiativeRelation]().Size())*2+2048); err != nil {
			return nil, err
		}
		candidate.InitiativeRelations = append(candidate.InitiativeRelations, relation)
	}
	for _, favorite := range data.Favorites {
		if err := r.reserve(ctx, uint64(reflect.TypeFor[domain.Favorite]().Size())*2+2048); err != nil {
			return nil, err
		}
		candidate.Favorites = append(candidate.Favorites, domain.Favorite{UserID: favorite.UserID, ResourceType: favorite.ResourceType, ResourceID: favorite.ResourceID})
	}
	for _, label := range data.Labels {
		if err := r.reserve(ctx, uint64(reflect.TypeFor[domain.IssueLabel]().Size())*2+2048); err != nil {
			return nil, err
		}
		candidate.Labels = append(candidate.Labels, domain.IssueLabel{ID: label.ID, Scope: label.Scope})
	}
	return captureMetadataLogicalSource(ctx, candidate, copyStamp{workspaceID: data.Workspace.ID, revision: -1})
}

// BeginInstalledResourcePointRead returns one unfiltered installed project or
// initiative. The bool distinguishes a missing workspace from a missing record.
// This is trusted storage, not authorization. Keep the read open through encoding.
func (s *SQLiteStore) BeginInstalledResourcePointRead(ctx context.Context, workspace, kind, ref string) (_ *ResourceRead, found bool, err error) {
	if kind != "projects" && kind != "initiatives" {
		return nil, false, ErrMetadataRecordQuery
	}
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
	var selected metadataLogicalNode
	rank := domain.SlugNoMatch
	err = read.visitNative(ctx, kind, func(_ int, record metadataLogicalNode) error {
		next, err := read.nativeRank(ctx, record, ref)
		if err != nil {
			return err
		}
		if next > rank {
			selected, rank = record, next
		}
		if rank == domain.SlugMatchExact {
			return ErrMetadataIterationDone
		}
		return nil
	})
	if err != nil {
		return nil, true, err
	}
	if rank != domain.SlugNoMatch {
		value, err := read.node(ctx, selected, true)
		if err != nil {
			return nil, true, err
		}
		switch kind {
		case "projects":
			read.Data.Projects = []domain.Project{value.Interface().(domain.Project)}
		case "initiatives":
			read.Data.Initiatives = []domain.Initiative{value.Interface().(domain.Initiative)}
		}
		read.Allowed = true
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	return read, true, nil
}
