package store

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// CycleHistoryIssueIDs lists issues whose recorded activity ever moved them
// into or out of the cycle. Issues removed mid-cycle are no longer found by a
// cycle_id filter, so the scope history has to start from the activity log.
// Callers must still load the returned IDs through an authorized issue query.
func (s *SQLiteStore) CycleHistoryIssueIDs(ctx context.Context, workspace, cycleID string) ([]string, error) {
	cycle, before, initial := s.jsonText("data", "metadata.cycle"), s.jsonText("data", "metadata.cycleBefore"), s.jsonText("data", "metadata.initialCycle")
	rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT resource_id FROM workspace_content_records WHERE workspace_key=? AND kind='activity' AND ("+cycle+"=? OR "+before+"=? OR "+initial+"=?)", workspace, cycleID, cycleID, cycleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// IssueHistoryEvents reads the issue.created / issue.updated activity of a
// bounded, already-authorized issue set, oldest first per issue.
func (s *SQLiteStore) IssueHistoryEvents(ctx context.Context, workspace string, issueIDs []string) (map[string][]domain.ActivityEvent, error) {
	result := make(map[string][]domain.ActivityEvent, len(issueIDs))
	eventType := s.jsonText("data", "type")
	for chunk := range slices.Chunk(issueIDs, 500) {
		args := []any{workspace}
		marks := make([]string, len(chunk))
		for i, id := range chunk {
			marks[i] = "?"
			args = append(args, id)
		}
		rows, err := s.db.QueryContext(ctx, "SELECT resource_id,data FROM workspace_content_records WHERE workspace_key=? AND kind='activity' AND resource_id IN ("+strings.Join(marks, ",")+") AND "+eventType+" IN ('issue.created','issue.updated') ORDER BY resource_id,created_at,id", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var raw []byte
			if err := rows.Scan(&id, &raw); err != nil {
				rows.Close()
				return nil, err
			}
			var event domain.ActivityEvent
			if err := json.Unmarshal(raw, &event); err != nil {
				rows.Close()
				return nil, err
			}
			result[id] = append(result[id], event)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}
