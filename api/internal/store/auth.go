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
	"os"
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

func (s *SQLiteStore) ensureAuthSeed(ctx context.Context) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_users`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	password := os.Getenv("FLOW_SEED_PASSWORD")
	if password == "" {
		password = "flow-demo"
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
	var role string
	err := s.db.QueryRowContext(ctx, `SELECT role FROM team_memberships WHERE workspace_id=? AND team_id=? AND user_id=?`, workspaceID, teamID, userID).Scan(&role)
	if err != nil {
		return "", ErrAuthForbidden
	}
	return role, nil
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
	if role != "admin" || data.WorkspaceSettings.Plan != "enterprise" {
		data.AuditLog = []domain.AuditLogEntry{}
	}
	data.Members, _ = s.ListMembers(ctx, data.Workspace.ID)
	data.TeamMembers, _ = s.ListTeamMembers(ctx, data.Workspace.ID)
	if role == "admin" {
		data.Invitations, _ = s.ListInvitations(ctx, data.Workspace.ID)
	} else {
		data.Invitations = []domain.Invitation{}
	}
	data.Users = make([]domain.User, 0, len(data.Members))
	for _, member := range data.Members {
		data.Users = append(data.Users, member.User)
	}
	allowed := map[string]bool{}
	if role != "guest" {
		for _, team := range data.Teams {
			if !team.Private {
				allowed[team.ID] = true
			}
		}
	}
	for _, member := range data.TeamMembers {
		if member.UserID == userID {
			allowed[member.TeamID] = true
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
	if role != "admin" {
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
		var verified, lastSeen sql.NullString
		var active int
		var role, status, joinedRaw string
		if err := rows.Scan(&user.ID, &user.Email, &user.Name, &user.DisplayName, &user.AvatarURL, &verified, &active, &role, &status, &joinedRaw, &lastSeen); err != nil {
			return nil, err
		}
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
		if _, err = tx.ExecContext(ctx, `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING`, workspaceID, teamID, userID, "member", now.Format(time.RFC3339Nano)); err != nil {
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
	if role != "admin" {
		if err := s.ensureAdminRemains(ctx, workspaceID, userID); err != nil {
			return err
		}
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
	if role != "admin" || status != "active" {
		return nil
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_memberships WHERE workspace_id=? AND role='admin' AND status='active'`, workspaceID).Scan(&count); err != nil {
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
	if !member {
		_, err := s.db.ExecContext(ctx, `DELETE FROM team_memberships WHERE workspace_id=? AND team_id=? AND user_id=?`, workspaceID, teamID, userID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) VALUES(?,?,?,?,?) ON CONFLICT(workspace_id,team_id,user_id) DO UPDATE SET role=excluded.role`, workspaceID, teamID, userID, role, time.Now().UTC().Format(time.RFC3339Nano))
	return err
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
	var verified sql.NullString
	var active int
	err := s.db.QueryRowContext(ctx, `SELECT id,email,name,display_name,avatar_url,email_verified_at,active FROM auth_users WHERE id=?`, id).Scan(&user.ID, &user.Email, &user.Name, &user.DisplayName, &user.AvatarURL, &verified, &active)
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

func filterBootstrapTeams(data *domain.Bootstrap, allowed map[string]bool, guest bool) {
	data.Teams = slices.DeleteFunc(data.Teams, func(team domain.Team) bool { return !allowed[team.ID] })
	data.Issues = slices.DeleteFunc(data.Issues, func(issue domain.Issue) bool { return !allowed[issue.Team.ID] })
	data.Cycles = slices.DeleteFunc(data.Cycles, func(cycle domain.Cycle) bool { return !allowed[cycle.TeamID] })
	data.Projects = slices.DeleteFunc(data.Projects, func(project domain.Project) bool {
		for _, id := range project.TeamIDs {
			if allowed[id] {
				return false
			}
		}
		return true
	})
	visibleProjects := map[string]bool{}
	for _, project := range data.Projects {
		visibleProjects[project.ID] = true
	}
	visibleIssues := map[string]bool{}
	for _, issue := range data.Issues {
		visibleIssues[issue.ID] = true
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
	for id := range data.ProjectUpdates {
		if !visibleProjects[id] {
			delete(data.ProjectUpdates, id)
		}
	}
	data.SavedViews = slices.DeleteFunc(data.SavedViews, func(view domain.SavedView) bool {
		return view.Scope == "team" && !allowed[view.TeamID] || view.Scope == "personal" && view.OwnerID != data.Viewer.ID
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
	return value == "admin" || value == "member" || value == "guest"
}
func titleRole(value string) string {
	if value == "admin" {
		return "Admin"
	}
	if value == "guest" {
		return "Guest"
	}
	return "Member"
}
