package store

import (
	"context"
	"encoding/json"

	"flow/api/internal/domain"
)

// Compatibility predicates that cannot yet use an index are evaluated a row
// at a time. The caller retains only its bounded result, never the workspace.
func (s *SQLiteStore) WalkIssueRecords(ctx context.Context, q IssueRecordQuery, visit func(domain.Issue) error) error {
	where, args, err := issueRecordWhere(q)
	if err != nil {
		return err
	}
	prefix, prefixArgs := issueAccessCTE(q)
	reader, metadata, ok := s.workspaceReadSource(ctx, q.Workspace)
	if !ok {
		return ErrAuthForbidden
	}
	refs := newIssueReferences(metadata)
	rows, err := reader.QueryContext(ctx, prefix+"SELECT i.data FROM issue_records i WHERE "+where+" ORDER BY i.collection_order,i.id", append(prefixArgs, args...)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var raw []byte
		var issue domain.Issue
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &issue); err != nil {
			return err
		}
		normalizeIssueRecord(&issue)
		if err := visit(refs.resolve(issue)); err != nil {
			return err
		}
	}
	return rows.Err()
}
