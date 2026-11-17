package main

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func scaleP21SW211Node(t *testing.T, node ast.Node) string {
	t.Helper()
	var text bytes.Buffer
	if err := format.Node(&text, token.NewFileSet(), node); err != nil {
		t.Fatal(err)
	}
	return text.String()
}

func scaleP21SW211FunctionName(fn *ast.FuncDecl) string {
	if fn.Recv == nil {
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

func TestScaleP21SW211LegacyCollectionHotBoundaries(t *testing.T) {
	// These cold callers retain literal storage semantics until the separate
	// production cold-routing manifest supplies their facade. This allowlist
	// is a callset guard, not certification of those cold routing obligations.
	coldAccount := map[string]string{
		"codingDirectory.CodingUser":     "coding provisioning/signing setup",
		"server.userHasLegacySigningKey": "coding signing setup",
		"server.accountPasskeys":         "MFA setup/authenticator account directory",
		"server.listPasskeys":            "account security settings",
		"server.getUserSettings":         "account settings endpoint",
		"server.getCommitSigningKey":     "account signing setup",
		"server.removeCommitSigningKey":  "account signing setup",
	}
	hotAccount := map[string]string{
		"server.startCommentActions": "s.startCommentActionsBounded(r, issueID, comment)",
		"server.listCommentActions":  "s.listCommentActionsBounded(w, r)",
	}
	counts := map[string]int{}
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".go") || strings.HasSuffix(file.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(file.Name())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(raw, []byte("AccountCollections")) && !bytes.Contains(raw, []byte("TrashSnapshot")) {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file.Name(), raw, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				ast.Inspect(decl, func(node ast.Node) bool {
					selector, ok := node.(*ast.SelectorExpr)
					if ok && (selector.Sel.Name == "AccountCollections" || selector.Sel.Name == "TrashSnapshot") {
						t.Errorf("package-level %s reference requires explicit reachability review", selector.Sel.Name)
					}
					return true
				})
				continue
			}
			name := scaleP21SW211FunctionName(fn)
			var ancestors []ast.Node
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				if node == nil {
					ancestors = ancestors[:len(ancestors)-1]
					return false
				}
				ancestors = append(ancestors, node)
				selector, ok := node.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "AccountCollections" && selector.Sel.Name != "TrashSnapshot" {
					return true
				}
				counts[selector.Sel.Name+"/"+name]++
				call, direct := ancestors[len(ancestors)-2].(*ast.CallExpr)
				if !direct || call.Fun != selector {
					t.Errorf("%s captures %s indirectly; explicit reachability review is required", name, selector.Sel.Name)
					return true
				}
				if selector.Sel.Name == "AccountCollections" {
					if coldAccount[name] != "" {
						return true
					}
					if boundedCall := hotAccount[name]; boundedCall != "" {
						first, ok := fn.Body.List[0].(*ast.IfStmt)
						if !ok || scaleP21SW211Node(t, first.Cond) != "s.store.BoundedMetadataEnabled()" ||
							len(first.Body.List) != 2 || scaleP21SW211Node(t, first.Body.List[0]) != boundedCall {
							t.Errorf("%s lost its first bounded owned dispatch", name)
						} else if _, returns := first.Body.List[1].(*ast.ReturnStmt); !returns {
							t.Errorf("%s bounded dispatch falls through to legacy account collections", name)
						}
						return true
					}
					if name == "server.requestAccountSettings" {
						offOnly := false
						for _, ancestor := range ancestors {
							if branch, ok := ancestor.(*ast.IfStmt); ok && scaleP21SW211Node(t, branch.Cond) == "!s.store.BoundedMetadataEnabled()" &&
								branch.Body.Pos() <= call.Pos() && call.End() <= branch.Body.End() {
								offOnly = true
							}
						}
						if !offOnly {
							t.Error("owned account settings adapter reaches the wrapper with the flag on")
						}
						return true
					}
					t.Errorf("new AccountCollections caller %s requires explicit HOT-owned or COLD-routing classification", name)
					return true
				}
				if name == "server.restoreAgentChangeEntity" {
					return true // Cold undo/recreate obligation remains with its existing owner.
				}
				if name != "server.authorizeWorkspaceRequest" {
					t.Errorf("new TrashSnapshot caller %s requires explicit reachability review", name)
					return true
				}
				trashOnly := false
				for _, ancestor := range ancestors {
					branch, ok := ancestor.(*ast.IfStmt)
					if ok && scaleP21SW211Node(t, branch.Cond) == `ok && r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/trash/")` &&
						branch.Body.Pos() <= call.Pos() && call.End() <= branch.Body.End() {
						trashOnly = true
					}
				}
				if !trashOnly {
					t.Error("ordinary HOT authorization can reach the literal trash snapshot")
				}
				return true
			})
		}
	}
	for name := range coldAccount {
		if counts["AccountCollections/"+name] != 1 {
			t.Errorf("cold account caller changed: %s count=%d", name, counts["AccountCollections/"+name])
		}
	}
	for name := range hotAccount {
		if counts["AccountCollections/"+name] != 1 {
			t.Errorf("HOT account branch guard lost its witnessed literal call: %s", name)
		}
	}
	for _, key := range []string{
		"AccountCollections/server.requestAccountSettings",
		"TrashSnapshot/server.authorizeWorkspaceRequest",
		"TrashSnapshot/server.restoreAgentChangeEntity",
	} {
		if counts[key] != 1 {
			t.Errorf("wrapper callset changed: %s count=%d", key, counts[key])
		}
	}
}
