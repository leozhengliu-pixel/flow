package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"flow/api/internal/domain"
)

func (s *SQLiteStore) HasNotificationDeliveries(ctx context.Context, workspace string) bool {
	var id string
	return s.db.QueryRowContext(ctx, `SELECT id FROM workspace_content_records WHERE workspace_key=? AND kind='delivery' LIMIT 1`, workspace).Scan(&id) == nil
}

func (s *SQLiteStore) IssueContent(ctx context.Context, workspace, id string) ([]domain.Comment, []domain.ActivityEvent, error) {
	comments, err := readContentRecords[domain.Comment](ctx, s, workspace, "comment", &id)
	if err != nil {
		return nil, nil, err
	}
	activities, err := readContentRecords[domain.ActivityEvent](ctx, s, workspace, "activity", &id)
	if err != nil {
		return nil, nil, err
	}
	return comments[id], activities[id], nil
}

func (s *SQLiteStore) ensureContentRecords(ctx context.Context) error {
	blob := "BLOB"
	if s.dialect == "mysql" {
		blob = "LONGBLOB"
	}
	if s.dialect == "postgres" {
		blob = "BYTEA"
	}
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE IF NOT EXISTS workspace_content_records(workspace_key VARCHAR(191) NOT NULL,kind VARCHAR(24) NOT NULL,resource_id VARCHAR(191) NOT NULL,id VARCHAR(191) NOT NULL,created_at VARCHAR(40) NOT NULL,data %s NOT NULL,PRIMARY KEY(workspace_key,kind,resource_id,id))`, blob))
	if err != nil {
		return err
	}
	for _, column := range []string{"owner_id VARCHAR(191)", "parent_id VARCHAR(191)", "status VARCHAR(32)", "next_attempt_at VARCHAR(40)"} {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE workspace_content_records ADD COLUMN `+column+` NOT NULL DEFAULT ''`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") && !strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE workspace_content_records ADD COLUMN record_version INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") && !strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return err
	}
	for _, index := range []string{"content_owner_idx ON workspace_content_records(workspace_key,kind,owner_id,created_at,id)", "content_parent_idx ON workspace_content_records(workspace_key,kind,parent_id,id)", "content_identity_idx ON workspace_content_records(workspace_key,kind,id)", "content_status_idx ON workspace_content_records(workspace_key,kind,status,next_attempt_at,id)", "content_version_idx ON workspace_content_records(record_version,workspace_key,kind,id)"} {
		prefix := "CREATE INDEX IF NOT EXISTS "
		if s.dialect == "mysql" {
			prefix = "CREATE INDEX "
		}
		if _, err := s.db.ExecContext(ctx, prefix+index); err != nil && !(s.dialect == "mysql" && isDuplicateIndex(err)) {
			return err
		}
	}
	return nil
}

func contentRecordIdentity[T any](item T) (string, string, error) {
	raw, err := json.Marshal(item)
	if err != nil {
		return "", "", err
	}
	var identity struct {
		ID        string `json:"id"`
		CreatedAt string `json:"createdAt"`
	}
	err = json.Unmarshal(raw, &identity)
	return identity.ID, identity.CreatedAt, err
}

func writeContentRecord[T any](ctx context.Context, tx *sqlTx, workspace, kind, resource string, item T) error {
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	id, created, err := contentRecordIdentity(item)
	if err != nil {
		return err
	}
	var identity struct {
		RecipientID    string      `json:"recipientId"`
		NotificationID string      `json:"notificationId"`
		ParentID       *string     `json:"parentId"`
		Status         string      `json:"status"`
		NextAttemptAt  *time.Time  `json:"nextAttemptAt"`
		Actor          domain.User `json:"actor"`
		User           domain.User `json:"user"`
	}
	if err := json.Unmarshal(raw, &identity); err != nil {
		return err
	}
	owner := identity.RecipientID
	if owner == "" {
		owner = identity.Actor.ID
	}
	if owner == "" {
		owner = identity.User.ID
	}
	parent := identity.NotificationID
	if parent == "" && identity.ParentID != nil {
		parent = *identity.ParentID
	}
	nextAttempt := ""
	if identity.NextAttemptAt != nil {
		nextAttempt = identity.NextAttemptAt.UTC().Format(issueRecordTimestamp)
	}
	if kind == "notification" {
		var notification domain.Notification
		if err := json.Unmarshal(raw, &notification); err != nil {
			return err
		}
		identity.Status = "unread"
		if notification.ReadAt != nil {
			identity.Status = "read"
		}
		if notification.ArchivedAt != nil {
			identity.Status = "archived"
		}
		if notification.DeletedAt != nil {
			identity.Status = "deleted"
		}
		if notification.SnoozedUntil != nil {
			nextAttempt = notification.SnoozedUntil.UTC().Format(issueRecordTimestamp)
		}
	}
	if date, err := time.Parse(time.RFC3339Nano, created); err == nil {
		created = date.UTC().Format(issueRecordTimestamp)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data,owner_id,parent_id,status,next_attempt_at,record_version) VALUES(?,?,?,?,?,?,?,?,?,?,1) ON CONFLICT(workspace_key,kind,resource_id,id) DO UPDATE SET created_at=excluded.created_at,data=excluded.data,owner_id=excluded.owner_id,parent_id=excluded.parent_id,status=excluded.status,next_attempt_at=excluded.next_attempt_at,record_version=excluded.record_version`, workspace, kind, resource, id, created, raw, owner, parent, identity.Status, nextAttempt)
	if err == nil && kind == "activity" {
		var activity domain.ActivityEvent
		if err = json.Unmarshal(raw, &activity); err != nil {
			return err
		}
		if activity.Actor.ID != "" {
			_, err = tx.ExecContext(ctx, `INSERT INTO issue_actor_records(workspace_key,issue_id,user_id,last_at) VALUES(?,?,?,?) ON CONFLICT(workspace_key,issue_id,user_id) DO UPDATE SET last_at=CASE WHEN issue_actor_records.last_at>excluded.last_at THEN issue_actor_records.last_at ELSE excluded.last_at END`, workspace, resource, activity.Actor.ID, activity.CreatedAt.UTC().Format(issueRecordTimestamp))
		}
	}
	return err
}

func syncContentRecords[T any](ctx context.Context, tx *sqlTx, workspace, kind string, items map[string][]T, scopes ...[]string) error {
	query := `SELECT resource_id,id,data FROM workspace_content_records WHERE workspace_key=? AND kind=?`
	args := []any{workspace, kind}
	if len(scopes) > 0 {
		clause, values := bindList("resource_id", scopes[0])
		query += " AND " + clause
		args = append(args, values...)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	type key struct{ resource, id string }
	previous := map[key]string{}
	for rows.Next() {
		var k key
		var raw []byte
		if err := rows.Scan(&k.resource, &k.id, &raw); err != nil {
			rows.Close()
			return err
		}
		previous[k] = string(raw)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for resource, values := range items {
		for _, value := range values {
			id, _, err := contentRecordIdentity(value)
			if err != nil {
				return err
			}
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			k := key{resource, id}
			if previous[k] != string(raw) {
				if err := writeContentRecord(ctx, tx, workspace, kind, resource, value); err != nil {
					return err
				}
			}
			delete(previous, k)
		}
	}
	for k := range previous {
		if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_content_records WHERE workspace_key=? AND kind=? AND resource_id=? AND id=?`, workspace, kind, k.resource, k.id); err != nil {
			return err
		}
	}
	return nil
}

func readContentRecords[T any](ctx context.Context, s *SQLiteStore, workspace, kind string, resource *string) (map[string][]T, error) {
	query := `SELECT resource_id,data FROM workspace_content_records WHERE workspace_key=? AND kind=?`
	args := []any{workspace, kind}
	if resource != nil {
		query += ` AND resource_id=?`
		args = append(args, *resource)
	}
	query += ` ORDER BY created_at,id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := map[string][]T{}
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var item T
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		values[id] = append(values[id], item)
	}
	return values, rows.Err()
}

func (s *SQLiteStore) hydrateContentRecords(ctx context.Context, workspace string, data *domain.Bootstrap) error {
	var err error
	if data.Activities == nil {
		data.Activities, err = readContentRecords[domain.ActivityEvent](ctx, s, workspace, "activity", nil)
		if err != nil {
			return err
		}
	}
	if data.Comments == nil {
		data.Comments, err = readContentRecords[domain.Comment](ctx, s, workspace, "comment", nil)
		if err != nil {
			return err
		}
	}
	if data.Notifications == nil {
		values, err := readContentRecords[domain.Notification](ctx, s, workspace, "notification", nil)
		if err != nil {
			return err
		}
		data.Notifications = []domain.Notification{}
		for _, items := range values {
			data.Notifications = append(data.Notifications, items...)
		}
	}
	if data.NotificationDeliveries == nil {
		values, err := readContentRecords[domain.NotificationDelivery](ctx, s, workspace, "delivery", nil)
		if err != nil {
			return err
		}
		data.NotificationDeliveries = []domain.NotificationDelivery{}
		for _, items := range values {
			data.NotificationDeliveries = append(data.NotificationDeliveries, items...)
		}
	}
	return nil
}

func normalizeStoredMetadata(data *domain.Bootstrap) {
	issues, activities, comments, notifications, deliveries := data.Issues == nil, data.Activities == nil, data.Comments == nil, data.Notifications == nil, data.NotificationDeliveries == nil
	normalize(data)
	if issues {
		data.Issues = nil
	}
	if activities {
		data.Activities = nil
	}
	if comments {
		data.Comments = nil
	}
	if notifications {
		data.Notifications = nil
	}
	if deliveries {
		data.NotificationDeliveries = nil
	}
}

func notificationRecords(values []domain.Notification) map[string][]domain.Notification {
	result := map[string][]domain.Notification{}
	for _, item := range values {
		resource := item.IssueID
		if resource == "" {
			resource = item.SourceID
		}
		result[resource] = append(result[resource], item)
	}
	return result
}

func collectionMetadata(data domain.Bootstrap) domain.Bootstrap {
	data.Issues = nil
	data.Activities = nil
	data.Comments = nil
	data.Notifications = nil
	data.NotificationDeliveries = nil
	return data
}
