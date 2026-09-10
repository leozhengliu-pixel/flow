package store

import (
	"context"
	"flow/api/internal/domain"
)

// Memberships propagate upward as ordinary members. Existing owner roles are
// retained, and detaching a subtree does not silently remove memberships.
func syncTeamAncestorMembers(ctx context.Context, tx *sqlTx, data domain.Bootstrap, teamID string, userIDs ...string) error {
	ids := domain.TeamSubtreeIDs(&data, []string{teamID})
	clause, values := bindList("team_id", ids)
	if len(userIDs) > 0 {
		userClause, userValues := bindList("user_id", userIDs)
		clause += " AND " + userClause
		values = append(values, userValues...)
	}
	seen := map[string]bool{teamID: true}
	for parent := data.TeamSettings[teamID].ParentTeamID; parent != "" && !seen[parent]; parent = data.TeamSettings[parent].ParentTeamID {
		seen[parent] = true
		args := append([]any{parent, data.Workspace.ID}, values...)
		_, err := tx.ExecContext(ctx, `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) SELECT workspace_id,?,user_id,'member',MIN(joined_at) FROM team_memberships WHERE workspace_id=? AND `+clause+` AND user_id IN (SELECT user_id FROM workspace_memberships WHERE workspace_id=team_memberships.workspace_id AND role<>'guest' AND status='active') GROUP BY workspace_id,user_id ON CONFLICT(workspace_id,team_id,user_id) DO NOTHING`, args...)
		if err != nil {
			return err
		}
	}
	return nil
}
