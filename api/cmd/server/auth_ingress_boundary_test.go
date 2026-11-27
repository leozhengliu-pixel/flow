package main

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func authIngressBoundaryText(t *testing.T, node ast.Node) string {
	t.Helper()
	var output bytes.Buffer
	if err := format.Node(&output, token.NewFileSet(), node); err != nil {
		t.Fatal(err)
	}
	return output.String()
}

func authIngressBoundaryFunction(t *testing.T, file, receiver, name string) *ast.FuncDecl {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != name {
			continue
		}
		actual := ""
		if function.Recv != nil {
			actual = authIngressBoundaryText(t, function.Recv.List[0].Type)
		}
		if actual == receiver {
			return function
		}
	}
	t.Fatalf("missing source boundary %s %s.%s", file, receiver, name)
	return nil
}

func hasStatement(t *testing.T, function *ast.FuncDecl, expected string) bool {
	t.Helper()
	for _, statement := range function.Body.List {
		if authIngressBoundaryText(t, statement) == expected {
			return true
		}
	}
	return false
}

// This pins the current ingress and producer boundary. It does not declare
// borrowed snapshots impossible or replace issue read authorization's actual authorization table.
func TestAuthorizationIngressBoundaries(t *testing.T) {
	t.Run("production-snapshot-producer-callset", func(t *testing.T) {
		expected := map[string]int{
			"main.go:bootstrap:BeginWorkspaceRead":                                   1,
			"advanced_features.go:downloadExport:BeginWorkspaceRead":                 1,
			"personal_data.go:writeWorkspacePersonalData:BeginWorkspaceRead":         1,
			"notification_state.go:scopedNotificationState:BeginWorkspaceRead":       1,
			"general_bootstrap.go:generalBootstrapBounded:BeginGeneralBootstrapRead": 1,
		}
		actual := map[string]int{}
		functions := map[string][]*ast.FuncDecl{}
		entries, err := os.ReadDir(".")
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, declaration := range parsed.Decls {
				owner := "<package>"
				if function, ok := declaration.(*ast.FuncDecl); ok {
					owner = function.Name.Name
					functions[owner] = append(functions[owner], function)
				}
				calls := map[*ast.SelectorExpr]bool{}
				ast.Inspect(declaration, func(node ast.Node) bool {
					if call, ok := node.(*ast.CallExpr); ok {
						if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
							calls[selector] = true
						}
					}
					return true
				})
				ast.Inspect(declaration, func(node ast.Node) bool {
					selector, ok := node.(*ast.SelectorExpr)
					if !ok || selector.Sel.Name != "BeginWorkspaceRead" && selector.Sel.Name != "BeginGeneralBootstrapRead" {
						return true
					}
					if !calls[selector] || authIngressBoundaryText(t, selector.X) != "s.store" {
						t.Fatalf("captured or rebound snapshot producer in %s:%s", name, owner)
					}
					actual[name+":"+owner+":"+selector.Sel.Name]++
					return true
				})
			}
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("production snapshot producer callset changed: got %v want %v", actual, expected)
		}
		// Include named helpers and function values conservatively. Dynamic
		// next.ServeHTTP is the intended handoff through authenticate to mux.
		pending := []string{
			"withForwardingPolicy", "withSecurityHeaders", "recoverPanics", "requestLog",
			"cors", "compressAPIResponses", "signedFileURLs", "apiErrorEnvelope",
			"requestBodyBudget", "documentRequestBodies", "stripQueryNUL", "withStaticFiles", "boundedLegacyIssueWrites",
			"limitFailedAuthentication", "holdProjections", "withColdMetadataRouteMux", "authenticate",
		}
		seen := map[string]bool{}
		for len(pending) != 0 {
			name := pending[0]
			pending = pending[1:]
			if seen[name] {
				continue
			}
			seen[name] = true
			for _, function := range functions[name] {
				ast.Inspect(function.Body, func(node ast.Node) bool {
					reference := ""
					switch value := node.(type) {
					case *ast.SelectorExpr:
						reference = value.Sel.Name
					case *ast.Ident:
						reference = value.Name
					}
					if reference == "BeginWorkspaceRead" || reference == "BeginGeneralBootstrapRead" {
						t.Fatalf("pre-auth named ingress now reaches %s through %s", reference, name)
					}
					if len(functions[reference]) != 0 && !seen[reference] {
						pending = append(pending, reference)
					}
					return true
				})
			}
		}
	})

	t.Run("outer-http-order-and-output-scope", func(t *testing.T) {
		handler := authIngressBoundaryFunction(t, "main.go", "", "newHandler")
		if !hasStatement(t, handler,
			"handler := s.withStaticFiles(boundedLegacyIssueWrites(s.limitFailedAuthentication(s.projectionGate.holdProjections(s.withColdMetadataRouteMux(mux, s.authenticate(s.limitAuthenticatedAPI(mux)))))))") {
			t.Fatal("HTTP ingress order changed; review all pre-auth context producers")
		}
		authenticate := authIngressBoundaryFunction(t, "auth.go", "*server", "authenticate")
		text := authIngressBoundaryText(t, authenticate.Body)
		if strings.Count(text, "s.authorizeWorkspaceRequest(w, r, ") != 2 ||
			!strings.Contains(text, "r, release = beginRequestResourceReads(r)") ||
			!strings.Contains(text, "defer release()") {
			t.Fatal("authentication or containing output lifetime changed")
		}
		scope := authIngressBoundaryFunction(t, "request_resource_read.go", "", "beginRequestResourceReads")
		if !hasStatement(t, scope, "return r.WithContext(ctx), scope.close") {
			t.Fatal("request scope no longer closes its containing outputs")
		}
		storePath := filepath.Join("..", "..", "internal", "store")
		for _, item := range []struct{ file, receiver string }{
			{"owned_metadata_read.go", "*InstalledMetadataRead"},
			{"json_output_read.go", "*MetadataJSONRead"},
			{"collaboration_document_read.go", "*CollaborationDocumentRead"},
		} {
			function := authIngressBoundaryFunction(t, filepath.Join(storePath, item.file), item.receiver, "Context")
			if strings.Contains(authIngressBoundaryText(t, function.Body), "workspaceReadKey") {
				t.Fatalf("%s.Context now lends a workspace snapshot; review pre-auth use", item.receiver)
			}
		}
	})

	t.Run("bounded-unborrowed-source-refuses-before-hydration", func(t *testing.T) {
		storePath := filepath.Join("..", "..", "internal", "store")
		source := authIngressBoundaryFunction(t, filepath.Join(storePath, "read_snapshot.go"), "*SQLiteStore", "workspaceReadSource")
		if len(source.Body.List) < 3 {
			t.Fatal("workspace source boundary changed")
		}
		borrowed, ok := source.Body.List[0].(*ast.IfStmt)
		if !ok || authIngressBoundaryText(t, borrowed.Cond) !=
			"ok && snapshot.database == s.db && (workspace == \"\" || workspace == snapshot.workspace)" ||
			!hasStatement(t, &ast.FuncDecl{Body: borrowed.Body}, "return snapshot.reader, data, true") {
			t.Fatal("matching borrowed workspace source precedence changed")
		}
		refusal, ok := source.Body.List[1].(*ast.IfStmt)
		if !ok || refusal.Else != nil || authIngressBoundaryText(t, refusal.Cond) != "s.metadataResidency != nil" ||
			len(refusal.Body.List) != 1 ||
			authIngressBoundaryText(t, refusal.Body.List[0]) != "return s.db, domain.Bootstrap{}, false" {
			t.Fatal("bounded source may hydrate without a borrowed workspace snapshot")
		}
		paged := authIngressBoundaryFunction(t, filepath.Join(storePath, "issue_query.go"), "*SQLiteStore", "PagedWorkspaceMetadata")
		// A COLD request (default COLD routing) reads the legacy projection
		// only when a matching owned snapshot was not already supplied.
		const cold = `if !pinned && coldCapable(ctx) {
	if view, release, handled := s.coldProjectionView(ctx, "PagedWorkspaceMetadata", workspace); handled {
		defer release()
		if view == nil {
			return domain.Bootstrap{}, ErrAuthForbidden
		}
		return view.PagedWorkspaceMetadata(ctx, view.lastWorkspaceKey, userID)
	}
}`
		body := paged.Body.List
		if len(body) < 5 ||
			authIngressBoundaryText(t, body[0]) != "snapshot, pinned := ctx.Value(workspaceReadKey{}).(workspaceRead)" ||
			authIngressBoundaryText(t, body[1]) != `pinned = pinned && snapshot.database == s.db && (workspace == "" || workspace == snapshot.workspace)` ||
			strings.Join(strings.Fields(authIngressBoundaryText(t, body[2])), " ") != strings.Join(strings.Fields(cold), " ") {
			t.Fatal("paged metadata snapshot and COLD legacy-view boundary changed")
		}
		if authIngressBoundaryText(t, body[3]) != "_, data, ok := s.workspaceReadSource(ctx, workspace)" ||
			authIngressBoundaryText(t, body[4]) != "if !ok {\n\treturn data, ErrAuthForbidden\n}" {
			t.Fatal("paged metadata no-source refusal changed")
		}
	})

	t.Run("ordinary-issue-denial-remains-before-role-checks", func(t *testing.T) {
		function := authIngressBoundaryFunction(t, "auth.go", "*server", "resourceAllowedLegacy")
		var issue *ast.CaseClause
		ast.Inspect(function.Body, func(node ast.Node) bool {
			clause, ok := node.(*ast.CaseClause)
			if ok && len(clause.List) == 1 && authIngressBoundaryText(t, clause.List[0]) == `"issues"` {
				issue = clause
			}
			return true
		})
		if issue == nil {
			t.Fatal("missing literal issue authorization")
		}
		want := []string{
			"if !issueAllowed(parts[2]) {\n\treturn false\n}",
			"issueIndex := slices.IndexFunc(data.Issues, func(item domain.Issue) bool {\n\treturn issueAnswersTo(item, parts[2])\n})",
			"if issueIndex < 0 {\n\treturn false\n}",
			"issue := data.Issues[issueIndex]",
			"roleRank := issuePermissionRank(issueRole(s, data, issue))",
		}
		matched := false
		for start := range issue.Body {
			if start+len(want) > len(issue.Body) {
				break
			}
			equal := true
			for index, expected := range want {
				actual := authIngressBoundaryText(t, issue.Body[start+index])
				equal = equal && strings.Join(strings.Fields(actual), " ") == strings.Join(strings.Fields(expected), " ")
			}
			matched = matched || equal
		}
		if !matched {
			t.Fatal("literal issue-index refusal moved or changed; retain SW256's exact ordinary 403 outcomes")
		}
	})
}
