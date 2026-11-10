package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// Personal data rights: "Download your data" exports and account erasure.
//
// An export is a job row in personal_data_exports; the server builds the zip
// in the background, keeps it in object storage and serves it to its owner
// until expires_at. Erasure removes a person's personal data and leaves the
// workspace content they authored attributed to their pseudonymous "Deleted
// user" identity (domain.DeletedUserIdentity): the account id stays, the
// account row keeps no name, email, avatar, password or sign-in method
// (deleted_at is set), and a workspace keeps them as a former member with the
// departure role "erased".

var (
	// ErrPersonalDataExportLimited refuses an export over the rate limit.
	ErrPersonalDataExportLimited = errors.New("too many data exports requested; try again later")
	// ErrErasedFromWorkspace refuses adding back an account whose personal
	// data a workspace admin erased from that workspace: its old content is
	// attributed to the same pseudonymous id, so rejoining would re-identify it.
	ErrErasedFromWorkspace = errors.New("this account's personal data was erased from the workspace; it cannot join it again")
)

// DepartureRoleErased marks a former member whose personal data was erased
// (their account was deleted, or a workspace admin erased them).
const DepartureRoleErased = "erased"

// Export job states.
const (
	PersonalDataExportQueued    = "queued"
	PersonalDataExportRunning   = "running"
	PersonalDataExportCompleted = "completed"
	PersonalDataExportFailed    = "failed"
	PersonalDataExportExpired   = "expired"
)

// PersonalDataExportRecord is an export job with its owner and object.
type PersonalDataExportRecord struct {
	domain.PersonalDataExport
	UserID    string
	ObjectKey string
}

// createPersonalDataSchema is schema migration 24: the export jobs and the
// deletion stamp of erased accounts.
func (s *SQLiteStore) createPersonalDataSchema(ctx context.Context) error {
	idType := "TEXT"
	if s.dialect == "mysql" {
		idType = "VARCHAR(191)"
	}
	prefix := "CREATE INDEX IF NOT EXISTS "
	if s.dialect == "mysql" {
		prefix = "CREATE INDEX "
	}
	if err := s.applyMigrationStatements(ctx, []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS personal_data_exports (id %[1]s PRIMARY KEY, user_id %[1]s NOT NULL, status VARCHAR(16) NOT NULL, object_key VARCHAR(512) NOT NULL DEFAULT '', size BIGINT NOT NULL DEFAULT 0, error_text VARCHAR(512) NOT NULL DEFAULT '', created_at VARCHAR(40) NOT NULL, completed_at VARCHAR(40), expires_at VARCHAR(40))`, idType),
		prefix + "personal_data_exports_user_idx ON personal_data_exports(user_id,created_at)",
	}); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `ALTER TABLE auth_users ADD COLUMN deleted_at VARCHAR(40)`)
	if err != nil {
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "duplicate column") || strings.Contains(message, "already exists") {
			return nil
		}
	}
	return err
}

const personalDataExportColumns = `id,user_id,status,object_key,size,error_text,created_at,completed_at,expires_at`

func scanPersonalDataExport(row interface{ Scan(...any) error }) (PersonalDataExportRecord, error) {
	var item PersonalDataExportRecord
	var created string
	var completed, expires sql.NullString
	if err := row.Scan(&item.ID, &item.UserID, &item.Status, &item.ObjectKey, &item.Size, &item.Error, &created, &completed, &expires); err != nil {
		return item, err
	}
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if completed.Valid {
		item.CompletedAt = parseOptionalTime(completed.String)
	}
	if expires.Valid {
		item.ExpiresAt = parseOptionalTime(expires.String)
	}
	return item, nil
}

// CreatePersonalDataExport queues an export for userID. At most limit
// exports may be requested in window, and a queued or running one is
// returned instead of starting another (created is false then).
func (s *SQLiteStore) CreatePersonalDataExport(ctx context.Context, userID string, now time.Time, window time.Duration, limit int) (PersonalDataExportRecord, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PersonalDataExportRecord{}, false, err
	}
	defer tx.Rollback()
	pending, err := scanPersonalDataExport(tx.QueryRowContext(ctx, `SELECT `+personalDataExportColumns+` FROM personal_data_exports WHERE user_id=? AND status IN ('queued','running') ORDER BY created_at DESC LIMIT 1`, userID))
	if err == nil {
		return pending, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return PersonalDataExportRecord{}, false, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM personal_data_exports WHERE user_id=? AND created_at>?`, userID, now.Add(-window).UTC().Format(time.RFC3339Nano)).Scan(&count); err != nil {
		return PersonalDataExportRecord{}, false, err
	}
	if count >= limit {
		return PersonalDataExportRecord{}, false, ErrPersonalDataExportLimited
	}
	item := PersonalDataExportRecord{UserID: userID, PersonalDataExport: domain.PersonalDataExport{ID: idgen.New("pdx_"), Status: PersonalDataExportQueued, CreatedAt: now.UTC()}}
	if _, err := tx.ExecContext(ctx, `INSERT INTO personal_data_exports(id,user_id,status,created_at) VALUES(?,?,?,?)`, item.ID, userID, item.Status, item.CreatedAt.Format(time.RFC3339Nano)); err != nil {
		return PersonalDataExportRecord{}, false, err
	}
	return item, true, tx.Commit()
}

// PersonalDataExport reads one of userID's exports.
func (s *SQLiteStore) PersonalDataExport(ctx context.Context, userID, id string) (PersonalDataExportRecord, error) {
	return scanPersonalDataExport(s.db.QueryRowContext(ctx, `SELECT `+personalDataExportColumns+` FROM personal_data_exports WHERE id=? AND user_id=?`, id, userID))
}

// ListPersonalDataExports lists userID's exports, newest first.
func (s *SQLiteStore) ListPersonalDataExports(ctx context.Context, userID string) ([]PersonalDataExportRecord, error) {
	return s.queryPersonalDataExports(ctx, `SELECT `+personalDataExportColumns+` FROM personal_data_exports WHERE user_id=? ORDER BY created_at DESC LIMIT 20`, userID)
}

func (s *SQLiteStore) queryPersonalDataExports(ctx context.Context, query string, args ...any) ([]PersonalDataExportRecord, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PersonalDataExportRecord{}
	for rows.Next() {
		item, err := scanPersonalDataExport(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// StartPersonalDataExport claims a queued export for building.
func (s *SQLiteStore) StartPersonalDataExport(ctx context.Context, id string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE personal_data_exports SET status='running' WHERE id=? AND status='queued'`, id)
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	return count == 1, nil
}

// FinishPersonalDataExport records a built export (objectKey and size, until
// expires) or its failure (errText).
func (s *SQLiteStore) FinishPersonalDataExport(ctx context.Context, id, objectKey string, size int64, errText string, now, expires time.Time) error {
	status := PersonalDataExportCompleted
	if errText != "" {
		status, objectKey, size = PersonalDataExportFailed, "", 0
	}
	_, err := s.db.ExecContext(ctx, `UPDATE personal_data_exports SET status=?,object_key=?,size=?,error_text=?,completed_at=?,expires_at=? WHERE id=?`, status, objectKey, size, storableText(errText, 512), now.UTC().Format(time.RFC3339Nano), expires.UTC().Format(time.RFC3339Nano), id)
	return err
}

// ExpiredPersonalDataExports lists exports whose download expired but whose
// object is still kept, and exports stuck before completion past cutoff.
func (s *SQLiteStore) ExpiredPersonalDataExports(ctx context.Context, now time.Time, limit int) ([]PersonalDataExportRecord, error) {
	stamp := now.UTC().Format(time.RFC3339Nano)
	return s.queryPersonalDataExports(ctx, `SELECT `+personalDataExportColumns+` FROM personal_data_exports WHERE (status='completed' AND expires_at<?) OR (status IN ('queued','running') AND created_at<?) ORDER BY created_at LIMIT ?`, stamp, now.Add(-24*time.Hour).UTC().Format(time.RFC3339Nano), limit)
}

// ExpirePersonalDataExport marks an export expired once its object is gone.
func (s *SQLiteStore) ExpirePersonalDataExport(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE personal_data_exports SET status='expired',object_key='',size=0 WHERE id=?`, id)
	return err
}

// AccountSignInEvent is a sign-in notification the account was sent (new
// device or location), without the device fingerprint key.
type AccountSignInEvent struct {
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"createdAt"`
}

// AccountSignInEvents lists the sign-in notifications sent to userID.
func (s *SQLiteStore) AccountSignInEvents(ctx context.Context, userID string) ([]AccountSignInEvent, error) {
	if err := s.ensureAccountEmailEvents(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT kind,created_at FROM auth_email_events WHERE user_id=? ORDER BY created_at DESC LIMIT 500`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AccountSignInEvent{}
	for rows.Next() {
		var item AccountSignInEvent
		var created string
		if err := rows.Scan(&item.Kind, &created); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		items = append(items, item)
	}
	return items, rows.Err()
}

// AccountWorkspaces lists every workspace userID has a membership row in
// (any status) or left, by key.
func (s *SQLiteStore) AccountWorkspaces(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_id FROM workspace_memberships WHERE user_id=? UNION SELECT workspace_id FROM workspace_departures WHERE user_id=?`, userID, userID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	keys := []string{}
	for _, id := range ids {
		if _, key, ok := s.workspaceByID(id); ok {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

// SoleAdminWorkspaces lists the workspaces (by key) in which userID is the
// only active owner or admin: deleting the account there would leave the
// workspace without anyone who can manage it.
func (s *SQLiteStore) SoleAdminWorkspaces(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.workspace_id FROM workspace_memberships m WHERE m.user_id=? AND m.status='active' AND m.role IN ('admin','owner')
		AND (SELECT COUNT(*) FROM workspace_memberships o WHERE o.workspace_id=m.workspace_id AND o.status='active' AND o.role IN ('admin','owner'))=1`, userID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	keys := []string{}
	for _, id := range ids {
		if _, key, ok := s.workspaceByID(id); ok {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

// OtherWorkspaceMemberships reports whether userID belongs to (or left) any
// workspace other than workspaceID.
func (s *SQLiteStore) OtherWorkspaceMemberships(ctx context.Context, workspaceID, userID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT workspace_id FROM workspace_memberships WHERE user_id=? AND workspace_id<>? UNION SELECT workspace_id FROM workspace_departures WHERE user_id=? AND workspace_id<>? AND role<>?) other`, userID, workspaceID, userID, workspaceID, DepartureRoleErased).Scan(&count)
	return count > 0, err
}

// ErasedFromWorkspace reports whether userID's personal data was erased from
// the workspace.
func (s *SQLiteStore) ErasedFromWorkspace(ctx context.Context, workspaceID, userID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_departures WHERE workspace_id=? AND user_id=? AND role=?`, workspaceID, userID, DepartureRoleErased).Scan(&count)
	return count > 0, err
}

func (s *SQLiteStore) refuseErasedMember(ctx context.Context, q metadataReader, workspaceID, userID string) error {
	rows, err := q.QueryContext(ctx, `SELECT 1 FROM workspace_departures WHERE workspace_id=? AND user_id=? AND role=?`, workspaceID, userID, DepartureRoleErased)
	if err != nil {
		return err
	}
	erased := rows.Next()
	if err := rows.Close(); err != nil {
		return err
	}
	if erased {
		return ErrErasedFromWorkspace
	}
	return nil
}

// ErasePersonFromWorkspace removes userID from the workspace and deletes the
// personal data the workspace's tables keep about them outside its metadata:
// the membership and team memberships (kept as a former member with the
// departure role "erased"), SCIM links, search history, recently viewed,
// notifications and their deliveries, and linked chat accounts. The person's
// pseudonymous identity stays on what they authored. The workspace's
// metadata (directory, settings, agent chats, keys) is the caller's
// mutation; ScrubPersonCopies rewrites the stored copies of their identity.
func (s *SQLiteStore) ErasePersonFromWorkspace(ctx context.Context, workspaceKey, userID string) error {
	data, ok := s.storedWorkspace(workspaceKey)
	if !ok {
		return ErrAuthForbidden
	}
	workspaceID := data.Workspace.ID
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var member int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, userID).Scan(&member); err != nil {
		return err
	}
	if err := recordWorkspaceDepartureTx(ctx, tx, workspaceID, userID, now); err != nil {
		return err
	}
	stamp := now.Format(time.RFC3339Nano)
	statements := []struct {
		query string
		args  []any
	}{
		// People who were never a member here keep a departure too (with the
		// person's account still present), so they stay former members.
		{`INSERT INTO workspace_departures(workspace_id,user_id,role,joined_at,left_at) SELECT ?,id,?,?,? FROM auth_users WHERE id=? ON CONFLICT(workspace_id,user_id) DO UPDATE SET role='erased'`, []any{workspaceID, DepartureRoleErased, stamp, stamp, userID}},
		{`DELETE FROM team_memberships WHERE workspace_id=? AND user_id=?`, []any{workspaceID, userID}},
		{`DELETE FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, []any{workspaceID, userID}},
		{`DELETE FROM scim_group_memberships WHERE workspace_id=? AND user_id=?`, []any{workspaceID, userID}},
		{`DELETE FROM scim_team_memberships WHERE workspace_id=? AND user_id=?`, []any{workspaceID, userID}},
		{`DELETE FROM scim_user_roles WHERE workspace_id=? AND user_id=?`, []any{workspaceID, userID}},
		{`DELETE FROM auth_identities WHERE user_id=? AND provider IN ('scim',?) AND issuer=?`, []any{userID, scimDeletedProvider, workspaceID}},
		{`DELETE FROM search_history WHERE workspace_id=? AND user_id=?`, []any{workspaceID, userID}},
		{`DELETE FROM recently_viewed WHERE workspace_id=? AND user_id=?`, []any{workspaceID, userID}},
		{`DELETE FROM workspace_content_records WHERE workspace_key=? AND kind IN ('notification','delivery') AND owner_id=?`, []any{workspaceKey, userID}},
		{`DELETE FROM connector_secrets WHERE id IN (SELECT id FROM chat_actions WHERE workspace_key=? AND user_id=? AND id LIKE 'chat_oauth_%')`, []any{workspaceKey, userID}},
		{`DELETE FROM chat_actions WHERE workspace_key=? AND user_id=?`, []any{workspaceKey, userID}},
		{`DELETE FROM chat_identities WHERE workspace_key=? AND user_id=?`, []any{workspaceKey, userID}},
		// Invitations to the person's address name them too.
		{`DELETE FROM workspace_invitations WHERE workspace_id=? AND email=(SELECT email FROM auth_users WHERE id=?)`, []any{workspaceID, userID}},
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return fmt.Errorf("erase workspace member: %w", err)
		}
	}
	var event domain.DomainEvent
	if member > 0 {
		if event, err = recordMemberStatusTx(ctx, tx, workspaceID, userID, "removed"); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.accountAccessChanged(userID)
	if member > 0 {
		s.publishMemberStatus(ctx, workspaceID, event)
	}
	return nil
}

// DeleteAgentSessionRuns deletes the run ledgers (tool calls, states,
// activities, changes, keys and tokens) and live state of agent
// conversations that were deleted with their owner's personal data.
func (s *SQLiteStore) DeleteAgentSessionRuns(ctx context.Context, workspaceKey string, sessionIDs []string) error {
	if len(sessionIDs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, sessionID := range sessionIDs {
		runs := `SELECT id FROM agent_runs WHERE workspace_key=? AND session_id=?`
		for _, table := range []string{"agent_run_tool_calls", "agent_run_states", "agent_run_activities", "agent_run_changes", "agent_run_keys", "agent_run_tokens"} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE workspace_key=? AND run_id IN (`+runs+`)`, workspaceKey, workspaceKey, sessionID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM agent_runs WHERE workspace_key=? AND session_id=?`, workspaceKey, sessionID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM agent_session_states WHERE workspace_key=? AND session_id=?`, workspaceKey, sessionID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// EraseAccount deletes userID's account-level personal data and turns the
// account into its pseudonymous tombstone: no email (so the address can sign
// up again), name, avatar, password, sign-in methods, sessions, tokens,
// search history, recently viewed, chat links, signing key, invitations to
// the address or export jobs. Callers erase the person from
// every workspace first (memberships are refused here) and delete what lives
// in object storage and behind provider credentials (personal connections,
// code host accounts) through their own helpers.
func (s *SQLiteStore) EraseAccount(ctx context.Context, userID string) error {
	user, err := s.authUserByID(ctx, userID)
	if err != nil {
		return err
	}
	// Created on first use; the transaction below must not fail on it.
	if err := s.ensureAccountEmailEvents(ctx); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var memberships int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_memberships WHERE user_id=?`, userID).Scan(&memberships); err != nil {
		return err
	}
	if memberships > 0 {
		return fmt.Errorf("erase the account from its %d workspaces first", memberships)
	}
	anonymous := domain.DeletedUserIdentity(userID)
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	statements := []struct {
		query string
		args  []any
	}{
		{`DELETE FROM auth_session_security WHERE token_hash IN (SELECT token_hash FROM auth_sessions WHERE user_id=?)`, []any{userID}},
		{`DELETE FROM auth_sessions WHERE user_id=?`, []any{userID}},
		{`DELETE FROM auth_identities WHERE user_id=?`, []any{userID}},
		{`DELETE FROM auth_tokens WHERE user_id=?`, []any{userID}},
		{`DELETE FROM auth_account_state WHERE user_id=?`, []any{userID}},
		{`DELETE FROM auth_email_events WHERE user_id=?`, []any{userID}},
		{`DELETE FROM search_history WHERE user_id=?`, []any{userID}},
		{`DELETE FROM recently_viewed WHERE user_id=?`, []any{userID}},
		{`DELETE FROM connector_secrets WHERE id IN (SELECT id FROM chat_actions WHERE user_id=? AND id LIKE 'chat_oauth_%')`, []any{userID}},
		{`DELETE FROM chat_actions WHERE user_id=?`, []any{userID}},
		{`DELETE FROM chat_identities WHERE user_id=?`, []any{userID}},
		{`DELETE FROM coding_signing_keys WHERE user_id=?`, []any{userID}},
		{`DELETE FROM personal_data_exports WHERE user_id=?`, []any{userID}},
		{`UPDATE workspace_departures SET role=? WHERE user_id=?`, []any{DepartureRoleErased, userID}},
		{`UPDATE auth_users SET email=NULL,name=?,display_name=?,avatar_url='',password_hash='',email_verified_at=NULL,active=0,deleted_at=?,updated_at=? WHERE id=?`, []any{anonymous.Name, anonymous.DisplayName, stamp, stamp, userID}},
	}
	if user.Email != "" {
		statements = append(statements, struct {
			query string
			args  []any
		}{`DELETE FROM workspace_invitations WHERE email=?`, []any{normalizeEmail(user.Email)}})
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return fmt.Errorf("erase account: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.accountAccessChanged(userID)
	return nil
}

// personScrubTargets are the stored JSON values that can hold a copy of a
// person's identity (an issue's creator or assignee, a comment's author, an
// activity's actor, a notification's actor, a mention's label, an event's
// payload). Reads refresh people from the workspace directory, so these
// copies are never shown, but erasure must not leave them at rest. Each
// column's update is written out (value, then the scope, then the keys) so
// the metadata write scan sees every statement; the writes run in a
// transaction, which moves the metadata revision.
var personScrubTargets = []struct {
	table   string
	scope   string
	byID    bool
	keys    []string
	columns []string
	updates map[string]string
}{
	{"issue_records", "workspace_key", false, []string{"id"}, []string{"data", "list_data"}, map[string]string{
		"data":      `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`,
		"list_data": `UPDATE issue_records SET list_data=? WHERE workspace_key=? AND id=?`,
	}},
	{"workspace_content_records", "workspace_key", false, []string{"kind", "resource_id", "id"}, []string{"data"}, map[string]string{
		"data": `UPDATE workspace_content_records SET data=? WHERE workspace_key=? AND kind=? AND resource_id=? AND id=?`,
	}},
	{"workspace_metadata_records", "workspace_key", false, []string{"field", "record_key"}, []string{"data"}, map[string]string{
		"data": `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field=? AND record_key=?`,
	}},
	{"workspace_states", "workspace_key", false, nil, []string{"data"}, map[string]string{
		"data": `UPDATE workspace_states SET data=? WHERE workspace_key=?`,
	}},
	{"domain_events", "workspace_id", true, []string{"id"}, []string{"payload", "previous_values"}, map[string]string{
		"payload":         `UPDATE domain_events SET payload=? WHERE workspace_id=? AND id=?`,
		"previous_values": `UPDATE domain_events SET previous_values=? WHERE workspace_id=? AND id=?`,
	}},
}

// personalPayloadKeys are blanked in the events about the person themselves
// (their profile, username and identity updates).
var personalPayloadKeys = map[string]bool{"displayName": true, "username": true, "avatarUrl": true, "jobTitle": true, "email": true, "name": true, "phoneNumber": true}

// ScrubPersonCopies rewrites every stored copy of userID's identity in the
// workspace to the pseudonymous "Deleted user" one, blanks the personal
// fields of the events about them, and reloads the workspace. It returns
// how many values it rewrote.
func (s *SQLiteStore) ScrubPersonCopies(ctx context.Context, workspaceKey, userID string) (int, error) {
	data, ok := s.storedWorkspace(workspaceKey)
	if !ok {
		return 0, ErrAuthForbidden
	}
	anonymous := domain.DeletedUserIdentity(userID)
	changed := 0
	apply := func() error {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		count, err := s.scrubPersonCopies(ctx, workspaceKey, data.Workspace.ID, anonymous)
		changed = count
		return err
	}
	var err error
	if s.coordinator != nil {
		err = s.coordinator.WithWorkspaceLock(ctx, workspaceKey, apply)
	} else {
		err = apply()
	}
	if err != nil {
		return changed, err
	}
	if changed > 0 {
		if err := s.ReloadWorkspace(ctx, workspaceKey); err != nil {
			return changed, err
		}
		s.dropMetadataCache(ctx, workspaceKey)
		if cache := s.hotCache(); cache != nil {
			_, _ = cache.CacheIncr(ctx, s.issueQueryGenKey(workspaceKey))
		}
	}
	return changed, nil
}

// PersonalDataLeaks lists the stored values of the workspace (as
// "table.column") that still contain any of texts, among those
// ScrubPersonCopies rewrites. It verifies an erasure.
func (s *SQLiteStore) PersonalDataLeaks(ctx context.Context, workspaceKey string, texts ...string) []string {
	data, ok := s.storedWorkspace(workspaceKey)
	if !ok {
		return nil
	}
	leaks := []string{}
	for _, target := range personScrubTargets {
		scope := any(workspaceKey)
		if target.byID {
			scope = data.Workspace.ID
		}
		for _, column := range target.columns {
			for _, text := range texts {
				if strings.TrimSpace(text) == "" {
					continue
				}
				var count int
				err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+target.table+` WHERE `+target.scope+`=? AND `+s.textCast(column)+` LIKE ? ESCAPE '!'`, scope, labelReferencePattern(text)).Scan(&count)
				if err != nil || count > 0 {
					leaks = append(leaks, fmt.Sprintf("%s.%s (%q)", target.table, column, text))
				}
			}
		}
	}
	return leaks
}

// personTextColumns are the scrubbed columns stored as text rather than as
// a blob (bytes on PostgreSQL).
var personTextColumns = map[string]bool{"previous_values": true}

// textCast reads a scrubbed column as text, for LIKE.
func (s *SQLiteStore) textCast(column string) string {
	switch s.dialect {
	case "mysql":
		return "CAST(" + column + " AS CHAR)"
	case "postgres":
		if personTextColumns[column] {
			return column
		}
		return "convert_from(" + column + ",'UTF8')"
	default:
		return "CAST(" + column + " AS TEXT)"
	}
}

func (s *SQLiteStore) scrubPersonCopies(ctx context.Context, workspaceKey, workspaceID string, anonymous domain.User) (int, error) {
	type update struct {
		query string
		args  []any
	}
	updates := []update{}
	for _, target := range personScrubTargets {
		scope := any(workspaceKey)
		if target.byID {
			scope = workspaceID
		}
		events := target.table == "domain_events"
		for _, column := range target.columns {
			selected := append(append([]string{}, target.keys...), column)
			query := `SELECT ` + strings.Join(selected, ",") + ` FROM ` + target.table + ` WHERE ` + target.scope + `=? AND ` + s.textCast(column) + ` LIKE ? ESCAPE '!'`
			args := []any{scope, labelReferencePattern(anonymous.ID)}
			if events {
				// The events about the person (their profile, username and
				// identity updates) carry their profile without their id.
				query = `SELECT id,` + column + `,aggregate_id FROM domain_events WHERE workspace_id=? AND ` + column + ` IS NOT NULL AND (aggregate_id=? OR ` + s.textCast(column) + ` LIKE ? ESCAPE '!')`
				args = []any{scope, anonymous.ID, labelReferencePattern(anonymous.ID)}
			}
			rows, err := s.db.QueryContext(ctx, query, args...)
			if err != nil {
				return 0, fmt.Errorf("scan %s.%s: %w", target.table, column, err)
			}
			for rows.Next() {
				keys := make([]string, len(target.keys))
				dest := make([]any, 0, len(selected)+1)
				for index := range keys {
					dest = append(dest, &keys[index])
				}
				var raw []byte
				var aggregate string
				dest = append(dest, &raw)
				if events {
					dest = append(dest, &aggregate)
				}
				if err := rows.Scan(dest...); err != nil {
					rows.Close()
					return 0, err
				}
				next, changed := ScrubPersonJSON(raw, anonymous, events && aggregate == anonymous.ID)
				if !changed {
					continue
				}
				value := any(next)
				if personTextColumns[column] {
					value = string(next)
				}
				updateArgs := []any{value, scope}
				for index := range target.keys {
					updateArgs = append(updateArgs, keys[index])
				}
				updates = append(updates, update{target.updates[column], updateArgs})
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return 0, err
			}
		}
	}
	if len(updates) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, item := range updates {
		if _, err := tx.ExecContext(ctx, item.query, item.args...); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	slog.Info("scrubbed stored copies of an erased person", "workspace", workspaceKey, "values", len(updates))
	return len(updates), nil
}

// ScrubPersonJSON rewrites the copies of a person in a stored JSON value:
// an object with the person's id and a name, email or avatar becomes the
// pseudonymous identity, and a mention of them is relabelled "Deleted user".
// With aboutPerson (an event whose aggregate is the person), the personal
// fields anywhere in the value are blanked too. It reports whether anything
// changed; invalid JSON is left as it is.
func ScrubPersonJSON(raw []byte, anonymous domain.User, aboutPerson bool) ([]byte, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return raw, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return raw, false
	}
	changed := false
	var walk func(any)
	walk = func(node any) {
		switch typed := node.(type) {
		case map[string]any:
			id, _ := typed["id"].(string)
			userID, _ := typed["userId"].(string)
			if id == anonymous.ID {
				_, hasDisplay := typed["displayName"]
				_, hasEmail := typed["email"]
				_, hasAvatar := typed["avatarUrl"]
				if hasDisplay || hasEmail || hasAvatar {
					if typed["name"] != anonymous.Name || typed["displayName"] != anonymous.DisplayName || typed["email"] != "" || typed["deleted"] != true {
						changed = true
					}
					typed["name"], typed["displayName"], typed["email"], typed["deleted"] = anonymous.Name, anonymous.DisplayName, "", true
					for _, key := range []string{"avatarUrl", "username", "jobTitle", "userId"} {
						if _, ok := typed[key]; ok {
							delete(typed, key)
							changed = true
						}
					}
				}
			}
			if (id == anonymous.ID || userID == anonymous.ID) && typed["label"] != nil && typed["label"] != anonymous.DisplayName {
				typed["label"], changed = anonymous.DisplayName, true
			}
			// Records about the person (audit metadata, membership changes)
			// name them next to their id.
			if userID == anonymous.ID {
				for _, key := range []string{"userName", "userDisplayName", "userEmail", "userAvatarUrl"} {
					if text, ok := typed[key].(string); ok && text != "" && text != anonymous.DisplayName {
						typed[key], changed = "", true
						if key == "userName" || key == "userDisplayName" {
							typed[key] = anonymous.DisplayName
						}
					}
				}
			}
			if aboutPerson {
				for key, child := range typed {
					if text, ok := child.(string); ok && text != "" && personalPayloadKeys[key] && text != anonymous.Name && text != anonymous.DisplayName {
						typed[key], changed = "", true
					}
				}
			}
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	if !changed {
		return raw, false
	}
	next, err := json.Marshal(value)
	if err != nil {
		return raw, false
	}
	return StripJSONNUL(next), true
}
