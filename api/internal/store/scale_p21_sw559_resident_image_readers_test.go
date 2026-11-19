package store

import (
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// SW559: with bounded metadata on, a store's installed image
// (SQLiteStore.workspaces) holds only the resident core and policy fields
// (metadataResidencyFields "core"/"policy", plus the team directory rebuilt
// from them). A reader that takes any other Bootstrap field from that image
// silently answers an empty or partial collection. This guard type-checks the
// store and domain packages, follows every value read out of the installed
// image (s.workspaces[...] and range s.workspaces) through locals, address and
// copies, store/domain calls taking a Bootstrap, and calls returning one built
// from it, and collects each non-resident Bootstrap field read from it per
// function. Reads in code that runs only with bounded metadata off (inside
// `if s.metadataResidency == nil`, after `if s.metadataResidency != nil {
// ...return }`, and the same forms of BoundedMetadataEnabled) are not image
// reads of a bounded store. Every remaining read must be reviewed in
// scaleP21SW559Reviewed with the reason it cannot reach a bounded skeleton.

type scaleP21SW559Func struct {
	key  string
	decl *ast.FuncDecl
	obj  *types.Func
	pkg  string
}

type scaleP21SW559Analysis struct {
	fset      *token.FileSet
	info      *types.Info
	bootstrap *types.Named
	funcs     map[*types.Func]*scaleP21SW559Func
	memo      map[scaleP21SW559Context]*scaleP21SW559Result
	resident  map[string]bool
	// refs[callee][caller] is true when caller names callee in code that
	// may run on a bounded skeleton.
	refs map[*types.Func]map[*types.Func]bool
	// named marks functions named anywhere at all.
	named map[*types.Func]bool
	// noGuards ignores the residency guards (the detection test).
	noGuards bool
}

// scaleP21SW559Context is a function analysed with the Bootstrap parameters
// in params (a bit per parameter index) holding an image value.
type scaleP21SW559Context struct {
	fn     *types.Func
	params uint64
}

type scaleP21SW559Result struct {
	// fields maps each non-resident field read from the image to the call
	// path below this function that reads it ("" when read here).
	fields  map[string]string
	returns bool
	done    bool
}

type scaleP21SW559Importer struct {
	domain *types.Package
	source types.Importer
}

func (i scaleP21SW559Importer) Import(path string) (*types.Package, error) {
	if path == "flow/api/internal/domain" && i.domain != nil {
		return i.domain, nil
	}
	if !strings.Contains(strings.Split(path, "/")[0], ".") && !strings.HasPrefix(path, "flow/") {
		return i.source.Import(path)
	}
	fake := types.NewPackage(path, path[strings.LastIndex(path, "/")+1:])
	fake.MarkComplete()
	return fake, nil
}

func scaleP21SW559Parse(t *testing.T, fset *token.FileSet, dir string) []*ast.File {
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
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	return files
}

// scaleP21SW559ResidentFields are the Bootstrap Go fields a bounded image
// holds: core and policy fields by their JSON names, and the team directory
// RebuildTeamDirectory derives from them on install.
func scaleP21SW559ResidentFields() map[string]bool {
	resident := map[string]bool{"TeamByID": true, "TeamByKey": true, "TeamChildren": true}
	kind := reflect.TypeFor[domain.Bootstrap]()
	for i := range kind.NumField() {
		field := kind.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if mode := metadataResidencyFields()[name]; mode == "core" || mode == "policy" {
			resident[field.Name] = true
		}
	}
	return resident
}

func scaleP21SW559Load(t *testing.T) *scaleP21SW559Analysis {
	t.Helper()
	fset := token.NewFileSet()
	info := &types.Info{
		Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{}, Types: map[ast.Expr]types.TypeAndValue{},
	}
	source := importer.ForCompiler(fset, "source", nil)
	domainFiles := scaleP21SW559Parse(t, fset, "../domain")
	domainConfig := types.Config{Importer: scaleP21SW559Importer{source: source}, Error: func(error) {}}
	domainPkg, _ := domainConfig.Check("flow/api/internal/domain", fset, domainFiles, info)
	storeFiles := scaleP21SW559Parse(t, fset, ".")
	storeConfig := types.Config{Importer: scaleP21SW559Importer{domain: domainPkg, source: source}, Error: func(error) {}}
	storePkg, _ := storeConfig.Check("flow/api/internal/store", fset, storeFiles, info)
	bootstrap, _ := domainPkg.Scope().Lookup("Bootstrap").Type().(*types.Named)
	if bootstrap == nil {
		t.Fatal("domain.Bootstrap did not type-check")
	}
	a := &scaleP21SW559Analysis{
		fset: fset, info: info, bootstrap: bootstrap, funcs: map[*types.Func]*scaleP21SW559Func{},
		memo: map[scaleP21SW559Context]*scaleP21SW559Result{}, resident: scaleP21SW559ResidentFields(),
		refs: map[*types.Func]map[*types.Func]bool{}, named: map[*types.Func]bool{},
	}
	for pkgName, files := range map[string][]*ast.File{"domain": domainFiles, "store": storeFiles} {
		for _, file := range files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				obj, _ := info.Defs[fn.Name].(*types.Func)
				if obj == nil {
					continue
				}
				key := pkgName + "/" + fn.Name.Name
				if fn.Recv != nil && len(fn.Recv.List) == 1 {
					recv := fn.Recv.List[0].Type
					if star, ok := recv.(*ast.StarExpr); ok {
						recv = star.X
					}
					if index, ok := recv.(*ast.IndexExpr); ok {
						recv = index.X
					}
					if ident, ok := recv.(*ast.Ident); ok {
						key = pkgName + "/" + ident.Name + "." + fn.Name.Name
					}
				}
				a.funcs[obj] = &scaleP21SW559Func{key: key, decl: fn, obj: obj, pkg: pkgName}
			}
		}
	}
	_ = storePkg
	return a
}

func (a *scaleP21SW559Analysis) isBootstrap(kind types.Type) bool {
	if kind == nil {
		return false
	}
	if pointer, ok := kind.(*types.Pointer); ok {
		kind = pointer.Elem()
	}
	named, ok := kind.(*types.Named)
	return ok && named.Obj() == a.bootstrap.Obj()
}

// imageIndex reports whether expr reads the installed image map of a store.
func (a *scaleP21SW559Analysis) imageMap(expr ast.Expr) bool {
	selector, ok := ast.Unparen(expr).(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "workspaces" {
		return false
	}
	selection := a.info.Selections[selector]
	if selection == nil || selection.Kind() != types.FieldVal {
		return false
	}
	recv := selection.Recv()
	if pointer, ok := recv.(*types.Pointer); ok {
		recv = pointer.Elem()
	}
	named, ok := recv.(*types.Named)
	return ok && named.Obj().Name() == "SQLiteStore"
}

type scaleP21SW559Walk struct {
	a       *scaleP21SW559Analysis
	fn      *scaleP21SW559Func
	result  *scaleP21SW559Result
	tainted map[types.Object]bool
	changed bool
	// legacy > 0 while walking code that runs only on a complete image.
	legacy int
	// collect records references instead of following the image.
	collect bool
	// flags holds the guard class of local booleans (certified copies,
	// handled legacy views).
	flags map[types.Object]int
	// complete marks stores holding a complete image: COLD facades handed to
	// callbacks and legacy views (coldAccessorView).
	complete map[types.Object]bool
	// assigned marks selectors written by an assignment, not read.
	assigned map[*ast.SelectorExpr]bool
}

// analyze answers what fn reads from the image when the parameters in params
// hold image values, and whether it may return an image value.
func (a *scaleP21SW559Analysis) analyze(fn *types.Func, params uint64) *scaleP21SW559Result {
	key := scaleP21SW559Context{fn, params}
	if result := a.memo[key]; result != nil {
		return result
	}
	result := &scaleP21SW559Result{fields: map[string]string{}}
	a.memo[key] = result
	decl := a.funcs[fn]
	if decl == nil {
		result.done = true
		return result
	}
	w := &scaleP21SW559Walk{a: a, fn: decl, result: result, tainted: map[types.Object]bool{}}
	signature := fn.Type().(*types.Signature)
	for index := range signature.Params().Len() {
		if params&(1<<index) != 0 {
			w.tainted[signature.Params().At(index)] = true
		}
	}
	for range 16 {
		w.changed = false
		w.block(decl.decl.Body.List)
		if !w.changed {
			break
		}
	}
	result.done = true
	return result
}

func (w *scaleP21SW559Walk) taintedExpr(expr ast.Expr) bool {
	switch e := ast.Unparen(expr).(type) {
	case *ast.IndexExpr:
		return w.image(e.X)
	case *ast.Ident:
		obj := w.a.info.Uses[e]
		if obj == nil {
			obj = w.a.info.Defs[e]
		}
		return obj != nil && w.tainted[obj]
	case *ast.UnaryExpr:
		return e.Op == token.AND && w.taintedExpr(e.X)
	case *ast.StarExpr:
		return w.taintedExpr(e.X)
	case *ast.CallExpr:
		if !w.a.isBootstrap(w.a.info.Types[e].Type) {
			return false
		}
		if tv, ok := w.a.info.Types[e.Fun]; ok && tv.IsType() && len(e.Args) == 1 {
			return w.taintedExpr(e.Args[0])
		}
		callee := w.a.callee(e)
		if callee == nil || w.a.funcs[callee] == nil {
			return false
		}
		return w.a.analyze(callee, w.mask(callee, e)).returns
	}
	return false
}

// mask is the set of callee Bootstrap parameters an image value reaches.
func (w *scaleP21SW559Walk) mask(callee *types.Func, call *ast.CallExpr) uint64 {
	signature := callee.Type().(*types.Signature)
	mask := uint64(0)
	for i, arg := range call.Args {
		index := i
		if signature.Variadic() && index >= signature.Params().Len()-1 {
			index = signature.Params().Len() - 1
		}
		if index < signature.Params().Len() && index < 64 && w.a.isBootstrap(signature.Params().At(index).Type()) && w.taintedExpr(arg) {
			mask |= 1 << index
		}
	}
	return mask
}

func (a *scaleP21SW559Analysis) callee(call *ast.CallExpr) *types.Func {
	var ident *ast.Ident
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		ident = fun
	case *ast.SelectorExpr:
		ident = fun.Sel
	case *ast.IndexExpr:
		if sel, ok := fun.X.(*ast.SelectorExpr); ok {
			ident = sel.Sel
		} else if id, ok := fun.X.(*ast.Ident); ok {
			ident = id
		}
	}
	if ident == nil {
		return nil
	}
	fn, _ := a.info.Uses[ident].(*types.Func)
	if fn == nil {
		return nil
	}
	return fn.Origin()
}

func (w *scaleP21SW559Walk) taint(obj types.Object) {
	if obj != nil && !w.tainted[obj] {
		w.tainted[obj] = true
		w.changed = true
	}
}

func (w *scaleP21SW559Walk) objectOf(expr ast.Expr) types.Object {
	ident, ok := ast.Unparen(expr).(*ast.Ident)
	if !ok {
		return nil
	}
	if obj := w.a.info.Defs[ident]; obj != nil {
		return obj
	}
	return w.a.info.Uses[ident]
}

// guard classifies a condition: +1 when it holds only on a complete image
// (bounded metadata off, or a COLD facade), -1 when it holds whenever the
// image may be a bounded skeleton, 0 otherwise.
func (w *scaleP21SW559Walk) guard(cond ast.Expr) int {
	if w.a.noGuards {
		return 0
	}
	switch e := ast.Unparen(cond).(type) {
	case *ast.BinaryExpr:
		switch e.Op {
		case token.LAND:
			if w.guard(e.X) > 0 || w.guard(e.Y) > 0 {
				return 1
			}
			return 0
		case token.LOR:
			x, y := w.guard(e.X), w.guard(e.Y)
			if x < 0 || y < 0 {
				return -1
			}
			if x > 0 && y > 0 {
				return 1
			}
			return 0
		case token.EQL, token.NEQ:
		default:
			return 0
		}
		selector, ok := ast.Unparen(e.X).(*ast.SelectorExpr)
		if !ok {
			return 0
		}
		if ident, ok := ast.Unparen(e.Y).(*ast.Ident); !ok || ident.Name != "nil" {
			return 0
		}
		switch {
		case selector.Sel.Name == "metadataResidency" && e.Op == token.EQL:
			return 1
		case selector.Sel.Name == "metadataResidency":
			return -1
		case selector.Sel.Name == "coldParent" && e.Op == token.NEQ:
			return 1
		}
	case *ast.UnaryExpr:
		if e.Op == token.NOT {
			return -w.guard(e.X)
		}
	case *ast.Ident:
		// A copy certified exact at a metadata revision is a complete image:
		// a bounded image never is (copyRevisionLocked). A coldAccessorView
		// result that handled the read served it from a legacy view.
		if obj := w.objectOf(e); obj != nil {
			return w.flags[obj]
		}
	case *ast.CallExpr:
		if selector, ok := ast.Unparen(e.Fun).(*ast.SelectorExpr); ok {
			switch selector.Sel.Name {
			case "BoundedMetadataEnabled":
				return -1
			case "skeletalRead", "skeletalFields":
				// A production bounded store refuses the read (SW531).
				return -1
			}
		}
	}
	return 0
}

// flag records local booleans that classify the image: the exactness result
// of copyRevisionLocked/copyRevisionOf (true only on a complete image), a
// conjunction of one, and the handled result of coldAccessorView (true
// whenever a bounded store served the read from a legacy view).
func (w *scaleP21SW559Walk) flag(s *ast.AssignStmt) {
	if w.flags == nil {
		w.flags = map[types.Object]int{}
	}
	if len(s.Rhs) == 1 && len(s.Lhs) > 1 {
		for _, lhs := range s.Lhs {
			// The package's convention for a read a bounded store served
			// itself (coldAccessorView, installedCycleForCalendar).
			if ident, ok := lhs.(*ast.Ident); ok && ident.Name == "handled" {
				if obj := w.objectOf(ident); obj != nil {
					w.flags[obj] = -1
				}
			}
		}
		if call, ok := ast.Unparen(s.Rhs[0]).(*ast.CallExpr); ok {
			if selector, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr); ok {
				switch selector.Sel.Name {
				case "copyRevisionLocked", "copyRevisionOf":
					if obj := w.objectOf(s.Lhs[1]); obj != nil {
						w.flags[obj] = 1
					}
				case "coldAccessorView", "coldAccessorViewKept":
					if len(s.Lhs) == 3 {
						if obj := w.objectOf(s.Lhs[0]); obj != nil {
							w.markComplete(obj)
						}
						if obj := w.objectOf(s.Lhs[2]); obj != nil {
							w.flags[obj] = -1
						}
					}
				}
			}
		}
		return
	}
	for i := range min(len(s.Lhs), len(s.Rhs)) {
		if g := w.guard(s.Rhs[i]); g > 0 {
			if obj := w.objectOf(s.Lhs[i]); obj != nil {
				w.flags[obj] = g
			}
		}
	}
}

// image reports whether expr is the installed image map of a store that may
// be bounded (not a COLD facade or legacy view).
func (w *scaleP21SW559Walk) image(expr ast.Expr) bool {
	if !w.a.imageMap(expr) {
		return false
	}
	selector := ast.Unparen(expr).(*ast.SelectorExpr)
	ident, ok := ast.Unparen(selector.X).(*ast.Ident)
	return !ok || !w.complete[w.objectOf(ident)]
}

// funcLit walks a function literal; its *SQLiteStore parameters are COLD
// facades (every such callback in the package receives one).
func (w *scaleP21SW559Walk) funcLit(lit *ast.FuncLit) {
	for _, field := range lit.Type.Params.List {
		for _, name := range field.Names {
			if obj := w.a.info.Defs[name]; obj != nil && scaleP21SW559StoreType(obj.Type()) {
				w.markComplete(obj)
			}
		}
	}
	w.block(lit.Body.List)
}

func (w *scaleP21SW559Walk) markComplete(obj types.Object) {
	if w.complete == nil {
		w.complete = map[types.Object]bool{}
	}
	w.complete[obj] = true
}

func scaleP21SW559StoreType(kind types.Type) bool {
	pointer, ok := kind.(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := pointer.Elem().(*types.Named)
	return ok && named.Obj().Name() == "SQLiteStore"
}

func scaleP21SW559Terminates(block *ast.BlockStmt) bool {
	if block == nil || len(block.List) == 0 {
		return false
	}
	switch last := block.List[len(block.List)-1].(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BranchStmt:
		return last.Tok == token.CONTINUE || last.Tok == token.BREAK || last.Tok == token.GOTO
	case *ast.ExprStmt:
		if call, ok := last.X.(*ast.CallExpr); ok {
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "panic" {
				return true
			}
		}
	}
	return false
}

func (w *scaleP21SW559Walk) block(list []ast.Stmt) {
	depth := w.legacy
	for _, stmt := range list {
		w.stmt(stmt)
		if ifStmt, ok := stmt.(*ast.IfStmt); ok && ifStmt.Else == nil && w.guard(ifStmt.Cond) < 0 && scaleP21SW559Terminates(ifStmt.Body) {
			// After `if bounded { ... return }` the rest runs on a complete image.
			w.legacy++
		}
	}
	w.legacy = depth
}

func (w *scaleP21SW559Walk) stmt(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.IfStmt:
		if s.Init != nil {
			w.stmt(s.Init)
		}
		w.expr(s.Cond)
		g := w.guard(s.Cond)
		if g > 0 {
			w.legacy++
		}
		w.block(s.Body.List)
		if g > 0 {
			w.legacy--
		}
		if s.Else != nil {
			if g < 0 {
				w.legacy++
			}
			w.stmt(s.Else)
			if g < 0 {
				w.legacy--
			}
		}
		return
	case *ast.BlockStmt:
		w.block(s.List)
		return
	case *ast.AssignStmt:
		w.flag(s)
		if s.Tok == token.ASSIGN {
			if w.assigned == nil {
				w.assigned = map[*ast.SelectorExpr]bool{}
			}
			for _, lhs := range s.Lhs {
				if selector, ok := ast.Unparen(lhs).(*ast.SelectorExpr); ok {
					w.assigned[selector] = true
				}
			}
		}
		if len(s.Lhs) == len(s.Rhs) {
			for i := range s.Lhs {
				if w.taintedExpr(s.Rhs[i]) {
					w.taint(w.objectOf(s.Lhs[i]))
				}
			}
		} else if len(s.Rhs) == 1 && len(s.Lhs) > 0 {
			switch rhs := ast.Unparen(s.Rhs[0]).(type) {
			case *ast.IndexExpr:
				if w.taintedExpr(rhs) {
					w.taint(w.objectOf(s.Lhs[0]))
				}
			case *ast.CallExpr:
				callee := w.a.callee(rhs)
				if callee != nil && w.a.funcs[callee] != nil && w.a.analyze(callee, w.mask(callee, rhs)).returns {
					signature := callee.Type().(*types.Signature)
					for i := range min(signature.Results().Len(), len(s.Lhs)) {
						if w.a.isBootstrap(signature.Results().At(i).Type()) {
							w.taint(w.objectOf(s.Lhs[i]))
						}
					}
				}
			}
		}
	case *ast.RangeStmt:
		if w.image(s.X) && s.Value != nil {
			w.taint(w.objectOf(s.Value))
		}
	case *ast.DeclStmt:
		if gen, ok := s.Decl.(*ast.GenDecl); ok {
			for _, spec := range gen.Specs {
				if value, ok := spec.(*ast.ValueSpec); ok && len(value.Names) == len(value.Values) {
					for i := range value.Names {
						if w.taintedExpr(value.Values[i]) {
							w.taint(w.a.info.Defs[value.Names[i]])
						}
					}
				}
			}
		}
	case *ast.ReturnStmt:
		if len(s.Results) == 1 && w.legacy == 0 && !w.result.returns {
			// return helper(...) of a helper returning (image, ...).
			if call, ok := ast.Unparen(s.Results[0]).(*ast.CallExpr); ok {
				if tuple, ok := w.a.info.Types[call].Type.(*types.Tuple); ok {
					callee := w.a.callee(call)
					for i := range tuple.Len() {
						if w.a.isBootstrap(tuple.At(i).Type()) && callee != nil && w.a.funcs[callee] != nil && w.a.analyze(callee, w.mask(callee, call)).returns {
							w.result.returns = true
							w.changed = true
						}
					}
				}
			}
		}
		for _, result := range s.Results {
			if w.legacy == 0 && w.a.isBootstrap(w.a.info.Types[result].Type) && w.taintedExpr(result) && !w.result.returns {
				w.result.returns = true
				w.changed = true
			}
		}
	}
	ast.Inspect(stmt, func(node ast.Node) bool {
		switch n := node.(type) {
		case ast.Stmt:
			if n != stmt {
				w.stmt(n)
				return false
			}
		case *ast.FuncLit:
			w.funcLit(n)
			return false
		case ast.Expr:
			w.expr(n)
			return false
		}
		return true
	})
}

func (w *scaleP21SW559Walk) expr(expr ast.Expr) {
	ast.Inspect(expr, func(node ast.Node) bool {
		if w.collect {
			switch n := node.(type) {
			case *ast.FuncLit:
				w.funcLit(n)
				return false
			case *ast.SelectorExpr:
				// A method of a COLD facade or legacy view runs on a
				// complete image.
				if ident, ok := ast.Unparen(n.X).(*ast.Ident); ok && w.complete[w.objectOf(ident)] {
					if callee, ok := w.a.info.Uses[n.Sel].(*types.Func); ok && w.a.funcs[callee.Origin()] != nil {
						w.a.named[callee.Origin()] = true
					}
					return false
				}
			case *ast.Ident:
				if callee, ok := w.a.info.Uses[n].(*types.Func); ok && w.a.funcs[callee.Origin()] != nil {
					callee = callee.Origin()
					w.a.named[callee] = true
					if w.legacy == 0 {
						if w.a.refs[callee] == nil {
							w.a.refs[callee] = map[*types.Func]bool{}
						}
						w.a.refs[callee][w.fn.obj] = true
					}
				}
			}
			return true
		}
		switch n := node.(type) {
		case *ast.FuncLit:
			w.funcLit(n)
			return false
		case *ast.SelectorExpr:
			if w.legacy == 0 && !w.assigned[n] && w.taintedExpr(n.X) {
				if selection := w.a.info.Selections[n]; selection != nil && w.a.isBootstrap(selection.Recv()) {
					name := n.Sel.Name
					if selection.Kind() == types.MethodVal {
						name += "()"
					}
					w.read(name, "")
				}
			}
		case *ast.CallExpr:
			if w.legacy != 0 {
				return true
			}
			if tv, ok := w.a.info.Types[n.Fun]; ok && (tv.IsType() || tv.IsBuiltin()) {
				return true
			}
			callee := w.a.callee(n)
			if callee != nil && w.a.funcs[callee] != nil {
				if mask := w.mask(callee, n); mask != 0 {
					sub := w.a.analyze(callee, mask)
					for field, via := range sub.fields {
						path := w.a.funcs[callee].key
						if via != "" {
							path += " > " + via
						}
						w.read(field, path)
					}
				}
				return true
			}
			for _, arg := range n.Args {
				if w.taintedExpr(arg) {
					name := "?"
					if callee != nil {
						name = callee.FullName()
					}
					w.read("whole", name)
				}
			}
		}
		return true
	})
}

func (a *scaleP21SW559Analysis) sorted() []*scaleP21SW559Func {
	funcs := make([]*scaleP21SW559Func, 0, len(a.funcs))
	for _, fn := range a.funcs {
		funcs = append(funcs, fn)
	}
	sort.Slice(funcs, func(i, j int) bool {
		return funcs[i].key < funcs[j].key || funcs[i].key == funcs[j].key && funcs[i].decl.Pos() < funcs[j].decl.Pos()
	})
	return funcs
}

func (w *scaleP21SW559Walk) read(field, via string) {
	if w.a.resident[field] {
		return
	}
	if previous, ok := w.result.fields[field]; ok && (previous == "" || previous <= via) {
		return
	}
	w.result.fields[field] = via
}

// reachable answers the store functions that may run on a bounded
// skeleton: exported ones, ones never named in the package (interface
// dispatch, tests), and those any reachable function names outside a
// complete-image-only branch.
func (a *scaleP21SW559Analysis) reachable() map[*types.Func]bool {
	for _, fn := range a.sorted() {
		w := &scaleP21SW559Walk{a: a, fn: fn, result: &scaleP21SW559Result{fields: map[string]string{}}, tainted: map[types.Object]bool{}, collect: true}
		w.block(fn.decl.Body.List)
	}
	reached := map[*types.Func]bool{}
	var queue []*types.Func
	for _, fn := range a.sorted() {
		if fn.pkg == "store" && (fn.obj.Exported() || !a.named[fn.obj] || fn.decl.Name.Name == "init") {
			reached[fn.obj] = true
			queue = append(queue, fn.obj)
		}
	}
	callees := map[*types.Func][]*types.Func{}
	for callee, callers := range a.refs {
		for caller := range callers {
			callees[caller] = append(callees[caller], callee)
		}
	}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for _, callee := range callees[next] {
			if !reached[callee] {
				reached[callee] = true
				queue = append(queue, callee)
			}
		}
	}
	return reached
}

// scaleP21SW559OpenStage is the review of the startup data migrations.
const scaleP21SW559OpenStage = "OpenDatabase stage or versioned data migration on context.Background() before the store serves (SW557 review): " +
	"a bounded open loads the resident image (loadResidentWorkspaces), which refuses a database whose split records or catalog are incomplete, " +
	"so every legacy layout the pass migrates was migrated by the flag-off open that split it; TestScaleP21SW557BoundedStartupMatchesLegacy " +
	"finds identical rows after both opens."

// scaleP21SW559Reviewed is every store function that, with bounded metadata
// possibly on, reads a Bootstrap field the bounded image does not hold (or
// hands the image whole to code the walk cannot follow, "whole"), with the
// exact field set and why the read cannot answer from the skeleton. A new
// such read, or a changed field set, fails until it is reviewed; the fix is
// to read the field from the installed logical source (a legacy view,
// coldAccessorView, or the installed field decode), as the fixed readers do.
var scaleP21SW559Reviewed = map[string]struct{ fields, reason string }{
	"store/SQLiteStore.AccountCollections": {"Passkeys UserSettings",
		"Flag-off legacy accessor: every server call is flag-off-only (server SW559 test); bounded callers use BeginAccountCollectionRead (server accountCollections)."},
	"store/SQLiteStore.MetadataRecord": {"whole",
		"Flag-off legacy accessor (realtime event policy, gated on BoundedMetadataEnabled); pinned by the server SW559 test."},
	"store/SQLiteStore.TrashRecordHeader": {"Trash",
		"Flag-off legacy accessor: documentTrashAuthorizationHeaders returns before it when bounded; pinned by the server SW559 test."},
	"store/SQLiteStore.OAuthApplicationsWithLogo": {"OAuthApplications",
		"Flag-off legacy accessor (icon resolution reads it only flag off); pinned by the server SW559 test."},
	"store/SQLiteStore.AcceptInvitationWithChanges": {"Issues",
		"IssueCount is len(Issues) of the workspace copy: with record hydration (which bounded requires) every image holds Issues nil " +
			"(migrateIssueCollections), so it is 0 both ways."},
	"store/SQLiteStore.PulseFeed": {"InitiativeRelations InitiativeUpdates Initiatives ProjectStatuses ProjectUpdates Projects SavedViews Settings Subscriptions UserSettings Users",
		"Flag-off legacy accessor: every call dispatches to BeginPulseRead when bounded (chat cards: pulse != nil, their callers' bounded branches); pinned by TestScaleP21PulseLegacyFeedBoundary."},
	"store/SQLiteStore.PulseSources": {"InitiativeRelations InitiativeUpdates Initiatives ProjectStatuses ProjectUpdates Projects SavedViews Settings Subscriptions UserSettings Users",
		"Flag-off legacy accessor: its three dispatches are off-only (beginPulseRead and the realtime sources); pinned by TestScaleP21PulseLegacySourcesBoundary."},
	"store/SQLiteStore.BeginProjectTemplatesRead": {"Activities Comments Issues NotificationDeliveries Notifications ProjectTemplates",
		"ProjectTemplates sizes the admission only; bounded, the templates are decoded from the installed source (installedProjectTemplates). " +
			"The issue/content fields are nil-checks that are nil in both images (record hydration keeps them out of every image)."},
	"store/SQLiteStore.DeleteWorkflowStateRecords": {"States",
		"Reads the states from SQL (workspace_metadata_records); States of the image copy is only patched, and it is nil in the skeleton before and after."},
	"store/SQLiteStore.ResolveAccess": {"TeamMembers whole",
		"The image is used only when its copy revision is certified, which a bounded store never is (copyRevisionLocked); bounded reads the SQL " +
			"policy records (resourcePolicyRecords), and TeamMembers is assigned from SQL."},
	"store/SQLiteStore.UpdateIssueRecord": {"ActivityHistoryError ActivityHistoryHeads ActivityHistoryLoader Documents ImportJobs MigrationJobs NextIssueNumber Projects States Trash Users ViewerTeamIDs whole",
		"Bounded issue writer: its callback inputs are installed from the logical source (loadIssueWorkflowStates, loadIssueHotInputs, " +
			"loadIssueProjectInputs; issueMoveAudience from SQL), and an update outside those shapes refuses with ErrBoundedMetadataCoverage."},
	"store/SQLiteStore.backfillWorkspaceProjectProgress": {"Activities Comments ImportJobs Issues LabelIndex Labels MigrationJobs NotificationDeliveries Notifications Projects States Users whole",
		"OPEN (reported, not fixed here): the unversioned startup backfill (backfillAllProjectProgress) finds no project without a progress " +
			"history in the skeleton, so a bounded open never backfills a project created since the split; flag off it does. A fix runs the pass " +
			"on a COLD facade at open."},
	"store/SQLiteStore.boundedOAuthMetadataTransaction": {"whole",
		"The callback's copy is the policy copy with every record field it may read cleared (Users, APIKeys, OAuthAuthorizations, OAuthApplications, " +
			"Settings); it selects its records through the transaction state."},
	"store/SQLiteStore.captureLogicalTestView": {"NextIssueNumber whole", "Test-only: captures the owner's image (a complete flag-off image) into a test view."},
	"store/SQLiteStore.changeTeamMembership": {"LabelIndex Labels",
		"RebuildTeamDirectory derives LabelIndex from Labels alongside the resident team directory the membership change reads; LabelIndex is unused."},
	"store/SQLiteStore.installedOAuthApplications": {"whole",
		"Bounded, the applications come from the legacy view (coldAccessorView); visit receives the image only for its workspace record (BackfillUploadOwners)."},
	"store/SQLiteStore.migrateDocumentAccessBatch":      {"Documents ImportJobs MigrationJobs Trash Users whole", scaleP21SW559OpenStage},
	"store/SQLiteStore.migrateDocumentScopeBatch":       {"Documents ImportJobs MigrationJobs Projects Trash Users whole", scaleP21SW559OpenStage},
	"store/SQLiteStore.migrateIssueCollections":         {"Activities Comments ImportJobs Issues LabelIndex Labels MigrationJobs NotificationDeliveries Notifications Projects States Users whole", scaleP21SW559OpenStage},
	"store/SQLiteStore.migrateLoopRunRecords":           {"Activities Comments ImportJobs Issues LabelIndex Labels LoopRuns MigrationJobs NotificationDeliveries Notifications Projects States Users whole", scaleP21SW559OpenStage},
	"store/SQLiteStore.migrateWorkspaceCycleCapacity":   {"CycleSettings Cycles ImportJobs MigrationJobs Users whole", scaleP21SW559OpenStage},
	"store/SQLiteStore.migrateWorkspaceDocumentAccess":  {"Documents Trash", scaleP21SW559OpenStage},
	"store/SQLiteStore.migrateWorkspaceDocumentScope":   {"Documents Issues Projects Trash", scaleP21SW559OpenStage},
	"store/SQLiteStore.migrateWorkspaceMetadataRecords": {"Activities Comments ImportJobs Issues LabelIndex Labels MigrationJobs NotificationDeliveries Notifications Projects States Users whole", scaleP21SW559OpenStage},
	"store/SQLiteStore.migrateWorkspaceResourceSlugs":   {"ImportJobs Initiatives MigrationJobs Projects Users whole", scaleP21SW559OpenStage},
	"store/SQLiteStore.p19SnapshotAt": {"NextIssueNumber",
		"NextIssueNumber is json:\"-\": the snapshot's wire output never carries it; the projection reads the installed logical source."},
	"store/SQLiteStore.projectDirectoryInstalledSource": {"Projects",
		"Projects == nil detects a policy-only recovery only together with a missing logical source (the documented SQL fallback); otherwise the installed source is read."},
	"store/SQLiteStore.recordMetadataEvent": {"Cycles ImportJobs Issues Labels MigrationJobs Projects States TeamPinnedResources TeamResourceSections Users whole",
		"Its only caller (team_member.cleaned_up) names a team, which the resident team directory resolves; the other collections are the generic entity fallback."},
	"store/SQLiteStore.resourceReadCompatibility": {"Initiatives Projects",
		"The skeleton never matches the cached-copy fast path, so the record is normalized from SQL as one written after the copy is (bounded readers pass their owned read first)."},
	"store/SQLiteStore.updateWorkspace": {"IntegrationConnections LabelIndex Labels Settings Users whole",
		"Bounded, UpdateWorkspace runs it on a COLD facade (coldLegacyWrite); its routes are COLD (route temperature table), so the live store never runs it bounded."},
}

func TestScaleP21SW559ResidentImageReaders(t *testing.T) {
	a := scaleP21SW559Load(t)
	reached := a.reachable()
	found := map[string]map[string]string{}
	for _, fn := range a.sorted() {
		if fn.pkg != "store" || !reached[fn.obj] {
			continue
		}
		if result := a.analyze(fn.obj, 0); len(result.fields) != 0 {
			found[fn.key] = result.fields
		}
	}
	if len(found) == 0 || len(a.funcs) < 1000 {
		t.Fatalf("SW559: the walk found %d functions and %d image readers; it is stale", len(a.funcs), len(found))
	}
	keys := make([]string, 0, len(found))
	for key := range found {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fields := make([]string, 0, len(found[key]))
		paths := make([]string, 0, len(found[key]))
		for field, via := range found[key] {
			fields = append(fields, field)
			if via != "" {
				paths = append(paths, field+" via "+via)
			}
		}
		slices.Sort(fields)
		slices.Sort(paths)
		review, ok := scaleP21SW559Reviewed[key]
		if !ok {
			t.Errorf("SW559: %s reads %s from the installed image, which a bounded store holds without them (paths: %s); read them from the installed logical source or review the read",
				key, strings.Join(fields, " "), strings.Join(paths, "; "))
			continue
		}
		if review.fields != strings.Join(fields, " ") {
			t.Errorf("SW559: %s now reads %q from the installed image (reviewed: %q; paths: %s)", key, strings.Join(fields, " "), review.fields, strings.Join(paths, "; "))
		}
		if strings.TrimSpace(review.reason) == "" {
			t.Errorf("SW559: %s has no review reason", key)
		}
	}
	for key := range scaleP21SW559Reviewed {
		if found[key] == nil {
			t.Errorf("SW559: stale review %s: it no longer reads a non-resident field from the installed image", key)
		}
	}
}

// TestScaleP21SW559ResidentImageReadersDetect proves the walk finds the bug
// class: with the residency guards ignored, each reader this change fixed
// reads its record field from the image (the reads its legacy branch keeps),
// and with them it reads none.
func TestScaleP21SW559ResidentImageReadersDetect(t *testing.T) {
	want := map[string]string{
		"store/SQLiteStore.BeginGitLabConnectionConfigsRead": "IntegrationConnections",
		"store/SQLiteStore.CycleForCalendar":                 "Cycles",
		"store/SQLiteStore.TrashSnapshot":                    "Trash",
		"store/SQLiteStore.DocumentByID":                     "Documents",
		"store/SQLiteStore.ProjectByID":                      "Projects",
		"store/SQLiteStore.syncMemberDirectory":              "Users",
		"store/SQLiteStore.legacyAuditLogPending":            "AuditLog",
	}
	guarded, unguarded := scaleP21SW559Load(t), scaleP21SW559Load(t)
	unguarded.noGuards = true
	reached := guarded.reachable()
	seen := map[string]bool{}
	for _, fn := range guarded.sorted() {
		if _, ok := want[fn.key]; !ok || fn.pkg != "store" {
			continue
		}
		seen[fn.key] = true
		if !reached[fn.obj] {
			t.Errorf("%s is not reachable", fn.key)
		}
		if fields := guarded.analyze(fn.obj, 0).fields; len(fields) != 0 {
			t.Errorf("SW559 fixed reader %s still reads %v from the image", fn.key, fields)
		}
	}
	for _, fn := range unguarded.sorted() {
		if field, ok := want[fn.key]; ok && fn.pkg == "store" {
			if _, read := unguarded.analyze(fn.obj, 0).fields[field]; !read {
				t.Errorf("SW559 walk without guards does not see %s read %s", fn.key, field)
			}
		}
	}
	for key := range want {
		if !seen[key] {
			t.Errorf("%s not found", key)
		}
	}
}
