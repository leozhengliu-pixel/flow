package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"flow/api/internal/domain"
)

const (
	scaleP21SW22VisibilityWork       = 128 << 20
	scaleP21SW22VisibilityPolicy     = 8 << 20
	scaleP21SW22VisibilityMembers    = 8 << 20
	scaleP21SW22VisibilityMembership = 16 << 20
	scaleP21SW22VisibilityResult     = 16 << 20
	scaleP21SW22VisibilityRow        = 64 << 10
	scaleP21SW22VisibilityApp        = 512 << 10
)

type scaleP21SW22VisibilityMember struct {
	id, role, status string
}

// ViewerVisibilityKeysRead retains work admission through use of Keys.
// Keys and its strings must not be retained after Close. A caller that keeps
// detached values must first charge its own reservation or bounded cache.
type ViewerVisibilityKeysRead struct {
	Keys    map[string]string
	release func()
}

func (r *ViewerVisibilityKeysRead) Close() error {
	if r != nil {
		r.Keys = nil
		if r.release != nil {
			r.release()
			r.release = nil
		}
	}
	return nil
}

func (s *SQLiteStore) BeginViewerVisibilityKeysRead(ctx context.Context, workspace string) (*ViewerVisibilityKeysRead, error) {
	if !s.BoundedMetadataEnabled() {
		keys, err := s.ViewerVisibilityKeys(ctx, workspace)
		if err != nil {
			return nil, err
		}
		return &ViewerVisibilityKeysRead{Keys: keys}, nil
	}
	// The handle owns an independent lease. Borrowing a caller's live lease
	// would let that caller release admission while Keys remains in use.
	ctx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, scaleP21SW22VisibilityWork)
	if err != nil {
		return nil, err
	}
	keys, err := s.scaleP21SW22ViewerVisibilityKeys(ctx, workspace)
	if err != nil {
		release()
		return nil, err
	}
	return &ViewerVisibilityKeysRead{Keys: keys, release: release}, nil
}

// This compatibility helper returns one key per active workspace membership.
// Native headers are retained under fixed limits; bodies and unrelated metadata
// never enter the policy graph or returned map. No credential intersects this
// global invalidation map: its callers select the relevant principal's key.
// BeginViewerVisibilityKeysRead owns its construction and result lease.
func (s *SQLiteStore) scaleP21SW22ViewerVisibilityKeys(ctx context.Context, workspace string) (map[string]string, error) {
	data, ok, err := s.scaleP21SW22VisibilityPolicy(workspace)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrAuthForbidden
	}
	members, err := s.scaleP21SW22VisibilityMemberHeaders(ctx, data.Workspace.ID)
	if err != nil {
		return nil, err
	}
	// ListMembers decorates every app before the caller skips suspended rows.
	// Its only decorated field consumed here is ID; decoding the whole
	// installation preserves malformed-field errors without its global
	// name/logo/scope refresh, which cannot change that ID.
	memberBytes := 0
	for index := range members {
		memberBytes += len(members[index].id) + len(members[index].role) + len(members[index].status) + 256
		if memberBytes > scaleP21SW22VisibilityMembers {
			return nil, ErrMetadataWorkBudget
		}
		if !strings.HasPrefix(members[index].id, "app_") {
			continue
		}
		var size int
		var raw []byte
		length := s.scaleP21SW22VisibilityLength("data")
		err := s.db.QueryRowContext(ctx, `SELECT `+length+`,CASE WHEN `+length+`<=? THEN data ELSE NULL END FROM application_installations WHERE user_id=?`, scaleP21SW22VisibilityApp, members[index].id).Scan(&size, &raw)
		if err != nil {
			return nil, err
		}
		if size > scaleP21SW22VisibilityApp {
			return nil, ErrMetadataWorkBudget
		}
		var app domain.ApplicationInstallation
		if err := json.Unmarshal(raw, &app); err != nil {
			return nil, err
		}
		memberBytes += len(app.UserID)
		if memberBytes > scaleP21SW22VisibilityMembers {
			return nil, ErrMetadataWorkBudget
		}
		members[index].id = app.UserID
	}
	byUser, err := s.scaleP21SW22VisibilityMembershipHeaders(ctx, data.Workspace.ID)
	if err != nil {
		return nil, err
	}
	result, used := make(map[string]string, len(members)), 0
	allowed := make([]string, 0, len(data.Teams))
	for _, member := range members {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if member.status != "active" {
			continue
		}
		data.TeamMembers = byUser[member.id]
		allowed = allowed[:0]
		for _, team := range data.Teams {
			if teamVisibleToUser(data, team.ID, member.id, member.role) {
				allowed = append(allowed, team.ID)
			}
		}
		slices.Sort(allowed)
		key := member.role + "|" + strings.Join(allowed, ",")
		if isWorkspaceAdminRole(member.role) {
			readable := visibleIssueTeams(data, member.id, member.role)
			slices.Sort(readable)
			key += "|" + strings.Join(readable, ",")
		}
		cost := len(member.id) + len(key) + 256
		if cost > scaleP21SW22VisibilityResult-used {
			return nil, ErrMetadataWorkBudget
		}
		used += cost
		result[member.id] = key
	}
	return result, nil
}

func (s *SQLiteStore) scaleP21SW22VisibilityPolicy(workspace string) (domain.Bootstrap, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	source, ok := s.workspaces[workspace]
	if !ok {
		return domain.Bootstrap{}, false, nil
	}
	used := len(source.Workspace.ID) + len(source.Workspace.URLKey) + 512
	for _, team := range source.Teams {
		used += 512 + 2*(len(team.ID)+len(team.Key)+len(team.ExternalSource))
		if used > scaleP21SW22VisibilityPolicy {
			return domain.Bootstrap{}, true, ErrMetadataWorkBudget
		}
	}
	for id, settings := range source.TeamSettings {
		used += 512 + 2*(len(id)+len(settings.ParentTeamID)+len(settings.Access)+len(settings.MembershipRestriction)+len(settings.SettingsPermission)+len(settings.LabelPermission)+len(settings.MemberPermission))
		if used > scaleP21SW22VisibilityPolicy {
			return domain.Bootstrap{}, true, ErrMetadataWorkBudget
		}
	}
	if used > scaleP21SW22VisibilityPolicy {
		return domain.Bootstrap{}, true, ErrMetadataWorkBudget
	}
	data := resourcePolicyMetadata(source)
	return data, true, nil
}

func (s *SQLiteStore) scaleP21SW22VisibilityMemberHeaders(ctx context.Context, workspaceID string) ([]scaleP21SW22VisibilityMember, error) {
	cast := "TEXT"
	if s.dialect == "mysql" {
		cast = "CHAR"
	}
	subject := `COALESCE((SELECT i.subject FROM auth_identities i WHERE i.user_id=u.id AND (i.provider IN ('oidc','saml') OR (i.provider='scim' AND i.issuer=m.workspace_id)) ORDER BY CASE WHEN i.provider='scim' THEN 0 ELSE 1 END,i.created_at,i.id LIMIT 1),'')`
	columns := []string{"u.id", "u.email", "u.name", "u.display_name", "u.avatar_url", "u.email_verified_at", "u.active", "u.deleted_at", "m.role", "m.status", "m.joined_at", "m.last_seen_at", subject}
	sizes := slices.Clone(columns)
	sizes[6] = "CAST(u.active AS " + cast + ")"
	size := s.scaleP21SW22VisibilityLength(sizes...)
	selected := []string{size}
	for _, column := range columns {
		selected = append(selected, "CASE WHEN ("+size+")<="+strconv.Itoa(scaleP21SW22VisibilityRow)+" THEN "+column+" ELSE NULL END")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+strings.Join(selected, ",")+`,CASE WHEN `+scimManagedMemberSQL+` THEN 1 ELSE 0 END
		FROM workspace_memberships m JOIN auth_users u ON u.id=m.user_id WHERE m.workspace_id=? ORDER BY lower(u.display_name)`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []scaleP21SW22VisibilityMember
	used := 0
	for rows.Next() {
		var member scaleP21SW22VisibilityMember
		var email, verified, deleted, lastSeen sql.NullString
		var name, displayName, avatar, joined, subject string
		var active, managed, size int
		err := rows.Scan(&size, &member.id, &email, &name, &displayName, &avatar, &verified, &active, &deleted, &member.role, &member.status, &joined, &lastSeen, &subject, &managed)
		if size > scaleP21SW22VisibilityRow {
			return nil, ErrMetadataWorkBudget
		}
		if err != nil {
			return nil, err
		}
		cost := len(member.id) + len(member.role) + len(member.status) + 256
		if cost > scaleP21SW22VisibilityMembers-used {
			return nil, ErrMetadataWorkBudget
		}
		used += cost
		result = append(result, member)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) scaleP21SW22VisibilityMembershipHeaders(ctx context.Context, workspaceID string) (map[string][]domain.TeamMember, error) {
	columns := []string{"m.team_id", "m.user_id", "m.role", "m.joined_at", "m.origin"}
	size := s.scaleP21SW22VisibilityLength(columns...)
	selected := []string{size}
	for _, column := range columns {
		selected = append(selected, "CASE WHEN ("+size+")<="+strconv.Itoa(scaleP21SW22VisibilityRow)+" THEN "+column+" ELSE NULL END")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+strings.Join(selected, ",")+`,
		CASE WHEN EXISTS (SELECT 1 FROM scim_team_memberships sm WHERE sm.workspace_id=m.workspace_id AND sm.team_id=m.team_id AND sm.user_id=m.user_id AND sm.managed=1) THEN 1 ELSE 0 END
		FROM team_memberships m WHERE m.workspace_id=? ORDER BY m.joined_at`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result, used := map[string][]domain.TeamMember{}, 0
	for rows.Next() {
		var member domain.TeamMember
		var joined, origin string
		var managed, size int
		err := rows.Scan(&size, &member.TeamID, &member.UserID, &member.Role, &joined, &origin, &managed)
		if size > scaleP21SW22VisibilityRow {
			return nil, ErrMetadataWorkBudget
		}
		if err != nil {
			return nil, err
		}
		cost := len(member.TeamID) + len(member.UserID) + len(member.Role) + 512
		if cost > scaleP21SW22VisibilityMembership-used {
			return nil, ErrMetadataWorkBudget
		}
		used += cost
		result[member.UserID] = append(result[member.UserID], member)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) scaleP21SW22VisibilityLength(columns ...string) string {
	result := make([]string, 0, len(columns))
	for _, column := range columns {
		if s.dialect == "sqlite" {
			result = append(result, "COALESCE(LENGTH(CAST("+column+" AS BLOB)),0)")
		} else {
			result = append(result, "COALESCE(OCTET_LENGTH("+column+"),0)")
		}
	}
	return strings.Join(result, "+")
}
