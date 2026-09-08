package store

import "context"

type IssueRecordTotals struct {
	Total     int64 `json:"total"`
	Started   int64 `json:"started"`
	Completed int64 `json:"completed"`
}

type IssueRecordSummary struct {
	IssueRecordTotals
	Milestones map[string]IssueRecordTotals `json:"milestones"`
	Assignees  map[string]IssueRecordTotals `json:"assignees"`
	Labels     map[string]IssueRecordTotals `json:"labels"`
}

func (s *SQLiteStore) QueryIssueRecordSummary(ctx context.Context, query IssueRecordQuery) (IssueRecordSummary, error) {
	result := IssueRecordSummary{Milestones: map[string]IssueRecordTotals{}, Assignees: map[string]IssueRecordTotals{}, Labels: map[string]IssueRecordTotals{}}
	query.Cursor, query.GroupBy, query.GroupValue = "", "none", nil
	where, args, err := issueRecordWhere(query)
	if err != nil {
		return result, err
	}
	prefix, prefixArgs := issueAccessCTE(query)
	for _, dimension := range []struct {
		name, column, join string
		values             map[string]IssueRecordTotals
	}{
		{"assignees", "i.assignee_id", "", result.Assignees},
		{"milestones", "COALESCE(a.value,'')", " LEFT JOIN issue_attribute_records a ON a.workspace_key=i.workspace_key AND a.issue_id=i.id AND a.field='projectMilestoneId'", result.Milestones},
		{"labels", "l.label_id", " JOIN issue_label_records l ON l.workspace_key=i.workspace_key AND l.issue_id=i.id", result.Labels},
	} {
		rows, err := s.db.QueryContext(ctx, prefix+"SELECT "+dimension.column+",i.state_type,COUNT(*) FROM issue_records i"+dimension.join+" WHERE "+where+" GROUP BY "+dimension.column+",i.state_type", append(prefixArgs, args...)...)
		if err != nil {
			return result, err
		}
		for rows.Next() {
			var id, state string
			var count int64
			if err := rows.Scan(&id, &state, &count); err != nil {
				rows.Close()
				return result, err
			}
			value := dimension.values[id]
			value.Total += count
			if state == "started" {
				value.Started += count
			}
			if state == "completed" {
				value.Completed += count
			}
			dimension.values[id] = value
			if dimension.name == "assignees" {
				result.Total += count
				if state == "started" {
					result.Started += count
				}
				if state == "completed" {
					result.Completed += count
				}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return result, err
		}
	}
	return result, nil
}
