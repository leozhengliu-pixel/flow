package main

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP21SW231FilterSuggestionAuthorizationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_ACCESS", "on")
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			other := scaleP03Fixture(t, repo)
			f := scaleP21M2Setup(t, repo)
			workspace := f.data.Workspace.URLKey
			tokens := map[string]string{}
			for _, principal := range f.principals {
				_, token, err := repo.Login(t.Context(), principal.user.Email, "p03-fixture-password")
				if err != nil {
					t.Fatal(err)
				}
				tokens[principal.user.ID] = token
			}
			type outcome struct {
				Allowed bool
				Status  int
				Body    string
			}
			var pairs, legacySQL, boundedSQL, legacyMax, boundedMax int
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				t.Run(method, func(t *testing.T) {
					f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
						cases := []struct {
							name, workspace string
							suspended       bool
							want            bool
						}{
							{"active", workspace, false, true},
							{"foreign-workspace", other.data.Workspace.URLKey, false, false},
						}
						if user.ID == f.principals[2].user.ID {
							cases = append(cases, struct {
								name, workspace string
								suspended       bool
								want            bool
							}{"suspended", workspace, true, false})
						}
						for _, cell := range cases {
							t.Run(cell.name, func(t *testing.T) {
								if cell.suspended {
									if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, user.ID); err != nil {
										t.Fatal(err)
									}
									defer func() {
										if err := repo.ResumeMember(t.Context(), f.data.Workspace.ID, user.ID); err != nil {
											t.Error(err)
										}
									}()
								}
								var want outcome
								for _, source := range []*store.SQLiteStore{repo, f.probe} {
									request := f.literalRequest(user, key, nil)
									request.Method, request.URL.Path = method, "/api/search/filter-suggestions"
									request.Header.Set("X-Workspace-Key", cell.workspace)
									request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokens[user.ID]})
									if boundedIssueAuthorizationRequest(request) {
										t.Fatal("shared predicate changed literal off authorization scope")
									}
									request, release := beginRequestResourceReads(request)
									statements := 0
									request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) {
										statements++
									}))
									service := &server{store: source}
									recorder := httptest.NewRecorder()
									allowed := service.authorizeWorkspaceRequest(recorder, request, user)
									if allowed {
										recorder.WriteHeader(http.StatusNoContent)
									}
									got := outcome{allowed, recorder.Code, recorder.Body.String()}
									if got.Allowed != cell.want || !allowed && recorder.Code != http.StatusForbidden {
										release()
										t.Fatalf("required authorization=%t got=%+v", cell.want, got)
									}
									if !source.BoundedMetadataEnabled() {
										want = got
										legacySQL += statements
										legacyMax = max(legacyMax, statements)
									} else {
										pairs++
										boundedSQL += statements
										boundedMax = max(boundedMax, statements)
										if !reflect.DeepEqual(want, got) {
											release()
											t.Fatalf("actual middleware differs: legacy=%+v bounded=%+v", want, got)
										}
										if allowed && request.Context().Value(issueRequestAccessKey{}) == nil {
											release()
											t.Fatal("successful bounded middleware did not use existing issue access")
										}
										if request.Context().Value(pagedMetadataHandoffKey{}) != nil {
											release()
											t.Fatal("bounded authorization created a complete legacy handoff")
										}
									}
									release()
									release()
									if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
										t.Fatal("bounded middleware retained work or decoded complete metadata")
									}
								}
							})
						}
					})
				})
			}
			t.Logf("pairs=%d authorization SQL legacy=%d bounded=%d max=%d/%d", pairs, legacySQL, boundedSQL, legacyMax, boundedMax)
		})
	}
}

func TestScaleP21SW231FilterSuggestionAuthorizationBoundary(t *testing.T) {
	raw, err := os.ReadFile("auth.go")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "auth.go", raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	text := func(node ast.Node) string {
		var result bytes.Buffer
		if err := format.Node(&result, token.NewFileSet(), node); err != nil {
			t.Fatal(err)
		}
		return result.String()
	}
	count := 0
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "resourceAllowedLegacy" {
			continue
		}
		for _, stmt := range fn.Body.List {
			branch, ok := stmt.(*ast.IfStmt)
			if !ok || text(branch.Cond) != `s.store.BoundedMetadataEnabled() && (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.Path == "/api/search/filter-suggestions"` {
				continue
			}
			count++
			if branch.Init != nil || branch.Else != nil || len(branch.Body.List) != 2 ||
				text(branch.Body.List[0]) != "_, _, err := s.requestIssueQueryAccess(r)" ||
				text(branch.Body.List[1]) != "return err == nil" {
				t.Fatal("bounded-only filter authorization lost its exact owned dispatch")
			}
		}
	}
	if count != 1 {
		t.Fatalf("exact bounded GET/HEAD authorization branches=%d want=1", count)
	}
}
