package store

import (
	"context"
	"encoding/json"

	"flow/api/internal/domain"
)

// Only retain the two reminder flags. Older project notifications used an empty
// resource ID; include that bucket without loading issue notifications.
func (s *SQLiteStore) ProjectReminderFlags(ctx context.Context, workspace, projectID string) (missing, due bool, err error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM workspace_content_records WHERE workspace_key=? AND kind='notification' AND resource_id IN (?, '') AND status IN ('read','unread')`, workspace, projectID)
	if err != nil {
		return false, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var item domain.Notification
		if err := rows.Scan(&raw); err != nil {
			return false, false, err
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return false, false, err
		}
		if item.ProjectID != projectID || item.ArchivedAt != nil || item.DeletedAt != nil {
			continue
		}
		missing = missing || item.Type == "projectUpdateReminder"
		due = due || item.Type == "projectUpdateDueReminder"
		if missing && due {
			return missing, due, nil
		}
	}
	return missing, due, rows.Err()
}
