package store

import (
	"context"
	"encoding/json"
	"reflect"

	"flow/api/internal/domain"
)

// RetainIssueScanResult holds the budget for a row kept after the walk's
// callback returns. Release it when the caller discards or writes the row.
func (s *SQLiteStore) RetainIssueScanResult(ctx context.Context, issue domain.Issue) (func(), error) {
	if !s.BoundedMetadataEnabled() {
		return func() {}, nil
	}
	cost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(issue), 0)
	if err != nil {
		return nil, err
	}
	_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, int(cost))
	return release, err
}

// Compatibility predicates that cannot yet use an index are evaluated a row
// at a time. The caller retains only its bounded result, never the workspace.
func (s *SQLiteStore) WalkIssueRecords(ctx context.Context, q IssueRecordQuery, visit func(domain.Issue) error) error {
	if s.BoundedMetadataEnabled() {
		return s.walkBoundedBootstrapIssueRows(ctx, q, false, true, visit)
	}
	reader, metadata, ok := s.workspaceReadSource(ctx, q.Workspace)
	if !ok {
		return ErrAuthForbidden
	}
	refs := newIssueReferences(metadata)
	return walkIssueRecords(ctx, reader, q, func(issue domain.Issue) error {
		return visit(refs.resolve(issue))
	})
}

// WalkBootstrapIssueBodies streams records within the outline's transaction.
// The encoder applies the already-projected references from that outline.
func (s *SQLiteStore) WalkBootstrapIssueBodies(ctx context.Context, q IssueRecordQuery, visit func(domain.Issue) error) error {
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == s.db && snapshot.workspace == q.Workspace {
		return walkIssueRecords(ctx, snapshot.reader, q, visit)
	}
	return ErrAuthForbidden
}

func walkIssueRecords(ctx context.Context, reader metadataReader, q IssueRecordQuery, visit func(domain.Issue) error) error {
	where, args, err := issueRecordWhere(q)
	if err != nil {
		return err
	}
	prefix, prefixArgs := issueAccessCTE(q)
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
		if err := visit(issue); err != nil {
			return err
		}
	}
	return rows.Err()
}
