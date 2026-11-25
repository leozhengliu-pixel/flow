package main

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW323HotAuthorizationClosure(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Run("finite-registered-shapes-and-literal-decisions", func(t *testing.T) {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			if !scaleP21SW306MembershipAuthorizationRequest(httptest.NewRequest(method, "/api/files/sign?url=/uploads/image.png", nil)) {
				t.Fatal("file-signing membership shape absent", method)
			}
			for _, kind := range []string{"projects", "initiatives"} {
				got, ref, handled := scaleP21SW255ResourceAuthorizationRequest(httptest.NewRequest(method, "/api/"+kind+"/target/updates/diff-preview", nil))
				if !handled || got != kind || ref != "target" {
					t.Fatal("composer preview did not reuse source-resource authorization", method, kind, got, ref, handled)
				}
			}
		}
		if !scaleP21SW306MembershipAuthorizationRequest(httptest.NewRequest(http.MethodPost, "/api/workflows/workflow/run", nil)) {
			t.Fatal("finite manual workflow-run membership shape absent")
		}
		for _, path := range []string{"/api/workflows/a%2Fb/run", "/%61pi/workflows/a%2Fb/%72un", "/api/workflows/%2F/run"} {
			if !scaleP21SW306MembershipAuthorizationRequest(httptest.NewRequest(http.MethodPost, path, nil)) {
				t.Fatal("registered escaped workflow segment was rejected", path)
			}
		}
		for _, row := range []struct{ method, path string }{
			{http.MethodPost, "/api/files/sign"}, {http.MethodGet, "/api/files/sign/"},
			{http.MethodGet, "/api/files/signed/image.png"}, {http.MethodGet, "/api/files/uploads/token"},
			{http.MethodGet, "/api/files/sign/child"}, {http.MethodPost, "/api/projects/id/updates/diff-preview"},
			{http.MethodGet, "/api/projects//updates/diff-preview"}, {http.MethodHead, "/api/projects/id/relations/diff-preview"},
			{http.MethodGet, "/api/initiatives/id/updates/diff-preview/"}, {http.MethodGet, "/api/projects/id/updates/not-diff-preview"},
			{http.MethodGet, "/api/projects/id/updates/diff-preview/child"},
			{http.MethodGet, "/api/workflows/workflow/run"}, {http.MethodPost, "/api/workflows//run"},
			{http.MethodPost, "/api/workflows/workflow/run/"}, {http.MethodPost, "/api/workflows/workflow/run/child"},
			{http.MethodPost, "/api/workflows"}, {http.MethodPost, "/api/workflows/workflow/retry"},
		} {
			request := httptest.NewRequest(row.method, row.path, nil)
			if scaleP21SW306MembershipAuthorizationRequest(request) {
				t.Fatal("membership extension captured another contract", row)
			}
			if _, _, handled := scaleP21SW255ResourceAuthorizationRequest(request); handled {
				t.Fatal("point extension captured another contract", row)
			}
		}
		text := func(node ast.Node) string {
			var output bytes.Buffer
			if err := format.Node(&output, token.NewFileSet(), node); err != nil {
				t.Fatal(err)
			}
			return output.String()
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), "auth.go", nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		projects, initiatives, fallback := 0, 0, 0
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Name.Name != "resourceAllowedLegacy" {
				continue
			}
			if text(function.Body.List[len(function.Body.List)-1]) != "return true" {
				t.Fatal("literal default resource decision changed")
			}
			for _, statement := range function.Body.List {
				selection, ok := statement.(*ast.SwitchStmt)
				if !ok || selection.Tag == nil || text(selection.Tag) != "parts[1]" {
					continue
				}
				for _, statement := range selection.Body.List {
					clause := statement.(*ast.CaseClause)
					for _, value := range clause.List {
						switch text(value) {
						case `"files"`, `"workflows"`:
							t.Fatal("membership-only route gained a literal per-resource authorization predicate", text(value))
						case `"projects"`, `"initiatives"`:
							name := text(value)
							for _, branch := range clause.Body {
								if branch, ok := branch.(*ast.IfStmt); ok {
									if name == `"projects"` && text(branch.Cond) == "!projectAllowed(parts[2])" {
										projects++
									}
									if name == `"initiatives"` && text(branch.Cond) == "!initiativeAllowed(parts[2])" {
										initiatives++
									}
									// An updates-specific predicate would invalidate
									// reusing the existing point decision.
									ast.Inspect(branch.Cond, func(node ast.Node) bool {
										if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING && literal.Value == `"updates"` {
											t.Fatal("literal preview gained a child-resource predicate", name)
										}
										return true
									})
								}
							}
							if text(clause.Body[len(clause.Body)-1]) != "return true" {
								t.Fatal("literal preview no longer ends in source-resource success", name)
							}
						}
					}
				}
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				if call, ok := node.(*ast.CallExpr); ok && text(call.Fun) == "repo.PagedWorkspaceMetadata" {
					fallback++
				}
				return true
			})
		}
		if projects != 1 || initiatives != 1 || fallback != 1 {
			t.Fatal("literal resource/default/fallback contracts changed", projects, initiatives, fallback)
		}
		registered := map[string]bool{
			"GET /api/files/sign":                            false,
			"GET /api/projects/{id}/updates/diff-preview":    false,
			"GET /api/initiatives/{id}/updates/diff-preview": false,
			"POST /api/workflows/{id}/run":                   false,
		}
		for _, filename := range []string{"main.go", "public_api_routes.go"} {
			parsed, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(parsed, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || len(call.Args) < 1 {
					return true
				}
				function, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || function.Sel.Name != "HandleFunc" {
					return true
				}
				if literal, ok := call.Args[0].(*ast.BasicLit); ok && literal.Kind == token.STRING {
					pattern, err := strconv.Unquote(literal.Value)
					if err == nil {
						if _, selected := registered[pattern]; selected {
							registered[pattern] = true
						}
					}
				}
				return true
			})
		}
		for pattern, found := range registered {
			if !found {
				t.Fatal("proposed authorization shape is no longer registered", pattern)
			}
		}
	})
	type outcome struct {
		Allowed bool
		Status  int
		Body    string
		Pattern string
	}
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			other := scaleP03Fixture(t, repo)
			f := scaleP21M2Setup(t, repo)
			workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw323-")
			project, initiative := prefix+"project", prefix+"initiative"
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw323_seed", "", nil, func(data *domain.Bootstrap) error {
				data.Projects = append(data.Projects,
					domain.Project{ID: project, SlugID: prefix + "project-current", PreviousSlugIDs: []string{prefix + "project-old"}, TeamIDs: []string{f.public}},
					domain.Project{ID: prefix + "unscoped-project", TeamIDs: []string{}},
				)
				data.Initiatives = append(data.Initiatives,
					domain.Initiative{ID: initiative, SlugID: prefix + "initiative-current", PreviousSlugIDs: []string{prefix + "initiative-old"}, ProjectIDs: []string{project}, ContributingTeamIDs: []string{f.public}},
					domain.Initiative{ID: prefix + "unscoped-initiative", ProjectIDs: []string{}, ContributingTeamIDs: []string{}},
				)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			tokens := map[string]string{}
			for _, principal := range f.principals {
				_, token, err := repo.Login(t.Context(), principal.user.Email, "p03-fixture-password")
				if err != nil {
					t.Fatal(err)
				}
				tokens[principal.user.ID] = token
			}
			mux := http.NewServeMux()
			for _, pattern := range []string{"GET /api/files/sign", "GET /api/projects/{id}/updates/diff-preview", "GET /api/initiatives/{id}/updates/diff-preview", "POST /api/workflows/{id}/run"} {
				mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPost {
						w.WriteHeader(http.StatusAccepted)
					} else {
						w.WriteHeader(http.StatusNoContent)
					}
				})
			}
			pairs, successes, denials, legacySQL, boundedSQL, increased := 0, 0, 0, 0, 0, 0
			for _, row := range []struct {
				name, kind, ref, mode               string
				foreign, missing, unscoped, private bool
			}{
				{name: "sign-membership", mode: "off"},
				{name: "sign-foreign", mode: "shadow", foreign: true},
				{name: "project-public", kind: "projects", ref: project, mode: "off"},
				{name: "project-old-slug", kind: "projects", ref: prefix + "project-old", mode: "shadow"},
				{name: "project-private", kind: "projects", ref: "m2-unrelated", mode: "on", private: true},
				{name: "project-unscoped", kind: "projects", ref: prefix + "unscoped-project", mode: "off", unscoped: true},
				{name: "project-missing", kind: "projects", ref: prefix + "missing", mode: "shadow", missing: true},
				{name: "project-foreign", kind: "projects", ref: project, mode: "on", foreign: true},
				{name: "initiative-public", kind: "initiatives", ref: initiative, mode: "on"},
				{name: "initiative-old-slug", kind: "initiatives", ref: prefix + "initiative-old", mode: "off"},
				{name: "initiative-unscoped", kind: "initiatives", ref: prefix + "unscoped-initiative", mode: "shadow", unscoped: true},
				{name: "initiative-missing", kind: "initiatives", ref: prefix + "missing", mode: "on", missing: true},
				{name: "workflow-run-admin-gate", kind: "workflows", ref: prefix + "workflow", mode: "off"},
				{name: "workflow-run-escaped-id", kind: "workflows", ref: prefix + "workflow/escaped", mode: "on"},
				{name: "workflow-run-foreign", kind: "workflows", ref: prefix + "workflow", mode: "shadow", foreign: true},
			} {
				t.Run(row.name, func(t *testing.T) {
					t.Setenv("FLOW_RECORD_ACCESS", row.mode)
					methods := []string{http.MethodGet, http.MethodHead}
					if row.kind == "workflows" {
						methods = []string{http.MethodPost}
					}
					for _, method := range methods {
						t.Run(method, func(t *testing.T) {
							f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
								var want outcome
								beforeSQL := 0
								for _, source := range []*store.SQLiteStore{repo, f.probe} {
									request := f.literalRequest(user, key, nil)
									path := "/api/files/sign?url=/uploads/sw323-policy-only.png"
									pattern := "GET /api/files/sign"
									status := http.StatusNoContent
									if row.kind == "workflows" {
										path = "/api/workflows/" + url.PathEscape(row.ref) + "/run"
										pattern, status = "POST /api/workflows/{id}/run", http.StatusAccepted
									} else if row.kind != "" {
										path = "/api/" + row.kind + "/" + row.ref + "/updates/diff-preview"
										pattern = "GET /api/" + row.kind + "/{id}/updates/diff-preview"
									}
									request.Method = method
									var err error
									request.URL, err = url.Parse(path)
									if err != nil {
										t.Fatal(err)
									}
									if row.foreign {
										request.Header.Set("X-Workspace-Key", other.data.Workspace.URLKey)
									}
									request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokens[user.ID]})
									request, release := beginRequestResourceReads(request)
									service := &server{store: source}
									if !source.BoundedMetadataEnabled() {
										var handled bool
										if row.kind == "" || row.kind == "workflows" {
											_, handled = service.boundedMembershipAuthorization(request, workspace, user.ID)
										} else {
											_, handled = service.boundedPointResourceAuthorization(request, workspace, user.ID)
										}
										if handled {
											release()
											t.Fatal("adapter changed flag-off source")
										}
									}
									statements := 0
									request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { statements++ }))
									recorder := httptest.NewRecorder()
									allowed := service.authorizeWorkspaceRequest(recorder, request, user)
									actualPattern := ""
									if allowed {
										_, actualPattern = mux.Handler(request)
										mux.ServeHTTP(recorder, request)
									}
									got := outcome{allowed, recorder.Code, recorder.Body.String(), actualPattern}
									guest := user.ID == f.principals[3].user.ID
									selectedEmpty := key != nil && apiKeyTeamRestrictionSelected(*key) && len(key.TeamIDs) == 0
									required := -1
									switch {
									case row.foreign || row.missing || row.kind == "initiatives" && guest:
										required = 0
									case row.kind == "workflows":
										required = 0
										if user.ID == f.principals[0].user.ID || user.ID == f.principals[1].user.ID {
											required = 1
										}
									case row.kind == "" || row.unscoped:
										required = 1
									case !row.private:
										required = 1
										if selectedEmpty {
											required = 0
										}
									case user.ID == f.principals[1].user.ID && (key == nil || !apiKeyTeamRestrictionSelected(*key)):
										required = 1
									}
									if required >= 0 && allowed != (required == 1) || allowed && (recorder.Code != status || actualPattern != pattern) || !allowed && recorder.Code != http.StatusForbidden {
										release()
										t.Fatalf("required=%d actual=%+v expectedPattern=%s", required, got, pattern)
									}
									if !source.BoundedMetadataEnabled() {
										want, beforeSQL = got, statements
									} else {
										pairs++
										legacySQL, boundedSQL = legacySQL+beforeSQL, boundedSQL+statements
										if statements > beforeSQL {
											increased++
										}
										if !reflect.DeepEqual(want, got) {
											release()
											t.Fatalf("literal/bounded policy differs: literal=%+v bounded=%+v", want, got)
										}
										if allowed {
											successes++
										} else {
											denials++
										}
										if request.Context().Value(pagedMetadataHandoffKey{}) != nil {
											release()
											t.Fatal("bounded policy created complete paged metadata")
										}
									}
									release()
									release()
									if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
										t.Fatal("bounded policy leaked work or decoded complete metadata")
									}
								}
							})
						})
					}
				})
			}
			if pairs != 540 || successes == 0 || denials == 0 {
				t.Fatal("grouped policy table lost cells or required controls", pairs, successes, denials)
			}
			t.Logf("SW323 policy pairs=%d SQL literal/bounded=%d/%d increased=%d success/denial=%d/%d", pairs, legacySQL, boundedSQL, increased, successes, denials)
		})
	}
}
