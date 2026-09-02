package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"flow/api/internal/domain"
)

var (
	ErrAuthInvalid   = errors.New("invalid email or password")
	ErrAuthExpired   = errors.New("token is invalid or expired")
	ErrAuthConflict  = errors.New("account already exists")
	ErrAuthForbidden = errors.New("forbidden")
	ErrLastAdmin     = errors.New("a workspace needs at least one admin")
	ErrLastTeamOwner = errors.New("a team needs at least one owner")
)

type actorContextKey struct{}
type realtimeClientContextKey struct{}

func ContextWithActor(ctx context.Context, user domain.User) context.Context {
	return context.WithValue(ctx, actorContextKey{}, user)
}

func ContextWithRealtimeClient(ctx context.Context, clientID string) context.Context {
	return context.WithValue(ctx, realtimeClientContextKey{}, strings.TrimSpace(clientID))
}

func realtimeClientFromContext(ctx context.Context) string {
	value, _ := ctx.Value(realtimeClientContextKey{}).(string)
	return value
}

func actorFromContext(ctx context.Context) (domain.User, bool) {
	user, ok := ctx.Value(actorContextKey{}).(domain.User)
	return user, ok && user.ID != ""
}

func (s *SQLiteStore) ensureAuthTestFixture(ctx context.Context) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_users`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	password := s.fixturePassword
	if password == "" {
		return errors.New("test fixture password is required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, data := range s.workspaces {
		for _, user := range data.Users {
			_, err = tx.ExecContext(ctx, `INSERT INTO auth_users(id,email,name,display_name,avatar_url,password_hash,email_verified_at,active,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`,
				user.ID, normalizeEmail(user.Email), user.Name, user.DisplayName, user.AvatarURL, string(hash), now.Format(time.RFC3339Nano), boolInt(user.Active), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at,last_seen_at) VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING`,
				data.Workspace.ID, user.ID, "admin", "active", now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
			if err != nil {
				return err
			}
			teamRole := "member"
			if user.ID == data.Viewer.ID {
				teamRole = "owner"
			}
			for _, team := range data.Teams {
				_, err = tx.ExecContext(ctx, `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING`, data.Workspace.ID, team.ID, user.ID, teamRole, now.Format(time.RFC3339Nano))
				if err != nil {
					return err
				}
			}
		}
	}
	return tx.Commit()
}

// ensureSeedWorkspaceOwners covers workspaces created from an empty install:
// SQL migrations run before the seed snapshot exists, so the first creator is
// promoted here after the workspace state has been loaded.
func (s *SQLiteStore) ensureSeedWorkspaceOwners(ctx context.Context) error {
	for _, data := range s.workspaces {
		var ownerCount int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_memberships WHERE workspace_id=? AND role='owner'`, data.Workspace.ID).Scan(&ownerCount); err != nil {
			return err
		}
		if ownerCount > 0 {
			continue
		}
		var userID string
		if err := s.db.QueryRowContext(ctx, `SELECT user_id FROM workspace_memberships WHERE workspace_id=? AND role IN ('admin','member') AND status='active' ORDER BY joined_at,user_id LIMIT 1`, data.Workspace.ID).Scan(&userID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE workspace_memberships SET role='owner' WHERE workspace_id=? AND user_id=?`, data.Workspace.ID, userID); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) Register(ctx context.Context, name, email, password string) (domain.User, string, error) {
	email = normalizeEmail(email)
	name = strings.TrimSpace(name)
	if name == "" || !strings.Contains(email, "@") || len(password) < 8 {
		return domain.User{}, "", fmt.Errorf("invalid registration")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return domain.User{}, "", err
	}
	now := time.Now().UTC()
	user := domain.User{ID: fmt.Sprintf("usr_%d", now.UnixNano()), Name: name, DisplayName: name, Email: email, Active: true}
	_, err = s.db.ExecContext(ctx, `INSERT INTO auth_users(id,email,name,display_name,password_hash,active,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		user.ID, user.Email, user.Name, user.DisplayName, string(hash), 1, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return domain.User{}, "", ErrAuthConflict
		}
		return domain.User{}, "", err
	}
	token, err := s.createAuthToken(ctx, user.ID, "verify", 24*time.Hour)
	return user, token, err
}

func (s *SQLiteStore) Login(ctx context.Context, email, password string) (domain.AuthSession, string, error) {
	user, hash, err := s.authUserByEmail(ctx, email)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil || !user.Active {
		return domain.AuthSession{}, "", ErrAuthInvalid
	}
	if !user.EmailVerified {
		return domain.AuthSession{}, "", errors.New("email is not verified")
	}
	return s.createSession(ctx, user)
}

func (s *SQLiteStore) LoginExternal(ctx context.Context, email, name, avatarURL string, autoProvision bool) (domain.AuthSession, string, error) {
	email = normalizeEmail(email)
	name = strings.TrimSpace(name)
	if email == "" || !strings.Contains(email, "@") {
		return domain.AuthSession{}, "", ErrAuthInvalid
	}
	if name == "" {
		name = strings.Split(email, "@")[0]
	}
	user, _, err := s.authUserByEmail(ctx, email)
	now := time.Now().UTC()
	if errors.Is(err, sql.ErrNoRows) {
		if !autoProvision {
			return domain.AuthSession{}, "", ErrAuthInvalid
		}
		user = domain.User{ID: fmt.Sprintf("usr_%d", now.UnixNano()), Name: name, DisplayName: name, Email: email, AvatarURL: strings.TrimSpace(avatarURL), Active: true, EmailVerified: true}
		_, err = s.db.ExecContext(ctx, `INSERT INTO auth_users(id,email,name,display_name,avatar_url,password_hash,email_verified_at,active,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, user.ID, user.Email, user.Name, user.DisplayName, user.AvatarURL, "", now.Format(time.RFC3339Nano), 1, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	} else if err == nil {
		if !user.Active {
			return domain.AuthSession{}, "", ErrAuthInvalid
		}
		_, err = s.db.ExecContext(ctx, `UPDATE auth_users SET name=?,display_name=?,avatar_url=?,email_verified_at=?,updated_at=? WHERE id=?`, name, name, strings.TrimSpace(avatarURL), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), user.ID)
		user.Name, user.DisplayName, user.AvatarURL, user.EmailVerified = name, name, strings.TrimSpace(avatarURL), true
	}
	if err != nil {
		return domain.AuthSession{}, "", err
	}
	if autoProvision {
		s.mu.RLock()
		workspaceKey := s.lastWorkspaceKey
		if workspaceKey == "" {
			workspaceKey = firstWorkspaceKey(s.workspaces)
		}
		workspace := s.workspaces[workspaceKey]
		s.mu.RUnlock()
		if workspace.Workspace.ID != "" {
			_, err = s.db.ExecContext(ctx, `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at,last_seen_at) VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING`, workspace.Workspace.ID, user.ID, "member", "active", now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
			if err == nil && len(workspace.Teams) > 0 {
				_, err = s.db.ExecContext(ctx, `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING`, workspace.Workspace.ID, workspace.Teams[0].ID, user.ID, "member", now.Format(time.RFC3339Nano))
			}
		}
	}
	if err != nil {
		return domain.AuthSession{}, "", err
	}
	return s.createSession(ctx, user)
}

// LoginExternalIdentity authenticates an IdP identity independently of email.
// Email remains the compatibility lookup used by LoginExternal, but is optional
// here so enterprise providers may identify users with an employee number.
func (s *SQLiteStore) LoginExternalIdentity(ctx context.Context, provider, issuer, subject, username, email, name, avatarURL, claimsJSON string, autoProvision bool) (domain.AuthSession, string, error) {
	provider, issuer, subject = strings.TrimSpace(provider), strings.TrimSpace(issuer), strings.TrimSpace(subject)
	if provider == "" || issuer == "" || subject == "" {
		return domain.AuthSession{}, "", ErrAuthInvalid
	}
	email = normalizeEmail(email)
	name, username, avatarURL = strings.TrimSpace(name), strings.TrimSpace(username), strings.TrimSpace(avatarURL)
	if strings.TrimSpace(claimsJSON) == "" {
		claimsJSON = "{}"
	}
	var user domain.User
	var identityID string
	var storedUsername string
	var verified sql.NullString
	var nullableEmail sql.NullString
	var active int
	err := s.db.QueryRowContext(ctx, `SELECT i.id,i.username,u.id,u.email,u.name,u.display_name,u.avatar_url,u.email_verified_at,u.active FROM auth_identities i JOIN auth_users u ON u.id=i.user_id WHERE i.identity_key=?`, externalIdentityKey(provider, issuer, subject)).Scan(&identityID, &storedUsername, &user.ID, &nullableEmail, &user.Name, &user.DisplayName, &user.AvatarURL, &verified, &active)
	if err == nil {
		if active == 0 {
			return domain.AuthSession{}, "", ErrAuthInvalid
		}
		user.Email, user.Active, user.EmailVerified = nullableEmail.String, true, verified.Valid
		if name == "" {
			name = user.Name
		}
		if username == "" {
			username = storedUsername
		}
		if name != user.Name || (avatarURL != "" && avatarURL != user.AvatarURL) {
			if avatarURL == "" {
				avatarURL = user.AvatarURL
			}
			_, err = s.db.ExecContext(ctx, `UPDATE auth_users SET name=?,display_name=?,avatar_url=?,updated_at=? WHERE id=?`, name, name, avatarURL, time.Now().UTC().Format(time.RFC3339Nano), user.ID)
			if err != nil {
				return domain.AuthSession{}, "", err
			}
			user.Name, user.DisplayName, user.AvatarURL = name, name, avatarURL
		}
		if email != "" && user.Email == "" {
			now := time.Now().UTC().Format(time.RFC3339Nano)
			_, err = s.db.ExecContext(ctx, `UPDATE auth_users SET email=?,email_verified_at=?,updated_at=? WHERE id=? AND (email IS NULL OR email='')`, email, now, now, user.ID)
			if err == nil {
				user.Email, user.EmailVerified = email, true
			}
		}
		if err == nil {
			_, err = s.db.ExecContext(ctx, `UPDATE auth_identities SET username=?,claims_json=?,last_login_at=? WHERE id=?`, username, claimsJSON, time.Now().UTC().Format(time.RFC3339Nano), identityID)
		}
		if err != nil {
			return domain.AuthSession{}, "", err
		}
		return s.createSession(ctx, user)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.AuthSession{}, "", err
	}
	if email != "" {
		if existing, _, lookupErr := s.authUserByEmail(ctx, email); lookupErr == nil {
			if !existing.Active {
				return domain.AuthSession{}, "", ErrAuthInvalid
			}
			user = existing
			if name != "" || avatarURL != "" {
				if name == "" {
					name = user.Name
				}
				if avatarURL == "" {
					avatarURL = user.AvatarURL
				}
				now := time.Now().UTC().Format(time.RFC3339Nano)
				_, err = s.db.ExecContext(ctx, `UPDATE auth_users SET name=?,display_name=?,avatar_url=?,email_verified_at=?,updated_at=? WHERE id=?`, name, name, avatarURL, now, now, user.ID)
				if err != nil {
					return domain.AuthSession{}, "", err
				}
				user.Name, user.DisplayName, user.AvatarURL, user.EmailVerified = name, name, avatarURL, true
			}
			if autoProvision {
				if err = s.provisionExternalMemberships(ctx, user.ID, time.Now().UTC()); err != nil {
					return domain.AuthSession{}, "", err
				}
			}
		} else {
			if !autoProvision {
				return domain.AuthSession{}, "", ErrAuthInvalid
			}
			now := time.Now().UTC()
			if name == "" {
				name = strings.Split(email, "@")[0]
			}
			user = domain.User{ID: fmt.Sprintf("usr_%d", now.UnixNano()), Name: name, DisplayName: name, Email: email, AvatarURL: avatarURL, Active: true, EmailVerified: true}
			_, err = s.db.ExecContext(ctx, `INSERT INTO auth_users(id,email,name,display_name,avatar_url,password_hash,email_verified_at,active,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, user.ID, user.Email, user.Name, user.DisplayName, user.AvatarURL, "", now.Format(time.RFC3339Nano), 1, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
			if err != nil {
				return domain.AuthSession{}, "", err
			}
			if err = s.provisionExternalMemberships(ctx, user.ID, now); err != nil {
				return domain.AuthSession{}, "", err
			}
		}
	} else {
		if !autoProvision || name == "" {
			return domain.AuthSession{}, "", ErrAuthInvalid
		}
		now := time.Now().UTC()
		user = domain.User{ID: fmt.Sprintf("usr_%d", now.UnixNano()), Name: name, DisplayName: name, Active: true}
		_, err = s.db.ExecContext(ctx, `INSERT INTO auth_users(id,email,name,display_name,avatar_url,password_hash,email_verified_at,active,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, user.ID, nil, user.Name, user.DisplayName, avatarURL, "", nil, 1, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
		if err != nil {
			return domain.AuthSession{}, "", err
		}
		if err = s.provisionExternalMemberships(ctx, user.ID, now); err != nil {
			return domain.AuthSession{}, "", err
		}
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `INSERT INTO auth_identities(id,user_id,provider,issuer,subject,identity_key,username,claims_json,created_at,last_login_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, fmt.Sprintf("identity_%d", now.UnixNano()), user.ID, provider, issuer, subject, externalIdentityKey(provider, issuer, subject), username, claimsJSON, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return domain.AuthSession{}, "", err
	}
	return s.createSession(ctx, user)
}

func externalIdentityKey(provider, issuer, subject string) string {
	digest := sha256.Sum256([]byte(provider + "\x00" + issuer + "\x00" + subject))
	return fmt.Sprintf("%x", digest[:])
}

func (s *SQLiteStore) provisionExternalMemberships(ctx context.Context, userID string, now time.Time) error {
	s.mu.RLock()
	workspaceKey := s.lastWorkspaceKey
	if workspaceKey == "" {
		workspaceKey = firstWorkspaceKey(s.workspaces)
	}
	workspace := s.workspaces[workspaceKey]
	s.mu.RUnlock()
	if workspace.Workspace.ID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at,last_seen_at) VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING`, workspace.Workspace.ID, userID, "member", "active", now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err == nil && len(workspace.Teams) > 0 {
		_, err = s.db.ExecContext(ctx, `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING`, workspace.Workspace.ID, workspace.Teams[0].ID, userID, "member", now.Format(time.RFC3339Nano))
	}
	return err
}

func (s *SQLiteStore) createSession(ctx context.Context, user domain.User) (domain.AuthSession, string, error) {
	token, err := randomToken()
	if err != nil {
		return domain.AuthSession{}, "", err
	}
	now := time.Now().UTC()
	expires := now.Add(30 * 24 * time.Hour)
	_, err = s.db.ExecContext(ctx, `INSERT INTO auth_sessions(token_hash,user_id,expires_at,created_at,last_seen_at) VALUES(?,?,?,?,?)`, tokenHash(token), user.ID, expires.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return domain.AuthSession{}, "", err
	}
	return s.sessionForUser(ctx, user, expires), token, nil
}

func (s *SQLiteStore) AuthenticateSession(ctx context.Context, token string) (domain.User, error) {
	var userID, expiresRaw string
	err := s.db.QueryRowContext(ctx, `SELECT user_id,expires_at FROM auth_sessions WHERE token_hash=?`, tokenHash(token)).Scan(&userID, &expiresRaw)
	if err != nil {
		return domain.User{}, ErrAuthInvalid
	}
	expires, err := time.Parse(time.RFC3339Nano, expiresRaw)
	if err != nil || time.Now().After(expires) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE token_hash=?`, tokenHash(token))
		return domain.User{}, ErrAuthExpired
	}
	user, err := s.authUserByID(ctx, userID)
	if err != nil || !user.Active {
		return domain.User{}, ErrAuthInvalid
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE auth_sessions SET last_seen_at=? WHERE token_hash=?`, time.Now().UTC().Format(time.RFC3339Nano), tokenHash(token))
	return user, nil
}

func (s *SQLiteStore) EnforceSessionDuration(ctx context.Context, token string, days int) bool {
	if token == "" || days < 1 {
		return false
	}
	var createdRaw string
	if err := s.db.QueryRowContext(ctx, `SELECT created_at FROM auth_sessions WHERE token_hash=?`, tokenHash(token)).Scan(&createdRaw); err != nil {
		return false
	}
	created, err := time.Parse(time.RFC3339Nano, createdRaw)
	if err != nil || time.Now().UTC().After(created.Add(time.Duration(days)*24*time.Hour)) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE token_hash=?`, tokenHash(token))
		return false
	}
	return true
}

func (s *SQLiteStore) Session(ctx context.Context, token string) (domain.AuthSession, error) {
	user, err := s.AuthenticateSession(ctx, token)
	if err != nil {
		return domain.AuthSession{}, err
	}
	var expiresRaw string
	_ = s.db.QueryRowContext(ctx, `SELECT expires_at FROM auth_sessions WHERE token_hash=?`, tokenHash(token)).Scan(&expiresRaw)
	expires, _ := time.Parse(time.RFC3339Nano, expiresRaw)
	return s.sessionForUser(ctx, user, expires), nil
}

func (s *SQLiteStore) Logout(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE token_hash=?`, tokenHash(token))
	return err
}

func (s *SQLiteStore) UpdateProfile(ctx context.Context, userID, displayName, username, avatarURL string) (domain.User, error) {
	displayName, username = strings.TrimSpace(displayName), strings.TrimSpace(username)
	if displayName == "" || username == "" {
		return domain.User{}, errors.New("name and username are required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.ExecContext(ctx, `UPDATE auth_users SET name=?,display_name=?,avatar_url=?,updated_at=? WHERE id=?`, username, displayName, strings.TrimSpace(avatarURL), now, userID); err != nil {
		return domain.User{}, err
	}
	return s.authUserByID(ctx, userID)
}

func (s *SQLiteStore) UpdateMemberIdentity(ctx context.Context, userID, displayName, username, email string) (domain.User, error) {
	displayName, username, email = strings.TrimSpace(displayName), strings.TrimSpace(username), normalizeEmail(email)
	if displayName == "" || username == "" || !strings.Contains(email, "@") {
		return domain.User{}, fmt.Errorf("invalid member identity")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE auth_users SET display_name=?,name=?,email=?,updated_at=? WHERE id=?`, displayName, username, email, time.Now().UTC().Format(time.RFC3339Nano), userID)
	if err != nil {
		return domain.User{}, err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return domain.User{}, ErrAuthForbidden
	}
	return s.authUserByID(ctx, userID)
}

func (s *SQLiteStore) ChangePassword(ctx context.Context, userID, currentPassword, nextPassword string) error {
	if len(nextPassword) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	var hash string
	if err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM auth_users WHERE id=?`, userID).Scan(&hash); err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(currentPassword)) != nil {
		return ErrAuthInvalid
	}
	nextHash, err := bcrypt.GenerateFromPassword([]byte(nextPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE auth_users SET password_hash=?,updated_at=? WHERE id=?`, string(nextHash), time.Now().UTC().Format(time.RFC3339Nano), userID)
	return err
}

func (s *SQLiteStore) ListSessions(ctx context.Context, userID, currentToken string) ([]domain.AccountSession, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT token_hash,expires_at,created_at,last_seen_at FROM auth_sessions WHERE user_id=? AND expires_at>? ORDER BY last_seen_at DESC`, userID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.AccountSession{}
	currentHash := tokenHash(currentToken)
	for rows.Next() {
		var hash, expiresRaw, createdRaw, seenRaw string
		if err := rows.Scan(&hash, &expiresRaw, &createdRaw, &seenRaw); err != nil {
			return nil, err
		}
		expires, _ := time.Parse(time.RFC3339Nano, expiresRaw)
		created, _ := time.Parse(time.RFC3339Nano, createdRaw)
		seen, _ := time.Parse(time.RFC3339Nano, seenRaw)
		result = append(result, domain.AccountSession{ID: hash[:min(12, len(hash))], Current: hash == currentHash, ExpiresAt: expires, CreatedAt: created, LastSeenAt: seen})
	}
	return result, rows.Err()
}

func (s *SQLiteStore) RevokeOtherSessions(ctx context.Context, userID, currentToken string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE user_id=? AND token_hash<>?`, userID, tokenHash(currentToken))
	return err
}

func (s *SQLiteStore) RevokeSession(ctx context.Context, userID, sessionID, currentToken string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ErrAuthInvalid
	}
	currentHash := tokenHash(currentToken)
	result, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE user_id=? AND token_hash LIKE ? AND token_hash<>?`, userID, sessionID+"%", currentHash)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) ListAuthIdentities(ctx context.Context, userID string) ([]domain.AuthIdentity, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,user_id,provider,issuer,subject,username,claims_json,created_at,last_login_at FROM auth_identities WHERE user_id=? ORDER BY last_login_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.AuthIdentity{}
	for rows.Next() {
		var item domain.AuthIdentity
		var created, lastLogin string
		if err := rows.Scan(&item.ID, &item.UserID, &item.Provider, &item.Issuer, &item.Subject, &item.Username, &item.ClaimsJSON, &created, &lastLogin); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		item.LastLoginAt, _ = time.Parse(time.RFC3339Nano, lastLogin)
		item.ClaimsJSON = "" // claims can contain sensitive IdP attributes
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) UnlinkAuthIdentity(ctx context.Context, userID, identityID string) error {
	var passwordHash string
	if err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM auth_users WHERE id=?`, userID).Scan(&passwordHash); err != nil {
		return err
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_identities WHERE user_id=?`, userID).Scan(&count); err != nil {
		return err
	}
	if count <= 1 && passwordHash == "" {
		return errors.New("cannot unlink the only sign-in method")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM auth_identities WHERE id=? AND user_id=?`, identityID, userID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) EnsureWorkspaceMembership(ctx context.Context, workspaceID, userID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at,last_seen_at) VALUES(?,?,?,?,?,?) ON CONFLICT(workspace_id,user_id) DO UPDATE SET status='active',last_seen_at=excluded.last_seen_at`, workspaceID, userID, "member", "active", now, now)
	return err
}

func (s *SQLiteStore) VerifyEmail(ctx context.Context, token string) error {
	userID, err := s.consumeAuthToken(ctx, token, "verify")
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `UPDATE auth_users SET email_verified_at=?,updated_at=? WHERE id=?`, now, now, userID)
	return err
}

func (s *SQLiteStore) RequestEmailVerification(ctx context.Context, email string) (string, error) {
	user, _, err := s.authUserByEmail(ctx, email)
	if err != nil || user.EmailVerified || !user.Active {
		return "", nil
	}
	return s.createAuthToken(ctx, user.ID, "verify", 24*time.Hour)
}

func (s *SQLiteStore) RequestPasswordReset(ctx context.Context, email string) (string, error) {
	user, _, err := s.authUserByEmail(ctx, email)
	if err != nil {
		return "", nil
	}
	return s.createAuthToken(ctx, user.ID, "reset", time.Hour)
}

func (s *SQLiteStore) ResetPassword(ctx context.Context, token, password string) error {
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	userID, err := s.consumeAuthToken(ctx, token, "reset")
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE auth_users SET password_hash=?,updated_at=? WHERE id=?`, string(hash), time.Now().UTC().Format(time.RFC3339Nano), userID)
	if err == nil {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE user_id=?`, userID)
	}
	return err
}

func (s *SQLiteStore) WorkspaceRole(ctx context.Context, workspaceID, userID string) (string, string, error) {
	var role, status string
	err := s.db.QueryRowContext(ctx, `SELECT role,status FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, userID).Scan(&role, &status)
	if err != nil {
		return "", "", ErrAuthForbidden
	}
	return role, status, nil
}

func (s *SQLiteStore) TeamRole(ctx context.Context, workspaceID, teamID, userID string) (string, error) {
	// Workspace owners/admins can administer every team. Team owners are also
	// inherited by descendants.
	if workspaceRole, status, err := s.WorkspaceRole(ctx, workspaceID, userID); err == nil && status == "active" && isWorkspaceAdminRole(workspaceRole) {
		return "owner", nil
	}
	data, _, ok := s.workspaceByID(workspaceID)
	if !ok {
		return "", ErrAuthForbidden
	}
	role := s.teamRoleDirect(ctx, workspaceID, teamID, userID)
	if role == "owner" {
		return role, nil
	}
	// Walk the parent chain defensively. A malformed cycle must not turn into
	// an unbounded authorization query.
	seen := map[string]bool{}
	for current := teamID; current != "" && !seen[current]; {
		seen[current] = true
		settings, exists := data.TeamSettings[current]
		if !exists || settings.ParentTeamID == "" {
			break
		}
		parent := settings.ParentTeamID
		if parentRole := s.teamRoleDirect(ctx, workspaceID, parent, userID); parentRole == "owner" {
			return "owner", nil
		}
		current = parent
	}
	if role != "" {
		return role, nil
	}
	return "", ErrAuthForbidden
}

func (s *SQLiteStore) teamRoleDirect(ctx context.Context, workspaceID, teamID, userID string) string {
	var role string
	if err := s.db.QueryRowContext(ctx, `SELECT role FROM team_memberships WHERE workspace_id=? AND team_id=? AND user_id=?`, workspaceID, teamID, userID).Scan(&role); err != nil {
		return ""
	}
	return role
}

func isWorkspaceAdminRole(role string) bool {
	role = strings.ToLower(strings.TrimSpace(role))
	return role == "admin" || role == "owner"
}

func (s *SQLiteStore) AccountForUser(ctx context.Context, userID string) (domain.AccountBootstrap, error) {
	user, err := s.authUserByID(ctx, userID)
	if err != nil {
		return domain.AccountBootstrap{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_id,role,joined_at FROM workspace_memberships WHERE user_id=? AND status='active' ORDER BY joined_at`, userID)
	if err != nil {
		return domain.AccountBootstrap{}, err
	}
	defer rows.Close()
	result := domain.AccountBootstrap{Viewer: user, Workspaces: []domain.WorkspaceMembership{}}
	for rows.Next() {
		var workspaceID, role, joinedRaw string
		if err := rows.Scan(&workspaceID, &role, &joinedRaw); err != nil {
			return result, err
		}
		data, key, ok := s.workspaceByID(workspaceID)
		if !ok {
			continue
		}
		joined, _ := time.Parse(time.RFC3339Nano, joinedRaw)
		result.Workspaces = append(result.Workspaces, domain.WorkspaceMembership{Workspace: data.Workspace, Role: titleRole(role), JoinedAt: joined, IssueCount: len(data.Issues)})
		if result.LastWorkspaceKey == "" {
			result.LastWorkspaceKey = key
		}
	}
	var preferred string
	if err := s.db.QueryRowContext(ctx, `SELECT last_workspace_key FROM auth_account_state WHERE user_id=?`, userID).Scan(&preferred); err == nil && slices.ContainsFunc(result.Workspaces, func(item domain.WorkspaceMembership) bool { return item.Workspace.URLKey == preferred }) {
		result.LastWorkspaceKey = preferred
	}
	return result, rows.Err()
}

func (s *SQLiteStore) SetLastWorkspace(ctx context.Context, userID, workspaceKey string) error {
	data, ok := s.BootstrapFor(workspaceKey)
	if !ok {
		return ErrAuthForbidden
	}
	_, status, err := s.WorkspaceRole(ctx, data.Workspace.ID, userID)
	if err != nil || status != "active" {
		return ErrAuthForbidden
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `INSERT INTO auth_account_state(user_id,last_workspace_key,updated_at) VALUES(?,?,?) ON CONFLICT(user_id) DO UPDATE SET last_workspace_key=excluded.last_workspace_key,updated_at=excluded.updated_at`, userID, workspaceKey, now)
	return err
}

func (s *SQLiteStore) BootstrapForUser(ctx context.Context, workspaceKey, userID string) (domain.Bootstrap, bool, error) {
	data, ok := s.BootstrapFor(workspaceKey)
	if !ok {
		return domain.Bootstrap{}, false, nil
	}
	role, status, err := s.WorkspaceRole(ctx, data.Workspace.ID, userID)
	if err != nil || status != "active" {
		return domain.Bootstrap{}, false, ErrAuthForbidden
	}
	user, err := s.authUserByID(ctx, userID)
	if err != nil {
		return domain.Bootstrap{}, false, err
	}
	data.Viewer, data.ViewerRole = user, role
	if !isWorkspaceAdminRole(role) || data.WorkspaceSettings.Plan != "enterprise" {
		data.AuditLog = []domain.AuditLogEntry{}
	}
	data.Members, _ = s.ListMembers(ctx, data.Workspace.ID)
	data.TeamMembers, _ = s.ListTeamMembers(ctx, data.Workspace.ID)
	if isWorkspaceAdminRole(role) {
		data.Invitations, _ = s.ListInvitations(ctx, data.Workspace.ID)
	} else {
		data.Invitations = []domain.Invitation{}
	}
	data.Users = make([]domain.User, 0, len(data.Members))
	for _, member := range data.Members {
		data.Users = append(data.Users, member.User)
	}
	allowed := map[string]bool{}
	for _, team := range data.Teams {
		if teamVisibleToUser(data, team.ID, userID, role) {
			allowed[team.ID] = true
		}
	}
	filterBootstrapTeams(&data, allowed, role == "guest")
	hasSpecificStates := map[string]bool{}
	for _, state := range data.States {
		if state.TeamID != "" {
			hasSpecificStates[state.TeamID] = true
		}
	}
	needGlobalStates := slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return allowed[team.ID] && !hasSpecificStates[team.ID] })
	data.States = slices.DeleteFunc(data.States, func(state domain.WorkflowState) bool {
		if state.TeamID == "" {
			return !needGlobalStates
		}
		return !allowed[state.TeamID]
	})
	for teamID := range data.TeamSettings {
		if !allowed[teamID] {
			delete(data.TeamSettings, teamID)
		}
	}
	for teamID := range data.CycleSettings {
		if !allowed[teamID] {
			delete(data.CycleSettings, teamID)
		}
	}
	data.IssueTemplates = slices.DeleteFunc(data.IssueTemplates, func(item domain.IssueTemplate) bool { return item.TeamID != "" && !allowed[item.TeamID] })
	data.ProjectTemplates = slices.DeleteFunc(data.ProjectTemplates, func(item domain.ProjectTemplate) bool {
		return len(item.TeamIDs) > 0 && !slices.ContainsFunc(item.TeamIDs, func(teamID string) bool { return allowed[teamID] })
	})
	data.Notifications = slices.DeleteFunc(data.Notifications, func(item domain.Notification) bool { return item.RecipientID != userID })
	data.NotificationDeliveries = slices.DeleteFunc(data.NotificationDeliveries, func(item domain.NotificationDelivery) bool { return item.RecipientID != userID })
	if preferences, ok := data.NotificationPreferences[userID]; ok {
		data.NotificationPreferences = map[string]domain.NotificationPreferences{userID: preferences}
	} else {
		data.NotificationPreferences = map[string]domain.NotificationPreferences{}
	}
	if settings, ok := data.UserSettings[userID]; ok {
		data.UserSettings = map[string]domain.UserSettings{userID: settings}
	} else {
		data.UserSettings = map[string]domain.UserSettings{}
	}
	data.Drafts = slices.DeleteFunc(data.Drafts, func(item domain.Draft) bool { return item.UserID != userID })
	data.Favorites = slices.DeleteFunc(data.Favorites, func(item domain.Favorite) bool { return item.UserID != userID })
	data.Subscriptions = slices.DeleteFunc(data.Subscriptions, func(item domain.Subscription) bool { return item.UserID != userID })
	data.ImportJobs = slices.DeleteFunc(data.ImportJobs, func(item domain.ImportJob) bool { return item.UserID != userID })
	data.ExportJobs = slices.DeleteFunc(data.ExportJobs, func(item domain.ExportJob) bool { return item.UserID != userID })
	if !isWorkspaceAdminRole(role) {
		data.AuditLog = []domain.AuditLogEntry{}
		data.Trash = slices.DeleteFunc(data.Trash, func(item domain.TrashEntry) bool { return item.DeletedBy.ID != userID })
	}
	if role == "guest" {
		visibleUsers := map[string]bool{userID: true}
		for _, membership := range data.TeamMembers {
			if allowed[membership.TeamID] {
				visibleUsers[membership.UserID] = true
			}
		}
		data.Members = slices.DeleteFunc(data.Members, func(member domain.WorkspaceMember) bool { return !visibleUsers[member.User.ID] })
		data.Users = slices.DeleteFunc(data.Users, func(member domain.User) bool { return !visibleUsers[member.ID] })
	}
	data.TeamMembers = slices.DeleteFunc(data.TeamMembers, func(member domain.TeamMember) bool { return !allowed[member.TeamID] })
	refreshResourceCounts(&data)
	return data, true, nil
}

func (s *SQLiteStore) ListMembers(ctx context.Context, workspaceID string) ([]domain.WorkspaceMember, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT u.id,u.email,u.name,u.display_name,u.avatar_url,u.email_verified_at,u.active,m.role,m.status,m.joined_at,m.last_seen_at FROM workspace_memberships m JOIN auth_users u ON u.id=m.user_id WHERE m.workspace_id=? ORDER BY lower(u.display_name)`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.WorkspaceMember{}
	for rows.Next() {
		var user domain.User
		var nullableEmail, verified, lastSeen sql.NullString
		var active int
		var role, status, joinedRaw string
		if err := rows.Scan(&user.ID, &nullableEmail, &user.Name, &user.DisplayName, &user.AvatarURL, &verified, &active, &role, &status, &joinedRaw, &lastSeen); err != nil {
			return nil, err
		}
		user.Email = nullableEmail.String
		user.Active, user.EmailVerified = active == 1, verified.Valid
		joined, _ := time.Parse(time.RFC3339Nano, joinedRaw)
		var last *time.Time
		if lastSeen.Valid {
			value, _ := time.Parse(time.RFC3339Nano, lastSeen.String)
			last = &value
		}
		result = append(result, domain.WorkspaceMember{User: user, Role: role, Status: status, JoinedAt: joined, LastSeenAt: last})
	}
	return result, rows.Err()
}

func (s *SQLiteStore) ListTeamMembers(ctx context.Context, workspaceID string) ([]domain.TeamMember, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT team_id,user_id,role,joined_at FROM team_memberships WHERE workspace_id=? ORDER BY joined_at`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.TeamMember{}
	for rows.Next() {
		var item domain.TeamMember
		var joined string
		if err := rows.Scan(&item.TeamID, &item.UserID, &item.Role, &joined); err != nil {
			return nil, err
		}
		item.JoinedAt, _ = time.Parse(time.RFC3339Nano, joined)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) Invite(ctx context.Context, workspaceID, inviterID, email, role string, teamIDs []string) (domain.Invitation, error) {
	email = normalizeEmail(email)
	if !validWorkspaceRole(role) || !strings.Contains(email, "@") || role == "guest" && len(teamIDs) == 0 {
		return domain.Invitation{}, fmt.Errorf("invalid invitation")
	}
	data, _, ok := s.workspaceByID(workspaceID)
	if !ok || slices.ContainsFunc(teamIDs, func(teamID string) bool {
		return !slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == teamID })
	}) {
		return domain.Invitation{}, fmt.Errorf("invalid team")
	}
	token, err := randomToken()
	if err != nil {
		return domain.Invitation{}, err
	}
	now := time.Now().UTC()
	item := domain.Invitation{ID: fmt.Sprintf("invite_%d", now.UnixNano()), WorkspaceID: workspaceID, Email: email, Role: role, TeamIDs: teamIDs, Status: "pending", InviterID: inviterID, ExpiresAt: now.Add(7 * 24 * time.Hour), CreatedAt: now, Token: token}
	teamRaw, _ := json.Marshal(teamIDs)
	_, err = s.db.ExecContext(ctx, `INSERT INTO workspace_invitations(id,workspace_id,email,role,team_ids,token_hash,inviter_id,status,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, item.ID, workspaceID, email, role, teamRaw, tokenHash(token), inviterID, item.Status, item.ExpiresAt.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	return item, err
}

func (s *SQLiteStore) AcceptInvitation(ctx context.Context, token, userID string) (domain.WorkspaceMembership, error) {
	var id, workspaceID, email, role, teamRaw, expiresRaw, status string
	err := s.db.QueryRowContext(ctx, `SELECT id,workspace_id,email,role,team_ids,expires_at,status FROM workspace_invitations WHERE token_hash=?`, tokenHash(token)).Scan(&id, &workspaceID, &email, &role, &teamRaw, &expiresRaw, &status)
	if err != nil || status != "pending" {
		return domain.WorkspaceMembership{}, ErrAuthExpired
	}
	expires, _ := time.Parse(time.RFC3339Nano, expiresRaw)
	if time.Now().After(expires) {
		return domain.WorkspaceMembership{}, ErrAuthExpired
	}
	user, err := s.authUserByID(ctx, userID)
	if err != nil || !strings.EqualFold(user.Email, email) {
		return domain.WorkspaceMembership{}, ErrAuthForbidden
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.WorkspaceMembership{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at,last_seen_at) VALUES(?,?,?,?,?,?) ON CONFLICT(workspace_id,user_id) DO UPDATE SET role=excluded.role,status='active'`, workspaceID, userID, role, "active", now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return domain.WorkspaceMembership{}, err
	}
	var teamIDs []string
	_ = json.Unmarshal([]byte(teamRaw), &teamIDs)
	for _, teamID := range teamIDs {
		teamRole := "member"
		if role == "owner" {
			teamRole = "owner"
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING`, workspaceID, teamID, userID, teamRole, now.Format(time.RFC3339Nano)); err != nil {
			return domain.WorkspaceMembership{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workspace_invitations SET status='accepted',accepted_at=? WHERE id=?`, now.Format(time.RFC3339Nano), id); err != nil {
		return domain.WorkspaceMembership{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.WorkspaceMembership{}, err
	}
	data, _, ok := s.workspaceByID(workspaceID)
	if !ok {
		return domain.WorkspaceMembership{}, ErrAuthForbidden
	}
	return domain.WorkspaceMembership{Workspace: data.Workspace, Role: titleRole(role), JoinedAt: now, IssueCount: len(data.Issues)}, nil
}

func (s *SQLiteStore) ListInvitations(ctx context.Context, workspaceID string) ([]domain.Invitation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,email,role,team_ids,inviter_id,status,expires_at,created_at,accepted_at FROM workspace_invitations WHERE workspace_id=? ORDER BY created_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Invitation{}
	for rows.Next() {
		var item domain.Invitation
		var teamRaw, expiresRaw, createdRaw string
		var accepted sql.NullString
		item.WorkspaceID = workspaceID
		if err := rows.Scan(&item.ID, &item.Email, &item.Role, &teamRaw, &item.InviterID, &item.Status, &expiresRaw, &createdRaw, &accepted); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(teamRaw), &item.TeamIDs)
		item.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expiresRaw)
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdRaw)
		if accepted.Valid {
			value, _ := time.Parse(time.RFC3339Nano, accepted.String)
			item.AcceptedAt = &value
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) UpdateMemberRole(ctx context.Context, workspaceID, userID, role string) error {
	if !validWorkspaceRole(role) {
		return fmt.Errorf("invalid role")
	}
	if err := s.ensureAdminRemains(ctx, workspaceID, userID); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE workspace_memberships SET role=? WHERE workspace_id=? AND user_id=?`, role, workspaceID, userID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return ErrAuthForbidden
	}
	return nil
}

func (s *SQLiteStore) SuspendMember(ctx context.Context, workspaceID, userID string) error {
	if err := s.ensureAdminRemains(ctx, workspaceID, userID); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, workspaceID, userID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return ErrAuthForbidden
	}
	return nil
}

func (s *SQLiteStore) ResumeMember(ctx context.Context, workspaceID, userID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE workspace_memberships SET status='active' WHERE workspace_id=? AND user_id=? AND status='suspended'`, workspaceID, userID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return ErrAuthForbidden
	}
	return nil
}

func (s *SQLiteStore) RemoveMember(ctx context.Context, workspaceID, userID string) error {
	if err := s.ensureAdminRemains(ctx, workspaceID, userID); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM team_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, userID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) ensureAdminRemains(ctx context.Context, workspaceID, userID string) error {
	var role, status string
	if err := s.db.QueryRowContext(ctx, `SELECT role,status FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, userID).Scan(&role, &status); err != nil {
		return ErrAuthForbidden
	}
	if !isWorkspaceAdminRole(role) || status != "active" {
		return nil
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_memberships WHERE workspace_id=? AND role IN ('admin','owner') AND status='active'`, workspaceID).Scan(&count); err != nil {
		return err
	}
	if count <= 1 {
		return ErrLastAdmin
	}
	return nil
}

func (s *SQLiteStore) InvitationPreview(ctx context.Context, token string) (domain.Invitation, domain.Workspace, error) {
	var item domain.Invitation
	var teamRaw, expiresRaw, createdRaw string
	err := s.db.QueryRowContext(ctx, `SELECT id,workspace_id,email,role,team_ids,inviter_id,status,expires_at,created_at FROM workspace_invitations WHERE token_hash=?`, tokenHash(token)).Scan(&item.ID, &item.WorkspaceID, &item.Email, &item.Role, &teamRaw, &item.InviterID, &item.Status, &expiresRaw, &createdRaw)
	if err != nil || item.Status != "pending" {
		return item, domain.Workspace{}, ErrAuthExpired
	}
	_ = json.Unmarshal([]byte(teamRaw), &item.TeamIDs)
	item.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expiresRaw)
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdRaw)
	if time.Now().After(item.ExpiresAt) {
		return item, domain.Workspace{}, ErrAuthExpired
	}
	data, _, ok := s.workspaceByID(item.WorkspaceID)
	if !ok {
		return item, domain.Workspace{}, ErrAuthExpired
	}
	return item, data.Workspace, nil
}

func (s *SQLiteStore) RevokeInvitation(ctx context.Context, workspaceID, invitationID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE workspace_invitations SET status='revoked' WHERE id=? AND workspace_id=? AND status='pending'`, invitationID, workspaceID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return ErrAuthForbidden
	}
	return nil
}

func (s *SQLiteStore) ResendInvitation(ctx context.Context, workspaceID, invitationID string) (domain.Invitation, error) {
	var email, role, inviterID, teamRaw string
	err := s.db.QueryRowContext(ctx, `SELECT email,role,inviter_id,team_ids FROM workspace_invitations WHERE id=? AND workspace_id=? AND status='pending'`, invitationID, workspaceID).Scan(&email, &role, &inviterID, &teamRaw)
	if err != nil {
		return domain.Invitation{}, ErrAuthForbidden
	}
	var teamIDs []string
	_ = json.Unmarshal([]byte(teamRaw), &teamIDs)
	if _, err := s.db.ExecContext(ctx, `UPDATE workspace_invitations SET status='revoked' WHERE id=?`, invitationID); err != nil {
		return domain.Invitation{}, err
	}
	return s.Invite(ctx, workspaceID, inviterID, email, role, teamIDs)
}

func (s *SQLiteStore) SetTeamMembership(ctx context.Context, workspaceID, teamID, userID, role string, member bool) error {
	if role != "owner" && role != "member" {
		return fmt.Errorf("invalid team role")
	}
	data, workspaceKey, ok := s.workspaceByID(workspaceID)
	if !ok || !slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == teamID }) {
		return ErrAuthForbidden
	}
	if _, status, err := s.WorkspaceRole(ctx, workspaceID, userID); err != nil || status != "active" {
		return ErrAuthForbidden
	}
	if !member {
		if direct := s.teamRoleDirect(ctx, workspaceID, teamID, userID); direct == "owner" {
			var owners int
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_memberships WHERE workspace_id=? AND team_id=? AND role='owner'`, workspaceID, teamID).Scan(&owners); err != nil {
				return err
			}
			if owners <= 1 {
				return ErrLastTeamOwner
			}
		}
		_, err := s.db.ExecContext(ctx, `DELETE FROM team_memberships WHERE workspace_id=? AND team_id=? AND user_id=?`, workspaceID, teamID, userID)
		if err != nil {
			return err
		}
		return s.cleanupTeamMemberData(ctx, workspaceKey, teamID, userID)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) VALUES(?,?,?,?,?) ON CONFLICT(workspace_id,team_id,user_id) DO UPDATE SET role=excluded.role`, workspaceID, teamID, userID, role, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// cleanupTeamMemberData removes assignments and subscriptions that are no
// longer valid after a member leaves a team. Explicit issue shares remain
// intact so a private issue can still be shared intentionally.
func (s *SQLiteStore) cleanupTeamMemberData(ctx context.Context, workspaceKey, teamID, userID string) error {
	return s.MutateWorkspace(ctx, workspaceKey, "team_member.cleaned_up", teamID, map[string]string{"userId": userID}, func(data *domain.Bootstrap) error {
		for index := range data.Issues {
			if data.Issues[index].Team.ID != teamID {
				continue
			}
			if data.Issues[index].Assignee != nil && data.Issues[index].Assignee.ID == userID {
				data.Issues[index].Assignee = nil
			}
			data.Issues[index].SubscriberIDs = slices.DeleteFunc(data.Issues[index].SubscriberIDs, func(id string) bool { return id == userID })
		}
		return nil
	})
}

func (s *SQLiteStore) DeleteTeamMemberships(ctx context.Context, workspaceID, teamID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM team_memberships WHERE workspace_id=? AND team_id=?`, workspaceID, teamID)
	return err
}

func (s *SQLiteStore) authUserByEmail(ctx context.Context, email string) (domain.User, string, error) {
	var user domain.User
	var hash string
	var verified sql.NullString
	var active int
	err := s.db.QueryRowContext(ctx, `SELECT id,email,name,display_name,avatar_url,password_hash,email_verified_at,active FROM auth_users WHERE email=?`, normalizeEmail(email)).Scan(&user.ID, &user.Email, &user.Name, &user.DisplayName, &user.AvatarURL, &hash, &verified, &active)
	user.Active, user.EmailVerified = active == 1, verified.Valid
	return user, hash, err
}

func (s *SQLiteStore) authUserByID(ctx context.Context, id string) (domain.User, error) {
	var user domain.User
	var nullableEmail sql.NullString
	var verified sql.NullString
	var active int
	err := s.db.QueryRowContext(ctx, `SELECT id,email,name,display_name,avatar_url,email_verified_at,active FROM auth_users WHERE id=?`, id).Scan(&user.ID, &nullableEmail, &user.Name, &user.DisplayName, &user.AvatarURL, &verified, &active)
	user.Email = nullableEmail.String
	user.Active, user.EmailVerified = active == 1, verified.Valid
	return user, err
}

func (s *SQLiteStore) createAuthToken(ctx context.Context, userID, kind string, duration time.Duration) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `INSERT INTO auth_tokens(token_hash,user_id,kind,expires_at,created_at) VALUES(?,?,?,?,?)`, tokenHash(token), userID, kind, now.Add(duration).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	return token, err
}

func (s *SQLiteStore) consumeAuthToken(ctx context.Context, token, kind string) (string, error) {
	var userID, expiresRaw string
	var used sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT user_id,expires_at,used_at FROM auth_tokens WHERE token_hash=? AND kind=?`, tokenHash(token), kind).Scan(&userID, &expiresRaw, &used)
	if err != nil || used.Valid {
		return "", ErrAuthExpired
	}
	expires, _ := time.Parse(time.RFC3339Nano, expiresRaw)
	if time.Now().After(expires) {
		return "", ErrAuthExpired
	}
	_, err = s.db.ExecContext(ctx, `UPDATE auth_tokens SET used_at=? WHERE token_hash=?`, time.Now().UTC().Format(time.RFC3339Nano), tokenHash(token))
	return userID, err
}

func (s *SQLiteStore) sessionForUser(ctx context.Context, user domain.User, expires time.Time) domain.AuthSession {
	account, _ := s.AccountForUser(ctx, user.ID)
	return domain.AuthSession{User: user, Memberships: account.Workspaces, ExpiresAt: expires}
}

func (s *SQLiteStore) workspaceByID(id string) (domain.Bootstrap, string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for key, data := range s.workspaces {
		if data.Workspace.ID == id {
			return data, key, true
		}
	}
	return domain.Bootstrap{}, "", false
}

// teamVisibleToUser applies the team access and membership settings before a
// bootstrap projection is returned. Public teams are visible to workspace
// members; private and restricted teams require explicit membership. A parent
// team owner inherits owner access to descendants, but ordinary parent members
// do not bypass a restricted child.
func teamVisibleToUser(data domain.Bootstrap, teamID, userID, workspaceRole string) bool {
	if isWorkspaceAdminRole(workspaceRole) && !strings.EqualFold(data.WorkspaceSettings.Plan, "enterprise") {
		return true
	}
	exists := false
	for _, item := range data.Teams {
		if item.ID == teamID {
			exists = true
			break
		}
	}
	if !exists {
		return false
	}
	memberRole := ""
	for _, membership := range data.TeamMembers {
		if membership.TeamID == teamID && membership.UserID == userID {
			memberRole = membership.Role
			break
		}
	}
	if memberRole != "" {
		return true
	}
	// Parent-team owners inherit access to descendants, including private
	// children. Ordinary parent members do not.
	seen := map[string]bool{}
	for current := teamID; current != "" && !seen[current]; {
		seen[current] = true
		settings := data.TeamSettings[current]
		if settings.ParentTeamID == "" {
			break
		}
		parentRole := ""
		for _, membership := range data.TeamMembers {
			if membership.TeamID == settings.ParentTeamID && membership.UserID == userID {
				parentRole = membership.Role
				break
			}
		}
		if strings.EqualFold(parentRole, "owner") {
			return true
		}
		current = settings.ParentTeamID
	}
	settings := data.TeamSettings[teamID]
	access := strings.ToLower(strings.TrimSpace(settings.Access))
	if access == "" {
		for _, item := range data.Teams {
			if item.ID == teamID && item.Private {
				access = "private"
				break
			}
		}
	}
	if workspaceRole == "guest" {
		return false
	}
	if access == "private" || access == "restricted" || strings.EqualFold(settings.MembershipRestriction, "members") || strings.EqualFold(settings.MembershipRestriction, "owners") {
		return false
	}
	return true
}

func filterBootstrapTeams(data *domain.Bootstrap, allowed map[string]bool, guest bool) {
	data.Teams = slices.DeleteFunc(data.Teams, func(team domain.Team) bool { return !allowed[team.ID] })
	data.Issues = slices.DeleteFunc(data.Issues, func(issue domain.Issue) bool {
		if allowed[issue.Team.ID] {
			return false
		}
		return !issuePermissionAllows(*data, issue, allowed)
	})
	// Documents can be scoped to private teams just like issues and projects.
	// Keep unscoped documents workspace-visible, while preventing a member from
	// discovering the title or content of a team document they cannot access.
	if !isWorkspaceAdminRole(data.ViewerRole) || strings.EqualFold(data.WorkspaceSettings.Plan, "enterprise") {
		data.Documents = slices.DeleteFunc(data.Documents, func(document domain.Document) bool {
			if len(document.TeamIDs) == 0 || slices.ContainsFunc(document.TeamIDs, func(teamID string) bool { return allowed[teamID] }) {
				return false
			}
			return !documentPermissionAllows(data, document, allowed)
		})
	}
	data.Cycles = slices.DeleteFunc(data.Cycles, func(cycle domain.Cycle) bool { return !allowed[cycle.TeamID] })
	data.Projects = slices.DeleteFunc(data.Projects, func(project domain.Project) bool {
		if len(project.TeamIDs) == 0 {
			return false
		}
		for _, id := range project.TeamIDs {
			if allowed[id] {
				return false
			}
		}
		return true
	})
	// A project can be shared by public and private teams. Keep the project
	// shell when at least one team is visible, but redact hidden team bindings
	// and all cross-team references from the projected payload.
	for index := range data.Projects {
		data.Projects[index].TeamIDs = slices.DeleteFunc(data.Projects[index].TeamIDs, func(teamID string) bool { return !allowed[teamID] })
		data.Projects[index].DependencyIDs = slices.DeleteFunc(data.Projects[index].DependencyIDs, func(projectID string) bool {
			return !slices.ContainsFunc(data.Projects, func(project domain.Project) bool { return project.ID == projectID })
		})
	}
	visibleProjects := map[string]bool{}
	for _, project := range data.Projects {
		visibleProjects[project.ID] = true
	}
	visibleIssues := map[string]bool{}
	for _, issue := range data.Issues {
		visibleIssues[issue.ID] = true
	}
	// Recompute project issue counts from the visible issue projection so a
	// public project shell cannot reveal the size of a private team's backlog.
	for index := range data.Projects {
		count := 0
		for _, issue := range data.Issues {
			if issue.Project != nil && issue.Project.ID == data.Projects[index].ID {
				count++
			}
		}
		data.Projects[index].IssueCount = count
	}
	data.ReleasePipelines = slices.DeleteFunc(data.ReleasePipelines, func(pipeline domain.ReleasePipeline) bool {
		return len(pipeline.TeamIDs) > 0 && !slices.ContainsFunc(pipeline.TeamIDs, func(teamID string) bool { return allowed[teamID] })
	})
	visiblePipelines := map[string]bool{}
	for index := range data.ReleasePipelines {
		data.ReleasePipelines[index].TeamIDs = slices.DeleteFunc(data.ReleasePipelines[index].TeamIDs, func(teamID string) bool { return !allowed[teamID] })
		visiblePipelines[data.ReleasePipelines[index].ID] = true
	}
	data.Releases = slices.DeleteFunc(data.Releases, func(release domain.Release) bool {
		if release.PipelineID != "" && !visiblePipelines[release.PipelineID] {
			return true
		}
		return slices.ContainsFunc(release.ProjectIDs, func(id string) bool { return !visibleProjects[id] }) ||
			slices.ContainsFunc(release.IssueIDs, func(id string) bool { return !visibleIssues[id] })
	})
	for index := range data.Releases {
		data.Releases[index].ProjectIDs = slices.DeleteFunc(data.Releases[index].ProjectIDs, func(id string) bool { return !visibleProjects[id] })
		data.Releases[index].IssueIDs = slices.DeleteFunc(data.Releases[index].IssueIDs, func(id string) bool { return !visibleIssues[id] })
	}
	for id := range data.ProjectUpdates {
		if !visibleProjects[id] {
			delete(data.ProjectUpdates, id)
		}
	}
	data.CustomerRequests = slices.DeleteFunc(data.CustomerRequests, func(item domain.CustomerRequest) bool {
		if item.IssueID != "" && !visibleIssues[item.IssueID] {
			return true
		}
		if item.ProjectID != "" && !visibleProjects[item.ProjectID] {
			return true
		}
		return data.ViewerRole == "guest"
	})
	// Preserve workspace initiatives but redact links into projects the viewer
	// cannot access. This keeps a cross-team initiative shell useful without
	// disclosing private project identifiers.
	for index := range data.Initiatives {
		data.Initiatives[index].ProjectIDs = slices.DeleteFunc(data.Initiatives[index].ProjectIDs, func(id string) bool { return !visibleProjects[id] })
		data.Initiatives[index].ParentInitiativeIDs = slices.DeleteFunc(data.Initiatives[index].ParentInitiativeIDs, func(id string) bool {
			return !slices.ContainsFunc(data.Initiatives, func(item domain.Initiative) bool { return item.ID == id })
		})
		if !allowed[data.Initiatives[index].LeadTeamID] {
			data.Initiatives[index].LeadTeamID = ""
		}
		data.Initiatives[index].ContributingTeamIDs = slices.DeleteFunc(data.Initiatives[index].ContributingTeamIDs, func(id string) bool { return !allowed[id] })
	}
	data.Asks = slices.DeleteFunc(data.Asks, func(item domain.Ask) bool {
		return item.TeamID != "" && !allowed[item.TeamID]
	})
	filterSettingsByVisibility(data, allowed)
	data.SavedViews = slices.DeleteFunc(data.SavedViews, func(view domain.SavedView) bool {
		if view.Scope == "team" && !allowed[view.TeamID] || view.Scope == "personal" && view.OwnerID != data.Viewer.ID {
			return true
		}
		if view.ProjectID != "" && !visibleProjects[view.ProjectID] {
			return true
		}
		return false
	})
	data.Notifications = slices.DeleteFunc(data.Notifications, func(item domain.Notification) bool { return item.RecipientID != data.Viewer.ID })
	if !guest {
		return
	}
	data.Initiatives = []domain.Initiative{}
	data.InitiativeUpdates = map[string][]domain.InitiativeUpdate{}
	data.Customers = []domain.Customer{}
	for id := range data.Comments {
		if !visibleIssues[id] {
			delete(data.Comments, id)
		}
	}
	for id := range data.Activities {
		if !visibleIssues[id] {
			delete(data.Activities, id)
		}
	}
	data.Notifications = slices.DeleteFunc(data.Notifications, func(item domain.Notification) bool {
		return item.RecipientID != data.Viewer.ID || !visibleIssues[item.IssueID]
	})
}

// filterSettingsByVisibility redacts team-scoped setting collections embedded
// in the bootstrap map. Keeping this projection at the store boundary avoids
// leaking private posts, meetings, or dashboards through a raw settings blob.
func filterSettingsByVisibility(data *domain.Bootstrap, allowed map[string]bool) {
	if data.Settings == nil {
		return
	}
	if raw, err := json.Marshal(data.Settings["posts.v1"]); err == nil {
		var posts []domain.Post
		if json.Unmarshal(raw, &posts) == nil {
			posts = slices.DeleteFunc(posts, func(item domain.Post) bool {
				return len(item.TeamIDs) > 0 && !slices.ContainsFunc(item.TeamIDs, func(id string) bool { return allowed[id] })
			})
			data.Settings["posts.v1"] = posts
		}
	}
	if raw, err := json.Marshal(data.Settings["meetings.v1"]); err == nil {
		var meetings []domain.Meeting
		if json.Unmarshal(raw, &meetings) == nil {
			meetings = slices.DeleteFunc(meetings, func(item domain.Meeting) bool {
				if slices.ContainsFunc(item.TeamIDs, func(id string) bool { return allowed[id] }) {
					return false
				}
				return len(item.TeamIDs) > 0 || slices.ContainsFunc(item.IssueIDs, func(issueID string) bool {
					return !slices.ContainsFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == issueID })
				})
			})
			data.Settings["meetings.v1"] = meetings
		}
	}
	if raw, err := json.Marshal(data.Settings["dashboards.v1"]); err == nil {
		var dashboards []domain.Dashboard
		if json.Unmarshal(raw, &dashboards) == nil {
			dashboards = slices.DeleteFunc(dashboards, func(item domain.Dashboard) bool {
				switch item.Visibility {
				case "private":
					return item.OwnerID != data.Viewer.ID && !isWorkspaceAdminRole(data.ViewerRole)
				case "team":
					return !slices.ContainsFunc(item.TeamIDs, func(id string) bool { return allowed[id] })
				default:
					return data.ViewerRole == "guest"
				}
			})
			for index := range dashboards {
				dashboards[index].TeamIDs = slices.DeleteFunc(dashboards[index].TeamIDs, func(id string) bool { return !allowed[id] })
			}
			data.Settings["dashboards.v1"] = dashboards
		}
	}
}

func issuePermissionAllows(data domain.Bootstrap, issue domain.Issue, allowed map[string]bool) bool {
	if isWorkspaceAdminRole(data.ViewerRole) && !strings.EqualFold(data.WorkspaceSettings.Plan, "enterprise") {
		return true
	}
	seenIssues := map[string]bool{}
	for current := &issue; current != nil && !seenIssues[current.ID]; {
		seenIssues[current.ID] = true
		for _, permission := range current.Permissions {
			if permission.Role == "" || strings.EqualFold(permission.Role, "none") {
				continue
			}
			switch permission.SubjectType {
			case "user":
				if permission.SubjectID == data.Viewer.ID {
					return true
				}
			case "workspace":
				if permission.SubjectID == "" || permission.SubjectID == data.Workspace.ID || permission.SubjectID == data.Workspace.URLKey {
					return true
				}
			case "team":
				if allowed[permission.SubjectID] {
					return true
				}
			}
		}
		if current.ParentID == nil || *current.ParentID == "" {
			break
		}
		parentIndex := slices.IndexFunc(data.Issues, func(candidate domain.Issue) bool { return candidate.ID == *current.ParentID })
		if parentIndex < 0 {
			break
		}
		current = &data.Issues[parentIndex]
	}
	return false
}

func documentPermissionAllows(data *domain.Bootstrap, document domain.Document, allowed map[string]bool) bool {
	for _, permission := range document.Permissions {
		if permission.Role == "" || permission.Role == "none" {
			continue
		}
		switch permission.SubjectType {
		case "user":
			if permission.SubjectID == data.Viewer.ID {
				return true
			}
		case "workspace":
			if permission.SubjectID == "" || permission.SubjectID == data.Workspace.ID || permission.SubjectID == data.Workspace.URLKey {
				return true
			}
		case "team":
			if allowed[permission.SubjectID] {
				return true
			}
		}
	}
	return false
}

func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", sum[:])
}

func normalizeEmail(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func validWorkspaceRole(value string) bool {
	return value == "owner" || value == "admin" || value == "member" || value == "guest"
}
func titleRole(value string) string {
	if value == "owner" {
		return "Owner"
	}
	if value == "admin" {
		return "Admin"
	}
	if value == "guest" {
		return "Guest"
	}
	return "Member"
}
