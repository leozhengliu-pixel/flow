package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type NotificationQuery struct {
	Workspace, UserID, Cursor                       string
	IncludeArchived, IncludeDeleted, IncludeSnoozed bool
	Read                                            *bool
	Limit                                           int
}
type NotificationPage struct {
	Notifications []domain.Notification `json:"notifications"`
	UnreadCount   int                   `json:"unreadCount"`
	NextCursor    string                `json:"nextCursor,omitempty"`
	HasMore       bool                  `json:"hasMore"`
}
type contentCursor struct{ Date, ID string }

func (s *SQLiteStore) jsonText(column, field string) string {
	switch s.dialect {
	case "mysql":
		return "NULLIF(JSON_UNQUOTE(JSON_EXTRACT(CONVERT(" + column + " USING utf8mb4), '$." + field + "')), 'null')"
	case "postgres":
		if strings.Contains(field, ".") {
			return "(convert_from(" + column + ", 'UTF8')::jsonb #>> '{" + strings.ReplaceAll(field, ".", ",") + "}')"
		}
		return "(convert_from(" + column + ", 'UTF8')::jsonb ->> '" + field + "')"
	default:
		return "json_extract(" + column + ", '$." + field + "')"
	}
}

func (s *SQLiteStore) QueryNotifications(ctx context.Context, q NotificationQuery) (NotificationPage, error) {
	result := NotificationPage{Notifications: []domain.Notification{}}
	where := "workspace_key=? AND kind='notification' AND owner_id=?"
	args := []any{q.Workspace, q.UserID}
	if !q.IncludeArchived {
		where += " AND " + s.jsonText("data", "archivedAt") + " IS NULL"
	}
	if !q.IncludeDeleted {
		where += " AND " + s.jsonText("data", "deletedAt") + " IS NULL"
	}
	if !q.IncludeSnoozed {
		where += " AND (next_attempt_at='' OR next_attempt_at<=?)"
		args = append(args, time.Now().UTC().Format(issueRecordTimestamp))
	}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM workspace_content_records WHERE "+where+" AND "+s.jsonText("data", "readAt")+" IS NULL", args...).Scan(&result.UnreadCount); err != nil {
		return result, err
	}
	if q.Read != nil {
		where += " AND " + s.jsonText("data", "readAt")
		if *q.Read {
			where += " IS NOT NULL"
		} else {
			where += " IS NULL"
		}
	}
	if q.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)
		var cursor contentCursor
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Date == "" || cursor.ID == "" {
			return result, ErrIssueQuery
		}
		where += " AND (created_at<? OR (created_at=? AND id<?))"
		args = append(args, cursor.Date, cursor.Date, cursor.ID)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, "SELECT data,created_at,id FROM workspace_content_records WHERE "+where+" ORDER BY created_at DESC,id DESC LIMIT ?", append(args, limit+1)...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	var last contentCursor
	for rows.Next() {
		var raw []byte
		var cursor contentCursor
		if err := rows.Scan(&raw, &cursor.Date, &cursor.ID); err != nil {
			return result, err
		}
		if len(result.Notifications) == limit {
			result.HasMore = true
			break
		}
		var n domain.Notification
		if err := json.Unmarshal(raw, &n); err != nil {
			return result, err
		}
		if n.RecipientID != q.UserID {
			return result, fmt.Errorf("invalid notification owner index")
		}
		result.Notifications = append(result.Notifications, n)
		last = cursor
	}
	if result.HasMore {
		raw, _ := json.Marshal(last)
		result.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	rows.Close()
	ids := []string{}
	for _, notification := range result.Notifications {
		ids = append(ids, notification.Actor.ID)
	}
	refs, err := s.readIssueReferences(ctx, q.Workspace, ids)
	if err != nil {
		return result, err
	}
	for i, notification := range result.Notifications {
		if actor, ok := refs.users[notification.Actor.ID]; ok {
			result.Notifications[i].Actor = actor
		}
	}
	return result, nil
}
