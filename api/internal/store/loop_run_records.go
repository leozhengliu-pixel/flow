package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Loop runs live in their own tables, not in the workspace metadata: a run
// records progress many times while it works, and each record write is a
// small scoped UPDATE of one row instead of a workspace mutation (snapshot
// clone, metadata persist and realtime fan-out under the workspace write
// lock). loop_run_records holds the current state of each run (one JSON row
// plus indexed status, lease and timing columns); loop_run_events is an
// append-only log of what happened during the run.

var (
	ErrLoopRunNotFound  = errors.New("loop run not found")
	ErrLoopRunLeaseLost = errors.New("loop run lease lost")
)

// loopRunTime is fixed width so stored timestamps compare as strings.
const loopRunTime = "2006-01-02T15:04:05.000000000Z"

func formatLoopRunTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(loopRunTime)
}

// LoopRunLease names the process that executes a running run and until when
// its claim holds without a heartbeat.
type LoopRunLease struct {
	Owner     string
	ExpiresAt time.Time
}

// LoopRunEvent is one entry of a run's append-only event log.
type LoopRunEvent struct {
	Seq       int64           `json:"seq"`
	Kind      string          `json:"kind"`
	Data      json.RawMessage `json:"data,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
}

// RunningLoopRun is the lease state of a run whose status is running.
type RunningLoopRun struct {
	Workspace       string
	ID              string
	LoopID          string
	LeaseOwner      string
	LeaseExpiresAt  time.Time
	HeartbeatAt     time.Time
	CancelRequested bool
}

func (s *SQLiteStore) ensureLoopRunRecords(ctx context.Context) error {
	blob := "BLOB"
	switch s.dialect {
	case "mysql":
		blob = "LONGBLOB"
	case "postgres":
		blob = "BYTEA"
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS loop_run_records(workspace_key VARCHAR(191) NOT NULL,id VARCHAR(191) NOT NULL,loop_id VARCHAR(191) NOT NULL,status VARCHAR(32) NOT NULL,started_at VARCHAR(40) NOT NULL,finished_at VARCHAR(40) NOT NULL DEFAULT '',lease_owner VARCHAR(191) NOT NULL DEFAULT '',lease_expires_at VARCHAR(40) NOT NULL DEFAULT '',heartbeat_at VARCHAR(40) NOT NULL DEFAULT '',cancel_requested INTEGER NOT NULL DEFAULT 0,data ` + blob + ` NOT NULL,PRIMARY KEY(workspace_key,id))`,
		`CREATE TABLE IF NOT EXISTS loop_run_events(workspace_key VARCHAR(191) NOT NULL,run_id VARCHAR(191) NOT NULL,seq BIGINT NOT NULL,kind VARCHAR(32) NOT NULL,data ` + blob + ` NOT NULL,created_at VARCHAR(40) NOT NULL,PRIMARY KEY(workspace_key,run_id,seq))`,
		`CREATE TABLE IF NOT EXISTS loop_run_migrations(workspace_key VARCHAR(191) PRIMARY KEY,migrated_runs INTEGER NOT NULL,migrated_at VARCHAR(40) NOT NULL)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	prefix := "CREATE INDEX IF NOT EXISTS "
	if s.dialect == "mysql" {
		prefix = "CREATE INDEX "
	}
	for _, index := range []string{"loop_run_loop_idx ON loop_run_records(workspace_key,loop_id,started_at)", "loop_run_status_idx ON loop_run_records(status,lease_owner)"} {
		if _, err := s.db.ExecContext(ctx, prefix+index); err != nil && !(s.dialect == "mysql" && isDuplicateIndex(err)) {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) lockClause() string {
	if s.dialect == "sqlite" {
		return ""
	}
	return " FOR UPDATE"
}

// storedLoopRun strips the per-viewer fields before a run is written.
func storedLoopRun(run domain.LoopRun) ([]byte, error) {
	run.ViewerRating, run.ViewerComment, run.FeedbackCounts = nil, "", nil
	return json.Marshal(run)
}

// decodeLoopRun rebuilds a run from its row. status and heartbeat come from
// their columns: a heartbeat only renews the lease columns (it must not
// rewrite the whole run document every few seconds), so the document's
// heartbeatAt is the value from when the run was last saved and the column
// holds the live one.
func decodeLoopRun(raw []byte, status, heartbeat string) (domain.LoopRun, error) {
	var run domain.LoopRun
	if err := json.Unmarshal(raw, &run); err != nil {
		return run, err
	}
	run.Status = status
	if at, err := time.Parse(loopRunTime, heartbeat); err == nil && (run.HeartbeatAt == nil || at.After(*run.HeartbeatAt)) {
		at = at.UTC()
		run.HeartbeatAt = &at
	}
	return run, nil
}

// CreateLoopRun records a new run. A running run carries the lease of the
// process executing it.
func (s *SQLiteStore) CreateLoopRun(ctx context.Context, workspace string, run domain.LoopRun, lease LoopRunLease) error {
	raw, err := storedLoopRun(run)
	if err != nil {
		return err
	}
	finished := ""
	if run.FinishedAt != nil {
		finished = formatLoopRunTime(*run.FinishedAt)
	}
	heartbeat := ""
	if run.HeartbeatAt != nil {
		heartbeat = formatLoopRunTime(*run.HeartbeatAt)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO loop_run_records(workspace_key,id,loop_id,status,started_at,finished_at,lease_owner,lease_expires_at,heartbeat_at,cancel_requested,data) VALUES(?,?,?,?,?,?,?,?,?,0,?)`,
		workspace, run.ID, run.LoopID, run.Status, formatLoopRunTime(run.StartedAt), finished, lease.Owner, formatLoopRunTime(lease.ExpiresAt), heartbeat, raw)
	return err
}

// LoopRun reads one run.
func (s *SQLiteStore) LoopRun(ctx context.Context, workspace, id string) (domain.LoopRun, error) {
	var raw []byte
	var status, heartbeat string
	err := s.db.QueryRowContext(ctx, `SELECT data,status,heartbeat_at FROM loop_run_records WHERE workspace_key=? AND id=?`, workspace, id).Scan(&raw, &status, &heartbeat)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LoopRun{}, ErrLoopRunNotFound
	}
	if err != nil {
		return domain.LoopRun{}, err
	}
	return decodeLoopRun(raw, status, heartbeat)
}

// ListLoopRuns returns a loop's newest runs first.
func (s *SQLiteStore) ListLoopRuns(ctx context.Context, workspace, loopID string, limit int) ([]domain.LoopRun, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT data,status,heartbeat_at FROM loop_run_records WHERE workspace_key=? AND loop_id=? ORDER BY started_at DESC,id DESC LIMIT %d`, limit), workspace, loopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []domain.LoopRun{}
	for rows.Next() {
		var raw []byte
		var status, heartbeat string
		if err := rows.Scan(&raw, &status, &heartbeat); err != nil {
			return nil, err
		}
		run, err := decodeLoopRun(raw, status, heartbeat)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// UpdateLoopRun changes one run in a transaction that locks only its row and
// appends events to its log. mutate returning ErrNoMutation leaves the run
// (and the log) unchanged.
func (s *SQLiteStore) UpdateLoopRun(ctx context.Context, workspace, id string, events []LoopRunEvent, mutate func(*domain.LoopRun) error) (domain.LoopRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.LoopRun{}, err
	}
	defer tx.Rollback()
	var raw []byte
	var status, heartbeat string
	err = tx.QueryRowContext(ctx, `SELECT data,status,heartbeat_at FROM loop_run_records WHERE workspace_key=? AND id=?`+s.lockClause(), workspace, id).Scan(&raw, &status, &heartbeat)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LoopRun{}, ErrLoopRunNotFound
	}
	if err != nil {
		return domain.LoopRun{}, err
	}
	run, err := decodeLoopRun(raw, status, heartbeat)
	if err != nil {
		return run, err
	}
	if err := mutate(&run); err != nil {
		if errors.Is(err, ErrNoMutation) {
			current, _ := decodeLoopRun(raw, status, heartbeat)
			return current, nil
		}
		return run, err
	}
	encoded, err := storedLoopRun(run)
	if err != nil {
		return run, err
	}
	finished := ""
	if run.FinishedAt != nil {
		finished = formatLoopRunTime(*run.FinishedAt)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE loop_run_records SET status=?,finished_at=?,data=? WHERE workspace_key=? AND id=?`, run.Status, finished, encoded, workspace, id); err != nil {
		return run, err
	}
	if len(events) > 0 {
		var last sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT MAX(seq) FROM loop_run_events WHERE workspace_key=? AND run_id=?`, workspace, id).Scan(&last); err != nil {
			return run, err
		}
		seq := last.Int64
		for _, event := range events {
			seq++
			data := event.Data
			if len(data) == 0 {
				data = json.RawMessage(`{}`)
			}
			at := event.CreatedAt
			if at.IsZero() {
				at = time.Now().UTC()
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO loop_run_events(workspace_key,run_id,seq,kind,data,created_at) VALUES(?,?,?,?,?,?)`, workspace, id, seq, event.Kind, []byte(data), formatLoopRunTime(at)); err != nil {
				return run, err
			}
		}
	}
	return run, tx.Commit()
}

// LoopRunEvents lists a run's log after the given sequence number.
func (s *SQLiteStore) LoopRunEvents(ctx context.Context, workspace, runID string, after int64) ([]LoopRunEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq,kind,data,created_at FROM loop_run_events WHERE workspace_key=? AND run_id=? AND seq>? ORDER BY seq LIMIT 1000`, workspace, runID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []LoopRunEvent{}
	for rows.Next() {
		var event LoopRunEvent
		var raw []byte
		var created string
		if err := rows.Scan(&event.Seq, &event.Kind, &raw, &created); err != nil {
			return nil, err
		}
		event.Data = json.RawMessage(raw)
		event.CreatedAt, _ = time.Parse(loopRunTime, created)
		events = append(events, event)
	}
	return events, rows.Err()
}

// HeartbeatLoopRun renews the lease owner holds on a run (while the run, or
// the agent's answer to a reply on it, executes) and reports whether a cancel
// was requested. ErrLoopRunLeaseLost means owner no longer holds the lease
// (the work finished, or the run was reconciled as interrupted).
func (s *SQLiteStore) HeartbeatLoopRun(ctx context.Context, workspace, id, owner string, now, expires time.Time) (bool, error) {
	if _, err := s.db.ExecContext(ctx, `UPDATE loop_run_records SET heartbeat_at=?,lease_expires_at=? WHERE workspace_key=? AND id=? AND lease_owner=?`, formatLoopRunTime(now), formatLoopRunTime(expires), workspace, id, owner); err != nil {
		return false, err
	}
	// MySQL counts changed rows only, so read the holder back instead of
	// trusting RowsAffected.
	var holder string
	var cancel int
	err := s.db.QueryRowContext(ctx, `SELECT lease_owner,cancel_requested FROM loop_run_records WHERE workspace_key=? AND id=?`, workspace, id).Scan(&holder, &cancel)
	if errors.Is(err, sql.ErrNoRows) || err == nil && holder != owner {
		return false, ErrLoopRunLeaseLost
	}
	if err != nil {
		return false, err
	}
	return cancel != 0, nil
}

// ClaimLoopRunLease gives owner the lease on a run that holds none (a reply
// starts executing on a finished run).
func (s *SQLiteStore) ClaimLoopRunLease(ctx context.Context, workspace, id string, lease LoopRunLease) error {
	// A matched row always changes (the owner was empty), so RowsAffected is
	// reliable here on every dialect.
	result, err := s.db.ExecContext(ctx, `UPDATE loop_run_records SET lease_owner=?,lease_expires_at=?,heartbeat_at=?,cancel_requested=0 WHERE workspace_key=? AND id=? AND lease_owner=''`, lease.Owner, formatLoopRunTime(lease.ExpiresAt), formatLoopRunTime(time.Now()), workspace, id)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil || changed > 0 {
		return err
	}
	if _, err := s.LoopRun(ctx, workspace, id); err != nil {
		return err
	}
	return ErrLoopRunLeaseLost
}

// ReleaseLoopRunLease drops the lease when the work finished. An empty owner
// releases whoever holds it.
func (s *SQLiteStore) ReleaseLoopRunLease(ctx context.Context, workspace, id, owner string) error {
	query := `UPDATE loop_run_records SET lease_owner='',lease_expires_at='',cancel_requested=0 WHERE workspace_key=? AND id=?`
	args := []any{workspace, id}
	if owner != "" {
		query += ` AND lease_owner=?`
		args = append(args, owner)
	}
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

// RequestLoopRunCancel flags a run whose work holds a lease; the process that
// holds it sees the flag on its next heartbeat. It reports false when no
// process holds the run.
func (s *SQLiteStore) RequestLoopRunCancel(ctx context.Context, workspace, id string) (bool, error) {
	if _, err := s.db.ExecContext(ctx, `UPDATE loop_run_records SET cancel_requested=1 WHERE workspace_key=? AND id=? AND lease_owner<>'' AND lease_expires_at>?`, workspace, id, formatLoopRunTime(time.Now())); err != nil {
		return false, err
	}
	var cancel int
	err := s.db.QueryRowContext(ctx, `SELECT cancel_requested FROM loop_run_records WHERE workspace_key=? AND id=?`, workspace, id).Scan(&cancel)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return cancel != 0, err
}

// RunningLoopRuns lists, across workspaces, every run that is running or
// whose work holds a lease, with the lease.
func (s *SQLiteStore) RunningLoopRuns(ctx context.Context) ([]RunningLoopRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_key,id,loop_id,lease_owner,lease_expires_at,heartbeat_at,cancel_requested FROM loop_run_records WHERE status='running' OR lease_owner<>'' LIMIT 1000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []RunningLoopRun{}
	for rows.Next() {
		var run RunningLoopRun
		var expires, heartbeat string
		var cancel int
		if err := rows.Scan(&run.Workspace, &run.ID, &run.LoopID, &run.LeaseOwner, &expires, &heartbeat, &cancel); err != nil {
			return nil, err
		}
		run.LeaseExpiresAt, _ = time.Parse(loopRunTime, expires)
		run.HeartbeatAt, _ = time.Parse(loopRunTime, heartbeat)
		run.CancelRequested = cancel != 0
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// LoopRunCounts counts each loop's runs started since the given time.
func (s *SQLiteStore) LoopRunCounts(ctx context.Context, workspace string, since time.Time) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT loop_id,COUNT(*) FROM loop_run_records WHERE workspace_key=? AND started_at>=? GROUP BY loop_id`, workspace, formatLoopRunTime(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var loopID string
		var count int
		if err := rows.Scan(&loopID, &count); err != nil {
			return nil, err
		}
		counts[loopID] = count
	}
	return counts, rows.Err()
}

// TrimLoopRuns keeps a loop's newest finished runs (running runs are never
// removed) and their event logs.
func (s *SQLiteStore) TrimLoopRuns(ctx context.Context, workspace, loopID string, keep int) error {
	var cutoff string
	err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT started_at FROM loop_run_records WHERE workspace_key=? AND loop_id=? ORDER BY started_at DESC,id DESC LIMIT 1 OFFSET %d`, keep), workspace, loopID).Scan(&cutoff)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.deleteLoopRunsWhere(ctx, workspace, `loop_id=? AND started_at<=? AND status<>'running'`, loopID, cutoff)
}

// DeleteLoopRuns removes every run of a deleted loop.
func (s *SQLiteStore) DeleteLoopRuns(ctx context.Context, workspace, loopID string) error {
	return s.deleteLoopRunsWhere(ctx, workspace, `loop_id=?`, loopID)
}

func (s *SQLiteStore) deleteLoopRunsWhere(ctx context.Context, workspace, condition string, args ...any) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM loop_run_records WHERE workspace_key=? AND `+condition, append([]any{workspace}, args...)...)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for start := 0; start < len(ids); start += 100 {
		chunk := ids[start:min(start+100, len(ids))]
		marks := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		chunkArgs := []any{workspace}
		for _, id := range chunk {
			chunkArgs = append(chunkArgs, id)
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM loop_run_events WHERE workspace_key=? AND run_id IN (`+marks+`)`, chunkArgs...); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM loop_run_records WHERE workspace_key=? AND id IN (`+marks+`)`, chunkArgs...); err != nil {
			return err
		}
	}
	return nil
}

// migrateLoopRunRecords moves runs stored in the workspace metadata
// (Bootstrap.LoopRuns, before runs had their own tables) into
// loop_run_records once, then drops them from the metadata. Inserts skip runs
// that already exist, so an interrupted migration resumes where it stopped.
// Runs that were running are copied without a lease; the run reconciler marks
// them interrupted.
func (s *SQLiteStore) migrateLoopRunRecords(ctx context.Context) error {
	for key, data := range s.workspaces {
		if len(data.LoopRuns) == 0 {
			continue
		}
		started := time.Now()
		runs := data.LoopRuns
		for start := 0; start < len(runs); start += 100 {
			chunk := runs[start:min(start+100, len(runs))]
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			for _, run := range chunk {
				if run.ID == "" {
					continue
				}
				raw, err := storedLoopRun(run)
				if err != nil {
					tx.Rollback()
					return err
				}
				finished := ""
				if run.FinishedAt != nil {
					finished = formatLoopRunTime(*run.FinishedAt)
				}
				status := firstNonEmptyString(run.Status, "completed")
				if _, err := tx.ExecContext(ctx, `INSERT INTO loop_run_records(workspace_key,id,loop_id,status,started_at,finished_at,lease_owner,lease_expires_at,heartbeat_at,cancel_requested,data) VALUES(?,?,?,?,?,?,'','','',0,?) ON CONFLICT(workspace_key,id) DO NOTHING`,
					key, run.ID, run.LoopID, status, formatLoopRunTime(run.StartedAt), finished, raw); err != nil {
					tx.Rollback()
					return err
				}
			}
			if err := tx.Commit(); err != nil {
				return err
			}
		}
		before := data
		after := data
		after.LoopRuns = nil
		if err := s.persistWorkspaceFrom(ctx, key, &before, after, nil); err != nil {
			return fmt.Errorf("drop migrated loop runs from workspace %s: %w", key, err)
		}
		s.workspaces[key] = after
		if _, err := s.db.ExecContext(ctx, `INSERT INTO loop_run_migrations(workspace_key,migrated_runs,migrated_at) VALUES(?,?,?) ON CONFLICT(workspace_key) DO UPDATE SET migrated_runs=excluded.migrated_runs,migrated_at=excluded.migrated_at`, key, len(runs), formatLoopRunTime(time.Now())); err != nil {
			return err
		}
		slog.Info("migrated loop runs to loop_run_records", "workspace", key, "runs", len(runs), "duration_ms", time.Since(started).Milliseconds())
	}
	return nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
