package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A direct read from an omitted collection is not an error in Go: it sees
// nil. Keep that regression visible in the audited production callbacks too.
func TestAuditedMetadataCallbackReads(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "cmd", "server", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	audited := map[string]bool{}
	for _, test := range metadataEventCases() {
		audited[test.event] = true
	}
	forbidden := map[string]bool{"Issues": true, "Comments": true, "Activities": true, "Notifications": true, "NotificationDeliveries": true}
	calls, literals, dynamic, checked := 0, map[string]bool{}, 0, 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			// The P21 scoped writers take the event and the callback in the same positions as the legacy ones.
			switch selector.Sel.Name {
			case "MutateWorkspace", "MutateWorkspaceWithAggregate", "MutateWorkspaceScoped", "MutateWorkspaceScopedWithAggregate",
				"MutateWorkspaceAppending", "MutateWorkspaceAppendingWithAggregate", "MutateWorkspaceResolved", "MutateWorkspaceResolvedWithAggregate":
			default:
				ok = false
			}
			if !ok || len(call.Args) < 4 {
				return true
			}
			calls++
			literal, ok := call.Args[2].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				dynamic++
				return true
			}
			event, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			literals[event] = true
			if !audited[event] {
				return true
			}
			callback, ok := call.Args[len(call.Args)-1].(*ast.FuncLit)
			if !ok || len(callback.Type.Params.List) != 1 || len(callback.Type.Params.List[0].Names) != 1 {
				t.Fatalf("%s: unresolved audited callback", path)
			}
			checked++
			parameter := callback.Type.Params.List[0].Names[0].Name
			ast.Inspect(callback.Body, func(node ast.Node) bool {
				field, ok := node.(*ast.SelectorExpr)
				if !ok || !forbidden[field.Sel.Name] {
					return true
				}
				owner, ok := field.X.(*ast.Ident)
				if ok && owner.Name == parameter {
					t.Errorf("%s: %s reads omitted %s; use an issue/content scope", path, event, field.Sel.Name)
				}
				return true
			})
			return true
		})
	}
	if checked < 15 {
		t.Fatalf("only %d production callbacks checked", checked)
	}
	t.Logf("METADATA_INVENTORY calls=%d distinct_literal_events=%d dynamic_calls=%d audited_literal_callbacks=%d", calls, len(literals), dynamic, checked)
}
