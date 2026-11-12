package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type RealtimeReplayWindow struct {
	WorkspaceID    string
	Head           int64
	ResyncRequired bool
	MaxRecordBytes int
}

func (s *SQLiteStore) OpenRealtimeReplay(ctx context.Context, workspaceID string, since int64) (RealtimeReplayWindow, error) {
	window := RealtimeReplayWindow{WorkspaceID: workspaceID}
	if err := s.rescueStaleSequencing(ctx, workspaceID); err != nil {
		return window, err
	}
	var pruned int64
	if err := s.db.QueryRowContext(ctx, `SELECT head,pruned FROM workspace_event_seq WHERE workspace_id=?`, workspaceID).Scan(&window.Head, &pruned); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return window, err
	}
	window.ResyncRequired = since < 0 || since > window.Head || since < pruned
	return window, nil
}

func (s *SQLiteStore) ReadRealtimeReplay(ctx context.Context, workspaceID string, since int64, maxBytes int) (ChangePage, error) {
	window, err := s.OpenRealtimeReplay(ctx, workspaceID, since)
	if err != nil {
		return ChangePage{}, err
	}
	window.MaxRecordBytes = maxBytes
	return s.ReadRealtimeReplayPage(ctx, window, since, maxBytes)
}

// The SQL window transfers only a byte-bounded prefix. The overflow row is
// left after the returned cursor, so the next keyset page reads it in full.
func (s *SQLiteStore) ReadRealtimeReplayPage(ctx context.Context, window RealtimeReplayWindow, since int64, maxBytes int) (ChangePage, error) {
	page := ChangePage{Head: window.Head, ResyncRequired: window.ResyncRequired}
	if page.ResyncRequired || since >= window.Head {
		return page, nil
	}
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	length := "OCTET_LENGTH(payload)"
	if s.dialect == "sqlite" {
		length = "LENGTH(CAST(payload AS BLOB))"
	}
	query := `SELECT seq,id,event_type,aggregate_id,CASE WHEN payload_bytes<=? AND (running_bytes<=? OR ordinal=1) THEN payload ELSE NULL END,created_at,actor_id,payload_bytes FROM (SELECT bounded.*,SUM(payload_bytes+512) OVER (ORDER BY seq) AS running_bytes,ROW_NUMBER() OVER (ORDER BY seq) AS ordinal FROM (SELECT seq,id,event_type,aggregate_id,payload,created_at,actor_id,` + length + ` AS payload_bytes FROM domain_events WHERE workspace_id=? AND seq>? AND seq<=? ORDER BY seq LIMIT 64) bounded) budgeted ORDER BY seq`
	maxRecord := window.MaxRecordBytes
	if maxRecord <= 0 {
		maxRecord = 8 << 20
	}
	rows, err := s.db.QueryContext(ctx, query, maxRecord, maxBytes, window.WorkspaceID, since, window.Head)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var record ChangeRecord
		var raw []byte
		var created string
		var actor sql.NullString
		var payloadBytes int
		if err := rows.Scan(&record.Seq, &record.Event.ID, &record.Event.Type, &record.Event.AggregateID, &raw, &created, &actor, &payloadBytes); err != nil {
			return page, err
		}
		if record.Seq != since+1 {
			page.Records = nil
			page.ResyncRequired = true
			return page, nil
		}
		if raw == nil {
			if len(page.Records) == 0 {
				page.ResyncRequired = true
			}
			page.HasMore = !page.ResyncRequired
			return page, nil
		}
		record.Event.Payload = raw
		record.Event.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		record.ActorID = actor.String
		page.Records = append(page.Records, record)
		since = record.Seq
		count++
	}
	page.HasMore = since < window.Head
	if count == 0 && page.HasMore {
		page.ResyncRequired = true
		page.HasMore = false
	}
	return page, rows.Err()
}
