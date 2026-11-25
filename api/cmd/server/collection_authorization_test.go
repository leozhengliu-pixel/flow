package main

import (
	"bytes"
	"context"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP21SW253HotCollectionAuthorization(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_ACCESS", "on")
	t.Run("exact-method-and-path-boundary", func(t *testing.T) {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			for _, path := range []string{
				"/api/projects", "/api/initiatives", "/api/project-milestones", "/api/comments",
				"/api/issue-labels", "/api/issue-suggestions", "/api/documents?connection=true",
				"/api/documents?connection=1", "/api/documents?first=1", "/api/documents?last=1",
				"/api/documents?after=x", "/api/documents?before=x", "/api/documents?orderBy=id",
			} {
				if !scaleP21SW253CollectionAuthorizationRequest(httptest.NewRequest(method, path, nil)) {
					t.Fatal("membership-only collection root not selected", method, path)
				}
			}
		}
		for _, path := range []string{
			"/api/projects", "/api/initiatives", "/api/project-milestones", "/api/comments",
			"/api/issue-labels", "/api/issue-suggestions", "/api/documents?connection=true",
		} {
			for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete, http.MethodOptions} {
				if scaleP21SW253CollectionAuthorizationRequest(httptest.NewRequest(method, path, nil)) {
					t.Fatal("collection authorization captured a mutation or other method", method, path)
				}
			}
		}
		for _, path := range []string{
			"/api/projects/", "/api/projects/target", "/api/projects/target/updates",
			"/api/initiatives/target/relations", "/api/documents", "/api/documents?connection=false",
			"/api/documents/target?connection=true", "/api/issue-labels/target", "/api/comments/target",
			"/api/issues/target/permissions", "/api/issue-records/target", "/api/search/filter-suggestions",
			"/api/workspaces/target/teams", "/api/desktop-notifications/ack",
		} {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				if scaleP21SW253CollectionAuthorizationRequest(httptest.NewRequest(method, path, nil)) {
					t.Fatal("collection authorization bypassed an independent route predicate", method, path)
				}
			}
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), "auth.go", nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		text := func(node ast.Node) string {
			var out bytes.Buffer
			if err := format.Node(&out, token.NewFileSet(), node); err != nil {
				t.Fatal(err)
			}
			return out.String()
		}
		hooks := 0
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Name.Name != "resourceAllowedLegacy" {
				continue
			}
			for _, statement := range function.Body.List {
				branch, ok := statement.(*ast.IfStmt)
				if !ok || branch.Init == nil ||
					text(branch.Init) != "allowed, handled := s.boundedHotCollectionAuthorization(r, workspace, userID)" {
					continue
				}
				hooks++
				if text(branch.Cond) != "handled" || branch.Else != nil || len(branch.Body.List) != 1 ||
					text(branch.Body.List[0]) != "return allowed" {
					t.Fatal("collection hook lost its explicit binding or handled-only return")
				}
			}
		}
		if hooks != 1 {
			t.Fatal("collection authorization hook absent or duplicated", hooks)
		}
	})
	type outcome struct {
		Allowed bool
		Status  int
		Body    string
	}
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			type sqlCounts struct {
				pairs, legacy, bounded, legacyMax, boundedMax       int
				regressions, maxIncrease, worstLegacy, worstBounded int
			}
			var totalSQL sqlCounts
			routeSQL := map[string]*sqlCounts{}
			var sqlRoutes []string
			recordSQL := func(route string, legacy, bounded int) {
				if routeSQL[route] == nil {
					routeSQL[route] = &sqlCounts{}
					sqlRoutes = append(sqlRoutes, route)
				}
				for _, counts := range []*sqlCounts{&totalSQL, routeSQL[route]} {
					counts.pairs++
					counts.legacy += legacy
					counts.bounded += bounded
					counts.legacyMax = max(counts.legacyMax, legacy)
					counts.boundedMax = max(counts.boundedMax, bounded)
					if bounded > legacy {
						counts.regressions++
						if bounded-legacy > counts.maxIncrease {
							counts.maxIncrease = bounded - legacy
							counts.worstLegacy, counts.worstBounded = legacy, bounded
						}
					}
				}
			}
			defer func() {
				for _, route := range append([]string{"TOTAL"}, sqlRoutes...) {
					counts := &totalSQL
					if route != "TOTAL" {
						counts = routeSQL[route]
					}
					t.Logf("authorization SQL %s pairs=%d legacy/bounded=%d/%d max=%d/%d regressed-pairs=%d worst-pair=%d/%d increase=%d", route, counts.pairs, counts.legacy, counts.bounded, counts.legacyMax, counts.boundedMax, counts.regressions, counts.worstLegacy, counts.worstBounded, counts.maxIncrease)
				}
			}()
			other := scaleP03Fixture(t, repo)
			f := scaleP21M2Setup(t, repo)
			workspace := f.data.Workspace.URLKey
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw253_features", "", nil, func(data *domain.Bootstrap) error {
				if data.WorkspaceSettings.FeatureFlags == nil {
					data.WorkspaceSettings.FeatureFlags = map[string]bool{}
				}
				data.WorkspaceSettings.FeatureFlags["triage-intelligence"] = true
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
			for _, target := range []string{
				"/api/projects", "/api/initiatives", "/api/documents?connection=true",
				"/api/project-milestones", "/api/comments?issueId=sw253-unselected",
				"/api/issue-labels", "/api/issue-suggestions",
			} {
				for _, method := range []string{http.MethodGet, http.MethodHead} {
					t.Run(method+" "+target, func(t *testing.T) {
						f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
							type cell struct {
								name, workspace, binding string
								suspended                bool
								want                     bool
							}
							active := target != "/api/initiatives" || user.ID != f.principals[3].user.ID
							cases := []cell{
								{name: "active", workspace: workspace, want: active},
								{name: "foreign-workspace", workspace: other.data.Workspace.URLKey},
							}
							if user.ID == f.principals[2].user.ID {
								cases = append(cases, cell{name: "suspended", workspace: workspace, suspended: true})
							}
							if target == "/api/initiatives" {
								cases = append(cases, cell{name: "direct-root-guest-policy", workspace: workspace, binding: "direct", want: active})
							}
							if target == "/api/projects" && method == http.MethodGet {
								cases = append(cases,
									cell{name: "explicit-workspace-argument", workspace: workspace, binding: "workspace", want: true},
									cell{name: "explicit-viewer-argument", workspace: workspace, binding: "viewer", want: true})
							}
							for _, item := range cases {
								t.Run(item.name, func(t *testing.T) {
									if item.suspended {
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
									legacyStatements := 0
									for _, source := range []*store.SQLiteStore{repo, f.probe} {
										request := f.literalRequest(user, key, nil)
										request.Method = method
										var err error
										request.URL, err = url.Parse(target)
										if err != nil {
											t.Fatal(err)
										}
										request.Header.Set("X-Workspace-Key", item.workspace)
										request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokens[user.ID]})
										if item.binding == "workspace" {
											request.Header.Set("X-Workspace-Key", other.data.Workspace.URLKey)
											request = request.WithContext(context.WithValue(request.Context(), workspaceKeyContextKey{}, other.data.Workspace.URLKey))
										} else if item.binding == "viewer" {
											request = request.WithContext(context.WithValue(request.Context(), authUserContextKey{}, other.data.Viewer))
										}
										beforeWorkspace, beforeUser := workspaceKey(request), authUser(request).ID
										if boundedIssueAuthorizationRequest(request) {
											t.Fatal("shared issue predicate changed literal collection authorization scope")
										}
										request, release := beginRequestResourceReads(request)
										service := &server{store: source}
										if !source.BoundedMetadataEnabled() {
											if _, handled := service.boundedHotCollectionAuthorization(request, item.workspace, user.ID); handled {
												release()
												t.Fatal("collection helper changed the literal OFF path")
											}
										}
										statements := 0
										request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) {
											statements++
										}))
										recorder := httptest.NewRecorder()
										var allowed bool
										if item.binding == "" {
											allowed = service.authorizeWorkspaceRequest(recorder, request, user)
										} else {
											allowed = service.resourceAllowedLegacy(request, item.workspace, user.ID)
											if !allowed {
												writeError(recorder, http.StatusForbidden, "This resource is outside your teams")
											}
										}
										if allowed {
											recorder.WriteHeader(http.StatusNoContent)
										}
										got := outcome{allowed, recorder.Code, recorder.Body.String()}
										if got.Allowed != item.want || !allowed && recorder.Code != http.StatusForbidden {
											release()
											t.Fatalf("required authorization=%t got=%+v", item.want, got)
										}
										if workspaceKey(request) != beforeWorkspace || authUser(request).ID != beforeUser {
											release()
											t.Fatal("explicit authorization binding changed the containing request")
										}
										if !source.BoundedMetadataEnabled() {
											want = got
											legacyStatements = statements
										} else {
											recordSQL(recordAccessMode()+" "+method+" "+target+" binding="+item.binding, legacyStatements, statements)
											if !reflect.DeepEqual(want, got) {
												release()
												t.Fatalf("collection authorization differs: literal=%+v bounded=%+v", want, got)
											}
											if allowed && item.binding == "" && request.Context().Value(issueRequestAccessKey{}) == nil {
												release()
												t.Fatal("bounded collection authorization did not reuse issue access")
											}
											if request.Context().Value(pagedMetadataHandoffKey{}) != nil {
												release()
												t.Fatal("bounded collection authorization handed off complete legacy metadata")
											}
										}
										release()
										release()
										if source.BoundedMetadataEnabled() &&
											(source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
											t.Fatal("collection authorization retained work or decoded complete metadata")
										}
									}
								})
							}
						})
					})
				}
			}
			t.Run("feature-off-exact-refusal", func(t *testing.T) {
				if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw253_feature_off", "", nil, func(data *domain.Bootstrap) error {
					data.WorkspaceSettings.FeatureFlags["triage-intelligence"] = false
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
				user := f.principals[2].user
				var want outcome
				legacyStatements := 0
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					request := f.literalRequest(user, nil, nil)
					request.Method, request.URL.Path = http.MethodGet, "/api/issue-suggestions"
					request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokens[user.ID]})
					request, release := beginRequestResourceReads(request)
					statements := 0
					request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { statements++ }))
					recorder := httptest.NewRecorder()
					allowed := (&server{store: source}).authorizeWorkspaceRequest(recorder, request, user)
					got := outcome{allowed, recorder.Code, recorder.Body.String()}
					if allowed || recorder.Code != http.StatusForbidden || !bytes.Contains(recorder.Body.Bytes(), []byte("This workspace feature is disabled")) {
						release()
						t.Fatalf("feature-off route did not retain its outer refusal: %+v", got)
					}
					if !source.BoundedMetadataEnabled() {
						want, legacyStatements = got, statements
					} else {
						recordSQL(recordAccessMode()+" GET /api/issue-suggestions feature-off", legacyStatements, statements)
						if !reflect.DeepEqual(want, got) {
							release()
							t.Fatalf("feature-off refusal differs: literal=%+v bounded=%+v", want, got)
						}
					}
					if request.Context().Value(pagedMetadataHandoffKey{}) != nil {
						release()
						t.Fatal("feature-off refusal reached the whole projection")
					}
					release()
					release()
					if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
						t.Fatal("feature-off refusal retained work or decoded complete metadata")
					}
				}
			})
		})
	}
}
