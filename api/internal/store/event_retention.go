package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// domain_events is an append-only log and, through its per-workspace seq
// column, the change log behind GET /api/realtime/changes (change_log.go).
// Clients that resume from a cursor older than the pruned range are told to
// reload a snapshot (workspace_event_seq.pruned records the high-water mark).
// Pruning therefore keeps events newer than the retention cutoff (never
// shorter than MinChangeLogRetention, enforced by the caller) and, per
// workspace, the newest SetChangeLogMinRows changes regardless of age. Besides
// the change log, GET /api/events and one startup migration read it back.

// retainedDomainEventTypes are never pruned: agentWebSearchFirstEnabled
// reads them on startup. They are rare and small.
var retainedDomainEventTypes = []string{"workspace_preferences.updated"}

// DomainEventPruneBatch is how many events one prune statement deletes.
const DomainEventPruneBatch = 1000

// PruneDomainEvents deletes up to one batch of events created before cutoff
// and reports how many it deleted. Callers loop (pausing between calls) until
// it deletes fewer than DomainEventPruneBatch, so no single statement holds
// locks for long.
func (s *SQLiteStore) PruneDomainEvents(ctx context.Context, cutoff time.Time) (int, error) {
	args := []any{cutoff.UTC().Format(time.RFC3339Nano)}
	for _, kind := range retainedDomainEventTypes {
		args = append(args, kind)
	}
	args = append(args, s.changeLogMinRows.Load(), DomainEventPruneBatch)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// Rows inside a workspace's newest-N window are kept; rows without a
	// cursor (written by an older version) are plain old events.
	rows, err := tx.QueryContext(ctx, `SELECT d.id,d.workspace_id,d.seq FROM domain_events d WHERE d.created_at<? AND d.event_type NOT IN (`+strings.TrimSuffix(strings.Repeat("?,", len(retainedDomainEventTypes)), ",")+`) AND (d.seq IS NULL OR d.seq<=(SELECT e.head-? FROM workspace_event_seq e WHERE e.workspace_id=d.workspace_id)) ORDER BY d.created_at LIMIT ?`, args...)
	if err != nil {
		return 0, err
	}
	ids := []string{}
	prunedThrough := map[string]int64{}
	for rows.Next() {
		var id string
		var workspace sql.NullString
		var seq sql.NullInt64
		if err := rows.Scan(&id, &workspace, &seq); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
		if workspace.Valid && seq.Valid {
			prunedThrough[workspace.String] = max(prunedThrough[workspace.String], seq.Int64)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	for workspace, seq := range prunedThrough {
		if _, err := tx.ExecContext(ctx, `UPDATE workspace_event_seq SET pruned=? WHERE workspace_id=? AND pruned<?`, seq, workspace, seq); err != nil {
			return 0, err
		}
	}
	clause, idArgs := bindList("id", ids)
	result, err := tx.ExecContext(ctx, `DELETE FROM domain_events WHERE `+clause, idArgs...)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	deleted, err := result.RowsAffected()
	return int(deleted), err
}
