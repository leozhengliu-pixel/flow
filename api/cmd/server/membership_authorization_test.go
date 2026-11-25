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
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP21SW306MembershipAuthorization(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_ACCESS", "on")
	type route struct{ method, path string }
	routes := []route{
		{http.MethodGet, "/api/issues"}, {http.MethodHead, "/api/issues"},
		{http.MethodPost, "/api/workspaces/workspace/teams"},
		{http.MethodPost, "/api/desktop-notifications/ack"},
		{http.MethodGet, "/uploads/name"}, {http.MethodHead, "/uploads/name"},
		{http.MethodPost, "/api/agent/sessions/stream"},
		{http.MethodPost, "/api/agent/sessions/session/messages/stream"},
		{http.MethodPost, "/api/agent/sessions/session/approvals/approval"},
	}
	literalCaseNames := map[string]bool{
		"issues": true, "projects": true, "initiatives": true, "views": true,
		"dashboards": true, "customers": true, "customer-requests": true, "asks": true,
		"favorites": true, "subscriptions": true, "reviews": true, "documents": true,
		"releases": true, "release-pipelines": true, "cycles": true, "teams": true,
		"notifications": true, "workspaces": true,
	}
	var reservedNames []string
	for name := range literalCaseNames {
		reservedNames = append(reservedNames, name)
	}
	slices.Sort(reservedNames)
	for _, name := range reservedNames {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			routes = append(routes, route{method, "/uploads/" + name})
		}
	}
	for _, name := range []string{"realtime", "default-favorites"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			routes = append(routes, route{method, "/uploads/" + name})
		}
	}
	t.Run("finite-membership-only-boundary", func(t *testing.T) {
		for _, row := range routes {
			if !scaleP21SW306MembershipAuthorizationRequest(httptest.NewRequest(row.method, row.path, nil)) {
				t.Fatal("membership-only registered route not selected", row)
			}
		}
		for _, row := range []struct{ method, path string }{
			{http.MethodPost, "/api/issues"}, {http.MethodGet, "/api/issues/id"},
			{http.MethodHead, "/api/issues/id/permissions"}, {http.MethodGet, "/api/issues/"},
			{http.MethodGet, "/api/workspaces/workspace/teams"}, {http.MethodPost, "/api/workspaces//teams"},
			{http.MethodPost, "/api/workspaces/workspace/teams/team"},
			{http.MethodPost, "/api/workspaces/workspace/teams/"},
			{http.MethodGet, "/api/desktop-notifications/ack"}, {http.MethodPost, "/api/desktop-notifications/ack/"},
			{http.MethodPost, "/uploads/name"}, {http.MethodGet, "/uploads/"},
			{http.MethodGet, "/uploads/name/child"}, {http.MethodGet, "/api/teams/team/resources"},
			{http.MethodGet, "/api/agent/sessions/stream"}, {http.MethodPost, "/api/agent/sessions/stream/"},
			{http.MethodPost, "/api/agent/sessions//messages/stream"},
			{http.MethodPatch, "/api/agent/sessions/session/messages/message/stream"},
			{http.MethodPost, "/api/agent/sessions/session/messages"},
			{http.MethodPost, "/api/agent/sessions/session/approvals/"},
			{http.MethodPost, "/api/agent/sessions/session/approvals/approval/extra"},
		} {
			if scaleP21SW306MembershipAuthorizationRequest(httptest.NewRequest(row.method, row.path, nil)) {
				t.Fatal("membership adapter captured another predicate", row)
			}
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), "auth.go", nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		text := func(node ast.Node) string {
			var output bytes.Buffer
			if err := format.Node(&output, token.NewFileSet(), node); err != nil {
				t.Fatal(err)
			}
			return output.String()
		}
		hooks, issueRoots, workspaceRoots := 0, 0, 0
		guestRoots := map[string]bool{}
		literalRoots := map[string]bool{}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Name.Name != "resourceAllowedLegacy" {
				continue
			}
			for _, statement := range function.Body.List {
				branch, ok := statement.(*ast.IfStmt)
				if !ok || branch.Init == nil || text(branch.Init) != "allowed, handled := s.boundedMembershipAuthorization(r, workspace, userID)" {
					continue
				}
				hooks++
				if text(branch.Cond) != "handled" || branch.Else != nil || len(branch.Body.List) != 1 || text(branch.Body.List[0]) != "return allowed" {
					t.Fatal("membership hook lost explicit arguments or handled-only return")
				}
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				selection, ok := node.(*ast.SwitchStmt)
				if !ok || selection.Tag == nil || text(selection.Tag) != "parts[1]" {
					return true
				}
				for _, statement := range selection.Body.List {
					clause := statement.(*ast.CaseClause)
					for _, value := range clause.List {
						literal, ok := value.(*ast.BasicLit)
						if !ok || len(literal.Value) < 2 {
							t.Fatal("literal authorization root is no longer a string", text(value))
						}
						literalRoots[literal.Value[1:len(literal.Value)-1]] = true
						name := literal.Value[1 : len(literal.Value)-1]
						if name != "workspaces" {
							condition := "len(parts) < 3"
							expected := "return true"
							if name == "favorites" || name == "subscriptions" {
								condition = "len(parts) < 4"
							} else if name == "documents" {
								condition = "len(parts) == 2 && (r.Method == http.MethodGet || r.Method == http.MethodHead)"
							} else if name == "initiatives" || name == "customers" || name == "customer-requests" {
								expected = `return data.ViewerRole != "guest"`
							}
							found := false
							for _, body := range clause.Body {
								branch, ok := body.(*ast.IfStmt)
								if ok && branch.Init == nil && text(branch.Cond) == condition {
									found = branch.Else == nil && len(branch.Body.List) == 1 && text(branch.Body.List[0]) == expected
								}
							}
							if !found {
								t.Fatal("literal upload length-two predicate changed", name, condition, expected)
							}
						}
						switch text(value) {
						case `"agent"`, `"desktop-notifications"`:
							t.Fatal("membership-only default gained a resource predicate", text(value))
						case `"issues"`:
							if len(clause.Body) < 2 || text(clause.Body[1]) != "if len(parts) < 3 {\n\treturn true\n}" {
								t.Fatal("literal issue collection gained a target predicate")
							}
							issueRoots++
						case `"initiatives"`, `"customers"`, `"customer-requests"`:
							found := false
							for _, body := range clause.Body {
								branch, ok := body.(*ast.IfStmt)
								if ok && branch.Init == nil && text(branch.Cond) == "len(parts) < 3" {
									found = branch.Else == nil && len(branch.Body.List) == 1 && text(branch.Body.List[0]) == `return data.ViewerRole != "guest"`
								}
							}
							if !found {
								t.Fatal("literal upload role-only root changed", text(value))
							}
							guestRoots[literal.Value[1:len(literal.Value)-1]] = true
						case `"workspaces"`:
							if len(clause.Body) != 1 || text(clause.Body[0]) != "if len(parts) >= 5 && parts[3] == \"teams\" {\n\treturn teamRowAllowed(s.store.ResolveTeamID(workspace, parts[4]))\n}" {
								t.Fatal("literal workspace team collection predicate changed")
							}
							workspaceRoots++
						}
					}
				}
				return true
			})
		}
		if hooks != 1 || issueRoots != 1 || workspaceRoots != 1 {
			t.Fatal("literal membership boundary or mounted hook absent/duplicated", hooks, issueRoots, workspaceRoots)
		}
		if !reflect.DeepEqual(literalRoots, literalCaseNames) || len(guestRoots) != 3 {
			t.Fatal("literal upload root/role case set changed", literalRoots, literalCaseNames, guestRoots)
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
			tokens := map[string]string{}
			for _, principal := range f.principals {
				_, token, err := repo.Login(t.Context(), principal.user.Email, "p03-fixture-password")
				if err != nil {
					t.Fatal(err)
				}
				tokens[principal.user.ID] = token
				account, err := repo.OAuthAccountForUser(t.Context(), principal.user.ID)
				if err != nil || account.LastWorkspaceKey != workspace {
					t.Fatal("missing-workspace fixture does not resolve to the intended workspace", err)
				}
			}
			for _, row := range routes {
				t.Run(row.method+" "+row.path, func(t *testing.T) {
					literalSuccesses := 0
					f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
						cells := []string{"supplied", "missing", "foreign", "explicit-workspace", "explicit-viewer"}
						reservedUpload := strings.HasPrefix(row.path, "/uploads/") && row.path != "/uploads/name"
						if reservedUpload {
							cells = []string{"supplied"}
						}
						if row.path == "/api/workspaces/workspace/teams" {
							cells = append(cells, "foreign-path")
						}
						for _, cell := range cells {
							t.Run(cell, func(t *testing.T) {
								var want outcome
								legacyStatements := 0
								for _, source := range []*store.SQLiteStore{repo, f.probe} {
									request := f.literalRequest(user, key, nil)
									request.Method = row.method
									path := row.path
									if path == "/api/workspaces/workspace/teams" {
										selected := workspace
										if cell == "foreign-path" {
											selected = other.data.Workspace.URLKey
										}
										path = "/api/workspaces/" + selected + "/teams"
									}
									var err error
									request.URL, err = url.Parse(path)
									if err != nil {
										t.Fatal(err)
									}
									if cell == "missing" {
										request.Header.Del("X-Workspace-Key")
									} else if cell == "foreign" || cell == "explicit-workspace" {
										request.Header.Set("X-Workspace-Key", other.data.Workspace.URLKey)
									}
									if cell == "explicit-workspace" {
										request = request.WithContext(context.WithValue(request.Context(), workspaceKeyContextKey{}, other.data.Workspace.URLKey))
									} else if cell == "explicit-viewer" {
										request = request.WithContext(context.WithValue(request.Context(), authUserContextKey{}, other.data.Viewer))
									}
									request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokens[user.ID]})
									beforeWorkspace, beforeUser := workspaceKey(request), authUser(request).ID
									request, release := beginRequestResourceReads(request)
									service := &server{store: source}
									if !source.BoundedMetadataEnabled() {
										if _, handled := service.boundedMembershipAuthorization(request, workspace, user.ID); handled {
											release()
											t.Fatal("membership helper changed the literal OFF path")
										}
									}
									statements := 0
									request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { statements++ }))
									recorder := httptest.NewRecorder()
									direct := cell == "explicit-workspace" || cell == "explicit-viewer"
									var allowed bool
									if direct {
										allowed = service.resourceAllowedLegacy(request, workspace, user.ID)
										if !allowed {
											writeError(recorder, http.StatusForbidden, "This resource is outside your teams")
										}
									} else {
										allowed = service.authorizeWorkspaceRequest(recorder, request, user)
									}
									if allowed {
										recorder.WriteHeader(http.StatusNoContent)
									}
									got := outcome{allowed, recorder.Code, recorder.Body.String()}
									foreign := cell == "foreign-path" || cell == "foreign" && row.path != "/api/workspaces/workspace/teams"
									roleOnlyUpload := row.path == "/uploads/initiatives" || row.path == "/uploads/customers" || row.path == "/uploads/customer-requests"
									guestRefusal := roleOnlyUpload && user.ID == f.principals[3].user.ID
									if direct && !allowed || foreign && allowed || guestRefusal && allowed || row.path == "/uploads/default-favorites" && allowed {
										release()
										t.Fatalf("required binding/foreign authorization direct=%t foreign=%t got=%+v", direct, foreign, got)
									}
									if direct && (workspaceKey(request) != beforeWorkspace || authUser(request).ID != beforeUser) {
										release()
										t.Fatal("explicit authorization arguments changed the containing request")
									}
									if !source.BoundedMetadataEnabled() {
										want, legacyStatements = got, statements
										if allowed && cell == "supplied" {
											literalSuccesses++
										}
									} else {
										recordSQL(recordAccessMode()+" "+row.method+" "+row.path+" "+cell, legacyStatements, statements)
										if !reflect.DeepEqual(want, got) {
											release()
											t.Fatalf("membership authorization differs: literal=%+v bounded=%+v", want, got)
										}
										if request.Context().Value(pagedMetadataHandoffKey{}) != nil {
											release()
											t.Fatal("bounded membership authorization created a complete paged handoff")
										}
										earlyUpload := row.path == "/uploads/notifications" || row.path == "/uploads/realtime"
										if allowed && !direct && workspaceKey(request) != "" && request.Context().Value(issueRequestAccessKey{}) == nil && row.path != "/api/workspaces/workspace/teams" && !earlyUpload {
											release()
											t.Fatal("bounded membership authorization did not reuse existing issue access")
										}
									}
									release()
									release()
									if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
										t.Fatal("membership authorization retained work or decoded complete metadata")
									}
								}
							})
						}
					})
					if literalSuccesses == 0 && row.path != "/uploads/default-favorites" {
						t.Fatal("membership table never exercised a literal-success supplied-workspace request")
					}
				})
			}
		})
	}
}
