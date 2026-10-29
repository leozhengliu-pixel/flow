package store

import (
	"context"
	"slices"
	"strings"
)

// Selections read matching ids once from a secondary index, rather than
// probing it once per forest row. Each selection sets one filter bit.
type issueForestSelection struct {
	statement string
	args      []any
	mask      uint64
}

func (p *issueForestPlan) selection(statement string, args []any) func(*issueForestRow) bool {
	mask := uint64(1) << p.bits
	p.bits++
	p.selections = append(p.selections, issueForestSelection{statement, args, mask})
	return func(row *issueForestRow) bool { return row.bits&mask != 0 }
}

func (p *issueForestPlan) indexedTable(table, alias, index string) string {
	if index == "" {
		return table + " " + alias
	}
	switch p.dialect {
	case "mysql":
		return table + " " + alias + " FORCE INDEX (" + index + ")"
	case "sqlite":
		return table + " " + alias + " INDEXED BY " + index
	}
	return table + " " + alias
}

func (p *issueForestPlan) columnTable(field string) string {
	index := map[string]string{"project": "issue_records_project_idx", "creator": "issue_records_creator_counts_idx", "cycle": "issue_records_cycle_idx"}[field]
	return p.indexedTable("issue_records", "i", index)
}

func (p *issueForestPlan) textSelection(clause string, args []any) func(*issueForestRow) bool {
	scope, scopeArgs, _ := issueNestedChildScope(p.query, "i")
	values := append([]any{p.query.Workspace}, scopeArgs...)
	return p.selection("SELECT i.id FROM issue_records i WHERE i.workspace_key=? AND "+scope+" AND "+clause, append(values, args...))
}

func (s *SQLiteStore) compileIssueForestScope(p *issueForestPlan) bool {
	q := p.query
	tests := []func(*issueForestRow) bool{p.filter}
	if len(q.ProjectIDs) > 0 {
		test, ok := p.compileTerm(IssueFilter{Field: "project", Values: q.ProjectIDs}, s.dialect == "mysql")
		if !ok {
			return false
		}
		tests = append(tests, test)
	}
	if len(q.IssueIDs) > 0 || q.RestrictToIssueIDs {
		clause, args := bindList("i.id", q.IssueIDs)
		tests = append(tests, p.term(clause, args, false))
	}
	if q.Text != "" {
		value := "%" + escapeIssueLike(strings.ToLower(q.Text)) + "%"
		// Title/identifier substring matching has no covering index. Read the
		// narrow columns once, not the payload or one parent probe per row.
		tests = append(tests, p.textSelection("(LOWER(i.title) LIKE ? ESCAPE '!' OR LOWER(i.identifier) LIKE ? ESCAPE '!')", []any{value, value}))
	}
	if q.Nested && q.SearchText != "" {
		statement, args := s.issueTextSelection(q.Workspace, q.SearchText)
		p.search = len(p.selections)
		tests = append(tests, p.selection(statement, args))
	}
	p.filter = func(row *issueForestRow) bool {
		for _, test := range tests {
			if !test(row) {
				return false
			}
		}
		return true
	}
	return true
}

func (p *issueForestPlan) relationTerm(node IssueFilter) (func(*issueForestRow) bool, bool) {
	table, index := "issue_subscriber_records", "issue_subscribers_user_idx"
	if node.Field == "myActivity" {
		table, index = "issue_actor_records", "issue_actors_user_idx"
	}
	prefix := "SELECT l.issue_id FROM " + p.indexedTable(table, "l", index) + " WHERE l.workspace_key=?"
	anyMember := func() func(*issueForestRow) bool { return p.selection(prefix, []any{p.query.Workspace}) }
	op := strings.ToLower(node.Operator)
	negate := op == "isnot" || op == "notin" || op == "excludesall" || op == "isempty"
	var test func(*issueForestRow) bool
	switch op {
	case "isempty", "isnotempty":
		test = anyMember()
	case "", "is", "in", "isnot", "notin":
		clause, args := bindList("l.user_id", node.Values)
		match := p.selection(prefix+" AND "+clause, append([]any{p.query.Workspace}, args...))
		test = match
		if slices.Contains(node.Values, "") {
			present := anyMember()
			test = func(row *issueForestRow) bool { return match(row) || !present(row) }
		}
	case "includesall", "excludesall":
		tests := []func(*issueForestRow) bool{}
		for _, value := range node.Values {
			if value == "" {
				present := anyMember()
				tests = append(tests, func(row *issueForestRow) bool { return !present(row) })
			} else {
				tests = append(tests, p.selection(prefix+" AND l.user_id=?", []any{p.query.Workspace, value}))
			}
		}
		test = func(row *issueForestRow) bool {
			for _, match := range tests {
				if !match(row) {
					return false
				}
			}
			return true
		}
	default:
		return nil, false
	}
	return func(row *issueForestRow) bool { return test(row) != negate }, true
}

func (p *issueForestPlan) attributeTerm(node IssueFilter) (func(*issueForestRow) bool, bool) {
	clause, args, missing, negate, err := issueAttributeValueClause(node)
	if err != nil {
		return nil, false
	}
	prefix := "SELECT a.issue_id FROM " + p.indexedTable("issue_attribute_records", "a", "issue_attributes_value_idx") + " WHERE a.workspace_key=? AND a.field=?"
	values := []any{p.query.Workspace, indexKey(node.Field)}
	match := p.selection(prefix+" AND "+clause, append(slices.Clone(values), args...))
	test := match
	if missing {
		present := p.selection(prefix, values)
		test = func(row *issueForestRow) bool { return match(row) || !present(row) }
	}
	return func(row *issueForestRow) bool { return test(row) != negate }, true
}

func (s *SQLiteStore) readIssueForestSelections(ctx context.Context, p *issueForestPlan, each func(string, []any, func(*issueForestRow, string)) error, intern func(string) string) error {
	for index, item := range p.selections {
		if index == p.search {
			continue
		}
		// Wrap the selection so search's dialect-specific query is unchanged.
		if err := each("WITH matched(id) AS ("+item.statement+") SELECT id,'' FROM matched", item.args, func(row *issueForestRow, _ string) { row.bits |= item.mask }); err != nil {
			return err
		}
	}
	if p.columnGroup != "" {
		column, _ := issueGroupColumn(p.columnGroup)
		if err := each("SELECT i.id,i."+column+" FROM "+p.columnTable(p.columnGroup)+" WHERE i.workspace_key=?", []any{p.query.Workspace}, func(row *issueForestRow, value string) { row.key = intern(value) }); err != nil {
			return err
		}
	}
	if p.query.GroupBy == "customer" {
		statement := "SELECT DISTINCT r.issue_id,COALESCE(c.customer_id,'" + UnknownCustomerGroup + "') FROM customer_request_filter_records r LEFT JOIN customer_filter_records c ON c.workspace_key=r.workspace_key AND c.customer_id=r.customer_id WHERE r.workspace_key=?"
		if err := each(statement, []any{p.query.Workspace}, func(row *issueForestRow, value string) { row.customers = append(row.customers, intern(value)) }); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) orderedIssueForestCustomers(ctx context.Context, workspace string, counts map[string]int64) ([]IssueRecordGroup, error) {
	// Customer counts append the no-customer group last, unlike attributes.
	rows, err := s.db.QueryContext(ctx, "SELECT customer_id FROM customer_filter_records WHERE workspace_key=? UNION SELECT ? ORDER BY 1", workspace, UnknownCustomerGroup)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []IssueRecordGroup{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		if count := counts[value]; count > 0 {
			groups = append(groups, IssueRecordGroup{Value: value, Count: count})
		}
	}
	if count := counts[""]; count > 0 {
		groups = append(groups, IssueRecordGroup{Value: "", Count: count})
	}
	return groups, rows.Err()
}
