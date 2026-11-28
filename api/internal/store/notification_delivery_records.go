package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"flow/api/internal/domain"
)

// NotificationDeliverySelection filters due deliveries before the batch limit.
// An empty Channel selects across all channels.
type NotificationDeliverySelection struct {
	Statuses                []string
	Channel                 string
	ExcludeTerminalFailures bool
}

func (s *SQLiteStore) dueNotificationDeliveryQuery(workspace string, selection NotificationDeliverySelection, now time.Time) (string, []any) {
	status, statuses := bindList("status", selection.Statuses)
	// An unset next-attempt is stored as ''. It sorts before UTC timestamps,
	// so both kinds of due row fit one range on content_status_idx.
	query := ` FROM workspace_content_records WHERE workspace_key=? AND kind='delivery' AND ` + status + ` AND next_attempt_at<=?`
	args := append(append([]any{workspace}, statuses...), now.UTC().Format(issueRecordTimestamp))
	if selection.Channel != "" {
		channel := s.jsonText("data", "channel")
		if s.dialect == "mysql" {
			// Match Go's exact channel comparison on older MySQL collations too.
			channel = "CAST(" + channel + " AS BINARY)"
		}
		query += ` AND ` + channel + `=?`
		args = append(args, selection.Channel)
	}
	if selection.ExcludeTerminalFailures {
		query += ` AND (status<>'failed' OR next_attempt_at<>'')`
	}
	return query + ` ORDER BY next_attempt_at,id LIMIT 1000`, args
}

// NotificationDeliveries returns the selected due deliveries without their
// rendering metadata. Unfiltered reads retain the legacy workspace settings.
func (s *SQLiteStore) NotificationDeliveries(ctx context.Context, workspace string, selection NotificationDeliverySelection, now time.Time) (domain.Bootstrap, error) {
	return s.notificationDeliverySnapshot(ctx, workspace, selection, now, false)
}

func (s *SQLiteStore) NotificationDeliverySnapshot(ctx context.Context, workspace string, selection NotificationDeliverySelection, now time.Time) (domain.Bootstrap, error) {
	return s.notificationDeliverySnapshot(ctx, workspace, selection, now, true)
}

func (s *SQLiteStore) notificationDeliverySnapshot(ctx context.Context, workspace string, selection NotificationDeliverySelection, now time.Time, full bool) (domain.Bootstrap, error) {
	if s.BoundedMetadataEnabled() {
		return domain.Bootstrap{}, ErrBoundedMetadataCoverage
	}
	var data domain.Bootstrap
	var ok bool
	if selection.Channel == "" {
		data, ok = s.WorkspaceSettingsMetadata(workspace)
	} else {
		// Scheduler passes need only the installed workspace identity to query.
		s.mu.RLock()
		if workspace == "" {
			workspace = s.lastWorkspaceKey
		}
		var source domain.Bootstrap
		source, ok = s.workspaces[workspace]
		data.Workspace = domain.Workspace{ID: source.Workspace.ID, URLKey: source.Workspace.URLKey}
		s.mu.RUnlock()
	}
	if !ok {
		return data, fmt.Errorf("workspace not found")
	}
	workspace = data.Workspace.URLKey
	query, args := s.dueNotificationDeliveryQuery(workspace, selection, now)
	rows, err := s.db.QueryContext(ctx, `SELECT data`+query, args...)
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
	if len(data.NotificationDeliveries) == 0 || !full {
		return data, nil
	}
	// Only the matching due rows need email-rendering metadata.
	deliveries := data.NotificationDeliveries
	if data, ok = s.WorkspaceMetadata(workspace); !ok {
		return data, fmt.Errorf("workspace not found")
	}
	data.NotificationDeliveries = deliveries
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
	var owner *ResourceRead
	if s.BoundedMetadataEnabled() {
		work, release, err := s.tryAdmitAdditionalMetadataWork(ctx, 2<<20)
		if err != nil {
			return err
		}
		ctx = work
		owner = &ResourceRead{store: s, release: []func(){release}}
		defer owner.Close()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	lock := ""
	if s.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	if owner != nil {
		var size int64
		if err := tx.QueryRowContext(ctx, `SELECT `+s.visibilityLength("data")+` FROM workspace_content_records WHERE workspace_key=? AND kind='delivery' AND resource_id='' AND id=?`+lock, workspace, id).Scan(&size); err != nil {
			return err
		}
		if size < 0 || size > (int64(metadataWorkBytes())-4096)/32 {
			return ErrMetadataWorkBudget
		}
		if err := owner.reserve(ctx, uint64(size)*32+4096); err != nil {
			return err
		}
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_content_records WHERE workspace_key=? AND kind='delivery' AND resource_id='' AND id=?`+lock, workspace, id).Scan(&raw); err != nil {
		return err
	}
	if owner != nil {
		work, err := metadataAggregatePayloadJSONWork(ctx, raw)
		if err != nil {
			return err
		}
		if err := owner.reserve(ctx, work); err != nil {
			return err
		}
	}
	var delivery domain.NotificationDelivery
	if err := json.Unmarshal(raw, &delivery); err != nil {
		return err
	}
	if err := mutate(&delivery); err != nil {
		return err
	}
	if owner != nil {
		work, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(delivery), 0)
		if err != nil {
			return err
		}
		if err := owner.reserve(ctx, uint64(work)); err != nil {
			return err
		}
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
