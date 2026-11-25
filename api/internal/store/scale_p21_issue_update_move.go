package store

import (
	"context"
	"errors"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// issueMoveAudienceChunk bounds the bound user IDs of one membership query.
const issueMoveAudienceChunk = 500

// issueMoveAudience is loadMemberAccess for a bounded issue update: instead
// of every live member and team membership it reads, in the write
// transaction, the rows of users only. Under WithIssueRenumbering an issue
// update reads them for the people it can name: the viewer (viewerOnTeam,
// whose memberships decide a triage destination) and the notification
// recipients of a moved issue (issueVisibleToMember: role, status, team
// memberships and team grants), who are always users the callback can
// resolve, so users must list the viewer and every selected user.
//
// Each person has at most one workspace and one team-membership row per
// team, and every reader looks rows up by person, so the order of the
// partial lists is immaterial. Members keeps the fields those readers use
// (user ID, role, status) and only lists people whose user row exists, like
// ListMembers. issueVisibleToMember also refuses a non-member when the
// directory is not empty: when the workspace has members but none of these
// users is one, a single empty entry stands for the rest of the directory
// (no reader matches the empty user ID).
func (r *issueHotInputs) issueMoveAudience(ctx context.Context, tx *sqlTx, workspaceID string, users []string) ([]domain.WorkspaceMember, []domain.TeamMember, error) {
	members, teamMembers := []domain.WorkspaceMember{}, []domain.TeamMember{}
	ids := []string{}
	for _, id := range users {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	for start := 0; start < len(ids); start += issueMoveAudienceChunk {
		chunk := ids[start:min(start+issueMoveAudienceChunk, len(ids))]
		args := []any{workspaceID}
		for _, id := range chunk {
			args = append(args, id)
		}
		marks := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		rows, err := tx.QueryContext(ctx, `SELECT m.user_id,m.role,m.status FROM workspace_memberships m JOIN auth_users u ON u.id=m.user_id WHERE m.workspace_id=? AND m.user_id IN (`+marks+`) ORDER BY m.user_id`, args...)
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var member domain.WorkspaceMember
			if err := rows.Scan(&member.User.ID, &member.Role, &member.Status); err != nil {
				rows.Close()
				return nil, nil, err
			}
			if err := r.reserve(ctx, uint64(len(member.User.ID)+len(member.Role)+len(member.Status))*2+512); err != nil {
				rows.Close()
				return nil, nil, err
			}
			members = append(members, member)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, nil, err
		}
		rows, err = tx.QueryContext(ctx, `SELECT team_id,user_id,role FROM team_memberships WHERE workspace_id=? AND user_id IN (`+marks+`) ORDER BY user_id,team_id`, args...)
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var member domain.TeamMember
			if err := rows.Scan(&member.TeamID, &member.UserID, &member.Role); err != nil {
				rows.Close()
				return nil, nil, err
			}
			if err := r.reserve(ctx, uint64(len(member.TeamID)+len(member.UserID)+len(member.Role))*2+512); err != nil {
				rows.Close()
				return nil, nil, err
			}
			teamMembers = append(teamMembers, member)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, nil, err
		}
	}
	if len(members) == 0 {
		rows, err := tx.QueryContext(ctx, `SELECT 1 FROM workspace_memberships m JOIN auth_users u ON u.id=m.user_id WHERE m.workspace_id=? LIMIT 1`, workspaceID)
		if err != nil {
			return nil, nil, err
		}
		directory := rows.Next()
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, nil, err
		}
		if directory {
			members = append(members, domain.WorkspaceMember{})
		}
	}
	return members, teamMembers, ctx.Err()
}

// issueUpdateSequence locks the issue sequence before any issue row and
// returns the number a renumbering update's callback sees as
// NextIssueNumber, from the installed image as issueTeamMoveSequence does.
func (s *SQLiteStore) issueUpdateSequence(ctx context.Context, tx *sqlTx, workspace string) (int, error) {
	owner := &ResourceRead{store: s}
	defer owner.Close()
	image, err := s.retainMetadataLogicalSource(metadataLogicalUnpinnedContext(ctx), workspace)
	if err != nil {
		return 0, err
	}
	defer image.Release()
	return s.issueTeamMoveSequence(ctx, tx, workspace, owner, image)
}
