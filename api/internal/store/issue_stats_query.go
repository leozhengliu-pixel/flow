package store

import "context"

// IssueCountByTeam reads the maintained issue counters instead of scanning
// issue_records. It is intentionally limited to the dimensions represented by
// issue_scope_counts; callers use QueryIssueRecords for richer predicates.
func (s *SQLiteStore) IssueCountByTeam(ctx context.Context, workspace, teamID string, includeArchived bool) (int64, error) {
	archived := 0
	clause := " AND archived=?"
	args := []any{workspace, teamID, archived}
	if includeArchived {
		clause = ""
		args = []any{workspace, teamID}
	}
	var count int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(total),0) FROM issue_scope_counts WHERE workspace_key=? AND scope_type='team' AND scope_id=?`+clause, args...).Scan(&count)
	return count, err
}
