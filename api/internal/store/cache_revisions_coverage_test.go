package store

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// metadataSQLStatement is a SQL statement written in the store's source: its
// text with every non-literal part (a column expression, a bound list) as
// sqlHole, where it is, and the receiver of the ExecContext that runs it
// (empty when it is not passed to one directly).
type metadataSQLStatement struct {
	query, position, receiver string
}

type sqlTableLoop struct {
	*ast.RangeStmt
	tables []string
}

const sqlHole = "HOLE"

// metadataSQLStatements reads the store package's non-test sources and
// returns every string expression that starts a SQL write statement.
func metadataSQLStatements(t *testing.T) []metadataSQLStatement {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	set := token.NewFileSet()
	statements := []metadataSQLStatement{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(set, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		// The receivers of the ExecContext calls each string expression is passed to.
		receivers := map[ast.Expr]string{}
		tableLoops := []sqlTableLoop{}
		ast.Inspect(file, func(node ast.Node) bool {
			if loop, ok := node.(*ast.RangeStmt); ok {
				tables := []string{}
				if source, ok := loop.X.(*ast.Ident); ok && source.Name == "workspaceRecordTables" {
					tables = workspaceRecordTables
				} else if source, ok := loop.X.(*ast.CompositeLit); ok {
					for _, item := range source.Elts {
						if value, ok := item.(*ast.BasicLit); ok && value.Kind == token.STRING {
							table, err := strconv.Unquote(value.Value)
							if err != nil {
								t.Fatal(err)
							}
							tables = append(tables, table)
						}
					}
				}
				if len(tables) > 0 {
					tableLoops = append(tableLoops, sqlTableLoop{loop, tables})
				}
			}
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "ExecContext" || len(call.Args) < 2 {
				return true
			}
			var receiver bytes.Buffer
			_ = printer.Fprint(&receiver, set, selector.X)
			receivers[call.Args[1]] = receiver.String()
			return true
		})
		var visit func(node ast.Node) bool
		visit = func(node ast.Node) bool {
			expression, ok := node.(ast.Expr)
			if !ok {
				return true
			}
			text, literal := sqlText(expression)
			if !literal {
				return true
			}
			if target := sqlWriteTarget.FindStringSubmatchIndex(text); target != nil {
				statement := metadataSQLStatement{query: text, position: set.Position(expression.Pos()).String(), receiver: receivers[expression]}
				if strings.EqualFold(text[target[2]:target[3]], sqlHole) {
					// Expand literal table loops and the workspace catalog's
					// generic rename/delete builders, not just literal SQL.
					expanded := false
					for _, loop := range tableLoops {
						if loop.Pos() <= expression.Pos() && expression.End() <= loop.End() {
							expanded = true
							for _, table := range loop.tables {
								statement.query = text[:target[2]] + table + text[target[3]:]
								statements = append(statements, statement)
							}
						}
					}
					if !expanded && !nonMetadataTableBuilders[enclosingFunction(t, statement.position)] {
						t.Errorf("%s: classify the dynamic table builder %q so metadata writes cannot escape this scan", statement.position, text)
					}
				} else {
					statements = append(statements, statement)
				}
			}
			// The parts of a concatenation are not statements of their own.
			return false
		}
		ast.Inspect(file, visit)
	}
	return statements
}

// sqlText flattens a string literal or a concatenation holding one: literal
// parts as written, any other operand as sqlHole.
func sqlText(expression ast.Expr) (string, bool) {
	switch node := expression.(type) {
	case *ast.BasicLit:
		if node.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(node.Value)
		return value, err == nil
	case *ast.ParenExpr:
		return sqlText(node.X)
	case *ast.BinaryExpr:
		if node.Op != token.ADD {
			return "", false
		}
		left, leftLiteral := sqlText(node.X)
		right, rightLiteral := sqlText(node.Y)
		if !leftLiteral && !rightLiteral {
			return "", false
		}
		if !leftLiteral {
			left = sqlHole
		}
		if !rightLiteral {
			right = sqlHole
		}
		return left + right, true
	}
	return "", false
}

// sqlWriteTarget matches a write statement and names the table it writes.
var sqlWriteTarget = regexp.MustCompile(`(?is)^\s*(?:INSERT\s+(?:IGNORE\s+)?INTO|REPLACE\s+INTO|UPSERT\s+INTO|MERGE\s+INTO|UPDATE|DELETE\s+FROM)\s+([a-z_]+)`)

// metadataTables are the tables whose writes move the metadata revision.
var metadataTables = map[string]bool{"workspace_metadata_records": true, "workspace_states": true}

// Audited dynamic-table builders whose callers only supply non-metadata
// tables. New builders must either be expanded above or audited here.
var nonMetadataTableBuilders = map[string]bool{
	"PurgeChatHistory":           true,
	"purgeChatThreads":           true,
	"renameChatPayloadWorkspace": true,
	"renameChatWorkspace":        true,
	"deleteChatWorkspace":        true,
	"syncIssueIndexRows":         true,
	"ExchangeOAuthGrant":         true,
	"deleteRowsByIDs":            true,
}

// expectedWorkspaceArg is the index of the bound value naming the workspace a
// metadata write statement writes, found independently of
// metadataWriteWorkspace: an insert's workspace_key column and its value, or
// the workspace_key=? assignment/condition of an update or a delete. A
// workspace rename writes the destination key.
func expectedWorkspaceArg(query string) (int, error) {
	insert := regexp.MustCompile(`(?is)^\s*INSERT\s+(?:IGNORE\s+)?INTO\s+[a-z_]+\s*\(([^)]*)\)\s*VALUES\s*\(([^)]*)\)`).FindStringSubmatch(query)
	if insert != nil {
		columns, values := strings.Split(insert[1], ","), strings.Split(insert[2], ",")
		if len(columns) != len(values) {
			return -1, fmt.Errorf("%d columns, %d values", len(columns), len(values))
		}
		before := strings.Count(query[:strings.Index(query, insert[2])], "?")
		for index, column := range columns {
			if strings.TrimSpace(column) == "workspace_key" {
				if strings.TrimSpace(values[index]) != "?" {
					return -1, fmt.Errorf("workspace_key is not bound")
				}
				return before + strings.Count(strings.Join(values[:index], ","), "?"), nil
			}
		}
		return -1, fmt.Errorf("no workspace_key column")
	}
	if regexp.MustCompile(`(?is)^\s*INSERT\b`).MatchString(query) {
		return -1, fmt.Errorf("an insert without a VALUES list")
	}
	where := regexp.MustCompile(`(?is)\bWHERE\b`).FindStringIndex(query)
	if where == nil {
		return -1, fmt.Errorf("no WHERE clause")
	}
	if assignment := regexp.MustCompile(`(?i)\bworkspace_key\s*=\s*\?`).FindStringIndex(query[:where[0]]); assignment != nil {
		return strings.Count(query[:assignment[1]], "?") - 1, nil
	}
	condition := regexp.MustCompile(`(?i)(?:^|[^a-z_.])workspace_key\s*=\s*\?`).FindStringIndex(query[where[1]:])
	if condition == nil {
		return -1, fmt.Errorf("no workspace_key=? condition")
	}
	return strings.Count(query[:where[1]+condition[1]], "?") - 1, nil
}

// nonTransactionalMetadataWrites run outside a transaction on purpose, so
// they move no revision: an API key's or authorization's last use (at most
// once a minute per key, display only; authentication reads the database).
var nonTransactionalMetadataWrites = map[string]bool{"writeAPIKeyUse": true}

// Every statement in the store that writes the workspace document or a
// metadata record is one metadataWriteWorkspace recognises, naming the
// workspace the statement binds, and runs through a transaction (sqlTx
// ExecContext queues the revision): no write path leaves the metadata
// revision where it was, which would let other servers serve the copy cached
// before it.
func TestEveryMetadataWriteStatementMovesTheMetadataRevision(t *testing.T) {
	statements := metadataSQLStatements(t)
	writes := 0
	for _, statement := range statements {
		target := sqlWriteTarget.FindStringSubmatch(statement.query)
		if !metadataTables[strings.ToLower(target[1])] {
			if workspace, ok := metadataWriteWorkspace(statement.query, []any{"a", "b", "c", "d", "e", "f"}); ok {
				t.Errorf("%s: %q is taken for a metadata write of %q", statement.position, statement.query, workspace)
			}
			continue
		}
		writes++
		index, err := expectedWorkspaceArg(statement.query)
		if err != nil {
			t.Errorf("%s: %q does not bind the workspace it writes: %v", statement.position, statement.query, err)
			continue
		}
		args := make([]any, strings.Count(statement.query, "?"))
		for position := range args {
			args[position] = fmt.Sprintf("arg%d", position)
		}
		workspace, ok := metadataWriteWorkspace(statement.query, args)
		if !ok || workspace != args[index] {
			t.Errorf("%s: %q: metadataWriteWorkspace = %q, %v; want %q", statement.position, statement.query, workspace, ok, args[index])
		}
		switch receiver := statement.receiver; {
		case receiver == "":
		case strings.HasSuffix(receiver, ".db") || strings.HasSuffix(receiver, ".DB") || strings.HasSuffix(receiver, ".Tx"):
			if !nonTransactionalMetadataWrites[enclosingFunction(t, statement.position)] {
				t.Errorf("%s: %q runs on %s, outside a transaction: it moves no metadata revision", statement.position, statement.query, receiver)
			}
		}
	}
	// The scan found the writers it is meant to check.
	if writes < 30 {
		t.Fatalf("found %d metadata write statements; the source scan is broken", writes)
	}
	t.Logf("checked %d metadata write statements and builder shapes", writes)
}

// enclosingFunction names the function declared around a source position.
func enclosingFunction(t *testing.T, position string) string {
	t.Helper()
	parts := strings.Split(position, ":")
	line, _ := strconv.Atoi(parts[1])
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, parts[0], nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok && set.Position(function.Pos()).Line <= line && line <= set.Position(function.End()).Line {
			return function.Name.Name
		}
	}
	return ""
}
