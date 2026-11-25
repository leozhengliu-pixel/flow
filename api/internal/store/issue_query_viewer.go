package store

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"flow/api/internal/domain"
)

type issueQueryMembership struct {
	Role  string
	Teams []domain.TeamMember
}

// Only the principal's memberships can be keyed by the authorization epoch.
// Profile edits need not advance it. Read the profile live with the stamp.
func (s *SQLiteStore) issueQueryViewer(ctx context.Context, workspace, workspaceID, userID string) (domain.User, issueQueryMembership, error) {
	cache := s.sharedResults(ctx)
	if cache != nil && cache.SharedCacheViewerEnabled() && workspace != "" && userID != "" && s.RecordJournalReady() && !strings.HasPrefix(userID, "app_") {
		user, generation, epoch, err := s.readIssueViewerEpoch(ctx, workspaceID, userID)
		if err == nil && generation != "" {
			version := "1." + strconv.FormatInt(epoch, 10)
			stamp := generation + ":" + strconv.FormatInt(epoch, 10)
			key := cache.SharedCacheKey(workspace, "viewer", version, workspaceID, userID, generation)
			membership, err := sharedJSONResult(ctx, cache, key, func(ctx context.Context) (issueQueryMembership, error) {
				role, status, err := s.WorkspaceRole(ctx, workspaceID, userID)
				if err != nil {
					return issueQueryMembership{}, err
				}
				if status != "active" {
					return issueQueryMembership{}, ErrAuthForbidden
				}
				return s.readIssueMemberships(ctx, workspaceID, userID, role)
			}, func(ctx context.Context) bool {
				current, err := s.AuthzEpoch(ctx, workspaceID)
				return err == nil && current == stamp
			})
			return user, membership, err
		}
	}
	role, status, err := s.WorkspaceRole(ctx, workspaceID, userID)
	if err != nil {
		return domain.User{}, issueQueryMembership{}, err
	}
	if status != "active" {
		return domain.User{}, issueQueryMembership{}, ErrAuthForbidden
	}
	user, err := s.authUserByID(ctx, userID)
	if err != nil {
		return user, issueQueryMembership{}, err
	}
	membership, err := s.readIssueMemberships(ctx, workspaceID, userID, role)
	return user, membership, err
}

func (s *SQLiteStore) readIssueViewerEpoch(ctx context.Context, workspaceID, userID string) (domain.User, string, int64, error) {
	var user domain.User
	var email, verified, deleted sql.NullString
	var generation string
	var active int
	var epoch int64
	err := s.db.QueryRowContext(ctx, `SELECT u.id,u.email,u.name,u.display_name,u.avatar_url,u.email_verified_at,u.active,u.deleted_at,m.generation,
		COALESCE((SELECT epoch FROM workspace_authz_epochs a WHERE a.workspace_id=`+recordJournalIdentitySQL(s.dialect, "w.workspace_id")+`),0)
		FROM workspace_states w CROSS JOIN record_journal_mode m JOIN auth_users u ON u.id=?
		WHERE w.workspace_id=? AND m.id=1 AND m.enabled=1`, userID, workspaceID).
		Scan(&user.ID, &email, &user.Name, &user.DisplayName, &user.AvatarURL, &verified, &active, &deleted, &generation, &epoch)
	user.Email, user.Active, user.EmailVerified, user.Deleted = email.String, active == 1, verified.Valid, deleted.Valid
	return user, generation, epoch, err
}

func (s *SQLiteStore) readIssueMemberships(ctx context.Context, workspaceID, userID, role string) (issueQueryMembership, error) {
	result := issueQueryMembership{Role: role}
	rows, err := s.db.QueryContext(ctx, `SELECT team_id,role FROM team_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, userID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		member := domain.TeamMember{UserID: userID}
		if err := rows.Scan(&member.TeamID, &member.Role); err != nil {
			return result, err
		}
		result.Teams = append(result.Teams, member)
	}
	return result, rows.Err()
}
