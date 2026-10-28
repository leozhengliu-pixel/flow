package store

import (
	"context"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Group counts that neither the precomputed counters nor the tree index read
// (issue_nested_forest.go) serve, such as a team's issues by assignee or by
// label, a filter with an OR or a label term, or Focus cases, are counted in
// memory from index-only reads of the query's scope:
//
//   - the scope's rows (id, parent, group column and one bit per filter term
//     on an indexed column, which the database evaluates on the index entry)
//     from issue_records_team_tree_idx, which leads with the team;
//   - on MySQL, whose key limit leaves the status out of that index, the
//     rows' statuses from issue_records_team_state_order_idx, with the status
//     and status type terms evaluated per status on the workspace counters;
//   - the rows' labels for label terms, the grouping attribute (first label,
//     milestone, estimate) and the issues shared with a viewer who cannot see
//     every team, each from its own index.
//
// The terms are combined in memory as the filter combines them, so every
// comparison of a value stays the database's (its collation included); only
// ids are joined here. The same rows then give the flat counts and, by
// following each row's parents within the result, the nested ones. A team of
// 20,000 issues reads in about 7 ms per index on MySQL; the SQL counts read
// every row of the result and probe the label and attribute tables per row
// (60-120 ms), and the nested ones walk the result again.

// issueTeamTreeIndex leads with the team and archived state, then carries the
// parent and the columns team views group and filter by. MySQL keeps the key
// within its 3072-byte limit (four VARCHAR(191) columns): the status is read
// from issue_records_team_state_order_idx there, and every index carries the
// primary key (the id).
var issueTeamTreeIndex = map[string]string{
	"sqlite":   "issue_records_team_tree_idx ON issue_records(workspace_key,team_id,archived,parent_id,state_id,state_type,priority,assignee_id,id)",
	"mysql":    "issue_records_team_tree_idx ON issue_records(workspace_key,team_id,archived,parent_id,assignee_id,priority)",
	"postgres": "issue_records_team_tree_idx ON issue_records(workspace_key,team_id,archived,parent_id) INCLUDE (id,state_id,state_type,priority,assignee_id)",
}

// issueTeamTreeIndexed is issue_records under alias, read through the team
// tree index where the dialect can name it.
func (s *SQLiteStore) issueTeamTreeIndexed(alias string) string {
	switch s.dialect {
	case "sqlite":
		return "issue_records " + alias + " INDEXED BY issue_records_team_tree_idx"
	case "mysql":
		return "issue_records " + alias + " FORCE INDEX (issue_records_team_tree_idx)"
	}
	return "issue_records " + alias
}

// maxIssueForestBits bounds the terms (and the distinct label values) of one
// in-memory count: each takes a bit of a signed 64-bit column.
const maxIssueForestBits = 62

type issueForestRow struct {
	parent string
	key    string
	state  string
	bits   uint64
	labels uint64
	flags  uint8
}

const (
	issueForestHasLabel = 1 << iota
	issueForestHasState
	issueForestShared
)

type issueForestTerm struct {
	bit    uint
	clause string
	args   []any
}

type issueForestPlan struct {
	query IssueRecordQuery
	// rowTerms are evaluated on the scope's index entries, stateTerms (MySQL)
	// per status on the workspace counters; each sets its bit of a row.
	rowTerms, stateTerms []issueForestTerm
	bits                 uint
	// labels are the label ids label terms test, a bit each; anyLabel reads
	// whether a row has any label at all.
	labels   []string
	anyLabel bool
	filter   func(*issueForestRow) bool
	cases    []func(*issueForestRow) bool
	// access is the bit of the viewer's teams (-1 when every row is visible);
	// a row outside them is visible when it is shared with the viewer.
	access int
	// keyColumn is the grouping column read with the row ("" when the key
	// comes from elsewhere); stateStream reads the statuses (MySQL);
	// attribute names the grouping attribute.
	keyColumn   string
	stateStream bool
	attribute   string
}

// issueFilterOnStatusAndPriority reports a filter made only of AND-ed status,
// status type and priority terms, which the counters and the tree index read
// serve.
func issueFilterOnStatusAndPriority(filter IssueFilter) bool {
	if len(filter.Or) > 0 || filter.Not != nil {
		return false
	}
	for _, child := range filter.And {
		if !issueFilterOnStatusAndPriority(child) {
			return false
		}
	}
	switch strings.ToLower(filter.Field) {
	case "", "status", "state", "stateid", "statustype", "priority":
		return true
	}
	return false
}

// preferIssueForest reports a query that the counters and the tree index
// read do not serve, which the in-memory count serves faster than SQL.
func preferIssueForest(query IssueRecordQuery) bool {
	simple := (query.GroupBy == "status" || query.GroupBy == "priority" || query.GroupBy == "none") && issueFilterOnStatusAndPriority(query.Filter) && len(query.StateIDs) == 0 && query.AllowedTeamIDs == nil
	return !simple
}

// issueForestPlan plans the in-memory count of query, or ok=false when it has
// a term or grouping the index reads do not carry.
func (s *SQLiteStore) issueForestPlan(query IssueRecordQuery) (*issueForestPlan, bool) {
	query = query.withoutGroupValue()
	if query.Workspace == "" || query.Text != "" || query.SearchText != "" || len(query.ProjectIDs) > 0 || len(query.IssueIDs) > 0 || query.RestrictToIssueIDs || len(query.ReleaseIDs) > 0 {
		return nil, false
	}
	// Invalid filters and cases are reported by the SQL count.
	if _, _, err := issueRecordWhere(query); err != nil {
		return nil, false
	}
	mysql := s.dialect == "mysql"
	p := &issueForestPlan{query: query, access: -1}
	switch query.GroupBy {
	case "status":
		if mysql {
			p.stateStream = true
		} else {
			p.keyColumn = "i.state_id"
		}
	case "priority":
		p.keyColumn = "i.priority"
	case "assignee":
		p.keyColumn = "i.assignee_id"
	case "team":
		p.keyColumn = "i.team_id"
	case "none", "cases":
	default:
		if p.attribute = issueGroupAttribute(query.GroupBy); p.attribute == "" {
			return nil, false
		}
	}
	var ok bool
	if p.filter, ok = p.compile(query.Filter, mysql); !ok {
		return nil, false
	}
	if len(query.StateIDs) > 0 {
		clause, values := bindList("i.state_id", query.StateIDs)
		states := p.term(clause, values, mysql)
		filter := p.filter
		p.filter = func(row *issueForestRow) bool { return states(row) && filter(row) }
	}
	if query.GroupBy == "cases" {
		for _, item := range query.GroupCases {
			match, ok := p.compile(item.Filter, mysql)
			if !ok {
				return nil, false
			}
			p.cases = append(p.cases, match)
		}
	}
	if access := query.Access; access != nil && !access.Admin {
		clause, values := bindList("i.team_id", access.VisibleTeamIDs)
		p.access = int(p.bits)
		p.term(clause, values, false)
	}
	if len(p.stateTerms) > 0 {
		p.stateStream = true
	}
	if p.bits > maxIssueForestBits || len(p.labels) > maxIssueForestBits {
		return nil, false
	}
	return p, true
}

// term adds a condition on alias i's indexed columns (on the workspace
// counters when onState) and returns its test.
func (p *issueForestPlan) term(clause string, args []any, onState bool) func(*issueForestRow) bool {
	item := issueForestTerm{bit: p.bits, clause: clause, args: args}
	p.bits++
	if onState {
		p.stateTerms = append(p.stateTerms, item)
	} else {
		p.rowTerms = append(p.rowTerms, item)
	}
	mask := uint64(1) << item.bit
	return func(row *issueForestRow) bool { return row.bits&mask != 0 }
}

// compile is compileIssueFilter's condition as a test of a row: AND-ed parts
// (the AND children, the OR children, the negated child and the term).
func (p *issueForestPlan) compile(node IssueFilter, mysql bool) (func(*issueForestRow) bool, bool) {
	if field, ok := issueFilterFieldAliases[strings.ToLower(node.Field)]; ok {
		node.Field = field
	}
	parts := []func(*issueForestRow) bool{}
	for _, child := range node.And {
		test, ok := p.compile(child, mysql)
		if !ok {
			return nil, false
		}
		parts = append(parts, test)
	}
	if len(node.Or) > 0 {
		alternatives := []func(*issueForestRow) bool{}
		for _, child := range node.Or {
			test, ok := p.compile(child, mysql)
			if !ok {
				return nil, false
			}
			alternatives = append(alternatives, test)
		}
		parts = append(parts, func(row *issueForestRow) bool {
			for _, test := range alternatives {
				if test(row) {
					return true
				}
			}
			return false
		})
	}
	if node.Not != nil {
		test, ok := p.compile(*node.Not, mysql)
		if !ok {
			return nil, false
		}
		parts = append(parts, func(row *issueForestRow) bool { return !test(row) })
	}
	if node.Field != "" {
		test, ok := p.compileTerm(IssueFilter{Field: node.Field, Operator: node.Operator, Values: node.Values}, mysql)
		if !ok {
			return nil, false
		}
		parts = append(parts, test)
	}
	switch len(parts) {
	case 0:
		return func(*issueForestRow) bool { return true }, true
	case 1:
		return parts[0], true
	}
	return func(row *issueForestRow) bool {
		for _, test := range parts {
			if !test(row) {
				return false
			}
		}
		return true
	}, true
}

// compileTerm tests one term: label terms on the rows' labels, terms on the
// index columns through their condition's bit.
func (p *issueForestPlan) compileTerm(node IssueFilter, mysql bool) (func(*issueForestRow) bool, bool) {
	onState := false
	switch node.Field {
	case "labels":
		return p.labelTerm(node)
	case "status", "statusType":
		onState = mysql
	case "priority", "assignee", "team", "parent", "id":
	default:
		return nil, false
	}
	budget := 1
	clause, args, err := compileIssueFilter(node, 0, &budget)
	if err != nil {
		return nil, false
	}
	return p.term(clause, args, onState), true
}

// labelTerm is compileIssueFilter's label condition on the rows' labels.
func (p *issueForestPlan) labelTerm(node IssueFilter) (func(*issueForestRow) bool, bool) {
	bit := func(value string) uint64 {
		index := slices.Index(p.labels, value)
		if index < 0 {
			index = len(p.labels)
			p.labels = append(p.labels, value)
		}
		if index >= maxIssueForestBits {
			return 0
		}
		return 1 << uint(index)
	}
	hasLabel := func(row *issueForestRow) bool { return row.flags&issueForestHasLabel != 0 }
	operator := strings.ToLower(node.Operator)
	switch operator {
	case "", "is", "in", "isnot", "notin":
		var mask uint64
		empty := slices.Contains(node.Values, "")
		for _, value := range node.Values {
			mask |= bit(value)
		}
		p.anyLabel = p.anyLabel || empty
		negate := operator == "isnot" || operator == "notin"
		return func(row *issueForestRow) bool {
			return (row.labels&mask != 0 || empty && !hasLabel(row)) != negate
		}, true
	case "includesall", "excludesall":
		var mask uint64
		empty := false
		for _, value := range node.Values {
			if value == "" {
				empty = true
				continue
			}
			mask |= bit(value)
		}
		p.anyLabel = p.anyLabel || empty
		negate := operator == "excludesall"
		return func(row *issueForestRow) bool {
			return (row.labels&mask == mask && !(empty && hasLabel(row))) != negate
		}, true
	case "isempty", "isnotempty":
		p.anyLabel = true
		want := operator == "isnotempty"
		return func(row *issueForestRow) bool { return hasLabel(row) == want }, true
	}
	return nil, false
}

// issueForestBits is the SQL sum of the terms' bits.
func issueForestBits(terms []issueForestTerm) (string, []any) {
	if len(terms) == 0 {
		return "0", nil
	}
	parts := make([]string, len(terms))
	args := []any{}
	for i, item := range terms {
		parts[i] = "(CASE WHEN " + item.clause + " THEN " + strconv.FormatUint(uint64(1)<<item.bit, 10) + " ELSE 0 END)"
		args = append(args, item.args...)
	}
	return strings.Join(parts, "+"), args
}

// issueForestGroups counts query's groups in memory (nested: each row in its
// top-level row's group); handled is false for a query the plan does not
// cover, or when the reads disagree (a write between them), for the SQL
// count to answer.
func (s *SQLiteStore) issueForestGroups(ctx context.Context, query IssueRecordQuery, nested bool) ([]IssueRecordGroup, bool, error) {
	p, ok := s.issueForestPlan(query)
	if !ok {
		return nil, false, nil
	}
	rows, index, ok, err := s.readIssueForest(ctx, p)
	if err != nil {
		return nil, true, err
	}
	if !ok {
		return nil, false, nil
	}
	in := make([]bool, len(rows))
	for i := range rows {
		row := &rows[i]
		if p.access >= 0 && row.bits&(uint64(1)<<uint(p.access)) == 0 && row.flags&issueForestShared == 0 {
			continue
		}
		in[i] = p.filter(row)
	}
	if p.query.GroupBy == "cases" {
		labels := make([]string, len(p.cases)+1)
		for k := range p.cases {
			labels[k] = strconv.Itoa(k)
		}
		labels[len(p.cases)] = "-1"
		for i := range rows {
			if !in[i] {
				continue
			}
			rows[i].key = labels[len(p.cases)]
			for k, match := range p.cases {
				if match(&rows[i]) {
					rows[i].key = labels[k]
					break
				}
			}
		}
	}
	if p.query.GroupBy == "none" {
		for i := range rows {
			rows[i].key = "all"
		}
	}
	counts := map[string]int64{}
	for i := range rows {
		if !in[i] {
			continue
		}
		top, depth := i, 0
		if nested {
			// Up to the top-level row: the nearest ancestor whose parent is
			// not in the result (issue_nested_forest.go). A row further than
			// maxNestedDepth levels down, or on a parent cycle, is on no
			// page and is not counted.
			for depth <= maxNestedDepth {
				parent := rows[top].parent
				next, ok := index[parent]
				if parent == "" || !ok || !in[next] {
					break
				}
				top, depth = int(next), depth+1
			}
			if depth > maxNestedDepth {
				continue
			}
		}
		counts[rows[top].key]++
	}
	groups, err := s.orderedIssueGroups(ctx, p, counts)
	return groups, true, err
}

// readIssueForest reads the plan's scope with its terms' bits, statuses,
// labels, grouping attribute and shared issues. ok is false when a row's
// status is missing or not one the counters describe unambiguously.
func (s *SQLiteStore) readIssueForest(ctx context.Context, p *issueForestPlan) ([]issueForestRow, map[string]int32, bool, error) {
	q := p.query
	where := []string{"i.workspace_key=?"}
	whereArgs := []any{q.Workspace}
	if len(q.TeamIDs) > 0 {
		clause, values := bindList("i.team_id", q.TeamIDs)
		where = append(where, clause)
		whereArgs = append(whereArgs, values...)
	}
	if q.Archived != "all" {
		archived := 0
		if q.Archived == "true" {
			archived = 1
		}
		where = append(where, "i.archived=?")
		whereArgs = append(whereArgs, archived)
	}
	if q.Access != nil && len(q.Access.ArchivedTeamIDs) > 0 {
		clause, values := bindList("i.team_id", q.Access.ArchivedTeamIDs)
		where = append(where, "NOT "+clause)
		whereArgs = append(whereArgs, values...)
	}
	if q.AllowedTeamIDs != nil {
		clause, values := bindList("i.team_id", q.AllowedTeamIDs)
		where = append(where, clause)
		whereArgs = append(whereArgs, values...)
	}
	bits, bitArgs := issueForestBits(p.rowTerms)
	key := "''"
	if p.keyColumn != "" {
		key = p.keyColumn
	}
	intern := map[string]string{}
	shared := func(value string) string {
		if known, ok := intern[value]; ok {
			return known
		}
		intern[value] = value
		return value
	}
	rows := []issueForestRow{}
	index := map[string]int32{}
	result, err := s.db.QueryContext(ctx, "SELECT i.id,i.parent_id,"+key+","+bits+" FROM "+s.issueTeamTreeIndexed("i")+" WHERE "+strings.Join(where, " AND "), append(bitArgs, whereArgs...)...)
	if err != nil {
		return nil, nil, false, err
	}
	for result.Next() {
		var id string
		var row issueForestRow
		var mask int64
		if err := result.Scan(&id, &row.parent, &row.key, &mask); err != nil {
			result.Close()
			return nil, nil, false, err
		}
		row.key, row.bits = shared(row.key), uint64(mask)
		index[id] = int32(len(rows))
		rows = append(rows, row)
	}
	result.Close()
	if err := result.Err(); err != nil {
		return nil, nil, false, err
	}
	// each reads (id, value) rows into the scope's rows.
	each := func(statement string, args []any, apply func(*issueForestRow, string)) error {
		result, err := s.db.QueryContext(ctx, statement, args...)
		if err != nil {
			return err
		}
		defer result.Close()
		for result.Next() {
			var id, value string
			if err := result.Scan(&id, &value); err != nil {
				return err
			}
			if at, ok := index[id]; ok {
				apply(&rows[at], value)
			}
		}
		return result.Err()
	}
	if p.stateStream && len(rows) > 0 {
		statement := "SELECT i.id,i.state_id FROM issue_records i FORCE INDEX (issue_records_team_state_order_idx) WHERE i.workspace_key=?"
		args := []any{q.Workspace}
		if len(q.TeamIDs) > 0 {
			clause, values := bindList("i.team_id", q.TeamIDs)
			statement += " AND " + clause
			args = append(args, values...)
		}
		if err := each(statement, args, func(row *issueForestRow, state string) {
			row.state = shared(state)
			row.flags |= issueForestHasState
		}); err != nil {
			return nil, nil, false, err
		}
		var states map[string]uint64
		if len(p.stateTerms) > 0 {
			var ok bool
			if states, ok, err = s.issueForestStateBits(ctx, q.Workspace, p.stateTerms); err != nil || !ok {
				return nil, nil, false, err
			}
		}
		for i := range rows {
			row := &rows[i]
			if row.flags&issueForestHasState == 0 {
				return nil, nil, false, nil
			}
			if states != nil {
				mask, ok := states[row.state]
				if !ok {
					return nil, nil, false, nil
				}
				row.bits |= mask
			}
			if q.GroupBy == "status" {
				row.key = row.state
			}
		}
	}
	if len(p.labels) > 0 || p.anyLabel {
		labels, labelArgs := issueForestBits(issueForestLabelTerms(p.labels))
		statement := "SELECT l.issue_id," + labels + " FROM issue_label_records l WHERE l.workspace_key=?"
		args := append(labelArgs, q.Workspace)
		if !p.anyLabel {
			clause, values := bindList("l.label_id", p.labels)
			statement += " AND " + clause
			args = append(args, values...)
		}
		if err := each(statement, args, func(row *issueForestRow, mask string) {
			value, _ := strconv.ParseUint(mask, 10, 64)
			row.labels |= value
			row.flags |= issueForestHasLabel
		}); err != nil {
			return nil, nil, false, err
		}
	}
	if p.attribute != "" {
		// Through the value index, which holds only this field's rows (the
		// primary key leads with the issue: every attribute of the workspace).
		attributes := map[string]string{"sqlite": "issue_attribute_records g INDEXED BY issue_attributes_value_idx", "mysql": "issue_attribute_records g FORCE INDEX (issue_attributes_value_idx)"}[s.dialect]
		if attributes == "" {
			attributes = "issue_attribute_records g"
		}
		if err := each("SELECT g.issue_id,g.value FROM "+attributes+" WHERE g.workspace_key=? AND g.field=?", []any{q.Workspace, p.attribute}, func(row *issueForestRow, value string) {
			row.key = shared(value)
		}); err != nil {
			return nil, nil, false, err
		}
	}
	if p.access >= 0 {
		prefix, prefixArgs := issueAccessCTE(q)
		if err := each(prefix+"SELECT s.id,'' FROM shared_issues s", prefixArgs, func(row *issueForestRow, _ string) {
			row.flags |= issueForestShared
		}); err != nil {
			return nil, nil, false, err
		}
	}
	return rows, index, true, nil
}

// issueForestLabelTerms are the label bits: one per label id, set on the
// label records of that id.
func issueForestLabelTerms(labels []string) []issueForestTerm {
	terms := make([]issueForestTerm, len(labels))
	for i, label := range labels {
		terms[i] = issueForestTerm{bit: uint(i), clause: "l.label_id=?", args: []any{label}}
	}
	return terms
}

// issueForestStateBits evaluates the status terms per status on the
// workspace counters, which hold every (status, status type) an issue has;
// ok is false when a status is seen with two status types that the terms
// tell apart.
func (s *SQLiteStore) issueForestStateBits(ctx context.Context, workspace string, terms []issueForestTerm) (map[string]uint64, bool, error) {
	bits, args := issueForestBits(terms)
	rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT i.state_id,"+bits+" FROM issue_scope_counts i WHERE i.workspace_key=? AND i.scope_type='workspace' AND i.total>0", append(args, workspace)...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	states := map[string]uint64{}
	for rows.Next() {
		var state string
		var mask int64
		if err := rows.Scan(&state, &mask); err != nil {
			return nil, false, err
		}
		if seen, ok := states[state]; ok && seen != uint64(mask) {
			return nil, false, nil
		}
		states[state] = uint64(mask)
	}
	return states, true, rows.Err()
}

// orderedIssueGroups lists the counted groups in the SQL count's order: the
// cases' order for cases, numeric for priority, else the grouping column's
// order in the database (its collation), which a query on the column's own
// index returns.
func (s *SQLiteStore) orderedIssueGroups(ctx context.Context, p *issueForestPlan, counts map[string]int64) ([]IssueRecordGroup, error) {
	q := p.query
	groups := []IssueRecordGroup{}
	switch q.GroupBy {
	case "none":
		if count := counts["all"]; count > 0 {
			groups = append(groups, IssueRecordGroup{Value: "all", Count: count})
		}
		return groups, nil
	case "cases":
		byValue := map[string]int64{}
		for index, count := range counts {
			value, ok := issueGroupCaseValue(q.GroupCases, index)
			if !ok {
				return nil, ErrIssueQuery
			}
			byValue[value] += count
		}
		for _, item := range append(slices.Clone(q.GroupCases), IssueGroupCase{}) {
			if count := byValue[item.Value]; count > 0 {
				groups = append(groups, IssueRecordGroup{Value: item.Value, Count: count})
			}
		}
		return groups, nil
	}
	values := make([]string, 0, len(counts))
	for value, count := range counts {
		if count > 0 {
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return groups, nil
	}
	sort.Strings(values)
	if q.GroupBy == "priority" {
		// Integers, read as text.
		sort.SliceStable(values, func(i, j int) bool {
			a, _ := strconv.Atoi(values[i])
			b, _ := strconv.Atoi(values[j])
			return a < b
		})
		for _, value := range values {
			groups = append(groups, IssueRecordGroup{Value: value, Count: counts[value]})
		}
		return groups, nil
	}
	var statement string
	args := []any{q.Workspace}
	switch {
	case s.dialect != "mysql":
		// The columns take the database's default collation (BINARY on
		// SQLite), which the values sort by as literals too.
		statement = "SELECT column1 FROM (VALUES " + strings.TrimSuffix(strings.Repeat("(?),", len(values)), ",") + ") ORDER BY column1"
		if s.dialect == "postgres" {
			statement = "SELECT v.k FROM (VALUES " + strings.TrimSuffix(strings.Repeat("(?),", len(values)), ",") + ") v(k) ORDER BY v.k"
		}
		args = args[:0]
		for _, value := range values {
			args = append(args, value)
		}
	// On MySQL the values sort by the column's own collation, read from its
	// index: grouped by the index prefix, each value is one skip-scan step.
	case q.GroupBy == "status":
		clause, values := bindList("i.state_id", values)
		statement = "SELECT i.state_id FROM issue_scope_counts i WHERE i.workspace_key=? AND i.scope_type='workspace' AND i.scope_id='' AND " + clause + " GROUP BY i.workspace_key,i.scope_type,i.scope_id,i.state_id ORDER BY i.state_id"
		args = append(args, values...)
	case p.attribute != "":
		clause, values := bindList("g.value", values)
		statement = "SELECT g.value FROM issue_attribute_records g WHERE g.workspace_key=? AND g.field=? AND " + clause + " GROUP BY g.workspace_key,g.field,g.value ORDER BY g.value"
		args = append(append(args, p.attribute), values...)
	default:
		column := strings.TrimPrefix(p.keyColumn, "i.")
		clause, values := bindList("i."+column, values)
		statement = "SELECT i." + column + " FROM issue_records i WHERE i.workspace_key=? AND " + clause + " GROUP BY i.workspace_key,i." + column + " ORDER BY i." + column
		args = append(args, values...)
	}
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ordered := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		ordered = append(ordered, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// The empty value (no attribute record) sorts first in every collation;
	// a value the column query does not return (it never does for values
	// read from the column) follows the others.
	emitted := map[string]bool{}
	emit := func(value string) {
		if count := counts[value]; count > 0 && !emitted[value] {
			emitted[value] = true
			groups = append(groups, IssueRecordGroup{Value: value, Count: count})
		}
	}
	emit("")
	for _, value := range ordered {
		emit(value)
	}
	for _, value := range values {
		emit(value)
	}
	return groups, nil
}
