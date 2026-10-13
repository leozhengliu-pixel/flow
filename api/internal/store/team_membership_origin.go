package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Team membership origins (domain.TeamMemberOrigin*) are stored on each
// team_memberships row. Rows written before the column existed take the
// column default, manual; SCIM-managed rows read as scim whatever the column
// says (scim_team_memberships.managed stays the source of truth for what SCIM
// may remove).

var (
	// ErrTeamMemberOriginMismatch: an origin-scoped removal found a
	// membership with a different origin (see TeamMemberOriginMismatchError).
	ErrTeamMemberOriginMismatch = errors.New("team membership has a different origin")
	// ErrTeamOwnerProtected: an origin-scoped (sync) removal never removes a
	// team owner; owners are removed by a person in team settings.
	ErrTeamOwnerProtected = errors.New("team owners are kept by origin-scoped removals; remove the owner role first")
	// ErrInvalidTeamMemberOrigin: an origin outside domain.TeamMemberOrigins,
	// or one Flow sets itself (invitation, scim).
	ErrInvalidTeamMemberOrigin = errors.New("origin must be manual, api or sync")
)

// TeamMemberOriginMismatchError carries the membership's actual origin.
type TeamMemberOriginMismatchError struct{ Origin string }

func (e TeamMemberOriginMismatchError) Error() string {
	return fmt.Sprintf("team membership origin is %q, not the origin this removal is limited to", e.Origin)
}

func (e TeamMemberOriginMismatchError) Unwrap() error { return ErrTeamMemberOriginMismatch }

// addTeamMembershipOrigin is schema migration 7.
func (s *SQLiteStore) addTeamMembershipOrigin(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `ALTER TABLE team_memberships ADD COLUMN origin VARCHAR(16) NOT NULL DEFAULT 'manual'`)
	if err != nil {
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "duplicate column") || strings.Contains(message, "already exists") {
			return nil
		}
	}
	return err
}

// TeamMembershipChangeRequest adds, updates or removes one team membership.
type TeamMembershipChangeRequest struct {
	WorkspaceID string
	TeamID      string
	UserID      string
	// Member false removes the membership.
	Member bool
	Role   string
	// Origin of an added membership (default manual). With SetOrigin it also
	// replaces the origin of an existing membership.
	Origin    string
	SetOrigin bool
	// RequireOrigins limits a removal to memberships with one of these
	// origins; others fail with TeamMemberOriginMismatchError.
	RequireOrigins []string
	// KeepOwners makes a removal fail with ErrTeamOwnerProtected for an owner.
	KeepOwners bool
	// DryRun computes the result without writing; guards that would fail
	// are reported in Kept instead of as errors.
	DryRun bool
}

// TeamMembershipChangeResult lists what the request changed (or, in a dry
// run, would change). Kept is set when a removal left the member in place.
type TeamMembershipChangeResult struct {
	Changes []domain.TeamMemberChange
	Kept    *domain.TeamMemberKept
}

// ChangeTeamMembership is SetTeamMembership with origins, removal guards, a
// dry run and the list of changes (including parent-team memberships added
// because the user joined a sub-team) for the audit log.
func (s *SQLiteStore) ChangeTeamMembership(ctx context.Context, request TeamMembershipChangeRequest) (TeamMembershipChangeResult, error) {
	result := TeamMembershipChangeResult{}
	workspaceID, teamID, userID, role := request.WorkspaceID, request.TeamID, request.UserID, request.Role
	if role == "" {
		role = "member"
	}
	if role != "owner" && role != "member" {
		return result, fmt.Errorf("invalid team role")
	}
	origin := domain.TeamMemberOriginManual
	if strings.TrimSpace(request.Origin) != "" {
		if !domain.ValidTeamMemberOrigin(request.Origin) {
			return result, ErrInvalidTeamMemberOrigin
		}
		origin = domain.TeamMemberOriginOrDefault(request.Origin)
	}
	data, workspaceKey, ok := s.workspaceByID(workspaceID)
	if !ok || !slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == teamID }) {
		return result, ErrAuthForbidden
	}
	workspaceRole, status, err := s.WorkspaceRole(ctx, workspaceID, userID)
	if err != nil || status != "active" {
		return result, ErrAuthForbidden
	}
	// Serialize local writers, while row locks below coordinate separate
	// application instances on databases that support SELECT ... FOR UPDATE.
	s.teamMembershipMu.Lock()
	defer s.teamMembershipMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if s.dialect != "sqlite" && !request.DryRun {
		rows, err := tx.QueryContext(ctx, `SELECT user_id FROM team_memberships WHERE workspace_id=? AND team_id=? ORDER BY user_id FOR UPDATE`, workspaceID, teamID)
		if err != nil {
			return result, err
		}
		for rows.Next() {
			var lockedUserID string
			if err := rows.Scan(&lockedUserID); err != nil {
				rows.Close()
				return result, err
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return result, err
		}
		if err := rows.Close(); err != nil {
			return result, err
		}
	}
	var currentRole, currentOrigin string
	currentErr := tx.QueryRowContext(ctx, `SELECT role,origin FROM team_memberships WHERE workspace_id=? AND team_id=? AND user_id=?`, workspaceID, teamID, userID).Scan(&currentRole, &currentOrigin)
	if currentErr != nil && !errors.Is(currentErr, sql.ErrNoRows) {
		return result, currentErr
	}
	exists := currentErr == nil
	var managed int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM scim_team_memberships WHERE workspace_id=? AND team_id=? AND user_id=? AND managed=1`, workspaceID, teamID, userID).Scan(&managed); err != nil {
		return result, err
	}
	if exists {
		currentOrigin = domain.TeamMemberOriginOrDefault(currentOrigin)
		if managed > 0 {
			currentOrigin = domain.TeamMemberOriginSCIM
		}
	}
	teamKey := ""
	if index := slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.ID == teamID }); index >= 0 {
		teamKey = data.Teams[index].Key
	}
	keep := func(reason string, err error) (TeamMembershipChangeResult, error) {
		result.Kept = &domain.TeamMemberKept{TeamID: teamID, UserID: userID, Role: currentRole, Origin: currentOrigin, Reason: reason}
		if request.DryRun {
			return result, nil
		}
		return result, err
	}
	if !request.Member {
		if !exists {
			result.Kept = &domain.TeamMemberKept{TeamID: teamID, UserID: userID, Reason: domain.TeamMemberKeptNotMember}
			return result, nil
		}
		if managed > 0 {
			return keep(domain.TeamMemberKeptSCIMManaged, ErrManagedTeamMembership)
		}
		if len(request.RequireOrigins) > 0 && !slices.Contains(request.RequireOrigins, currentOrigin) {
			return keep(domain.TeamMemberKeptOriginMismatch, TeamMemberOriginMismatchError{Origin: currentOrigin})
		}
		if request.KeepOwners && currentRole == "owner" {
			return keep(domain.TeamMemberKeptOwner, ErrTeamOwnerProtected)
		}
		for _, descendant := range domain.TeamSubtreeIDs(&data, []string{teamID}) {
			if workspaceRole == "guest" || descendant == teamID {
				continue
			}
			var descendantRole string
			err := tx.QueryRowContext(ctx, `SELECT role FROM team_memberships WHERE workspace_id=? AND team_id=? AND user_id=?`, workspaceID, descendant, userID).Scan(&descendantRole)
			if err == nil {
				return keep(domain.TeamMemberKeptSubTeamMember, ErrTeamHasSubteamMembership)
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return result, err
			}
		}
		if currentRole == "owner" {
			if err := ensureAnotherTeamOwner(ctx, tx, workspaceID, teamID, userID); err != nil {
				if errors.Is(err, ErrLastTeamOwner) {
					return keep(domain.TeamMemberKeptLastOwner, err)
				}
				return result, err
			}
		}
		result.Changes = append(result.Changes, domain.TeamMemberChange{TeamID: teamID, TeamKey: teamKey, UserID: userID, Action: domain.TeamMemberRemoved, RoleBefore: currentRole, Origin: currentOrigin})
		if request.DryRun {
			return result, nil
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM team_memberships WHERE workspace_id=? AND team_id=? AND user_id=?`, workspaceID, teamID, userID); err != nil {
			return result, err
		}
		if err := tx.Commit(); err != nil {
			return result, err
		}
		return result, s.cleanupTeamMemberData(ctx, workspaceKey, teamID, userID)
	}
	if managed > 0 {
		return result, ErrManagedTeamMembership
	}
	if exists {
		if currentRole == "owner" && role != "owner" {
			if err := ensureAnotherTeamOwner(ctx, tx, workspaceID, teamID, userID); err != nil {
				return result, err
			}
		}
		if currentRole != role {
			result.Changes = append(result.Changes, domain.TeamMemberChange{TeamID: teamID, TeamKey: teamKey, UserID: userID, Action: domain.TeamMemberRoleChanged, RoleBefore: currentRole, RoleAfter: role, Origin: currentOrigin})
		}
		nextOrigin := currentOrigin
		if request.SetOrigin && origin != currentOrigin {
			nextOrigin = origin
			result.Changes = append(result.Changes, domain.TeamMemberChange{TeamID: teamID, TeamKey: teamKey, UserID: userID, Action: domain.TeamMemberOriginChanged, RoleBefore: currentRole, RoleAfter: role, Origin: origin, OriginBefore: currentOrigin})
		}
		if !request.DryRun && len(result.Changes) > 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE team_memberships SET role=?,origin=? WHERE workspace_id=? AND team_id=? AND user_id=?`, role, nextOrigin, workspaceID, teamID, userID); err != nil {
				return result, err
			}
		}
		// Re-adding an existing member also restores parent-team memberships
		// it is missing (as the former upsert did).
		inherited, err := addTeamAncestorMemberships(ctx, tx, data, teamID, userID, workspaceRole, nextOrigin, request.DryRun)
		if err != nil {
			return result, err
		}
		result.Changes = append(result.Changes, inherited...)
		if request.DryRun || len(result.Changes) == 0 {
			return result, nil
		}
		return result, tx.Commit()
	}
	result.Changes = append(result.Changes, domain.TeamMemberChange{TeamID: teamID, TeamKey: teamKey, UserID: userID, Action: domain.TeamMemberAdded, RoleAfter: role, Origin: origin})
	if !request.DryRun {
		if _, err := tx.ExecContext(ctx, `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at,origin) VALUES(?,?,?,?,?,?)`, workspaceID, teamID, userID, role, time.Now().UTC().Format(time.RFC3339Nano), origin); err != nil {
			return result, err
		}
	}
	inherited, err := addTeamAncestorMemberships(ctx, tx, data, teamID, userID, workspaceRole, origin, request.DryRun)
	if err != nil {
		return result, err
	}
	result.Changes = append(result.Changes, inherited...)
	if request.DryRun {
		return result, nil
	}
	return result, tx.Commit()
}

// addTeamAncestorMemberships is syncTeamAncestorMembers for one user who
// just joined teamID: they become an ordinary member of every ancestor team
// they are not on yet, with the same origin. Guests do not propagate.
func addTeamAncestorMemberships(ctx context.Context, tx *sqlTx, data domain.Bootstrap, teamID, userID, workspaceRole, origin string, dryRun bool) ([]domain.TeamMemberChange, error) {
	if workspaceRole == "guest" {
		return nil, nil
	}
	changes := []domain.TeamMemberChange{}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	seen := map[string]bool{teamID: true}
	for parent := data.TeamSettings[teamID].ParentTeamID; parent != "" && !seen[parent]; parent = data.TeamSettings[parent].ParentTeamID {
		seen[parent] = true
		var role string
		err := tx.QueryRowContext(ctx, `SELECT role FROM team_memberships WHERE workspace_id=? AND team_id=? AND user_id=?`, data.Workspace.ID, parent, userID).Scan(&role)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if !dryRun {
			if _, err := tx.ExecContext(ctx, `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at,origin) VALUES(?,?,?,'member',?,?) ON CONFLICT(workspace_id,team_id,user_id) DO NOTHING`, data.Workspace.ID, parent, userID, stamp, origin); err != nil {
				return nil, err
			}
		}
		teamKey := ""
		if index := slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.ID == parent }); index >= 0 {
			teamKey = data.Teams[index].Key
		}
		changes = append(changes, domain.TeamMemberChange{TeamID: parent, TeamKey: teamKey, UserID: userID, Action: domain.TeamMemberAdded, RoleAfter: "member", Origin: origin, Inherited: true})
	}
	return changes, nil
}

// RemoveMemberWithChanges is RemoveMember that also returns the team
// memberships the removal deleted, for the audit log.
func (s *SQLiteStore) RemoveMemberWithChanges(ctx context.Context, workspaceID, userID string) ([]domain.TeamMemberChange, error) {
	if err := s.ensureAdminRemains(ctx, workspaceID, userID); err != nil {
		return nil, err
	}
	data, _, _ := s.workspaceByID(workspaceID)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT m.team_id,m.role,m.origin,
		CASE WHEN EXISTS (SELECT 1 FROM scim_team_memberships sm WHERE sm.workspace_id=m.workspace_id AND sm.team_id=m.team_id AND sm.user_id=m.user_id AND sm.managed=1) THEN 1 ELSE 0 END
		FROM team_memberships m WHERE m.workspace_id=? AND m.user_id=? ORDER BY m.team_id`, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	changes := []domain.TeamMemberChange{}
	for rows.Next() {
		var teamID, role, origin string
		var managed int
		if err := rows.Scan(&teamID, &role, &origin, &managed); err != nil {
			rows.Close()
			return nil, err
		}
		origin = domain.TeamMemberOriginOrDefault(origin)
		if managed == 1 {
			origin = domain.TeamMemberOriginSCIM
		}
		change := domain.TeamMemberChange{TeamID: teamID, UserID: userID, Action: domain.TeamMemberRemoved, RoleBefore: role, Origin: origin}
		if index := slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.ID == teamID }); index >= 0 {
			change.TeamKey = data.Teams[index].Key
		}
		changes = append(changes, change)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM team_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, userID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, userID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return changes, nil
}

// InvitationAcceptance is AcceptInvitation's result with the invitation and
// the team memberships it added.
type InvitationAcceptance struct {
	Membership   domain.WorkspaceMembership
	InvitationID string
	InviterID    string
	Changes      []domain.TeamMemberChange
}

// SCIMTokenIdentity names the active SCIM token a secret belongs to (id and
// name only), so SCIM changes can be attributed to their connection.
func (s *SQLiteStore) SCIMTokenIdentity(ctx context.Context, secret string) (SCIMToken, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return SCIMToken{}, ErrAuthInvalid
	}
	var item SCIMToken
	err := s.db.QueryRowContext(ctx, `SELECT id,workspace_id,name FROM scim_tokens WHERE token_hash=? AND revoked_at IS NULL`, tokenHash(secret)).Scan(&item.ID, &item.WorkspaceID, &item.Name)
	if err != nil {
		return SCIMToken{}, ErrAuthInvalid
	}
	return item, nil
}
