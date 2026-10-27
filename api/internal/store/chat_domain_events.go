package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"flow/api/internal/domain"
)

var ErrChatDomainCursorExpired = errors.New("chat notification cursor is older than the retained event log")

type ChatDomainEvent struct {
	Seq   int64
	Event domain.DomainEvent
}

func (s *SQLiteStore) createChatDomainCursorSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS chat_domain_cursors (workspace_key VARCHAR(191) PRIMARY KEY, seq BIGINT NOT NULL, lease_until BIGINT NOT NULL DEFAULT 0)`)
	return err
}

// InitializeChatDomainCursor must run before the first binding becomes active.
// The existing workspace sequence follows commit order, so replay cannot skip
// an event committed between a live callback and a worker restart.
func (s *SQLiteStore) InitializeChatDomainCursor(ctx context.Context, workspace string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO chat_domain_cursors(workspace_key,seq) SELECT w.workspace_key,COALESCE(e.head,0) FROM workspace_states w LEFT JOIN workspace_event_seq e ON e.workspace_id=w.workspace_id WHERE w.workspace_key=? ON CONFLICT DO NOTHING`, workspace)
	return err
}

func (s *SQLiteStore) ChatDomainEventPage(ctx context.Context, workspace string, limit int) ([]ChatDomainEvent, error) {
	if limit < 1 || limit > 500 {
		limit = 200
	}
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var since, head, pruned int64
	var workspaceID string
	err = tx.QueryRowContext(ctx, `SELECT c.seq,COALESCE(e.head,0),COALESCE(e.pruned,0),w.workspace_id FROM chat_domain_cursors c JOIN workspace_states w ON w.workspace_key=c.workspace_key LEFT JOIN workspace_event_seq e ON e.workspace_id=w.workspace_id WHERE c.workspace_key=?`, workspace).Scan(&since, &head, &pruned, &workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if since < pruned || since > head {
		return nil, ErrChatDomainCursorExpired
	}
	rows, err := tx.QueryContext(ctx, `SELECT seq,id,event_type,aggregate_id,payload,previous_values,created_at,actor_id FROM domain_events WHERE workspace_id=? AND seq>? ORDER BY seq ASC LIMIT ?`, workspaceID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ChatDomainEvent{}
	for rows.Next() {
		var record ChatDomainEvent
		var created string
		var previous, actor sql.NullString
		if err := rows.Scan(&record.Seq, &record.Event.ID, &record.Event.Type, &record.Event.AggregateID, &record.Event.Payload, &previous, &created, &actor); err != nil {
			return nil, err
		}
		if previous.Valid {
			record.Event.PreviousValues = json.RawMessage(previous.String)
		}
		record.Event.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, err
		}
		record.Event.ActorID = actor.String
		result = append(result, record)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) AdvanceChatDomainCursor(ctx context.Context, workspace string, seq int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chat_domain_cursors SET seq=? WHERE workspace_key=? AND seq<?`, seq, workspace, seq)
	return err
}

// ClaimChatWorker serializes message updates even when several servers share a
// database without a Redis coordinator. The worker deadline must be shorter
// than this lease; conditional release cannot clear a successor's claim.
func (s *SQLiteStore) ClaimChatWorker(ctx context.Context, workspace string, now time.Time, lease time.Duration) (time.Time, bool, error) {
	until := time.UnixMilli(now.Add(lease).UnixMilli())
	result, err := s.db.ExecContext(ctx, `UPDATE chat_domain_cursors SET lease_until=? WHERE workspace_key=? AND lease_until<=?`, until.UnixMilli(), workspace, now.UnixMilli())
	if err != nil {
		return time.Time{}, false, err
	}
	count, err := result.RowsAffected()
	return until, count == 1, err
}

func (s *SQLiteStore) ReleaseChatWorker(ctx context.Context, workspace string, lease time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chat_domain_cursors SET lease_until=0 WHERE workspace_key=? AND lease_until=?`, workspace, lease.UnixMilli())
	return err
}
