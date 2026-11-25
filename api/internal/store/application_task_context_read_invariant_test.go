package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// Task contexts omit unrelated discussion/delivery/import bodies. Their normal
// producers serialize typed values; raw SQL corruption is not a write API.
func TestScaleP21ApplicationTaskContextReadContentProducerGuard(t *testing.T) {
	for _, fixture := range []struct {
		file, function string
	}{
		{"content_records.go", "writeContentRecord"},
		{"content_records.go", "contentRecordRowFor"},
		{"content_records.go", "syncContentRecordsFrom"},
		{"content_record_migration.go", "migrateContentIndexes"},
	} {
		parsed, err := parser.ParseFile(token.NewFileSet(), fixture.file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		found, marshals := false, false
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Name.Name != fixture.function {
				continue
			}
			found = true
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if ok {
					pkg, ok := selector.X.(*ast.Ident)
					marshals = marshals || ok && pkg.Name == "json" && selector.Sel.Name == "Marshal"
				}
				return true
			})
		}
		if !found || !marshals {
			t.Fatal("task content producer serialization requires review", fixture)
		}
	}
	// The database patch producer changes only typed notification timestamps.
	parsed, err := parser.ParseFile(token.NewFileSet(), "notification_batch.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	ast.Inspect(parsed, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if ok && literal.Kind == token.STRING {
			for _, field := range []string{"readAt", "archivedAt", "deletedAt", "snoozedUntil"} {
				if strings.Trim(literal.Value, `"`) == field {
					found[field] = true
				}
			}
		}
		return true
	})
	if len(found) != 4 {
		t.Fatal("notification batch typed timestamp fields changed", found)
	}
}
