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

const scaleP21SW90QueryBoundary = `package boundary
func (s *SQLiteStore) QueryProjectDirectory(ctx context.Context, query ProjectRecordQuery) (ProjectRecordPage, error) {
	if s.BoundedMetadataEnabled() {
		read, err := s.BeginProjectDirectoryQuery(ctx, query)
		if err != nil {
			return ProjectRecordPage{}, err
		}
		if !registerMetadataOutputClose(ctx, read.Close) {
			read.Close()
			return ProjectRecordPage{}, recordMetadataOutputError(ctx, ErrBoundedMetadataCoverage)
		}
		return read.Page, nil
	}
	return s.queryProjectDirectoryAdapter(ctx, query)
}`

func scaleP21SW90BoundaryBody(function *ast.FuncDecl) (string, error) {
	if function == nil || function.Body == nil {
		return "", fmt.Errorf("missing project directory dispatch")
	}
	var body bytes.Buffer
	err := format.Node(&body, token.NewFileSet(), function.Body)
	return body.String(), err
}

func scaleP21SW90CheckQueryBoundary(function *ast.FuncDecl) error {
	expected, err := parser.ParseFile(token.NewFileSet(), "boundary.go", scaleP21SW90QueryBoundary, 0)
	if err != nil {
		return err
	}
	want, err := scaleP21SW90BoundaryBody(expected.Decls[0].(*ast.FuncDecl))
	if err != nil {
		return err
	}
	got, err := scaleP21SW90BoundaryBody(function)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("QueryProjectDirectory must retain its immediate same-store bounded return and owned output transfer before the literal adapter call")
	}
	return nil
}

func scaleP21SW90BoundaryReferences(node ast.Node) (map[string]int, error) {
	references := map[string]int{}
	var failure error
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
	ast.Inspect(node, func(current ast.Node) bool {
		var name string
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
		if name != "projectSnapshot" && name != "queryProjectDirectoryAdapter" && name != "queryProjectDirectoryLegacy" {
			return true
		}
		call, direct := parents[current].(*ast.CallExpr)
		if !direct || call.Fun != current {
			failure = fmt.Errorf("%s escaped its reviewed direct call boundary", name)
			return true
		}
		references[name]++
		return true
	})
	return references, failure
}

func TestScaleP21SW90ProjectDirectoryLegacyBoundary(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate project directory source")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(source), "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	// Closing the legacy helper's callers prevents a new bounded caller
	// from bypassing the public dispatch while keeping snapshot sites intact.
	want := map[string]map[string]int{
		"projectSnapshot": {
			"project_query.go:queryProjectDirectoryLegacy":              1,
			"scale_p10_directory_reads.go:queryProjectDirectoryAdapter": 1,
		},
		"queryProjectDirectoryAdapter": {
			"project_query.go:QueryProjectDirectory": 1,
		},
		"queryProjectDirectoryLegacy": {
			"scale_p10_directory_reads.go:queryProjectDirectoryAdapter": 4,
		},
	}
	got := map[string]map[string]int{}
	for name := range want {
		got[name] = map[string]int{}
	}
	var wrapper *ast.FuncDecl
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
				if site == "project_query.go:QueryProjectDirectory" {
					wrapper = function
				}
				if function.Body == nil {
					continue
				}
				inspected = function.Body
			}
			references, err := scaleP21SW90BoundaryReferences(inspected)
			if err != nil {
				t.Errorf("%s: %v", site, err)
			}
			for name, count := range references {
				got[name][site] += count
			}
		}
	}
	for name, expected := range want {
		if !reflect.DeepEqual(got[name], expected) {
			t.Errorf("%s callers=%v, want reviewed compatibility callers %v", name, got[name], expected)
		}
	}
	if err := scaleP21SW90CheckQueryBoundary(wrapper); err != nil {
		t.Error(err)
	}
}

func TestScaleP21SW90ProjectDirectoryBoundaryRejectsNewEdges(t *testing.T) {
	for _, fixture := range []struct {
		name, body string
		direct     bool
	}{
		{"selector-call", `s.projectSnapshot("workspace")`, true},
		{"free-call", `projectSnapshot("workspace")`, true},
		{"selector-alias", `consume(s.projectSnapshot)`, false},
		{"free-alias", `consume(queryProjectDirectoryAdapter)`, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "probe.go", "package probe; func caller() {"+fixture.body+"}", 0)
			if err != nil {
				t.Fatal(err)
			}
			references, err := scaleP21SW90BoundaryReferences(file.Decls[0].(*ast.FuncDecl).Body)
			if fixture.direct && (err != nil || references["projectSnapshot"] != 1) ||
				!fixture.direct && err == nil {
				t.Fatal("legacy reference classification changed", references, err)
			}
		})
	}
	for name, mutation := range map[string]string{
		"reversed-flag":    strings.Replace(scaleP21SW90QueryBoundary, "if s.BoundedMetadataEnabled()", "if !s.BoundedMetadataEnabled()", 1),
		"other-store-flag": strings.Replace(scaleP21SW90QueryBoundary, "if s.BoundedMetadataEnabled()", "if other.BoundedMetadataEnabled()", 1),
		"unowned-output":   strings.Replace(scaleP21SW90QueryBoundary, "registerMetadataOutputClose(ctx, read.Close)", "true", 1),
		"changed-context":  strings.Replace(scaleP21SW90QueryBoundary, "s.BeginProjectDirectoryQuery(ctx, query)", "s.BeginProjectDirectoryQuery(other, query)", 1),
		"closed-output":    strings.Replace(scaleP21SW90QueryBoundary, "return read.Page, nil", "read.Close(); return read.Page, nil", 1),
	} {
		t.Run(name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "mutated.go", mutation, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := scaleP21SW90CheckQueryBoundary(file.Decls[0].(*ast.FuncDecl)); err == nil {
				t.Fatal("unreviewed bounded-to-legacy dispatch passed")
			}
		})
	}
}
