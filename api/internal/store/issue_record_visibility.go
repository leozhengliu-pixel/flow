package store

import "context"

// Related IDs are checked independently of the current view's filters: an
// issue outside the view can still be readable, but a private reference cannot.
func (s *SQLiteStore) VisibleIssueRecordIDs(ctx context.Context, q IssueRecordQuery, ids []string) (map[string]bool, error) {
	result := map[string]bool{}
	q.Text = ""
	q.Archived = "all"
	q.TeamIDs = nil
	q.StateIDs = nil
	q.ProjectIDs = nil
	q.GroupValue = nil
	q.GroupBy = ""
	for start := 0; start < len(ids); start += 500 {
		q.Filter = IssueFilter{Field: "id", Values: ids[start:min(start+500, len(ids))]}
		where, args, err := issueRecordWhere(q)
		if err != nil {
			return nil, err
		}
		prefix, prefixArgs := issueAccessCTE(q)
		rows, err := s.db.QueryContext(ctx, prefix+"SELECT i.id FROM issue_records i WHERE "+where, append(prefixArgs, args...)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			result[id] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return result, nil
}
