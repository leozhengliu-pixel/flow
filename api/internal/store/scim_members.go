package store

import (
	"context"
	"strings"
)

// SCIMMemberState is a workspace membership's role and status ("" when the
// user is not a member), read before and after SCIM writes for the audit log.
type SCIMMemberState struct {
	Role   string
	Status string
}

// SCIMMemberStates reads the membership state of userIDs in one query per
// 500 ids. Users that are not members are absent from the result.
func (s *SQLiteStore) SCIMMemberStates(ctx context.Context, workspaceID string, userIDs []string) (map[string]SCIMMemberState, error) {
	result := map[string]SCIMMemberState{}
	for start := 0; start < len(userIDs); start += 500 {
		chunk := userIDs[start:min(start+500, len(userIDs))]
		args := []any{workspaceID}
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := s.db.QueryContext(ctx, `SELECT user_id,role,status FROM workspace_memberships WHERE workspace_id=? AND user_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var userID string
			var state SCIMMemberState
			if err := rows.Scan(&userID, &state.Role, &state.Status); err != nil {
				rows.Close()
				return nil, err
			}
			result[userID] = state
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// SCIMUserGroupIDs lists the SCIM groups a user belongs to.
func (s *SQLiteStore) SCIMUserGroupIDs(ctx context.Context, workspaceID, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT group_id FROM scim_group_memberships WHERE workspace_id=? AND user_id=? ORDER BY group_id`, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []string{}
	for rows.Next() {
		var groupID string
		if err := rows.Scan(&groupID); err != nil {
			return nil, err
		}
		groups = append(groups, groupID)
	}
	return groups, rows.Err()
}

// DetachSCIMUserTeams forgets which team memberships SCIM manages for a user
// whose team memberships were removed with the workspace membership, so a
// later re-provision adds them again as SCIM-managed.
func (s *SQLiteStore) DetachSCIMUserTeams(ctx context.Context, workspaceID, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM scim_team_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, userID)
	return err
}

// DetachSCIMGroupTeams forgets the team memberships a group managed (the
// group no longer links to that team); the memberships stay as ordinary
// ones.
func (s *SQLiteStore) DetachSCIMGroupTeams(ctx context.Context, workspaceID, groupID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM scim_team_memberships WHERE workspace_id=? AND group_id=?`, workspaceID, groupID)
	return err
}

// scimManagedMemberSQL is true for a workspace membership (alias m) whose
// user has an active SCIM identity in that workspace while the workspace has
// an unrevoked SCIM token. Revoking every token (or turning SCIM off, which
// callers check in the workspace settings) hands the member back to admins.
const scimManagedMemberSQL = `(EXISTS (SELECT 1 FROM auth_identities si WHERE si.user_id=m.user_id AND si.provider='scim' AND si.issuer=m.workspace_id) AND EXISTS (SELECT 1 FROM scim_tokens st WHERE st.workspace_id=m.workspace_id AND st.revoked_at IS NULL))`

// IsSCIMManagedMember reports whether a member's role, profile and status
// are managed by the workspace's SCIM provisioning: SCIM is enabled and
// scimManagedMemberSQL holds.
func (s *SQLiteStore) IsSCIMManagedMember(ctx context.Context, workspaceID, userID string) (bool, error) {
	if data, _, ok := s.workspaceByID(workspaceID); !ok || !data.WorkspaceSettings.SCIMEnabled {
		return false, nil
	}
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_memberships m WHERE m.workspace_id=? AND m.user_id=? AND `+scimManagedMemberSQL, workspaceID, userID).Scan(&count)
	return count > 0, err
}
