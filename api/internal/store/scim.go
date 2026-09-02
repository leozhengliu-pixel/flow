package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// SCIMToken is the non-secret representation returned by token listing APIs.
// Secret is populated only by CreateSCIMToken and is never persisted.
type SCIMToken struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspaceId"`
	Name        string     `json:"name"`
	Secret      string     `json:"secret,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	LastUsedAt  *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt   *time.Time `json:"revokedAt,omitempty"`
}

type SCIMUser struct {
	User       domain.User
	ExternalID string
	UserName   string
}

func (s *SQLiteStore) ensureSCIMSchema(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS scim_tokens (id VARCHAR(191) PRIMARY KEY, workspace_id VARCHAR(191) NOT NULL, token_hash VARCHAR(191) NOT NULL UNIQUE, name VARCHAR(191) NOT NULL, created_at VARCHAR(40) NOT NULL, last_used_at VARCHAR(40), revoked_at VARCHAR(40))`); err != nil {
		return err
	}
	index := `CREATE INDEX IF NOT EXISTS scim_tokens_workspace_idx ON scim_tokens(workspace_id,revoked_at)`
	if s.dialect == "mysql" {
		index = `CREATE INDEX scim_tokens_workspace_idx ON scim_tokens(workspace_id,revoked_at)`
	}
	if _, err := s.db.ExecContext(ctx, index); err != nil && !(s.dialect == "mysql" && strings.Contains(strings.ToLower(err.Error()), "duplicate")) {
		return err
	}
	return nil
}

func (s *SQLiteStore) MustWorkspaceID(workspaceKey string) string {
	if data, ok := s.BootstrapFor(strings.TrimSpace(workspaceKey)); ok {
		return data.Workspace.ID
	}
	return ""
}

func (s *SQLiteStore) CreateSCIMToken(ctx context.Context, workspaceID, name string) (SCIMToken, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "SCIM token"
	}
	secret, err := randomToken()
	if err != nil {
		return SCIMToken{}, err
	}
	now := time.Now().UTC()
	item := SCIMToken{ID: fmt.Sprintf("scim_%d", now.UnixNano()), WorkspaceID: workspaceID, Name: name, Secret: secret, CreatedAt: now}
	_, err = s.db.ExecContext(ctx, `INSERT INTO scim_tokens(id,workspace_id,token_hash,name,created_at) VALUES(?,?,?,?,?)`, item.ID, workspaceID, tokenHash(secret), name, now.Format(time.RFC3339Nano))
	return item, err
}

func (s *SQLiteStore) ListSCIMTokens(ctx context.Context, workspaceID string) ([]SCIMToken, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,created_at,last_used_at,revoked_at FROM scim_tokens WHERE workspace_id=? ORDER BY created_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []SCIMToken{}
	for rows.Next() {
		var item SCIMToken
		var created string
		var last, revoked sql.NullString
		item.WorkspaceID = workspaceID
		if err := rows.Scan(&item.ID, &item.Name, &created, &last, &revoked); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		if last.Valid {
			value, _ := time.Parse(time.RFC3339Nano, last.String)
			item.LastUsedAt = &value
		}
		if revoked.Valid {
			value, _ := time.Parse(time.RFC3339Nano, revoked.String)
			item.RevokedAt = &value
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) RevokeSCIMToken(ctx context.Context, workspaceID, tokenID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE scim_tokens SET revoked_at=? WHERE workspace_id=? AND id=? AND revoked_at IS NULL`, time.Now().UTC().Format(time.RFC3339Nano), workspaceID, tokenID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// AuthenticateSCIMToken validates an opaque bearer token and returns the
// workspace it belongs to. Revoked tokens are rejected and successful use is
// recorded without exposing the hash to callers.
func (s *SQLiteStore) AuthenticateSCIMToken(ctx context.Context, secret string) (string, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", ErrAuthInvalid
	}
	var workspaceID string
	err := s.db.QueryRowContext(ctx, `SELECT workspace_id FROM scim_tokens WHERE token_hash=? AND revoked_at IS NULL`, tokenHash(secret)).Scan(&workspaceID)
	if err != nil {
		return "", ErrAuthInvalid
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE scim_tokens SET last_used_at=? WHERE token_hash=? AND revoked_at IS NULL`, time.Now().UTC().Format(time.RFC3339Nano), tokenHash(secret))
	return workspaceID, nil
}

func (s *SQLiteStore) ListSCIMUsers(ctx context.Context, workspaceID string, offset, limit int, filter string) ([]SCIMUser, int, error) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	pattern := "%"
	if strings.TrimSpace(filter) != "" {
		pattern = "%" + strings.ToLower(strings.TrimSpace(filter)) + "%"
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_identities i JOIN workspace_memberships m ON m.user_id=i.user_id JOIN auth_users u ON u.id=i.user_id WHERE i.provider='scim' AND i.issuer=? AND m.workspace_id=? AND (lower(i.username) LIKE ? OR lower(COALESCE(u.email,'')) LIKE ? OR lower(u.display_name) LIKE ?)`, workspaceID, workspaceID, pattern, pattern, pattern).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT i.subject,i.username,u.id,u.email,u.name,u.display_name,u.avatar_url,u.email_verified_at,u.active FROM auth_identities i JOIN workspace_memberships m ON m.user_id=i.user_id JOIN auth_users u ON u.id=i.user_id WHERE i.provider='scim' AND i.issuer=? AND m.workspace_id=? AND (lower(i.username) LIKE ? OR lower(COALESCE(u.email,'')) LIKE ? OR lower(u.display_name) LIKE ?) ORDER BY lower(i.username),i.subject LIMIT ? OFFSET ?`, workspaceID, workspaceID, pattern, pattern, pattern, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	result := []SCIMUser{}
	for rows.Next() {
		var item SCIMUser
		var email, verified sql.NullString
		var active int
		if err := rows.Scan(&item.ExternalID, &item.UserName, &item.User.ID, &email, &item.User.Name, &item.User.DisplayName, &item.User.AvatarURL, &verified, &active); err != nil {
			return nil, 0, err
		}
		item.User.Email = email.String
		item.User.Active, item.User.EmailVerified = active == 1, verified.Valid
		result = append(result, item)
	}
	return result, total, rows.Err()
}

func (s *SQLiteStore) SCIMUser(ctx context.Context, workspaceID, externalID string) (SCIMUser, error) {
	var item SCIMUser
	var email, verified sql.NullString
	var active int
	err := s.db.QueryRowContext(ctx, `SELECT i.subject,i.username,u.id,u.email,u.name,u.display_name,u.avatar_url,u.email_verified_at,u.active FROM auth_identities i JOIN workspace_memberships m ON m.user_id=i.user_id JOIN auth_users u ON u.id=i.user_id WHERE i.provider='scim' AND i.issuer=? AND m.workspace_id=? AND (i.subject=? OR u.id=?)`, workspaceID, workspaceID, externalID, externalID).Scan(&item.ExternalID, &item.UserName, &item.User.ID, &email, &item.User.Name, &item.User.DisplayName, &item.User.AvatarURL, &verified, &active)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SCIMUser{}, ErrAuthForbidden
		}
		return SCIMUser{}, err
	}
	item.User.Email, item.User.Active, item.User.EmailVerified = email.String, active == 1, verified.Valid
	return item, nil
}

func (s *SQLiteStore) ProvisionSCIMUser(ctx context.Context, workspaceID, externalID, username, displayName, email, avatarURL, role string, active bool) (SCIMUser, error) {
	externalID, username, displayName, email = strings.TrimSpace(externalID), strings.TrimSpace(username), strings.TrimSpace(displayName), normalizeEmail(email)
	if externalID == "" {
		externalID = username
	}
	if externalID == "" || username == "" {
		return SCIMUser{}, errors.New("scim externalId and userName are required")
	}
	if displayName == "" {
		displayName = username
	}
	now := time.Now().UTC()
	var userID string
	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM auth_identities WHERE provider='scim' AND issuer=? AND subject=?`, workspaceID, externalID).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		userID = fmt.Sprintf("usr_%d", now.UnixNano())
		_, err = s.db.ExecContext(ctx, `INSERT INTO auth_users(id,email,name,display_name,avatar_url,password_hash,email_verified_at,active,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, userID, nullableString(email), username, displayName, strings.TrimSpace(avatarURL), "", nullableTime(active, now), boolInt(active), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
		if err != nil {
			return SCIMUser{}, err
		}
		_, err = s.db.ExecContext(ctx, `INSERT INTO auth_identities(id,user_id,provider,issuer,subject,identity_key,username,claims_json,created_at,last_login_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, fmt.Sprintf("identity_%d", now.UnixNano()), userID, "scim", workspaceID, externalID, externalIdentityKey("scim", workspaceID, externalID), username, "{}", now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	} else if err == nil {
		_, err = s.db.ExecContext(ctx, `UPDATE auth_users SET email=?,name=?,display_name=?,avatar_url=?,active=?,updated_at=? WHERE id=?`, nullableString(email), username, displayName, strings.TrimSpace(avatarURL), boolInt(active), now.Format(time.RFC3339Nano), userID)
		if err == nil {
			_, err = s.db.ExecContext(ctx, `UPDATE auth_identities SET username=? WHERE provider='scim' AND issuer=? AND subject=?`, username, workspaceID, externalID)
		}
	} else {
		return SCIMUser{}, err
	}
	if err != nil {
		return SCIMUser{}, err
	}
	status := "suspended"
	if active {
		status = "active"
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at,last_seen_at) VALUES(?,?,?,?,?,?) ON CONFLICT(workspace_id,user_id) DO UPDATE SET role=excluded.role,status=excluded.status,last_seen_at=excluded.last_seen_at`, workspaceID, userID, role, status, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return SCIMUser{}, err
	}
	return s.SCIMUser(ctx, workspaceID, externalID)
}

func (s *SQLiteStore) DeprovisionSCIMUser(ctx context.Context, workspaceID, externalID string) error {
	item, err := s.SCIMUser(ctx, workspaceID, externalID)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE auth_users SET active=0,updated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), item.User.ID)
	if err == nil {
		_, err = s.db.ExecContext(ctx, `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, workspaceID, item.User.ID)
	}
	return err
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableTime(active bool, now time.Time) any {
	if active {
		return now.Format(time.RFC3339Nano)
	}
	return nil
}
