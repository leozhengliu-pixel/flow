package store

import (
	"context"
	"strings"
	"time"
)

// domain_events is an append-only log. Nothing that keeps clients in sync
// reads it: realtime catch-up replays an in-memory ring per workspace and
// asks for a full resync (workspace.resync_required) when the client's cursor
// is older than the ring, webhooks are queued from the committed event in
// process, and the audit log is its own collection. Only GET /api/events and
// one startup migration read it back, so old events can be pruned.

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
	args = append(args, DomainEventPruneBatch)
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM domain_events WHERE created_at<? AND event_type NOT IN (`+strings.TrimSuffix(strings.Repeat("?,", len(retainedDomainEventTypes)), ",")+`) ORDER BY created_at LIMIT ?`, args...)
	if err != nil {
		return 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	clause, idArgs := bindList("id", ids)
	result, err := s.db.ExecContext(ctx, `DELETE FROM domain_events WHERE `+clause, idArgs...)
	if err != nil {
		return 0, err
	}
	deleted, err := result.RowsAffected()
	return int(deleted), err
}
