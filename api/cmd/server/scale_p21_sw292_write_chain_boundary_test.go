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

// The SW292 guard closes the legacy write/install/refresh chain of the
// resident workspace copy: installWrittenWorkspaceLocked, refreshWorkspaceCopy,
// refreshWorkspaceCopyLocked, setWorkspaceLocked (metadata_copy_revision.go)
// and refreshMetadataRecords (metadata_record_merge.go). They stay the literal
// flag-off chain; the guard proves which HOT routes and tickers can still
// execute them with FLOW_BOUNDED_METADATA on. Cold hooks are not evidence:
// only the flag-on reference graph is.
//
// The walker is the SW286 store-wrapper walker (review/scale-p21-store-
// bootstrap-wrappers), copied under this test's prefix so that the two
// guards land independently. Both packages are parsed (non-test files only)
// and type-checked with go/types: internal/store first, then cmd/server
// against that exact store package, so s.store.X resolves to the one
// SQLiteStore method it names. Every other import is an empty stand-in; an
// expression of an imported type is unresolved and handled conservatively.
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
//     if <true whenever the flag is on> { ...; return | continue | break }
//     (first-return dominance), or
//   - it is a function value bound to a local that the next statement
//     rebinds whenever the flag is on.
// The flag is s.store.BoundedMetadataEnabled() (or on repo, repository, or
// the store's own receiver s), s.metadataResidency != nil inside the store,
// a call to a function whose every flag-on return is true, and locals
// assigned once from these. Function literals inherit the gating of the
// place they are created, so a refresh closure returned after
// if s.metadataResidency != nil { return ... } is flag-off-only.
//
// For each target the guard walks every flag-on reference backwards,
// stopping at route registrations, at the pinned scaleP21SW292Boundaries and
// at allow-listed consumers. Every inventory consumer it meets whose
// temperature is hot is a HOT reacher, except the closed consumers (HOT
// inventory entries that nothing references with the flag on, proven here
// and by their own callset tests). A second walk through the functions the
// inventory does not classify must meet no route handler and no startup
// root. The test fails on any reacher not explained by the allow-list and on
// any allow-list entry that no longer reaches its target, so the list
// shrinks as the migrations land. A target is reviewed in the inventory
// exactly when no allow-list entry names it.

var scaleP21SW292Root = "."

type scaleP21SW292Func struct {
	key, pkg, file string
	decl           *ast.FuncDecl
	imports        map[string]bool // the file's import names
	scope          *types.Scope    // the package scope
}

type scaleP21SW292Ref struct {
	from, to, file string
	line           int
	kind           string // call, value or route
	offOnly        bool
	async          bool
	byName         bool // an unresolved receiver: every package method of that name
	authOnly       bool // runs only with authentication disabled (development mode)
	pattern        string
}

type scaleP21SW292Graph struct {
	fset      *token.FileSet
	info      *types.Info
	pkgs      map[*types.Package]string // package -> server or store
	funcs     map[string]*scaleP21SW292Func
	byName    map[string][]string
	named     []*types.Named
	refs      []scaleP21SW292Ref
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
func (g *scaleP21SW292Graph) enter(fn *scaleP21SW292Func) func() {
	imports, scope := g.imports, g.scope
	g.imports, g.scope = fn.imports, fn.scope
	return func() { g.imports, g.scope = imports, scope }
}

type scaleP21SW292Importer struct {
	store *types.Package
}

func (importer scaleP21SW292Importer) Import(path string) (*types.Package, error) {
	if path == "flow/api/internal/store" && importer.store != nil {
		return importer.store, nil
	}
	imported := types.NewPackage(path, path[strings.LastIndex(path, "/")+1:])
	imported.MarkComplete()
	return imported, nil
}

func scaleP21SW292Source(set *token.FileSet, node ast.Node) string {
	var text bytes.Buffer
	if err := format.Node(&text, set, node); err != nil {
		return fmt.Sprintf("<%T>", node)
	}
	return text.String()
}

// key names a package function as server/name or store/name and a method as
// server/Type.name or store/Type.name. Interface methods have no key.
func (g *scaleP21SW292Graph) key(fn *types.Func) string {
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

func (g *scaleP21SW292Graph) parse(t *testing.T, dir string) []*ast.File {
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

func scaleP21SW292Load(t *testing.T) *scaleP21SW292Graph {
	t.Helper()
	graph := &scaleP21SW292Graph{
		fset: token.NewFileSet(), pkgs: map[*types.Package]string{}, funcs: map[string]*scaleP21SW292Func{},
		byName: map[string][]string{}, into: map[string][]int{}, pkgLevel: map[string][]string{},
		onTrue: map[string]bool{}, onTrueRun: map[string]bool{}, flagVars: map[types.Object][]types.Object{},
		prepared: map[string]bool{}, single: map[string]map[types.Object]int{},
		info: &types.Info{Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{},
			Selections: map[*ast.SelectorExpr]*types.Selection{}, Types: map[ast.Expr]types.TypeAndValue{}},
	}
	storeFiles := graph.parse(t, filepath.Join(scaleP21SW292Root, "../../internal/store"))
	storeConfig := types.Config{Importer: scaleP21SW292Importer{}, Error: func(error) {}}
	storePackage, _ := storeConfig.Check("flow/api/internal/store", graph.fset, storeFiles, graph.info)
	graph.pkgs[storePackage] = "store"
	serverFiles := graph.parse(t, scaleP21SW292Root)
	serverConfig := types.Config{Importer: scaleP21SW292Importer{store: storePackage}, Error: func(error) {}}
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
				graph.funcs[key] = &scaleP21SW292Func{key: key, pkg: pkg, file: name, decl: fn, imports: imports, scope: scopes[pkg]}
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

func (g *scaleP21SW292Graph) declKey(pkg, file string, fn *ast.FuncDecl) string {
	if fn.Name.Name == "init" && fn.Recv == nil {
		return pkg + "/init@" + file
	}
	object, _ := g.info.Defs[fn.Name].(*types.Func)
	return g.key(object)
}

func scaleP21SW292RouteCall(node ast.Node) bool {
	call, ok := node.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && (selector.Sel.Name == "HandleFunc" || selector.Sel.Name == "Handle") && len(call.Args) == 2
}

// implementers returns the package methods called method on every package
// type whose method set names every method of iface.
func (g *scaleP21SW292Graph) implementers(iface *types.Interface, method string) []string {
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

func (g *scaleP21SW292Graph) targets(node ast.Node, parent ast.Node) []string {
	result, _ := g.resolve(node, parent)
	return result
}

// resolve also reports whether the targets come from the conservative
// by-name fallback for an unresolved receiver.
func (g *scaleP21SW292Graph) resolve(node ast.Node, parent ast.Node) ([]string, bool) {
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

func (g *scaleP21SW292Graph) collect(owner *scaleP21SW292Func) {
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
			if scaleP21SW292RouteCall(ancestor) && ancestors[index+1] != ancestor.(*ast.CallExpr).Fun {
				kind = "route"
			}
		}
		pattern := ""
		for index, ancestor := range ancestors[:len(ancestors)-1] {
			if scaleP21SW292RouteCall(ancestor) && ancestors[index+1] != ancestor.(*ast.CallExpr).Fun {
				if literal, ok := ancestor.(*ast.CallExpr).Args[0].(*ast.BasicLit); ok {
					pattern, _ = strconv.Unquote(literal.Value)
				}
			}
		}
		offOnly := g.offOnly(ancestors) || kind == "value" && g.rebound(owner, ancestors)
		async := slices.ContainsFunc(ancestors, func(node ast.Node) bool { _, ok := node.(*ast.GoStmt); return ok })
		authOnly := scaleP21SW292AuthOnly(ancestors)
		position := g.fset.Position(node.Pos())
		for _, target := range targets {
			g.into[target] = append(g.into[target], len(g.refs))
			g.refs = append(g.refs, scaleP21SW292Ref{from: owner.key, to: target, file: owner.pkg + "/" + owner.file, line: position.Line,
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
func (g *scaleP21SW292Graph) rebound(owner *scaleP21SW292Func, ancestors []ast.Node) bool {
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
func (g *scaleP21SW292Graph) prepare(fn *scaleP21SW292Func) {
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
func (g *scaleP21SW292Graph) conditionalFlag(expression ast.Expr) ([]types.Object, bool) {
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

func (g *scaleP21SW292Graph) flagReceiver(expression ast.Expr) bool {
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

func (g *scaleP21SW292Graph) flagCall(expression ast.Expr) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "BoundedMetadataEnabled" && g.flagReceiver(selector.X)
}

// residency reports s.metadataResidency compared with nil inside the store:
// on is true for !=, false for ==.
func (g *scaleP21SW292Graph) residency(expression *ast.BinaryExpr) (on, ok bool) {
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
func (g *scaleP21SW292Graph) onImplied(expression ast.Expr, known map[types.Object]bool) bool {
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
func (g *scaleP21SW292Graph) offImplied(expression ast.Expr, known map[types.Object]bool) bool {
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
func (g *scaleP21SW292Graph) resultOnTrue(key string, index, arity int) bool {
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

func scaleP21SW292Terminates(body *ast.BlockStmt) bool {
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
func (g *scaleP21SW292Graph) offOnly(ancestors []ast.Node) bool {
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
func (g *scaleP21SW292Graph) withConjuncts(known map[types.Object]bool, condition ast.Expr) map[types.Object]bool {
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

func (g *scaleP21SW292Graph) dominatedByOnReturn(list []ast.Stmt, child ast.Node, known map[types.Object]bool) bool {
	for _, statement := range list {
		if ast.Node(statement) == child {
			return false
		}
		branch, ok := statement.(*ast.IfStmt)
		if ok && branch.Init == nil && branch.Else == nil && g.onImplied(branch.Cond, known) && scaleP21SW292Terminates(branch.Body) {
			return true
		}
	}
	return false
}

type scaleP21SW292Review struct {
	Status, Temperature, TemperatureReason, Reason string
}

// scaleP21SW292Inventory reads both inventories keyed server/<file>:<name>
// and store/<file>:<name>.
func scaleP21SW292Inventory(t *testing.T) map[string]scaleP21SW292Review {
	t.Helper()
	result := map[string]scaleP21SW292Review{}
	for _, pkg := range []string{"server", "store"} {
		paths, err := filepath.Glob(filepath.Join(scaleP21SW292Root, "../../internal/store/scale_p21_inventory", pkg, "*.json"))
		if err != nil || len(paths) == 0 {
			t.Fatalf("%s inventory missing: %v", pkg, err)
		}
		for _, path := range paths {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var file struct {
				Consumers map[string]scaleP21SW292Review `json:"consumers"`
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

func (g *scaleP21SW292Graph) inventoryKey(key string) string {
	fn := g.funcs[key]
	if fn == nil {
		return ""
	}
	return fn.pkg + "/" + fn.file + ":" + fn.decl.Name.Name
}

func scaleP21SW292AuthDisabled(expression ast.Expr) bool {
	if paren, ok := expression.(*ast.ParenExpr); ok {
		return scaleP21SW292AuthDisabled(paren.X)
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "authDisabled" {
		return false
	}
	receiver, ok := selector.X.(*ast.Ident)
	return ok && receiver.Name == "s"
}

func scaleP21SW292AuthEnabled(expression ast.Expr) bool {
	unary, ok := expression.(*ast.UnaryExpr)
	return ok && unary.Op == token.NOT && scaleP21SW292AuthDisabled(unary.X)
}

func scaleP21SW292Conjuncts(expression ast.Expr) []ast.Expr {
	switch value := expression.(type) {
	case *ast.ParenExpr:
		return scaleP21SW292Conjuncts(value.X)
	case *ast.BinaryExpr:
		if value.Op == token.LAND {
			return append(scaleP21SW292Conjuncts(value.X), scaleP21SW292Conjuncts(value.Y)...)
		}
	}
	return []ast.Expr{expression}
}

// scaleP21SW292AuthOnly reports a node that runs only with authentication
// disabled: in the then-branch of if s.authDisabled [&& ...], the else-branch
// of if !s.authDisabled, or after if !s.authDisabled { ...; return }.
func scaleP21SW292AuthOnly(ancestors []ast.Node) bool {
	for index := len(ancestors) - 2; index >= 0; index-- {
		child := ancestors[index+1]
		var list []ast.Stmt
		switch parent := ancestors[index].(type) {
		case *ast.IfStmt:
			if child == parent.Body && slices.ContainsFunc(scaleP21SW292Conjuncts(parent.Cond), scaleP21SW292AuthDisabled) {
				return true
			}
			if parent.Else != nil && child == parent.Else && scaleP21SW292AuthEnabled(parent.Cond) {
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
			if ok && branch.Init == nil && branch.Else == nil && scaleP21SW292AuthEnabled(branch.Cond) && scaleP21SW292Terminates(branch.Body) {
				return true
			}
		}
	}
	return false
}

var scaleP21SW292Targets = []string{
	"store/SQLiteStore.installWrittenWorkspaceLocked",
	"store/SQLiteStore.refreshWorkspaceCopy",
	"store/SQLiteStore.refreshWorkspaceCopyLocked",
	"store/SQLiteStore.setWorkspaceLocked",
	"store/SQLiteStore.refreshMetadataRecords",
}

func scaleP21SW292Short(target string) string {
	return target[strings.LastIndex(target, ".")+1:]
}

type scaleP21SW292Boundary struct {
	mode, reason string
}

// scaleP21SW292Boundaries are the edges at which the walk stops, besides
// route registrations (the same edges SW286 pins for the store wrappers):
//   - "async": every reference runs inside a go statement that hands an AI
//     run to its worker after the HOT start boundary has committed;
//   - "handler": an in-process dispatch to a route handler that is also
//     bound by HandleFunc, so its temperature is that route's inventory
//     classification, exactly like a route registration.
var scaleP21SW292Boundaries = map[string]scaleP21SW292Boundary{
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
}

func scaleP21SW292Cut(ref scaleP21SW292Ref) bool {
	_, cut := scaleP21SW292Boundaries[ref.from+"->"+ref.to]
	return cut
}

// scaleP21SW292Closed are HOT inventory consumers that call a target
// directly but that nothing references with the flag on (no call, value,
// route or package-level reference outside a flag-off-only branch). Each
// names the existing callset test that pins its single flag-off entry.
var scaleP21SW292Closed = map[string]string{
	"store/SQLiteStore.mutateTeamMetadata": "TestScaleP21TeamMetadataCallset",
}

// scaleP21SW292Dispatches are the bounded dispatches in front of the chain:
// every reference from the first function to the second must be
// flag-off-only (it follows if s.metadataResidency != nil { return ... }).
var scaleP21SW292Dispatches = map[string]string{
	"store/SQLiteStore.writeAPIKeyUse->store/SQLiteStore.setWorkspaceLocked":                "API key telemetry installs into the logical generation (publishBoundedAPIKeyUse)",
	"store/SQLiteStore.refreshConflictedMetadata->store/SQLiteStore.refreshMetadataRecords": "write conflicts refresh the logical generation (refreshConflictedLogicalRecords)",
	"store/SQLiteStore.RefreshMetadataRecord->store/SQLiteStore.refreshMetadataRecords":     "a record refresh replaces the logical generation's record (refreshConflictedLogicalRecords)",
	"store/SQLiteStore.dispatchMutation->store/SQLiteStore.mutateTeamMetadata":              "the bounded dispatch block always returns before the legacy team writer",
	"store/SQLiteStore.mutateFull->store/SQLiteStore.refreshWorkspaceCopy":                  "the full writer refreshes its copy only without residency (a cold facade has none)",
	"store/SQLiteStore.createIssueRecords->store/SQLiteStore.refreshMetadataRecords":        "bounded issue creation retries through the logical refresh",
	"store/SQLiteStore.ReloadWorkspace->store/SQLiteStore.setWorkspaceLocked":               "a bounded reload installs the resident policy instead",
}

type scaleP21SW292Allowance struct {
	targets       []string // short target names the consumer reaches with the flag on
	owner, reason string
}

// scaleP21SW292Allowed names every consumer still migrating whose HOT paths
// reach a target with the flag on. The walk does not pass them. An entry must
// name exactly the targets it still reaches: the test fails when one no
// longer reaches a target, so the list shrinks as the migrations land.
var scaleP21SW292Allowed = map[string]scaleP21SW292Allowance{
	"store/SQLiteStore.UpdateIssueRecord": {targets: []string{"installWrittenWorkspaceLocked"},
		owner: "lead: setup fixtures (review/scale-p21-setup-fixtures, persistIssueMetadata) and issue update (issue_record_mutation.go)",
		reason: "the non-project hot-input path assigns progressMetadata from persistIssueMetadata, which still reads the policy-only copy on this branch; " +
			"once persistIssueMetadata returns nil on every flag-on path, replace this entry by a proof boundary pinning that return"},
}

type scaleP21SW292Reacher struct {
	key, path string
}

// reach walks every flag-on reference backwards from start. It returns the
// HOT inventory consumers it meets (closed consumers excepted) with the
// shortest path that reached each, and the allow-listed consumers in stop it
// met (it does not pass them).
func (g *scaleP21SW292Graph) reach(inventory map[string]scaleP21SW292Review, start string, stop map[string]bool) (reachers []scaleP21SW292Reacher, met, closed map[string]string) {
	type step struct{ key, path string }
	seen := map[string]bool{start: true}
	queue := []step{{start, start}}
	met, closed = map[string]string{}, map[string]string{}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, index := range g.into[current.key] {
			ref := g.refs[index]
			if ref.offOnly || ref.kind == "route" || seen[ref.from] || scaleP21SW292Cut(ref) {
				continue
			}
			seen[ref.from] = true
			path := fmt.Sprintf("%s -(%s %s:%d)-> %s", ref.from, ref.kind, ref.file, ref.line, current.path)
			if stop[ref.from] {
				met[ref.from] = path
				continue
			}
			if scaleP21SW292Closed[ref.from] != "" {
				closed[ref.from] = path
			} else if review := inventory[g.inventoryKey(ref.from)]; review.Temperature == "hot" && !slices.Contains(scaleP21SW292Targets, ref.from) {
				reachers = append(reachers, scaleP21SW292Reacher{ref.from, path})
			}
			queue = append(queue, step{ref.from, path})
		}
	}
	sort.Slice(reachers, func(i, j int) bool { return reachers[i].key < reachers[j].key })
	return reachers, met, closed
}

// unclassified walks flag-on references backwards from start through
// functions the inventory gives no temperature, and returns every route
// handler and startup root (main, init) it meets with its path.
func (g *scaleP21SW292Graph) unclassified(inventory map[string]scaleP21SW292Review, start string, stop map[string]bool) []string {
	paths := map[string]string{start: start}
	queue := []string{start}
	var result []string
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, index := range g.into[current] {
			ref := g.refs[index]
			if ref.offOnly || scaleP21SW292Cut(ref) {
				continue
			}
			if ref.kind == "route" {
				if paths["route:"+current] == "" {
					paths["route:"+current] = current
					result = append(result, fmt.Sprintf("route %q -> %s", ref.pattern, paths[current]))
				}
				continue
			}
			if paths[ref.from] != "" || stop[ref.from] {
				continue
			}
			paths[ref.from] = fmt.Sprintf("%s -(%s %s:%d)-> %s", ref.from, ref.kind, ref.file, ref.line, paths[current])
			review := inventory[g.inventoryKey(ref.from)]
			if review.Temperature != "" && !slices.Contains(scaleP21SW292Targets, ref.from) {
				continue
			}
			if ref.from == "server/main" || strings.Contains(ref.from, "/init@") {
				result = append(result, "startup "+paths[ref.from])
				continue
			}
			queue = append(queue, ref.from)
		}
	}
	sort.Strings(result)
	return result
}

func scaleP21SW292TestExists(t *testing.T, name string) bool {
	t.Helper()
	var paths []string
	for _, dir := range []string{scaleP21SW292Root, filepath.Join(scaleP21SW292Root, "../../internal/store")} {
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

func (g *scaleP21SW292Graph) verifyPins(t *testing.T) {
	t.Helper()
	for edge, boundary := range scaleP21SW292Boundaries {
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
	}
	for edge, reason := range scaleP21SW292Dispatches {
		from, to, _ := strings.Cut(edge, "->")
		count := 0
		for _, index := range g.into[to] {
			if ref := g.refs[index]; ref.from == from {
				count++
				if !ref.offOnly {
					t.Errorf("dispatch %s (%s) no longer dominates the reference at %s:%d", edge, reason, ref.file, ref.line)
				}
			}
		}
		if count == 0 {
			t.Errorf("stale dispatch %s", edge)
		}
	}
	for consumer, test := range scaleP21SW292Closed {
		if g.funcs[consumer] == nil {
			t.Errorf("closed consumer %s no longer exists", consumer)
		}
		if !scaleP21SW292TestExists(t, test) {
			t.Errorf("closed consumer %s cites a missing test %q", consumer, test)
		}
		if files := g.pkgLevel[consumer]; len(files) != 0 {
			t.Errorf("closed consumer %s is referenced at package level in %v", consumer, files)
		}
		for _, index := range g.into[consumer] {
			if ref := g.refs[index]; !ref.offOnly {
				t.Errorf("closed consumer %s is referenced with the flag on by %s (%s %s:%d)", consumer, ref.from, ref.kind, ref.file, ref.line)
			}
		}
	}
}

// TestScaleP21SW292WriteChainHotBoundaries computes, for each target of the
// legacy write/install/refresh chain, every HOT inventory consumer that can
// reach it with FLOW_BOUNDED_METADATA on, and fails on any not explained by
// the named allow-list (and on any allow-list entry that no longer reaches
// its target). A target is reviewed in the inventory exactly when no
// allow-list entry names it, and a pending target's reason names every
// remaining allow-listed consumer.
func TestScaleP21SW292WriteChainHotBoundaries(t *testing.T) {
	graph := scaleP21SW292Load(t)
	inventory := scaleP21SW292Inventory(t)
	graph.verifyPins(t)
	stop := map[string]bool{}
	for consumer, allowance := range scaleP21SW292Allowed {
		if graph.funcs[consumer] == nil {
			t.Errorf("allow-listed consumer %s no longer exists", consumer)
		}
		if allowance.owner == "" || allowance.reason == "" || len(allowance.targets) == 0 {
			t.Errorf("allow-list entry %s must name its owner, reason and targets", consumer)
		}
		stop[consumer] = true
	}
	passedClosed := map[string]bool{}
	pending := map[string][]string{}
	for _, target := range scaleP21SW292Targets {
		short := scaleP21SW292Short(target)
		if graph.funcs[target] == nil {
			t.Fatalf("target %s missing", target)
		}
		if files := graph.pkgLevel[target]; len(files) != 0 {
			t.Errorf("package-level reference to %s in %v requires reachability review", target, files)
		}
		reachers, met, closed := graph.reach(inventory, target, stop)
		for _, reacher := range reachers {
			t.Errorf("HOT %s reaches %s with FLOW_BOUNDED_METADATA on and is not allow-listed:\n  %s", reacher.key, short, reacher.path)
		}
		for _, entry := range graph.unclassified(inventory, target, stop) {
			t.Errorf("unclassified entry point reaches %s with FLOW_BOUNDED_METADATA on: %s", short, entry)
		}
		for consumer := range closed {
			passedClosed[consumer] = true
		}
		for consumer, allowance := range scaleP21SW292Allowed {
			listed := slices.Contains(allowance.targets, short)
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
	for consumer := range scaleP21SW292Closed {
		if review := inventory[graph.inventoryKey(consumer)]; review.Temperature != "hot" || review.Status != "reviewed" {
			t.Errorf("closed consumer %s is not a reviewed HOT inventory consumer (%+v)", consumer, review)
		}
		if !passedClosed[consumer] {
			t.Errorf("stale closed consumer %s: no target walk meets it", consumer)
		}
	}
	for _, target := range scaleP21SW292Targets {
		short := scaleP21SW292Short(target)
		key := graph.inventoryKey(target)
		review, ok := inventory[key]
		if !ok {
			t.Errorf("target %s has no inventory entry %s", short, key)
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

// TestScaleP21SW292GuardDetectsFlagOnReach runs the SW292 graph over a probe
// store and server: a refresh closure returned after a residency dispatch,
// a telemetry tail after an early bounded return and a dispatch block that
// always returns are flag-off-only; the same shapes without the dispatch, a
// goroutine launch and a closure capture reach the target with the flag on,
// and a HOT route through them is reported while an allow-listed or closed
// consumer is not passed.
func TestScaleP21SW292GuardDetectsFlagOnReach(t *testing.T) {
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
func (s *SQLiteStore) setWorkspaceLocked()            {}
func (s *SQLiteStore) logical() func(error) error     { return func(error) error { return nil } }
func (s *SQLiteStore) refreshDispatched() func(error) error {
	if s.metadataResidency != nil {
		return s.logical()
	}
	return func(error) error { s.setWorkspaceLocked(); return nil }
}
func (s *SQLiteStore) refreshOpen() func(error) error {
	return func(error) error { s.setWorkspaceLocked(); return nil }
}
func (s *SQLiteStore) telemetryDispatched() {
	if s.metadataResidency != nil {
		return
	}
	s.setWorkspaceLocked()
}
func (s *SQLiteStore) telemetryOpen() {
	if s.metadataResidency != nil {
		s.logical()
	}
	s.setWorkspaceLocked()
}
func (s *SQLiteStore) worker()  { go s.telemetryOpen() }
func (s *SQLiteStore) legacy()  { s.setWorkspaceLocked() }
func (s *SQLiteStore) Dispatch() {
	if s.BoundedMetadataEnabled() {
		return
	}
	s.legacy()
}
func (s *SQLiteStore) Write() { s.refreshOpen()(nil) }
`)
	write(filepath.Join(root, "cmd/server/main.go"), `package main

import "flow/api/internal/store"

type server struct{ store *store.SQLiteStore }

func (s *server) hot()     { s.store.Write() }
func (s *server) closed()  { s.store.Dispatch() }
func main()                {}
`)
	previous := scaleP21SW292Root
	scaleP21SW292Root = filepath.Join(root, "cmd/server")
	defer func() { scaleP21SW292Root = previous }()
	graph := scaleP21SW292Load(t)
	target := "store/SQLiteStore.setWorkspaceLocked"
	direct := map[string]bool{}
	for _, index := range graph.into[target] {
		if ref := graph.refs[index]; !ref.offOnly {
			direct[strings.TrimPrefix(ref.from, "store/SQLiteStore.")] = true
		}
	}
	want := map[string]bool{"refreshOpen": true, "telemetryOpen": true, "legacy": true}
	if !maps.Equal(direct, want) {
		t.Fatalf("flag-on references to the target = %v, want %v", direct, want)
	}
	for _, index := range graph.into["store/SQLiteStore.legacy"] {
		if ref := graph.refs[index]; !ref.offOnly {
			t.Fatalf("the always-returning dispatch block does not dominate %s:%d", ref.file, ref.line)
		}
	}
	inventory := map[string]scaleP21SW292Review{
		"server/main.go:hot":           {Status: "reviewed", Temperature: "hot"},
		"store/store.go:legacy":        {Status: "reviewed", Temperature: "hot"},
		"store/store.go:telemetryOpen": {Status: "reviewed", Temperature: "cold"},
	}
	reachers, met, _ := graph.reach(inventory, target, map[string]bool{})
	if len(reachers) != 2 || reachers[0].key != "server/server.hot" || reachers[1].key != "store/SQLiteStore.legacy" || len(met) != 0 {
		t.Fatalf("HOT reachers = %+v met=%v", reachers, met)
	}
	_, met, _ = graph.reach(inventory, target, map[string]bool{"store/SQLiteStore.Write": true})
	if met["store/SQLiteStore.Write"] == "" {
		t.Fatalf("an allow-listed consumer was passed or not met: %v", met)
	}
	if entries := graph.unclassified(inventory, target, map[string]bool{}); len(entries) != 0 {
		t.Fatalf("probe has no unclassified entry: %v", entries)
	}
}
