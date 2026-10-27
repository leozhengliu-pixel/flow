package store

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"flow/api/internal/domain"
)

// Nested issue queries ("Nested sub-issues" in issue lists): a sub-issue whose
// parent is in the result is shown under that parent, wherever its own group
// is, instead of as a row of its own. Pages hold the top-level rows only and
// carry each row's matching descendants; group counts include the descendants
// under the group of their top-level row.

// MaxNestedDescendants bounds the descendants returned with one page.
const MaxNestedDescendants = 1000

// maxNestedDepth bounds sub-issue levels (and guards against a parent cycle).
const maxNestedDepth = 32

// issueNestedBase is the condition (on alias i) a parent must meet for its
// sub-issues to nest under it: the whole query, whatever group is paged.
func (s *SQLiteStore) issueNestedBase(query IssueRecordQuery) (string, []any, error) {
	query = query.withoutGroupValue()
	where, args, err := issueRecordWhere(query)
	if err != nil {
		return "", nil, err
	}
	if query.SearchText != "" {
		selection, values := s.issueTextSelection(query.Workspace, query.SearchText)
		where += " AND i.id IN (" + selection + ")"
		args = append(args, values...)
	}
	return where, args, nil
}

// issueNestedRootClause keeps the rows (alias i) whose parent is not in the
// result. The parent is checked by primary key: the inner query re-aliases it
// as i, so the query's own condition applies to it unchanged, and nest_parent
// carries the outer row's parent id into that scope.
func (s *SQLiteStore) issueNestedRootClause(query IssueRecordQuery) (string, []any, error) {
	base, args, err := s.issueNestedBase(query)
	if err != nil {
		return "", nil, err
	}
	return "(i.parent_id='' OR NOT EXISTS (SELECT 1 FROM issue_records nest_parent WHERE nest_parent.workspace_key=i.workspace_key AND nest_parent.id=i.parent_id AND EXISTS (SELECT 1 FROM issue_records i WHERE i.workspace_key=nest_parent.workspace_key AND i.id=nest_parent.id AND " + base + ")))", args, nil
}

// withIssueCTE adds a common table expression to the access prefix (which may
// be empty) of a query.
func withIssueCTE(prefix, cte string) string {
	if prefix == "" {
		return "WITH RECURSIVE " + cte + " "
	}
	return strings.TrimRight(prefix, " \n\t") + ", " + cte + " "
}

// issueNestedChildStep is the recursive member of a nest_tree query: the
// children of one tree row that match base (one workspace argument, then
// base's). The children are found through the parent index (c carries only
// the workspace and parent terms) and checked by primary key as i, so base's
// own terms (archived, state, ...) never pick the index. The parent index is
// named outright where the dialect allows it: without statistics (a fresh
// import) SQLite and MySQL otherwise scan the workspace's issues once per
// tree row. CROSS JOIN keeps SQLite's join order.
func (s *SQLiteStore) issueNestedChildStep(columns, base string) string {
	children := "issue_records c"
	switch s.dialect {
	case "sqlite":
		children += " INDEXED BY issue_records_parent_idx"
	case "mysql":
		children += " FORCE INDEX (issue_records_parent_idx)"
	}
	return "SELECT c.id," + columns + " FROM nest_tree t CROSS JOIN " + children + " CROSS JOIN issue_records i WHERE c.workspace_key=? AND c.parent_id=t.id AND i.workspace_key=c.workspace_key AND i.id=c.id AND t.depth<" + strconv.Itoa(maxNestedDepth) + " AND " + base
}

// issueNestedDescendants reads the descendants of roots that match the query,
// level by level through the parent index, in the page's order.
func (s *SQLiteStore) issueNestedDescendants(ctx context.Context, query IssueRecordQuery, roots []domain.Issue, order, payload, descriptionColumn string) ([]domain.Issue, bool, error) {
	base, baseArgs, err := s.issueNestedBase(query)
	if err != nil {
		return nil, false, err
	}
	ids := make([]string, len(roots))
	for i, issue := range roots {
		ids[i] = issue.ID
	}
	rootIDs, rootArgs := bindList("i.id", ids)
	prefix, prefixArgs := issueAccessCTE(query)
	cte := "nest_tree(id,depth) AS (SELECT i.id,0 FROM issue_records i WHERE i.workspace_key=? AND " + rootIDs +
		" UNION ALL " + s.issueNestedChildStep("t.depth+1", base) + ")"
	args := append([]any{}, prefixArgs...)
	args = append(args, query.Workspace)
	args = append(args, rootArgs...)
	args = append(args, query.Workspace)
	args = append(args, baseArgs...)
	args = append(args, query.Workspace, MaxNestedDescendants+1)
	rows, err := s.db.QueryContext(ctx, withIssueCTE(prefix, cte)+"SELECT "+payload+","+descriptionColumn+" FROM nest_tree t JOIN issue_records i ON i.workspace_key=? AND i.id=t.id WHERE t.depth>0 ORDER BY "+order+" LIMIT ?", args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	items := []domain.Issue{}
	seen := map[string]bool{}
	truncated := false
	for rows.Next() {
		var raw []byte
		var description string
		if err := rows.Scan(&raw, &description); err != nil {
			return nil, false, err
		}
		if len(items) == MaxNestedDescendants {
			truncated = true
			break
		}
		var issue domain.Issue
		if err := json.Unmarshal(raw, &issue); err != nil {
			return nil, false, err
		}
		if seen[issue.ID] {
			continue
		}
		seen[issue.ID] = true
		normalizeIssueRecord(&issue)
		if query.Summary {
			issue = issueListProjection(issue)
			if query.IncludeDescription {
				issue.Description = description
			}
		}
		items = append(items, issue)
	}
	return items, truncated, rows.Err()
}

// nestedIssueGroups counts each group's top-level rows with their matching
// descendants: every row of the result is counted once, in the group(s) of
// its top-level row.
func (s *SQLiteStore) nestedIssueGroups(ctx context.Context, query IssueRecordQuery) ([]IssueRecordGroup, error) {
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
	// A column grouping carries the top-level row's value down the tree (g);
	// the others join the top-level row (its id) for their group joins.
	carried := ""
	var carriedArgs []any
	if query.GroupBy == "cases" {
		// The top-level row's case index is carried like a column.
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
	// The top-level row of each counted row, for its group joins.
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
	// The field comes from the fixed allowlist, never from input SQL.
	from += " LEFT JOIN issue_attribute_records g ON g.workspace_key=i.workspace_key AND g.issue_id=i.id AND g.field='" + issueGroupAttribute(query.GroupBy) + "'"
	return count("SELECT COALESCE(g.value,''),COUNT(*) FROM " + from + " GROUP BY COALESCE(g.value,'') ORDER BY COALESCE(g.value,'')")
}
