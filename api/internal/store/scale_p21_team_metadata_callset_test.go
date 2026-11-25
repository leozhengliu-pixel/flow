package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// The events metadataTeamMutation routes to mutateTeamMetadata.
var scaleP21TeamMetadataEvents = map[string]bool{
	"team.created": true, "team.updated": true, "team.settings_updated": true, "team.deleted": true,
	"alm.org_teams_imported": true, "alm.users_imported": true, "alm.projects_imported": true,
}

type scaleP21TeamMetadataUse struct {
	function string
	// call names the function or method the event literal is passed to; empty
	// for any other use (comparison, table, list).
	call string
	// bounded: the call sits in the else branch of the function's own
	// BoundedMetadataEnabled() check, whose then branch does not mutate.
	bounded bool
}

func scaleP21TeamMetadataUses(file *ast.File) []scaleP21TeamMetadataUse {
	var uses []scaleP21TeamMetadataUse
	visit := func(function string, root ast.Node) {
		var stack []ast.Node
		ast.Inspect(root, func(node ast.Node) bool {
			if node == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			stack = append(stack, node)
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil || !scaleP21TeamMetadataEvents[value] {
				return true
			}
			use := scaleP21TeamMetadataUse{function: function}
			if len(stack) >= 2 {
				if call, ok := stack[len(stack)-2].(*ast.CallExpr); ok && scaleP21TeamMetadataArgument(call.Args, literal) {
					switch callee := call.Fun.(type) {
					case *ast.SelectorExpr:
						use.call = callee.Sel.Name
					case *ast.Ident:
						use.call = callee.Name
					}
				}
			}
			// The nearest enclosing if whose else holds the literal.
			for index := len(stack) - 2; index >= 1; index-- {
				branch, ok := stack[index].(*ast.IfStmt)
				if !ok || branch.Else == nil || stack[index+1] != branch.Else {
					continue
				}
				condition, ok := branch.Cond.(*ast.CallExpr)
				if !ok {
					break
				}
				selector, ok := condition.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "BoundedMetadataEnabled" || len(condition.Args) != 0 {
					break
				}
				mutates := false
				ast.Inspect(branch.Body, func(node ast.Node) bool {
					if selector, ok := node.(*ast.SelectorExpr); ok && strings.HasPrefix(selector.Sel.Name, "Mutate") {
						mutates = true
					}
					return true
				})
				use.bounded = !mutates
				break
			}
			uses = append(uses, use)
			return true
		})
	}
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok {
			visit(function.Name.Name, function)
		} else {
			visit("", declaration)
		}
	}
	return uses
}

func scaleP21TeamMetadataArgument(items []ast.Expr, target ast.Expr) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

// TestScaleP21TeamMetadataCallset proves mutateTeamMetadata (the original
// full-snapshot team writer) is unreachable from a bounded store except
// through a cold facade: dispatchMutation's bounded branch returns before it,
// and every production producer of the events it serves is either createTeam
// on its flag-off branch (bounded createTeam uses CreateTeamRecords) or a
// registered COLD consumer, which only a cold facade (with the bounded
// representation disabled) runs.
func TestScaleP21TeamMetadataCallset(t *testing.T) {
	// 1. The only reference is dispatchMutation's call, after the bounded
	// dispatch block that always returns.
	references := 0
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range sources {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(function, func(node ast.Node) bool {
				if selector, ok := node.(*ast.SelectorExpr); ok && selector.Sel.Name == "mutateTeamMetadata" {
					references++
					if path != "sqlite.go" || function.Name.Name != "dispatchMutation" {
						t.Errorf("mutateTeamMetadata referenced from %s:%s", path, function.Name.Name)
					}
				}
				return true
			})
			if path != "sqlite.go" || function.Name.Name != "dispatchMutation" {
				continue
			}
			boundedReturn, teamCall := -1, -1
			for index, statement := range function.Body.List {
				branch, ok := statement.(*ast.IfStmt)
				if !ok {
					continue
				}
				if binary, ok := branch.Cond.(*ast.BinaryExpr); ok && binary.Op == token.NEQ {
					if selector, ok := binary.X.(*ast.SelectorExpr); ok && selector.Sel.Name == "metadataResidency" {
						if receiver, ok := selector.X.(*ast.Ident); ok && receiver.Name == "s" && branch.Else == nil && branch.Init == nil {
							if last := branch.Body.List[len(branch.Body.List)-1]; reflect.TypeOf(last) == reflect.TypeFor[*ast.ReturnStmt]() && boundedReturn < 0 {
								boundedReturn = index
							}
						}
					}
				}
				ast.Inspect(branch, func(node ast.Node) bool {
					if selector, ok := node.(*ast.SelectorExpr); ok && selector.Sel.Name == "mutateTeamMetadata" {
						teamCall = index
					}
					return true
				})
			}
			if boundedReturn < 0 || teamCall <= boundedReturn {
				t.Errorf("dispatchMutation: bounded return at %d, team writer at %d", boundedReturn, teamCall)
			}
		}
	}
	if references != 1 {
		t.Fatalf("mutateTeamMetadata has %d references, want dispatchMutation's one", references)
	}
	// 2. Every production use of its events.
	expected := map[string]int{
		// Producers (event passed to a mutation entry point).
		"cmd/server/main.go:createTeam:MutateWorkspace:bounded":                             1,
		"cmd/server/main.go:updateTeam:MutateWorkspace":                                     1,
		"cmd/server/main.go:purgeTeam:MutateWorkspace":                                      1,
		"cmd/server/notifications_workflow.go:updateStructuredTeamSettings:MutateWorkspace": 1,
		// Consumers of published events.
		"cmd/server/realtime_event_policy.go:":                                1,
		"cmd/server/application_webhooks.go:applicationWebhookLifecycleEvent": 4,
		"internal/store/scale_p21_team_create_write.go:CreateTeamRecords":     1,
		"internal/store/metadata_mutation.go:metadataOnlyMutation":            3,
		"internal/store/hot_cache.go:hotCacheTouch":                           4,
		"internal/store/sqlite.go:persistWorkspaceTx":                         1,
	}
	cold := map[string]string{
		"cmd/server/main.go:updateTeam":                                     "server/main.go:updateTeam",
		"cmd/server/main.go:purgeTeam":                                      "server/main.go:purgeTeam",
		"cmd/server/notifications_workflow.go:updateStructuredTeamSettings": "server/notifications_workflow.go:updateStructuredTeamSettings",
	}
	actual := map[string]int{}
	root := filepath.Join("..", "..")
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); name == "node_modules" || name == "testdata" || strings.HasPrefix(name, ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == "internal/store/team_mutation.go" {
			// The writer itself.
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
		if err != nil {
			return err
		}
		for _, use := range scaleP21TeamMetadataUses(file) {
			key := relative + ":" + use.function
			if use.call != "" && (strings.HasPrefix(use.call, "Mutate") || strings.Contains(use.call, "dispatch") || strings.Contains(strings.ToLower(use.call), "cold")) {
				key += ":" + use.call
				if use.bounded {
					key += ":bounded"
				} else if consumer, ok := cold[relative+":"+use.function]; ok {
					if err := validateColdMetadataDispatch(consumer); err != nil {
						t.Errorf("%s produces a team metadata event but is not a registered cold consumer: %v", key, err)
					}
				} else {
					t.Errorf("%s produces a team metadata event on a bounded path", key)
				}
			}
			actual[key]++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("team metadata event callset changed:\nactual=%v\nexpected=%v", actual, expected)
	}
}

func TestScaleP21TeamMetadataCallsetRejectsFalseProofs(t *testing.T) {
	for _, fixture := range []struct {
		name, body string
		bounded    bool
	}{
		{"else", `if s.store.BoundedMetadataEnabled() { err = s.createTeamRecords() } else { err = s.store.MutateWorkspace(ctx, key, "team.created", id, input, create) }`, true},
		{"unguarded", `err = s.store.MutateWorkspace(ctx, key, "team.created", id, input, create)`, false},
		{"then-arm", `if s.store.BoundedMetadataEnabled() { err = s.store.MutateWorkspace(ctx, key, "team.created", id, input, create) }`, false},
		{"then-arm-mutates", `if s.store.BoundedMetadataEnabled() { err = s.store.MutateWorkspaceScoped(ctx) } else { err = s.store.MutateWorkspace(ctx, key, "team.created", id, input, create) }`, false},
		{"other-condition", `if enabled { err = nil } else { err = s.store.MutateWorkspace(ctx, key, "team.created", id, input, create) }`, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "callset.go", "package main; func (s *server) check() { "+fixture.body+" }", 0)
			if err != nil {
				t.Fatal(err)
			}
			uses := scaleP21TeamMetadataUses(file)
			if len(uses) != 1 || uses[0].call == "" || uses[0].bounded != fixture.bounded {
				t.Fatalf("uses=%+v want bounded=%t", uses, fixture.bounded)
			}
		})
	}
}
