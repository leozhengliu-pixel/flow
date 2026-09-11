package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"flow/api/internal/domain"
)

// OAuth consent needs an identity and authentication/application policy, not
// issue counts, team membership projections, or the workspace entity catalog.
func (s *SQLiteStore) OAuthWorkspaceAccess(ctx context.Context, workspace, userID string) (domain.Bootstrap, error) {
	data := domain.Bootstrap{Settings: map[string]any{}}
	if workspace == "" {
		account, err := s.OAuthAccountForUser(ctx, userID)
		if err != nil {
			return data, err
		}
		workspace = account.LastWorkspaceKey
	}
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT `+s.jsonText("s.data", "workspace")+`,m.role FROM workspace_states s JOIN workspace_memberships m ON m.workspace_id=s.workspace_id WHERE s.workspace_key=? AND m.user_id=? AND m.status='active'`, workspace, userID).Scan(&raw, &data.ViewerRole)
	if errors.Is(err, sql.ErrNoRows) {
		return data, ErrAuthForbidden
	}
	if err != nil {
		return data, err
	}
	if err = json.Unmarshal(raw, &data.Workspace); err != nil {
		return data, err
	}
	fields := []string{"reviewThirdPartyApplications", "requireTwoFactor", "disableAdminBypass", "emailAuthEnabled", "googleAuthEnabled", "sessionDurationDays"}
	clause, args := bindList("record_key", fields)
	args = append([]any{workspace}, args...)
	rows, err := s.db.QueryContext(ctx, `SELECT field,record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND ((field='workspaceSettings' AND `+clause+`) OR field='identityProviders' OR (field='settings' AND record_key='applicationPolicies'))`, args...)
	if err != nil {
		return data, err
	}
	defer rows.Close()
	settings := map[string]json.RawMessage{}
	for rows.Next() {
		var field, id string
		var value []byte
		if err := rows.Scan(&field, &id, &value); err != nil {
			return data, err
		}
		switch field {
		case "workspaceSettings":
			settings[id] = value
		case "identityProviders":
			var provider domain.IdentityProvider
			if err := json.Unmarshal(value, &provider); err != nil {
				return data, err
			}
			data.IdentityProviders = append(data.IdentityProviders, provider)
		case "settings":
			var policies any
			if err := json.Unmarshal(value, &policies); err != nil {
				return data, err
			}
			data.Settings[id] = policies
		}
	}
	if err := rows.Err(); err != nil {
		return data, err
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return data, err
	}
	err = json.Unmarshal(encoded, &data.WorkspaceSettings)
	return data, err
}

func (s *SQLiteStore) OAuthAccountForUser(ctx context.Context, userID string) (domain.AccountBootstrap, error) {
	result := domain.AccountBootstrap{Workspaces: []domain.WorkspaceMembership{}}
	user, err := s.authUserByID(ctx, userID)
	if err != nil {
		return result, err
	}
	result.Viewer = user
	rows, err := s.db.QueryContext(ctx, `SELECT `+s.jsonText("s.data", "workspace")+`,m.role,m.joined_at FROM workspace_memberships m JOIN workspace_states s ON s.workspace_id=m.workspace_id WHERE m.user_id=? AND m.status='active' ORDER BY m.joined_at,s.workspace_key`, userID)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var raw []byte
		var role, joined string
		if err = rows.Scan(&raw, &role, &joined); err != nil {
			rows.Close()
			return result, err
		}
		var workspace domain.Workspace
		if err = json.Unmarshal(raw, &workspace); err != nil {
			rows.Close()
			return result, err
		}
		at, _ := time.Parse(time.RFC3339Nano, joined)
		result.Workspaces = append(result.Workspaces, domain.WorkspaceMembership{Workspace: workspace, Role: titleRole(role), JoinedAt: at})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	if len(result.Workspaces) > 0 {
		result.LastWorkspaceKey = result.Workspaces[0].Workspace.URLKey
	}
	var preferred string
	if s.db.QueryRowContext(ctx, `SELECT last_workspace_key FROM auth_account_state WHERE user_id=?`, userID).Scan(&preferred) == nil {
		for _, membership := range result.Workspaces {
			if membership.Workspace.URLKey == preferred {
				result.LastWorkspaceKey = preferred
				break
			}
		}
	}
	return result, nil
}
