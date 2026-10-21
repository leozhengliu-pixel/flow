package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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

// ErrSCIMUniqueness reports a SCIM create or update that would duplicate
// another member's userName, externalId or email (409 uniqueness).
var ErrSCIMUniqueness = errors.New("a member with this userName, externalId or email already exists")

// ErrSCIMNotFound reports a SCIM user that does not exist in the workspace
// (or was deleted through SCIM).
var ErrSCIMNotFound = errors.New("SCIM user not found")

// ErrSCIMInvalid wraps a SCIM value the store rejects (400 invalidValue).
var ErrSCIMInvalid = errors.New("invalid SCIM value")

// SCIM identities removed with DELETE keep their row (and the suspended
// account) under this provider, so the resource reads as gone while a
// later create with the same externalId restores the same account.
const scimDeletedProvider = "scim_deleted"

type SCIMUser struct {
	User       domain.User
	ExternalID string
	UserName   string
	Name       SCIMName
	// ExternalIDDefaulted is true when the IdP sent no externalId and the
	// identity is keyed by userName; the resource then has no externalId.
	ExternalIDDefaulted bool
	Role                string
	// MemberStatus is the workspace membership status ("active",
	// "suspended", or "" when the member was removed from the workspace).
	MemberStatus string
	Profile      SCIMProfile
	// NoDisplayName is set when the IdP sent no displayName.
	NoDisplayName bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
	// Restored and Linked describe what CreateSCIMUser did: restored an
	// account deleted through SCIM earlier, or linked an existing account
	// with the same email.
	Restored, Linked bool
}

// Active is the SCIM active attribute: the account is active and is an
// active member of the workspace.
func (u SCIMUser) Active() bool {
	return u.User.Active && u.MemberStatus == "active"
}

// SCIMName is the SCIM name complex attribute, kept with the SCIM identity
// so IdPs read back what they wrote.
type SCIMName struct {
	Formatted       string `json:"formatted,omitempty"`
	GivenName       string `json:"givenName,omitempty"`
	FamilyName      string `json:"familyName,omitempty"`
	MiddleName      string `json:"middleName,omitempty"`
	HonorificPrefix string `json:"honorificPrefix,omitempty"`
	HonorificSuffix string `json:"honorificSuffix,omitempty"`
}

// FullName is formatted, else givenName familyName.
func (n SCIMName) FullName() string {
	if strings.TrimSpace(n.Formatted) != "" {
		return strings.TrimSpace(n.Formatted)
	}
	return strings.TrimSpace(strings.TrimSpace(n.GivenName) + " " + strings.TrimSpace(n.FamilyName))
}

type scimIdentityClaims struct {
	Name                SCIMName    `json:"name,omitzero"`
	ExternalIDDefaulted bool        `json:"externalIdDefaulted,omitempty"`
	Profile             SCIMProfile `json:"profile,omitzero"`
	// NoDisplayName records that the IdP set no displayName (the account
	// still shows one derived from the name), so the resource omits it.
	NoDisplayName bool `json:"noDisplayName,omitempty"`
}

// SCIMUserInput is a complete SCIM user write. Role is applied only when
// RoleExplicit is set or the member has no role yet, so profile updates do
// not reset a role an IdP assigned earlier.
type SCIMUserInput struct {
	ExternalID   string
	UserName     string
	DisplayName  string
	Name         SCIMName
	Email        string
	AvatarURL    string
	Role         string
	RoleExplicit bool
	Active       bool
	Profile      SCIMProfile
	// KeepRemoved leaves an inactive user that has no workspace membership
	// (removed on deprovisioning) out of the workspace instead of adding
	// them back as a suspended member.
	KeepRemoved bool
	// ClearExternalID removes the externalId (PATCH remove): the identity
	// is keyed by userName again.
	ClearExternalID bool
}

// SCIMUserQuery narrows a SCIM user listing to one exact attribute value
// (userName and email compare case-insensitively). Limit 0 counts only.
type SCIMUserQuery struct {
	Attribute string // "", "userName", "externalId", "id", "email", "displayName"
	Value     string
	// SortBy orders the page: "" or "userName", "displayName", "externalId",
	// "email", "id", "created", "lastModified". Missing values sort last
	// ascending and first descending.
	SortBy     string
	Descending bool
	Offset     int
	Limit      int
}

// SCIMUserSortColumns are the sortBy values ListSCIMUsers orders in SQL.
var SCIMUserSortColumns = map[string]bool{"userName": true, "displayName": true, "externalId": true, "email": true, "id": true, "created": true, "lastModified": true}

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
		`CREATE TABLE IF NOT EXISTS scim_team_memberships (workspace_id VARCHAR(191) NOT NULL, team_id VARCHAR(191) NOT NULL, group_id VARCHAR(191) NOT NULL, user_id VARCHAR(191) NOT NULL, managed INTEGER NOT NULL DEFAULT 1, updated_at VARCHAR(40) NOT NULL, PRIMARY KEY(group_id,user_id))`,
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
		"scim_team_memberships_team_user_idx ON scim_team_memberships(workspace_id,team_id,user_id)",
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
	if data, ok := s.WorkspaceMetadata(strings.TrimSpace(workspaceKey)); ok {
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

const scimUserColumns = `i.subject,i.username,i.claims_json,u.id,u.email,u.name,u.display_name,u.avatar_url,u.email_verified_at,u.active,u.created_at,u.updated_at,COALESCE(m.role,''),COALESCE(m.status,'')`

// A SCIM user stays a resource after the deprovisioning setting "remove from
// workspace" removed the membership (active false), so the join is outer.
const scimUserFrom = ` FROM auth_identities i JOIN auth_users u ON u.id=i.user_id LEFT JOIN workspace_memberships m ON m.user_id=i.user_id AND m.workspace_id=? WHERE i.provider='scim' AND i.issuer=?`

type scimRowScanner interface{ Scan(...any) error }

func scanSCIMUser(row scimRowScanner) (SCIMUser, error) {
	var item SCIMUser
	var claims string
	var email, verified sql.NullString
	var active int
	var created, updated string
	if err := row.Scan(&item.ExternalID, &item.UserName, &claims, &item.User.ID, &email, &item.User.Name, &item.User.DisplayName, &item.User.AvatarURL, &verified, &active, &created, &updated, &item.Role, &item.MemberStatus); err != nil {
		return SCIMUser{}, err
	}
	item.User.Email = email.String
	item.User.Active, item.User.EmailVerified = active == 1, verified.Valid
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	var parsed scimIdentityClaims
	if json.Unmarshal([]byte(claims), &parsed) == nil {
		item.Name, item.ExternalIDDefaulted, item.Profile, item.NoDisplayName = parsed.Name, parsed.ExternalIDDefaulted, parsed.Profile, parsed.NoDisplayName
	}
	return item, nil
}

// ListSCIMUsers pages the workspace's SCIM users ordered by userName. An
// exact-match query uses the identity and user indexes instead of a scan.
func (s *SQLiteStore) ListSCIMUsers(ctx context.Context, workspaceID string, query SCIMUserQuery) ([]SCIMUser, int, error) {
	where, args := "", []any{workspaceID, workspaceID}
	value := strings.TrimSpace(query.Value)
	switch query.Attribute {
	case "":
	case "userName":
		where, args = ` AND lower(i.username)=?`, append(args, strings.ToLower(value))
	case "externalId":
		where, args = ` AND `+s.scimExactEquals("i.subject"), append(args, value)
	case "id":
		where, args = ` AND u.id=?`, append(args, value)
	case "email":
		where, args = ` AND u.email=?`, append(args, normalizeEmail(value))
	case "displayName":
		where, args = ` AND lower(u.display_name)=?`, append(args, strings.ToLower(value))
	default:
		return nil, 0, fmt.Errorf("unsupported SCIM user query attribute %q", query.Attribute)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)`+scimUserFrom+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	result := []SCIMUser{}
	if query.Limit <= 0 || total == 0 {
		return result, total, nil
	}
	order, err := scimUserOrder(query.SortBy, query.Descending)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+scimUserColumns+scimUserFrom+where+` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(args, query.Limit, max(query.Offset, 0))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanSCIMUser(rows)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, item)
	}
	return result, total, rows.Err()
}

// scimUserOrder is the ORDER BY clause for a sortBy value, with a unique
// tie-breaker so pages are stable. An externalId the IdP did not send (the
// identity is keyed by userName) is missing, so it sorts last ascending.
func scimUserOrder(sortBy string, descending bool) (string, error) {
	direction := ""
	if descending {
		direction = " DESC"
	}
	var columns []string
	switch sortBy {
	case "", "userName":
		columns = []string{"lower(i.username)"}
	case "displayName":
		columns = []string{"lower(u.display_name)"}
	case "externalId":
		columns = []string{`CASE WHEN i.claims_json LIKE '%"externalIdDefaulted":true%' THEN 1 ELSE 0 END`, "i.subject"}
	case "email":
		columns = []string{"CASE WHEN u.email IS NULL THEN 1 ELSE 0 END", "u.email"}
	case "id":
		columns = []string{"u.id"}
	case "created":
		columns = []string{"u.created_at"}
	case "lastModified":
		columns = []string{"u.updated_at"}
	default:
		return "", fmt.Errorf("unsupported SCIM user sort %q", sortBy)
	}
	for index := range columns {
		columns[index] += direction
	}
	return strings.Join(append(columns, "i.subject"+direction), ","), nil
}

// scimExactEquals compares column with a bound value byte for byte: externalId is case-exact in SCIM, and MySQL's
// default collation would otherwise match "EXT-3" to "ext-3".
func (s *SQLiteStore) scimExactEquals(column string) string {
	return s.metadataRecordKeyEquals(column, "?")
}

// SCIMUser finds a SCIM user by Flow user id or externalId.
func (s *SQLiteStore) SCIMUser(ctx context.Context, workspaceID, id string) (SCIMUser, error) {
	item, err := scanSCIMUser(s.db.QueryRowContext(ctx, `SELECT `+scimUserColumns+scimUserFrom+` AND (u.id=? OR `+s.scimExactEquals("i.subject")+`) ORDER BY CASE WHEN u.id=? THEN 0 ELSE 1 END LIMIT 1`, workspaceID, workspaceID, id, id, id))
	if errors.Is(err, sql.ErrNoRows) {
		return SCIMUser{}, ErrSCIMNotFound
	}
	return item, err
}

// ProvisionSCIMUser creates the user for externalID, or updates it when it
// already exists. role is always applied.
func (s *SQLiteStore) ProvisionSCIMUser(ctx context.Context, workspaceID, externalID, username, displayName, email, avatarURL, role string, active bool) (SCIMUser, error) {
	input := SCIMUserInput{ExternalID: externalID, UserName: username, DisplayName: displayName, Email: email, AvatarURL: avatarURL, Role: role, RoleExplicit: true, Active: active}
	subject := strings.TrimSpace(externalID)
	if subject == "" {
		subject = strings.TrimSpace(username)
	}
	if existing, err := s.SCIMUser(ctx, workspaceID, subject); err == nil {
		return s.UpdateSCIMUser(ctx, workspaceID, existing.User.ID, input)
	}
	return s.CreateSCIMUser(ctx, workspaceID, input)
}

// CreateSCIMUser provisions a member (POST /Users). An existing account
// with the same email is linked rather than duplicated, keeping its
// workspace role unless the IdP sends one. A userName or externalId already
// provisioned in the workspace is ErrSCIMUniqueness. An externalId deleted
// through SCIM earlier restores that account.
func (s *SQLiteStore) CreateSCIMUser(ctx context.Context, workspaceID string, input SCIMUserInput) (SCIMUser, error) {
	input = normalizeSCIMUserInput(input)
	if input.UserName == "" {
		return SCIMUser{}, fmt.Errorf("%w: userName is required", ErrSCIMInvalid)
	}
	subject, defaulted := input.ExternalID, false
	if subject == "" {
		subject, defaulted = input.UserName, true
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_identities WHERE provider='scim' AND issuer=? AND (`+s.scimExactEquals("subject")+` OR lower(username)=?)`, workspaceID, subject, strings.ToLower(input.UserName)).Scan(&count); err != nil {
		return SCIMUser{}, err
	}
	if count > 0 {
		return SCIMUser{}, ErrSCIMUniqueness
	}
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	claims := scimClaimsJSON(input, defaulted)
	// The account is a SCIM user deleted earlier (restored), an existing
	// account with the same email (linked), or new.
	var userID string
	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM auth_identities WHERE provider=? AND issuer=? AND `+s.scimExactEquals("subject"), scimDeletedProvider, workspaceID, subject).Scan(&userID)
	restored, linked := err == nil, false
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return SCIMUser{}, err
	}
	if !restored && input.Email != "" {
		if err := s.db.QueryRowContext(ctx, `SELECT id FROM auth_users WHERE email=?`, input.Email).Scan(&userID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return SCIMUser{}, err
		}
		if userID != "" {
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_identities WHERE provider='scim' AND issuer=? AND user_id=?`, workspaceID, userID).Scan(&count); err != nil {
				return SCIMUser{}, err
			}
			if count > 0 {
				return SCIMUser{}, ErrSCIMUniqueness
			}
			linked = true
			var role string
			if err := s.db.QueryRowContext(ctx, `SELECT role FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, userID).Scan(&role); err == nil && !input.RoleExplicit {
				input.Role = role
			}
		}
	}
	if restored && input.Email != "" {
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_users WHERE email=? AND id<>?`, input.Email, userID).Scan(&count); err != nil {
			return SCIMUser{}, err
		}
		if count > 0 {
			return SCIMUser{}, ErrSCIMUniqueness
		}
	}
	if userID == "" {
		userID = fmt.Sprintf("usr_%d", now.UnixNano())
		_, err = s.db.ExecContext(ctx, `INSERT INTO auth_users(id,email,name,display_name,avatar_url,password_hash,email_verified_at,active,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, userID, nullableString(input.Email), scimFullName(input), scimDisplayName(input), input.AvatarURL, "", nullableTime(input.Active && input.Email != "", now), boolInt(input.Active), stamp, stamp)
	} else {
		_, err = s.db.ExecContext(ctx, `UPDATE auth_users SET email=?,name=?,display_name=?,active=?,updated_at=? WHERE id=?`, nullableString(input.Email), scimFullName(input), scimDisplayName(input), boolInt(input.Active), stamp, userID)
	}
	if err != nil {
		return SCIMUser{}, err
	}
	if restored {
		_, err = s.db.ExecContext(ctx, `UPDATE auth_identities SET provider='scim',username=?,claims_json=? WHERE provider=? AND issuer=? AND `+s.scimExactEquals("subject"), input.UserName, claims, scimDeletedProvider, workspaceID, subject)
	} else {
		_, err = s.db.ExecContext(ctx, `INSERT INTO auth_identities(id,user_id,provider,issuer,subject,identity_key,username,claims_json,created_at,last_login_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, fmt.Sprintf("identity_%d", now.UnixNano()), userID, "scim", workspaceID, subject, externalIdentityKey("scim", workspaceID, subject), input.UserName, claims, stamp, stamp)
	}
	if err != nil {
		return SCIMUser{}, err
	}
	// A restored account keeps the role it had unless the IdP sends one.
	input.RoleExplicit = input.RoleExplicit || !restored
	item, err := s.writeSCIMUser(ctx, workspaceID, userID, input, now)
	item.Restored, item.Linked = restored, linked
	return item, err
}

// UpdateSCIMUser replaces a SCIM user's attributes (PUT, or the result of a
// PATCH). Taking another member's userName or email is ErrSCIMUniqueness.
func (s *SQLiteStore) UpdateSCIMUser(ctx context.Context, workspaceID, userID string, input SCIMUserInput) (SCIMUser, error) {
	input = normalizeSCIMUserInput(input)
	current, err := s.SCIMUser(ctx, workspaceID, userID)
	if err != nil {
		return SCIMUser{}, err
	}
	if input.UserName == "" {
		return SCIMUser{}, fmt.Errorf("%w: userName is required", ErrSCIMInvalid)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_identities WHERE provider='scim' AND issuer=? AND lower(username)=? AND user_id<>?`, workspaceID, strings.ToLower(input.UserName), current.User.ID).Scan(&count); err != nil {
		return SCIMUser{}, err
	}
	if count == 0 && input.Email != "" {
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_users WHERE email=? AND id<>?`, input.Email, current.User.ID).Scan(&count); err != nil {
			return SCIMUser{}, err
		}
	}
	if count > 0 {
		return SCIMUser{}, ErrSCIMUniqueness
	}
	// externalId is readWrite: a new value re-keys the identity, and
	// removing it keys the identity by userName again. Either must stay
	// unique among the workspace's SCIM identities, deleted ones included
	// (they restore by externalId).
	subject, defaulted := current.ExternalID, current.ExternalIDDefaulted
	switch {
	case input.ExternalID != "" && (input.ExternalID != current.ExternalID || current.ExternalIDDefaulted):
		subject, defaulted = input.ExternalID, false
	case input.ClearExternalID && !current.ExternalIDDefaulted:
		subject, defaulted = input.UserName, true
	}
	if subject != current.ExternalID {
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_identities WHERE provider IN ('scim',?) AND issuer=? AND user_id<>? AND `+s.scimExactEquals("subject"), scimDeletedProvider, workspaceID, current.User.ID, subject).Scan(&count); err != nil {
			return SCIMUser{}, err
		}
		if count > 0 {
			return SCIMUser{}, ErrSCIMUniqueness
		}
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE auth_identities SET subject=?,identity_key=?,username=?,claims_json=? WHERE provider='scim' AND issuer=? AND `+s.scimExactEquals("subject"), subject, externalIdentityKey("scim", workspaceID, subject), input.UserName, scimClaimsJSON(input, defaulted), workspaceID, current.ExternalID); err != nil {
		return SCIMUser{}, err
	}
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `UPDATE auth_users SET email=?,name=?,display_name=?,avatar_url=?,active=?,updated_at=? WHERE id=?`, nullableString(input.Email), scimFullName(input), scimDisplayName(input), input.AvatarURL, boolInt(input.Active), now.Format(time.RFC3339Nano), current.User.ID); err != nil {
		return SCIMUser{}, err
	}
	return s.writeSCIMUser(ctx, workspaceID, current.User.ID, input, now)
}

// writeSCIMUser applies membership status and role after the account and
// identity rows are written.
func (s *SQLiteStore) writeSCIMUser(ctx context.Context, workspaceID, userID string, input SCIMUserInput, now time.Time) (SCIMUser, error) {
	role := strings.ToLower(strings.TrimSpace(input.Role))
	if !input.RoleExplicit || !validSCIMRole(role) {
		var existing string
		err := s.db.QueryRowContext(ctx, `SELECT role FROM scim_user_roles WHERE workspace_id=? AND user_id=?`, workspaceID, userID).Scan(&existing)
		switch {
		case err == nil:
			role = existing
		case !errors.Is(err, sql.ErrNoRows):
			return SCIMUser{}, err
		case !validSCIMRole(role):
			role = "member"
		}
	}
	status := "suspended"
	if input.Active {
		status = "active"
	}
	if !input.Active && input.KeepRemoved {
		var memberships int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, userID).Scan(&memberships); err != nil {
			return SCIMUser{}, err
		}
		if memberships == 0 {
			if err := s.setSCIMUserRole(ctx, workspaceID, userID, role, now); err != nil {
				return SCIMUser{}, err
			}
			return s.SCIMUser(ctx, workspaceID, userID)
		}
	}
	stamp := now.Format(time.RFC3339Nano)
	// Provisioning is not activity: last_seen_at stays unset (or as it was), so a provisioned member is not shown online.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at,last_seen_at) VALUES(?,?,?,?,?,NULL) ON CONFLICT(workspace_id,user_id) DO UPDATE SET role=excluded.role,status=excluded.status`, workspaceID, userID, role, status, stamp); err != nil {
		return SCIMUser{}, err
	}
	if err := s.setSCIMUserRole(ctx, workspaceID, userID, role, now); err != nil {
		return SCIMUser{}, err
	}
	if err := s.recomputeSCIMUserRole(ctx, workspaceID, userID); err != nil {
		return SCIMUser{}, err
	}
	return s.SCIMUser(ctx, workspaceID, userID)
}

func normalizeSCIMUserInput(input SCIMUserInput) SCIMUserInput {
	input.ExternalID, input.UserName, input.DisplayName = strings.TrimSpace(input.ExternalID), strings.TrimSpace(input.UserName), strings.TrimSpace(input.DisplayName)
	name := &input.Name
	for _, value := range []*string{&name.Formatted, &name.GivenName, &name.FamilyName, &name.MiddleName, &name.HonorificPrefix, &name.HonorificSuffix} {
		*value = strings.TrimSpace(*value)
	}
	input.Profile = normalizeSCIMProfile(input.Profile)
	input.Email, input.AvatarURL = normalizeEmail(input.Email), strings.TrimSpace(input.AvatarURL)
	if input.Email == "" && strings.Contains(input.UserName, "@") {
		input.Email = normalizeEmail(input.UserName)
	}
	return input
}

// scimFullName is the account name: name.formatted, else given and family
// name, else displayName, else userName.
func scimFullName(input SCIMUserInput) string {
	for _, value := range []string{input.Name.FullName(), input.DisplayName, input.UserName} {
		if value != "" {
			return value
		}
	}
	return input.UserName
}

func scimDisplayName(input SCIMUserInput) string {
	if input.DisplayName != "" {
		return input.DisplayName
	}
	return scimFullName(input)
}

func scimClaimsJSON(input SCIMUserInput, externalIDDefaulted bool) string {
	raw, _ := json.Marshal(scimIdentityClaims{Name: input.Name, ExternalIDDefaulted: externalIDDefaulted, Profile: input.Profile, NoDisplayName: input.DisplayName == ""})
	return string(raw)
}

// DeprovisionSCIMUser handles DELETE /Users/{id}: the account is suspended
// in the workspace (never deleted, so its history stays attributed), its
// SCIM group memberships end, and the SCIM resource reads as gone. It
// returns the groups the user left so callers can resync linked teams.
func (s *SQLiteStore) DeprovisionSCIMUser(ctx context.Context, workspaceID, id string) ([]string, error) {
	item, err := s.SCIMUser(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT group_id FROM scim_group_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, item.User.ID)
	if err != nil {
		return nil, err
	}
	groups := []string{}
	for rows.Next() {
		var groupID string
		if err := rows.Scan(&groupID); err != nil {
			rows.Close()
			return nil, err
		}
		groups = append(groups, groupID)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	statements := []struct {
		query string
		args  []any
	}{
		{`UPDATE auth_users SET active=0,updated_at=? WHERE id=?`, []any{stamp, item.User.ID}},
		{`UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, []any{workspaceID, item.User.ID}},
		{`DELETE FROM scim_group_memberships WHERE workspace_id=? AND user_id=?`, []any{workspaceID, item.User.ID}},
		{`UPDATE auth_identities SET provider=? WHERE provider='scim' AND issuer=? AND ` + s.scimExactEquals("subject"), []any{scimDeletedProvider, workspaceID, item.ExternalID}},
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return nil, err
		}
	}
	return groups, tx.Commit()
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

// ErrSCIMMemberNotFound reports a group member value that is not a SCIM
// user of the workspace (400 invalidValue).
var ErrSCIMMemberNotFound = errors.New("SCIM group member not found")

// CreateSCIMGroup persists a group pushed by an IdP. Groups never create Flow
// teams; role is assigned by the caller from the workspace role-group mapping.
// A second group with the same displayName or externalId is
// ErrSCIMUniqueness.
func (s *SQLiteStore) CreateSCIMGroup(ctx context.Context, workspaceID, externalID, displayName, role string) (SCIMGroup, error) {
	externalID, displayName, role = strings.TrimSpace(externalID), strings.TrimSpace(displayName), strings.ToLower(strings.TrimSpace(role))
	if displayName == "" {
		return SCIMGroup{}, fmt.Errorf("%w: SCIM group displayName is required", ErrSCIMInvalid)
	}
	if role != "" && !validSCIMRole(role) {
		return SCIMGroup{}, fmt.Errorf("%w: invalid SCIM group role", ErrSCIMInvalid)
	}
	if err := s.checkSCIMGroupUnique(ctx, workspaceID, "", externalID, displayName); err != nil {
		return SCIMGroup{}, err
	}
	now := time.Now().UTC()
	id := fmt.Sprintf("scimg_%d", now.UnixNano())
	_, err := s.db.ExecContext(ctx, `INSERT INTO scim_groups(id,workspace_id,external_id,display_name,role,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, id, workspaceID, externalID, displayName, role, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return SCIMGroup{}, err
	}
	return s.SCIMGroup(ctx, workspaceID, id)
}

func (s *SQLiteStore) checkSCIMGroupUnique(ctx context.Context, workspaceID, groupID, externalID, displayName string) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scim_groups WHERE workspace_id=? AND id<>? AND (lower(display_name)=? OR (external_id<>'' AND `+s.scimExactEquals("external_id")+`))`, workspaceID, groupID, strings.ToLower(displayName), externalID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrSCIMUniqueness
	}
	return nil
}

func (s *SQLiteStore) SCIMGroup(ctx context.Context, workspaceID, id string) (SCIMGroup, error) {
	var group SCIMGroup
	var created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id,workspace_id,external_id,display_name,role,created_at,updated_at FROM scim_groups WHERE workspace_id=? AND (id=? OR `+s.scimExactEquals("external_id")+`) ORDER BY CASE WHEN id=? THEN 0 ELSE 1 END LIMIT 1`, workspaceID, id, id, id).Scan(&group.ID, &group.WorkspaceID, &group.ExternalID, &group.DisplayName, &group.Role, &created, &updated)
	if err != nil {
		return SCIMGroup{}, err
	}
	group.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	group.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	members, err := s.scimGroupMembers(ctx, workspaceID, group.ID)
	group.Members = members[group.ID]
	return group, err
}

// scimGroupMembers loads members of one group, or of every group in the
// workspace when groupID is empty, keyed by group id.
func (s *SQLiteStore) scimGroupMembers(ctx context.Context, workspaceID, groupID string) (map[string][]SCIMGroupMember, error) {
	query := `SELECT m.group_id,m.user_id,COALESCE(i.subject,''),m.updated_at FROM scim_group_memberships m LEFT JOIN auth_identities i ON i.user_id=m.user_id AND i.provider='scim' AND i.issuer=? WHERE m.workspace_id=?`
	args := []any{workspaceID, workspaceID}
	if groupID != "" {
		query += ` AND m.group_id=?`
		args = append(args, groupID)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY m.updated_at,m.user_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string][]SCIMGroupMember{}
	for rows.Next() {
		var member SCIMGroupMember
		var group, stamp string
		if err := rows.Scan(&group, &member.UserID, &member.ExternalID, &stamp); err != nil {
			return nil, err
		}
		member.UpdatedAt, _ = time.Parse(time.RFC3339Nano, stamp)
		result[group] = append(result[group], member)
	}
	return result, rows.Err()
}

// ListSCIMGroups lists the workspace's SCIM groups by display name. Members
// are loaded (in one query) only when withMembers is set.
func (s *SQLiteStore) ListSCIMGroups(ctx context.Context, workspaceID string, withMembers bool) ([]SCIMGroup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,workspace_id,external_id,display_name,role,created_at,updated_at FROM scim_groups WHERE workspace_id=? ORDER BY lower(display_name),id`, workspaceID)
	if err != nil {
		return nil, err
	}
	groups := []SCIMGroup{}
	for rows.Next() {
		var group SCIMGroup
		var created, updated string
		if err := rows.Scan(&group.ID, &group.WorkspaceID, &group.ExternalID, &group.DisplayName, &group.Role, &created, &updated); err != nil {
			rows.Close()
			return nil, err
		}
		group.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		group.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		groups = append(groups, group)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if !withMembers || len(groups) == 0 {
		return groups, nil
	}
	members, err := s.scimGroupMembers(ctx, workspaceID, "")
	if err != nil {
		return nil, err
	}
	for index := range groups {
		groups[index].Members = members[groups[index].ID]
	}
	return groups, nil
}

// UpdateSCIMGroup sets a group's externalId (kept when empty), displayName
// (kept when empty) and role ("" clears it), then recomputes members' roles.
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
	if role != "" && !validSCIMRole(role) {
		return SCIMGroup{}, fmt.Errorf("%w: invalid SCIM group role", ErrSCIMInvalid)
	}
	group.Role = role
	if err := s.checkSCIMGroupUnique(ctx, workspaceID, group.ID, group.ExternalID, group.DisplayName); err != nil {
		return SCIMGroup{}, err
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

// ClearSCIMGroupExternalID removes a group's externalId (PATCH remove).
func (s *SQLiteStore) ClearSCIMGroupExternalID(ctx context.Context, workspaceID, id string) (SCIMGroup, error) {
	if _, err := s.db.ExecContext(ctx, `UPDATE scim_groups SET external_id='',updated_at=? WHERE workspace_id=? AND id=?`, time.Now().UTC().Format(time.RFC3339Nano), workspaceID, id); err != nil {
		return SCIMGroup{}, err
	}
	return s.SCIMGroup(ctx, workspaceID, id)
}

// DeleteSCIMGroup removes a group and its memberships, recomputes its
// members' roles, and unlinks the team memberships it managed: they stay on
// the team as ordinary memberships.
func (s *SQLiteStore) DeleteSCIMGroup(ctx context.Context, workspaceID, id string) error {
	group, err := s.SCIMGroup(ctx, workspaceID, id)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`DELETE FROM scim_group_memberships WHERE workspace_id=? AND group_id=?`,
		`DELETE FROM scim_team_memberships WHERE workspace_id=? AND group_id=?`,
		`DELETE FROM scim_groups WHERE workspace_id=? AND id=?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, workspaceID, group.ID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, member := range group.Members {
		if err := s.recomputeSCIMUserRole(ctx, workspaceID, member.UserID); err != nil {
			return err
		}
	}
	return nil
}

// resolveSCIMMembers maps member values (Flow user ids or externalIds) to
// user ids, dropping duplicates. Unknown values are ErrSCIMMemberNotFound
// unless lenient.
func (s *SQLiteStore) resolveSCIMMembers(ctx context.Context, workspaceID string, values []string, lenient bool) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		member, err := s.SCIMUser(ctx, workspaceID, value)
		if errors.Is(err, ErrSCIMNotFound) {
			if lenient {
				continue
			}
			return nil, fmt.Errorf("%w: %q", ErrSCIMMemberNotFound, value)
		}
		if err != nil {
			return nil, err
		}
		if !seen[member.User.ID] {
			seen[member.User.ID] = true
			result = append(result, member.User.ID)
		}
	}
	return result, nil
}

// ReplaceSCIMGroupMembers sets a group's complete member list (PUT, or a
// PATCH replace of members). Members already in the group keep their
// membership timestamp, so re-sending the same list does not change which
// role group is the latest for anyone.
func (s *SQLiteStore) ReplaceSCIMGroupMembers(ctx context.Context, workspaceID, id string, userIDs []string) (SCIMGroup, error) {
	group, err := s.SCIMGroup(ctx, workspaceID, id)
	if err != nil {
		return SCIMGroup{}, err
	}
	next, err := s.resolveSCIMMembers(ctx, workspaceID, userIDs, false)
	if err != nil {
		return SCIMGroup{}, err
	}
	keep := map[string]bool{}
	for _, userID := range next {
		keep[userID] = true
	}
	remove := []string{}
	for _, member := range group.Members {
		if !keep[member.UserID] {
			remove = append(remove, member.UserID)
		}
	}
	return s.applySCIMGroupMembers(ctx, workspaceID, group, next, remove)
}

// PatchSCIMGroupMembers adds and removes members in one step. Adding a
// current member or removing a non-member changes nothing; removing an
// unknown user is ignored.
func (s *SQLiteStore) PatchSCIMGroupMembers(ctx context.Context, workspaceID, id string, add, remove []string) (SCIMGroup, error) {
	group, err := s.SCIMGroup(ctx, workspaceID, id)
	if err != nil {
		return SCIMGroup{}, err
	}
	added, err := s.resolveSCIMMembers(ctx, workspaceID, add, false)
	if err != nil {
		return SCIMGroup{}, err
	}
	removed, err := s.resolveSCIMMembers(ctx, workspaceID, remove, true)
	if err != nil {
		return SCIMGroup{}, err
	}
	return s.applySCIMGroupMembers(ctx, workspaceID, group, added, removed)
}

func (s *SQLiteStore) applySCIMGroupMembers(ctx context.Context, workspaceID string, group SCIMGroup, add, remove []string) (SCIMGroup, error) {
	current := map[string]bool{}
	for _, member := range group.Members {
		current[member.UserID] = true
	}
	affected := []string{}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SCIMGroup{}, err
	}
	defer tx.Rollback()
	for _, userID := range remove {
		if !current[userID] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM scim_group_memberships WHERE workspace_id=? AND group_id=? AND user_id=?`, workspaceID, group.ID, userID); err != nil {
			return SCIMGroup{}, err
		}
		delete(current, userID)
		affected = append(affected, userID)
	}
	for _, userID := range add {
		if current[userID] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO scim_group_memberships(workspace_id,group_id,user_id,updated_at) VALUES(?,?,?,?)`, workspaceID, group.ID, userID, stamp); err != nil {
			return SCIMGroup{}, err
		}
		current[userID] = true
		affected = append(affected, userID)
	}
	if len(affected) > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE scim_groups SET updated_at=? WHERE workspace_id=? AND id=?`, stamp, workspaceID, group.ID); err != nil {
			return SCIMGroup{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return SCIMGroup{}, err
	}
	for _, userID := range affected {
		if err := s.recomputeSCIMUserRole(ctx, workspaceID, userID); err != nil {
			return SCIMGroup{}, err
		}
	}
	return s.SCIMGroup(ctx, workspaceID, group.ID)
}

// SyncSCIMGroupTeam mirrors a mapped IdP group into Flow team membership.
// Memberships introduced by SCIM are tracked separately so removing a user
// from the IdP group does not remove a manually managed team membership.
func (s *SQLiteStore) SyncSCIMGroupTeam(ctx context.Context, workspaceID, groupID, teamID string) error {
	_, err := s.SyncSCIMGroupTeamChanges(ctx, workspaceID, groupID, teamID)
	return err
}

// SyncSCIMGroupTeamChanges is SyncSCIMGroupTeam that also returns the team
// memberships it added (origin scim) and removed, for the audit log. It only
// removes memberships SCIM added itself (managed), and never a team owner.
func (s *SQLiteStore) SyncSCIMGroupTeamChanges(ctx context.Context, workspaceID, groupID, teamID string) ([]domain.TeamMemberChange, error) {
	teamID, groupID = strings.TrimSpace(teamID), strings.TrimSpace(groupID)
	if workspaceID == "" || groupID == "" || teamID == "" {
		return nil, nil
	}
	group, err := s.SCIMGroup(ctx, workspaceID, groupID)
	if err != nil {
		return nil, err
	}
	teamKey := ""
	if data, _, ok := s.workspaceByID(workspaceID); ok {
		if index := slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.ID == teamID }); index >= 0 {
			teamKey = data.Teams[index].Key
		}
	}
	changes := []domain.TeamMemberChange{}
	nextMembers := map[string]bool{}
	for _, member := range group.Members {
		if strings.TrimSpace(member.UserID) != "" {
			nextMembers[member.UserID] = true
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT user_id,managed FROM scim_team_memberships WHERE workspace_id=? AND team_id=? AND group_id=?`, workspaceID, teamID, groupID)
	if err != nil {
		return nil, err
	}
	previous := map[string]bool{}
	for rows.Next() {
		var userID string
		var managed int
		if err := rows.Scan(&userID, &managed); err != nil {
			rows.Close()
			return nil, err
		}
		previous[userID] = managed == 1
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM scim_team_memberships WHERE workspace_id=? AND team_id=? AND group_id=?`, workspaceID, teamID, groupID); err != nil {
		return nil, err
	}
	for userID := range nextMembers {
		var existingRole string
		existingErr := tx.QueryRowContext(ctx, `SELECT role FROM team_memberships WHERE workspace_id=? AND team_id=? AND user_id=?`, workspaceID, teamID, userID).Scan(&existingRole)
		managed := 0
		if errors.Is(existingErr, sql.ErrNoRows) {
			// A user removed from the workspace (deprovisioning set to remove)
			// stays in their IdP groups but gets no team membership.
			var memberships int
			if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, userID).Scan(&memberships); err != nil {
				return nil, err
			}
			if memberships == 0 {
				continue
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at,origin) VALUES(?,?,?,?,?,?)`, workspaceID, teamID, userID, "member", stamp, domain.TeamMemberOriginSCIM); err != nil {
				return nil, err
			}
			managed = 1
			changes = append(changes, domain.TeamMemberChange{TeamID: teamID, TeamKey: teamKey, UserID: userID, Action: domain.TeamMemberAdded, RoleAfter: "member", Origin: domain.TeamMemberOriginSCIM})
		} else if existingErr != nil {
			return nil, existingErr
		} else if previous[userID] {
			managed = 1
		} else {
			var otherManaged int
			if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM scim_team_memberships WHERE workspace_id=? AND team_id=? AND user_id=? AND group_id<>?`, workspaceID, teamID, userID, groupID).Scan(&otherManaged); err != nil {
				return nil, err
			}
			if otherManaged > 0 {
				managed = 1
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO scim_team_memberships(workspace_id,team_id,group_id,user_id,managed,updated_at) VALUES(?,?,?,?,?,?)`, workspaceID, teamID, groupID, userID, managed, stamp); err != nil {
			return nil, err
		}
	}
	for userID, managed := range previous {
		if !managed || nextMembers[userID] {
			continue
		}
		var otherGroups int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM scim_team_memberships WHERE workspace_id=? AND team_id=? AND user_id=?`, workspaceID, teamID, userID).Scan(&otherGroups); err != nil {
			return nil, err
		}
		if otherGroups > 0 {
			continue
		}
		var role string
		membershipErr := tx.QueryRowContext(ctx, `SELECT role FROM team_memberships WHERE workspace_id=? AND team_id=? AND user_id=?`, workspaceID, teamID, userID).Scan(&role)
		if errors.Is(membershipErr, sql.ErrNoRows) {
			continue
		}
		if membershipErr != nil {
			return nil, membershipErr
		}
		if strings.EqualFold(role, "member") {
			if _, err = tx.ExecContext(ctx, `DELETE FROM team_memberships WHERE workspace_id=? AND team_id=? AND user_id=? AND role='member'`, workspaceID, teamID, userID); err != nil {
				return nil, err
			}
			changes = append(changes, domain.TeamMemberChange{TeamID: teamID, TeamKey: teamKey, UserID: userID, Action: domain.TeamMemberRemoved, RoleBefore: "member", Origin: domain.TeamMemberOriginSCIM})
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	slices.SortFunc(changes, func(a, b domain.TeamMemberChange) int {
		return strings.Compare(a.Action+"\x00"+a.UserID, b.Action+"\x00"+b.UserID)
	})
	return changes, nil
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
