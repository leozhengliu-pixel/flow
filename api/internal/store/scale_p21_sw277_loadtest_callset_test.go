package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestScaleP21SW277LoadFixtureCallset proves the synthetic seed and state
// measurement are load-test setup only: they and their bounded writers are
// declared in files built only with -tags loadtest, no build or release
// recipe sets that tag, every reference outside tests is the declared
// wrapper-to-bounded-branch call, and every test reference sits in a
// loadtest-tagged test file. No HTTP route or ticker can therefore reach
// them (the server binary does not contain them).
func TestScaleP21SW277LoadFixtureCallset(t *testing.T) {
	declared := map[string]string{
		"SeedSyntheticMetadata": "loadtest_seed.go", "WorkspaceStateBytes": "loadtest_seed.go",
		"seedSyntheticBatchBounded": "scale_p21_loadtest_seed.go", "workspaceStateBytesBounded": "scale_p21_loadtest_seed.go",
		"seedSyntheticRecords": "scale_p21_loadtest_seed.go", "SeedScaleP01Principals": "scale_p01_seed.go",
	}
	allowed := map[string]bool{
		"loadtest_seed.go:SeedSyntheticMetadata->seedSyntheticBatchBounded":          true,
		"loadtest_seed.go:WorkspaceStateBytes->workspaceStateBytesBounded":           true,
		"scale_p21_loadtest_seed.go:seedSyntheticBatchBounded->seedSyntheticRecords": true,
	}
	tagged := func(path string) bool {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		first, _, _ := strings.Cut(string(raw), "\n")
		return strings.TrimSpace(first) == "//go:build loadtest"
	}
	found := map[string]bool{}
	for _, dir := range []string{".", "../../cmd/server", "../../cmd", "../../internal"} {
		paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		if dir == "../../internal" || dir == "../../cmd" {
			nested, err := filepath.Glob(filepath.Join(dir, "*", "*.go"))
			if err != nil {
				t.Fatal(err)
			}
			paths = slices.DeleteFunc(nested, func(path string) bool {
				return strings.HasPrefix(path, filepath.Clean("../../internal/store")+string(filepath.Separator)) ||
					strings.HasPrefix(path, filepath.Clean("../../cmd/server")+string(filepath.Separator))
			})
		}
		for _, path := range paths {
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			test := strings.HasSuffix(path, "_test.go")
			for _, declaration := range file.Decls {
				owner := ""
				if function, ok := declaration.(*ast.FuncDecl); ok {
					owner = function.Name.Name
					if want, ok := declared[owner]; ok && function.Recv != nil && dir == "." {
						if filepath.Base(path) != want || !tagged(path) {
							t.Errorf("%s declared in %s (loadtest tag=%t)", owner, path, tagged(path))
						}
						found[owner] = true
					}
				}
				ast.Inspect(declaration, func(node ast.Node) bool {
					selector, ok := node.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					if _, ok := declared[selector.Sel.Name]; !ok {
						return true
					}
					if !tagged(path) {
						t.Errorf("%s referenced from %s:%s, a file the server binary builds", selector.Sel.Name, path, owner)
						return true
					}
					if !test && !allowed[filepath.Base(path)+":"+owner+"->"+selector.Sel.Name] {
						t.Errorf("%s referenced from %s:%s", selector.Sel.Name, path, owner)
					}
					return true
				})
			}
		}
	}
	for name := range declared {
		if !found[name] {
			t.Errorf("%s declaration not found", name)
		}
	}
	// No image, release or CI build compiles the load-test tag into a binary.
	recipes, err := filepath.Glob("../../../.github/workflows/*.yml")
	if err != nil {
		t.Fatal(err)
	}
	recipes = append(recipes, "../../../Dockerfile")
	for _, path := range recipes {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "loadtest") {
			t.Errorf("%s builds with the loadtest tag", path)
		}
	}
	if len(recipes) < 2 {
		t.Fatal("build recipes not found")
	}
}
