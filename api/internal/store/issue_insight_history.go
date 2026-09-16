package store

import (
	"context"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type IssueStatusInterval struct {
	StateID   string     `json:"stateId"`
	StateType string     `json:"stateType,omitempty"`
	EnteredAt time.Time  `json:"enteredAt"`
	ExitedAt  *time.Time `json:"exitedAt,omitempty"`
}

// Call only with a bounded, authorized query page. Read transition metadata in
// one query, without loading comments, actors, descriptions or workspace state.
func (s *SQLiteStore) IssueInsightIntervals(ctx context.Context, workspace string, issues []domain.Issue, states []domain.WorkflowState) (map[string][]IssueStatusInterval, error) {
	result := make(map[string][]IssueStatusInterval, len(issues))
	if len(issues) == 0 {
		return result, nil
	}
	byID := make(map[string]domain.Issue, len(issues))
	stateByID := make(map[string]domain.WorkflowState, len(states))
	for _, state := range states {
		stateByID[state.ID] = state
	}
	resolve := func(id, name, team string) string {
		if id != "" {
			return id
		}
		for _, state := range states {
			if state.Name == name && (state.TeamID == "" || state.TeamID == team) {
				return state.ID
			}
		}
		return ""
	}
	args := []any{workspace}
	marks := make([]string, len(issues))
	for i, issue := range issues {
		byID[issue.ID] = issue
		marks[i] = "?"
		args = append(args, issue.ID)
	}
	fields := []string{"metadata.stateId", "metadata.state", "metadata.stateBeforeId", "metadata.stateBefore"}
	columns := []string{"resource_id", "created_at"}
	for _, field := range fields {
		columns = append(columns, "COALESCE("+s.jsonText("data", field)+",'')")
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+strings.Join(columns, ",")+" FROM workspace_content_records WHERE workspace_key=? AND kind='activity' AND resource_id IN ("+strings.Join(marks, ",")+") AND ("+s.jsonText("data", "metadata.stateId")+" IS NOT NULL OR "+s.jsonText("data", "metadata.state")+" IS NOT NULL) ORDER BY resource_id,created_at,id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var issueID, date, id, name, beforeID, beforeName string
		if err := rows.Scan(&issueID, &date, &id, &name, &beforeID, &beforeName); err != nil {
			return nil, err
		}
		at, err := time.Parse(time.RFC3339Nano, date)
		if err != nil {
			return nil, err
		}
		issue := byID[issueID]
		id = resolve(id, name, issue.Team.ID)
		if id == "" {
			continue
		}
		intervals := result[issueID]
		if len(intervals) == 0 {
			previous := resolve(beforeID, beforeName, issue.Team.ID)
			if previous != "" {
				intervals = append(intervals, IssueStatusInterval{StateID: previous, StateType: stateByID[previous].Type, EnteredAt: issue.CreatedAt, ExitedAt: &at})
			}
		} else {
			if intervals[len(intervals)-1].StateID == id {
				continue
			}
			intervals[len(intervals)-1].ExitedAt = &at
		}
		result[issueID] = append(intervals, IssueStatusInterval{StateID: id, StateType: stateByID[id].Type, EnteredAt: at})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, issue := range issues {
		if len(result[issue.ID]) == 0 {
			at := issue.CreatedAt
			if issue.StatusChangedAt != nil {
				at = *issue.StatusChangedAt
			}
			result[issue.ID] = []IssueStatusInterval{{StateID: issue.State.ID, StateType: issue.State.Type, EnteredAt: at}}
		}
	}
	return result, nil
}
