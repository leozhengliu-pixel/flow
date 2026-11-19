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
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The SW286 guard closes the seven store compatibility read wrappers
// (Bootstrap, BootstrapFor, BootstrapForContext, BootstrapForUser,
// WorkspaceMetadata, WorkspaceMetadataForRead, WorkspaceMetadataFields) by
// reachability across cmd/server and internal/store together. The wrappers
// stay as literal hydrating APIs; the guard proves which HOT routes and
// tickers can still execute them with FLOW_BOUNDED_METADATA on.
//
// Both packages are parsed (non-test files only) and type-checked with
// go/types: internal/store first, then cmd/server against that exact store
// package, so s.store.X resolves to the one SQLiteStore method it names.
// Every other import is an empty stand-in; an expression of an imported type
// is unresolved and handled conservatively.
//
// A reference to a package function or method is an edge from the function
// whose body (including nested function literals and go statements) holds it:
//   - call: a direct call;
//   - value: a function or method value (s.handler, store.Func passed as an
//     argument or stored); the capturing function is treated as its caller,
//     which is where the value escapes;
//   - route: a handler bound by HandleFunc/Handle; a route is a root and its
//     temperature is its own inventory classification.
//
// A method named through a package interface reaches the method of that name
// on every package type whose method set has all of the interface's method
// names; a selector whose receiver type is unresolved reaches every package
// method of that name. Dispatch by imported code into package methods
// through imported interfaces is not followed; the place a package method
// escapes as a value is.
//
// A reference is flag-off-only (and is not an edge with the flag on) when
//   - it sits in the then-branch of an if whose condition is false whenever
//     the flag is on, or the else-branch of one that is true whenever the
//     flag is on, or
//   - an earlier statement of an enclosing block (or case) is
//     if <true whenever the flag is on> { ...; return | continue | break }, or
//   - it is a function value bound to a local that the next statement
//     rebinds whenever the flag is on (read := s.store.WorkspaceMetadata;
//     if s.store.BoundedMetadataEnabled() { read = ... }).
// The flag is s.store.BoundedMetadataEnabled() (or on repo, repository, or
// the store's own receiver s), s.metadataResidency != nil inside the store,
// a call to a function whose every flag-on return is true, a local assigned
// once from any of these, a local assigned once from a result that is true in
// every flag-on return of its function (metadata, handled, err := ...), and a
// local assigned once from locals && flag where those locals are known true
// by the enclosing then-branches. Function literals inherit the gating of the
// place they are created.
//
// For each wrapper the guard walks every flag-on reference backwards from
// the wrapper, stopping at route registrations (a route's temperature is its
// own inventory classification) and at the pinned scaleP21SW286Boundaries.
// Every inventory consumer it meets whose temperature is hot is a HOT
// reacher, except the pinned relays (helpers judged by their callers) and
// unrouted handlers. Reviewed entries without a temperature are outside the
// route/ticker table: the walk passes them. A second walk through the
// functions the inventory does not classify must meet no route handler and
// no startup root, so no entry point escapes classification. The walks do
// not pass an allow-listed consumer: those are the named consumers still
// migrating. The test fails on any reacher not explained by the allow-list,
// and on any allow-list entry that no longer reaches its wrapper, so the list
// shrinks as the migrations land. A wrapper is reviewed in the inventory
// exactly when no allow-list entry names it.

var scaleP21SW286Root = "."

type scaleP21SW286Func struct {
	key, pkg, file string
	decl           *ast.FuncDecl
	imports        map[string]bool // the file's import names
	scope          *types.Scope    // the package scope
}

type scaleP21SW286Ref struct {
	from, to, file string
	line           int
	kind           string // call, value or route
	offOnly        bool
	async          bool
	byName         bool // an unresolved receiver: every package method of that name
	authOnly       bool // runs only with authentication disabled (development mode)
	pattern        string
}

type scaleP21SW286Graph struct {
	fset      *token.FileSet
	info      *types.Info
	pkgs      map[*types.Package]string // package -> server or store
	funcs     map[string]*scaleP21SW286Func
	byName    map[string][]string
	named     []*types.Named
	refs      []scaleP21SW286Ref
	into      map[string][]int
	pkgLevel  map[string][]string
	onTrue    map[string]bool
	onTrueRun map[string]bool
	flagVars  map[types.Object][]types.Object // flag local -> locals that must be known true
	prepared  map[string]bool
	single    map[string]map[types.Object]int
	// The function being analysed: its file's import names and its package
	// scope, for identifiers the checker left unresolved (inside an invalid
	// expression).
	imports map[string]bool
	scope   *types.Scope
}

// enter makes fn the function whose identifiers are being resolved and
// returns the restore of the previous one.
func (g *scaleP21SW286Graph) enter(fn *scaleP21SW286Func) func() {
	imports, scope := g.imports, g.scope
	g.imports, g.scope = fn.imports, fn.scope
	return func() { g.imports, g.scope = imports, scope }
}

type scaleP21SW286Importer struct {
	store *types.Package
}

func (importer scaleP21SW286Importer) Import(path string) (*types.Package, error) {
	if path == "flow/api/internal/store" && importer.store != nil {
		return importer.store, nil
	}
	imported := types.NewPackage(path, path[strings.LastIndex(path, "/")+1:])
	imported.MarkComplete()
	return imported, nil
}

func scaleP21SW286Source(set *token.FileSet, node ast.Node) string {
	var text bytes.Buffer
	if err := format.Node(&text, set, node); err != nil {
		return fmt.Sprintf("<%T>", node)
	}
	return text.String()
}

// key names a package function as server/name or store/name and a method as
// server/Type.name or store/Type.name. Interface methods have no key.
func (g *scaleP21SW286Graph) key(fn *types.Func) string {
	if fn == nil {
		return ""
	}
	fn = fn.Origin()
	prefix, ok := g.pkgs[fn.Pkg()]
	if !ok {
		return ""
	}
	signature, ok := fn.Type().(*types.Signature)
	if !ok {
		return ""
	}
	receiver := signature.Recv()
	if receiver == nil {
		return prefix + "/" + fn.Name()
	}
	kind := receiver.Type()
	if pointer, ok := kind.(*types.Pointer); ok {
		kind = pointer.Elem()
	}
	if named, ok := kind.(*types.Named); ok && !types.IsInterface(named) {
		return prefix + "/" + named.Origin().Obj().Name() + "." + fn.Name()
	}
	return ""
}

func (g *scaleP21SW286Graph) parse(t *testing.T, dir string) []*ast.File {
	t.Helper()
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
		file, err := parser.ParseFile(g.fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		t.Fatalf("no sources in %s", dir)
	}
	return files
}

func scaleP21SW286Load(t *testing.T) *scaleP21SW286Graph {
	t.Helper()
	graph := &scaleP21SW286Graph{
		fset: token.NewFileSet(), pkgs: map[*types.Package]string{}, funcs: map[string]*scaleP21SW286Func{},
		byName: map[string][]string{}, into: map[string][]int{}, pkgLevel: map[string][]string{},
		onTrue: map[string]bool{}, onTrueRun: map[string]bool{}, flagVars: map[types.Object][]types.Object{},
		prepared: map[string]bool{}, single: map[string]map[types.Object]int{},
		info: &types.Info{Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{},
			Selections: map[*ast.SelectorExpr]*types.Selection{}, Types: map[ast.Expr]types.TypeAndValue{}},
	}
	storeFiles := graph.parse(t, filepath.Join(scaleP21SW286Root, "../../internal/store"))
	storeConfig := types.Config{Importer: scaleP21SW286Importer{}, Error: func(error) {}}
	storePackage, _ := storeConfig.Check("flow/api/internal/store", graph.fset, storeFiles, graph.info)
	graph.pkgs[storePackage] = "store"
	serverFiles := graph.parse(t, scaleP21SW286Root)
	serverConfig := types.Config{Importer: scaleP21SW286Importer{store: storePackage}, Error: func(error) {}}
	serverPackage, _ := serverConfig.Check("main", graph.fset, serverFiles, graph.info)
	graph.pkgs[serverPackage] = "server"
	for _, pkg := range []*types.Package{storePackage, serverPackage} {
		for _, name := range pkg.Scope().Names() {
			if typeName, ok := pkg.Scope().Lookup(name).(*types.TypeName); ok {
				if named, ok := typeName.Type().(*types.Named); ok && !types.IsInterface(named) {
					graph.named = append(graph.named, named)
				}
			}
		}
	}
	all := map[string][]*ast.File{"store": storeFiles, "server": serverFiles}
	scopes := map[string]*types.Scope{"store": storePackage.Scope(), "server": serverPackage.Scope()}
	for _, pkg := range []string{"store", "server"} {
		for _, file := range all[pkg] {
			name := filepath.Base(graph.fset.Position(file.Pos()).Filename)
			imports := map[string]bool{}
			for _, spec := range file.Imports {
				path := strings.Trim(spec.Path.Value, `"`)
				local := path[strings.LastIndex(path, "/")+1:]
				if spec.Name != nil {
					local = spec.Name.Name
				}
				imports[local] = true
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				key := graph.declKey(pkg, name, fn)
				if key == "" || graph.funcs[key] != nil {
					t.Fatalf("function %s in %s/%s has no unique key %q", fn.Name.Name, pkg, name, key)
				}
				graph.funcs[key] = &scaleP21SW286Func{key: key, pkg: pkg, file: name, decl: fn, imports: imports, scope: scopes[pkg]}
				if fn.Recv != nil {
					graph.byName[fn.Name.Name] = append(graph.byName[fn.Name.Name], key)
				}
			}
		}
	}
	for _, pkg := range []string{"store", "server"} {
		for _, file := range all[pkg] {
			name := filepath.Base(graph.fset.Position(file.Pos()).Filename)
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					// A package-level declaration can capture a function
					// outside any request; every such capture is reported.
					ast.Inspect(decl, func(node ast.Node) bool {
						if ident, ok := node.(*ast.Ident); ok {
							if object, ok := graph.info.Uses[ident].(*types.Func); ok && graph.key(object) != "" {
								graph.pkgLevel[graph.key(object)] = append(graph.pkgLevel[graph.key(object)], pkg+"/"+name)
							}
						}
						return true
					})
					continue
				}
				graph.collect(graph.funcs[graph.declKey(pkg, name, fn)])
			}
		}
	}
	return graph
}

func (g *scaleP21SW286Graph) declKey(pkg, file string, fn *ast.FuncDecl) string {
	if fn.Name.Name == "init" && fn.Recv == nil {
		return pkg + "/init@" + file
	}
	object, _ := g.info.Defs[fn.Name].(*types.Func)
	return g.key(object)
}

func scaleP21SW286RouteCall(node ast.Node) bool {
	call, ok := node.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && (selector.Sel.Name == "HandleFunc" || selector.Sel.Name == "Handle") && len(call.Args) == 2
}

// implementers returns the package methods called method on every package
// type whose method set names every method of iface.
func (g *scaleP21SW286Graph) implementers(iface *types.Interface, method string) []string {
	var names []string
	for index := 0; index < iface.NumMethods(); index++ {
		names = append(names, iface.Method(index).Name())
	}
	var result []string
	for _, named := range g.named {
		set := types.NewMethodSet(types.NewPointer(named))
		complete := true
		for _, name := range names {
			if set.Lookup(named.Obj().Pkg(), name) == nil {
				complete = false
				break
			}
		}
		if !complete {
			continue
		}
		if selection := set.Lookup(named.Obj().Pkg(), method); selection != nil {
			if fn, ok := selection.Obj().(*types.Func); ok {
				if key := g.key(fn); key != "" && g.funcs[key] != nil {
					result = append(result, key)
				}
			}
		}
	}
	if len(result) == 0 {
		return g.byName[method]
	}
	return result
}

func (g *scaleP21SW286Graph) targets(node ast.Node, parent ast.Node) []string {
	result, _ := g.resolve(node, parent)
	return result
}

// resolve also reports whether the targets come from the conservative
// by-name fallback for an unresolved receiver.
func (g *scaleP21SW286Graph) resolve(node ast.Node, parent ast.Node) ([]string, bool) {
	switch expression := node.(type) {
	case *ast.SelectorExpr:
		if selection := g.info.Selections[expression]; selection != nil {
			if selection.Kind() == types.FieldVal {
				return nil, false
			}
			object, _ := selection.Obj().(*types.Func)
			if object == nil {
				return nil, false
			}
			if _, local := g.pkgs[object.Pkg()]; !local {
				return nil, false
			}
			receiver := selection.Recv()
			if pointer, ok := receiver.(*types.Pointer); ok {
				receiver = pointer.Elem()
			}
			if iface, ok := receiver.Underlying().(*types.Interface); ok {
				return g.implementers(iface, expression.Sel.Name), false
			}
			if key := g.key(object); key != "" {
				return []string{key}, false
			}
			return nil, false
		}
		if ident, ok := expression.X.(*ast.Ident); ok {
			_, imported := g.info.Uses[ident].(*types.PkgName)
			if imported || g.info.Uses[ident] == nil && g.info.Defs[ident] == nil && g.imports[ident.Name] {
				if object, ok := g.info.Uses[expression.Sel].(*types.Func); ok {
					if key := g.key(object); key != "" {
						return []string{key}, false
					}
				}
				if ident.Name == "store" && g.imports["store"] {
					// An unresolved store.Func inside an invalid expression.
					for pkg, prefix := range g.pkgs {
						if object, ok := pkg.Scope().Lookup(expression.Sel.Name).(*types.Func); ok && prefix == "store" {
							return []string{g.key(object)}, false
						}
					}
				}
				return nil, false
			}
		}
		if typed, ok := g.info.Types[expression.X]; ok && typed.Type != nil && typed.Type != types.Typ[types.Invalid] {
			return nil, false // a resolved non-package type: its methods are not ours
		}
		return g.byName[expression.Sel.Name], true
	case *ast.Ident:
		if selector, ok := parent.(*ast.SelectorExpr); ok && selector.Sel == expression {
			return nil, false
		}
		object, ok := g.info.Uses[expression].(*types.Func)
		if !ok && g.info.Uses[expression] == nil && g.info.Defs[expression] == nil && g.scope != nil {
			// Unresolved inside an invalid expression: a package function of
			// that name is conservatively referenced.
			object, ok = g.scope.Lookup(expression.Name).(*types.Func)
		}
		if !ok {
			return nil, false
		}
		if key := g.key(object); key != "" && !strings.Contains(key[strings.Index(key, "/")+1:], ".") {
			return []string{key}, false
		}
	}
	return nil, false
}

func (g *scaleP21SW286Graph) collect(owner *scaleP21SW286Func) {
	defer g.enter(owner)()
	g.prepare(owner)
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
		targets, byName := g.resolve(node, ancestors[len(ancestors)-2])
		if len(targets) == 0 {
			return true
		}
		kind := "value"
		if call, ok := ancestors[len(ancestors)-2].(*ast.CallExpr); ok && call.Fun == node {
			kind = "call"
		}
		for index, ancestor := range ancestors[:len(ancestors)-1] {
			if scaleP21SW286RouteCall(ancestor) && ancestors[index+1] != ancestor.(*ast.CallExpr).Fun {
				kind = "route"
			}
		}
		pattern := ""
		for index, ancestor := range ancestors[:len(ancestors)-1] {
			if scaleP21SW286RouteCall(ancestor) && ancestors[index+1] != ancestor.(*ast.CallExpr).Fun {
				if literal, ok := ancestor.(*ast.CallExpr).Args[0].(*ast.BasicLit); ok {
					pattern, _ = strconv.Unquote(literal.Value)
				}
			}
		}
		offOnly := g.offOnly(ancestors) || kind == "value" && g.rebound(owner, ancestors)
		async := slices.ContainsFunc(ancestors, func(node ast.Node) bool { _, ok := node.(*ast.GoStmt); return ok })
		authOnly := scaleP21SW286AuthOnly(ancestors)
		position := g.fset.Position(node.Pos())
		for _, target := range targets {
			g.into[target] = append(g.into[target], len(g.refs))
			g.refs = append(g.refs, scaleP21SW286Ref{from: owner.key, to: target, file: owner.pkg + "/" + owner.file, line: position.Line,
				kind: kind, offOnly: offOnly, async: async, byName: byName, authOnly: authOnly, pattern: pattern})
		}
		return true
	})
}

// rebound reports a function value bound to a local that is rebound whenever
// the flag is on before anything can read it:
//
//	read := s.store.WorkspaceMetadata
//	if s.store.BoundedMetadataEnabled() {
//		read = s.store.WorkspaceSettingsMetadata
//	}
//
// The local must have exactly these two assignments.
func (g *scaleP21SW286Graph) rebound(owner *scaleP21SW286Func, ancestors []ast.Node) bool {
	if len(ancestors) < 3 {
		return false
	}
	define, ok := ancestors[len(ancestors)-2].(*ast.AssignStmt)
	if !ok || define.Tok != token.DEFINE || len(define.Lhs) != 1 || len(define.Rhs) != 1 || define.Rhs[0] != ancestors[len(ancestors)-1] {
		return false
	}
	local, ok := define.Lhs[0].(*ast.Ident)
	if !ok || g.info.Defs[local] == nil || g.single[owner.key][g.info.Defs[local]] != 2 {
		return false
	}
	block, ok := ancestors[len(ancestors)-3].(*ast.BlockStmt)
	if !ok {
		return false
	}
	for index, statement := range block.List {
		if statement != define || index+1 >= len(block.List) {
			continue
		}
		branch, ok := block.List[index+1].(*ast.IfStmt)
		if !ok || branch.Init != nil || branch.Else != nil || len(branch.Body.List) != 1 || !g.onImplied(branch.Cond, nil) {
			return false
		}
		rebind, ok := branch.Body.List[0].(*ast.AssignStmt)
		if !ok || rebind.Tok != token.ASSIGN || len(rebind.Lhs) != 1 || len(rebind.Rhs) != 1 {
			return false
		}
		target, ok := rebind.Lhs[0].(*ast.Ident)
		return ok && g.info.Uses[target] == g.info.Defs[local]
	}
	return false
}

// prepare finds the flag locals of fn: a local assigned exactly once (its
// definition, address never taken) from
//   - an expression that is true whenever the flag is on (bounded := s.store.BoundedMetadataEnabled());
//   - a conjunction of single-assignment locals and such an expression
//     (boundedChat := chatAuthenticated && s.store.BoundedMetadataEnabled()),
//     which is the flag wherever every one of those locals is known true;
//   - a result of a call whose every flag-on return yields true there
//     (metadata, handled, err := s.chatCardMetadata(...)).
func (g *scaleP21SW286Graph) prepare(fn *scaleP21SW286Func) {
	if fn == nil || g.prepared[fn.key] {
		return
	}
	g.prepared[fn.key] = true
	defer g.enter(fn)()
	assigned := map[types.Object]int{}
	candidates := map[types.Object][]types.Object{}
	object := func(ident *ast.Ident) types.Object {
		if defined := g.info.Defs[ident]; defined != nil {
			return defined
		}
		return g.info.Uses[ident]
	}
	ast.Inspect(fn.decl.Body, func(node ast.Node) bool {
		switch statement := node.(type) {
		case *ast.AssignStmt:
			for index, lhs := range statement.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok || object(ident) == nil {
					continue
				}
				assigned[object(ident)]++
				if statement.Tok != token.DEFINE {
					continue
				}
				if len(statement.Lhs) == len(statement.Rhs) {
					if required, ok := g.conditionalFlag(statement.Rhs[index]); ok {
						candidates[object(ident)] = required
					}
				} else if call, ok := statement.Rhs[0].(*ast.CallExpr); ok && len(statement.Rhs) == 1 {
					if targets := g.targets(call.Fun, call); len(targets) == 1 && g.resultOnTrue(targets[0], index, len(statement.Lhs)) {
						candidates[object(ident)] = nil
					}
				}
			}
		case *ast.ValueSpec:
			for _, ident := range statement.Names {
				if object(ident) != nil {
					assigned[object(ident)] += 2 // var declarations are not flag locals
				}
			}
		case *ast.UnaryExpr:
			if ident, ok := statement.X.(*ast.Ident); ok && statement.Op == token.AND && object(ident) != nil {
				assigned[object(ident)] += 2
			}
		case *ast.IncDecStmt:
			if ident, ok := statement.X.(*ast.Ident); ok && object(ident) != nil {
				assigned[object(ident)] += 2
			}
		}
		return true
	})
	g.single[fn.key] = assigned
	// A parameter is assigned once on entry; any assignment in the body
	// changes it. A local is assigned once by its definition.
	once := func(local types.Object) bool {
		if fn.decl.Type.Pos() <= local.Pos() && local.Pos() < fn.decl.Type.End() {
			return assigned[local] == 0
		}
		return assigned[local] == 1
	}
	for local, required := range candidates {
		if !once(local) || slices.ContainsFunc(required, func(other types.Object) bool { return !once(other) }) {
			continue
		}
		g.flagVars[local] = required
	}
}

// conditionalFlag reports an expression that is true whenever the flag is on
// and every returned local is true: a conjunction of plain locals and at
// least one unconditional flag expression.
func (g *scaleP21SW286Graph) conditionalFlag(expression ast.Expr) ([]types.Object, bool) {
	if g.onImplied(expression, nil) {
		return nil, true
	}
	var required []types.Object
	flag := false
	var walk func(ast.Expr) bool
	walk = func(expression ast.Expr) bool {
		switch value := expression.(type) {
		case *ast.ParenExpr:
			return walk(value.X)
		case *ast.BinaryExpr:
			if value.Op == token.LAND {
				return walk(value.X) && walk(value.Y)
			}
		case *ast.Ident:
			if local, ok := g.info.Uses[value].(*types.Var); ok && !local.IsField() {
				required = append(required, local)
				return true
			}
		}
		if g.onImplied(expression, nil) {
			flag = true
			return true
		}
		return false
	}
	if walk(expression) && flag && len(required) > 0 {
		return required, true
	}
	return nil, false
}

func (g *scaleP21SW286Graph) flagReceiver(expression ast.Expr) bool {
	switch receiver := expression.(type) {
	case *ast.SelectorExpr:
		return receiver.Sel.Name == "store"
	case *ast.Ident:
		if receiver.Name == "repo" || receiver.Name == "repository" {
			return true
		}
		// The store's own receiver.
		if typed, ok := g.info.Types[receiver]; ok && typed.Type != nil && receiver.Name == "s" {
			if pointer, ok := typed.Type.(*types.Pointer); ok {
				if named, ok := pointer.Elem().(*types.Named); ok && named.Obj().Name() == "SQLiteStore" && g.pkgs[named.Obj().Pkg()] == "store" {
					return true
				}
			}
		}
	}
	return false
}

func (g *scaleP21SW286Graph) flagCall(expression ast.Expr) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "BoundedMetadataEnabled" && g.flagReceiver(selector.X)
}

// residency reports s.metadataResidency compared with nil inside the store:
// on is true for !=, false for ==.
func (g *scaleP21SW286Graph) residency(expression *ast.BinaryExpr) (on, ok bool) {
	if expression.Op != token.NEQ && expression.Op != token.EQL {
		return false, false
	}
	selector, isSelector := expression.X.(*ast.SelectorExpr)
	nilIdent, isNil := expression.Y.(*ast.Ident)
	if !isSelector || !isNil || nilIdent.Name != "nil" || selector.Sel.Name != "metadataResidency" || !g.flagReceiver(selector.X) {
		return false, false
	}
	return expression.Op == token.NEQ, true
}

// onImplied reports whether expression is true whenever the flag is on, given
// the locals known true at that point.
func (g *scaleP21SW286Graph) onImplied(expression ast.Expr, known map[types.Object]bool) bool {
	switch value := expression.(type) {
	case *ast.ParenExpr:
		return g.onImplied(value.X, known)
	case *ast.Ident:
		required, ok := g.flagVars[g.info.Uses[value]]
		return ok && !slices.ContainsFunc(required, func(local types.Object) bool { return !known[local] })
	case *ast.UnaryExpr:
		return value.Op == token.NOT && g.offImplied(value.X, known)
	case *ast.BinaryExpr:
		if on, ok := g.residency(value); ok {
			return on
		}
		switch value.Op {
		case token.LOR:
			return g.onImplied(value.X, known) || g.onImplied(value.Y, known)
		case token.LAND:
			return g.onImplied(value.X, known) && g.onImplied(value.Y, known)
		}
	case *ast.CallExpr:
		if g.flagCall(value) {
			return true
		}
		targets := g.targets(value.Fun, value)
		return len(targets) == 1 && g.resultOnTrue(targets[0], 0, 1)
	}
	return false
}

// offImplied reports whether expression is false whenever the flag is on.
func (g *scaleP21SW286Graph) offImplied(expression ast.Expr, known map[types.Object]bool) bool {
	switch value := expression.(type) {
	case *ast.ParenExpr:
		return g.offImplied(value.X, known)
	case *ast.UnaryExpr:
		return value.Op == token.NOT && g.onImplied(value.X, known)
	case *ast.BinaryExpr:
		if on, ok := g.residency(value); ok {
			return !on
		}
		switch value.Op {
		case token.LAND:
			return g.offImplied(value.X, known) || g.offImplied(value.Y, known)
		case token.LOR:
			return g.offImplied(value.X, known) && g.offImplied(value.Y, known)
		}
	}
	return false
}

// resultOnTrue reports whether result index of the arity-result function key
// is the literal true in every return statement that can run with the flag
// on (there is at least one, and no naked return).
func (g *scaleP21SW286Graph) resultOnTrue(key string, index, arity int) bool {
	memo := fmt.Sprintf("%s#%d/%d", key, index, arity)
	if done, ok := g.onTrue[memo]; ok {
		return done
	}
	if g.onTrueRun[memo] {
		return false
	}
	g.onTrueRun[memo] = true
	fn := g.funcs[key]
	result := false
	if fn != nil && fn.decl.Type.Results != nil && fn.decl.Type.Results.NumFields() == arity {
		g.prepare(fn)
		defer g.enter(fn)()
		result = true
		returns := 0
		var ancestors []ast.Node
		ast.Inspect(fn.decl.Body, func(node ast.Node) bool {
			if node == nil {
				ancestors = ancestors[:len(ancestors)-1]
				return false
			}
			if _, literal := node.(*ast.FuncLit); literal {
				return false
			}
			ancestors = append(ancestors, node)
			statement, ok := node.(*ast.ReturnStmt)
			if !ok || g.offOnly(ancestors) {
				return true
			}
			returns++
			if len(statement.Results) != arity {
				result = false
			} else if ident, ok := statement.Results[index].(*ast.Ident); !ok || ident.Name != "true" || g.info.Uses[ident] != types.Universe.Lookup("true") {
				result = false
			}
			return true
		})
		if returns == 0 {
			result = false
		}
	}
	g.onTrue[memo] = result
	return result
}

func scaleP21SW286Terminates(body *ast.BlockStmt) bool {
	if len(body.List) == 0 {
		return false
	}
	switch last := body.List[len(body.List)-1].(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BranchStmt:
		// continue/break leave the rest of the enclosing loop body.
		return last.Label == nil && (last.Tok == token.CONTINUE || last.Tok == token.BREAK)
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
// the flag on. ancestors[0] is the function body. Conditions are evaluated
// from the outside in, each with the locals known true by the then-branches
// enclosing it.
func (g *scaleP21SW286Graph) offOnly(ancestors []ast.Node) bool {
	known := map[types.Object]bool{}
	for index := 0; index < len(ancestors)-1; index++ {
		child := ancestors[index+1]
		switch parent := ancestors[index].(type) {
		case *ast.IfStmt:
			if child == parent.Body && g.offImplied(parent.Cond, known) {
				return true
			}
			if parent.Else != nil && child == parent.Else && g.onImplied(parent.Cond, known) {
				return true
			}
			if child == parent.Body {
				known = g.withConjuncts(known, parent.Cond)
			}
		case *ast.BlockStmt:
			if g.dominatedByOnReturn(parent.List, child, known) {
				return true
			}
		case *ast.CaseClause:
			if g.dominatedByOnReturn(parent.Body, child, known) {
				return true
			}
		case *ast.CommClause:
			if g.dominatedByOnReturn(parent.Body, child, known) {
				return true
			}
		case *ast.FuncLit:
			// A literal inherits the gating of the place it is created, but
			// not the facts known there: it may run later.
			known = map[types.Object]bool{}
		}
	}
	return false
}

// withConjuncts adds the plain single-assignment locals that are top-level
// conjuncts of a then-branch condition.
func (g *scaleP21SW286Graph) withConjuncts(known map[types.Object]bool, condition ast.Expr) map[types.Object]bool {
	var locals []types.Object
	var walk func(ast.Expr)
	walk = func(expression ast.Expr) {
		switch value := expression.(type) {
		case *ast.ParenExpr:
			walk(value.X)
		case *ast.BinaryExpr:
			if value.Op == token.LAND {
				walk(value.X)
				walk(value.Y)
			}
		case *ast.Ident:
			if local, ok := g.info.Uses[value].(*types.Var); ok && !local.IsField() {
				locals = append(locals, local)
			}
		}
	}
	walk(condition)
	if len(locals) == 0 {
		return known
	}
	result := maps.Clone(known)
	for _, local := range locals {
		result[local] = true
	}
	return result
}

func (g *scaleP21SW286Graph) dominatedByOnReturn(list []ast.Stmt, child ast.Node, known map[types.Object]bool) bool {
	for _, statement := range list {
		if ast.Node(statement) == child {
			return false
		}
		branch, ok := statement.(*ast.IfStmt)
		if ok && branch.Init == nil && branch.Else == nil && g.onImplied(branch.Cond, known) && scaleP21SW286Terminates(branch.Body) {
			return true
		}
	}
	return false
}

type scaleP21SW286Review struct {
	Status, Temperature, TemperatureReason, Reason string
}

// scaleP21SW286Inventory reads both inventories keyed server/<file>:<name>
// and store/<file>:<name>.
func scaleP21SW286Inventory(t *testing.T) map[string]scaleP21SW286Review {
	t.Helper()
	result := map[string]scaleP21SW286Review{}
	for _, pkg := range []string{"server", "store"} {
		paths, err := filepath.Glob(filepath.Join(scaleP21SW286Root, "../../internal/store/scale_p21_inventory", pkg, "*.json"))
		if err != nil || len(paths) == 0 {
			t.Fatalf("%s inventory missing: %v", pkg, err)
		}
		for _, path := range paths {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var file struct {
				Consumers map[string]scaleP21SW286Review `json:"consumers"`
			}
			if err := json.Unmarshal(raw, &file); err != nil {
				t.Fatal(err)
			}
			source := strings.TrimSuffix(filepath.Base(path), ".json") + ".go"
			for name, review := range file.Consumers {
				// No temperature: a reviewed entry outside the route/ticker
				// table; its callers decide.
				result[pkg+"/"+source+":"+name] = review
			}
		}
	}
	return result
}

func (g *scaleP21SW286Graph) inventoryKey(key string) string {
	fn := g.funcs[key]
	if fn == nil {
		return ""
	}
	return fn.pkg + "/" + fn.file + ":" + fn.decl.Name.Name
}

func scaleP21SW286AuthDisabled(expression ast.Expr) bool {
	if paren, ok := expression.(*ast.ParenExpr); ok {
		return scaleP21SW286AuthDisabled(paren.X)
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "authDisabled" {
		return false
	}
	receiver, ok := selector.X.(*ast.Ident)
	return ok && receiver.Name == "s"
}

func scaleP21SW286AuthEnabled(expression ast.Expr) bool {
	unary, ok := expression.(*ast.UnaryExpr)
	return ok && unary.Op == token.NOT && scaleP21SW286AuthDisabled(unary.X)
}

func scaleP21SW286Conjuncts(expression ast.Expr) []ast.Expr {
	switch value := expression.(type) {
	case *ast.ParenExpr:
		return scaleP21SW286Conjuncts(value.X)
	case *ast.BinaryExpr:
		if value.Op == token.LAND {
			return append(scaleP21SW286Conjuncts(value.X), scaleP21SW286Conjuncts(value.Y)...)
		}
	}
	return []ast.Expr{expression}
}

// scaleP21SW286AuthOnly reports a node that runs only with authentication
// disabled: in the then-branch of if s.authDisabled [&& ...], the else-branch
// of if !s.authDisabled, or after if !s.authDisabled { ...; return }.
func scaleP21SW286AuthOnly(ancestors []ast.Node) bool {
	for index := len(ancestors) - 2; index >= 0; index-- {
		child := ancestors[index+1]
		var list []ast.Stmt
		switch parent := ancestors[index].(type) {
		case *ast.IfStmt:
			if child == parent.Body && slices.ContainsFunc(scaleP21SW286Conjuncts(parent.Cond), scaleP21SW286AuthDisabled) {
				return true
			}
			if parent.Else != nil && child == parent.Else && scaleP21SW286AuthEnabled(parent.Cond) {
				return true
			}
		case *ast.BlockStmt:
			list = parent.List
		case *ast.CaseClause:
			list = parent.Body
		}
		for _, statement := range list {
			if ast.Node(statement) == child {
				break
			}
			branch, ok := statement.(*ast.IfStmt)
			if ok && branch.Init == nil && branch.Else == nil && scaleP21SW286AuthEnabled(branch.Cond) && scaleP21SW286Terminates(branch.Body) {
				return true
			}
		}
	}
	return false
}

var scaleP21SW286Wrappers = []string{
	"store/SQLiteStore.Bootstrap",
	"store/SQLiteStore.BootstrapFor",
	"store/SQLiteStore.BootstrapForContext",
	"store/SQLiteStore.BootstrapForUser",
	"store/SQLiteStore.WorkspaceMetadata",
	"store/SQLiteStore.WorkspaceMetadataForRead",
	"store/SQLiteStore.WorkspaceMetadataFields",
}

func scaleP21SW286Short(wrapper string) string {
	return wrapper[strings.LastIndex(wrapper, ".")+1:]
}

type scaleP21SW286Boundary struct {
	mode, reason string
	// proof and auth only:
	test    string            // an existing test of this package that proves the edge
	guard   string            // every reference of the edge lies in then:<cond> or else:<cond>
	pins    map[string]string // function -> statement it contains exactly once
	off     map[string]string // function -> statement that runs only with the flag off
	callers map[string]int    // exact flag-on non-route references into the edge's caller
	routes  string            // method prefix of every route binding the edge's caller
}

// scaleP21SW286Boundaries are the edges at which the walk stops, besides
// route registrations. Each is pinned by verifyBoundaries:
//   - "async": every reference runs inside a go statement that hands an AI
//     run to its worker after the HOT start boundary has committed;
//   - "handler": an in-process dispatch to a route handler that is also
//     bound by HandleFunc, so its temperature is that route's inventory
//     classification, exactly like a route registration;
//   - "name": every reference is the by-name fallback for a receiver of an
//     imported type that merely shares the wrapper's method name;
//   - "proof": a data-flow fact (a context marker, an owned kind, a request
//     method, a read's construction) that the flag-insensitive walk cannot
//     see, pinned by its statements and by the existing test that proves it;
//   - "auth": the caller enters the callee only with authentication enabled
//     and the callee reaches a wrapper only in development mode; a forward
//     walk from the callee that skips development-only references must reach
//     no wrapper.
var scaleP21SW286Boundaries = map[string]scaleP21SW286Boundary{
	"server/server.startLoopRun->server/server.executeLoopRun": {mode: "async",
		reason: "the loop AI run (model and tool calls: executeLoopRun -> runToolCall -> callFlowTool -> runFlowTool, COLD in the inventory) runs on its worker after the HOT start boundary"},
	"server/server.startBoundedLoopRun->server/server.executeLoopRun": {mode: "async",
		reason: "the loop AI run (model and tool calls: executeLoopRun -> runToolCall -> callFlowTool -> runFlowTool, COLD in the inventory) runs on its worker after the HOT start boundary"},
	"server/server.chatDMAgentCall->server/server.createAgentSessionStream": {mode: "handler",
		reason: "chat DM agent chat is POST /api/agent/sessions/stream, a COLD agent-session route"},
	"server/server.chatDMAgentCall->server/server.createAgentSessionMessageStream": {mode: "handler",
		reason: "chat DM agent follow-up is POST /api/agent/sessions/{id}/messages/stream, a COLD agent-session route"},
	"server/server.chatDMAgentCall->server/server.resolveAgentApproval": {mode: "handler",
		reason: "chat DM approval card is POST /api/agent/sessions/{id}/approvals/{approvalId}, a COLD agent-session route"},
	"server/server.codingRuntimeBootstrap->store/SQLiteStore.Bootstrap": {mode: "name",
		reason: "s.coding.service.Bootstrap is the sandbox runtime protocol of internal/coding, not the store wrapper"},
	"server/server.createIssue->store/SQLiteStore.BootstrapForUser": {mode: "proof",
		reason: "the literal default-team read runs only without the issue-record mutation marker; the only in-process caller adds the marker and POST /api/issues is rewritten to /api/issue-records before dispatch",
		test:   "TestScaleP21LegacyIssueWritesReachRecordRoutes",
		guard:  "else:store.UsesIssueRecordMutations(r.Context())",
		pins: map[string]string{
			"server/server.createIssueRecord": "s.createIssue(w, r.WithContext(store.WithIssueRecordMutations(r.Context())))",
			"server/boundedLegacyIssueWrites": `copy.URL.Path = "/api/issue-records" + strings.TrimPrefix(r.URL.Path, "/api/issues")`,
		},
		callers: map[string]int{"server/server.createIssueRecord": 1}},
	"server/server.chatAuthorizedMutation->server/server.issueRecordsBootstrapData": {mode: "proof",
		reason: "HOT chat callers select an owned bootstrap kind (or a point read that refuses in-process bootstrap) and return through the owned reader first; only COLD group/unfurl callers reach the literal bootstrap",
		test:   "TestScaleP21SW225IssueBootstrapLegacyBoundaries"},
	"server/server.teamForMemberRequest->server/server.publicMetadata": {mode: "auth",
		reason: "with the flag on the team-member routes fall through to publicMetadata only with an authenticated paged handoff; publicMetadata reads WorkspaceMetadata only in development mode",
		test:   "TestScaleP21HotTeamMemberRequestDevelopmentAndErrors",
		pins: map[string]string{
			"server/server.teamForMemberRequest": "handedOff := !s.authDisabled && handoff != nil && handoff.workspace == workspace && handoff.userID == authUser(r).ID && !handoff.taken.Load()",
		}},
	"store/PulseRead.LoadSchedule->store/SQLiteStore.WorkspaceMetadataFields": {mode: "proof",
		reason: "a pulse read has no logical source only when the flag-off branch of beginPulseRead built it; the bounded branch retains a non-nil source or returns no read",
		test:   "TestScaleP21SW288PulseScheduleReadSource",
		guard:  "then:r.source == nil",
		pins: map[string]string{
			"store/SQLiteStore.beginPulseRead": "read.source, err = owner.retainSource(ctx, owner.workspace)",
		},
		off: map[string]string{
			"store/SQLiteStore.beginPulseRead": "return &PulseRead{Snapshot: snapshot, store: s, workspace: workspace}, true, nil",
		}},
	"server/server.listIssuePermissions->server/server.findIssueForPermissions": {mode: "proof",
		reason:  "both permission-list routes are GET patterns (which also serve HEAD), and with the flag on every GET or HEAD permission list reads the retained authorized issue headers; only the COLD permission writers reach workspaceData",
		test:    "TestScaleP21SW113IssuePermissionGETMiddlewareDifferential",
		routes:  "GET ",
		callers: map[string]int{},
		pins: map[string]string{
			"server/boundedIssuePermissionReadRequest": "if r.Method != http.MethodGet && r.Method != http.MethodHead {\n\treturn false\n}",
		}},
}

// scaleP21SW286Unrouted are HOT inventory consumers whose every reference is
// a route registration that no request reaches: the outermost handler
// wrapper rewrites the path first. They are not reachers.
var scaleP21SW286Unrouted = map[string]string{
	"server/server.updateIssue": "boundedLegacyIssueWrites rewrites every PATCH /api/issues/{id} to /api/issue-records/{id} before dispatch (TestScaleP21LegacyIssueWritesReachRecordRoutes)",
}

// scaleP21SW286Relays are HOT inventory consumers that are neither routes
// nor tickers: helpers whose temperature comes from their callers. The walk
// passes them and judges their callers instead.
var scaleP21SW286Relays = map[string]string{
	"server/server.workspaceData":             "server compatibility wrapper: every HOT caller dispatches to an owned reader first (SW270 on the compat-wrappers branch)",
	"server/server.findIssueForPermissions":   "permission helper shared by the GET list and the COLD permission writers",
	"server/server.issueRecordsBootstrapData": "literal bootstrap helper: the HTTP route returns through issueRecordsBootstrapBounded first and chat is pinned by SW225",
	"server/server.publicMetadata":            "public API metadata helper: its other callers are COLD public API routes",
	"server/server.socketWorkspaceMetadata":   "collaboration socket helper: the bounded socket paths read owned collaboration documents first",
}

type scaleP21SW286Allowance struct {
	wrappers      []string // short wrapper names the consumer reaches with the flag on
	owner, reason string
}

// scaleP21SW286Allowed names every consumer still migrating whose HOT paths
// reach a wrapper with the flag on. The walk does not pass them. An entry
// must name exactly the wrappers it still reaches when the other entries are
// not passed either: the test fails when one no longer reaches a wrapper, so
// the list shrinks as the migrations land.
var scaleP21SW286Allowed = map[string]scaleP21SW286Allowance{
	"server/server.processIntegrationDelivery": {wrappers: []string{"WorkspaceMetadata", "WorkspaceMetadataForRead"},
		owner: "swarm: integration delivery", reason: "every-tick integration delivery reads the workspace metadata for the delivery's integration"},
	"server/server.refuseFormerMemberCollaborationUpdate": {wrappers: []string{"WorkspaceMetadata", "WorkspaceMetadataForRead"},
		owner: "swarm: former members", reason: "collaboration frames of a standalone document read socketWorkspaceMetadata, whose development-mode branch is WorkspaceMetadata"},
	"server/server.oauthApplicationsByClient": {wrappers: []string{"WorkspaceMetadataFields"},
		owner: "unowned (reported)", reason: "the once-a-minute HOT delivery tick (reconcileAllApplicationWebhooks) and HOT dispatchWebhookEvent (application, OAuth authorization and team lifecycle events, on its own goroutine) index every workspace's OAuth applications"},
}

func scaleP21SW286Cut(ref scaleP21SW286Ref) bool {
	_, cut := scaleP21SW286Boundaries[ref.from+"->"+ref.to]
	return cut
}

func (g *scaleP21SW286Graph) refAncestors(ref scaleP21SW286Ref) [][]ast.Node {
	fn := g.funcs[ref.from]
	defer g.enter(fn)()
	var result [][]ast.Node
	var ancestors []ast.Node
	ast.Inspect(fn.decl.Body, func(node ast.Node) bool {
		if node == nil {
			ancestors = ancestors[:len(ancestors)-1]
			return false
		}
		ancestors = append(ancestors, node)
		if len(ancestors) < 2 || g.fset.Position(node.Pos()).Line != ref.line {
			return true
		}
		if slices.Contains(g.targets(node, ancestors[len(ancestors)-2]), ref.to) {
			result = append(result, slices.Clone(ancestors))
		}
		return true
	})
	return result
}

// statements returns the statements of fn whose source is text, each with
// whether it runs only with the flag off.
func (g *scaleP21SW286Graph) statements(key, text string) []bool {
	fn := g.funcs[key]
	if fn == nil {
		return nil
	}
	defer g.enter(fn)()
	var result []bool
	var ancestors []ast.Node
	ast.Inspect(fn.decl.Body, func(node ast.Node) bool {
		if node == nil {
			ancestors = ancestors[:len(ancestors)-1]
			return false
		}
		ancestors = append(ancestors, node)
		if statement, ok := node.(ast.Stmt); ok && scaleP21SW286Source(g.fset, statement) == text {
			result = append(result, g.offOnly(ancestors))
		}
		return true
	})
	return result
}

// scaleP21SW286TestExists finds a test of cmd/server or internal/store.
func scaleP21SW286TestExists(t *testing.T, name string) bool {
	t.Helper()
	var paths []string
	for _, dir := range []string{scaleP21SW286Root, filepath.Join(scaleP21SW286Root, "../../internal/store")} {
		found, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, found...)
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("\nfunc "+name+"(t *testing.T)")) {
			return true
		}
	}
	return false
}

func (g *scaleP21SW286Graph) verifyBoundaries(t *testing.T) {
	t.Helper()
	for edge, boundary := range scaleP21SW286Boundaries {
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
			switch boundary.mode {
			case "async":
				if !ref.async {
					t.Errorf("boundary %s is no longer only inside a go statement (%s:%d)", edge, ref.file, ref.line)
				}
			case "handler":
				if ref.kind != "value" {
					t.Errorf("boundary %s is no longer a handler dispatch (%s:%d)", edge, ref.file, ref.line)
				}
			case "name":
				if !ref.byName {
					t.Errorf("boundary %s now resolves to the store wrapper (%s:%d)", edge, ref.file, ref.line)
				}
			case "proof", "auth":
				if boundary.guard == "" {
					break
				}
				branch, condition, _ := strings.Cut(boundary.guard, ":")
				for _, ancestors := range g.refAncestors(ref) {
					guarded := false
					for depth := 0; depth+1 < len(ancestors); depth++ {
						statement, ok := ancestors[depth].(*ast.IfStmt)
						if !ok || scaleP21SW286Source(g.fset, statement.Cond) != condition {
							continue
						}
						if branch == "then" && ancestors[depth+1] == statement.Body || branch == "else" && statement.Else != nil && ancestors[depth+1] == statement.Else {
							guarded = true
						}
					}
					if !guarded {
						t.Errorf("boundary %s lost its %s guard at %s:%d", edge, boundary.guard, ref.file, ref.line)
					}
				}
			default:
				t.Errorf("boundary %s has unknown mode %q", edge, boundary.mode)
			}
		}
		if count == 0 {
			t.Errorf("stale boundary %s", edge)
		}
		if boundary.mode == "handler" && !routed {
			t.Errorf("boundary %s dispatches to a handler that no route binds", edge)
		}
		if boundary.mode != "proof" && boundary.mode != "auth" {
			continue
		}
		if boundary.test == "" || !scaleP21SW286TestExists(t, boundary.test) {
			t.Errorf("boundary %s cites a missing test %q", edge, boundary.test)
		}
		for key, text := range boundary.pins {
			if found := g.statements(key, text); len(found) != 1 {
				t.Errorf("boundary %s: %s contains %q %d times, want 1", edge, key, text, len(found))
			}
		}
		for key, text := range boundary.off {
			if found := g.statements(key, text); len(found) != 1 || !found[0] {
				t.Errorf("boundary %s: %s must contain %q once, only with the flag off (%v)", edge, key, text, found)
			}
		}
		if boundary.callers != nil {
			callers := map[string]int{}
			for _, index := range g.into[from] {
				if ref := g.refs[index]; ref.kind != "route" && !ref.offOnly {
					callers[ref.from]++
				}
			}
			if !maps.Equal(callers, boundary.callers) {
				t.Errorf("boundary %s: flag-on callers of %s are %v, want %v", edge, from, callers, boundary.callers)
			}
		}
		if boundary.routes != "" {
			routes := 0
			for _, index := range g.into[from] {
				if ref := g.refs[index]; ref.kind == "route" {
					routes++
					if !strings.HasPrefix(ref.pattern, boundary.routes) {
						t.Errorf("boundary %s: %s is bound to %q at %s:%d", edge, from, ref.pattern, ref.file, ref.line)
					}
				}
			}
			if routes == 0 {
				t.Errorf("boundary %s: no route binds %s", edge, from)
			}
		}
		if boundary.mode == "auth" {
			if paths := g.forward(to, func(ref scaleP21SW286Ref) bool { return ref.authOnly }); len(paths) != 0 {
				t.Errorf("boundary %s: %s reaches a wrapper with authentication enabled:\n  %s", edge, to, strings.Join(paths, "\n  "))
			}
		}
	}
	for key := range scaleP21SW286Unrouted {
		if g.funcs[key] == nil || len(g.into[key]) == 0 {
			t.Errorf("stale unrouted consumer %s", key)
		}
		for _, index := range g.into[key] {
			if ref := g.refs[index]; ref.kind != "route" {
				t.Errorf("unrouted consumer %s is referenced by %s at %s:%d", key, ref.from, ref.file, ref.line)
			}
		}
	}
	rewrites := 0
	for _, index := range g.into["server/boundedLegacyIssueWrites"] {
		if ref := g.refs[index]; ref.from == "server/newHandler" && ref.kind == "call" && !ref.offOnly {
			rewrites++
		}
	}
	if rewrites != 1 {
		t.Errorf("newHandler no longer wraps every request in boundedLegacyIssueWrites (%d)", rewrites)
	}
}

// forward returns a path to every wrapper start reaches with the flag on,
// skipping references for which skip is true.
func (g *scaleP21SW286Graph) forward(start string, skip func(scaleP21SW286Ref) bool) []string {
	out := map[string][]int{}
	for index, ref := range g.refs {
		out[ref.from] = append(out[ref.from], index)
	}
	paths := map[string]string{start: start}
	queue := []string{start}
	var result []string
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, index := range out[current] {
			ref := g.refs[index]
			if ref.offOnly || ref.kind == "route" || skip(ref) || paths[ref.to] != "" || scaleP21SW286Cut(ref) {
				continue
			}
			paths[ref.to] = fmt.Sprintf("%s -(%s %s:%d)-> %s", paths[current], ref.kind, ref.file, ref.line, ref.to)
			if slices.Contains(scaleP21SW286Wrappers, ref.to) {
				result = append(result, paths[ref.to])
				continue
			}
			queue = append(queue, ref.to)
		}
	}
	sort.Strings(result)
	return result
}

type scaleP21SW286Reacher struct {
	key, path string
}

// reach walks every flag-on reference backwards from start. It returns the
// HOT inventory consumers it meets (temperature "hot", relays and unrouted
// handlers excepted) with the shortest path that reached each, the
// allow-listed consumers in stop it met (it does not pass them) and the
// relays it passed.
func (g *scaleP21SW286Graph) reach(inventory map[string]scaleP21SW286Review, start string, stop map[string]bool) (reachers []scaleP21SW286Reacher, met, relays map[string]string) {
	type step struct{ key, path string }
	seen := map[string]bool{start: true}
	queue := []step{{start, start}}
	met, relays = map[string]string{}, map[string]string{}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, index := range g.into[current.key] {
			ref := g.refs[index]
			if ref.offOnly || ref.kind == "route" || seen[ref.from] || scaleP21SW286Cut(ref) {
				continue
			}
			seen[ref.from] = true
			path := fmt.Sprintf("%s -(%s %s:%d)-> %s", ref.from, ref.kind, ref.file, ref.line, current.path)
			if stop[ref.from] {
				met[ref.from] = path
				continue
			}
			if scaleP21SW286Relays[ref.from] != "" {
				relays[ref.from] = path
			} else if review := inventory[g.inventoryKey(ref.from)]; review.Temperature == "hot" && scaleP21SW286Unrouted[ref.from] == "" && !slices.Contains(scaleP21SW286Wrappers, ref.from) {
				reachers = append(reachers, scaleP21SW286Reacher{ref.from, path})
			}
			queue = append(queue, step{ref.from, path})
		}
	}
	sort.Slice(reachers, func(i, j int) bool { return reachers[i].key < reachers[j].key })
	return reachers, met, relays
}

// unclassified walks flag-on references backwards from start through
// functions the inventory gives no temperature (and relays), and returns
// every route handler and startup root (main, init) it meets: an entry point
// whose HOT or COLD temperature nothing in the inventory decides.
func (g *scaleP21SW286Graph) unclassified(inventory map[string]scaleP21SW286Review, start string, stop map[string]bool) []string {
	seen := map[string]bool{start: true}
	queue := []string{start}
	var result []string
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, index := range g.into[current] {
			ref := g.refs[index]
			if ref.offOnly || scaleP21SW286Cut(ref) {
				continue
			}
			if ref.kind == "route" {
				if !seen["route:"+current] && scaleP21SW286Unrouted[current] == "" {
					seen["route:"+current] = true
					result = append(result, fmt.Sprintf("route %q -> %s (%s:%d)", ref.pattern, current, ref.file, ref.line))
				}
				continue
			}
			if seen[ref.from] || stop[ref.from] {
				continue
			}
			seen[ref.from] = true
			review := inventory[g.inventoryKey(ref.from)]
			if review.Temperature != "" && scaleP21SW286Relays[ref.from] == "" && !slices.Contains(scaleP21SW286Wrappers, ref.from) {
				continue
			}
			if ref.from == "server/main" || strings.Contains(ref.from, "/init@") {
				result = append(result, fmt.Sprintf("startup %s -> %s (%s:%d)", ref.from, current, ref.file, ref.line))
				continue
			}
			queue = append(queue, ref.from)
		}
	}
	sort.Strings(result)
	return result
}

// TestScaleP21SW286StoreWrapperHotBoundaries computes, for each of the seven
// store compatibility read wrappers, every HOT inventory consumer that can
// reach it with FLOW_BOUNDED_METADATA on, and fails on any not explained by
// the named allow-list (and on any allow-list entry that no longer reaches
// its wrapper). A wrapper is reviewed in the inventory exactly when no
// allow-list entry names it, and a pending wrapper's reason names every
// remaining allow-listed consumer.
func TestScaleP21SW286StoreWrapperHotBoundaries(t *testing.T) {
	graph := scaleP21SW286Load(t)
	inventory := scaleP21SW286Inventory(t)
	graph.verifyBoundaries(t)
	stop := map[string]bool{}
	for consumer, allowance := range scaleP21SW286Allowed {
		if graph.funcs[consumer] == nil {
			t.Errorf("allow-listed consumer %s no longer exists", consumer)
		}
		if allowance.owner == "" || allowance.reason == "" || len(allowance.wrappers) == 0 {
			t.Errorf("allow-list entry %s must name its owner, reason and wrappers", consumer)
		}
		stop[consumer] = true
	}
	passedRelays := map[string]bool{}
	pending := map[string][]string{}
	for _, wrapper := range scaleP21SW286Wrappers {
		short := scaleP21SW286Short(wrapper)
		if graph.funcs[wrapper] == nil {
			t.Fatalf("wrapper %s missing", wrapper)
		}
		if files := graph.pkgLevel[wrapper]; len(files) != 0 {
			t.Errorf("package-level reference to %s in %v requires reachability review", wrapper, files)
		}
		reachers, met, relays := graph.reach(inventory, wrapper, stop)
		for _, reacher := range reachers {
			t.Errorf("HOT %s reaches %s with FLOW_BOUNDED_METADATA on and is not allow-listed:\n  %s", reacher.key, short, reacher.path)
		}
		for _, entry := range graph.unclassified(inventory, wrapper, stop) {
			t.Errorf("unclassified entry point reaches %s with FLOW_BOUNDED_METADATA on: %s", short, entry)
		}
		for relay := range relays {
			passedRelays[relay] = true
		}
		for consumer, allowance := range scaleP21SW286Allowed {
			listed := slices.Contains(allowance.wrappers, short)
			path, reached := met[consumer]
			switch {
			case listed && !reached:
				t.Errorf("allow-list entry %s no longer reaches %s with the flag on: remove %s from it", consumer, short, short)
			case !listed && reached:
				t.Errorf("allow-listed %s also reaches %s with the flag on; name it:\n  %s", consumer, short, path)
			case listed:
				pending[short] = append(pending[short], consumer)
			}
		}
	}
	for relay, reason := range scaleP21SW286Relays {
		if review := inventory[graph.inventoryKey(relay)]; review.Temperature != "hot" || reason == "" {
			t.Errorf("relay %s is not a HOT inventory consumer (%+v)", relay, review)
		}
		for _, index := range graph.into[relay] {
			if ref := graph.refs[index]; ref.kind == "route" {
				t.Errorf("relay %s is a route handler (%s:%d); judge it as a HOT consumer", relay, ref.file, ref.line)
			}
		}
		if !passedRelays[relay] {
			t.Errorf("stale relay %s: no wrapper walk passes it", relay)
		}
	}
	for _, wrapper := range scaleP21SW286Wrappers {
		short := scaleP21SW286Short(wrapper)
		key := graph.inventoryKey(wrapper)
		review, ok := inventory[key]
		if !ok {
			t.Errorf("wrapper %s has no inventory entry %s", short, key)
			continue
		}
		blockers := pending[short]
		sort.Strings(blockers)
		switch {
		case len(blockers) == 0 && review.Status != "reviewed":
			t.Errorf("%s has no remaining HOT reacher with the flag on; mark it reviewed", key)
		case len(blockers) != 0 && review.Status == "reviewed":
			t.Errorf("%s is reviewed but still reached with the flag on through %v", key, blockers)
		}
		for _, consumer := range blockers {
			if !strings.Contains(review.Reason, consumer[strings.LastIndex(consumer, ".")+1:]) {
				t.Errorf("%s reason does not name its remaining blocker %s", key, consumer)
			}
		}
	}
}

// TestScaleP21SW290WrapperGuardDetectsFlagOnReach runs the SW286 graph over
// a probe store and server: a direct call, a method value, an interface
// method, a goroutine launch, a captured literal, a falling-through bounded
// branch, a reassigned or unconditioned flag local and a development-only
// branch reach the wrapper with the flag on; the recognised flag-off forms do
// not.
func TestScaleP21SW290WrapperGuardDetectsFlagOnReach(t *testing.T) {
	root := t.TempDir()
	write := func(path, source string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "internal/store/store.go"), `package store

type SQLiteStore struct{ metadataResidency *int }

func (s *SQLiteStore) BoundedMetadataEnabled() bool { return s.metadataResidency != nil }
func (s *SQLiteStore) WorkspaceMetadata(workspace string) (int, bool) { return 0, true }
func (s *SQLiteStore) WorkspaceSettingsMetadata(workspace string) (int, bool) { return 0, true }
func (s *SQLiteStore) residencyGate() {
	if s.metadataResidency == nil {
		s.WorkspaceMetadata("")
	}
}
func (s *SQLiteStore) residencyOpen() {
	if s.metadataResidency != nil {
		s.WorkspaceMetadata("")
	}
}
`)
	write(filepath.Join(root, "cmd/server/main.go"), `package main

import "flow/api/internal/store"

type server struct {
	store        *store.SQLiteStore
	authDisabled bool
}

type reader interface{ WorkspaceMetadata(string) (int, bool) }

func (s *server) direct()               { s.store.WorkspaceMetadata("") }
func (s *server) value()                { read := s.store.WorkspaceMetadata; read("") }
func (s *server) viaInterface(r reader) { r.WorkspaceMetadata("") }
func (s *server) async()                { go s.direct() }
func (s *server) literal() func()       { return func() { s.store.WorkspaceMetadata("") } }
func (s *server) fallthroughBranch() {
	if s.store.BoundedMetadataEnabled() {
		s.direct2()
	}
	s.store.WorkspaceMetadata("")
}
func (s *server) direct2() {}
func (s *server) reassigned() {
	bounded := s.store.BoundedMetadataEnabled()
	bounded = false
	if !bounded {
		s.store.WorkspaceMetadata("")
	}
}
func (s *server) unconditioned(a bool) {
	b := a && s.store.BoundedMetadataEnabled()
	if b {
		return
	}
	s.store.WorkspaceMetadata("")
}
func (s *server) development() {
	if s.store.BoundedMetadataEnabled() {
		if s.authDisabled {
			s.store.WorkspaceMetadata("")
		}
	}
}
func (s *server) offOnly() {
	if !s.store.BoundedMetadataEnabled() {
		s.store.WorkspaceMetadata("")
	}
}
func (s *server) dominated() {
	if s.store.BoundedMetadataEnabled() {
		return
	}
	s.store.WorkspaceMetadata("")
}
func (s *server) rebound() {
	read := s.store.WorkspaceMetadata
	if s.store.BoundedMetadataEnabled() {
		read = s.store.WorkspaceSettingsMetadata
	}
	read("")
}
func (s *server) helper() (int, bool, error) {
	if !s.store.BoundedMetadataEnabled() {
		return 0, false, nil
	}
	return 1, true, nil
}
func (s *server) handled() {
	_, handled, err := s.helper()
	if err != nil {
		return
	}
	if !handled {
		s.store.WorkspaceMetadata("")
	}
}
func (s *server) conditional(a bool) {
	b := a && s.store.BoundedMetadataEnabled()
	if a {
		if b {
			return
		}
		s.store.WorkspaceMetadata("")
	}
}
func (s *server) loop(keys []string) {
	for _, key := range keys {
		if s.store.BoundedMetadataEnabled() {
			continue
		}
		s.store.WorkspaceMetadata(key)
	}
}
func main() {}
`)
	previous := scaleP21SW286Root
	scaleP21SW286Root = filepath.Join(root, "cmd/server")
	defer func() { scaleP21SW286Root = previous }()
	graph := scaleP21SW286Load(t)
	wrapper := "store/SQLiteStore.WorkspaceMetadata"
	direct, development := map[string]bool{}, map[string]bool{}
	for _, index := range graph.into[wrapper] {
		if ref := graph.refs[index]; !ref.offOnly {
			direct[strings.TrimPrefix(strings.TrimPrefix(ref.from, "server/server."), "store/SQLiteStore.")] = true
			if ref.authOnly {
				development[ref.from] = true
			}
		}
	}
	want := map[string]bool{"direct": true, "value": true, "viaInterface": true, "literal": true, "fallthroughBranch": true,
		"reassigned": true, "unconditioned": true, "development": true, "residencyOpen": true}
	if !maps.Equal(direct, want) {
		t.Fatalf("flag-on references to the wrapper = %v, want %v", direct, want)
	}
	if !maps.Equal(development, map[string]bool{"server/server.development": true}) {
		t.Fatalf("development-only references = %v", development)
	}
	if paths := graph.forward("server/server.async", func(scaleP21SW286Ref) bool { return false }); len(paths) != 1 {
		t.Fatalf("goroutine launch does not reach the wrapper: %v", paths)
	}
	if paths := graph.forward("server/server.development", func(ref scaleP21SW286Ref) bool { return ref.authOnly }); len(paths) != 0 {
		t.Fatalf("development-only reach was not skipped: %v", paths)
	}
}
