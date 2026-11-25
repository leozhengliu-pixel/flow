package store

import (
	"context"
	"errors"
)

// VisibleLabelIssueCounts is the issue term of refreshResourceCounts over a
// viewer projection, read from the issue indexes instead of hydrated issues:
// per label ID, the unarchived issues access reads (nil access: every issue,
// as the unprojected workspace counts them). Each issue counts a label once
// (issue_label_records holds an issue's distinct label IDs) and an empty ID
// never counts. teamIDs, when not nil, further narrows the issues to those
// teams (an API key's selected teams; empty selects none).
func (s *SQLiteStore) VisibleLabelIssueCounts(ctx context.Context, workspace string, access *IssueRecordAccess, teamIDs []string) (map[string]int, error) {
	counts := map[string]int{}
	if teamIDs != nil && len(teamIDs) == 0 {
		return counts, ctx.Err()
	}
	query := IssueRecordQuery{Workspace: workspace, Access: access, TeamIDs: teamIDs}
	where, args, err := issueRecordWhere(query)
	if err != nil {
		return nil, err
	}
	prefix, prefixArgs := issueAccessCTE(query)
	// Label IDs are grouped exactly, not by a case-insensitive collation.
	label := "l.label_id"
	if s.dialect == "mysql" {
		label = "BINARY l.label_id"
	}
	rows, err := s.db.QueryContext(ctx, prefix+`SELECT `+label+`,COUNT(*) FROM issue_records i JOIN issue_label_records l ON l.workspace_key=i.workspace_key AND l.issue_id=i.id WHERE `+where+` GROUP BY `+label, append(prefixArgs, args...)...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		if id != "" {
			counts[id] += count
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return counts, ctx.Err()
}
