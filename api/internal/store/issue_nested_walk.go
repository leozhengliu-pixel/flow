package store

import "context"

// nestedIssueGroupsByWalk counts nested groups in one pass over the result:
// it walks the whole result down from every top-level row and counts each
// tree row under its top-level row's group. Its cost follows the result's
// size, so it serves small results (see nestedIssueGroups); tests also use it
// as the reference for the corrected flat counts.
func (s *SQLiteStore) nestedIssueGroupsByWalk(ctx context.Context, query IssueRecordQuery) ([]IssueRecordGroup, error) {
	query.Cursor = ""
	query = query.withoutGroupValue()
	base, baseArgs, err := s.issueNestedBase(query)
	if err != nil {
		return nil, err
	}
	root, rootArgs, err := s.issueNestedRootClause(query)
	if err != nil {
		return nil, err
	}
	carried := ""
	var carriedArgs []any
	if query.GroupBy == "cases" {
		if carried, carriedArgs, err = issueGroupCaseExpression(query.GroupCases); err != nil {
			return nil, err
		}
	} else if query.GroupBy != "none" && query.GroupBy != "customer" && issueGroupAttribute(query.GroupBy) == "" {
		column, err := issueGroupColumn(query.GroupBy)
		if err != nil {
			return nil, err
		}
		carried = "i." + column
	}
	prefix, prefixArgs := issueAccessCTE(query)
	anchor, step := "i.id", "t.root_key,t.depth+1"
	if carried != "" {
		anchor = carried
	}
	cte := "nest_tree(id,root_key,depth) AS (SELECT i.id," + anchor + ",0 FROM issue_records i WHERE " + base + " AND " + root +
		" UNION ALL " + s.issueNestedChildStep(step, base) + ")"
	head := withIssueCTE(prefix, cte)
	args := append([]any{}, prefixArgs...)
	args = append(args, carriedArgs...)
	args = append(args, baseArgs...)
	args = append(args, rootArgs...)
	args = append(args, query.Workspace)
	args = append(args, baseArgs...)
	if query.GroupBy == "cases" {
		rows, err := s.db.QueryContext(ctx, head+"SELECT t.root_key,COUNT(*) FROM nest_tree t GROUP BY t.root_key", args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanIssueCaseGroups(rows, query.GroupCases)
	}
	count := func(statement string, extra ...any) ([]IssueRecordGroup, error) {
		rows, err := s.db.QueryContext(ctx, head+statement, append(append([]any{}, args...), extra...)...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		groups := []IssueRecordGroup{}
		for rows.Next() {
			var group IssueRecordGroup
			if err := rows.Scan(&group.Value, &group.Count); err != nil {
				return nil, err
			}
			if group.Count > 0 {
				groups = append(groups, group)
			}
		}
		return groups, rows.Err()
	}
	if query.GroupBy == "none" {
		return count("SELECT 'all',COUNT(*) FROM nest_tree t")
	}
	if carried != "" {
		return count("SELECT t.root_key,COUNT(*) FROM nest_tree t GROUP BY t.root_key ORDER BY t.root_key")
	}
	from := "nest_tree t JOIN issue_records i ON i.workspace_key=? AND i.id=t.root_key"
	args = append(args, query.Workspace)
	if query.GroupBy == "customer" {
		key := "COALESCE(c.customer_id,'" + UnknownCustomerGroup + "')"
		groups, err := count("SELECT " + key + ",COUNT(DISTINCT t.id) FROM " + from + " JOIN customer_request_filter_records r ON r.workspace_key=i.workspace_key AND r.issue_id=i.id LEFT JOIN customer_filter_records c ON c.workspace_key=r.workspace_key AND c.customer_id=r.customer_id GROUP BY " + key + " ORDER BY " + key)
		if err != nil {
			return nil, err
		}
		none, noneArgs := customerGroupClause("")
		empty, err := count("SELECT '',COUNT(*) FROM "+from+" WHERE "+none, noneArgs...)
		if err != nil {
			return nil, err
		}
		return append(groups, empty...), nil
	}
	from += " LEFT JOIN issue_attribute_records g ON g.workspace_key=i.workspace_key AND g.issue_id=i.id AND g.field='" + issueGroupAttribute(query.GroupBy) + "'"
	return count("SELECT COALESCE(g.value,''),COUNT(*) FROM " + from + " GROUP BY COALESCE(g.value,'') ORDER BY COALESCE(g.value,'')")
}
