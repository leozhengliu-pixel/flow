package store

import (
	"context"
	"strings"

	"flow/api/internal/domain"
)

// The payload is decoded only after its access predicate has matched. Both
// identifiers and internal IDs use the same authorized point-read contract.
func (s *SQLiteStore) AuthorizedIssueRecord(ctx context.Context, query IssueRecordQuery, id string) (domain.Issue, error) {
	if raw, ok := s.cacheGetIssue(ctx, query.Workspace, id); ok {
		// The access check reads the row's version too: a cached copy is only
		// served while it is the stored version (a read racing an update can
		// put the old record back after the update dropped it).
		if authorizedID, version, err := s.authorizedIssueRecordVersion(ctx, query, id); err == nil && authorizedID != "" {
			issue, err := unmarshalIssueRecord(raw)
			if err == nil && issue.ID == authorizedID && issue.Version == version {
				issues := []domain.Issue{issue}
				if err := s.resolveIssueReferences(ctx, query.Workspace, issues); err != nil {
					return domain.Issue{}, err
				}
				return issues[0], nil
			}
		}
	}
	statement, args, err := authorizedIssueStatement(query, id, "i.data")
	if err != nil {
		return domain.Issue{}, err
	}
	var raw []byte
	if err := s.db.QueryRowContext(ctx, statement, args...).Scan(&raw); err != nil {
		return domain.Issue{}, err
	}
	issue, err := unmarshalIssueRecord(raw)
	if err != nil {
		return issue, err
	}
	s.cacheSetIssue(ctx, query.Workspace, issue.ID, raw)
	issues := []domain.Issue{issue}
	if err := s.resolveIssueReferences(ctx, query.Workspace, issues); err != nil {
		return domain.Issue{}, err
	}
	return issues[0], nil
}

func (s *SQLiteStore) AuthorizedIssueRecordID(ctx context.Context, query IssueRecordQuery, id string) (string, error) {
	statement, args, err := authorizedIssueStatement(query, id, "i.id")
	if err != nil {
		return "", err
	}
	var result string
	err = s.db.QueryRowContext(ctx, statement, args...).Scan(&result)
	return result, err
}

func (s *SQLiteStore) authorizedIssueRecordVersion(ctx context.Context, query IssueRecordQuery, id string) (string, int64, error) {
	statement, args, err := authorizedIssueStatement(query, id, "i.id,i.version")
	if err != nil {
		return "", 0, err
	}
	var result string
	var version int64
	err = s.db.QueryRowContext(ctx, statement, args...).Scan(&result, &version)
	return result, version, err
}

func authorizedIssueStatement(query IssueRecordQuery, id, column string) (string, []any, error) {
	query = IssueRecordQuery{Workspace: query.Workspace, Access: query.Access, AllowedTeamIDs: query.AllowedTeamIDs, Archived: "all"}
	where, args, err := issueRecordWhere(query)
	if err != nil {
		return "", nil, err
	}
	prefix, prefixArgs := issueAccessCTE(query)
	args = append(prefixArgs, args...)
	args = append(args, query.Workspace, id, query.Workspace, strings.ToUpper(id), query.Workspace, strings.ToUpper(id))
	// An issue that moved to another team keeps answering to its old identifiers.
	return prefix + "SELECT " + column + " FROM issue_records i WHERE " + where + " AND i.id IN (SELECT id FROM issue_records WHERE workspace_key=? AND id=? UNION SELECT id FROM issue_records WHERE workspace_key=? AND identifier=? UNION SELECT issue_id FROM issue_identifier_aliases WHERE workspace_key=? AND identifier=?) LIMIT 1", args, nil
}
