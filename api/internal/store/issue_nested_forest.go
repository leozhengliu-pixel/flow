package store

import (
	"context"
	"slices"
	"strings"
)

// Nested counts of a query whose whole condition is on columns of the tree
// index (archived, team, status and priority terms, the viewer's teams and
// shared issues) and that group by status or priority need no walk in the
// database: the moves follow from the index entries of the result's
// sub-issues and of their top-level parents, which one index-only read
// returns, joined here in memory. The walk instead looks up every parent's
// row by primary key and probes the index once per tree row, about ten
// microseconds a row on MySQL (over 300 ms for a workspace of 75k issues and
// 21k sub-issues).

// issueTreeBase is the query's whole nested base (issueNestedBase) on the
// tree index columns of alias i, given the query's filter compiled for the
// index (issueTreeFilter), or ok=false when the base has other terms.
func issueTreeBase(query IssueRecordQuery, filter string, filterArgs []any) (string, []any, bool) {
	if query.Text != "" || query.SearchText != "" || len(query.ProjectIDs) > 0 || len(query.IssueIDs) > 0 || query.RestrictToIssueIDs || len(query.ReleaseIDs) > 0 {
		return "", nil, false
	}
	// The scope carries the archived, team, status and allowed-team terms
	// and the viewer's archived teams.
	scope, args, _ := issueNestedChildScope(query, "i")
	if query.Access != nil && !query.Access.Admin {
		if query.Access.UserID == "" {
			return "", nil, false
		}
		teams, values := bindList("i.team_id", query.Access.VisibleTeamIDs)
		scope += " AND (" + teams + " OR i.id IN (SELECT id FROM shared_issues))"
		args = append(args, values...)
	}
	if filter != "" {
		scope += " AND " + filter
		args = append(args, filterArgs...)
	}
	return scope, args, true
}

// issueTreeFilter compiles a filter made of status, status type and priority
// terms (in any combination) for the tree index; ok is false for other
// filters. The MySQL tree index has no status type column: there a filter
// with status type terms becomes the list of status ids it selects, read
// from the workspace's counters, which hold every (status, status type,
// priority) an issue has. A status seen with several status types, one
// selected and one not, has no such list.
func (s *SQLiteStore) issueTreeFilter(ctx context.Context, query IssueRecordQuery) (string, []any, bool, error) {
	filter := query.Filter
	if filter.Field == "" && len(filter.And) == 0 && len(filter.Or) == 0 && filter.Not == nil {
		return "", nil, true, nil
	}
	fields := map[string]bool{}
	var leaves func(IssueFilter) bool
	leaves = func(node IssueFilter) bool {
		for _, child := range append(slices.Clone(node.And), node.Or...) {
			if !leaves(child) {
				return false
			}
		}
		if node.Not != nil && !leaves(*node.Not) {
			return false
		}
		if node.Field == "" {
			return true
		}
		field := map[string]string{"status": "status", "state": "status", "stateid": "status", "statustype": "statusType", "priority": "priority"}[strings.ToLower(node.Field)]
		fields[field] = true
		return field != ""
	}
	if !leaves(filter) {
		return "", nil, false, nil
	}
	remaining := 100
	condition, args, err := compileIssueFilter(filter, 0, &remaining)
	if err != nil {
		return "", nil, false, err
	}
	if s.dialect != "mysql" || !fields["statusType"] {
		return condition, args, true, nil
	}
	if fields["priority"] {
		return "", nil, false, nil
	}
	rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT i.state_id,CASE WHEN "+condition+" THEN 1 ELSE 0 END FROM issue_scope_counts i WHERE i.workspace_key=? AND i.scope_type='workspace' AND i.total>0", append(append([]any{}, args...), query.Workspace)...)
	if err != nil {
		return "", nil, false, err
	}
	defer rows.Close()
	selected := map[string]bool{}
	for rows.Next() {
		var state string
		var matches int
		if err := rows.Scan(&state, &matches); err != nil {
			return "", nil, false, err
		}
		if seen, ok := selected[state]; ok && seen != (matches == 1) {
			return "", nil, false, nil
		}
		selected[state] = matches == 1
	}
	if err := rows.Err(); err != nil {
		return "", nil, false, err
	}
	states := []string{}
	for state, matches := range selected {
		if matches {
			states = append(states, state)
		}
	}
	slices.Sort(states)
	clause, values := bindList("i.state_id", states)
	return clause, values, true, nil
}

// nestedIssueMovesIndexSQL reads the id, parent id (empty on top-level rows)
// and group key of the result's sub-issues, then of the top-level rows of the
// result that are parents of one of them, all from the tree index.
func (s *SQLiteStore) nestedIssueMovesIndexSQL(ctx context.Context, query IssueRecordQuery) (string, []any, bool, error) {
	if query.GroupBy != "status" && query.GroupBy != "priority" {
		return "", nil, false, nil
	}
	column, err := issueGroupColumn(query.GroupBy)
	if err != nil {
		return "", nil, false, nil
	}
	filter, filterArgs, ok, err := s.issueTreeFilter(ctx, query)
	if err != nil || !ok {
		return "", nil, false, err
	}
	base, baseArgs, ok := issueTreeBase(query, filter, filterArgs)
	if !ok {
		return "", nil, false, nil
	}
	prefix, prefixArgs := issueAccessCTE(query)
	// On MySQL the parents are found by scanning the top-level entries
	// against the distinct parent ids (about half a microsecond an entry),
	// not by probing the sub-issues once per top-level entry, which it picks
	// for a whole workspace otherwise (three times slower). Index condition
	// pushdown, which it applies to the terms after the parent, makes the
	// entry scans about half as fast again. (The subquery's alias i is its
	// own; it reads no column of the outer i.)
	outer, inner := "", ""
	if s.dialect == "mysql" {
		outer, inner = "/*+ NO_ICP(i) */ ", "/*+ SEMIJOIN(MATERIALIZATION) NO_ICP(i) */ "
	}
	index := s.issueTreeIndexed("i")
	statement := prefix + "SELECT " + outer + "i.id,i.parent_id,i." + column + " FROM " + index + " WHERE i.workspace_key=? AND i.parent_id>'' AND " + base +
		" UNION ALL SELECT " + outer + "i.id,'',i." + column + " FROM " + index + " WHERE i.workspace_key=? AND i.parent_id='' AND " + base +
		" AND i.id IN (SELECT " + inner + "i.parent_id FROM " + index + " WHERE i.workspace_key=? AND i.parent_id>'' AND " + base + ")"
	args := append([]any{}, prefixArgs...)
	for range 3 {
		args = append(args, query.Workspace)
		args = append(args, baseArgs...)
	}
	return statement, args, true, nil
}

// nestedIssueMovesFromIndex is nestedIssueMoves for a query issueTreeBase
// covers, grouped by status or priority; handled is false for other queries.
func (s *SQLiteStore) nestedIssueMovesFromIndex(ctx context.Context, query IssueRecordQuery) (map[string]int64, bool, error) {
	statement, args, ok, err := s.nestedIssueMovesIndexSQL(ctx, query)
	if err != nil || !ok {
		return nil, ok, err
	}
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, true, err
	}
	defer rows.Close()
	type entry struct{ parent, key string }
	entries := map[string]entry{}
	for rows.Next() {
		var id string
		var item entry
		if err := rows.Scan(&id, &item.parent, &item.key); err != nil {
			return nil, true, err
		}
		entries[id] = item
	}
	if err := rows.Err(); err != nil {
		return nil, true, err
	}
	moves := map[string]int64{}
	for _, item := range entries {
		if _, nested := entries[item.parent]; item.parent == "" || !nested {
			continue // a top-level row
		}
		// Up to the top-level row: the nearest ancestor whose parent is not
		// in the result. Nested pages list descendants maxNestedDepth levels
		// down; a row further down, or on a parent cycle (no top-level row),
		// is on no page, so it leaves its group without joining another.
		top, depth := item, 0
		for depth <= maxNestedDepth {
			next, ok := entries[top.parent]
			if top.parent == "" || !ok {
				break
			}
			top, depth = next, depth+1
		}
		if depth > maxNestedDepth {
			moves[item.key]--
			continue
		}
		if top.key == item.key {
			continue
		}
		moves[top.key]++
		moves[item.key]--
	}
	return moves, true, nil
}
