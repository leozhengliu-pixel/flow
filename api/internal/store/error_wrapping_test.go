package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// errorLikeName matches the names the store and object storage give error
// values: err, copyErr, closeErr, err2.
var errorLikeName = regexp.MustCompile(`^(err|[a-z][A-Za-z0-9]*Err)\d*$`)

// TestErrorsAreWrappedWithW keeps database, driver and object-storage errors
// recognisable: IsDatabaseError and objectstore.IsStorageError follow %w
// chains, so an error formatted with %v or %s, or rebuilt from its text with
// errors.New(err.Error()), loses its type and its raw text can reach a client
// as a 4xx message. Wrap with %w (several %w are allowed).
func TestErrorsAreWrappedWithW(t *testing.T) {
	var problems []string
	for _, dir := range []string{".", "../objectstore"} {
		problems = append(problems, errorWrappingProblems(t, dir)...)
	}
	if len(problems) > 0 {
		t.Fatalf("errors formatted without %%w (use %%w so the error type survives):\n%s", strings.Join(problems, "\n"))
	}
}

func errorWrappingProblems(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var problems []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch callName(call) {
			case "fmt.Errorf":
				if problem := errorfProblem(call); problem != "" {
					problems = append(problems, fset.Position(call.Pos()).String()+": "+problem)
				}
			case "errors.New":
				if len(call.Args) == 1 && containsErrorCall(call.Args[0]) {
					problems = append(problems, fset.Position(call.Pos()).String()+": errors.New built from err.Error()")
				}
			}
			return true
		})
	}
	return problems
}

func callName(call *ast.CallExpr) string {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := selector.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return pkg.Name + "." + selector.Sel.Name
}

// errorfProblem reports an error-like argument of fmt.Errorf whose verb is
// not %w, or an err.Error() argument.
func errorfProblem(call *ast.CallExpr) string {
	if len(call.Args) < 2 {
		return ""
	}
	literal, ok := call.Args[0].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return ""
	}
	format, err := strconv.Unquote(literal.Value)
	if err != nil {
		return ""
	}
	verbs := formatVerbs(format)
	for index, arg := range call.Args[1:] {
		if containsErrorCall(arg) {
			return "err.Error() formatted into an error"
		}
		ident, ok := arg.(*ast.Ident)
		if !ok || !errorLikeName.MatchString(ident.Name) {
			continue
		}
		if index < len(verbs) && verbs[index] != 'w' {
			return "%" + string(verbs[index]) + " formats error " + ident.Name
		}
	}
	return ""
}

// formatVerbs lists the verbs of a format string in argument order.
func formatVerbs(format string) []rune {
	var verbs []rune
	runes := []rune(format)
	for index := 0; index < len(runes); index++ {
		if runes[index] != '%' {
			continue
		}
		index++
		for index < len(runes) && strings.ContainsRune("+-# 0123456789.[]*", runes[index]) {
			index++
		}
		if index < len(runes) && runes[index] != '%' {
			verbs = append(verbs, runes[index])
		}
	}
	return verbs
}

func containsErrorCall(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 0 {
			return !found
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Error" {
			if ident, ok := selector.X.(*ast.Ident); ok && errorLikeName.MatchString(ident.Name) {
				found = true
			}
		}
		return !found
	})
	return found
}
