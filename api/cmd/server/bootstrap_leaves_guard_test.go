package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

type bootstrapLeavesGuardRef struct {
	File, Function string
	Call           bool
	Pos            token.Pos
}

type bootstrapLeavesPackage struct {
	fset  *token.FileSet
	files map[string]*ast.File
}

func parseBootstrapLeavesPackage(t *testing.T) bootstrapLeavesPackage {
	t.Helper()
	_, path, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate the server package")
	}
	directory := filepath.Dir(path)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	result := bootstrapLeavesPackage{fset: token.NewFileSet(), files: map[string]*ast.File{}}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(result.fset, filepath.Join(directory, name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		result.files[name] = file
	}
	if len(result.files) < 100 {
		t.Fatal("server package parse found too few production files", len(result.files))
	}
	return result
}

func (p bootstrapLeavesPackage) function(t *testing.T, name string) *ast.FuncDecl {
	t.Helper()
	var found *ast.FuncDecl
	for _, file := range p.files {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name {
				if found != nil {
					t.Fatal("duplicate production function", name)
				}
				found = fn
			}
		}
	}
	if found == nil || found.Body == nil {
		t.Fatal("missing production function", name)
	}
	return found
}

// references lists every identifier or selector naming name in production
// code (calls, method values and function values alike), by enclosing
// top-level function ("" for package-level declarations).
func (p bootstrapLeavesPackage) references(name string) []bootstrapLeavesGuardRef {
	refs := []bootstrapLeavesGuardRef{}
	for fileName, file := range p.files {
		for _, decl := range file.Decls {
			enclosing := ""
			var root ast.Node = decl
			if fn, ok := decl.(*ast.FuncDecl); ok {
				enclosing = fn.Name.Name
				if fn.Body == nil {
					continue
				}
				root = fn.Body
			}
			calls := map[ast.Node]bool{}
			ast.Inspect(root, func(node ast.Node) bool {
				if call, ok := node.(*ast.CallExpr); ok {
					calls[call.Fun] = true
				}
				switch node := node.(type) {
				case *ast.SelectorExpr:
					if node.Sel.Name == name {
						refs = append(refs, bootstrapLeavesGuardRef{fileName, enclosing, calls[node], node.Pos()})
					}
				case *ast.Ident:
					if node.Name == name && !selectorName(root, node) {
						refs = append(refs, bootstrapLeavesGuardRef{fileName, enclosing, calls[node], node.Pos()})
					}
				}
				return true
			})
		}
	}
	slices.SortFunc(refs, func(a, b bootstrapLeavesGuardRef) int {
		return strings.Compare(a.File+"/"+a.Function, b.File+"/"+b.Function)
	})
	for index := range refs {
		refs[index].Pos = 0
	}
	return refs
}

func selectorName(root ast.Node, ident *ast.Ident) bool {
	selected := false
	ast.Inspect(root, func(node ast.Node) bool {
		if selector, ok := node.(*ast.SelectorExpr); ok && selector.Sel == ident {
			selected = true
		}
		return !selected
	})
	return selected
}

// boundedFirst requires fn's first statement to be
// `if s.store.BoundedMetadataEnabled() { ...; return ... }` whose body names
// every wanted identifier.
func boundedFirst(t *testing.T, fn *ast.FuncDecl, wanted ...string) {
	t.Helper()
	if len(fn.Body.List) == 0 {
		t.Fatal("empty production function", fn.Name.Name)
	}
	branch, ok := fn.Body.List[0].(*ast.IfStmt)
	if !ok || branch.Init != nil || branch.Else != nil {
		t.Fatal("first statement is not the bounded branch", fn.Name.Name)
	}
	call, ok := branch.Cond.(*ast.CallExpr)
	selector, _ := func() (*ast.SelectorExpr, bool) {
		if !ok {
			return nil, false
		}
		s, ok := call.Fun.(*ast.SelectorExpr)
		return s, ok
	}()
	if selector == nil || selector.Sel.Name != "BoundedMetadataEnabled" || len(call.Args) != 0 {
		t.Fatal("first branch is not conditioned on bounded mode", fn.Name.Name)
	}
	statements := branch.Body.List
	if len(statements) == 0 {
		t.Fatal("bounded branch is empty", fn.Name.Name)
	}
	if _, ok := statements[len(statements)-1].(*ast.ReturnStmt); !ok {
		t.Fatal("bounded branch falls through to the original body", fn.Name.Name)
	}
	names := map[string]bool{}
	ast.Inspect(branch.Body, func(node ast.Node) bool {
		if ident, ok := node.(*ast.Ident); ok {
			names[ident.Name] = true
		}
		return true
	})
	for _, name := range wanted {
		if !names[name] {
			t.Fatalf("bounded branch of %s does not use %s", fn.Name.Name, name)
		}
	}
}

func bootstrapLeavesGuardNames(root ast.Node) map[string]bool {
	names := map[string]bool{}
	ast.Inspect(root, func(node ast.Node) bool {
		if ident, ok := node.(*ast.Ident); ok {
			names[ident.Name] = true
		}
		return true
	})
	return names
}

// The literal accessors a bounded consumer must not reach on its own.
var hydratingAccessors = []string{
	"Bootstrap", "BootstrapFor", "BootstrapForContext", "BootstrapForUser", "MutateWorkspace",
	"WorkspaceMetadata", "WorkspaceMetadataForRead", "WorkspaceMetadataFields", "PagedWorkspaceMetadata",
	"issueRecordsBootstrapData", "attachmentVisible", "workspaceData", "BeginWorkspaceRead",
}

func TestAttachmentVisibilityBoundary(t *testing.T) {
	pkg := parseBootstrapLeavesPackage(t)
	boundedFirst(t, pkg.function(t, "attachmentVisible"), "boundedAttachmentVisible")
	want := []bootstrapLeavesGuardRef{
		{File: "files_api.go", Function: "fileReadableContext", Call: true},
		{File: "main.go", Function: "serveUpload", Call: true},
	}
	if got := pkg.references("attachmentVisible"); !reflect.DeepEqual(got, want) {
		t.Fatalf("upload visibility callset changed: %+v", got)
	}
	// The bounded check and its predicates reach no hydrating accessor; its
	// only metadata reads are the owned store primitive and indexed issue SQL.
	file := pkg.files["attachment_visibility.go"]
	if file == nil {
		t.Fatal("missing bounded upload visibility file")
	}
	names := bootstrapLeavesGuardNames(file)
	// Type names (domain.Bootstrap) are not reads; every other selector or
	// identifier, called or passed as a value, is.
	values := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.SelectorExpr:
			if x, ok := node.X.(*ast.Ident); !ok || x.Name != "domain" {
				values[node.Sel.Name] = true
			}
		case *ast.CallExpr:
			if ident, ok := node.Fun.(*ast.Ident); ok {
				values[ident.Name] = true
			}
		}
		return true
	})
	for _, accessor := range hydratingAccessors {
		if values[accessor] {
			t.Fatal("bounded upload visibility reads a hydrating accessor", accessor)
		}
	}
	for _, required := range []string{"BeginAttachmentVisibilityRead", "Project", "IssueQueryAccess", "IssueAttachmentVisible", "loopAttachmentVisible", "documentAttachmentVisible"} {
		if !names[required] {
			t.Fatal("bounded upload visibility lost a step", required)
		}
	}
	for _, name := range []string{"boundedAttachmentVisible", "boundedWorkspaceAttachmentVisible", "projectedAttachmentVisible", "projectedAttachmentFamiliesName"} {
		refs := pkg.references(name)
		for _, ref := range refs {
			if ref.File != "attachment_visibility.go" && !(name == "boundedAttachmentVisible" && ref.File == "main.go" && ref.Function == "attachmentVisible") {
				t.Fatalf("bounded upload visibility helper %s escaped: %+v", name, refs)
			}
		}
	}
}
