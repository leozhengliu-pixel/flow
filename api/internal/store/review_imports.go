package store

// Pull/merge request import state (migration 13). One review_import_state row
// per workspace holds the admin's settings (enabled, approved repositories),
// the last run's status, the rotation cursor, the next due time and the run
// lease; review_import_items holds, per repository and per request, the last
// imported fingerprint, the head commit last fully synced and the backoff of
// failing remote calls. Neither table is part of the workspace snapshot, so
// recording progress never writes a workspace mutation or a realtime event.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ReviewImportRecord is a workspace's import row.
type ReviewImportRecord struct {
	WorkspaceKey string
	// Settings and Status are JSON documents owned by the server.
	Settings   string
	Enabled    bool
	Status     string
	Cursor     int
	LeaseOwner string
	LeaseUntil time.Time
	NextRunAt  time.Time
}

// ReviewImportItem is the import's memory of one repository or request.
type ReviewImportItem struct {
	Key   string
	Scope string // the repository key the item belongs to
	// Fingerprint is head commit, remote update time and state at the last
	// import; SyncedHead the head commit of the last successful full sync.
	Fingerprint  string
	SyncedHead   string
	Failures     int
	BackoffUntil time.Time
	LastError    string
	UpdatedAt    time.Time
}

func (s *SQLiteStore) createReviewImportSchema(ctx context.Context) error {
	id, text := "VARCHAR(191)", "TEXT"
	if s.dialect == "mysql" {
		text = "MEDIUMTEXT"
	}
	prefix := "CREATE INDEX IF NOT EXISTS "
	if s.dialect == "mysql" {
		prefix = "CREATE INDEX "
	}
	return s.applyMigrationStatements(ctx, []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS review_import_state (workspace_key %[1]s PRIMARY KEY, settings %[2]s NOT NULL, enabled INTEGER NOT NULL DEFAULT 0, status %[2]s NOT NULL, cursor_position INTEGER NOT NULL DEFAULT 0, lease_owner VARCHAR(96) NOT NULL DEFAULT '', lease_until BIGINT NOT NULL DEFAULT 0, next_run_at BIGINT NOT NULL DEFAULT 0, updated_at BIGINT NOT NULL DEFAULT 0)`, id, text),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS review_import_items (workspace_key %[1]s NOT NULL, item_key %[1]s NOT NULL, scope %[1]s NOT NULL, fingerprint VARCHAR(255) NOT NULL DEFAULT '', synced_head VARCHAR(128) NOT NULL DEFAULT '', failures INTEGER NOT NULL DEFAULT 0, backoff_until BIGINT NOT NULL DEFAULT 0, last_error %[2]s NOT NULL, updated_at BIGINT NOT NULL DEFAULT 0, PRIMARY KEY(workspace_key,item_key))`, id, text),
		prefix + "review_import_state_due ON review_import_state(enabled,next_run_at)",
		prefix + "review_import_items_scope ON review_import_items(workspace_key,scope)",
		prefix + "review_import_items_backoff ON review_import_items(workspace_key,backoff_until)",
	})
}

// ReviewImportKey bounds an item key to the column width; long keys keep a
// readable prefix and a hash of the whole key.
func ReviewImportKey(key string) string {
	if len(key) <= 180 {
		return key
	}
	sum := sha256.Sum256([]byte(key))
	return key[:150] + "~" + hex.EncodeToString(sum[:])[:20]
}

const reviewImportStateColumns = `workspace_key,settings,enabled,status,cursor_position,lease_owner,lease_until,next_run_at`

func scanReviewImportRecord(row interface{ Scan(...any) error }) (ReviewImportRecord, error) {
	var (
		record                ReviewImportRecord
		enabled               int
		leaseUntil, nextRunAt int64
	)
	if err := row.Scan(&record.WorkspaceKey, &record.Settings, &enabled, &record.Status, &record.Cursor, &record.LeaseOwner, &leaseUntil, &nextRunAt); err != nil {
		return record, err
	}
	record.Enabled = enabled != 0
	if leaseUntil > 0 {
		record.LeaseUntil = time.UnixMilli(leaseUntil).UTC()
	}
	if nextRunAt > 0 {
		record.NextRunAt = time.UnixMilli(nextRunAt).UTC()
	}
	return record, nil
}

// ReviewImportState reads the workspace's row; a workspace without one has
// the zero record (import off, nothing approved).
func (s *SQLiteStore) ReviewImportState(ctx context.Context, workspaceKey string) (ReviewImportRecord, error) {
	record, err := scanReviewImportRecord(s.db.QueryRowContext(ctx, `SELECT `+reviewImportStateColumns+` FROM review_import_state WHERE workspace_key=?`, workspaceKey))
	if errors.Is(err, sql.ErrNoRows) {
		return ReviewImportRecord{WorkspaceKey: workspaceKey}, nil
	}
	return record, err
}

func (s *SQLiteStore) ensureReviewImportRow(ctx context.Context, workspaceKey string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO review_import_state(workspace_key,settings,enabled,status,cursor_position,lease_owner,lease_until,next_run_at,updated_at) VALUES(?,'{}',0,'{}',0,'',0,0,?) ON CONFLICT DO NOTHING`, workspaceKey, millis(time.Now()))
	return err
}

// SaveReviewImportSettings stores the admin's settings. Enabling makes the
// workspace due at nextRunAt (zero leaves the due time alone).
func (s *SQLiteStore) SaveReviewImportSettings(ctx context.Context, workspaceKey, settings string, enabled bool, nextRunAt time.Time) error {
	if err := s.ensureReviewImportRow(ctx, workspaceKey); err != nil {
		return err
	}
	flag := 0
	if enabled {
		flag = 1
	}
	if nextRunAt.IsZero() {
		_, err := s.db.ExecContext(ctx, `UPDATE review_import_state SET settings=?,enabled=?,updated_at=? WHERE workspace_key=?`, settings, flag, millis(time.Now()), workspaceKey)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE review_import_state SET settings=?,enabled=?,next_run_at=?,updated_at=? WHERE workspace_key=?`, settings, flag, millis(nextRunAt), millis(time.Now()), workspaceKey)
	return err
}

// ClaimReviewImport takes the workspace's run lease for owner until
// now+lease when nobody holds an unexpired one. The claim is one conditional
// UPDATE (a compare-and-swap the database serializes on the row in SQLite,
// MySQL and Postgres alike), so across all replicas at most one run of a
// workspace's import holds the lease. owner must be unique per run.
func (s *SQLiteStore) ClaimReviewImport(ctx context.Context, workspaceKey, owner string, lease time.Duration) (bool, error) {
	if workspaceKey == "" || owner == "" || len(owner) > 96 {
		return false, errors.New("review import lease: workspace and owner are required")
	}
	if err := s.ensureReviewImportRow(ctx, workspaceKey); err != nil {
		return false, err
	}
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `UPDATE review_import_state SET lease_owner=?,lease_until=? WHERE workspace_key=? AND lease_until<?`, owner, now.Add(lease).UnixMilli(), workspaceKey, now.UnixMilli())
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

// FinishReviewImport records a run's status, cursor and next due time and
// releases owner's lease (a lease another owner took over is left alone).
func (s *SQLiteStore) FinishReviewImport(ctx context.Context, workspaceKey, owner, status string, cursor int, nextRunAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE review_import_state SET status=?,cursor_position=?,next_run_at=?,lease_owner='',lease_until=0,updated_at=? WHERE workspace_key=? AND lease_owner=?`, status, cursor, millis(nextRunAt), millis(time.Now()), workspaceKey, owner)
	return err
}

// DueReviewImports lists enabled workspaces whose next run is due and whose
// lease is free.
func (s *SQLiteStore) DueReviewImports(ctx context.Context, now time.Time, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_key FROM review_import_state WHERE enabled=1 AND next_run_at<=? AND lease_until<? ORDER BY next_run_at LIMIT ?`, millis(now), millis(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

const reviewImportItemColumns = `item_key,scope,fingerprint,synced_head,failures,backoff_until,last_error,updated_at`

func scanReviewImportItem(row interface{ Scan(...any) error }) (ReviewImportItem, error) {
	var (
		item             ReviewImportItem
		backoff, updated int64
	)
	if err := row.Scan(&item.Key, &item.Scope, &item.Fingerprint, &item.SyncedHead, &item.Failures, &backoff, &item.LastError, &updated); err != nil {
		return item, err
	}
	if backoff > 0 {
		item.BackoffUntil = time.UnixMilli(backoff).UTC()
	}
	if updated > 0 {
		item.UpdatedAt = time.UnixMilli(updated).UTC()
	}
	return item, nil
}

func (s *SQLiteStore) queryReviewImportItems(ctx context.Context, query string, args ...any) ([]ReviewImportItem, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ReviewImportItem{}
	for rows.Next() {
		item, err := scanReviewImportItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ReviewImportItems reads the items of one repository (scope), by key.
func (s *SQLiteStore) ReviewImportItems(ctx context.Context, workspaceKey, scope string) (map[string]ReviewImportItem, error) {
	items, err := s.queryReviewImportItems(ctx, `SELECT `+reviewImportItemColumns+` FROM review_import_items WHERE workspace_key=? AND scope=?`, workspaceKey, ReviewImportKey(scope))
	if err != nil {
		return nil, err
	}
	result := make(map[string]ReviewImportItem, len(items))
	for _, item := range items {
		result[item.Key] = item
	}
	return result, nil
}

// ReviewImportBackoffs lists the items still backing off at now, latest first.
func (s *SQLiteStore) ReviewImportBackoffs(ctx context.Context, workspaceKey string, now time.Time, limit int) ([]ReviewImportItem, error) {
	return s.queryReviewImportItems(ctx, `SELECT `+reviewImportItemColumns+` FROM review_import_items WHERE workspace_key=? AND backoff_until>? ORDER BY updated_at DESC LIMIT ?`, workspaceKey, millis(now), limit)
}

// SaveReviewImportItem writes one item.
func (s *SQLiteStore) SaveReviewImportItem(ctx context.Context, workspaceKey string, item ReviewImportItem) error {
	if len(item.LastError) > 1000 {
		item.LastError = item.LastError[:1000]
	}
	if len(item.Fingerprint) > 255 {
		item.Fingerprint = item.Fingerprint[:255]
	}
	if len(item.SyncedHead) > 128 {
		item.SyncedHead = item.SyncedHead[:128]
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO review_import_items(workspace_key,item_key,scope,fingerprint,synced_head,failures,backoff_until,last_error,updated_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(workspace_key,item_key) DO UPDATE SET scope=excluded.scope,fingerprint=excluded.fingerprint,synced_head=excluded.synced_head,failures=excluded.failures,backoff_until=excluded.backoff_until,last_error=excluded.last_error,updated_at=excluded.updated_at`,
		workspaceKey, ReviewImportKey(item.Key), ReviewImportKey(item.Scope), item.Fingerprint, item.SyncedHead, item.Failures, millis(item.BackoffUntil), strings.TrimSpace(item.LastError), millis(time.Now()))
	return err
}
