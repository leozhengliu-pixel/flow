package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

var ErrResourceAccessChanged = errors.New("resource access metadata changed during read")

// AccessContext owns an immutable policy. Exported identity fields are also
// checked against that policy, so copying or editing them cannot widen access.
type AccessContext struct {
	WorkspaceID, PrincipalID, CredentialScope, AuthzEpoch string
	policy                                                *resourceAccessPolicy
}

type resourceAccessPolicy struct {
	store                       *SQLiteStore
	workspace, principal, scope string
	epoch                       string
	stamp                       copyStamp
	metadata                    domain.Bootstrap
	teams                       bootstrapTeamScope
	settingsTeams               map[string]bool
	key                         *domain.APIKey
}

// ResolveAccess reads current membership on every call. Until the shared
// authorization journal supplies a workspace epoch, AuthzEpoch is a digest of
// the authoritative metadata revision and this principal's live policy. It is
// opaque, scoped to this principal/credential, and is never a replay cursor.
func (s *SQLiteStore) ResolveAccess(ctx context.Context, workspace, principal string, key *domain.APIKey) (AccessContext, error) {
	if workspace == "" || principal == "" {
		return AccessContext{}, ErrAuthForbidden
	}
	stamp, err := readMetadataStamp(ctx, s.db, workspace)
	if err != nil {
		return AccessContext{}, err
	}
	s.mu.RLock()
	data, ok := s.workspaces[workspace]
	known, exact := s.copyRevisionLocked(workspace)
	current := ok && exact && known.workspaceID == stamp.workspaceID && known.revision == stamp.revision
	if current {
		data = resourcePolicyMetadata(data)
	}
	s.mu.RUnlock()
	if shared, ok := eventPolicySourceFor(ctx, s, workspace, stamp); ok {
		data, current = shared, true
	}
	if !current {
		data, err = s.resourcePolicyRecords(ctx, workspace, stamp.workspaceID)
		if err != nil {
			return AccessContext{}, err
		}
	}
	user, role, members, err := s.resourceAccessPrincipal(ctx, data.Workspace.ID, principal)
	if err != nil {
		return AccessContext{}, err
	}
	data.Viewer, data.ViewerRole = user, role
	data.TeamMembers = members
	policy := &resourceAccessPolicy{store: s, workspace: workspace, principal: principal, stamp: stamp, metadata: data}
	policy.key = resourceAccessCredential(key)
	raw, err := json.Marshal(policy.key)
	if err != nil {
		return AccessContext{}, err
	}
	policy.scope = fmt.Sprintf("%x", sha256.Sum256(raw))
	allowed := map[string]bool{}
	for _, team := range data.Teams {
		if teamVisibleToUser(data, team.ID, principal, role) {
			allowed[team.ID] = true
		}
	}
	policy.teams = viewerTeamScope(data, allowed, principal, role)
	policy.settingsTeams = resourceSettingsTeams(data)
	raw, err = json.Marshal(struct {
		Workspace, Principal, Scope string
		Revision                    int64
		Policy                      domain.Bootstrap
	}{data.Workspace.ID, principal, policy.scope, stamp.revision, data})
	if err != nil {
		return AccessContext{}, err
	}
	policy.epoch = fmt.Sprintf("p03:%x", sha256.Sum256(raw))
	return AccessContext{data.Workspace.ID, principal, policy.scope, policy.epoch, policy}, nil
}

// One statement observes the principal, workspace role and explicit team
// grants together. Close its rows before decorating an application user.
func (s *SQLiteStore) resourceAccessPrincipal(ctx context.Context, workspaceID, principal string) (user domain.User, role string, members []domain.TeamMember, err error) {
	rows, err := s.db.QueryContext(ctx, `SELECT u.id,u.email,u.name,u.display_name,u.avatar_url,u.email_verified_at,u.active,u.deleted_at,m.role,m.status,t.team_id,t.role
		FROM workspace_memberships m JOIN auth_users u ON u.id=m.user_id
		LEFT JOIN team_memberships t ON t.workspace_id=m.workspace_id AND t.user_id=m.user_id
		WHERE m.workspace_id=? AND m.user_id=? ORDER BY t.team_id`, workspaceID, principal)
	if err != nil {
		return user, role, members, err
	}
	for rows.Next() {
		var email, verified, deleted, teamID, teamRole sql.NullString
		var active int
		var status string
		if err := rows.Scan(&user.ID, &email, &user.Name, &user.DisplayName, &user.AvatarURL, &verified, &active, &deleted, &role, &status, &teamID, &teamRole); err != nil {
			rows.Close()
			return user, role, members, err
		}
		if status != "active" {
			rows.Close()
			return user, role, members, ErrAuthForbidden
		}
		user.Email, user.Active, user.EmailVerified, user.Deleted = email.String, active == 1, verified.Valid, deleted.Valid
		if teamID.Valid {
			members = append(members, domain.TeamMember{UserID: principal, TeamID: teamID.String, Role: teamRole.String})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return user, role, members, err
	}
	if user.ID == "" {
		return user, role, members, ErrAuthForbidden
	}
	err = s.decorateAppUser(ctx, &user)
	return user, role, members, err
}

func resourceAccessCredential(key *domain.APIKey) *domain.APIKey {
	if key == nil {
		return nil
	}
	// Authentication already checked the live credential. Retain only its
	// effective scope, never its secret or usage telemetry.
	return &domain.APIKey{ID: key.ID, Actor: key.Actor, Scopes: slices.Clone(key.Scopes), TeamIDs: slices.Clone(key.TeamIDs), TeamRestriction: key.TeamRestriction}
}

func (access AccessContext) MatchesCredential(key *domain.APIKey) bool {
	return access.policy != nil && access.CredentialScope == access.policy.scope && reflect.DeepEqual(access.policy.key, resourceAccessCredential(key))
}

func resourceSettingsTeams(data domain.Bootstrap) map[string]bool {
	roles := map[string]string{}
	for _, member := range data.TeamMembers {
		roles[member.TeamID] = member.Role
	}
	allowed := map[string]bool{}
	for _, team := range data.Teams {
		if isWorkspaceAdminRole(data.ViewerRole) {
			allowed[team.ID] = true
			continue
		}
		if data.ViewerRole == "guest" {
			continue
		}
		role := roles[team.ID]
		seen := map[string]bool{team.ID: true}
		for parent := data.TeamSettings[team.ID].ParentTeamID; parent != "" && !seen[parent] && role != "owner"; parent = data.TeamSettings[parent].ParentTeamID {
			seen[parent] = true
			if roles[parent] == "owner" {
				role = "owner"
			}
		}
		permission := data.TeamSettings[team.ID].SettingsPermission
		if role == "owner" || role == "member" && (permission == "teamMembers" || permission == "allMembers") {
			allowed[team.ID] = true
		}
	}
	return allowed
}

func resourcePolicyMetadata(data domain.Bootstrap) domain.Bootstrap {
	result := domain.Bootstrap{Workspace: domain.Workspace{ID: data.Workspace.ID, URLKey: data.Workspace.URLKey}, TeamSettings: map[string]domain.TeamSettings{}}
	for _, team := range data.Teams {
		result.Teams = append(result.Teams, domain.Team{ID: team.ID, Key: team.Key, ExternalSource: team.ExternalSource, Private: team.Private, ArchivedAt: team.ArchivedAt, RetiredAt: team.RetiredAt})
	}
	for id, settings := range data.TeamSettings {
		result.TeamSettings[id] = domain.TeamSettings{ParentTeamID: settings.ParentTeamID, Access: settings.Access, MembershipRestriction: settings.MembershipRestriction, SettingsPermission: settings.SettingsPermission, LabelPermission: settings.LabelPermission, MemberPermission: settings.MemberPermission}
	}
	return cloneBootstrap(result)
}

// ValidateAccess rejects policy objects retained across a permission change.
// Callers sending queued or replayed batches must resolve/validate per batch.
func (s *SQLiteStore) ValidateAccess(ctx context.Context, access AccessContext) error {
	if err := access.checkBinding(s); err != nil {
		return err
	}
	p := access.policy
	current, err := s.ResolveAccess(ctx, p.workspace, p.principal, p.key)
	if err != nil {
		return err
	}
	if current.AuthzEpoch != access.AuthzEpoch {
		return ErrAuthForbidden
	}
	return nil
}

func (access AccessContext) checkBinding(s *SQLiteStore) error {
	p := access.policy
	if p == nil || p.store != s || access.WorkspaceID != p.metadata.Workspace.ID || access.PrincipalID != p.principal || access.CredentialScope != p.scope || access.AuthzEpoch != p.epoch {
		return ErrAuthForbidden
	}
	return nil
}

func (p *resourceAccessPolicy) teamAllowed(id string) bool {
	return p.teams.Content[id] && (!p.selected() || slices.Contains(p.key.TeamIDs, id))
}

func (p *resourceAccessPolicy) selected() bool {
	return p.key != nil && (p.key.TeamRestriction == "selected" || p.key.TeamRestriction == "" && len(p.key.TeamIDs) > 0)
}

func (p *resourceAccessPolicy) projectVisible(project domain.Project) bool {
	return len(project.TeamIDs) == 0 || slices.ContainsFunc(project.TeamIDs, func(id string) bool { return p.teams.Content[id] })
}

func (p *resourceAccessPolicy) projectAllowed(project domain.Project) bool {
	return p.projectVisible(project) && (len(project.TeamIDs) == 0 || slices.ContainsFunc(project.TeamIDs, p.teamAllowed))
}

func (p *resourceAccessPolicy) projectByID(data domain.Bootstrap, id string, credential bool) bool {
	return slices.ContainsFunc(data.Projects, func(project domain.Project) bool {
		return project.ID == id && p.projectVisible(project) && (!credential || p.projectAllowed(project) && (!p.selected() || slices.ContainsFunc(project.TeamIDs, p.teamAllowed)))
	})
}

func (p *resourceAccessPolicy) initiativeVisible(data domain.Bootstrap, item domain.Initiative) bool {
	if p.metadata.ViewerRole == "guest" || item.LeadTeamID != "" && !p.teams.Content[item.LeadTeamID] {
		return false
	}
	return item.LeadTeamID != "" || len(item.ContributingTeamIDs) == 0 && len(item.ProjectIDs) == 0 ||
		slices.ContainsFunc(item.ContributingTeamIDs, func(id string) bool { return p.teams.Content[id] }) ||
		slices.ContainsFunc(item.ProjectIDs, func(id string) bool { return p.projectByID(data, id, false) })
}

func (p *resourceAccessPolicy) initiativeAllowed(data domain.Bootstrap, item domain.Initiative) bool {
	if item.LeadTeamID != "" {
		return p.teamAllowed(item.LeadTeamID)
	}
	contributing := slices.ContainsFunc(item.ContributingTeamIDs, func(id string) bool { return p.teams.Content[id] })
	projects := slices.ContainsFunc(item.ProjectIDs, func(id string) bool { return p.projectByID(data, id, false) })
	if !contributing && !projects {
		return p.metadata.ViewerRole != "guest"
	}
	return slices.ContainsFunc(item.ContributingTeamIDs, p.teamAllowed) || slices.ContainsFunc(item.ProjectIDs, func(id string) bool {
		return slices.ContainsFunc(data.Projects, func(project domain.Project) bool { return project.ID == id && p.projectAllowed(project) })
	})
}

func (p *resourceAccessPolicy) initiativeReadable(data domain.Bootstrap, item domain.Initiative) bool {
	if !p.initiativeVisible(data, item) || !p.initiativeAllowed(data, item) {
		return false
	}
	if p.key != nil && p.key.Actor == "app" && !resourceKeyHasScope(*p.key, "initiative:read") && !resourceKeyHasScope(*p.key, "initiative:write") {
		return false
	}
	return !p.selected() || slices.ContainsFunc(item.ProjectIDs, func(id string) bool { return p.projectByID(data, id, true) })
}

func (p *resourceAccessPolicy) labelAllowed(data domain.Bootstrap, id string) bool {
	return slices.ContainsFunc(data.Labels, func(label domain.IssueLabel) bool {
		scope := strings.ToLower(strings.TrimSpace(label.Scope))
		return label.ID == id && (scope == "" || scope == "workspace" || p.teamAllowed(label.Scope))
	})
}

func resourceSlugIndex[T any](items []T, ref string, key func(T) (string, string, []string), visible func(T) bool) int {
	best, rank := -1, domain.SlugNoMatch
	for index, item := range items {
		id, slug, previous := key(item)
		match := domain.MatchSlugRef(ref, id, slug, previous)
		if match > rank && visible(item) {
			best, rank = index, match
			if match == domain.SlugMatchExact {
				break
			}
		}
	}
	return best
}

// ResourceReadMetadata returns only the requested detail. allowed is the
// legacy middleware decision; an empty collection after allowed=true is the
// handler's 404 (application scopes and selected-team keys can narrow it).
// Access must be resolved for this request; retained contexts require an
// explicit ValidateAccess before reading. The trailing stamp protects records.
func (s *SQLiteStore) ResourceReadMetadata(ctx context.Context, access AccessContext, kind, ref string) (data domain.Bootstrap, allowed bool, err error) {
	if err = access.checkBinding(s); err != nil {
		return data, false, err
	}
	p := access.policy
	s.mu.RLock()
	source, ok := s.workspaces[p.workspace]
	known, exact := s.copyRevisionLocked(p.workspace)
	current := ok && exact && known.workspaceID == p.stamp.workspaceID && known.revision == p.stamp.revision
	if current {
		data, allowed, err = resourceReadMetadata(p, source, kind, ref)
	}
	s.mu.RUnlock()
	if !current {
		source, err = s.resourceReadRecords(ctx, p, kind, ref)
		if err == nil {
			data, allowed, err = resourceReadMetadata(p, source, kind, ref)
		}
	}
	if err == nil && len(data.Initiatives) == 1 && !data.Initiatives[0].Favorite {
		// Defaults are live SQL policy, independent of the metadata revision.
		var favorite int
		err = s.db.QueryRowContext(ctx, `SELECT CASE WHEN EXISTS (
			SELECT 1 FROM workspace_metadata_records WHERE workspace_key=? AND field='favorites' AND `+s.jsonText("data", "userId")+`=? AND `+s.jsonText("data", "resourceType")+`='initiative' AND `+s.jsonText("data", "resourceId")+`=?
		) OR EXISTS (
			SELECT 1 FROM team_default_favorites d JOIN team_memberships tm ON tm.team_id=d.team_id AND tm.workspace_id=? AND tm.user_id=?
			WHERE d.workspace_key=? AND d.resource_type='initiative' AND d.resource_id=? AND NOT EXISTS (
			SELECT 1 FROM team_default_favorite_overrides o WHERE o.workspace_key=d.workspace_key AND o.user_id=tm.user_id AND o.resource_type=d.resource_type AND o.resource_id=d.resource_id AND o.hidden=1)
		) THEN 1 ELSE 0 END`, p.workspace, access.PrincipalID, data.Initiatives[0].ID, access.WorkspaceID, access.PrincipalID, p.workspace, data.Initiatives[0].ID).Scan(&favorite)
		data.Initiatives[0].Favorite = favorite == 1
	}
	if err == nil {
		stamp, stampErr := readMetadataStamp(ctx, s.db, p.workspace)
		if stampErr != nil {
			return domain.Bootstrap{}, false, stampErr
		}
		if stamp != p.stamp {
			return domain.Bootstrap{}, false, errors.Join(ErrAuthForbidden, ErrResourceAccessChanged)
		}
	}
	return data, allowed, err
}

func resourceReadMetadata(p *resourceAccessPolicy, source domain.Bootstrap, kind, ref string) (data domain.Bootstrap, allowed bool, err error) {
	switch kind {
	case "projects":
		index := resourceSlugIndex(source.Projects, ref, domain.ProjectSlugKey, p.projectVisible)
		if index < 0 || !p.projectAllowed(source.Projects[index]) {
			return data, false, nil
		}
		if p.selected() {
			index = resourceSlugIndex(source.Projects, ref, domain.ProjectSlugKey, func(project domain.Project) bool {
				return p.projectVisible(project) && slices.ContainsFunc(project.TeamIDs, p.teamAllowed)
			})
			if index < 0 {
				return data, true, nil
			}
		}
		project := cloneSnapshotValue(reflect.ValueOf(source.Projects[index])).Interface().(domain.Project)
		project.TeamIDs = slices.DeleteFunc(project.TeamIDs, func(id string) bool { return !p.teams.Content[id] })
		project.DependencyIDs = slices.DeleteFunc(project.DependencyIDs, func(id string) bool { return !p.projectByID(source, id, true) })
		if p.selected() || p.key != nil && p.key.Actor == "app" {
			project.Initiatives = slices.DeleteFunc(project.Initiatives, func(id string) bool {
				return !slices.ContainsFunc(source.Initiatives, func(item domain.Initiative) bool {
					return item.ID == id && p.initiativeReadable(source, item)
				})
			})
		}
		if p.key != nil && p.key.Actor == "app" && !resourceKeyHasScope(*p.key, "customer:read") && !resourceKeyHasScope(*p.key, "customer:write") {
			project.Customers = slices.DeleteFunc(project.Customers, func(string) bool { return true })
		}
		// PagedWorkspaceMetadata deliberately contains no issues, so its
		// final refreshResourceCounts sets this count to zero as well.
		project.IssueCount = 0
		if p.selected() {
			if slices.ContainsFunc(project.TeamIDs, func(id string) bool { return !p.teamAllowed(id) }) {
				RedactProjectProgress(&project)
			}
			project.TeamIDs = slices.DeleteFunc(project.TeamIDs, func(id string) bool { return !p.teamAllowed(id) })
			project.LabelIDs = slices.DeleteFunc(project.LabelIDs, func(id string) bool { return !p.labelAllowed(source, id) })
			for i := range project.Resources {
				project.Resources[i].PinnedTeamIDs = slices.DeleteFunc(project.Resources[i].PinnedTeamIDs, func(id string) bool { return !p.teamAllowed(id) })
			}
			for _, user := range []*domain.User{project.Creator, project.Lead} {
				if user != nil {
					RedactUserTeams(user, p.teamAllowed)
				}
			}
			for i := range project.Comments {
				RedactUserTeams(&project.Comments[i].User, p.teamAllowed)
			}
			for i := range project.DescriptionRevisions {
				RedactUserTeams(&project.DescriptionRevisions[i].Author, p.teamAllowed)
			}
		}
		data.Projects = []domain.Project{project}
	case "initiatives":
		index := resourceSlugIndex(source.Initiatives, ref, domain.InitiativeSlugKey, func(item domain.Initiative) bool { return p.initiativeVisible(source, item) })
		if index < 0 {
			return data, false, nil
		}
		item := source.Initiatives[index]
		if !p.initiativeAllowed(source, item) {
			return data, false, nil
		}
		if p.key != nil && p.key.Actor == "app" && !resourceKeyHasScope(*p.key, "initiative:read") && !resourceKeyHasScope(*p.key, "initiative:write") {
			return data, true, nil
		}
		if p.selected() {
			index = resourceSlugIndex(source.Initiatives, ref, domain.InitiativeSlugKey, func(candidate domain.Initiative) bool {
				return p.initiativeVisible(source, candidate) && slices.ContainsFunc(candidate.ProjectIDs, func(id string) bool { return p.projectByID(source, id, true) })
			})
			if index < 0 {
				return data, true, nil
			}
			item = source.Initiatives[index]
		}
		item = cloneSnapshotValue(reflect.ValueOf(item)).Interface().(domain.Initiative)
		item.ProjectIDs = slices.DeleteFunc(item.ProjectIDs, func(id string) bool { return !p.projectByID(source, id, true) })
		item.ContributingTeamIDs = slices.DeleteFunc(item.ContributingTeamIDs, func(id string) bool { return !p.teamAllowed(id) })
		if p.selected() {
			item.LabelIDs = slices.DeleteFunc(item.LabelIDs, func(id string) bool { return !p.labelAllowed(source, id) })
			RedactUserTeams(&item.Creator, p.teamAllowed)
			if item.Owner != nil {
				RedactUserTeams(item.Owner, p.teamAllowed)
			}
			for i := range item.Comments {
				RedactUserTeams(&item.Comments[i].User, p.teamAllowed)
			}
			for i := range item.DescriptionHistory {
				RedactUserTeams(&item.DescriptionHistory[i].Editor, p.teamAllowed)
			}
		}
		parents := append([]string(nil), item.ParentInitiativeIDs...)
		for _, relation := range source.InitiativeRelations {
			if relation.Type == "parent" && relation.InitiativeID == item.ID && !slices.Contains(parents, relation.RelatedInitiativeID) {
				parents = append(parents, relation.RelatedInitiativeID)
			}
		}
		item.ParentInitiativeIDs = slices.DeleteFunc(parents, func(id string) bool {
			return !slices.ContainsFunc(source.Initiatives, func(parent domain.Initiative) bool {
				return parent.ID == id && p.initiativeVisible(source, parent) && (!p.selected() || p.initiativeReadable(source, parent))
			})
		})
		item.Favorite = slices.ContainsFunc(source.Favorites, func(f domain.Favorite) bool {
			return f.UserID == p.principal && f.ResourceType == "initiative" && f.ResourceID == item.ID
		})
		data.Initiatives = []domain.Initiative{item}
	default:
		return data, false, sql.ErrNoRows
	}
	return data, true, nil
}

func (s *SQLiteStore) AuthorizedProjectRecord(ctx context.Context, access AccessContext, ref string) (domain.Project, error) {
	data, allowed, err := s.ResourceReadMetadata(ctx, access, "projects", ref)
	if err != nil {
		return domain.Project{}, err
	}
	if !allowed || len(data.Projects) == 0 {
		return domain.Project{}, sql.ErrNoRows
	}
	return data.Projects[0], nil
}

func (s *SQLiteStore) AuthorizedInitiativeRecord(ctx context.Context, access AccessContext, ref string) (domain.Initiative, error) {
	data, allowed, err := s.ResourceReadMetadata(ctx, access, "initiatives", ref)
	if err != nil {
		return domain.Initiative{}, err
	}
	if !allowed || len(data.Initiatives) == 0 {
		return domain.Initiative{}, sql.ErrNoRows
	}
	return data.Initiatives[0], nil
}

func resourceKeyHasScope(key domain.APIKey, scope string) bool {
	if key.Scopes == nil {
		return true
	}
	return slices.ContainsFunc(key.Scopes, func(value string) bool {
		return strings.EqualFold(strings.TrimSpace(value), scope) || strings.EqualFold(strings.TrimSpace(value), "admin")
	})
}
