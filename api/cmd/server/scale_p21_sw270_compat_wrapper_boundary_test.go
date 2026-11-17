package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/build"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The SW270 guard closes the server compatibility wrappers by reachability,
// not by names or comments. It parses every non-test file of this package,
// resolves each direct call and function-value reference, and decides for
// each reference whether a request with FLOW_BOUNDED_METADATA on can execute
// it. A reference is flag-off-only when it is
//   - inside the then-branch of an if whose condition is false whenever the
//     flag is on (for example !s.store.BoundedMetadataEnabled() && ...),
//   - inside the else-branch of an if whose condition is true whenever the
//     flag is on (for example s.store.BoundedMetadataEnabled() || ...), or
//   - after an earlier statement of an enclosing block of the form
//     if <true whenever the flag is on> { ...; return }.
// A call to a helper whose first statement is
// if s.store.BoundedMetadataEnabled() { ...; return true } counts as a
// condition that is true whenever the flag is on (serveDocumentRecordList).
// Function literals inherit the gating of the place they are created.
//
// References are resolved with go/types over this package (imports are empty
// stand-ins): a method call or method value on a concrete package type names
// exactly that method; one on a package interface, or on a receiver whose
// type is imported and so unresolved, conservatively names every package
// method of that name. Dispatch by imported code into package methods through
// imported interfaces (an http.ResponseWriter wrapper's WriteHeader) is not
// followed; the place a package method escapes as a value is.
//
// For callers allowed to reach a wrapper with the flag on (COLD or relay),
// hotReach walks every flag-on reference backwards and fails on any
// HOT-classified inventory consumer it meets. It stops only at route
// registrations (a handler's temperature is its own inventory
// classification) and at the pinned scaleP21SW270Boundaries.

type scaleP21SW270Func struct {
	key, file string
	decl      *ast.FuncDecl
}

type scaleP21SW270Ref struct {
	from, to, file string
	line           int
	kind           string // call, value or route
	offOnly        bool
	async          bool // inside a go statement
}

type scaleP21SW270Graph struct {
	fset      *token.FileSet
	info      *types.Info
	local     *types.Package
	funcs     map[string]*scaleP21SW270Func
	byName    map[string][]string
	refs      []scaleP21SW270Ref
	into      map[string][]int
	pkgLevel  map[string][]string // function key -> package-level declarations referencing it
	onTrue    map[string]bool
	onTrueRun map[string]bool
	selectors map[string][]scaleP21SW270Ref // selector name (any package) -> uses
}

func scaleP21SW270Source(set *token.FileSet, node ast.Node) string {
	var text bytes.Buffer
	if err := format.Node(&text, set, node); err != nil {
		return fmt.Sprintf("<%T>", node)
	}
	return text.String()
}

// scaleP21SW270Importer resolves every import to an empty package. Only this
// package's own functions and methods matter to the guard, and they type-check
// exactly; expressions of imported types become invalid and are handled
// conservatively in collect.
type scaleP21SW270Importer struct{}

func (scaleP21SW270Importer) Import(path string) (*types.Package, error) {
	imported := types.NewPackage(path, path[strings.LastIndex(path, "/")+1:])
	imported.MarkComplete()
	return imported, nil
}

// key names a package function as name and a method as Type.name. A method
// of a package interface has no body and is not a key.
func (g *scaleP21SW270Graph) key(fn *types.Func) string {
	if fn == nil || fn.Pkg() != g.local {
		return ""
	}
	signature, ok := fn.Type().(*types.Signature)
	if !ok {
		return ""
	}
	receiver := signature.Recv()
	if receiver == nil {
		return fn.Name()
	}
	kind := receiver.Type()
	if pointer, ok := kind.(*types.Pointer); ok {
		kind = pointer.Elem()
	}
	if named, ok := kind.(*types.Named); ok && !types.IsInterface(named) {
		return named.Obj().Name() + "." + fn.Name()
	}
	return ""
}

func scaleP21SW270Load(t *testing.T, dir string) *scaleP21SW270Graph {
	t.Helper()
	graph := &scaleP21SW270Graph{
		fset: token.NewFileSet(), funcs: map[string]*scaleP21SW270Func{}, byName: map[string][]string{},
		into: map[string][]int{}, pkgLevel: map[string][]string{}, onTrue: map[string]bool{}, onTrueRun: map[string]bool{},
		selectors: map[string][]scaleP21SW270Ref{},
		info:      &types.Info{Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}, Types: map[ast.Expr]types.TypeAndValue{}},
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		if match, err := build.Default.MatchFile(dir, filepath.Base(path)); err != nil || !match {
			continue
		}
		file, err := parser.ParseFile(graph.fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	config := types.Config{Importer: scaleP21SW270Importer{}, Error: func(error) {}}
	graph.local, _ = config.Check("main", graph.fset, files, graph.info)
	for _, file := range files {
		name := filepath.Base(graph.fset.Position(file.Pos()).Filename)
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			object, _ := graph.info.Defs[fn.Name].(*types.Func)
			key := graph.key(object)
			if fn.Name.Name == "init" && fn.Recv == nil {
				key = "init@" + name
			}
			if key == "" || graph.funcs[key] != nil {
				t.Fatalf("function %s in %s has no unique key %q", fn.Name.Name, name, key)
			}
			graph.funcs[key] = &scaleP21SW270Func{key: key, file: name, decl: fn}
			if fn.Recv != nil {
				graph.byName[fn.Name.Name] = append(graph.byName[fn.Name.Name], key)
			}
		}
	}
	for _, file := range files {
		name := filepath.Base(graph.fset.Position(file.Pos()).Filename)
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				// Package-level variables could capture a function outside
				// any request; every such capture is reported.
				ast.Inspect(decl, func(node ast.Node) bool {
					if ident, ok := node.(*ast.Ident); ok {
						if object, ok := graph.info.Uses[ident].(*types.Func); ok && graph.key(object) != "" {
							graph.pkgLevel[graph.key(object)] = append(graph.pkgLevel[graph.key(object)], name)
						}
					}
					return true
				})
				continue
			}
			key := graph.key(graph.info.Defs[fn.Name].(*types.Func))
			if fn.Name.Name == "init" && fn.Recv == nil {
				key = "init@" + name
			}
			graph.collect(graph.funcs[key])
		}
	}
	return graph
}

func scaleP21SW270RouteCall(node ast.Node) bool {
	call, ok := node.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && (selector.Sel.Name == "HandleFunc" || selector.Sel.Name == "Handle") && len(call.Args) == 2
}

// targets resolves one identifier or selector to the package functions it
// names. Calls through a package interface reach every method of that name;
// a selector whose receiver has an imported (unresolved) type conservatively
// reaches every package method of that name too.
func (g *scaleP21SW270Graph) targets(node ast.Node, parent ast.Node) []string {
	switch expression := node.(type) {
	case *ast.SelectorExpr:
		if selection := g.info.Selections[expression]; selection != nil {
			if selection.Kind() == types.FieldVal {
				return nil
			}
			object, _ := selection.Obj().(*types.Func)
			if object == nil || object.Pkg() != g.local {
				return nil
			}
			if types.IsInterface(selection.Recv()) {
				return g.byName[expression.Sel.Name]
			}
			if key := g.key(object); key != "" {
				return []string{key}
			}
			return nil
		}
		if ident, ok := expression.X.(*ast.Ident); ok {
			if _, imported := g.info.Uses[ident].(*types.PkgName); imported {
				return nil
			}
		}
		if typed, ok := g.info.Types[expression.X]; ok && typed.Type != nil && typed.Type != types.Typ[types.Invalid] {
			return nil // a resolved non-package type: its methods are not ours
		}
		return g.byName[expression.Sel.Name]
	case *ast.Ident:
		if selector, ok := parent.(*ast.SelectorExpr); ok && selector.Sel == expression {
			return nil
		}
		object, ok := g.info.Uses[expression].(*types.Func)
		if !ok {
			return nil
		}
		if key := g.key(object); key != "" && !strings.Contains(key, ".") {
			return []string{key}
		}
	}
	return nil
}

// collect records every reference to a package function or method made by
// owner, with its route/call/value kind and its flag-on reachability.
func (g *scaleP21SW270Graph) collect(owner *scaleP21SW270Func) {
	var ancestors []ast.Node
	ast.Inspect(owner.decl.Body, func(node ast.Node) bool {
		if node == nil {
			ancestors = ancestors[:len(ancestors)-1]
			return false
		}
		ancestors = append(ancestors, node)
		if len(ancestors) < 2 {
			return true
		}
		if selector, ok := node.(*ast.SelectorExpr); ok {
			kind := "value"
			if call, ok := ancestors[len(ancestors)-2].(*ast.CallExpr); ok && call.Fun == node {
				kind = "call"
			}
			g.selectors[selector.Sel.Name] = append(g.selectors[selector.Sel.Name], scaleP21SW270Ref{
				from: owner.key, file: owner.file, line: g.fset.Position(node.Pos()).Line, kind: kind, offOnly: g.offOnly(ancestors),
			})
		}
		targets := g.targets(node, ancestors[len(ancestors)-2])
		if len(targets) == 0 {
			return true
		}
		kind := "value"
		if call, ok := ancestors[len(ancestors)-2].(*ast.CallExpr); ok && call.Fun == node {
			kind = "call"
		}
		for index, ancestor := range ancestors[:len(ancestors)-1] {
			if scaleP21SW270RouteCall(ancestor) && ancestors[index+1] != ancestor.(*ast.CallExpr).Fun {
				kind = "route"
			}
		}
		offOnly := g.offOnly(ancestors)
		async := slices.ContainsFunc(ancestors, func(node ast.Node) bool { _, ok := node.(*ast.GoStmt); return ok })
		position := g.fset.Position(node.Pos())
		for _, target := range targets {
			g.into[target] = append(g.into[target], len(g.refs))
			g.refs = append(g.refs, scaleP21SW270Ref{from: owner.key, to: target, file: owner.file, line: position.Line, kind: kind, offOnly: offOnly, async: async})
		}
		return true
	})
}

func scaleP21SW270FlagCall(expression ast.Expr) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "BoundedMetadataEnabled" {
		return false
	}
	// s.store.BoundedMetadataEnabled() or a store-typed local (repo).
	if inner, ok := selector.X.(*ast.SelectorExpr); ok {
		return inner.Sel.Name == "store"
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && (ident.Name == "repo" || ident.Name == "repository")
}

// onImplied reports whether expression is true whenever the flag is on.
func (g *scaleP21SW270Graph) onImplied(expression ast.Expr) bool {
	switch value := expression.(type) {
	case *ast.ParenExpr:
		return g.onImplied(value.X)
	case *ast.UnaryExpr:
		return value.Op == token.NOT && g.offImplied(value.X)
	case *ast.BinaryExpr:
		switch value.Op {
		case token.LOR:
			return g.onImplied(value.X) || g.onImplied(value.Y)
		case token.LAND:
			return g.onImplied(value.X) && g.onImplied(value.Y)
		}
	case *ast.CallExpr:
		if scaleP21SW270FlagCall(value) {
			return true
		}
		return g.callOnTrue(value)
	}
	return false
}

// offImplied reports whether expression is false whenever the flag is on.
func (g *scaleP21SW270Graph) offImplied(expression ast.Expr) bool {
	switch value := expression.(type) {
	case *ast.ParenExpr:
		return g.offImplied(value.X)
	case *ast.UnaryExpr:
		return value.Op == token.NOT && g.onImplied(value.X)
	case *ast.BinaryExpr:
		switch value.Op {
		case token.LAND:
			return g.offImplied(value.X) || g.offImplied(value.Y)
		case token.LOR:
			return g.offImplied(value.X) && g.offImplied(value.Y)
		}
	}
	return false
}

// callOnTrue resolves s.helper(...) / helper(...) to a function whose first
// statement returns true whenever the flag is on.
func (g *scaleP21SW270Graph) callOnTrue(call *ast.CallExpr) bool {
	targets := g.targets(call.Fun, call)
	if len(targets) != 1 {
		return false
	}
	key := targets[0]
	if done, ok := g.onTrue[key]; ok {
		return done
	}
	if g.onTrueRun[key] {
		return false
	}
	g.onTrueRun[key] = true
	fn := g.funcs[key]
	result := false
	if fn != nil && fn.decl.Type.Results != nil && len(fn.decl.Type.Results.List) == 1 && len(fn.decl.Body.List) > 0 {
		if ident, ok := fn.decl.Type.Results.List[0].Type.(*ast.Ident); ok && ident.Name == "bool" {
			if first, ok := fn.decl.Body.List[0].(*ast.IfStmt); ok && first.Init == nil && first.Else == nil && g.onImplied(first.Cond) {
				result = scaleP21SW270ReturnsTrue(first.Body)
			}
		}
	}
	g.onTrue[key] = result
	return result
}

func scaleP21SW270ReturnsTrue(body *ast.BlockStmt) bool {
	if len(body.List) == 0 {
		return false
	}
	last, ok := body.List[len(body.List)-1].(*ast.ReturnStmt)
	if !ok {
		return false
	}
	all := true
	ast.Inspect(body, func(node ast.Node) bool {
		if _, literal := node.(*ast.FuncLit); literal {
			return false
		}
		if statement, ok := node.(*ast.ReturnStmt); ok {
			if len(statement.Results) != 1 {
				all = false
			} else if ident, ok := statement.Results[0].(*ast.Ident); !ok || ident.Name != "true" {
				all = false
			}
		}
		return true
	})
	_ = last
	return all
}

func scaleP21SW270Terminates(body *ast.BlockStmt) bool {
	if len(body.List) == 0 {
		return false
	}
	switch last := body.List[len(body.List)-1].(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.ExprStmt:
		call, ok := last.X.(*ast.CallExpr)
		if !ok {
			return false
		}
		ident, ok := call.Fun.(*ast.Ident)
		return ok && ident.Name == "panic"
	}
	return false
}

// offOnly decides whether the innermost node of ancestors can execute with
// the flag on. ancestors[0] is the function body.
func (g *scaleP21SW270Graph) offOnly(ancestors []ast.Node) bool {
	node := ancestors[len(ancestors)-1]
	for index := len(ancestors) - 2; index >= 0; index-- {
		child := ancestors[index+1]
		switch parent := ancestors[index].(type) {
		case *ast.IfStmt:
			if child == parent.Body && g.offImplied(parent.Cond) {
				return true
			}
			if parent.Else != nil && child == parent.Else && g.onImplied(parent.Cond) {
				return true
			}
		case *ast.BlockStmt:
			if g.dominatedByOnReturn(parent.List, child) {
				return true
			}
		case *ast.CaseClause:
			if g.dominatedByOnReturn(parent.Body, child) {
				return true
			}
		case *ast.CommClause:
			if g.dominatedByOnReturn(parent.Body, child) {
				return true
			}
		}
	}
	_ = node
	return false
}

func (g *scaleP21SW270Graph) dominatedByOnReturn(list []ast.Stmt, child ast.Node) bool {
	for _, statement := range list {
		if ast.Node(statement) == child {
			return false
		}
		branch, ok := statement.(*ast.IfStmt)
		if ok && branch.Init == nil && branch.Else == nil && g.onImplied(branch.Cond) && scaleP21SW270Terminates(branch.Body) {
			return true
		}
	}
	return false
}

type scaleP21SW270Review struct {
	Status, Temperature, TemperatureReason string
	Accessors                              []string
}

func scaleP21SW270Inventory(t *testing.T) map[string]scaleP21SW270Review {
	t.Helper()
	paths, err := filepath.Glob("../../internal/store/scale_p21_inventory/server/*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("server inventory missing: %v", err)
	}
	result := map[string]scaleP21SW270Review{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var file struct {
			Consumers map[string]scaleP21SW270Review `json:"consumers"`
		}
		if err := json.Unmarshal(raw, &file); err != nil {
			t.Fatal(err)
		}
		source := strings.TrimSuffix(filepath.Base(path), ".json") + ".go"
		for name, review := range file.Consumers {
			if review.Temperature == "" {
				review.Temperature = "hot"
			}
			result[source+":"+name] = review
		}
	}
	return result
}

func (g *scaleP21SW270Graph) inventoryKey(key string) string {
	fn := g.funcs[key]
	return fn.file + ":" + fn.decl.Name.Name
}

type scaleP21SW270Boundary struct {
	mode, reason string
}

// scaleP21SW270Boundaries are the references at which the HOT walk stops,
// besides route registrations. Each is pinned by verifyBoundaries:
//   - "async": every reference runs inside a go statement that hands an AI
//     run to its worker after the HOT start boundary has committed;
//   - "handler": an in-process dispatch to a route handler that is also
//     bound by HandleFunc, so its temperature is that route's inventory
//     classification, exactly like a route registration.
var scaleP21SW270Boundaries = map[string]scaleP21SW270Boundary{
	"server.startLoopRun->server.executeLoopRun":                     {"async", "the loop AI run (model and tool calls: executeLoopRun -> runToolCall -> callFlowTool -> runFlowTool, COLD in the inventory) runs on its worker after the HOT start boundary"},
	"server.chatDMAgentCall->server.createAgentSessionStream":        {"handler", "chat DM agent chat is POST /api/agent/sessions/stream, a COLD agent-session route"},
	"server.chatDMAgentCall->server.createAgentSessionMessageStream": {"handler", "chat DM agent follow-up is POST /api/agent/sessions/{id}/messages/stream, a COLD agent-session route"},
	"server.chatDMAgentCall->server.resolveAgentApproval":            {"handler", "chat DM approval card is POST /api/agent/sessions/{id}/approvals/{approvalId}, a COLD agent-session route"},
}

func (g *scaleP21SW270Graph) verifyBoundaries(t *testing.T) {
	t.Helper()
	for edge, boundary := range scaleP21SW270Boundaries {
		from, to, _ := strings.Cut(edge, "->")
		count, routed := 0, false
		for _, index := range g.into[to] {
			ref := g.refs[index]
			if ref.kind == "route" {
				routed = true
			}
			if ref.from != from {
				continue
			}
			count++
			if boundary.mode == "async" && !ref.async {
				t.Errorf("boundary %s is no longer only inside a go statement (%s:%d)", edge, ref.file, ref.line)
			}
			if boundary.mode == "handler" && ref.kind != "value" {
				t.Errorf("boundary %s is no longer a handler dispatch (%s:%d)", edge, ref.file, ref.line)
			}
		}
		if count == 0 {
			t.Errorf("stale boundary %s", edge)
		}
		if boundary.mode == "handler" && !routed {
			t.Errorf("boundary %s dispatches to a handler that no route binds", edge)
		}
	}
}

// hotReach walks every caller of start that can run with the flag on and
// returns the HOT-classified inventory consumers it meets, each with the path
// that reached it. Route registrations are roots: a handler's temperature is
// its own inventory classification.
func (g *scaleP21SW270Graph) hotReach(inventory map[string]scaleP21SW270Review, start string, skip map[string]bool) []string {
	type step struct{ key, path string }
	seen := map[string]bool{start: true}
	queue := []step{{start, start}}
	var hot []string
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, index := range g.into[current.key] {
			ref := g.refs[index]
			if ref.offOnly || ref.kind == "route" || seen[ref.from] || skip[ref.from] || scaleP21SW270Boundaries[ref.from+"->"+ref.to] != (scaleP21SW270Boundary{}) {
				continue
			}
			seen[ref.from] = true
			path := fmt.Sprintf("%s -(%s %s:%d)-> %s", ref.from, ref.kind, ref.file, ref.line, current.path)
			if review, ok := inventory[g.inventoryKey(ref.from)]; ok && review.Temperature != "cold" {
				hot = append(hot, path)
			}
			queue = append(queue, step{ref.from, path})
		}
	}
	sort.Strings(hot)
	return hot
}

type scaleP21SW270Closure struct {
	wrapper string // graph key, e.g. server.workspaceData
	// hot callers whose every reference is flag-off-only, with expected counts.
	hot map[string]int
	// cold callers: inventory COLD, flag-on references allowed, and no
	// HOT-classified consumer reaches them with the flag on.
	cold map[string]int
	// value references allowed with their expected counts (method values).
	values map[string]int
	// relay callers may call the wrapper with the flag on because no HOT
	// consumer reaches them with the flag on (their HOT callers dispatch
	// to an owned reader first); proven by the same walk as cold callers.
	relay map[string]int
	// bounded lists the store accessors the wrapper itself calls only with
	// the flag off (a wrapper with its own bounded implementation), each
	// with its expected package-wide use count.
	bounded map[string]int
}

func (g *scaleP21SW270Graph) verifyClosure(t *testing.T, inventory map[string]scaleP21SW270Review, closure scaleP21SW270Closure) {
	t.Helper()
	if files := g.pkgLevel[closure.wrapper]; len(files) != 0 {
		t.Errorf("package-level reference to %s in %v requires reachability review", closure.wrapper, files)
	}
	if g.funcs[closure.wrapper] == nil {
		t.Fatalf("wrapper %s missing", closure.wrapper)
	}
	counts := map[string]int{}
	values := map[string]int{}
	for _, index := range g.into[closure.wrapper] {
		ref := g.refs[index]
		if ref.kind == "route" {
			t.Errorf("%s is bound as a route handler at %s:%d", closure.wrapper, ref.file, ref.line)
			continue
		}
		if ref.kind == "value" {
			values[ref.from]++
			if closure.values[ref.from] == 0 {
				t.Errorf("%s captures %s as a function value at %s:%d; explicit reachability review is required", ref.from, closure.wrapper, ref.file, ref.line)
			}
			continue
		}
		counts[ref.from]++
		switch {
		case closure.hot[ref.from] > 0:
			if !ref.offOnly {
				t.Errorf("HOT caller %s reaches %s with the flag on at %s:%d", ref.from, closure.wrapper, ref.file, ref.line)
			}
		case closure.cold[ref.from] > 0:
			review, ok := inventory[g.inventoryKey(ref.from)]
			if !ok || review.Temperature != "cold" || strings.TrimSpace(review.TemperatureReason) == "" {
				t.Errorf("cold caller %s is not COLD in the inventory (%+v)", ref.from, review)
			}
		case closure.relay[ref.from] > 0:
		default:
			t.Errorf("new %s caller %s at %s:%d requires explicit HOT-owned or COLD classification", closure.wrapper, ref.from, ref.file, ref.line)
		}
	}
	for caller, want := range closure.hot {
		if counts[caller] != want {
			t.Errorf("HOT caller set changed: %s calls %s %d times, want %d", caller, closure.wrapper, counts[caller], want)
		}
	}
	for caller, want := range closure.cold {
		if counts[caller] != want {
			t.Errorf("cold caller set changed: %s calls %s %d times, want %d", caller, closure.wrapper, counts[caller], want)
		}
	}
	for caller, want := range closure.values {
		if values[caller] != want {
			t.Errorf("function-value set changed: %s captures %s %d times, want %d", caller, closure.wrapper, values[caller], want)
		}
	}
	for caller, want := range closure.relay {
		if counts[caller] != want {
			t.Errorf("relay caller set changed: %s calls %s %d times, want %d", caller, closure.wrapper, counts[caller], want)
		}
	}
	// No HOT-classified consumer may reach a cold or relay caller through
	// references that run with the flag on.
	skip := map[string]bool{closure.wrapper: true}
	for _, set := range []map[string]int{closure.cold, closure.relay} {
		for caller := range set {
			if hot := g.hotReach(inventory, caller, skip); len(hot) != 0 {
				t.Errorf("%s caller %s is reachable from HOT consumers with the flag on:\n  %s", closure.wrapper, caller, strings.Join(hot, "\n  "))
			}
		}
	}
	for accessor, want := range closure.bounded {
		uses := g.selectors[accessor]
		if len(uses) != want {
			t.Errorf("%s has %d uses in this package, want %d", accessor, len(uses), want)
		}
		inside := 0
		for _, use := range uses {
			if use.from != closure.wrapper {
				continue
			}
			inside++
			if !use.offOnly {
				t.Errorf("%s reaches %s with the flag on at %s:%d", closure.wrapper, accessor, use.file, use.line)
			}
		}
		if inside == 0 {
			t.Errorf("%s no longer uses %s; its bounded guard is stale", closure.wrapper, accessor)
		}
	}
}

func scaleP21SW270Counts(names ...string) map[string]int {
	result := map[string]int{}
	for _, name := range names {
		count := 1
		if star := strings.LastIndex(name, "*"); star > 0 {
			count, _ = strconv.Atoi(name[star+1:])
			name = name[:star]
		}
		result["server."+name] = count
	}
	return result
}

// TestScaleP21SW270WorkspaceDataHotBoundaries closes server.workspaceData:
// every HOT caller reaches it only with the flag off, and each remaining
// caller is COLD in the inventory and unreachable from any HOT consumer with
// the flag on. workspaceData itself is unchanged, so flag-off routes are
// byte-identical.
func TestScaleP21SW270WorkspaceDataHotBoundaries(t *testing.T) {
	graph := scaleP21SW270Load(t, ".")
	inventory := scaleP21SW270Inventory(t)
	graph.verifyBoundaries(t)
	graph.verifyClosure(t, inventory, scaleP21SW270Closure{
		wrapper: "server.workspaceData",
		hot: scaleP21SW270Counts(
			"listDocuments", "applicationTaskContext", "applicationWebhookDataScoped", "startCommentActions",
			"listCommentActions", "connectorForRequest", "filterSuggestions", "documentWorkspaceData*2",
			"getNotificationPreferences", "acknowledgeDesktopNotifications", "listProjectRelations",
			"listInitiativeRelations", "listDocumentDrafts", "documentContentHistory", "updatePresence",
			"listPresence", "workspaceDataScoped",
		),
		// The HOT GET/HEAD permission list answers from its retained bounded
		// read (listIssuePermissionsBounded); only the COLD permission writes
		// still reach findIssueForPermissions with the flag on.
		relay: scaleP21SW270Counts("findIssueForPermissions"),
		cold:  scaleP21SW270WorkspaceDataCold(),
	})
}

// TestScaleP21SW271MCPWorkspaceDataHotBoundaries closes
// server.mcpWorkspaceData the same way. The triage routes dispatch to their
// owned projections first; every other caller is a COLD MCP/agent tool.
func TestScaleP21SW271MCPWorkspaceDataHotBoundaries(t *testing.T) {
	graph := scaleP21SW270Load(t, ".")
	inventory := scaleP21SW270Inventory(t)
	graph.verifyBoundaries(t)
	graph.verifyClosure(t, inventory, scaleP21SW270Closure{
		wrapper: "server.mcpWorkspaceData",
		hot:     scaleP21SW270Counts("previewIssueSuggestions", "listIssueSuggestions", "updateIssueSuggestionState", "requestTriageAIPlan"),
		cold: scaleP21SW270Counts("agentEntitySnapshot", "appendMCPDocumentEmbed", "deleteMCPResource", "flowDeletePreview",
			"deleteMCPDocument", "saveMCPProject", "deleteMCPComment", "mcpWorkspaceDataForTool"),
	})
	// The shared body takes its source as an argument: only the literal
	// wrapper hands it the hydrating paged metadata, and the owned triage
	// projection hands it installed projections.
	sources := map[string]int{}
	for _, index := range graph.into["server.mcpWorkspaceDataFrom"] {
		sources[graph.refs[index].from+"/"+graph.refs[index].kind]++
	}
	if want := map[string]int{"server.mcpWorkspaceData/call": 1, "server.mcpWorkspaceDataProjected/call": 1}; !reflect.DeepEqual(sources, want) {
		t.Errorf("mcpWorkspaceDataFrom references=%v want=%v", sources, want)
	}
	for _, use := range graph.selectors["PagedWorkspaceMetadata"] {
		if use.from == "server.mcpWorkspaceDataProjected" {
			t.Error("owned triage projection captures the hydrating paged metadata")
		}
	}
	if uses := graph.selectors["ProjectedWorkspace"]; !slices.ContainsFunc(uses, func(use scaleP21SW270Ref) bool { return use.from == "server.mcpWorkspaceDataProjected" }) {
		t.Error("owned triage projection no longer reads installed projections")
	}
}

// TestScaleP21SW272SnapshotReservationHotBoundaries closes
// server.hydratedLegacySnapshotReservation by its own bounded branch: its one
// reference is the admission estimator of signedFileURLs, and the store
// estimator, which takes the store lock and walks the hydrated graph, runs
// only with the flag off.
func TestScaleP21SW272SnapshotReservationHotBoundaries(t *testing.T) {
	graph := scaleP21SW270Load(t, ".")
	inventory := scaleP21SW270Inventory(t)
	graph.verifyClosure(t, inventory, scaleP21SW270Closure{
		wrapper: "server.hydratedLegacySnapshotReservation",
		values:  scaleP21SW270Counts("signedFileURLs"),
		bounded: map[string]int{"WorkspaceReadSnapshotReservation": 1},
	})
}

func scaleP21SW270WorkspaceDataCold() map[string]int {
	return scaleP21SW270Counts(
		"listWebhooks", "manageableWebhook", "listWebhookFailures", "testWebhook", "createExport",
		"checkAgentMentions", "listAgentSessions", "getAgentSession", "listAgentSkillsHTTP",
		"applyAgentMessageChangesLocked", "resolveAgentElicitation", "resolveAgentApproval", "runAgentSession",
		"stopAgentSession", "runAgentSessionOnWorker", "listAgentFeedback", "listImports", "getImport",
		"listExports", "getExport", "workspaceAnalytics", "rotateApplicationWebhookSecret",
		"listTriageResponsibilities", "listTriageRules", "listWorkflowDefinitions", "listWorkflowRuns",
		"retryWorkflowRun", "listPushSubscriptions", "createPushSubscription", "deletePushSubscription",
		"codingTriggerRequestContext", "startReviewCodingSession", "listCustomerTaxonomy", "getCycleGraph",
		"getCycleCapacity", "listDashboards", "createDashboard", "updateDashboard", "deleteDashboard",
		"subscribeDashboard", "shareDashboard", "dashboardResults", "previewDashboardWidget", "exportDashboard",
		"listPosts", "createPost", "updatePost", "deletePost", "listMeetings", "createMeeting", "updateMeeting",
		"deleteMeeting", "listFeedItems", "listEmailIntakeAddresses", "verifyEmailIntakeAddress",
		"listIdentityProviders", "createIdentityProvider", "findAttachmentsByURL", "filePreview",
		"connectGitLabToken", "gitlabConnectionSnapshot", "listOAuthAppFailures", "listOAuthSyncGroupRequests",
		"listJiraLinks", "listJiraRemoteProjects", "listJiraRemoteStatuses", "finishMFA",
		"scanLinearMigrationTarget", "listMigrations", "getMigration", "downloadMigrationManifest",
		"previewMigration", "inviteMigrationUsers*2", "executeMigration", "rollbackMigration",
		"downloadMigrationBundle", "updateNotificationPreferences", "listNotificationDeliveries",
		"retryNotificationDelivery", "listWorkflowStates", "getTeamSettings", "listIssueTemplates",
		"listTeamLabels", "exportOAuthApplicationManifest", "developerTokenApplication",
		"workspaceOAuthApplication", "getApplicationWebhookSecret", "listOAuthApplicationInstalls",
		"listReleaseNotes", "releaseHistory", "listAgentActivities", "listAIConversations",
		"finishPasskeyRegistration", "listReleases", "getRelease", "listReleasePipelines", "getReleasePipeline",
		"syncReviewFromCodeHost", "listReviews", "getReview", "updateReview*2", "submitReview",
		"commentOnReview", "resolveReviewThread", "listWorkspaceLabels", "listWorkspaceIssueTemplates",
		"listAPIKeys", "revokeAPIKey", "listOAuthApplications", "getOAuthApplication", "listIntegrations",
		"disconnectIntegrationConnection", "disconnectIntegration",
	)
}

func TestScaleP21SW273GuardRejectsFlagOnReach(t *testing.T) {
	const source = `package main

import "net/http"

type server struct{ store interface{ BoundedMetadataEnabled() bool } }

func (s *server) gated(r *http.Request) {
	if s.store.BoundedMetadataEnabled() {
		s.bounded(r)
		return
	}
	s.wrapper(r)
}

func (s *server) elseGated(r *http.Request) {
	if s.store.BoundedMetadataEnabled() || r == nil {
		s.bounded(r)
	} else {
		s.wrapper(r)
	}
}

func (s *server) helperGated(r *http.Request) {
	if s.serveBounded(r) {
		return
	}
	s.wrapper(r)
}

func (s *server) serveBounded(r *http.Request) bool {
	if s.store.BoundedMetadataEnabled() {
		s.bounded(r)
		return true
	}
	return false
}

func (s *server) partial(r *http.Request) {
	if s.store.BoundedMetadataEnabled() && r.Method == "GET" {
		s.bounded(r)
	} else {
		s.wrapper(r)
	}
}

func (s *server) fallsThrough(r *http.Request) {
	if s.store.BoundedMetadataEnabled() {
		s.bounded(r)
	}
	s.wrapper(r)
}

func (s *server) captured(r *http.Request) func(*http.Request) {
	return s.wrapper
}

func (s *server) wrapper(r *http.Request) {}
func (s *server) bounded(r *http.Request) {}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "probe.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	graph := scaleP21SW270Load(t, dir)
	reach := map[string]bool{}
	for _, index := range graph.into["server.wrapper"] {
		ref := graph.refs[index]
		reach[ref.from+"/"+ref.kind] = !ref.offOnly
	}
	want := map[string]bool{
		"server.gated/call": false, "server.elseGated/call": false, "server.helperGated/call": false,
		"server.partial/call": true, "server.fallsThrough/call": true, "server.captured/value": true,
	}
	if !reflect.DeepEqual(reach, want) {
		t.Fatalf("guard reach=%v want=%v", reach, want)
	}
}
