package store

import (
	"context"
	"encoding/json"
	"flow/api/internal/domain"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) jsonPatchExpression(field string) string {
	switch s.dialect {
	case "mysql":
		return "JSON_SET(CONVERT(data USING utf8mb4), '$." + field + "', ?, '$.updatedAt', ?)"
	case "postgres":
		return "convert_to((convert_from(data,'UTF8')::jsonb || jsonb_build_object('" + field + "',?::text,'updatedAt',?::text))::text,'UTF8')"
	default:
		return "json_set(data, '$." + field + "', ?, '$.updatedAt', ?)"
	}
}

func (s *SQLiteStore) BatchNotificationRecords(ctx context.Context, workspace, user string, input domain.NotificationBatchInput, snooze *time.Time) (int, error) {
	field := ""
	clear := false
	switch input.Action {
	case "delete", "deleteAll", "deleteRead", "deleteReadCompleted":
		field = "deletedAt"
	case "markRead", "markAllRead":
		field = "readAt"
	case "markUnread":
		field = "readAt"
		clear = true
	case "archive", "archiveAll":
		field = "archivedAt"
	case "unarchive":
		field = "archivedAt"
		clear = true
	case "snooze", "snoozeAll":
		field = "snoozedUntil"
	case "unsnooze":
		field = "snoozedUntil"
		clear = true
	default:
		return 0, ErrIssueQuery
	}
	where := "workspace_key=? AND kind='notification' AND owner_id=?"
	args := []any{workspace, user}
	if len(input.IDs) > 0 {
		clause, values := bindList("id", input.IDs)
		where += " AND " + clause
		args = append(args, values...)
	}
	if strings.HasPrefix(input.Action, "deleteRead") {
		where += " AND " + s.jsonText("data", "readAt") + " IS NOT NULL"
	}
	if input.Action == "deleteReadCompleted" {
		where += " AND resource_id IN (SELECT id FROM issue_records WHERE workspace_key=? AND state_type IN ('completed','canceled'))"
		args = append(args, workspace)
	}
	var updated int
	var event domain.DomainEvent
	err := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		now := time.Now().UTC()
		var value any = now.Format(time.RFC3339Nano)
		if clear {
			value = nil
		}
		if field == "snoozedUntil" && snooze != nil {
			value = snooze.UTC().Format(time.RFC3339Nano)
		}
		last := ""
		for {
			rows, err := tx.QueryContext(ctx, "SELECT id FROM workspace_content_records WHERE "+where+" AND id>? ORDER BY id LIMIT 500", append(args, last)...)
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
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			if len(ids) == 0 {
				break
			}
			clause, values := bindList("id", ids)
			statement := "UPDATE workspace_content_records SET data=" + s.jsonPatchExpression(field)
			params := []any{value, now.Format(time.RFC3339Nano)}
			if field == "snoozedUntil" {
				next := ""
				if snooze != nil && !clear {
					next = snooze.UTC().Format(issueRecordTimestamp)
				}
				statement += ",next_attempt_at=?"
				params = append(params, next)
			}
			statement += " WHERE workspace_key=? AND kind='notification' AND owner_id=? AND " + clause
			params = append(params, workspace, user)
			params = append(params, values...)
			if _, err := tx.ExecContext(ctx, statement, params...); err != nil {
				return err
			}
			status := "CASE WHEN " + s.jsonText("data", "deletedAt") + " IS NOT NULL THEN 'deleted' WHEN " + s.jsonText("data", "archivedAt") + " IS NOT NULL THEN 'archived' WHEN " + s.jsonText("data", "readAt") + " IS NOT NULL THEN 'read' ELSE 'unread' END"
			if _, err := tx.ExecContext(ctx, "UPDATE workspace_content_records SET status="+status+" WHERE workspace_key=? AND kind='notification' AND owner_id=? AND "+clause, append([]any{workspace, user}, values...)...); err != nil {
				return err
			}
			updated += len(ids)
			last = ids[len(ids)-1]
		}
		payload, _ := json.Marshal(input)
		event = domain.DomainEvent{ID: fmt.Sprintf("evt_%d", time.Now().UnixNano()), Type: "notifications.batch_updated", AggregateID: user, Payload: payload, CreatedAt: now}
		if _, err := tx.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,created_at) VALUES(?,?,?,?,?)`, event.ID, event.Type, event.AggregateID, payload, now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		return tx.Commit()
	}()
	if err != nil {
		return 0, err
	}
	if sink := s.realtime(); sink != nil {
		sink(workspace, domain.RealtimeEvent{ID: event.ID, Type: event.Type, AggregateID: user, ActorID: user, ClientID: realtimeClientFromContext(ctx), Payload: event.Payload, CreatedAt: event.CreatedAt})
	}
	return updated, nil
}
