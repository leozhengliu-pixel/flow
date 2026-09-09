package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"flow/api/internal/domain"
)

func (s *SQLiteStore) NotificationDeliverySnapshot(ctx context.Context, workspace string, statuses []string, now time.Time) (domain.Bootstrap, error) {
	data, ok := s.WorkspaceMetadata(workspace)
	if !ok {
		return data, fmt.Errorf("workspace not found")
	}
	clause, args := bindList("status", statuses)
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM workspace_content_records WHERE workspace_key=? AND kind='delivery' AND `+clause+` AND (next_attempt_at='' OR next_attempt_at<=?) ORDER BY next_attempt_at,id LIMIT 1000`, append(append([]any{workspace}, args...), now.UTC().Format(issueRecordTimestamp))...)
	if err != nil {
		return data, err
	}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return data, err
		}
		var delivery domain.NotificationDelivery
		if err := json.Unmarshal(raw, &delivery); err != nil {
			rows.Close()
			return data, err
		}
		data.NotificationDeliveries = append(data.NotificationDeliveries, delivery)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return data, err
	}
	rows.Close()
	seen := map[string]bool{}
	for _, delivery := range data.NotificationDeliveries {
		var raw []byte
		if err := s.db.QueryRowContext(ctx, `SELECT data FROM workspace_content_records WHERE workspace_key=? AND kind='notification' AND id=?`, workspace, delivery.NotificationID).Scan(&raw); err != nil {
			continue
		}
		var notification domain.Notification
		if err := json.Unmarshal(raw, &notification); err != nil {
			return data, err
		}
		data.Notifications = append(data.Notifications, notification)
		if notification.IssueID != "" && !seen[notification.IssueID] {
			seen[notification.IssueID] = true
			if issue, err := s.IssueRecord(ctx, workspace, notification.IssueID); err == nil {
				data.Issues = append(data.Issues, issue)
			}
		}
	}
	return data, nil
}

func (s *SQLiteStore) MutateNotificationDelivery(ctx context.Context, workspace, id string, mutate func(*domain.NotificationDelivery) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	lock := ""
	if s.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_content_records WHERE workspace_key=? AND kind='delivery' AND resource_id='' AND id=?`+lock, workspace, id).Scan(&raw); err != nil {
		return err
	}
	var delivery domain.NotificationDelivery
	if err := json.Unmarshal(raw, &delivery); err != nil {
		return err
	}
	if err := mutate(&delivery); err != nil {
		return err
	}
	if err := writeContentRecord(ctx, tx, workspace, "delivery", "", delivery); err != nil {
		return err
	}
	return tx.Commit()
}

func issueNotificationDeliveries(ctx context.Context, tx *sqlTx, workspace string, notifications []domain.Notification) ([]domain.NotificationDelivery, error) {
	ids := []string{}
	for _, notification := range notifications {
		ids = append(ids, notification.ID)
	}
	result := []domain.NotificationDelivery{}
	for start := 0; start < len(ids); start += 500 {
		where, args := bindList("parent_id", ids[start:min(start+500, len(ids))])
		rows, err := tx.QueryContext(ctx, `SELECT data FROM workspace_content_records WHERE workspace_key=? AND kind='delivery' AND `+where, append([]any{workspace}, args...)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return nil, err
			}
			var delivery domain.NotificationDelivery
			if err := json.Unmarshal(raw, &delivery); err != nil {
				rows.Close()
				return nil, err
			}
			result = append(result, delivery)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return result, nil
}
