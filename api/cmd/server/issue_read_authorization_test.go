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
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW256LegacyIssueReadAndLabelAuthorization(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_ACCESS", "on")
	t.Run("label-only-hook-and-literal-denial-boundary", func(t *testing.T) {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			if !scaleP21SW256IssueLabelPointRequest(httptest.NewRequest(method, "/api/issue-labels/missing", nil)) {
				t.Fatal("issue-label point not selected", method)
			}
		}
		for _, request := range []struct{ method, path string }{
			{http.MethodPost, "/api/issue-labels/id"}, {http.MethodPatch, "/api/issue-labels/id"},
			{http.MethodDelete, "/api/issue-labels/id"}, {http.MethodGet, "/api/issue-labels"},
			{http.MethodGet, "/api/issue-labels/"}, {http.MethodGet, "/api/issue-labels/id/children"},
			{http.MethodGet, "/api/issue-suggestions/id"}, {http.MethodGet, "/api/issues/id/permissions"},
			{http.MethodHead, "/api/issues/id/comment-actions"}, {http.MethodGet, "/api/issues/id/suggestions"},
			{http.MethodGet, "/api/issue-records/id/permissions"},
		} {
			if scaleP21SW256IssueLabelPointRequest(httptest.NewRequest(request.method, request.path, nil)) {
				t.Fatal("label helper captured another authorization contract", request)
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
		hooks, issueGuards := 0, 0
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Name.Name != "resourceAllowedLegacy" {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				branch, ok := node.(*ast.IfStmt)
				if ok && branch.Init == nil && text(branch.Cond) == "issueIndex < 0" &&
					text(branch.Body) == "{\n\treturn false\n}" {
					issueGuards++
				}
				return true
			})
			for _, statement := range function.Body.List {
				branch, ok := statement.(*ast.IfStmt)
				if !ok || branch.Init == nil ||
					text(branch.Init) != "allowed, handled := s.boundedIssueLabelPointAuthorization(r, workspace, userID)" {
					continue
				}
				hooks++
				if text(branch.Cond) != "handled" || branch.Else != nil || len(branch.Body.List) != 1 ||
					text(branch.Body.List[0]) != "return allowed" {
					t.Fatal("label hook lost explicit binding or handled-only return")
				}
			}
		}
		if hooks != 1 || issueGuards != 1 {
			t.Fatal("label hook or literal issue-index refusal changed", hooks, issueGuards)
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
			workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw256-")
			public := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records",
				map[string]any{"title": "SW256 visible issue", "teamId": f.public}, workspace, http.StatusCreated)
			metadata, found := repo.WorkspaceMetadata(workspace)
			if !found || len(metadata.Issues) != 0 {
				t.Fatal("ordinary literal metadata unexpectedly contains issue records")
			}
			privateTeam := ""
			for _, project := range metadata.Projects {
				if project.ID == "m2-unrelated" && len(project.TeamIDs) == 1 {
					privateTeam = project.TeamIDs[0]
				}
			}
			page, err := repo.QueryIssueRecords(t.Context(), store.IssueRecordQuery{Workspace: workspace, Archived: "all", Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			private := domain.Issue{}
			for _, issue := range page.Items {
				if issue.Team.ID == privateTeam && slices.ContainsFunc(issue.Permissions, func(permission domain.IssuePermission) bool {
					return permission.SubjectType == "user" && permission.SubjectID == f.principals[3].user.ID && permission.Role == "viewer"
				}) {
					private = issue
				}
			}
			if private.ID == "" {
				t.Fatal("existing M fixture's private issue with explicit guest grant is missing")
			}
			label := prefix + "label"
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw256_label", "", nil, func(data *domain.Bootstrap) error {
				data.Labels = append(data.Labels, domain.IssueLabel{ID: label, Name: "SW256 label", Scope: "workspace"})
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
			visibleDenials := 0
			for _, row := range []struct {
				name, path, issueID string
				methods             []string
				label, foreign      bool
			}{
				{name: "label-present", path: "/api/issue-labels/" + label, methods: []string{http.MethodGet, http.MethodHead}, label: true},
				{name: "label-missing", path: "/api/issue-labels/" + prefix + "missing", methods: []string{http.MethodGet, http.MethodHead}, label: true},
				{name: "label-foreign", path: "/api/issue-labels/" + label, methods: []string{http.MethodGet, http.MethodHead}, label: true, foreign: true},
				{name: "permissions-public", path: "/api/issues/" + public.ID + "/permissions", issueID: public.ID, methods: []string{http.MethodGet, http.MethodHead}},
				{name: "permissions-private-grant", path: "/api/issues/" + private.ID + "/permissions", issueID: private.ID, methods: []string{http.MethodGet, http.MethodHead}},
				{name: "permissions-identifier", path: "/api/issues/" + public.Identifier + "/permissions", issueID: public.ID, methods: []string{http.MethodGet, http.MethodHead}},
				{name: "suggestions-public", path: "/api/issues/" + public.ID + "/suggestions", issueID: public.ID, methods: []string{http.MethodGet, http.MethodHead}},
				{name: "suggestions-private-grant", path: "/api/issues/" + private.ID + "/suggestions", issueID: private.ID, methods: []string{http.MethodGet, http.MethodHead}},
				{name: "comment-actions-head", path: "/api/issues/" + public.ID + "/comment-actions", issueID: public.ID, methods: []string{http.MethodHead}},
				{name: "permissions-missing", path: "/api/issues/" + prefix + "missing/permissions", methods: []string{http.MethodGet, http.MethodHead}},
				{name: "suggestions-missing", path: "/api/issues/" + prefix + "missing/suggestions", methods: []string{http.MethodGet, http.MethodHead}},
				{name: "suggestions-foreign", path: "/api/issues/" + public.ID + "/suggestions", issueID: public.ID, methods: []string{http.MethodGet, http.MethodHead}, foreign: true},
			} {
				for _, method := range row.methods {
					t.Run(method+" "+row.name, func(t *testing.T) {
						f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
							var want outcome
							legacyStatements := 0
							for _, source := range []*store.SQLiteStore{repo, f.probe} {
								request := f.literalRequest(user, key, nil)
								request.Method = method
								var err error
								request.URL, err = url.Parse(row.path)
								if err != nil {
									t.Fatal(err)
								}
								if row.foreign {
									request.Header.Set("X-Workspace-Key", other.data.Workspace.URLKey)
								}
								request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokens[user.ID]})
								request, release := beginRequestResourceReads(request)
								service := &server{store: source}
								sqlVisible := false
								if row.issueID != "" && !row.foreign {
									data, access, err := service.requestIssueQueryAccess(request)
									if err != nil {
										release()
										t.Fatal(err)
									}
									query := store.IssueRecordQuery{Workspace: workspace, Archived: "all"}
									query.Access, query.AllowedTeamIDs = service.issueRecordAccess(request, data, access)
									visible, err := source.VisibleIssueRecordIDs(request.Context(), query, []string{row.issueID})
									if err != nil {
										release()
										t.Fatal(err)
									}
									sqlVisible = visible[row.issueID]
								}
								statements := 0
								request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) {
									statements++
								}))
								recorder := httptest.NewRecorder()
								allowed := service.authorizeWorkspaceRequest(recorder, request, user)
								if allowed {
									recorder.WriteHeader(http.StatusNoContent)
								}
								got := outcome{allowed, recorder.Code, recorder.Body.String()}
								expected := row.label && !row.foreign
								if allowed != expected || !allowed && recorder.Code != http.StatusForbidden {
									release()
									t.Fatalf("literal compatibility requires authorization=%t got=%+v SQL-visible=%t", expected, got, sqlVisible)
								}
								if !source.BoundedMetadataEnabled() {
									want = got
									legacyStatements = statements
									if sqlVisible {
										visibleDenials++
										if handoff, ok := request.Context().Value(pagedMetadataHandoffKey{}).(*pagedMetadataHandoff); !ok || handoff == nil || len(handoff.data.Issues) != 0 {
											release()
											t.Fatal("visible literal denial no longer comes from the ordinary empty issue projection")
										}
									}
								} else {
									recordSQL(recordAccessMode()+" "+method+" "+row.name, legacyStatements, statements)
									if !reflect.DeepEqual(want, got) {
										release()
										t.Fatalf("ordinary legacy read or label authorization differs: literal=%+v bounded=%+v", want, got)
									}
									if row.label && allowed && request.Context().Value(issueRequestAccessKey{}) == nil {
										release()
										t.Fatal("label authorization did not reuse existing query access")
									}
									if request.Context().Value(pagedMetadataHandoffKey{}) != nil {
										release()
										t.Fatal("bounded authorization created a complete paged handoff")
									}
								}
								release()
								release()
								if source.BoundedMetadataEnabled() &&
									(source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
									t.Fatal("authorization leaked work or decoded complete workspace metadata")
								}
							}
						})
					})
				}
			}
			if visibleDenials == 0 {
				t.Fatal("ordinary legacy-denial table never exercised an SQL-visible target")
			}
			t.Run("feature-off-exact-refusal", func(t *testing.T) {
				if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw256_feature_off", "", nil, func(data *domain.Bootstrap) error {
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
					request.Method, request.URL.Path = http.MethodGet, "/api/issues/"+public.ID+"/suggestions"
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
						recordSQL(recordAccessMode()+" GET issue-suggestions feature-off", legacyStatements, statements)
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
