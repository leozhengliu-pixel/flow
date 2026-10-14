package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"flow/api/internal/domain"
)

// ErrBuiltinApplication reports an attempt to uninstall the built-in Flow
// agent, which follows the server's agent configuration instead.
var ErrBuiltinApplication = errors.New("the built-in agent cannot be uninstalled")

// InstallationRef names an application installation in a workspace.
type InstallationRef struct {
	WorkspaceKey string
	ID           string
}

// ApplicationInstallationsByClient lists every workspace's installation of
// an OAuth client, e.g. to uninstall them when the application is deleted.
func (s *SQLiteStore) ApplicationInstallationsByClient(ctx context.Context, clientID string) ([]InstallationRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_key,id FROM application_installations WHERE client_id=? ORDER BY workspace_key,id`, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []InstallationRef{}
	for rows.Next() {
		var item InstallationRef
		if err := rows.Scan(&item.WorkspaceKey, &item.ID); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// UninstallApplication removes an application installation from the
// workspace (the reference app's "Revoke access" on an installed application): its
// tokens and the workspace's authorizations of the application are revoked,
// its active agent sessions are canceled, and its app user leaves the
// workspace (so it is no longer offered in assignee or delegate pickers). The
// app user record is kept, inactive, so history still names it, and
// installing the application again reuses it. Callers authorize.
func (s *SQLiteStore) UninstallApplication(ctx context.Context, workspace, id string) (domain.ApplicationInstallation, error) {
	var removed domain.ApplicationInstallation
	var event domain.DomainEvent
	err := s.oauthMetadataTransaction(ctx, workspace, func(tx *sqlTx, lock string, current domain.Bootstrap) (domain.Bootstrap, error) {
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT data FROM application_installations WHERE workspace_key=? AND id=?`+lock, current.Workspace.URLKey, id).Scan(&raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return current, sql.ErrNoRows
			}
			return current, err
		}
		if err := json.Unmarshal(raw, &removed); err != nil {
			return current, err
		}
		if removed.Builtin || removed.ClientID == BuiltinApplicationClientID {
			return current, ErrBuiltinApplication
		}
		next, err := revokeApplicationCredentialsTx(ctx, tx, current.Workspace.URLKey, removed.UserID, current)
		if err != nil {
			return current, err
		}
		now := time.Now().UTC()
		for index := range next.OAuthAuthorizations {
			grant := &next.OAuthAuthorizations[index]
			if grant.ClientID != removed.ClientID || grant.RevokedAt != nil {
				continue
			}
			grant.RevokedAt = &now
			grantRaw, err := json.Marshal(grant)
			if err != nil {
				return current, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='oauthAuthorizations' AND record_key=?`, grantRaw, current.Workspace.URLKey, grant.ID); err != nil {
				return current, err
			}
		}
		// The app user stays listed in Settings › Members as "Left workspace".
		if err := recordWorkspaceDepartureTx(ctx, tx, current.Workspace.ID, removed.UserID, now); err != nil {
			return current, err
		}
		statements := []struct {
			query string
			args  []any
		}{
			{`UPDATE application_agent_tasks SET status='canceled',version=version+1 WHERE workspace_key=? AND app_user_id=? AND status IN ('pending','active','awaitingInput')`, []any{current.Workspace.URLKey, removed.UserID}},
			{`DELETE FROM team_memberships WHERE workspace_id=? AND user_id=?`, []any{current.Workspace.ID, removed.UserID}},
			{`DELETE FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, []any{current.Workspace.ID, removed.UserID}},
			{`UPDATE auth_users SET active=0,updated_at=? WHERE id=?`, []any{now.Format(time.RFC3339Nano), removed.UserID}},
			{`DELETE FROM application_installations WHERE id=?`, []any{removed.ID}},
		}
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
				return current, err
			}
		}
		removed.Active = false
		user := removed.User()
		userRaw, err := json.Marshal(user)
		if err != nil {
			return current, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='users' AND record_key=?`, userRaw, current.Workspace.URLKey, removed.UserID); err != nil {
			return current, err
		}
		next.Users = slices.Clone(next.Users)
		if index := slices.IndexFunc(next.Users, func(item domain.User) bool { return item.ID == user.ID }); index >= 0 {
			next.Users[index] = user
		}
		event, err = oauthRecordEvent(ctx, tx, "application.uninstalled", removed.ID)
		return next, err
	})
	if err == nil {
		s.publishOAuthRecordEvent(workspace, event)
	}
	return removed, err
}
