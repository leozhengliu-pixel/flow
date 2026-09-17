package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

func (s *SQLiteStore) ensureApplicationAgents(ctx context.Context) error {
	blob := "BLOB"
	if s.dialect == "mysql" {
		blob = "LONGBLOB"
	}
	if s.dialect == "postgres" {
		blob = "BYTEA"
	}
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS application_installations (id VARCHAR(191) PRIMARY KEY,workspace_key VARCHAR(191) NOT NULL,client_id VARCHAR(191) NOT NULL,user_id VARCHAR(191) NOT NULL UNIQUE,data ` + blob + ` NOT NULL,webhook_secret TEXT NOT NULL,UNIQUE(workspace_key,client_id))`,
		`CREATE TABLE IF NOT EXISTS application_agent_tasks (id VARCHAR(191) PRIMARY KEY,workspace_key VARCHAR(191) NOT NULL,issue_id VARCHAR(191) NOT NULL,app_user_id VARCHAR(191) NOT NULL,status VARCHAR(32) NOT NULL,version BIGINT NOT NULL,data ` + blob + ` NOT NULL,lease_until VARCHAR(64) NOT NULL DEFAULT '',next_attempt VARCHAR(64) NOT NULL DEFAULT '',attempts INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS application_agent_activities (session_id VARCHAR(191) NOT NULL,id VARCHAR(191) NOT NULL,data ` + blob + ` NOT NULL,created_at VARCHAR(64) NOT NULL,PRIMARY KEY(session_id,id))`,
		`CREATE INDEX IF NOT EXISTS application_agent_issue ON application_agent_tasks(workspace_key,issue_id)`,
		`CREATE INDEX IF NOT EXISTS application_agent_pending ON application_agent_tasks(status,next_attempt)`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE application_agent_tasks ADD COLUMN created_at VARCHAR(64) NOT NULL DEFAULT ''`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") && !strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return err
	}
	for _, statement := range []string{
		`CREATE INDEX IF NOT EXISTS application_agent_resource_created ON application_agent_tasks(workspace_key,issue_id,created_at)`,
		`CREATE INDEX IF NOT EXISTS application_agent_owner_created ON application_agent_tasks(workspace_key,app_user_id,created_at)`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) ApplicationByUser(ctx context.Context, id string) (domain.ApplicationInstallation, error) {
	var a domain.ApplicationInstallation
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM application_installations WHERE user_id=?`, id).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &a)
	}
	return a, err
}

func (s *SQLiteStore) ListApplications(ctx context.Context, workspace string) ([]domain.ApplicationInstallation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM application_installations WHERE workspace_key=? ORDER BY id`, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ApplicationInstallation{}
	for rows.Next() {
		var raw []byte
		var a domain.ApplicationInstallation
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Installation creates a non-login principal and its memberships in the same
// transaction. Public teams are not implicitly granted to applications.
func (s *SQLiteStore) installApplicationTx(ctx context.Context, tx *sqlTx, lock string, current domain.Bootstrap, a domain.ApplicationInstallation, secret string) (domain.Bootstrap, domain.ApplicationInstallation, error) {
	var role, status string
	if err := tx.QueryRowContext(ctx, `SELECT role,status FROM workspace_memberships WHERE workspace_id=? AND user_id=?`+lock, current.Workspace.ID, a.InstalledBy).Scan(&role, &status); err != nil || status != "active" || !isWorkspaceAdminRole(role) {
		return current, a, ErrAuthForbidden
	}
	a.WorkspaceKey = current.Workspace.URLKey
	if a.ClientID == "" || a.Name == "" || len(a.Name) > 200 || len(a.TeamIDs) == 0 {
		return current, a, ErrIssueQuery
	}
	for _, id := range a.TeamIDs {
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='teams' AND record_key=?`, a.WorkspaceKey, id).Scan(&raw); err != nil {
			return current, a, ErrAuthForbidden
		}
	}
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT data FROM application_installations WHERE workspace_key=? AND client_id=?`+lock, a.WorkspaceKey, a.ClientID).Scan(&raw)
	now := time.Now().UTC()
	a.UpdatedAt = now
	if err == nil {
		var old domain.ApplicationInstallation
		if err := json.Unmarshal(raw, &old); err != nil {
			return current, a, err
		}
		a.ID, a.UserID, a.CreatedAt = old.ID, old.UserID, old.CreatedAt
		if secret == "" {
			if err := tx.QueryRowContext(ctx, `SELECT webhook_secret FROM application_installations WHERE id=?`, a.ID).Scan(&secret); err != nil {
				return current, a, err
			}
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(a.WorkspaceKey+":"+a.ClientID)))[:32]
		a.ID = "installation_" + hash
		a.UserID = "app_" + hash
		a.CreatedAt = now
		_, err = tx.ExecContext(ctx, `INSERT INTO auth_users(id,email,name,display_name,avatar_url,password_hash,active,created_at,updated_at) VALUES(?,?,?,?,?,'',1,?,?)`, a.UserID, nil, a.Name, a.Name, a.AvatarURL, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
		if err != nil {
			return current, a, err
		}
	} else {
		return current, a, err
	}
	a.TeamIDs = slices.Compact(slices.Sorted(slices.Values(a.TeamIDs)))
	a.Scopes = slices.Compact(slices.Sorted(slices.Values(a.Scopes)))
	for _, scope := range a.Scopes {
		if !slices.Contains([]string{"read", "write", "app:mentionable", "app:assignable"}, scope) {
			return current, a, ErrAuthForbidden
		}
	}
	raw, err = json.Marshal(a)
	if err != nil {
		return current, a, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO application_installations(id,workspace_key,client_id,user_id,data,webhook_secret) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data,webhook_secret=excluded.webhook_secret`, a.ID, a.WorkspaceKey, a.ClientID, a.UserID, raw, secret); err != nil {
		return current, a, err
	}
	memberStatus := "active"
	active := 1
	if !a.Active {
		memberStatus = "suspended"
		active = 0
	}
	if _, err = tx.ExecContext(ctx, `UPDATE auth_users SET name=?,display_name=?,avatar_url=?,active=?,updated_at=? WHERE id=?`, a.Name, a.Name, a.AvatarURL, active, now.Format(time.RFC3339Nano), a.UserID); err != nil {
		return current, a, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at,last_seen_at) VALUES(?,?,'app',?,?,?) ON CONFLICT(workspace_id,user_id) DO UPDATE SET role='app',status=excluded.status`, current.Workspace.ID, a.UserID, memberStatus, a.CreatedAt.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		return current, a, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM team_memberships WHERE workspace_id=? AND user_id=?`, current.Workspace.ID, a.UserID); err != nil {
		return current, a, err
	}
	if a.Active {
		for _, team := range a.TeamIDs {
			if _, err = tx.ExecContext(ctx, `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) VALUES(?,?,?,'member',?)`, current.Workspace.ID, team, a.UserID, now.Format(time.RFC3339Nano)); err != nil {
				return current, a, err
			}
		}
	}
	user := a.User()
	userRaw, _ := json.Marshal(user)
	if _, err = tx.ExecContext(ctx, `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'users',?,0,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET data=excluded.data`, a.WorkspaceKey, a.UserID, userRaw); err != nil {
		return current, a, err
	}
	if err = ensureOAuthMetadataShape(ctx, tx, a.WorkspaceKey, "users", "array", lock); err != nil {
		return current, a, err
	}
	current.Users = slices.Clone(current.Users)
	idx := slices.IndexFunc(current.Users, func(u domain.User) bool { return u.ID == user.ID })
	if idx < 0 {
		current.Users = append(current.Users, user)
	} else {
		current.Users[idx] = user
	}
	return current, a, nil
}

func (s *SQLiteStore) InstallApplication(ctx context.Context, workspace string, a domain.ApplicationInstallation, secret string) (domain.ApplicationInstallation, error) {
	var event domain.DomainEvent
	err := s.oauthMetadataTransaction(ctx, workspace, func(tx *sqlTx, lock string, current domain.Bootstrap) (domain.Bootstrap, error) {
		next, installed, err := s.installApplicationTx(ctx, tx, lock, current, a, secret)
		if err != nil {
			return current, err
		}
		a = installed
		event, err = oauthRecordEvent(ctx, tx, "application.updated", a.ID)
		return next, err
	})
	if err == nil {
		s.publishOAuthRecordEvent(workspace, event)
	}
	return a, err
}

func (s *SQLiteStore) decorateAppUser(ctx context.Context, u *domain.User) error {
	if !strings.HasPrefix(u.ID, "app_") {
		return nil
	}
	a, err := s.ApplicationByUser(ctx, u.ID)
	if err != nil {
		return err
	}
	*u = a.User()
	return nil
}

func (s *SQLiteStore) oauthClientInTransaction(ctx context.Context, tx *sqlTx, id string) (domain.OAuthClient, error) {
	var client domain.OAuthClient
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT data FROM oauth_clients WHERE client_id=?`, id).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &client)
		return client, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return client, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT data FROM workspace_metadata_records WHERE field='oauthApplications'`)
	if err != nil {
		return client, err
	}
	defer rows.Close()
	for rows.Next() {
		var a domain.OAuthApplication
		if err := rows.Scan(&raw); err != nil {
			return client, err
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return client, err
		}
		if a.ClientID == id {
			return domain.OAuthClient{ClientID: id, ClientName: a.Name, RedirectURIs: a.RedirectURIs}, nil
		}
	}
	return client, ErrAuthForbidden
}
