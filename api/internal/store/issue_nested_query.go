package store

import (
	"context"
	"encoding/json"
	"slices"
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
	return "SELECT c.id," + columns + " FROM nest_tree t CROSS JOIN " + s.issueParentIndex("c") + " CROSS JOIN issue_records i WHERE c.workspace_key=? AND c.parent_id=t.id AND i.workspace_key=c.workspace_key AND i.id=c.id AND t.depth<" + strconv.Itoa(maxNestedDepth) + " AND " + base
}

// issueParentIndex is issue_records under alias, read through the parent
// index where the dialect can name it.
func (s *SQLiteStore) issueParentIndex(alias string) string {
	switch s.dialect {
	case "sqlite":
		return "issue_records " + alias + " INDEXED BY issue_records_parent_idx"
	case "mysql":
		return "issue_records " + alias + " FORCE INDEX (issue_records_parent_idx)"
	}
	return "issue_records " + alias
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
//
// The flat counts already count every row once, in its own group (from the
// precomputed counters where the query allows); the nested rows (the
// matching descendants of top-level rows) then move from their own group to
// their top-level row's. Only the subtrees under top-level rows that have
// sub-issues are walked, so the cost follows the result's sub-issues rather
// than its size. A top-level row is a row of its own group, so the moves
// never add a group.
//
// Without team or status terms the walk starts from every parent of the
// workspace; a result the counters bound to a small part of the workspace (a
// project, one assignee) is cheaper to walk whole in one pass.
func (s *SQLiteStore) nestedIssueGroups(ctx context.Context, query IssueRecordQuery) ([]IssueRecordGroup, error) {
	query.Cursor = ""
	query = query.withoutGroupValue()
	if query.GroupBy != "none" {
		if _, _, narrowed := issueNestedChildScope(query, "x"); !narrowed {
			small, err := s.smallIssueResult(ctx, query)
			if err != nil {
				return nil, err
			}
			if small {
				return s.nestedIssueGroupsByWalk(ctx, query)
			}
		}
	}
	return s.nestedIssueGroupsByMoves(ctx, query)
}

// nestedIssueGroupsByMoves is nestedIssueGroups from the flat counts and the
// moves of the nested rows.
func (s *SQLiteStore) nestedIssueGroupsByMoves(ctx context.Context, query IssueRecordQuery) ([]IssueRecordGroup, error) {
	groups, err := s.flatIssueGroups(ctx, query)
	if err != nil || query.GroupBy == "none" || len(groups) == 0 {
		// Ungrouped, every row counts once whether it is nested or not.
		return groups, err
	}
	moves, err := s.nestedIssueMoves(ctx, query)
	if err != nil || len(moves) == 0 {
		return groups, err
	}
	if query.GroupBy == "cases" {
		// Moves are keyed by case index; groups by the case's value.
		byValue := make(map[string]int64, len(moves))
		for index, count := range moves {
			value, ok := issueGroupCaseValue(query.GroupCases, index)
			if !ok {
				return nil, ErrIssueQuery
			}
			byValue[value] += count
		}
		moves = byValue
	}
	result := make([]IssueRecordGroup, 0, len(groups))
	for _, group := range groups {
		group.Count += moves[group.Value]
		if group.Count > 0 {
			result = append(result, group)
		}
	}
	return result, nil
}

// nestedResultScanCost is how many workspace issues the parents scan of a
// nested count costs about as much as one row of a result read through its
// own index (a row lookup and a sub-issue probe against a parent's lookup for
// about a fifth of the workspace).
const nestedResultScanCost = 5

// smallIssueResult reports that the counters bound the query's result to less
// than 1/nestedResultScanCost of the workspace's issues. The bound keeps the
// query's team, project and assignee terms (the top-level ones of its filter)
// and drops the rest, which only narrow the result; without such a term
// there is no bound.
func (s *SQLiteStore) smallIssueResult(ctx context.Context, query IssueRecordQuery) (bool, error) {
	bound := IssueRecordQuery{Workspace: query.Workspace, Archived: query.Archived, TeamIDs: query.TeamIDs, ProjectIDs: query.ProjectIDs, GroupBy: "none"}
	terms := []IssueFilter{query.Filter}
	if query.Filter.Field == "" {
		terms = query.Filter.And
	}
	for _, term := range terms {
		field, operator := strings.ToLower(term.Field), strings.ToLower(term.Operator)
		if slices.Contains([]string{"assignee", "assigneeid", "team", "teamid", "project", "projectid"}, field) && (operator == "" || operator == "is" || operator == "in") && len(term.And) == 0 && len(term.Or) == 0 && term.Not == nil {
			bound.Filter.And = append(bound.Filter.And, term)
		}
	}
	if len(bound.TeamIDs) == 0 && len(bound.ProjectIDs) == 0 && len(bound.Filter.And) == 0 {
		return false, nil
	}
	rows, handled, err := s.issueGroupsFromStats(ctx, bound)
	if err != nil || !handled {
		return false, err
	}
	workspace, handled, err := s.issueGroupsFromStats(ctx, IssueRecordQuery{Workspace: query.Workspace, Archived: query.Archived, GroupBy: "none"})
	if err != nil || !handled {
		return false, err
	}
	count := func(groups []IssueRecordGroup) int64 {
		if len(groups) == 0 {
			return 0
		}
		return groups[0].Count
	}
	return count(rows)*nestedResultScanCost < count(workspace), nil
}

// issueTreeIndex leads with the parent, so nested counts walk sub-issues level
// by level, and carries the columns the common views test (team, archived,
// status, priority; more where the dialect allows), so the walk and the
// parents scan read index entries instead of rows. MySQL keeps the key within
// its 3072-byte limit; there every index carries the primary key (the id).
var issueTreeIndex = map[string]string{
	"sqlite":   "issue_records_tree_idx ON issue_records(workspace_key,parent_id,team_id,archived,state_id,priority,id,state_type,project_id,assignee_id)",
	"mysql":    "issue_records_tree_idx ON issue_records(workspace_key,parent_id,team_id,archived,state_id,priority)",
	"postgres": "issue_records_tree_idx ON issue_records(workspace_key,parent_id,team_id,archived,state_id,priority) INCLUDE (id,state_type,project_id,assignee_id)",
}

// issueTreeIndexed is issue_records under alias, read through the tree index
// where the dialect can name it (a fresh import has no statistics to pick it).
func (s *SQLiteStore) issueTreeIndexed(alias string) string {
	switch s.dialect {
	case "sqlite":
		return "issue_records " + alias + " INDEXED BY issue_records_tree_idx"
	case "mysql":
		return "issue_records " + alias + " FORCE INDEX (issue_records_tree_idx)"
	}
	return "issue_records " + alias
}

// issueNestedChildScope is a condition every row of the result meets, on
// columns of the tree index (alias): the parents scan reads only the parents
// of rows that may be in the result. narrowed reports a team, status or id
// term (beyond the archived state), which keeps that scan within the result.
func issueNestedChildScope(query IssueRecordQuery, alias string) (string, []any, bool) {
	clauses, args, narrowed := []string{}, []any{}, false
	add := func(clause string, values []any) {
		clauses = append(clauses, clause)
		args = append(args, values...)
	}
	if query.Archived != "all" {
		archived := 0
		if query.Archived == "true" {
			archived = 1
		}
		add(alias+".archived=?", []any{archived})
	}
	for _, item := range []struct {
		column   string
		values   []string
		required bool
	}{{"team_id", query.TeamIDs, false}, {"state_id", query.StateIDs, false}, {"team_id", query.AllowedTeamIDs, query.AllowedTeamIDs != nil}} {
		if len(item.values) > 0 || item.required {
			clause, values := bindList(alias+"."+item.column, item.values)
			add(clause, values)
			narrowed = true
		}
	}
	if query.Access != nil && len(query.Access.ArchivedTeamIDs) > 0 {
		clause, values := bindList(alias+".team_id", query.Access.ArchivedTeamIDs)
		add("NOT "+clause, values)
	}
	if len(clauses) == 0 {
		return "1=1", nil, false
	}
	return strings.Join(clauses, " AND "), args, narrowed
}

// nestedIssueMoves walks the nested rows of the result and returns, per group,
// how many of them move in (positive) or out (negative).
func (s *SQLiteStore) nestedIssueMoves(ctx context.Context, query IssueRecordQuery) (map[string]int64, error) {
	statement, args, err := s.nestedIssueMovesSQL(query)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	moves := map[string]int64{}
	for rows.Next() {
		if query.GroupBy == "customer" {
			var sign, count int64
			var key string
			if err := rows.Scan(&sign, &key, &count); err != nil {
				return nil, err
			}
			moves[key] += sign * count
			continue
		}
		var into, from string
		var count int64
		if err := rows.Scan(&into, &from, &count); err != nil {
			return nil, err
		}
		moves[into] += count
		moves[from] -= count
	}
	return moves, rows.Err()
}

// nestedIssueMovesSQL is nestedIssueMoves' statement: rows of (sign, customer,
// count) for a customer grouping, else (top-level key, own key, count).
//
// nest_tree holds the top-level rows that have a matching sub-issue and
// their matching descendants, carrying the top-level row's key (a column
// grouping, or the case index of a "cases" grouping) or id down the tree.
// It starts from the distinct parents of the sub-issues in the child scope
// (an index-only scan of the tree index, joined in that order). Each level is
// read through the tree index with the query's condition applied to the
// entry itself, so terms the index carries need no row.
func (s *SQLiteStore) nestedIssueMovesSQL(query IssueRecordQuery) (string, []any, error) {
	base, baseArgs, err := s.issueNestedBase(query)
	if err != nil {
		return "", nil, err
	}
	root, rootArgs, err := s.issueNestedRootClause(query)
	if err != nil {
		return "", nil, err
	}
	columns, anchor, step := "id,root_key,depth", "i.id,i.id,0", "t.root_key,t.depth+1"
	var keyArgs []any
	attribute := issueGroupAttribute(query.GroupBy)
	if query.GroupBy != "customer" && attribute == "" {
		var key string
		if query.GroupBy == "cases" {
			if key, keyArgs, err = issueGroupCaseExpression(query.GroupCases); err != nil {
				return "", nil, err
			}
		} else {
			column, err := issueGroupColumn(query.GroupBy)
			if err != nil {
				return "", nil, err
			}
			key = "i." + column
		}
		columns, anchor, step = "id,root_key,own_key,depth", "i.id,"+key+","+key+",0", "t.root_key,"+key+",t.depth+1"
	}
	scope, scopeArgs, _ := issueNestedChildScope(query, "x")
	prefix, prefixArgs := issueAccessCTE(query)
	args := append([]any{}, prefixArgs...)
	args = append(args, keyArgs...)
	args = append(args, keyArgs...)
	join := "CROSS JOIN"
	if s.dialect == "mysql" {
		join = "STRAIGHT_JOIN"
	}
	start := "SELECT " + anchor + " FROM (SELECT DISTINCT x.parent_id AS id FROM " + s.issueTreeIndexed("x") + " WHERE x.workspace_key=? AND x.parent_id>'' AND " + scope + ") p " + join + " issue_records i WHERE i.id=p.id AND " + base + " AND " + root
	args = append(args, query.Workspace)
	args = append(args, scopeArgs...)
	args = append(args, baseArgs...)
	args = append(args, rootArgs...)
	children := "SELECT i.id," + step + " FROM nest_tree t CROSS JOIN " + s.issueTreeIndexed("i") + " WHERE i.workspace_key=? AND i.parent_id=t.id AND t.depth<" + strconv.Itoa(maxNestedDepth) + " AND " + base
	cte := "nest_tree(" + columns + ") AS (" + start + " UNION ALL " + children + ")"
	args = append(args, keyArgs...)
	args = append(args, query.Workspace)
	args = append(args, baseArgs...)
	var statement string
	switch {
	case query.GroupBy == "customer":
		// Customer groups are multi-membership: a nested row leaves each of
		// its own customers' groups (or "No customer") and joins each of its
		// top-level row's.
		key := "COALESCE(c.customer_id,'" + UnknownCustomerGroup + "')"
		member := func(sign, issue string) string {
			return "SELECT " + sign + "," + key + ",COUNT(DISTINCT t.id) FROM nest_tree t JOIN customer_request_filter_records r ON r.workspace_key=? AND r.issue_id=" + issue + " LEFT JOIN customer_filter_records c ON c.workspace_key=r.workspace_key AND c.customer_id=r.customer_id WHERE t.depth>0 GROUP BY " + key
		}
		none := func(sign, issue string) string {
			return "SELECT " + sign + ",'',COUNT(*) FROM nest_tree t WHERE t.depth>0 AND NOT EXISTS (SELECT 1 FROM customer_request_filter_records r WHERE r.workspace_key=? AND r.issue_id=" + issue + ")"
		}
		statement = member("1", "t.root_key") + " UNION ALL " + member("-1", "t.id") + " UNION ALL " + none("1", "t.root_key") + " UNION ALL " + none("-1", "t.id")
		args = append(args, query.Workspace, query.Workspace, query.Workspace, query.Workspace)
	case attribute != "":
		// The field comes from the fixed allowlist, never from input SQL.
		value := func(alias, issue string) string {
			return "LEFT JOIN issue_attribute_records " + alias + " ON " + alias + ".workspace_key=? AND " + alias + ".issue_id=" + issue + " AND " + alias + ".field='" + attribute + "'"
		}
		statement = "SELECT COALESCE(gt.value,''),COALESCE(gs.value,''),COUNT(*) FROM nest_tree t " + value("gt", "t.root_key") + " " + value("gs", "t.id") + " WHERE t.depth>0 AND COALESCE(gt.value,'')<>COALESCE(gs.value,'') GROUP BY COALESCE(gt.value,''),COALESCE(gs.value,'')"
		args = append(args, query.Workspace, query.Workspace)
	default:
		statement = "SELECT t.root_key,t.own_key,COUNT(*) FROM nest_tree t WHERE t.depth>0 AND t.root_key<>t.own_key GROUP BY t.root_key,t.own_key"
	}
	return withIssueCTE(prefix, cte) + statement, args, nil
}
