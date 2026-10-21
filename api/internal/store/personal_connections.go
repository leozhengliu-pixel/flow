package store

// Members' personal connections to Google Calendar and Notion (Settings ›
// Account › Connected accounts). A row holds only the identity and the
// out-of-office window read from the calendar; Google's OAuth tokens are sealed
// in connector_secrets under PersonalConnectionSecretID (AES-256-GCM,
// FLOW_CONNECTOR_SECRET_KEY), so a database dump alone never reveals a token.
// One row per member and provider.
//
// Background sync is replica-safe without a separate lock: a replica claims a
// connection with one conditional UPDATE that also pushes its sync_next_at an
// interval ahead (ClaimPersonalConnectionSync), so across replicas a
// connection is synced at most once per interval and an idle deployment with
// no connections writes nothing.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// ErrPersonalConnectionNotFound is returned when the member has no such connection.
var ErrPersonalConnectionNotFound = errors.New("personal connection not found")

// PersonalConnectionSecretID is the connector secret holding a connection's tokens.
func PersonalConnectionSecretID(id string) string { return "personal-connection:" + id }

func (s *SQLiteStore) createPersonalConnectionsSchema(ctx context.Context) error {
	id, text := "TEXT", "TEXT"
	prefix, unique := "CREATE INDEX IF NOT EXISTS ", "CREATE UNIQUE INDEX IF NOT EXISTS "
	if s.dialect == "mysql" {
		id = "VARCHAR(191)"
		prefix, unique = "CREATE INDEX ", "CREATE UNIQUE INDEX "
	}
	return s.applyMigrationStatements(ctx, []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS personal_connections (id %[1]s PRIMARY KEY, user_id %[1]s NOT NULL, provider VARCHAR(32) NOT NULL, account VARCHAR(320) NOT NULL DEFAULT '', scopes %[2]s NOT NULL, status VARCHAR(16) NOT NULL, status_reason %[2]s NOT NULL, token_expires_at VARCHAR(40) NOT NULL DEFAULT '', away_from VARCHAR(40) NOT NULL DEFAULT '', away_until VARCHAR(40) NOT NULL DEFAULT '', refresh_lease_owner VARCHAR(64) NOT NULL DEFAULT '', refresh_lease_until BIGINT NOT NULL DEFAULT 0, sync_next_at BIGINT NOT NULL DEFAULT 0, sync_failures INTEGER NOT NULL DEFAULT 0, linked_at VARCHAR(40) NOT NULL, updated_at VARCHAR(40) NOT NULL)`, id, text),
		unique + "personal_connections_user_provider ON personal_connections(user_id,provider)",
		prefix + "personal_connections_sync ON personal_connections(provider,status,sync_next_at)",
	})
}

const personalConnectionColumns = `id,user_id,provider,account,scopes,status,status_reason,token_expires_at,away_from,away_until,linked_at,updated_at`

func parseOptionalTime(value string) *time.Time {
	if value == "" {
		return nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return &parsed
	}
	return nil
}

func scanPersonalConnection(row interface{ Scan(...any) error }) (domain.PersonalConnection, error) {
	var connection domain.PersonalConnection
	var scopes, expires, awayFrom, awayUntil, linked, updated string
	if err := row.Scan(&connection.ID, &connection.UserID, &connection.Provider, &connection.Account, &scopes, &connection.Status, &connection.StatusReason, &expires, &awayFrom, &awayUntil, &linked, &updated); err != nil {
		return connection, err
	}
	connection.Scopes = strings.Fields(scopes)
	if connection.Scopes == nil {
		connection.Scopes = []string{}
	}
	connection.TokenExpiresAt, connection.AwayFrom, connection.AwayUntil = parseOptionalTime(expires), parseOptionalTime(awayFrom), parseOptionalTime(awayUntil)
	connection.LinkedAt, _ = time.Parse(time.RFC3339Nano, linked)
	connection.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return connection, nil
}

// UpsertPersonalConnection stores the connection, replacing the member's
// previous one for the provider (its id is kept) and clearing any synced
// out-of-office window. It returns the stored connection.
func (s *SQLiteStore) UpsertPersonalConnection(ctx context.Context, connection domain.PersonalConnection) (domain.PersonalConnection, error) {
	connection.Provider = strings.ToLower(strings.TrimSpace(connection.Provider))
	if connection.ID == "" || connection.UserID == "" || (connection.Provider != domain.PersonalConnectionGoogleCalendar && connection.Provider != domain.PersonalConnectionNotion) {
		return connection, errors.New("personal connection: id, user and provider are required")
	}
	if len(connection.ID) > 191 || len(connection.UserID) > 191 || len(connection.Account) > 320 {
		return connection, errors.New("personal connection: value too long")
	}
	if connection.Status == "" {
		connection.Status = domain.PersonalConnectionActive
	}
	now := time.Now().UTC()
	if connection.LinkedAt.IsZero() {
		connection.LinkedAt = now
	}
	connection.UpdatedAt = now
	if existing, err := s.PersonalConnectionFor(ctx, connection.UserID, connection.Provider); err == nil {
		connection.ID = existing.ID
	}
	if connection.Scopes == nil {
		connection.Scopes = []string{}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO personal_connections(id,user_id,provider,account,scopes,status,status_reason,token_expires_at,away_from,away_until,sync_next_at,sync_failures,linked_at,updated_at) VALUES(?,?,?,?,?,?,?,?,'','',0,0,?,?)
		ON CONFLICT(id) DO UPDATE SET account=excluded.account,scopes=excluded.scopes,status=excluded.status,status_reason=excluded.status_reason,token_expires_at=excluded.token_expires_at,away_from='',away_until='',sync_next_at=0,sync_failures=0,linked_at=excluded.linked_at,updated_at=excluded.updated_at`,
		connection.ID, connection.UserID, connection.Provider, connection.Account, strings.Join(connection.Scopes, " "), connection.Status, connection.StatusReason, formatOptionalTime(connection.TokenExpiresAt), connection.LinkedAt.UTC().Format(time.RFC3339Nano), connection.UpdatedAt.Format(time.RFC3339Nano))
	connection.AwayFrom, connection.AwayUntil = nil, nil
	return connection, err
}

// PersonalConnectionFor returns the member's connection for a provider.
func (s *SQLiteStore) PersonalConnectionFor(ctx context.Context, userID, provider string) (domain.PersonalConnection, error) {
	connection, err := scanPersonalConnection(s.db.QueryRowContext(ctx, `SELECT `+personalConnectionColumns+` FROM personal_connections WHERE user_id=? AND provider=?`, userID, strings.ToLower(provider)))
	if errors.Is(err, sql.ErrNoRows) {
		return connection, ErrPersonalConnectionNotFound
	}
	return connection, err
}

// ListPersonalConnections lists the member's connections.
func (s *SQLiteStore) ListPersonalConnections(ctx context.Context, userID string) ([]domain.PersonalConnection, error) {
	return s.queryPersonalConnections(ctx, `SELECT `+personalConnectionColumns+` FROM personal_connections WHERE user_id=? ORDER BY provider`, userID)
}

func (s *SQLiteStore) queryPersonalConnections(ctx context.Context, query string, args ...any) ([]domain.PersonalConnection, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.PersonalConnection{}
	for rows.Next() {
		connection, err := scanPersonalConnection(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, connection)
	}
	return result, rows.Err()
}

// ActiveAwayWindows returns, for the given members, the out-of-office windows
// synced from their calendars that have not ended yet (one indexed query).
func (s *SQLiteStore) ActiveAwayWindows(ctx context.Context, userIDs []string, now time.Time) (map[string]domain.PersonalConnection, error) {
	result := map[string]domain.PersonalConnection{}
	if len(userIDs) == 0 {
		return result, nil
	}
	marks := make([]string, 0, len(userIDs))
	args := []any{domain.PersonalConnectionGoogleCalendar, domain.PersonalConnectionActive}
	for _, id := range userIDs {
		marks = append(marks, "?")
		args = append(args, id)
	}
	connections, err := s.queryPersonalConnections(ctx, `SELECT `+personalConnectionColumns+` FROM personal_connections WHERE provider=? AND status=? AND away_until<>'' AND user_id IN (`+strings.Join(marks, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	for _, connection := range connections {
		if connection.AwayUntil != nil && connection.AwayUntil.After(now) {
			result[connection.UserID] = connection
		}
	}
	return result, nil
}

// SetPersonalConnectionStatus marks a connection active or as needing a
// reconnect (revoked or expired grant), with a reason.
func (s *SQLiteStore) SetPersonalConnectionStatus(ctx context.Context, id, status, reason string, expiresAt *time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE personal_connections SET status=?,status_reason=?,token_expires_at=?,updated_at=? WHERE id=?`, status, reason, formatOptionalTime(expiresAt), time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// ClaimPersonalConnectionRefresh takes the connection's token refresh lease
// for owner until now+lease when nobody holds an unexpired one.
func (s *SQLiteStore) ClaimPersonalConnectionRefresh(ctx context.Context, id, owner string, lease time.Duration) (bool, error) {
	if id == "" || owner == "" || len(owner) > 64 {
		return false, errors.New("personal connection refresh lease: id and owner are required")
	}
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `UPDATE personal_connections SET refresh_lease_owner=?,refresh_lease_until=? WHERE id=? AND (refresh_lease_until<? OR refresh_lease_owner=?)`, owner, now.Add(lease).UnixMilli(), id, now.UnixMilli(), owner)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

// ReleasePersonalConnectionRefresh gives up owner's refresh lease.
func (s *SQLiteStore) ReleasePersonalConnectionRefresh(ctx context.Context, id, owner string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE personal_connections SET refresh_lease_owner='',refresh_lease_until=0 WHERE id=? AND refresh_lease_owner=?`, id, owner)
	return err
}

// DuePersonalConnections lists up to limit active connections of a provider
// whose next sync time has come, oldest first.
func (s *SQLiteStore) DuePersonalConnections(ctx context.Context, provider string, now time.Time, limit int) ([]domain.PersonalConnection, error) {
	if limit <= 0 || limit > 200 {
		limit = 25
	}
	return s.queryPersonalConnections(ctx, `SELECT `+personalConnectionColumns+` FROM personal_connections WHERE provider=? AND status=? AND sync_next_at<=? ORDER BY sync_next_at LIMIT `+fmt.Sprint(limit), provider, domain.PersonalConnectionActive, now.UnixMilli())
}

// ClaimPersonalConnectionSync reserves one sync of a connection: it moves
// sync_next_at to next only while the connection is still due, so exactly one
// replica wins. It reports whether the caller may sync.
func (s *SQLiteStore) ClaimPersonalConnectionSync(ctx context.Context, id string, now, next time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE personal_connections SET sync_next_at=? WHERE id=? AND status=? AND sync_next_at<=?`, next.UnixMilli(), id, domain.PersonalConnectionActive, now.UnixMilli())
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

// PersonalConnectionSyncFailures is the number of syncs that failed in a row.
func (s *SQLiteStore) PersonalConnectionSyncFailures(ctx context.Context, id string) (int, error) {
	var failures int
	err := s.db.QueryRowContext(ctx, `SELECT sync_failures FROM personal_connections WHERE id=?`, id).Scan(&failures)
	return failures, err
}

// RecordPersonalConnectionSyncFailure postpones the next sync after a failure.
func (s *SQLiteStore) RecordPersonalConnectionSyncFailure(ctx context.Context, id string, failures int, next time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE personal_connections SET sync_failures=?,sync_next_at=? WHERE id=?`, failures, next.UnixMilli(), id)
	return err
}

func samePersonalTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// RecordPersonalConnectionSyncSuccess stores the synced window. It writes
// only when the window (or a failure streak) actually changed, and reports
// whether it did.
func (s *SQLiteStore) RecordPersonalConnectionSyncSuccess(ctx context.Context, current domain.PersonalConnection, failures int, from, until *time.Time) (bool, error) {
	if failures == 0 && samePersonalTime(current.AwayFrom, from) && samePersonalTime(current.AwayUntil, until) {
		return false, nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE personal_connections SET away_from=?,away_until=?,sync_failures=0,updated_at=? WHERE id=?`, formatOptionalTime(from), formatOptionalTime(until), time.Now().UTC().Format(time.RFC3339Nano), current.ID)
	return err == nil, err
}

// DeletePersonalConnection removes one of the member's connections and its
// sealed tokens. It reports whether a connection was removed.
func (s *SQLiteStore) DeletePersonalConnection(ctx context.Context, userID, id string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM personal_connections WHERE id=? AND user_id=?`, id, userID)
	if err != nil {
		return false, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return false, nil
	}
	return true, s.DeleteConnectorSecret(ctx, PersonalConnectionSecretID(id))
}
