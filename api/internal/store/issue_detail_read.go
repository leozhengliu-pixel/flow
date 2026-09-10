package store

import (
	"context"
	"encoding/json"
	"strings"

	"flow/api/internal/domain"
)

// The payload is decoded only after its access predicate has matched. Both
// identifiers and internal IDs use the same authorized point-read contract.
func (s *SQLiteStore) AuthorizedIssueRecord(ctx context.Context, query IssueRecordQuery, id string) (domain.Issue, error) {
	statement, args, err := authorizedIssueStatement(query, id, "i.data")
	if err != nil {
		return domain.Issue{}, err
	}
	var raw []byte
	if err := s.db.QueryRowContext(ctx, statement, args...).Scan(&raw); err != nil {
		return domain.Issue{}, err
	}
	var issue domain.Issue
	if err := json.Unmarshal(raw, &issue); err != nil {
		return issue, err
	}
	normalizeIssueRecord(&issue)
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

func authorizedIssueStatement(query IssueRecordQuery, id, column string) (string, []any, error) {
	query = IssueRecordQuery{Workspace: query.Workspace, Access: query.Access, AllowedTeamIDs: query.AllowedTeamIDs, Archived: "all"}
	where, args, err := issueRecordWhere(query)
	if err != nil {
		return "", nil, err
	}
	prefix, prefixArgs := issueAccessCTE(query)
	args = append(prefixArgs, args...)
	args = append(args, query.Workspace, id, query.Workspace, strings.ToUpper(id))
	return prefix + "SELECT " + column + " FROM issue_records i WHERE " + where + " AND i.id IN (SELECT id FROM issue_records WHERE workspace_key=? AND id=? UNION SELECT id FROM issue_records WHERE workspace_key=? AND identifier=?) LIMIT 1", args, nil
}
