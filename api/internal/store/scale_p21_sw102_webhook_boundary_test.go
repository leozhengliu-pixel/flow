package store

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func scaleP21SW102Text(node ast.Node) string {
	var text bytes.Buffer
	if err := format.Node(&text, token.NewFileSet(), node); err != nil {
		return ""
	}
	return text.String()
}

func scaleP21SW102References(node ast.Node, names map[string]bool) (map[string][]token.Pos, error) {
	references := map[string][]token.Pos{}
	parents := map[ast.Node]ast.Node{}
	stack := []ast.Node{}
	ast.Inspect(node, func(current ast.Node) bool {
		if current == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if len(stack) > 0 {
			parents[current] = stack[len(stack)-1]
		}
		stack = append(stack, current)
		return true
	})
	var failure error
	ast.Inspect(node, func(current ast.Node) bool {
		name := ""
		switch value := current.(type) {
		case *ast.SelectorExpr:
			name = value.Sel.Name
		case *ast.Ident:
			if selector, ok := parents[current].(*ast.SelectorExpr); ok && selector.Sel == value {
				return true
			}
			name = value.Name
		default:
			return true
		}
		if !names[name] {
			return true
		}
		selector, method := current.(*ast.SelectorExpr)
		if !method || scaleP21SW102Text(selector.X) != "s" {
			failure = fmt.Errorf("%s must retain its reviewed same-store method receiver", name)
			return true
		}
		call, direct := parents[current].(*ast.CallExpr)
		if !direct || call.Fun != current {
			failure = fmt.Errorf("%s escaped its reviewed direct-call boundary", name)
			return true
		}
		references[name] = append(references[name], call.Pos())
		return true
	})
	return references, failure
}

func scaleP21SW102StableReceiver(function *ast.FuncDecl) error {
	var changed bool
	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.AssignStmt:
			for _, target := range value.Lhs {
				changed = changed || scaleP21SW102Text(target) == "s" || scaleP21SW102Text(target) == "s.metadataResidency"
			}
		case *ast.ValueSpec:
			for _, name := range value.Names {
				changed = changed || name.Name == "s"
			}
		case *ast.Field:
			for _, name := range value.Names {
				changed = changed || name.Name == "s"
			}
		}
		return true
	})
	if changed {
		return fmt.Errorf("reviewed off-only boundary may not replace or shadow its guarded store")
	}
	return nil
}

func scaleP21SW102EarlyResidencyReturn(function *ast.FuncDecl) (*ast.IfStmt, error) {
	if function == nil || function.Body == nil || len(function.Body.List) == 0 {
		return nil, fmt.Errorf("missing early bounded dispatch")
	}
	guard, ok := function.Body.List[0].(*ast.IfStmt)
	if !ok || guard.Init != nil || guard.Else != nil ||
		scaleP21SW102Text(guard.Cond) != "s.metadataResidency != nil" || len(guard.Body.List) == 0 {
		return nil, fmt.Errorf("bounded residency dispatch must precede every legacy operation")
	}
	last, ok := guard.Body.List[len(guard.Body.List)-1].(*ast.ReturnStmt)
	if !ok || len(last.Results) != 1 ||
		scaleP21SW102Text(last.Results[0]) != "s.mutateResidentRecords(ctx, workspaceKey, eventType, payload, mutate)" {
		return nil, fmt.Errorf("bounded dispatch must return unconditionally through the resident writer")
	}
	var escape bool
	ast.Inspect(guard.Body, func(node ast.Node) bool {
		if branch, ok := node.(*ast.BranchStmt); ok && branch.Tok == token.GOTO {
			escape = true
		}
		return true
	})
	if escape {
		return nil, fmt.Errorf("bounded dispatch may not jump into the legacy tail")
	}
	return guard, nil
}

func TestScaleP21SW102WebhookNeededLegacyBoundary(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate webhook boundary source")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(source), "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	dispatch := "sqlite.go:dispatchMutation"
	want := map[string]map[string]int{
		"webhookNeeded": {
			"team_mutation.go:mutateTeamMetadata":                           1,
			"loop_contract_mutation.go:mutateLoopContract":                  2,
			"scoped_mutation.go:mutateScoped":                               1,
			"sqlite.go:mutateFull":                                          2,
			"scale_p21_legacy_webhook_needed.go:boundedLegacyWebhookNeeded": 1,
			"issue_record_mutation.go:UpdateIssueRecord":                    1,
			"scale_p16_record_mutation.go:mutateProjectRecordSelection":     1,
		},
		"mutateTeamMetadata":           {dispatch: 1},
		"mutateLoopContract":           {dispatch: 1},
		"mutateScoped":                 {dispatch: 1},
		"mutateProjectRecordSelection": {dispatch: 1},
	}
	names, got := map[string]bool{}, map[string]map[string]int{}
	for name := range want {
		names[name], got[name] = true, map[string]int{}
	}
	functions := map[string]*ast.FuncDecl{}
	positions := map[string]map[string][]token.Pos{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			var inspected ast.Node = declaration
			site := filepath.Base(path) + ":<declaration>"
			if function, ok := declaration.(*ast.FuncDecl); ok {
				site = filepath.Base(path) + ":" + function.Name.Name
				functions[site] = function
				if function.Body == nil {
					continue
				}
				inspected = function.Body
			}
			references, err := scaleP21SW102References(inspected, names)
			if err != nil {
				t.Errorf("%s: %v", site, err)
			}
			positions[site] = references
			for name, calls := range references {
				got[name][site] += len(calls)
			}
		}
	}
	for name, expected := range want {
		if !reflect.DeepEqual(got[name], expected) {
			t.Errorf("%s production callers=%v, want off-only callers %v", name, got[name], expected)
		}
	}
	checked := map[string]bool{dispatch: true}
	for site := range want["webhookNeeded"] {
		checked[site] = true
	}
	for site := range checked {
		function := functions[site]
		if function == nil || function.Body == nil {
			t.Fatalf("reviewed boundary disappeared: %s", site)
		}
		if err := scaleP21SW102StableReceiver(function); err != nil {
			t.Errorf("%s: %v", site, err)
		}
	}
	enabled := functions["scale_p21_scoped_access.go:BoundedMetadataEnabled"]
	if enabled == nil || enabled.Body == nil || len(enabled.Body.List) != 1 ||
		scaleP21SW102Text(enabled.Body.List[0]) != "return s.metadataResidency != nil" {
		t.Fatal("bounded-mode representation no longer matches residency dispatch")
	}
	for _, site := range []string{dispatch, "sqlite.go:mutateFull"} {
		guard, err := scaleP21SW102EarlyResidencyReturn(functions[site])
		if err != nil {
			t.Fatalf("%s: %v", site, err)
		}
		for _, calls := range positions[site] {
			for _, call := range calls {
				if call <= guard.End() {
					t.Errorf("%s: legacy helper is reachable before bounded dispatch returns", site)
				}
			}
		}
	}
	wrapper := functions["scale_p21_legacy_webhook_needed.go:boundedLegacyWebhookNeeded"]
	if wrapper == nil || wrapper.Body == nil || len(wrapper.Body.List) == 0 {
		t.Fatal("missing bounded webhook predicate wrapper")
	}
	off, ok := wrapper.Body.List[0].(*ast.IfStmt)
	if !ok || off.Init != nil || off.Else != nil || scaleP21SW102Text(off.Cond) != "!s.BoundedMetadataEnabled()" ||
		len(off.Body.List) != 1 || scaleP21SW102Text(off.Body.List[0]) != "return s.webhookNeeded(ctx, workspace), nil" {
		t.Fatal("bounded webhook predicate must keep the literal accessor only in its first off-mode return")
	}
	for _, position := range positions["scale_p21_legacy_webhook_needed.go:boundedLegacyWebhookNeeded"]["webhookNeeded"] {
		if position <= off.Body.Pos() || position >= off.Body.End() {
			t.Fatal("legacy webhook predicate escaped its off-mode return")
		}
	}
	issue := functions["issue_record_mutation.go:UpdateIssueRecord"]
	if issue == nil || issue.Body == nil {
		t.Fatal("missing UpdateIssueRecord boundary")
	}
	guarded := false
	ast.Inspect(issue.Body, func(node ast.Node) bool {
		branch, ok := node.(*ast.IfStmt)
		if !ok || branch.Init != nil || scaleP21SW102Text(branch.Cond) != "s.BoundedMetadataEnabled()" {
			return true
		}
		otherwise, ok := branch.Else.(*ast.BlockStmt)
		if !ok {
			return true
		}
		for _, position := range positions["issue_record_mutation.go:UpdateIssueRecord"]["webhookNeeded"] {
			guarded = guarded || otherwise.Pos() < position && position < otherwise.End()
		}
		return true
	})
	if !guarded {
		t.Fatal("UpdateIssueRecord must keep webhookNeeded exclusively in its same-store flag-off else branch")
	}
}
