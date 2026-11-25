package store

import (
	"cmp"
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
//
// Events are ordered by (time, id). Rows written before fixed-width times hold
// RFC3339Nano text, which misorders events within one second
// (stored_time.go), but every text form of one second shares its first 19
// bytes, so in stored (created_at,id) order, which the aggregate index serves,
// each second's rows are adjacent and only their order within the second is
// off. A page therefore reads in stored order from the cursor's second, orders
// whole seconds by decoded time and takes no row from a second the read may
// have cut off; when that leaves too few rows it reads again, further. No event
// is skipped or repeated between pages.
func (s *SQLiteStore) EventsPage(ctx context.Context, workspaceID, aggregateID, cursor string, limit int) (events []domain.DomainEvent, next string, err error) {
	var after eventCursor
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(raw, &after) != nil || after.CreatedAt == "" || after.ID == "" {
			return nil, "", ErrInvalidEventCursor
		}
		// Cursors issued before fixed-width times carry RFC3339Nano text.
		if after.CreatedAt = normalizeStoredTime(after.CreatedAt); len(after.CreatedAt) < storedTimeSecond {
			return nil, "", ErrInvalidEventCursor
		}
	}
	limit = max(limit, 1)
	for read := limit + 1; ; read *= 2 {
		page, cut, err := s.eventPageRows(ctx, workspaceID, aggregateID, after, read)
		if err != nil {
			return nil, "", err
		}
		if cut && len(page) <= limit {
			continue
		}
		if len(page) > limit {
			page = page[:limit]
			last := page[limit-1]
			raw, _ := json.Marshal(eventCursor{CreatedAt: last.at, ID: last.event.ID})
			next = base64.RawURLEncoding.EncodeToString(raw)
		}
		events = make([]domain.DomainEvent, len(page))
		for index, row := range page {
			events[index] = row.event
		}
		return events, next, nil
	}
}

type eventPageRow struct {
	event domain.DomainEvent
	// at is the stored time in storedTimeLayout.
	at string
}

// eventPageRows reads up to read rows of the aggregate in stored order from
// the cursor's second and returns those after the cursor in (time,id) order,
// leaving out the last second read when the read was cut off (cut).
func (s *SQLiteStore) eventPageRows(ctx context.Context, workspaceID, aggregateID string, after eventCursor, read int) (page []eventPageRow, cut bool, err error) {
	query := `SELECT id,event_type,aggregate_id,payload,previous_values,created_at FROM domain_events WHERE aggregate_id = ? AND workspace_id = ?`
	args := []any{eventAggregateKey(aggregateID), workspaceID}
	if after.ID != "" {
		query += ` AND created_at >= ?`
		args = append(args, after.CreatedAt[:storedTimeSecond])
	}
	query += ` ORDER BY created_at ASC, id ASC LIMIT ?`
	args = append(args, read)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	seconds := []string{}
	for rows.Next() {
		var row eventPageRow
		var created string
		var previous sql.NullString
		if err := rows.Scan(&row.event.ID, &row.event.Type, &row.event.AggregateID, &row.event.Payload, &previous, &created); err != nil {
			return nil, false, err
		}
		if previous.Valid && previous.String != "" {
			row.event.PreviousValues = json.RawMessage(previous.String)
		}
		row.event.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		row.at = normalizeStoredTime(created)
		page = append(page, row)
		seconds = append(seconds, created[:min(len(created), storedTimeSecond)])
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if cut = len(page) == read; cut {
		last := seconds[len(seconds)-1]
		for len(page) > 0 && seconds[len(page)-1] == last {
			page = page[:len(page)-1]
		}
	}
	if after.ID != "" {
		page = slices.DeleteFunc(page, func(row eventPageRow) bool {
			return row.at < after.CreatedAt || row.at == after.CreatedAt && row.event.ID <= after.ID
		})
	}
	slices.SortStableFunc(page, func(a, b eventPageRow) int {
		return cmp.Or(strings.Compare(a.at, b.at), strings.Compare(a.event.ID, b.event.ID))
	})
	return page, cut, nil
}
