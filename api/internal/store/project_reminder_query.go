package store

import (
	"context"
	"encoding/json"

	"flow/api/internal/domain"
)

type ProjectUpdateReminderFlags struct {
	Missing bool
	Due     bool
}

// ProjectReminderFlags reads a batch of scheduled projects at once. Older
// project notifications used an empty resource ID, so also check that bucket.
func (s *SQLiteStore) ProjectReminderFlags(ctx context.Context, workspace string, projectIDs []string) (map[string]ProjectUpdateReminderFlags, error) {
	flags := make(map[string]ProjectUpdateReminderFlags, len(projectIDs))
	if len(projectIDs) == 0 {
		return flags, nil
	}
	for _, id := range projectIDs {
		flags[id] = ProjectUpdateReminderFlags{}
	}
	resources := make([]string, 0, len(projectIDs)+1)
	resources = append(resources, projectIDs...)
	resources = append(resources, "")
	clause, ids := bindList("resource_id", resources)
	rows, err := s.db.QueryContext(ctx, `SELECT resource_id,data FROM workspace_content_records WHERE workspace_key=? AND kind='notification' AND `+clause+` AND status IN ('read','unread')`, append([]any{workspace}, ids...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var resource string
		var raw []byte
		var item domain.Notification
		if err := rows.Scan(&resource, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		current, selected := flags[item.ProjectID]
		if !selected || (resource != "" && resource != item.ProjectID) || item.ArchivedAt != nil || item.DeletedAt != nil {
			continue
		}
		current.Missing = current.Missing || item.Type == "projectUpdateReminder"
		current.Due = current.Due || item.Type == "projectUpdateDueReminder"
		flags[item.ProjectID] = current
	}
	return flags, rows.Err()
}
