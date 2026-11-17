package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/build"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The SW282 guard closes the store wrappers SelectedDocumentMetadata and
// TrashRecordHeader by reachability over every non-test file of the module,
// not by names or comments. Each identifier spelling a wrapper name must be
// its own declaration or a direct call; a method value, method expression,
// interface method or field of that name fails. Each call must be
// flag-off-only, which holds when it is
//   - inside the then-branch of an if whose condition is false whenever
//     FLOW_BOUNDED_METADATA is on (!s.store.BoundedMetadataEnabled() && ...),
//   - inside the else-branch of an if whose condition is true whenever the
//     flag is on (s.store.BoundedMetadataEnabled() || ...), or
//   - after an earlier statement of an enclosing block of the form
//     if <true whenever the flag is on> { ...; return }.
// Function literals inherit the gating of the place they are created. No
// route, ticker, record-read mode (on/shadow/off) or development
// (auth-disabled) branch can therefore run a wrapper with the flag on.

type scaleP21SW282Use struct {
	file, function, kind string
	line                 int
	offOnly              bool
}

func scaleP21SW282Source(node ast.Node) string {
	var text bytes.Buffer
	if err := format.Node(&text, token.NewFileSet(), node); err != nil {
		return fmt.Sprintf("<%T>", node)
	}
	return text.String()
}

func scaleP21SW282Flag(expression ast.Expr) bool {
	text := scaleP21SW282Source(expression)
	return text == "s.store.BoundedMetadataEnabled()" || text == "s.BoundedMetadataEnabled()"
}

// scaleP21SW282OnImplied reports whether expression is true whenever the flag is on.
func scaleP21SW282OnImplied(expression ast.Expr) bool {
	switch value := expression.(type) {
	case *ast.ParenExpr:
		return scaleP21SW282OnImplied(value.X)
	case *ast.UnaryExpr:
		return value.Op == token.NOT && scaleP21SW282OffImplied(value.X)
	case *ast.BinaryExpr:
		switch value.Op {
		case token.LOR:
			return scaleP21SW282OnImplied(value.X) || scaleP21SW282OnImplied(value.Y)
		case token.LAND:
			return scaleP21SW282OnImplied(value.X) && scaleP21SW282OnImplied(value.Y)
		}
	case *ast.CallExpr:
		return scaleP21SW282Flag(value)
	}
	return false
}

// scaleP21SW282OffImplied reports whether expression is false whenever the flag is on.
func scaleP21SW282OffImplied(expression ast.Expr) bool {
	switch value := expression.(type) {
	case *ast.ParenExpr:
		return scaleP21SW282OffImplied(value.X)
	case *ast.UnaryExpr:
		return value.Op == token.NOT && scaleP21SW282OnImplied(value.X)
	case *ast.BinaryExpr:
		switch value.Op {
		case token.LAND:
			return scaleP21SW282OffImplied(value.X) || scaleP21SW282OffImplied(value.Y)
		case token.LOR:
			return scaleP21SW282OffImplied(value.X) && scaleP21SW282OffImplied(value.Y)
		}
	}
	return false
}

func scaleP21SW282Terminates(body *ast.BlockStmt) bool {
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

func scaleP21SW282DominatedByOnReturn(list []ast.Stmt, child ast.Node) bool {
	for _, statement := range list {
		if ast.Node(statement) == child {
			return false
		}
		branch, ok := statement.(*ast.IfStmt)
		if ok && branch.Init == nil && branch.Else == nil && scaleP21SW282OnImplied(branch.Cond) && scaleP21SW282Terminates(branch.Body) {
			return true
		}
	}
	return false
}

// scaleP21SW282OffOnly decides whether the last node of ancestors can execute
// with the flag on. ancestors[0] is the function body.
func scaleP21SW282OffOnly(ancestors []ast.Node) bool {
	for index := len(ancestors) - 2; index >= 0; index-- {
		child := ancestors[index+1]
		switch parent := ancestors[index].(type) {
		case *ast.IfStmt:
			if child == parent.Body && scaleP21SW282OffImplied(parent.Cond) {
				return true
			}
			if parent.Else != nil && child == parent.Else && scaleP21SW282OnImplied(parent.Cond) {
				return true
			}
		case *ast.BlockStmt:
			if scaleP21SW282DominatedByOnReturn(parent.List, child) {
				return true
			}
		case *ast.CaseClause:
			if scaleP21SW282DominatedByOnReturn(parent.Body, child) {
				return true
			}
		case *ast.CommClause:
			if scaleP21SW282DominatedByOnReturn(parent.Body, child) {
				return true
			}
		}
	}
	return false
}

func scaleP21SW282FunctionName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	receiver := fn.Recv.List[0].Type
	if pointer, ok := receiver.(*ast.StarExpr); ok {
		receiver = pointer.X
	}
	if named, ok := receiver.(*ast.Ident); ok {
		return named.Name + "." + fn.Name.Name
	}
	return "unknown." + fn.Name.Name
}

// scaleP21SW282Uses returns every identifier spelling one of names in the
// file, keyed by name, with its role and flag-on reachability.
func scaleP21SW282Uses(t *testing.T, path string, src []byte, names map[string]bool) (map[string][]scaleP21SW282Use, map[string]*ast.FuncDecl) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	uses := map[string][]scaleP21SW282Use{}
	decls := map[string]*ast.FuncDecl{}
	for _, decl := range file.Decls {
		fn, isFunc := decl.(*ast.FuncDecl)
		if isFunc && names[fn.Name.Name] && fn.Recv != nil {
			decls[fn.Name.Name] = fn
		}
		owner := "package-level"
		var root ast.Node = decl
		if isFunc {
			owner = scaleP21SW282FunctionName(fn)
			if fn.Body == nil {
				continue
			}
			if names[fn.Name.Name] && fn.Recv == nil {
				uses[fn.Name.Name] = append(uses[fn.Name.Name], scaleP21SW282Use{path, owner, "function", fset.Position(fn.Pos()).Line, false})
			}
			root = fn.Body
		}
		var ancestors []ast.Node
		ast.Inspect(root, func(node ast.Node) bool {
			if node == nil {
				ancestors = ancestors[:len(ancestors)-1]
				return false
			}
			ancestors = append(ancestors, node)
			ident, ok := node.(*ast.Ident)
			if !ok || !names[ident.Name] {
				return true
			}
			use := scaleP21SW282Use{file: path, function: owner, kind: "value", line: fset.Position(ident.Pos()).Line}
			if !isFunc {
				use.kind = "package-level"
			} else if len(ancestors) >= 3 {
				selector, isSelector := ancestors[len(ancestors)-2].(*ast.SelectorExpr)
				call, isCall := ancestors[len(ancestors)-3].(*ast.CallExpr)
				if isSelector && selector.Sel == ident && isCall && call.Fun == selector {
					use.kind = "call"
					use.offOnly = scaleP21SW282OffOnly(ancestors[:len(ancestors)-1])
				}
			}
			uses[ident.Name] = append(uses[ident.Name], use)
			return true
		})
	}
	return uses, decls
}

func scaleP21SW282ModuleUses(t *testing.T, root string, names map[string]bool) (map[string][]scaleP21SW282Use, map[string]*ast.FuncDecl) {
	t.Helper()
	uses := map[string][]scaleP21SW282Use{}
	decls := map[string]*ast.FuncDecl{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); path != root && (name == "testdata" || name == "vendor" || name == "node_modules" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if match, err := build.Default.MatchFile(filepath.Dir(path), filepath.Base(path)); err != nil || !match {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		matched := false
		for name := range names {
			matched = matched || bytes.Contains(src, []byte(name))
		}
		if !matched {
			return nil
		}
		relative, _ := filepath.Rel(root, path)
		fileUses, fileDecls := scaleP21SW282Uses(t, filepath.ToSlash(relative), src, names)
		for name, list := range fileUses {
			uses[name] = append(uses[name], list...)
		}
		for name, fn := range fileDecls {
			if decls[name] != nil {
				t.Errorf("%s is declared twice (%s)", name, relative)
			}
			decls[name] = fn
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return uses, decls
}

func TestScaleP21SW282DocumentAuthorizationWrapperBoundaries(t *testing.T) {
	names := map[string]bool{"SelectedDocumentMetadata": true, "TrashRecordHeader": true}
	uses, decls := scaleP21SW282ModuleUses(t, "../..", names)
	// Every reference in the module: declaration excluded, each a direct,
	// flag-off-only call. A new caller, function value or interface method
	// fails until it is reviewed.
	want := map[string][]string{
		"SelectedDocumentMetadata": {
			"cmd/server/document_record_scopes.go server.documentAuthorizationMetadata call off",
			"cmd/server/scale_p16_record_authorization.go server.projectRecordAuthorizationMetadata call off",
			"cmd/server/scale_p20_resource_reads.go server.scaleP20DocumentMetadata call off",
			"internal/store/scale_p21_document_read.go SQLiteStore.BeginDocumentRead call off",
		},
		"TrashRecordHeader": {
			"cmd/server/trash_record_scopes.go server.documentTrashAuthorizationHeaders call off",
		},
	}
	for name := range names {
		got := []string{}
		for _, use := range uses[name] {
			reach := "ON"
			if use.offOnly {
				reach = "off"
			}
			got = append(got, fmt.Sprintf("%s %s %s %s", use.file, use.function, use.kind, reach))
			if use.kind != "call" {
				t.Errorf("%s:%d %s references %s as a %s; explicit reachability review is required", use.file, use.line, use.function, name, use.kind)
			} else if !use.offOnly {
				t.Errorf("%s:%d %s reaches %s with FLOW_BOUNDED_METADATA on", use.file, use.line, use.function, name)
			}
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, want[name]) {
			t.Errorf("%s reference set changed:\n got=%q\nwant=%q", name, got, want[name])
		}
	}
	// The arbitrary selector also refuses by itself: its first statement
	// answers a bounded store before any resident or hydrated read.
	selected := decls["SelectedDocumentMetadata"]
	if selected == nil || scaleP21SW282Source(selected.Recv.List[0].Type) != "*SQLiteStore" {
		t.Fatal("SelectedDocumentMetadata declaration missing")
	}
	first, ok := selected.Body.List[0].(*ast.IfStmt)
	if !ok || first.Init != nil || first.Else != nil || scaleP21SW282Source(first.Cond) != "s.BoundedMetadataEnabled()" || len(first.Body.List) != 1 ||
		scaleP21SW282Source(first.Body.List[0]) != "return domain.Bootstrap{}, ErrBoundedMetadataCoverage" {
		t.Error("SelectedDocumentMetadata lost its first bounded refusal")
	}
	if header := decls["TrashRecordHeader"]; header == nil || scaleP21SW282Source(header.Recv.List[0].Type) != "*SQLiteStore" {
		t.Fatal("TrashRecordHeader declaration missing")
	}
}

func TestScaleP21SW283GuardRejectsFlagOnReach(t *testing.T) {
	const source = `package main

type server struct{ store interface{ BoundedMetadataEnabled() bool; SelectedDocumentMetadata() } }

func (s *server) gated() {
	if s.store.BoundedMetadataEnabled() {
		return
	}
	s.store.SelectedDocumentMetadata()
}

func (s *server) guarded(ok bool) {
	if s.store.BoundedMetadataEnabled() || !ok {
		return
	}
	func() { s.store.SelectedDocumentMetadata() }()
}

func (s *server) elseGated(ok bool) {
	if s.store.BoundedMetadataEnabled() || ok {
	} else {
		s.store.SelectedDocumentMetadata()
	}
}

func (s *server) negated() {
	if !s.store.BoundedMetadataEnabled() {
		s.store.SelectedDocumentMetadata()
	}
}

func (s *server) partial(ok bool) {
	if s.store.BoundedMetadataEnabled() && ok {
		return
	}
	s.store.SelectedDocumentMetadata()
}

func (s *server) fallsThrough() {
	if s.store.BoundedMetadataEnabled() {
		s.gated()
	}
	s.store.SelectedDocumentMetadata()
}

func (s *server) loop() {
	for i := 0; i < 3; i++ {
		if s.store.BoundedMetadataEnabled() {
			continue
		}
	}
	s.store.SelectedDocumentMetadata()
}

func (s *server) captured() func() {
	if s.store.BoundedMetadataEnabled() {
		return nil
	}
	return s.store.SelectedDocumentMetadata
}
`
	uses, _ := scaleP21SW282Uses(t, "probe.go", []byte(source), map[string]bool{"SelectedDocumentMetadata": true})
	got := map[string]string{}
	for _, use := range uses["SelectedDocumentMetadata"] {
		reach := use.kind
		if use.kind == "call" && !use.offOnly {
			reach = "call-on"
		}
		got[use.function] = reach
	}
	want := map[string]string{
		"package-level":      "package-level",
		"server.gated":       "call",
		"server.guarded":     "call",
		"server.elseGated":   "call",
		"server.negated":     "call",
		"server.partial":     "call-on",
		"server.fallsThrough": "call-on",
		"server.loop":        "call-on",
		"server.captured":    "value",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("guard reach=%v want=%v", got, want)
	}
}
