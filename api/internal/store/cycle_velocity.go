package store

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
)

// CycleCompletedIssue is an issue of a cycle that is in a completed state,
// with the estimate it has (nil when unestimated).
type CycleCompletedIssue struct {
	CycleID  string
	Estimate *float64
}

// CycleCompletedIssues reads the completed, unarchived issues of the given
// cycles, for the teams' velocity. Capacity is a team figure, so the read is
// not narrowed to the issues the viewer can open; only the cycle, the estimate
// and nothing else of an issue leaves the store.
func (s *SQLiteStore) CycleCompletedIssues(ctx context.Context, workspace string, cycleIDs []string) ([]CycleCompletedIssue, error) {
	result := []CycleCompletedIssue{}
	for chunk := range slices.Chunk(cycleIDs, 500) {
		args := []any{workspace}
		marks := make([]string, len(chunk))
		for index, id := range chunk {
			marks[index] = "?"
			args = append(args, id)
		}
		rows, err := s.db.QueryContext(ctx, "SELECT cycle_id,data FROM issue_records WHERE workspace_key=? AND archived=0 AND state_type='completed' AND cycle_id IN ("+strings.Join(marks, ",")+")", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var cycleID string
			var raw []byte
			if err := rows.Scan(&cycleID, &raw); err != nil {
				rows.Close()
				return nil, err
			}
			var issue struct {
				Estimate *float64 `json:"estimate"`
			}
			if err := json.Unmarshal(raw, &issue); err != nil {
				rows.Close()
				return nil, err
			}
			result = append(result, CycleCompletedIssue{CycleID: cycleID, Estimate: issue.Estimate})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}
