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
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

var (
	ErrAuthInvalid              = errors.New("invalid email or password")
	ErrAuthExpired              = errors.New("token is invalid or expired")
	ErrAuthConflict             = errors.New("account already exists")
	ErrAuthForbidden            = errors.New("forbidden")
	ErrLastAdmin                = errors.New("a workspace needs at least one admin")
	ErrLastTeamOwner            = errors.New("a team needs at least one owner")
	ErrManagedTeamMembership    = errors.New("team membership is managed by SCIM")
	ErrTeamHasSubteamMembership = errors.New("remove sub-team memberships before removing the parent team membership")
	// ErrWorkspaceKeyTaken is returned when a workspace is created or renamed to a
	// URL key another workspace already uses; the API reports it as a conflict.
	ErrWorkspaceKeyTaken = errors.New("workspace key already exists")
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
	if err := checkTextFields(textField{"name", name, domain.UserNameMaxLength}, textField{"email", email, domain.EmailMaxLength}); err != nil {
		return domain.User{}, "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return domain.User{}, "", err
	}
	now := time.Now().UTC()
	user := domain.User{ID: idgen.New("usr_"), Name: name, DisplayName: name, Email: email, Active: true}
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
		user = domain.User{ID: idgen.New("usr_"), Name: name, DisplayName: name, Email: email, AvatarURL: strings.TrimSpace(avatarURL), Active: true, EmailVerified: true}
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
			if err == nil {
				s.syncMemberDirectoryLogged(ctx, workspaceKey, user.ID)
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
	// Identity provider claims fit their columns: the key fields or they are
	// refused, profile fields are cut (a long picture URL is dropped).
	if checkTextFields(textField{"subject", subject, domain.SCIMValueMaxLength}, textField{"issuer", issuer, 2048}, textField{"email", email, domain.EmailMaxLength}) != nil {
		return domain.AuthSession{}, "", ErrAuthInvalid
	}
	name, username = TruncateRunes(name, domain.UserNameMaxLength), TruncateRunes(username, domain.SCIMValueMaxLength)
	if utf8.RuneCountInString(avatarURL) > domain.AvatarURLMaxLength {
		avatarURL = ""
	}
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
			user = domain.User{ID: idgen.New("usr_"), Name: name, DisplayName: name, Email: email, AvatarURL: avatarURL, Active: true, EmailVerified: true}
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
		user = domain.User{ID: idgen.New("usr_"), Name: name, DisplayName: name, Active: true}
		_, err = s.db.ExecContext(ctx, `INSERT INTO auth_users(id,email,name,display_name,avatar_url,password_hash,email_verified_at,active,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, user.ID, nil, user.Name, user.DisplayName, avatarURL, "", nil, 1, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
		if err != nil {
			return domain.AuthSession{}, "", err
		}
		if err = s.provisionExternalMemberships(ctx, user.ID, now); err != nil {
			return domain.AuthSession{}, "", err
		}
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `INSERT INTO auth_identities(id,user_id,provider,issuer,subject,identity_key,username,claims_json,created_at,last_login_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, idgen.New("identity_"), user.ID, provider, issuer, subject, externalIdentityKey(provider, issuer, subject), username, claimsJSON, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
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
	if err == nil {
		s.syncMemberDirectoryLogged(ctx, workspaceKey, userID)
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.AuthSession{}, "", err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO auth_sessions(token_hash,user_id,expires_at,created_at,last_seen_at) VALUES(?,?,?,?,?)`, tokenHash(token), user.ID, expires.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return domain.AuthSession{}, "", err
	}
	// External authentication callbacks replace this context before issuing
	// the cookie. Persist the password context atomically for normal sign-ins.
	if _, err = tx.ExecContext(ctx, `INSERT INTO auth_session_security(token_hash,provider,issuer,mfa_verified_at) VALUES(?,?,?,?)`, tokenHash(token), "password", "", nil); err != nil {
		return domain.AuthSession{}, "", err
	}
	if err = tx.Commit(); err != nil {
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
	var userID string
	_ = s.db.QueryRowContext(ctx, `SELECT user_id FROM auth_sessions WHERE token_hash=?`, tokenHash(token)).Scan(&userID)
	_, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE token_hash=?`, tokenHash(token))
	if err == nil {
		s.accountAccessChanged(userID)
	}
	return err
}

func (s *SQLiteStore) UpdateProfile(ctx context.Context, userID, displayName, username, avatarURL string) (domain.User, error) {
	displayName, username = strings.TrimSpace(displayName), strings.TrimSpace(username)
	if displayName == "" || username == "" {
		return domain.User{}, invalidValue("name and username are required")
	}
	if err := checkTextFields(textField{"displayName", displayName, domain.UserNameMaxLength}, textField{"username", username, domain.UserNameMaxLength}, textField{"avatarUrl", strings.TrimSpace(avatarURL), domain.AvatarURLMaxLength}); err != nil {
		return domain.User{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	avatarURL = strings.TrimSpace(avatarURL)
	// The current values again leave the row (and its updated_at) as it is.
	if current, err := s.authUserByID(ctx, userID); err == nil && current.Name == username && current.DisplayName == displayName && current.AvatarURL == avatarURL {
		return current, nil
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE auth_users SET name=?,display_name=?,avatar_url=?,updated_at=? WHERE id=?`, username, displayName, avatarURL, now, userID); err != nil {
		return domain.User{}, err
	}
	return s.authUserByID(ctx, userID)
}

func (s *SQLiteStore) UpdateMemberIdentity(ctx context.Context, userID, displayName, username, email string) (domain.User, error) {
	displayName, username, email = strings.TrimSpace(displayName), strings.TrimSpace(username), normalizeEmail(email)
	if displayName == "" || username == "" || !strings.Contains(email, "@") {
		return domain.User{}, invalidValue("invalid member identity")
	}
	if err := checkTextFields(textField{"displayName", displayName, domain.UserNameMaxLength}, textField{"username", username, domain.UserNameMaxLength}, textField{"email", email, domain.EmailMaxLength}); err != nil {
		return domain.User{}, err
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
	if err == nil {
		err = s.revokeChatIdentities(ctx, userID)
		s.accountAccessChanged(userID)
	}
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
	err = s.revokeChatIdentities(ctx, userID)
	s.accountAccessChanged(userID)
	return err
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
	if err == nil {
		s.syncMemberDirectoryByID(ctx, workspaceID, userID)
	}
	return err
}

// HasWorkspaceMembership reports whether userID has a membership row in the workspace (any status), including
// members provisioned by an identity provider who have not signed in yet.
func (s *SQLiteStore) HasWorkspaceMembership(ctx context.Context, workspaceID, userID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, userID).Scan(&count)
	return count > 0, err
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
		err = s.revokeChatIdentities(ctx, userID)
		s.accountAccessChanged(userID)
	}
	return err
}

// RequestLoginToken issues a one-time kind=login token for magic-link / SSO bounce (LS-0083).
func (s *SQLiteStore) RequestLoginToken(ctx context.Context, email string) (string, error) {
	user, _, err := s.authUserByEmail(ctx, email)
	if err != nil || !user.Active {
		return "", nil
	}
	if !user.EmailVerified {
		return "", nil
	}
	return s.createAuthToken(ctx, user.ID, "login", time.Hour)
}

// LoginWithAuthToken consumes a kind=login token for email+authToken sign-in.
// When forceReauth is true, existing sessions for the user are revoked first.
func (s *SQLiteStore) LoginWithAuthToken(ctx context.Context, email, token string, forceReauth bool) (domain.AuthSession, string, error) {
	email = normalizeEmail(email)
	if email == "" || strings.TrimSpace(token) == "" {
		return domain.AuthSession{}, "", ErrAuthInvalid
	}
	user, _, err := s.authUserByEmail(ctx, email)
	if err != nil || !user.Active {
		return domain.AuthSession{}, "", ErrAuthInvalid
	}
	userID, err := s.consumeAuthToken(ctx, token, "login")
	if err != nil {
		return domain.AuthSession{}, "", ErrAuthExpired
	}
	if userID != user.ID {
		return domain.AuthSession{}, "", ErrAuthInvalid
	}
	if !user.EmailVerified {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		_, _ = s.db.ExecContext(ctx, `UPDATE auth_users SET email_verified_at=?,updated_at=? WHERE id=? AND email_verified_at IS NULL`, now, now, user.ID)
		user.EmailVerified = true
	}
	if forceReauth {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE user_id=?`, user.ID)
		if err := s.revokeChatIdentities(ctx, user.ID); err != nil {
			return domain.AuthSession{}, "", err
		}
		s.accountAccessChanged(user.ID)
	}
	return s.createSession(ctx, user)
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
	result, err := s.OAuthAccountForUser(ctx, userID)
	if err != nil {
		return result, err
	}
	counts, err := s.issueCollectionCounts(ctx)
	if err != nil {
		return result, err
	}
	for i := range result.Workspaces {
		result.Workspaces[i].IssueCount = counts[result.Workspaces[i].Workspace.URLKey]
	}
	return result, nil
}

func (s *SQLiteStore) SetLastWorkspace(ctx context.Context, userID, workspaceKey string) error {
	data, ok := s.WorkspaceMetadata(workspaceKey)
	if !ok {
		return ErrAuthForbidden
	}
	_, status, err := s.WorkspaceRole(ctx, data.Workspace.ID, userID)
	if err != nil || status != "active" {
		return ErrAuthForbidden
	}
	// The current workspace again leaves the account state as it is.
	var current string
	if s.db.QueryRowContext(ctx, `SELECT last_workspace_key FROM auth_account_state WHERE user_id=?`, userID).Scan(&current) == nil && current == workspaceKey {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `INSERT INTO auth_account_state(user_id,last_workspace_key,updated_at) VALUES(?,?,?) ON CONFLICT(user_id) DO UPDATE SET last_workspace_key=excluded.last_workspace_key,updated_at=excluded.updated_at`, userID, workspaceKey, now)
	return err
}

func (s *SQLiteStore) BootstrapForUser(ctx context.Context, workspaceKey, userID string) (domain.Bootstrap, bool, error) {
	data, ok := s.BootstrapForContext(ctx, workspaceKey)
	if !ok {
		return domain.Bootstrap{}, false, nil
	}
	return s.projectBootstrapForUser(ctx, data, userID)
}

func (s *SQLiteStore) projectBootstrapForUser(ctx context.Context, data domain.Bootstrap, userID string) (domain.Bootstrap, bool, error) {
	role, status, err := s.WorkspaceRole(ctx, data.Workspace.ID, userID)
	if err != nil || status != "active" {
		return domain.Bootstrap{}, false, ErrAuthForbidden
	}
	user, err := s.authUserByID(ctx, userID)
	if err != nil {
		return domain.Bootstrap{}, false, err
	}
	data.Viewer, data.ViewerRole = user, role
	if err := s.mergeTeamDefaultFavorites(ctx, &data, userID); err != nil {
		return domain.Bootstrap{}, false, err
	}
	if !isWorkspaceAdminRole(role) {
		data.AuditLog = []domain.AuditLogEntry{}
	}
	data.Members, _ = s.ListMembers(ctx, data.Workspace.ID)
	if !data.WorkspaceSettings.SCIMEnabled {
		// Turning SCIM provisioning off hands every member back to admins.
		for index := range data.Members {
			data.Members[index].ManagedBy = ""
		}
	}
	data.TeamMembers, _ = s.ListTeamMembers(ctx, data.Workspace.ID)
	data.DepartedMembers = nil
	if isWorkspaceAdminRole(role) {
		data.Invitations, _ = s.ListInvitations(ctx, data.Workspace.ID)
		data.DepartedMembers, _ = s.ListDepartedMembers(ctx, data.Workspace.ID, data.Workspace.URLKey)
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
	filterBootstrapTeams(&data, viewerTeamScope(data, allowed, userID, role), role == "guest")
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
	data.ProviderJobs = slices.DeleteFunc(data.ProviderJobs, func(item domain.ProviderJob) bool { return item.UserID != userID })
	data.Favorites = slices.DeleteFunc(data.Favorites, func(item domain.Favorite) bool { return item.UserID != userID })
	data.FavoriteFolders = slices.DeleteFunc(data.FavoriteFolders, func(item domain.FavoriteFolder) bool { return item.UserID != userID })
	data.Subscriptions = slices.DeleteFunc(data.Subscriptions, func(item domain.Subscription) bool { return item.UserID != userID })
	// The persisted flag is a compatibility field. The viewer's own favorite
	// records determine its value, never another member's last toggle.
	favoriteIDs := map[string]bool{}
	for _, favorite := range data.Favorites {
		favoriteIDs[favorite.ResourceType+":"+favorite.ResourceID] = true
	}
	for i := range data.Documents {
		data.Documents[i].Favorite = favoriteIDs["document:"+data.Documents[i].ID]
	}
	for i := range data.Cycles {
		data.Cycles[i].Favorite = favoriteIDs["cycle:"+data.Cycles[i].ID]
	}
	for i := range data.Initiatives {
		data.Initiatives[i].Favorite = favoriteIDs["initiative:"+data.Initiatives[i].ID]
	}
	for i := range data.SavedViews {
		data.SavedViews[i].Favorite = favoriteIDs["view:"+data.SavedViews[i].ID]
	}
	for i := range data.Reviews {
		data.Reviews[i].Favorite = favoriteIDs["review:"+data.Reviews[i].ID] || favoriteIDs["review:"+data.Reviews[i].SlugID]
	}
	data.ImportJobs = slices.DeleteFunc(data.ImportJobs, func(item domain.ImportJob) bool { return item.UserID != userID })
	data.ExportJobs = slices.DeleteFunc(data.ExportJobs, func(item domain.ExportJob) bool { return item.UserID != userID })
	if !isWorkspaceAdminRole(role) {
		data.AuditLog = []domain.AuditLogEntry{}
		data.Trash = slices.DeleteFunc(data.Trash, func(item domain.TrashEntry) bool { return item.DeletedBy.ID != userID })
	}
	stripTrashLinks(&data)
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
	rows, err := s.db.QueryContext(ctx, `SELECT u.id,u.email,u.name,u.display_name,u.avatar_url,u.email_verified_at,u.active,m.role,m.status,m.joined_at,m.last_seen_at,
		COALESCE((SELECT i.subject FROM auth_identities i WHERE i.user_id=u.id AND (i.provider IN ('oidc','saml') OR (i.provider='scim' AND i.issuer=m.workspace_id)) ORDER BY CASE WHEN i.provider='scim' THEN 0 ELSE 1 END,i.created_at,i.id LIMIT 1),''),
		CASE WHEN `+scimManagedMemberSQL+` THEN 1 ELSE 0 END
		FROM workspace_memberships m JOIN auth_users u ON u.id=m.user_id WHERE m.workspace_id=? ORDER BY lower(u.display_name)`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.WorkspaceMember{}
	for rows.Next() {
		var user domain.User
		var nullableEmail, verified, lastSeen sql.NullString
		var active, managed int
		var role, status, joinedRaw string
		if err := rows.Scan(&user.ID, &nullableEmail, &user.Name, &user.DisplayName, &user.AvatarURL, &verified, &active, &role, &status, &joinedRaw, &lastSeen, &user.UserID, &managed); err != nil {
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
		member := domain.WorkspaceMember{User: user, Role: role, Status: status, JoinedAt: joined, LastSeenAt: last}
		if managed == 1 {
			member.ManagedBy = domain.MemberManagedBySCIM
		}
		result = append(result, member)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range result {
		if err := s.decorateAppUser(ctx, &result[i].User); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// actorTeamIDs lists the teams a user belongs to from the membership records
// (one indexed read), for a mutation's authorization callback. A failed read
// gives no teams, so team-based access fails closed.
func (s *SQLiteStore) actorTeamIDs(ctx context.Context, workspaceID, userID string) []string {
	rows, err := s.db.QueryContext(ctx, `SELECT team_id FROM team_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	teams := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			teams = append(teams, id)
		}
	}
	if rows.Err() != nil {
		return nil
	}
	return teams
}

func (s *SQLiteStore) ListTeamMembers(ctx context.Context, workspaceID string) ([]domain.TeamMember, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.team_id,m.user_id,m.role,m.joined_at,
		CASE WHEN EXISTS (SELECT 1 FROM scim_team_memberships sm WHERE sm.workspace_id=m.workspace_id AND sm.team_id=m.team_id AND sm.user_id=m.user_id AND sm.managed=1) THEN 1 ELSE 0 END,
		m.origin
		FROM team_memberships m WHERE m.workspace_id=? ORDER BY m.joined_at`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.TeamMember{}
	for rows.Next() {
		var item domain.TeamMember
		var joined string
		var managed int
		if err := rows.Scan(&item.TeamID, &item.UserID, &item.Role, &joined, &managed, &item.Origin); err != nil {
			return nil, err
		}
		item.JoinedAt, _ = time.Parse(time.RFC3339Nano, joined)
		item.Managed = managed == 1
		item.Origin = domain.TeamMemberOriginOrDefault(item.Origin)
		if item.Managed {
			item.ManagedSource = "scim"
			item.Origin = domain.TeamMemberOriginSCIM
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// LeavingLastActiveTeam reports whether leaving teamID would leave the user in
// no active (neither retired nor deleted) team of the workspace.
func (s *SQLiteStore) LeavingLastActiveTeam(ctx context.Context, workspaceID, teamID, userID string) (bool, error) {
	data, _, ok := s.workspaceByID(workspaceID)
	if !ok {
		return false, ErrAuthForbidden
	}
	active := map[string]bool{}
	for _, team := range data.Teams {
		if team.RetiredAt == nil && team.ArchivedAt == nil {
			active[team.ID] = true
		}
	}
	if !active[teamID] {
		return false, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT team_id FROM team_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, userID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return false, err
		}
		if id != teamID && active[id] {
			return false, nil
		}
	}
	return true, rows.Err()
}

// TeamMembership performs an indexed point read for permission checks on a
// single member. The boolean is false when the user is not in the team.
func (s *SQLiteStore) TeamMembership(ctx context.Context, workspaceID, teamID, userID string) (domain.TeamMember, bool, error) {
	var item domain.TeamMember
	var joined string
	var managed int
	err := s.db.QueryRowContext(ctx, `SELECT m.team_id,m.user_id,m.role,m.joined_at,
		CASE WHEN EXISTS (SELECT 1 FROM scim_team_memberships sm WHERE sm.workspace_id=m.workspace_id AND sm.team_id=m.team_id AND sm.user_id=m.user_id AND sm.managed=1) THEN 1 ELSE 0 END,
		m.origin
		FROM team_memberships m WHERE m.workspace_id=? AND m.team_id=? AND m.user_id=?`, workspaceID, teamID, userID).Scan(&item.TeamID, &item.UserID, &item.Role, &joined, &managed, &item.Origin)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.TeamMember{}, false, nil
	}
	if err != nil {
		return domain.TeamMember{}, false, err
	}
	item.JoinedAt, _ = time.Parse(time.RFC3339Nano, joined)
	item.Managed = managed == 1
	item.Origin = domain.TeamMemberOriginOrDefault(item.Origin)
	if item.Managed {
		item.ManagedSource = "scim"
		item.Origin = domain.TeamMemberOriginSCIM
	}
	return item, true, nil
}

func (s *SQLiteStore) Invite(ctx context.Context, workspaceID, inviterID, email, role string, teamIDs []string) (domain.Invitation, error) {
	email = normalizeEmail(email)
	if !validWorkspaceRole(role) || !strings.Contains(email, "@") || role == "guest" && len(teamIDs) == 0 {
		return domain.Invitation{}, fmt.Errorf("invalid invitation")
	}
	if err := checkTextFields(textField{"email", email, domain.EmailMaxLength}); err != nil {
		return domain.Invitation{}, err
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
	item := domain.Invitation{ID: idgen.New("invite_"), WorkspaceID: workspaceID, Email: email, Role: role, TeamIDs: teamIDs, Status: "pending", InviterID: inviterID, ExpiresAt: now.Add(7 * 24 * time.Hour), CreatedAt: now, Token: token}
	teamRaw, _ := json.Marshal(teamIDs)
	_, err = s.db.ExecContext(ctx, `INSERT INTO workspace_invitations(id,workspace_id,email,role,team_ids,token_hash,inviter_id,status,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, item.ID, workspaceID, email, role, teamRaw, tokenHash(token), inviterID, item.Status, item.ExpiresAt.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	return item, err
}

func (s *SQLiteStore) AcceptInvitation(ctx context.Context, token, userID string) (domain.WorkspaceMembership, error) {
	accepted, err := s.AcceptInvitationWithChanges(ctx, token, userID)
	return accepted.Membership, err
}

// AcceptInvitationWithChanges accepts an invitation and reports the team
// memberships it added (origin invitation) for the audit log.
func (s *SQLiteStore) AcceptInvitationWithChanges(ctx context.Context, token, userID string) (InvitationAcceptance, error) {
	var id, workspaceID, email, role, teamRaw, expiresRaw, status, inviterID string
	err := s.db.QueryRowContext(ctx, `SELECT id,workspace_id,email,role,team_ids,expires_at,status,inviter_id FROM workspace_invitations WHERE token_hash=?`, tokenHash(token)).Scan(&id, &workspaceID, &email, &role, &teamRaw, &expiresRaw, &status, &inviterID)
	if err != nil || status != "pending" {
		return InvitationAcceptance{}, ErrAuthExpired
	}
	expires, _ := time.Parse(time.RFC3339Nano, expiresRaw)
	if time.Now().After(expires) {
		return InvitationAcceptance{}, ErrAuthExpired
	}
	user, err := s.authUserByID(ctx, userID)
	if err != nil || !strings.EqualFold(user.Email, email) {
		return InvitationAcceptance{}, ErrAuthForbidden
	}
	// Read immutable workspace metadata before opening the SQL transaction.
	// Mutations take the store lock before acquiring a database connection, so
	// doing this in the reverse order can deadlock with background schedulers.
	// The workspace (or the teams the invitation names) may have been created
	// on another server moments ago, so read it from the database.
	metadata, _, ok := s.currentWorkspaceByID(ctx, workspaceID)
	if !ok {
		return InvitationAcceptance{}, ErrAuthForbidden
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return InvitationAcceptance{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at,last_seen_at) VALUES(?,?,?,?,?,?) ON CONFLICT(workspace_id,user_id) DO UPDATE SET role=excluded.role,status='active'`, workspaceID, userID, role, "active", now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return InvitationAcceptance{}, err
	}
	var teamIDs []string
	_ = json.Unmarshal([]byte(teamRaw), &teamIDs)
	changes := []domain.TeamMemberChange{}
	for _, teamID := range teamIDs {
		teamRole := "member"
		if role == "owner" {
			teamRole = "owner"
		}
		inserted, err := tx.ExecContext(ctx, `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at,origin) VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING`, workspaceID, teamID, userID, teamRole, now.Format(time.RFC3339Nano), domain.TeamMemberOriginInvitation)
		if err != nil {
			return InvitationAcceptance{}, err
		}
		if count, _ := inserted.RowsAffected(); count > 0 {
			change := domain.TeamMemberChange{TeamID: teamID, UserID: userID, Action: domain.TeamMemberAdded, RoleAfter: teamRole, Origin: domain.TeamMemberOriginInvitation}
			if index := slices.IndexFunc(metadata.Teams, func(team domain.Team) bool { return team.ID == teamID }); index >= 0 {
				change.TeamKey = metadata.Teams[index].Key
			}
			changes = append(changes, change)
		}
		inherited, err := addTeamAncestorMemberships(ctx, tx, metadata, teamID, userID, role, domain.TeamMemberOriginInvitation, false)
		if err != nil {
			return InvitationAcceptance{}, err
		}
		changes = append(changes, inherited...)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workspace_invitations SET status='accepted',accepted_at=? WHERE id=?`, now.Format(time.RFC3339Nano), id); err != nil {
		return InvitationAcceptance{}, err
	}
	if err = tx.Commit(); err != nil {
		return InvitationAcceptance{}, err
	}
	// After the commit: the directory write takes the workspace lock.
	s.syncMemberDirectoryLogged(ctx, metadata.Workspace.URLKey, userID)
	return InvitationAcceptance{Membership: domain.WorkspaceMembership{Workspace: metadata.Workspace, Role: titleRole(role), JoinedAt: now, IssueCount: len(metadata.Issues)}, InvitationID: id, InviterID: inviterID, Changes: changes}, nil
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
	var current string
	if err := s.db.QueryRowContext(ctx, `SELECT role FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, userID).Scan(&current); err != nil {
		return ErrAuthForbidden
	}
	if strings.EqualFold(current, role) {
		return nil
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
	// A member who becomes a guest may lose teams' issues.
	s.accountAccessChanged(userID)
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
	s.accountAccessChanged(userID)
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
	s.syncMemberDirectoryByID(ctx, workspaceID, userID)
	return nil
}

func (s *SQLiteStore) RemoveMember(ctx context.Context, workspaceID, userID string) error {
	_, err := s.RemoveMemberWithChanges(ctx, workspaceID, userID)
	return err
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
		// Created on another server and not loaded here yet.
		data, _, ok = s.currentWorkspaceByID(ctx, item.WorkspaceID)
	}
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

// SetTeamMembership adds (origin manual), updates or removes a team
// membership. ChangeTeamMembership is the full form.
func (s *SQLiteStore) SetTeamMembership(ctx context.Context, workspaceID, teamID, userID, role string, member bool) error {
	_, err := s.ChangeTeamMembership(ctx, TeamMembershipChangeRequest{WorkspaceID: workspaceID, TeamID: teamID, UserID: userID, Role: role, Member: member})
	return err
}

func ensureAnotherTeamOwner(ctx context.Context, tx *sqlTx, workspaceID, teamID, userID string) error {
	var owners int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_memberships WHERE workspace_id=? AND team_id=? AND role='owner' AND user_id<>?`, workspaceID, teamID, userID).Scan(&owners); err != nil {
		return err
	}
	if owners == 0 {
		return ErrLastTeamOwner
	}
	return nil
}

// cleanupTeamMemberData removes assignments and subscriptions that are no
// longer valid after a member leaves a team. Explicit issue shares remain
// intact so a private issue can still be shared intentionally. Only the
// team's issues assigned to or subscribed by the member are loaded (through
// the assignee column and subscriber index), never the whole workspace.
func (s *SQLiteStore) cleanupTeamMemberData(ctx context.Context, workspaceKey, teamID, userID string) error {
	payload := map[string]string{"userId": userID}
	cleanup := func(data *domain.Bootstrap) error {
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
	}
	if fullMutationsForced.Load() {
		return s.MutateWorkspace(ctx, workspaceKey, "team_member.cleaned_up", teamID, payload, cleanup)
	}
	ids, err := s.teamMemberIssueIDs(ctx, workspaceKey, teamID, userID)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return s.recordMetadataEvent(ctx, workspaceKey, "team_member.cleaned_up", teamID, payload)
	}
	const chunk = 500
	for start := 0; start < len(ids); start += chunk {
		scope := WithIssueRecordMutations(ctx, ids[start:min(start+chunk, len(ids))]...)
		if err := s.MutateWorkspace(scope, workspaceKey, "team_member.cleaned_up", teamID, payload, cleanup); err != nil {
			return err
		}
	}
	return nil
}

// teamMemberIssueIDs lists the team's issues assigned to or subscribed by the
// user, using indexed columns only: the assignee index, then the user's
// subscriber index rows narrowed to the team by primary key.
func (s *SQLiteStore) teamMemberIssueIDs(ctx context.Context, workspaceKey, teamID, userID string) ([]string, error) {
	collect := func(query string, args ...any) ([]string, error) {
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, rows.Err()
	}
	ids, err := collect(`SELECT id FROM issue_records WHERE workspace_key=? AND assignee_id=? AND team_id=?`, workspaceKey, userID, teamID)
	if err != nil {
		return nil, err
	}
	subscribed, err := collect(`SELECT issue_id FROM issue_subscriber_records WHERE workspace_key=? AND user_id=?`, workspaceKey, userID)
	if err != nil {
		return nil, err
	}
	for start := 0; start < len(subscribed); start += 500 {
		clause, args := bindList("id", subscribed[start:min(start+500, len(subscribed))])
		inTeam, err := collect(`SELECT id FROM issue_records WHERE workspace_key=? AND team_id=? AND `+clause, append([]any{workspaceKey, teamID}, args...)...)
		if err != nil {
			return nil, err
		}
		ids = append(ids, inTeam...)
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
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
	if err == nil {
		err = s.decorateAppUser(ctx, &user)
	}
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

func directoryExcludedTeam(team domain.Team) bool {
	if team.RetiredAt != nil {
		return true
	}
	return orgDirectoryLeafID(team.ExternalSource) || orgDirectoryLeafID(team.ID) || orgDirectoryLeafID(team.Key)
}

func orgDirectoryLeafID(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	return strings.HasPrefix(id, "hr:org:node:") || strings.HasPrefix(id, "hr:org:dc:")
}

// OmitDirectoryExcludedTeams drops retired and org leaf teams (hr:org:node / hr:org:dc)
// from a bootstrap payload unless the viewer is a direct member. Their workflow
// states and settings go with them so a full org-tree import cannot inflate
// GET /api/issue-records/bootstrap.
func OmitDirectoryExcludedTeams(data *domain.Bootstrap) {
	if data == nil || len(data.Teams) == 0 {
		return
	}
	memberOf := map[string]bool{}
	for _, member := range data.TeamMembers {
		if member.UserID == data.Viewer.ID && data.Viewer.ID != "" {
			memberOf[member.TeamID] = true
		}
	}
	allowed := make(map[string]bool, len(data.Teams))
	for _, team := range data.Teams {
		if team.ArchivedAt != nil || directoryExcludedTeam(team) && !memberOf[team.ID] {
			continue
		}
		allowed[team.ID] = true
	}
	// Memberships of deleted or hidden teams never leave with the payload.
	data.TeamMembers = slices.DeleteFunc(data.TeamMembers, func(member domain.TeamMember) bool { return !allowed[member.TeamID] })
	if len(allowed) == len(data.Teams) {
		return
	}
	filterBootstrapTeams(data, bootstrapTeamScope{Teams: allowed, Content: allowed}, false)
	data.States = slices.DeleteFunc(data.States, func(state domain.WorkflowState) bool {
		return state.TeamID != "" && !allowed[state.TeamID]
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
}

// teamVisibleToUser applies the team access and membership settings before a
// bootstrap projection is returned. Public teams are visible to workspace
// members; private and restricted teams require explicit membership. A parent
// team owner inherits owner access to descendants, but ordinary parent members
// do not bypass a restricted child. Retired and org-leaf teams stay out of
// directory bootstrap unless the viewer is a direct member, including admins.
func teamVisibleToUser(data domain.Bootstrap, teamID, userID, workspaceRole string) bool {
	var team *domain.Team
	for i := range data.Teams {
		if data.Teams[i].ID == teamID {
			team = &data.Teams[i]
			break
		}
	}
	if team == nil || team.ArchivedAt != nil {
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
	if directoryExcludedTeam(*team) {
		return false
	}
	if isWorkspaceAdminRole(workspaceRole) {
		return true
	}
	if workspaceRole == "app" {
		return data.Viewer.ID == userID && data.Viewer.App && data.Viewer.Active && slices.Contains(data.Viewer.AppTeamIDs, teamID)
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
	if access == "private" || strings.EqualFold(settings.MembershipRestriction, "members") || strings.EqualFold(settings.MembershipRestriction, "owners") {
		return false
	}
	// A non-private child of a private team is restricted to that boundary's
	// members; a public-looking child must not expose a private team's content.
	seen := map[string]bool{teamID: true}
	for parent := settings.ParentTeamID; parent != "" && !seen[parent]; parent = data.TeamSettings[parent].ParentTeamID {
		seen[parent] = true
		parentSettings := data.TeamSettings[parent]
		private := strings.EqualFold(parentSettings.Access, "private") || strings.EqualFold(parentSettings.Access, "restricted") && parentSettings.ParentTeamID == ""
		for _, team := range data.Teams {
			if team.ID == parent && team.Private {
				private = true
			}
		}
		if private {
			return slices.ContainsFunc(data.TeamMembers, func(member domain.TeamMember) bool { return member.TeamID == parent && member.UserID == userID })
		}
	}
	return access != "restricted"
}

// bootstrapTeamScope splits what a viewer may see of each team.
type bootstrapTeamScope struct {
	// Teams are the team rows, labels and settings the viewer may see.
	Teams map[string]bool
	// Content are the teams whose issues, projects, documents, cycles and
	// views the viewer may read. It equals Teams except for a workspace admin,
	// who sees and manages every team but reads a private team's content only
	// as a member (the reference app: "As an admin you have access to the team's settings").
	Content map[string]bool
	// AdminClosed, for a workspace admin, are the private teams whose content
	// stays closed to them. Admins otherwise read every issue and document,
	// including those of retired teams left out of Teams.
	AdminClosed map[string]bool
}

// viewerTeamScope derives the content scope from the visible team rows.
func viewerTeamScope(data domain.Bootstrap, teams map[string]bool, userID, role string) bootstrapTeamScope {
	scope := bootstrapTeamScope{Teams: teams, Content: teams}
	if !isWorkspaceAdminRole(role) {
		return scope
	}
	readable := map[string]bool{}
	for _, id := range visibleIssueTeams(data, userID, role) {
		readable[id] = true
	}
	scope.Content, scope.AdminClosed = map[string]bool{}, map[string]bool{}
	for _, team := range data.Teams {
		switch {
		case !readable[team.ID]:
			if team.ArchivedAt == nil {
				scope.AdminClosed[team.ID] = true
			}
		case teams[team.ID]:
			scope.Content[team.ID] = true
		}
	}
	return scope
}

func filterBootstrapTeams(data *domain.Bootstrap, scope bootstrapTeamScope, guest bool) {
	allowed, teamRows := scope.Content, scope.Teams
	archived := map[string]bool{}
	for _, id := range ArchivedTeamIDs(*data) {
		archived[id] = true
	}
	data.Teams = slices.DeleteFunc(data.Teams, func(team domain.Team) bool { return !teamRows[team.ID] })
	data.Issues = slices.DeleteFunc(data.Issues, func(issue domain.Issue) bool {
		if archived[issue.Team.ID] {
			return true
		}
		if allowed[issue.Team.ID] {
			return false
		}
		return !issuePermissionAllows(*data, issue, allowed, scope.AdminClosed)
	})
	visibleIssueIDs := map[string]bool{}
	for _, issue := range data.Issues {
		visibleIssueIDs[issue.ID] = true
	}
	data.IssueSuggestions = slices.DeleteFunc(data.IssueSuggestions, func(suggestion domain.IssueSuggestion) bool {
		return !visibleIssueIDs[suggestion.IssueID]
	})
	// Documents can be scoped to private teams just like issues and projects.
	// Keep unscoped documents workspace-visible, while preventing a member from
	// discovering the title or content of a team document they cannot access.
	// Admins read every document except those only in private teams closed to them.
	//
	// A document attached to an issue or project of a private team is scoped
	// to that team (see DocumentLinkScope): its workspace grant, and the
	// legacy workspace visibility, reach only people who can read the team;
	// user and team grants still share it outside.
	admin := isWorkspaceAdminRole(data.ViewerRole)
	readable := func(teamID string) bool { return allowed[teamID] }
	linkScope := NewDocumentLinkScope(data.Teams, data.TeamSettings)
	data.Documents = slices.DeleteFunc(data.Documents, func(document domain.Document) bool {
		linkOpen := linkScope.Open(document, readable)
		if admin && !slices.ContainsFunc(document.TeamIDs, func(teamID string) bool { return scope.AdminClosed[teamID] }) {
			return !linkOpen && !documentGrantAllows(data, document, allowed, false)
		}
		if len(document.TeamIDs) == 0 {
			// An unscoped document is workspace-visible until explicit
			// document permissions are configured. Once configured, only a
			// matching grant (or the canonical owner) may discover it.
			if len(document.Permissions) == 0 {
				return !linkOpen
			}
			return !documentGrantAllows(data, document, allowed, linkOpen)
		}
		if slices.ContainsFunc(document.TeamIDs, func(teamID string) bool { return allowed[teamID] }) {
			return false
		}
		return !documentGrantAllows(data, document, allowed, linkOpen)
	})
	data.Cycles = slices.DeleteFunc(data.Cycles, func(cycle domain.Cycle) bool { return !allowed[cycle.TeamID] })
	data.Labels = slices.DeleteFunc(data.Labels, func(label domain.IssueLabel) bool {
		scope := strings.ToLower(strings.TrimSpace(label.Scope))
		return scope != "" && scope != "workspace" && !teamRows[label.Scope]
	})
	visibleLabels := map[string]bool{}
	for _, label := range data.Labels {
		visibleLabels[label.ID] = true
	}
	data.LabelGroups = slices.DeleteFunc(data.LabelGroups, func(group domain.LabelGroup) bool {
		scope := strings.ToLower(strings.TrimSpace(group.Scope))
		return scope != "" && scope != "workspace" && !teamRows[group.Scope]
	})
	visibleLabelGroups := map[string]bool{}
	for _, group := range data.LabelGroups {
		visibleLabelGroups[group.ID] = true
	}
	for index := range data.Labels {
		if data.Labels[index].GroupID != "" && !visibleLabelGroups[data.Labels[index].GroupID] {
			data.Labels[index].GroupID = ""
		}
	}
	for index := range data.Issues {
		data.Issues[index].Labels = slices.DeleteFunc(data.Issues[index].Labels, func(label domain.IssueLabel) bool { return !visibleLabels[label.ID] })
	}
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
	data.IssueSuggestions = slices.DeleteFunc(data.IssueSuggestions, func(suggestion domain.IssueSuggestion) bool {
		if suggestion.SuggestedIssueID != "" && !visibleIssueIDs[suggestion.SuggestedIssueID] {
			return true
		}
		if suggestion.SuggestedProjectID != "" && !visibleProjects[suggestion.SuggestedProjectID] {
			return true
		}
		if suggestion.SuggestedTeamID != "" && !allowed[suggestion.SuggestedTeamID] {
			return true
		}
		return false
	})
	data.ProjectRelations = slices.DeleteFunc(data.ProjectRelations, func(relation domain.ProjectRelation) bool {
		return !visibleProjects[relation.ProjectID] || !visibleProjects[relation.RelatedProjectID]
	})
	visibleIssues := map[string]bool{}
	for _, issue := range data.Issues {
		visibleIssues[issue.ID] = true
	}
	visibleDocuments := map[string]bool{}
	for _, document := range data.Documents {
		visibleDocuments[document.ID] = true
		if document.SlugID != "" {
			visibleDocuments[document.SlugID] = true
		}
	}
	// Comments and activity are keyed by their parent id in the persisted
	// workspace snapshot.  They must follow the same projection as their
	// parent resources; otherwise a member of a public team could enumerate
	// comments belonging to a private issue by reading bootstrap data.
	for id := range data.Comments {
		if !visibleIssues[id] && !visibleDocuments[id] {
			delete(data.Comments, id)
		}
	}
	for id := range data.Activities {
		if !visibleIssues[id] {
			delete(data.Activities, id)
		}
	}
	// Shared issues can remain visible even when their parent issue is not.
	// Redact dangling hierarchy and relation references so the projection does
	// not disclose identifiers from private teams.
	for index := range data.Issues {
		issue := &data.Issues[index]
		if issue.ParentID != nil && !visibleIssues[*issue.ParentID] {
			issue.ParentID = nil
		}
		issue.SubIssueIDs = slices.DeleteFunc(issue.SubIssueIDs, func(id string) bool { return !visibleIssues[id] })
		issue.Relations = slices.DeleteFunc(issue.Relations, func(relation domain.IssueRelation) bool {
			return !visibleIssues[relation.RelatedIssueID]
		})
		if issue.Project != nil {
			if !visibleProjects[issue.Project.ID] {
				issue.Project = nil
			}
		}
	}
	data.Reviews = slices.DeleteFunc(data.Reviews, func(review domain.CodeReview) bool {
		if len(review.IssueIDs) == 0 {
			return false
		}
		return !slices.ContainsFunc(review.IssueIDs, func(id string) bool { return visibleIssues[id] })
	})
	for index := range data.Reviews {
		data.Reviews[index].IssueIDs = slices.DeleteFunc(data.Reviews[index].IssueIDs, func(id string) bool { return !visibleIssues[id] })
		data.Reviews[index].TeamReviewers = slices.DeleteFunc(data.Reviews[index].TeamReviewers, func(id string) bool { return !allowed[id] })
	}
	data.IssueSLAs = slices.DeleteFunc(data.IssueSLAs, func(item domain.IssueSLA) bool { return !visibleIssues[item.IssueID] })
	data.SLAEvents = slices.DeleteFunc(data.SLAEvents, func(item domain.SLAEvent) bool { return !visibleIssues[item.IssueID] })
	// SLA rules are workspace settings: they follow the team rows, like labels.
	data.SLARules = slices.DeleteFunc(data.SLARules, func(item domain.SLARule) bool {
		return len(item.TeamIDs) > 0 && !slices.ContainsFunc(item.TeamIDs, func(id string) bool { return teamRows[id] })
	})
	for index := range data.SLARules {
		data.SLARules[index].TeamIDs = slices.DeleteFunc(data.SLARules[index].TeamIDs, func(id string) bool { return !teamRows[id] })
	}
	// Recompute project issue counts from the visible issue projection so a
	// public project shell cannot reveal the size of a private team's backlog.
	projectCounts := map[string]int{}
	for _, issue := range data.Issues {
		if issue.Project != nil {
			projectCounts[issue.Project.ID]++
		}
	}
	for index := range data.Projects {
		data.Projects[index].IssueCount = projectCounts[data.Projects[index].ID]
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
		return release.PipelineID != "" && !visiblePipelines[release.PipelineID]
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
	// disclosing private project identifiers. Track initiatives that are scoped
	// exclusively to hidden resources so their title and update history can be
	// removed as well.
	hiddenInitiatives := map[string]bool{}
	initiativeParents := domain.InitiativeParents(data)
	for _, initiative := range data.Initiatives {
		if initiative.LeadTeamID != "" && !allowed[initiative.LeadTeamID] {
			hiddenInitiatives[initiative.ID] = true
			continue
		}
		hasScopedResource := initiative.LeadTeamID != "" || len(initiative.ContributingTeamIDs) > 0 || len(initiative.ProjectIDs) > 0
		if !hasScopedResource {
			continue
		}
		visible := initiative.LeadTeamID != "" && allowed[initiative.LeadTeamID]
		visible = visible || slices.ContainsFunc(initiative.ContributingTeamIDs, func(id string) bool { return allowed[id] })
		visible = visible || slices.ContainsFunc(initiative.ProjectIDs, func(id string) bool { return visibleProjects[id] })
		if !visible {
			hiddenInitiatives[initiative.ID] = true
		}
	}
	for index := range data.Initiatives {
		data.Initiatives[index].ProjectIDs = slices.DeleteFunc(data.Initiatives[index].ProjectIDs, func(id string) bool { return !visibleProjects[id] })
		data.Initiatives[index].ParentInitiativeIDs = slices.DeleteFunc(initiativeParents[data.Initiatives[index].ID], func(id string) bool {
			return hiddenInitiatives[id] || !slices.ContainsFunc(data.Initiatives, func(item domain.Initiative) bool { return item.ID == id })
		})
		if !allowed[data.Initiatives[index].LeadTeamID] {
			data.Initiatives[index].LeadTeamID = ""
		}
		data.Initiatives[index].ContributingTeamIDs = slices.DeleteFunc(data.Initiatives[index].ContributingTeamIDs, func(id string) bool { return !allowed[id] })
	}
	data.Initiatives = slices.DeleteFunc(data.Initiatives, func(initiative domain.Initiative) bool { return hiddenInitiatives[initiative.ID] })
	visibleInitiatives := map[string]bool{}
	for _, initiative := range data.Initiatives {
		visibleInitiatives[initiative.ID] = true
	}
	for id := range data.InitiativeUpdates {
		if !visibleInitiatives[id] {
			delete(data.InitiativeUpdates, id)
		}
	}
	data.InitiativeRelations = slices.DeleteFunc(data.InitiativeRelations, func(relation domain.InitiativeRelation) bool {
		return !visibleInitiatives[relation.InitiativeID] || !visibleInitiatives[relation.RelatedInitiativeID]
	})
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
	visibleResource := func(kind, id string) bool {
		switch kind {
		case "issue":
			return visibleIssues[id]
		case "project":
			return visibleProjects[id]
		case "initiative":
			return visibleInitiatives[id]
		case "document":
			return visibleDocuments[id]
		case "view":
			return slices.ContainsFunc(data.SavedViews, func(view domain.SavedView) bool { return view.ID == id || view.SlugID == id })
		case "team", "triage":
			return allowed[id]
		case "label":
			return slices.ContainsFunc(data.Labels, func(label domain.IssueLabel) bool { return label.ID == id && label.ArchivedAt == nil })
		case "review":
			return slices.ContainsFunc(data.Reviews, func(review domain.CodeReview) bool { return review.ID == id || review.SlugID == id })
		case "cycle":
			return slices.ContainsFunc(data.Cycles, func(cycle domain.Cycle) bool { return cycle.ID == id })
		case "release":
			return slices.ContainsFunc(data.Releases, func(release domain.Release) bool { return release.ID == id })
		case "release_pipeline":
			return slices.ContainsFunc(data.ReleasePipelines, func(pipeline domain.ReleasePipeline) bool { return pipeline.ID == id })
		case "customer":
			return data.ViewerRole != "guest" && slices.ContainsFunc(data.Customers, func(customer domain.Customer) bool { return customer.ID == id })
		case "dashboard":
			dashboards, _ := data.Settings["dashboards.v1"].([]domain.Dashboard)
			return slices.ContainsFunc(dashboards, func(dashboard domain.Dashboard) bool { return dashboard.ID == id })
		default:
			return false
		}
	}
	data.Favorites = slices.DeleteFunc(data.Favorites, func(item domain.Favorite) bool {
		return item.UserID == data.Viewer.ID && !visibleResource(item.ResourceType, item.ResourceID)
	})
	data.Subscriptions = slices.DeleteFunc(data.Subscriptions, func(item domain.Subscription) bool {
		return item.UserID == data.Viewer.ID && !visibleResource(item.ResourceType, item.ResourceID)
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

// issuePermissionAllows reports whether an issue outside the viewer's content
// teams is shared with them. adminClosed lists private teams whose issues a
// workspace admin reads only through such a grant.
func issuePermissionAllows(data domain.Bootstrap, issue domain.Issue, allowed, adminClosed map[string]bool) bool {
	if isWorkspaceAdminRole(data.ViewerRole) && !adminClosed[issue.Team.ID] {
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
	return documentGrantAllows(data, document, allowed, true)
}

// documentGrantAllows reports whether one of the document's grants reaches
// the viewer; workspace grants count only when workspace is true (the
// viewer passes the document's inherited team scope).
func documentGrantAllows(data *domain.Bootstrap, document domain.Document, allowed map[string]bool, workspace bool) bool {
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
			if workspace && (permission.SubjectID == "" || permission.SubjectID == data.Workspace.ID || permission.SubjectID == data.Workspace.URLKey) {
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

// ViewerVisibilityKeys groups the workspace's active members by what the
// per-viewer projection lets them see: viewers with the same key get the same
// teams, projects, initiatives and documents from PagedWorkspaceMetadata, so
// a caller that only counts shared resources can project once per key
// instead of once per member. Inactive members are left out.
func (s *SQLiteStore) ViewerVisibilityKeys(ctx context.Context, workspace string) (map[string]string, error) {
	data, ok := s.WorkspaceMetadataFields(workspace, "teams", "teamSettings")
	if !ok {
		return nil, ErrAuthForbidden
	}
	members, err := s.ListMembers(ctx, data.Workspace.ID)
	if err != nil {
		return nil, err
	}
	teamMembers, err := s.ListTeamMembers(ctx, data.Workspace.ID)
	if err != nil {
		return nil, err
	}
	// teamVisibleToUser only looks at the viewer's own memberships.
	byUser := map[string][]domain.TeamMember{}
	for _, membership := range teamMembers {
		byUser[membership.UserID] = append(byUser[membership.UserID], membership)
	}
	keys := make(map[string]string, len(members))
	for _, member := range members {
		if member.Status != "active" {
			continue
		}
		data.TeamMembers = byUser[member.User.ID]
		allowed := []string{}
		for _, team := range data.Teams {
			if teamVisibleToUser(data, team.ID, member.User.ID, member.Role) {
				allowed = append(allowed, team.ID)
			}
		}
		slices.Sort(allowed)
		keys[member.User.ID] = member.Role + "|" + strings.Join(allowed, ",")
		if isWorkspaceAdminRole(member.Role) {
			// Admins see every team row but only the private content they joined.
			readable := visibleIssueTeams(data, member.User.ID, member.Role)
			slices.Sort(readable)
			keys[member.User.ID] += "|" + strings.Join(readable, ",")
		}
	}
	return keys, nil
}
