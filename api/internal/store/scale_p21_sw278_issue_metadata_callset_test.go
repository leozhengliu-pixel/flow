package store

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// TestScaleP21SW278PersistIssueMetadataCallset proves the legacy issue
// metadata writer is reached on a bounded store only after the non-project
// hot inputs were selected: its only reference is UpdateIssueRecord's else
// branch of `projectInputs != nil`, and projectInputs is assigned only in the
// bounded issueProjectInputsSupported branch, whose sibling branches are the
// hot inputs and a coverage refusal.
func TestScaleP21SW278PersistIssueMetadataCallset(t *testing.T) {
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	references := 0
	for _, path := range sources {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			var stack []ast.Node
			ast.Inspect(declaration, func(node ast.Node) bool {
				if node == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				stack = append(stack, node)
				// A method is only reachable through a selector (a call or a
				// method value; the latter is rejected below).
				name := ""
				if value, ok := node.(*ast.SelectorExpr); ok {
					name = value.Sel.Name
				}
				if name != "persistIssueMetadata" {
					return true
				}
				function, _ := declaration.(*ast.FuncDecl)
				if function == nil || function.Name.Name == "persistIssueMetadata" {
					return true
				}
				references++
				if path != "issue_record_mutation.go" || function.Name.Name != "UpdateIssueRecord" {
					t.Errorf("persistIssueMetadata referenced from %s:%s", path, function.Name.Name)
					return true
				}
				// The call must be the right-hand side of an assignment in the
				// else branch of `if projectInputs != nil`.
				if len(stack) < 2 {
					t.Error("persistIssueMetadata reference without a call")
					return true
				}
				if _, ok := stack[len(stack)-2].(*ast.CallExpr); !ok {
					t.Error("persistIssueMetadata used as a method value")
				}
				guarded := false
				for index := len(stack) - 2; index >= 1; index-- {
					branch, ok := stack[index].(*ast.IfStmt)
					if !ok || branch.Else == nil || stack[index+1] != branch.Else {
						continue
					}
					condition, ok := branch.Cond.(*ast.BinaryExpr)
					if ok && condition.Op == token.NEQ && scaleP21SW278Ident(condition.X, "projectInputs") && scaleP21SW278Ident(condition.Y, "nil") {
						guarded = true
					}
					break
				}
				if !guarded {
					t.Error("persistIssueMetadata call is not the else branch of projectInputs != nil")
				}
				return true
			})
		}
		if path != "issue_record_mutation.go" {
			continue
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Name.Name != "UpdateIssueRecord" {
				continue
			}
			scaleP21SW278CheckProjectInputs(t, function)
		}
	}
	if references != 1 {
		t.Fatalf("persistIssueMetadata references=%d, want UpdateIssueRecord's single call", references)
	}
}

func scaleP21SW278Ident(expression ast.Expr, name string) bool {
	ident, ok := expression.(*ast.Ident)
	return ok && ident.Name == name
}

// scaleP21SW278CheckProjectInputs pins every assignment of projectInputs to
// the bounded selector chain
//
//	if s.BoundedMetadataEnabled() { ... if issueHotInputsSupported(...) {...}
//	else if issueProjectInputsSupported(...) { projectInputs, err = ... }
//	else { return ErrBoundedMetadataCoverage } ... }
func scaleP21SW278CheckProjectInputs(t *testing.T, function *ast.FuncDecl) {
	t.Helper()
	assignments, chains := 0, 0
	var stack []ast.Node
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, node)
		if branch, ok := node.(*ast.IfStmt); ok && scaleP21SW278CallNamed(branch.Cond, "issueHotInputsSupported") {
			chains++
			project, ok := branch.Else.(*ast.IfStmt)
			if !ok || !scaleP21SW278CallNamed(project.Cond, "issueProjectInputsSupported") {
				t.Error("hot inputs are not followed by the project-input branch")
				return true
			}
			refusal, ok := project.Else.(*ast.BlockStmt)
			if !ok || len(refusal.List) != 1 {
				t.Error("project-input branch has no single refusal else")
				return true
			}
			result, ok := refusal.List[0].(*ast.ReturnStmt)
			if !ok || len(result.Results) != 1 || !scaleP21SW278Ident(result.Results[0], "ErrBoundedMetadataCoverage") {
				t.Error("unsupported issue inputs are not refused")
			}
			bounded := false
			for index := len(stack) - 2; index >= 0; index-- {
				if outer, ok := stack[index].(*ast.IfStmt); ok && scaleP21SW278CallNamed(outer.Cond, "BoundedMetadataEnabled") && outer.Else == nil {
					bounded = true
					break
				}
			}
			if !bounded {
				t.Error("input selector chain is outside the bounded branch")
			}
		}
		assignment, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, left := range assignment.Lhs {
			if !scaleP21SW278Ident(left, "projectInputs") {
				continue
			}
			assignments++
			inProjectBranch := false
			for index := len(stack) - 2; index >= 1; index-- {
				branch, ok := stack[index].(*ast.IfStmt)
				if ok && scaleP21SW278CallNamed(branch.Cond, "issueProjectInputsSupported") {
					inProjectBranch = true
					break
				}
			}
			if !inProjectBranch {
				t.Error("projectInputs assigned outside the bounded project-input branch")
			}
		}
		return true
	})
	if assignments != 1 || chains != 1 {
		t.Fatalf("projectInputs assignments=%d selector chains=%d", assignments, chains)
	}
}

func scaleP21SW278CallNamed(expression ast.Expr, name string) bool {
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch callee := call.Fun.(type) {
		case *ast.Ident:
			found = found || callee.Name == name
		case *ast.SelectorExpr:
			found = found || callee.Sel.Name == name
		}
		return true
	})
	return found
}

// TestScaleP21SW278BoundedIssueMetadataRefusal shows the bounded writer
// neither reads past the policy copy nor skips a change the legacy writer
// would make: project-less issues produce no metadata (as legacy), project
// progress and team moves are refused.
func TestScaleP21SW278BoundedIssueMetadataRefusal(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	at := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	project := domain.Project{ID: "sw278-project", Name: "SW278", TeamIDs: []string{f.data.Teams[0].ID}, CreatedAt: at, UpdatedAt: at}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projects"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, project)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	plain := domain.Issue{ID: "sw278-issue", Team: f.data.Teams[0], State: f.data.States[0], CreatedAt: at, UpdatedAt: at}
	edited := plain
	edited.Title = "Edited"
	completed := plain
	completed.Project = &domain.ProjectSummary{ID: project.ID}
	done := completed
	done.State.Type = "completed"
	moved := plain
	moved.Team = domain.Team{ID: "sw278-other-team"}
	cases := []struct {
		name          string
		before, after []domain.Issue
		documents     []domain.Document
		legacyChanged bool
		refused       bool
	}{
		{"plain edit", []domain.Issue{plain}, []domain.Issue{edited}, nil, false, false},
		{"duplicate family rows", []domain.Issue{plain, plain}, []domain.Issue{edited, edited}, nil, false, false},
		{"empty family", nil, nil, nil, false, false},
		{"project progress", []domain.Issue{completed}, []domain.Issue{done}, nil, true, true},
		{"project joined", []domain.Issue{plain}, []domain.Issue{completed}, nil, true, true},
		{"team move without documents", []domain.Issue{plain}, []domain.Issue{moved}, nil, false, true},
		{"team move with documents", []domain.Issue{plain}, []domain.Issue{moved}, []domain.Document{}, false, true},
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	for _, test := range cases {
		for _, repo := range []*SQLiteStore{f.SQLiteStore, view} {
			tx, err := repo.db.BeginTx(t.Context(), repo.recordTxOptions())
			if err != nil {
				t.Fatal(err)
			}
			repo.writeMu.Lock()
			metadata, err := repo.persistIssueMetadata(t.Context(), tx, workspace, test.before, test.after, test.documents)
			repo.writeMu.Unlock()
			_ = tx.Rollback()
			if repo == view {
				if test.refused != errors.Is(err, ErrBoundedMetadataCoverage) || metadata != nil || test.refused == (err == nil) {
					t.Fatalf("%s bounded: metadata=%v err=%v", test.name, metadata != nil, err)
				}
				continue
			}
			if err != nil || test.legacyChanged != (metadata != nil) {
				t.Fatalf("%s legacy: metadata=%v err=%v", test.name, metadata != nil, err)
			}
		}
	}
	if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("bounded issue metadata decoded the workspace or leaked work")
	}
}
