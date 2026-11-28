package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// Task contexts can omit unrelated discussion/delivery/import bodies because
// their producers encode typed values. The reviewed metadata encoders match
// json.Marshal, including custom codecs. Point and sync lookups derive identity
// from those bytes, and all three writers use the existing StripJSONNUL pass
// to derive the stored JSON and index columns. See
// TestScaleApplicationTaskContextReadContentProducerDifferential.
func TestScaleApplicationTaskContextReadContentProducerGuard(t *testing.T) {
	for _, fixture := range []struct {
		file, function, encoder string
	}{
		{"content_records.go", "writeContentRecord", "encodeOwnedMetadataValue"},
		{"content_records.go", "contentRecordRowFor", "marshalMetadataJSON"},
		{"content_records.go", "syncContentRecordsFrom", "marshalMetadataJSON"},
		{"content_record_migration.go", "migrateContentIndexes", "json.Marshal"},
	} {
		parsed, err := parser.ParseFile(token.NewFileSet(), fixture.file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		found, encodes := false, false
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
				switch callee := call.Fun.(type) {
				case *ast.Ident:
					encodes = encodes || callee.Name == fixture.encoder
				case *ast.SelectorExpr:
					pkg, ok := callee.X.(*ast.Ident)
					encodes = encodes || ok && pkg.Name+"."+callee.Sel.Name == fixture.encoder
				}
				return true
			})
		}
		if !found || !encodes {
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
