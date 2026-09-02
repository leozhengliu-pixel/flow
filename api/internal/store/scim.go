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

// SCIMGroup is a provisioned SCIM group. Role is set when the group display
// name matches a configured SCIM role group; ordinary groups are retained for
// directory interoperability but never create Flow teams.
type SCIMGroup struct {
	ID          string
	WorkspaceID string
	ExternalID  string
	DisplayName string
	Role        string
	Members     []SCIMGroupMember
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type SCIMGroupMember struct {
	UserID     string
	ExternalID string
	UpdatedAt  time.Time
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
	// Groups and role assignments are deliberately kept outside workspace JSON:
	// SCIM pushes can be large and need indexed, per-member timestamps so role
	// precedence remains deterministic across restarts.
	groupSchema := []string{
		`CREATE TABLE IF NOT EXISTS scim_groups (id VARCHAR(191) PRIMARY KEY, workspace_id VARCHAR(191) NOT NULL, external_id VARCHAR(320) NOT NULL DEFAULT '', display_name VARCHAR(320) NOT NULL, role VARCHAR(32) NOT NULL DEFAULT '', created_at VARCHAR(40) NOT NULL, updated_at VARCHAR(40) NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS scim_group_memberships (workspace_id VARCHAR(191) NOT NULL, group_id VARCHAR(191) NOT NULL, user_id VARCHAR(191) NOT NULL, updated_at VARCHAR(40) NOT NULL, PRIMARY KEY(group_id,user_id))`,
		`CREATE TABLE IF NOT EXISTS scim_user_roles (workspace_id VARCHAR(191) NOT NULL, user_id VARCHAR(191) NOT NULL, role VARCHAR(32) NOT NULL, updated_at VARCHAR(40) NOT NULL, PRIMARY KEY(workspace_id,user_id))`,
	}
	for _, statement := range groupSchema {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	groupIndexes := []string{
		"scim_groups_workspace_idx ON scim_groups(workspace_id,display_name)",
		"scim_group_memberships_user_idx ON scim_group_memberships(workspace_id,user_id,updated_at)",
		"scim_group_memberships_group_idx ON scim_group_memberships(group_id,updated_at)",
		"scim_user_roles_workspace_idx ON scim_user_roles(workspace_id,updated_at)",
	}
	for _, index := range groupIndexes {
		prefix := "CREATE INDEX IF NOT EXISTS "
		if s.dialect == "mysql" {
			prefix = "CREATE INDEX "
		}
		if _, err := s.db.ExecContext(ctx, prefix+index); err != nil && !(s.dialect == "mysql" && strings.Contains(strings.ToLower(err.Error()), "duplicate")) {
			return err
		}
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
	if err := s.setSCIMUserRole(ctx, workspaceID, userID, role, now); err != nil {
		return SCIMUser{}, err
	}
	if err := s.recomputeSCIMUserRole(ctx, workspaceID, userID); err != nil {
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

func (s *SQLiteStore) setSCIMUserRole(ctx context.Context, workspaceID, userID, role string, at time.Time) error {
	role = strings.ToLower(strings.TrimSpace(role))
	if !validSCIMRole(role) {
		return errors.New("invalid SCIM role")
	}
	stamp := at.UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx, `UPDATE scim_user_roles SET role=?,updated_at=? WHERE workspace_id=? AND user_id=?`, role, stamp, workspaceID, userID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		_, err = s.db.ExecContext(ctx, `INSERT INTO scim_user_roles(workspace_id,user_id,role,updated_at) VALUES(?,?,?,?)`, workspaceID, userID, role, stamp)
	}
	return err
}

func validSCIMRole(role string) bool {
	role = strings.ToLower(strings.TrimSpace(role))
	return role == "owner" || role == "admin" || role == "member" || role == "guest"
}

// CreateSCIMGroup persists a group pushed by an IdP. Groups never create Flow
// teams; role is assigned by the caller from the workspace role-group mapping.
func (s *SQLiteStore) CreateSCIMGroup(ctx context.Context, workspaceID, externalID, displayName, role string) (SCIMGroup, error) {
	externalID, displayName, role = strings.TrimSpace(externalID), strings.TrimSpace(displayName), strings.ToLower(strings.TrimSpace(role))
	if displayName == "" {
		return SCIMGroup{}, errors.New("SCIM group displayName is required")
	}
	if role != "" && !validSCIMRole(role) {
		return SCIMGroup{}, errors.New("invalid SCIM group role")
	}
	now := time.Now().UTC()
	id := fmt.Sprintf("scimg_%d", now.UnixNano())
	_, err := s.db.ExecContext(ctx, `INSERT INTO scim_groups(id,workspace_id,external_id,display_name,role,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, id, workspaceID, externalID, displayName, role, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return SCIMGroup{}, err
	}
	return s.SCIMGroup(ctx, workspaceID, id)
}

func (s *SQLiteStore) SCIMGroup(ctx context.Context, workspaceID, id string) (SCIMGroup, error) {
	var group SCIMGroup
	var created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id,workspace_id,external_id,display_name,role,created_at,updated_at FROM scim_groups WHERE workspace_id=? AND (id=? OR external_id=?)`, workspaceID, id, id).Scan(&group.ID, &group.WorkspaceID, &group.ExternalID, &group.DisplayName, &group.Role, &created, &updated)
	if err != nil {
		return SCIMGroup{}, err
	}
	group.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	group.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	rows, err := s.db.QueryContext(ctx, `SELECT m.user_id,COALESCE(i.subject,''),m.updated_at FROM scim_group_memberships m LEFT JOIN auth_identities i ON i.user_id=m.user_id AND i.provider='scim' AND i.issuer=? WHERE m.workspace_id=? AND m.group_id=? ORDER BY m.updated_at,m.user_id`, workspaceID, workspaceID, group.ID)
	if err != nil {
		return SCIMGroup{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var member SCIMGroupMember
		var stamp string
		if err := rows.Scan(&member.UserID, &member.ExternalID, &stamp); err != nil {
			return SCIMGroup{}, err
		}
		member.UpdatedAt, _ = time.Parse(time.RFC3339Nano, stamp)
		group.Members = append(group.Members, member)
	}
	return group, rows.Err()
}

func (s *SQLiteStore) ListSCIMGroups(ctx context.Context, workspaceID string) ([]SCIMGroup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,workspace_id,external_id,display_name,role,created_at,updated_at FROM scim_groups WHERE workspace_id=? ORDER BY lower(display_name),id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []SCIMGroup{}
	for rows.Next() {
		var group SCIMGroup
		var created, updated string
		if err := rows.Scan(&group.ID, &group.WorkspaceID, &group.ExternalID, &group.DisplayName, &group.Role, &created, &updated); err != nil {
			return nil, err
		}
		group.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		group.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		loaded, err := s.SCIMGroup(ctx, workspaceID, group.ID)
		if err != nil {
			return nil, err
		}
		groups = append(groups, loaded)
	}
	return groups, rows.Err()
}

func (s *SQLiteStore) UpdateSCIMGroup(ctx context.Context, workspaceID, id, externalID, displayName, role string) (SCIMGroup, error) {
	group, err := s.SCIMGroup(ctx, workspaceID, id)
	if err != nil {
		return SCIMGroup{}, err
	}
	if strings.TrimSpace(externalID) != "" {
		group.ExternalID = strings.TrimSpace(externalID)
	}
	if strings.TrimSpace(displayName) != "" {
		group.DisplayName = strings.TrimSpace(displayName)
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if role != "" {
		if !validSCIMRole(role) {
			return SCIMGroup{}, errors.New("invalid SCIM group role")
		}
		group.Role = role
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `UPDATE scim_groups SET external_id=?,display_name=?,role=?,updated_at=? WHERE workspace_id=? AND id=?`, group.ExternalID, group.DisplayName, group.Role, now.Format(time.RFC3339Nano), workspaceID, group.ID)
	if err != nil {
		return SCIMGroup{}, err
	}
	for _, member := range group.Members {
		if err := s.recomputeSCIMUserRole(ctx, workspaceID, member.UserID); err != nil {
			return SCIMGroup{}, err
		}
	}
	return s.SCIMGroup(ctx, workspaceID, group.ID)
}

func (s *SQLiteStore) DeleteSCIMGroup(ctx context.Context, workspaceID, id string) error {
	group, err := s.SCIMGroup(ctx, workspaceID, id)
	if err != nil {
		return err
	}
	for _, member := range group.Members {
		if err := s.recomputeSCIMUserRole(ctx, workspaceID, member.UserID); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM scim_group_memberships WHERE workspace_id=? AND group_id=?`, workspaceID, group.ID); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM scim_groups WHERE workspace_id=? AND id=?`, workspaceID, group.ID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ReplaceSCIMGroupMembers applies one complete IdP group push and updates all
// affected users' effective workspace roles. A single timestamp is used for
// the push so membership precedence is deterministic.
func (s *SQLiteStore) ReplaceSCIMGroupMembers(ctx context.Context, workspaceID, id string, userIDs []string) (SCIMGroup, error) {
	group, err := s.SCIMGroup(ctx, workspaceID, id)
	if err != nil {
		return SCIMGroup{}, err
	}
	affected := map[string]struct{}{}
	for _, member := range group.Members {
		affected[member.UserID] = struct{}{}
	}
	for _, userID := range userIDs {
		if strings.TrimSpace(userID) != "" {
			affected[userID] = struct{}{}
		}
	}
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SCIMGroup{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM scim_group_memberships WHERE workspace_id=? AND group_id=?`, workspaceID, group.ID); err != nil {
		_ = tx.Rollback()
		return SCIMGroup{}, err
	}
	for _, userID := range userIDs {
		if strings.TrimSpace(userID) == "" {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO scim_group_memberships(workspace_id,group_id,user_id,updated_at) VALUES(?,?,?,?)`, workspaceID, group.ID, userID, stamp); err != nil {
			_ = tx.Rollback()
			return SCIMGroup{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE scim_groups SET updated_at=? WHERE workspace_id=? AND id=?`, stamp, workspaceID, group.ID); err != nil {
		_ = tx.Rollback()
		return SCIMGroup{}, err
	}
	if err = tx.Commit(); err != nil {
		return SCIMGroup{}, err
	}
	for userID := range affected {
		if err := s.recomputeSCIMUserRole(ctx, workspaceID, userID); err != nil {
			return SCIMGroup{}, err
		}
	}
	return s.SCIMGroup(ctx, workspaceID, group.ID)
}

func (s *SQLiteStore) recomputeSCIMUserRole(ctx context.Context, workspaceID, userID string) error {
	var role string
	err := s.db.QueryRowContext(ctx, `SELECT g.role FROM scim_group_memberships m JOIN scim_groups g ON g.id=m.group_id AND g.workspace_id=m.workspace_id WHERE m.workspace_id=? AND m.user_id=? AND g.role<>'' ORDER BY m.updated_at DESC,g.updated_at DESC,g.id DESC LIMIT 1`, workspaceID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		err = s.db.QueryRowContext(ctx, `SELECT role FROM scim_user_roles WHERE workspace_id=? AND user_id=?`, workspaceID, userID).Scan(&role)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE workspace_memberships SET role=? WHERE workspace_id=? AND user_id=?`, role, workspaceID, userID)
	return err
}
