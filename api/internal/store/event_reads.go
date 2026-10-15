package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// ErrInvalidEventCursor reports a cursor that EventsPage did not issue.
var ErrInvalidEventCursor = errors.New("invalid event cursor")

type eventCursor struct {
	CreatedAt string `json:"t"`
	ID        string `json:"id"`
}

// EventsPage reads up to limit domain events that workspaceID recorded for
// one aggregate, oldest first, after cursor. next is empty on the last page.
// Pages follow the stored (created_at,id) order, which the aggregate index
// serves, so no event is skipped or repeated between pages; each page is then
// ordered by decoded instant like Events (RFC3339Nano drops trailing zeros, so
// the stored text can misorder events within the same second).
func (s *SQLiteStore) EventsPage(ctx context.Context, workspaceID, aggregateID, cursor string, limit int) (events []domain.DomainEvent, next string, err error) {
	query := `SELECT id,event_type,aggregate_id,payload,previous_values,created_at FROM domain_events WHERE aggregate_id = ? AND workspace_id = ?`
	args := []any{aggregateID, workspaceID}
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		var after eventCursor
		if err != nil || json.Unmarshal(raw, &after) != nil || after.CreatedAt == "" || after.ID == "" {
			return nil, "", ErrInvalidEventCursor
		}
		query += ` AND (created_at > ? OR (created_at = ? AND id > ?))`
		args = append(args, after.CreatedAt, after.CreatedAt, after.ID)
	}
	query += ` ORDER BY created_at ASC, id ASC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	events = []domain.DomainEvent{}
	var last eventCursor
	for rows.Next() {
		var event domain.DomainEvent
		var created string
		var previous sql.NullString
		if err := rows.Scan(&event.ID, &event.Type, &event.AggregateID, &event.Payload, &previous, &created); err != nil {
			return nil, "", err
		}
		if len(events) == limit {
			raw, _ := json.Marshal(last)
			next = base64.RawURLEncoding.EncodeToString(raw)
			break
		}
		if previous.Valid && previous.String != "" {
			event.PreviousValues = json.RawMessage(previous.String)
		}
		event.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		events = append(events, event)
		last = eventCursor{CreatedAt: created, ID: event.ID}
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	slices.SortStableFunc(events, func(a, b domain.DomainEvent) int {
		if order := a.CreatedAt.Compare(b.CreatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return events, next, nil
}
