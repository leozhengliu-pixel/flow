package store

import (
	"regexp"
	"strings"
)

// Application capture (record_capture.go) analyses each write to a journal
// table to find the rows it changes. Statements are the ones this package
// writes: one target table, a column list, bound values and WHERE clauses in
// the portable dialect (ON CONFLICT) or MySQL's own (ON DUPLICATE KEY UPDATE,
// multi-table DELETE/UPDATE). A shape this analyser does not understand is
// reported as such; the caller then schedules a resync instead of guessing.

// captureSQL indexes one statement: the nesting depth outside quotes (-1
// inside a quoted string or identifier) and the positions of its bound
// placeholders.
type captureSQL struct {
	text  string
	depth []int16
	marks []int
}

func scanCaptureSQL(query string) captureSQL {
	s := captureSQL{text: query, depth: make([]int16, len(query))}
	depth := int16(0)
	for i := 0; i < len(query); i++ {
		c := query[i]
		switch c {
		case '\'', '"', '`':
			i = s.quoted(i, c)
			continue
		case '(':
			s.depth[i] = depth
			depth++
			continue
		case ')':
			depth--
		case '?':
			if depth >= 0 {
				s.marks = append(s.marks, i)
			}
		}
		s.depth[i] = depth
	}
	return s
}

// quoted marks a quoted string or identifier opened at start and returns
// the position of its closing quote (the end of the text when unclosed).
func (s *captureSQL) quoted(start int, quote byte) int {
	s.depth[start] = -1
	for j := start + 1; j < len(s.text); j++ {
		s.depth[j] = -1
		switch {
		case s.text[j] == '\\' && quote == '\'' && j+1 < len(s.text):
			j++
			s.depth[j] = -1
		case s.text[j] == quote && j+1 < len(s.text) && s.text[j+1] == quote:
			j++
			s.depth[j] = -1
		case s.text[j] == quote:
			return j
		}
	}
	return len(s.text) - 1
}

func captureWordByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.'
}

// keyword finds a whole-word keyword sequence ("ON DUPLICATE KEY UPDATE")
// at depth 0 in [from,to), or -1.
func (s captureSQL) keyword(words string, from, to int) int {
	parts := strings.Fields(words)
	for i := from; i < to; i++ {
		if s.depth[i] != 0 || i > 0 && captureWordByte(s.text[i-1]) {
			continue
		}
		j, ok := i, true
		for n, part := range parts {
			if n > 0 {
				k := j
				for k < to && (s.text[k] == ' ' || s.text[k] == '\t' || s.text[k] == '\n' || s.text[k] == '\r') {
					k++
				}
				if k == j {
					ok = false
					break
				}
				j = k
			}
			if j+len(part) > to || !strings.EqualFold(s.text[j:j+len(part)], part) || j+len(part) < len(s.text) && captureWordByte(s.text[j+len(part)]) {
				ok = false
				break
			}
			j += len(part)
		}
		if ok {
			return i
		}
	}
	return -1
}

// split separates [from,to) at the commas of its outermost level (inside a
// parenthesized list, the list's own level).
func (s captureSQL) split(from, to int) [][2]int {
	level := int16(-1)
	for i := from; i < to; i++ {
		if s.depth[i] >= 0 && (level < 0 || s.depth[i] < level) {
			level = s.depth[i]
		}
	}
	var parts [][2]int
	start := from
	for i := from; i < to; i++ {
		if s.text[i] == ',' && s.depth[i] == level {
			parts = append(parts, [2]int{start, i})
			start = i + 1
		}
	}
	return append(parts, [2]int{start, to})
}

// closing returns the position of the parenthesis closing the one at open.
func (s captureSQL) closing(open int) int {
	for i := open + 1; i < len(s.text); i++ {
		if s.text[i] == ')' && s.depth[i] == s.depth[open] {
			return i
		}
	}
	return -1
}

// args returns the bound values whose placeholders are in [from,to).
func (s captureSQL) args(args []any, from, to int) ([]any, bool) {
	first, last := -1, -1
	for n, mark := range s.marks {
		if mark >= from && mark < to {
			if first < 0 {
				first = n
			}
			last = n
		}
	}
	if first < 0 {
		return nil, true
	}
	if last >= len(args) {
		return nil, false
	}
	return args[first : last+1], true
}

// captureValue is one value position: a bound value, a literal, or an
// expression whose value only the database knows.
type captureValue struct {
	known bool
	value any
}

func (s captureSQL) value(args []any, from, to int) captureValue {
	for from < to && strings.ContainsRune(" \t\r\n", rune(s.text[from])) {
		from++
	}
	for to > from && strings.ContainsRune(" \t\r\n", rune(s.text[to-1])) {
		to--
	}
	text := s.text[from:to]
	if text == "?" {
		for n, mark := range s.marks {
			if mark == from && n < len(args) {
				return captureValue{known: true, value: args[n]}
			}
		}
		return captureValue{}
	}
	if len(text) >= 2 && text[0] == '\'' && text[len(text)-1] == '\'' && !strings.Contains(text[1:len(text)-1], "\\") {
		inner := text[1 : len(text)-1]
		if !strings.Contains(strings.ReplaceAll(inner, "''", ""), "'") {
			return captureValue{known: true, value: strings.ReplaceAll(inner, "''", "'")}
		}
	}
	if text != "" && strings.Trim(text, "0123456789") == "" {
		return captureValue{known: true, value: text}
	}
	return captureValue{}
}

type captureVerb int

const (
	captureInsert captureVerb = iota + 1
	captureUpdate
	captureDelete
)

type captureConflict int

const (
	conflictNone captureConflict = iota
	conflictNothing
	conflictUpdate
)

// captureStatement is an analysed write.
type captureStatement struct {
	verb  captureVerb
	table string
	// alias qualifies the target table's columns in the from clause; the
	// table name when the statement gives none.
	alias string
	// INSERT: the column list, then VALUES tuples or a SELECT.
	columns    []string
	tuples     [][]captureValue
	selectSQL  string
	selectArgs []any
	conflict   captureConflict
	// assigned: the columns SET (UPDATE) or the conflict clause updates,
	// lower case, with the value each SET assigns.
	assigned map[string]captureValue
	// UPDATE/DELETE: the row source and filter, and their bound values in
	// statement order.
	from, where, tail string
	filterArgs        []any
	limited           bool
	// unknown names why the statement could not be analysed.
	unknown string
}

var (
	captureInsertHead = regexp.MustCompile(`(?is)^\s*INSERT\s+(IGNORE\s+|OR\s+\w+\s+)?INTO\s+([A-Za-z_][A-Za-z0-9_]*)\s*`)
	captureUpdateHead = regexp.MustCompile(`(?is)^\s*UPDATE\s+`)
	captureDeleteHead = regexp.MustCompile(`(?is)^\s*DELETE\s+`)
	captureIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func captureColumnName(text string) string {
	text = strings.Trim(strings.TrimSpace(text), "`\"")
	if dot := strings.LastIndexByte(text, '.'); dot >= 0 {
		text = text[dot+1:]
	}
	return strings.ToLower(strings.Trim(text, "`\""))
}

// parseCaptureStatement analyses a write statement for application capture.
func parseCaptureStatement(query string, args []any) captureStatement {
	s := scanCaptureSQL(query)
	if match := captureInsertHead.FindStringSubmatchIndex(query); match != nil {
		return s.parseInsert(args, match)
	}
	if match := captureUpdateHead.FindStringIndex(query); match != nil {
		return s.parseUpdate(args, match[1])
	}
	if match := captureDeleteHead.FindStringIndex(query); match != nil {
		return s.parseDelete(args, match[1])
	}
	return captureStatement{unknown: "statement verb"}
}

func (s captureSQL) parseInsert(args []any, match []int) captureStatement {
	st := captureStatement{verb: captureInsert, table: strings.ToLower(s.text[match[4]:match[5]])}
	st.alias = st.table
	if match[2] >= 0 && strings.EqualFold(strings.TrimSpace(s.text[match[2]:match[3]]), "IGNORE") {
		st.conflict = conflictNothing
	}
	position := match[1]
	if position >= len(s.text) || s.text[position] != '(' {
		st.unknown = "insert without a column list"
		return st
	}
	end := s.closing(position)
	if end < 0 {
		st.unknown = "column list"
		return st
	}
	for _, part := range s.split(position+1, end) {
		st.columns = append(st.columns, captureColumnName(s.text[part[0]:part[1]]))
	}
	body := end + 1
	clause := len(s.text)
	for _, words := range []string{"ON CONFLICT", "ON DUPLICATE KEY UPDATE"} {
		if at := s.keyword(words, body, len(s.text)); at >= 0 && at < clause {
			clause = at
		}
	}
	if values := s.keyword("VALUES", body, clause); values >= 0 && strings.TrimSpace(s.text[body:values]) == "" {
		for i := values + len("VALUES"); i < clause; i++ {
			if s.text[i] != '(' || s.depth[i] != 0 {
				continue
			}
			close := s.closing(i)
			if close < 0 {
				st.unknown = "values tuple"
				return st
			}
			parts := s.split(i+1, close)
			if len(parts) != len(st.columns) {
				st.unknown = "values arity"
				return st
			}
			tuple := make([]captureValue, len(parts))
			for n, part := range parts {
				tuple[n] = s.value(args, part[0], part[1])
			}
			st.tuples = append(st.tuples, tuple)
			i = close
		}
		if len(st.tuples) == 0 {
			st.unknown = "values"
			return st
		}
	} else if selectAt := s.keyword("SELECT", body, clause); selectAt >= 0 && strings.TrimSpace(s.text[body:selectAt]) == "" {
		st.selectSQL = strings.TrimSpace(s.text[selectAt:clause])
		var ok bool
		if st.selectArgs, ok = s.args(args, selectAt, clause); !ok {
			st.unknown = "select arguments"
			return st
		}
	} else {
		st.unknown = "insert source"
		return st
	}
	if clause < len(s.text) {
		rest := s.text[clause:]
		st.assigned = map[string]captureValue{}
		set := -1
		if at := s.keyword("DO NOTHING", clause, len(s.text)); at >= 0 {
			st.conflict = conflictNothing
			return st
		} else if at := s.keyword("DO UPDATE SET", clause, len(s.text)); at >= 0 {
			set = s.keyword("SET", at, len(s.text)) + len("SET")
		} else if at := s.keyword("ON DUPLICATE KEY UPDATE", clause, len(s.text)); at >= 0 {
			set = at + len("ON DUPLICATE KEY UPDATE")
		}
		if set < 0 {
			st.unknown = "conflict clause " + strings.Fields(rest)[0]
			return st
		}
		st.conflict = conflictUpdate
		stop := len(s.text)
		if at := s.keyword("WHERE", set, len(s.text)); at >= 0 {
			stop = at
		}
		for _, part := range s.split(set, stop) {
			eq := strings.IndexByte(s.text[part[0]:part[1]], '=')
			if eq < 0 {
				st.unknown = "conflict assignment"
				return st
			}
			st.assigned[captureColumnName(s.text[part[0]:part[0]+eq])] = captureValue{}
		}
	}
	return st
}

// targetFrom reads the target table and alias from the start of a row
// source ("t", "t AS a", "t a", "t AS a JOIN ...").
func targetFrom(from string) (table, alias string, multi bool, ok bool) {
	fields := strings.Fields(strings.ReplaceAll(from, ",", " , "))
	if len(fields) == 0 || !captureIdentifier.MatchString(fields[0]) {
		return "", "", false, false
	}
	table, alias = strings.ToLower(fields[0]), fields[0]
	rest := fields[1:]
	if len(rest) > 0 && strings.EqualFold(rest[0], "AS") {
		rest = rest[1:]
		if len(rest) == 0 {
			return "", "", false, false
		}
		alias, rest = rest[0], rest[1:]
	} else if len(rest) > 0 && captureIdentifier.MatchString(rest[0]) && !strings.EqualFold(rest[0], "JOIN") &&
		!strings.EqualFold(rest[0], "INNER") && !strings.EqualFold(rest[0], "LEFT") && !strings.EqualFold(rest[0], "CROSS") && !strings.EqualFold(rest[0], "STRAIGHT_JOIN") {
		alias, rest = rest[0], rest[1:]
	}
	return table, alias, len(rest) > 0, true
}

// filterTail splits a WHERE clause from a trailing ORDER BY/LIMIT.
func (s captureSQL) filterTail(st *captureStatement, args []any, whereAt, end int) bool {
	start := end
	if whereAt >= 0 {
		start = whereAt + len("WHERE")
	}
	tailAt := end
	for _, words := range []string{"ORDER BY", "LIMIT"} {
		if at := s.keyword(words, start, end); at >= 0 && at < tailAt {
			tailAt = at
		}
	}
	if whereAt >= 0 {
		st.where = strings.TrimSpace(s.text[start:tailAt])
	}
	st.tail = strings.TrimSpace(s.text[tailAt:end])
	st.limited = s.keyword("LIMIT", tailAt, end) >= 0
	whereArgs, ok := s.args(args, start, end)
	if !ok {
		return false
	}
	st.filterArgs = append(st.filterArgs, whereArgs...)
	return true
}

func (s captureSQL) parseUpdate(args []any, position int) captureStatement {
	st := captureStatement{verb: captureUpdate, assigned: map[string]captureValue{}}
	set := s.keyword("SET", position, len(s.text))
	if set < 0 {
		st.unknown = "update without SET"
		return st
	}
	st.from = strings.TrimSpace(s.text[position:set])
	table, alias, _, ok := targetFrom(st.from)
	if !ok {
		st.unknown = "update target"
		return st
	}
	st.table, st.alias = table, alias
	fromArgs, ok := s.args(args, position, set)
	if !ok {
		st.unknown = "update arguments"
		return st
	}
	st.filterArgs = fromArgs
	whereAt := s.keyword("WHERE", set, len(s.text))
	stop := len(s.text)
	if whereAt >= 0 {
		stop = whereAt
	} else {
		for _, words := range []string{"ORDER BY", "LIMIT"} {
			if at := s.keyword(words, set, len(s.text)); at >= 0 && at < stop {
				stop = at
			}
		}
	}
	for _, part := range s.split(set+len("SET"), stop) {
		eq := -1
		for i := part[0]; i < part[1]; i++ {
			if s.text[i] == '=' && s.depth[i] == 0 {
				eq = i
				break
			}
		}
		if eq < 0 {
			st.unknown = "update assignment"
			return st
		}
		st.assigned[captureColumnName(s.text[part[0]:eq])] = s.value(args, eq+1, part[1])
	}
	if !s.filterTail(&st, args, whereAt, len(s.text)) {
		st.unknown = "update arguments"
	}
	return st
}

func (s captureSQL) parseDelete(args []any, position int) captureStatement {
	st := captureStatement{verb: captureDelete}
	from := s.keyword("FROM", position, len(s.text))
	if from < 0 {
		st.unknown = "delete without FROM"
		return st
	}
	target := strings.TrimSpace(s.text[position:from])
	whereAt := s.keyword("WHERE", from, len(s.text))
	stop := len(s.text)
	if whereAt >= 0 {
		stop = whereAt
	}
	st.from = strings.TrimSpace(s.text[from+len("FROM") : stop])
	if s.keyword("USING", from, stop) >= 0 {
		st.unknown = "delete using"
		return st
	}
	table, alias, multi, ok := targetFrom(st.from)
	if !ok {
		st.unknown = "delete target"
		return st
	}
	st.table, st.alias = table, alias
	if target != "" {
		// DELETE a FROM t AS a JOIN ...: the named alias is the target.
		if !captureIdentifier.MatchString(target) || !strings.EqualFold(target, alias) {
			st.unknown = "multi-table delete target"
			return st
		}
	} else if multi {
		st.unknown = "delete source"
		return st
	}
	fromArgs, ok := s.args(args, from, stop)
	if !ok {
		st.unknown = "delete arguments"
		return st
	}
	st.filterArgs = fromArgs
	if whereAt < 0 {
		st.unknown = "delete without WHERE"
		return st
	}
	if !s.filterTail(&st, args, whereAt, len(s.text)) {
		st.unknown = "delete arguments"
	}
	return st
}
