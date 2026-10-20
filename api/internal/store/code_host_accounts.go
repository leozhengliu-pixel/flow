package store

// Members' personal GitHub and GitLab accounts (Settings › Account ›
// Connected accounts). A row holds only the identity; the OAuth or personal
// access tokens are sealed in connector_secrets under CodeHostAccountSecretID
// (AES-256-GCM, FLOW_CONNECTOR_SECRET_KEY, bound to the secret id), so a
// database dump alone never reveals a token. One account per user, code host
// and host: a GitLab account is keyed by its instance's base URL.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// ErrCodeHostAccountNotFound is returned when the user has no such account.
var ErrCodeHostAccountNotFound = errors.New("code host account not found")

// CodeHostAccountSecretID is the connector secret holding an account's tokens.
func CodeHostAccountSecretID(accountID string) string { return "code-host-account:" + accountID }

func (s *SQLiteStore) createCodeHostAccountsSchema(ctx context.Context) error {
	id, text := "TEXT", "TEXT"
	if s.dialect == "mysql" {
		id = "VARCHAR(191)"
	}
	prefix := "CREATE INDEX IF NOT EXISTS "
	unique := "CREATE UNIQUE INDEX IF NOT EXISTS "
	if s.dialect == "mysql" {
		prefix, unique = "CREATE INDEX ", "CREATE UNIQUE INDEX "
	}
	return s.applyMigrationStatements(ctx, []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS code_host_accounts (id %[1]s PRIMARY KEY, user_id %[1]s NOT NULL, code_host VARCHAR(16) NOT NULL, host_name %[1]s NOT NULL, base_url VARCHAR(512) NOT NULL, external_user_id VARCHAR(64) NOT NULL, login VARCHAR(255) NOT NULL, email VARCHAR(320) NOT NULL DEFAULT '', auth_type VARCHAR(16) NOT NULL, scopes %[2]s NOT NULL, status VARCHAR(16) NOT NULL, status_reason %[2]s NOT NULL, workspace_key %[1]s NOT NULL DEFAULT '', connection_id %[1]s NOT NULL DEFAULT '', token_expires_at VARCHAR(40) NOT NULL DEFAULT '', linked_at VARCHAR(40) NOT NULL, updated_at VARCHAR(40) NOT NULL)`, id, text),
		unique + "code_host_accounts_user_host ON code_host_accounts(user_id,code_host,host_name)",
		prefix + "code_host_accounts_login ON code_host_accounts(code_host,host_name,login)",
	})
}

const codeHostAccountColumns = `id,user_id,code_host,host_name,base_url,external_user_id,login,email,auth_type,scopes,status,status_reason,workspace_key,connection_id,token_expires_at,linked_at,updated_at`

func scanCodeHostAccount(row interface{ Scan(...any) error }) (domain.CodeHostAccount, error) {
	var account domain.CodeHostAccount
	var scopes, expires, linked, updated string
	if err := row.Scan(&account.ID, &account.UserID, &account.CodeHost, &account.HostName, &account.BaseURL, &account.ExternalUserID, &account.Login, &account.Email, &account.AuthType, &scopes, &account.Status, &account.StatusReason, &account.WorkspaceKey, &account.ConnectionID, &expires, &linked, &updated); err != nil {
		return account, err
	}
	account.Scopes = []string{}
	for _, scope := range strings.Fields(scopes) {
		account.Scopes = append(account.Scopes, scope)
	}
	if expires != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, expires); err == nil {
			account.TokenExpiresAt = &parsed
		}
	}
	account.LinkedAt, _ = time.Parse(time.RFC3339Nano, linked)
	account.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return account, nil
}

func normalizeCodeHostAccount(account *domain.CodeHostAccount) error {
	account.CodeHost = strings.ToLower(strings.TrimSpace(account.CodeHost))
	account.HostName = strings.ToLower(strings.TrimSpace(account.HostName))
	if account.ID == "" || account.UserID == "" || account.HostName == "" || account.Login == "" || (account.CodeHost != domain.CodeHostGitHub && account.CodeHost != domain.CodeHostGitLab) {
		return errors.New("code host account: id, user, code host, host and login are required")
	}
	if len(account.ID) > 191 || len(account.UserID) > 191 || len(account.HostName) > 191 || len(account.BaseURL) > 512 || len(account.Login) > 255 {
		return errors.New("code host account: value too long")
	}
	if account.Status == "" {
		account.Status = domain.CodeHostAccountActive
	}
	return nil
}

func formatOptionalTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

// UpsertCodeHostAccount stores the account, replacing the user's previous
// account on the same code host and host (its id is kept). It returns the
// stored account.
func (s *SQLiteStore) UpsertCodeHostAccount(ctx context.Context, account domain.CodeHostAccount) (domain.CodeHostAccount, error) {
	if err := normalizeCodeHostAccount(&account); err != nil {
		return account, err
	}
	now := time.Now().UTC()
	if account.LinkedAt.IsZero() {
		account.LinkedAt = now
	}
	account.UpdatedAt = now
	if existing, err := s.CodeHostAccountFor(ctx, account.UserID, account.CodeHost, account.HostName); err == nil {
		account.ID = existing.ID
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO code_host_accounts(`+codeHostAccountColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET external_user_id=excluded.external_user_id,login=excluded.login,email=excluded.email,base_url=excluded.base_url,auth_type=excluded.auth_type,scopes=excluded.scopes,status=excluded.status,status_reason=excluded.status_reason,workspace_key=excluded.workspace_key,connection_id=excluded.connection_id,token_expires_at=excluded.token_expires_at,linked_at=excluded.linked_at,updated_at=excluded.updated_at`,
		account.ID, account.UserID, account.CodeHost, account.HostName, account.BaseURL, account.ExternalUserID, account.Login, account.Email, account.AuthType, strings.Join(account.Scopes, " "), account.Status, account.StatusReason, account.WorkspaceKey, account.ConnectionID, formatOptionalTime(account.TokenExpiresAt), account.LinkedAt.UTC().Format(time.RFC3339Nano), account.UpdatedAt.Format(time.RFC3339Nano))
	return account, err
}

// CodeHostAccountFor returns the user's account on a code host and host.
func (s *SQLiteStore) CodeHostAccountFor(ctx context.Context, userID, codeHost, hostName string) (domain.CodeHostAccount, error) {
	account, err := scanCodeHostAccount(s.db.QueryRowContext(ctx, `SELECT `+codeHostAccountColumns+` FROM code_host_accounts WHERE user_id=? AND code_host=? AND host_name=?`, userID, strings.ToLower(codeHost), strings.ToLower(strings.TrimSpace(hostName))))
	if errors.Is(err, sql.ErrNoRows) {
		return account, ErrCodeHostAccountNotFound
	}
	return account, err
}

// CodeHostAccount returns one of the user's accounts by id; another user's
// account is not found.
func (s *SQLiteStore) CodeHostAccount(ctx context.Context, userID, id string) (domain.CodeHostAccount, error) {
	account, err := scanCodeHostAccount(s.db.QueryRowContext(ctx, `SELECT `+codeHostAccountColumns+` FROM code_host_accounts WHERE id=? AND user_id=?`, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return account, ErrCodeHostAccountNotFound
	}
	return account, err
}

// ListCodeHostAccounts lists the user's accounts, GitHub first.
func (s *SQLiteStore) ListCodeHostAccounts(ctx context.Context, userID string) ([]domain.CodeHostAccount, error) {
	return s.queryCodeHostAccounts(ctx, `SELECT `+codeHostAccountColumns+` FROM code_host_accounts WHERE user_id=? ORDER BY code_host,host_name`, userID)
}

// CodeHostAccountsByLogin lists the accounts linked to host logins
// (case-insensitive), for attribution.
func (s *SQLiteStore) CodeHostAccountsByLogin(ctx context.Context, codeHost, hostName string, logins []string) ([]domain.CodeHostAccount, error) {
	values := []any{strings.ToLower(codeHost), strings.ToLower(strings.TrimSpace(hostName))}
	marks := []string{}
	for _, login := range logins {
		if login = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(login), "@")); login != "" && len(marks) < 200 {
			marks = append(marks, "?")
			values = append(values, login)
		}
	}
	if len(marks) == 0 {
		return []domain.CodeHostAccount{}, nil
	}
	return s.queryCodeHostAccounts(ctx, `SELECT `+codeHostAccountColumns+` FROM code_host_accounts WHERE code_host=? AND host_name=? AND LOWER(login) IN (`+strings.Join(marks, ",")+`) ORDER BY updated_at DESC`, values...)
}

func (s *SQLiteStore) queryCodeHostAccounts(ctx context.Context, query string, args ...any) ([]domain.CodeHostAccount, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := []domain.CodeHostAccount{}
	for rows.Next() {
		account, err := scanCodeHostAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

// addCodeHostAccountRefreshLease adds the refresh lease (migration 12).
// GitHub and GitLab rotate the refresh token on use, so two API replicas
// refreshing the same account at once would invalidate each other: the
// lease makes exactly one replica refresh while the others wait and re-read.
func (s *SQLiteStore) addCodeHostAccountRefreshLease(ctx context.Context) error {
	for _, statement := range []string{
		`ALTER TABLE code_host_accounts ADD COLUMN refresh_lease_owner VARCHAR(64) NOT NULL DEFAULT ''`,
		`ALTER TABLE code_host_accounts ADD COLUMN refresh_lease_until BIGINT NOT NULL DEFAULT 0`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			message := strings.ToLower(err.Error())
			if strings.Contains(message, "duplicate column") || strings.Contains(message, "already exists") {
				continue
			}
			return err
		}
	}
	return nil
}

// ClaimCodeHostAccountRefresh takes the account's refresh lease for owner
// until now+lease when nobody holds an unexpired one (or owner already
// does). The claim is one conditional UPDATE, a compare-and-swap that the
// database serializes on the row (SQLite, MySQL and Postgres alike), so
// across all API replicas at most one owner holds the lease at a time. It
// reports whether owner holds the lease.
func (s *SQLiteStore) ClaimCodeHostAccountRefresh(ctx context.Context, id, owner string, lease time.Duration) (bool, error) {
	if id == "" || owner == "" || len(owner) > 64 {
		return false, errors.New("code host account refresh lease: id and owner are required")
	}
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `UPDATE code_host_accounts SET refresh_lease_owner=?,refresh_lease_until=? WHERE id=? AND (refresh_lease_until<? OR refresh_lease_owner=?)`,
		owner, now.Add(lease).UnixMilli(), id, now.UnixMilli(), owner)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return count == 1, nil
}

// ReleaseCodeHostAccountRefresh gives up owner's refresh lease (a lease that
// another owner took over after it expired is left alone).
func (s *SQLiteStore) ReleaseCodeHostAccountRefresh(ctx context.Context, id, owner string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE code_host_accounts SET refresh_lease_owner='',refresh_lease_until=0 WHERE id=? AND refresh_lease_owner=?`, id, owner)
	return err
}

// SetCodeHostAccountStatus marks an account active or as needing a
// reconnect (revoked or expired token), with a reason.
func (s *SQLiteStore) SetCodeHostAccountStatus(ctx context.Context, id, status, reason string, expiresAt *time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE code_host_accounts SET status=?,status_reason=?,token_expires_at=?,updated_at=? WHERE id=?`, status, reason, formatOptionalTime(expiresAt), time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// DeleteCodeHostAccount removes one of the user's accounts and its sealed
// tokens. It reports whether an account was removed.
func (s *SQLiteStore) DeleteCodeHostAccount(ctx context.Context, userID, id string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM code_host_accounts WHERE id=? AND user_id=?`, id, userID)
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return false, nil
	}
	return true, s.DeleteConnectorSecret(ctx, CodeHostAccountSecretID(id))
}
