package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"

	"flow/api/internal/domain"
)

// MemberStatusDeparted marks a former member in Bootstrap.DepartedMembers
// (the reference app's "Left workspace").
const MemberStatusDeparted = "departed"

// createWorkspaceDeparturesSchema creates workspace_departures (schema
// migration 8): one row per former member of a workspace.
func (s *SQLiteStore) createWorkspaceDeparturesSchema(ctx context.Context) error {
	idType := "TEXT"
	if s.dialect == "mysql" {
		idType = "VARCHAR(191)"
	}
	return s.applyMigrationStatements(ctx, []string{fmt.Sprintf(`CREATE TABLE IF NOT EXISTS workspace_departures (workspace_id %s NOT NULL, user_id %s NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE, role VARCHAR(32) NOT NULL, joined_at VARCHAR(40) NOT NULL, left_at VARCHAR(40) NOT NULL, PRIMARY KEY(workspace_id,user_id))`, idType, idType)})
}

// recordWorkspaceDepartureTx keeps a member who is leaving the workspace in
// workspace_departures, with the role and join date of the membership that is
// about to be deleted, so Settings › Members can still list them as "Left
// workspace" (the reference app keeps departed users and the app users of revoked or
// deleted OAuth applications). Run it before the membership row is deleted;
// it does nothing when there is no membership.
func recordWorkspaceDepartureTx(ctx context.Context, tx *sqlTx, workspaceID, userID string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO workspace_departures(workspace_id,user_id,role,joined_at,left_at)
		SELECT m.workspace_id,m.user_id,m.role,m.joined_at,? FROM workspace_memberships m WHERE m.workspace_id=? AND m.user_id=?
		ON CONFLICT(workspace_id,user_id) DO UPDATE SET role=excluded.role,joined_at=excluded.joined_at,left_at=excluded.left_at`,
		now.UTC().Format(time.RFC3339Nano), workspaceID, userID)
	return err
}

// ListDepartedMembers lists the workspace's former members: people removed
// from the workspace and the app users of uninstalled applications, newest
// departure first. A user who has rejoined is a member again and is left out.
// LastSeenAt is the departure time (the reference app shows when they were last active
// in the workspace). App users installed before departures were recorded are
// found through the workspace's user records (inactive app users without a
// membership), dated by their account.
func (s *SQLiteStore) ListDepartedMembers(ctx context.Context, workspaceID, workspaceKey string) ([]domain.WorkspaceMember, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT u.id,u.email,u.name,u.display_name,u.avatar_url,d.role,d.joined_at,d.left_at
		FROM workspace_departures d JOIN auth_users u ON u.id=d.user_id
		WHERE d.workspace_id=? AND NOT EXISTS (SELECT 1 FROM workspace_memberships m WHERE m.workspace_id=d.workspace_id AND m.user_id=d.user_id)
		UNION ALL
		SELECT u.id,u.email,u.name,u.display_name,u.avatar_url,'app',u.created_at,u.updated_at
		FROM workspace_metadata_records r JOIN auth_users u ON u.id=r.record_key
		WHERE r.workspace_key=? AND r.field='users' AND r.record_key LIKE 'app_%' AND u.active=0
			AND NOT EXISTS (SELECT 1 FROM workspace_memberships m WHERE m.workspace_id=? AND m.user_id=u.id)
			AND NOT EXISTS (SELECT 1 FROM workspace_departures d WHERE d.workspace_id=? AND d.user_id=u.id)`,
		workspaceID, workspaceKey, workspaceID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.WorkspaceMember{}
	for rows.Next() {
		var user domain.User
		var email sql.NullString
		var role, joinedRaw, leftRaw string
		if err := rows.Scan(&user.ID, &email, &user.Name, &user.DisplayName, &user.AvatarURL, &role, &joinedRaw, &leftRaw); err != nil {
			return nil, err
		}
		// A departed user is no longer active here, and an app user without its
		// installation is listed as a plain former member (the reference app drops the app
		// flag together with the OAuth client).
		user.Email, user.Active = email.String, false
		joined, _ := time.Parse(time.RFC3339Nano, joinedRaw)
		left, _ := time.Parse(time.RFC3339Nano, leftRaw)
		result = append(result, domain.WorkspaceMember{User: user, Role: role, Status: MemberStatusDeparted, JoinedAt: joined, LastSeenAt: &left})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.SortStableFunc(result, func(a, b domain.WorkspaceMember) int { return b.LastSeenAt.Compare(*a.LastSeenAt) })
	return result, nil
}
