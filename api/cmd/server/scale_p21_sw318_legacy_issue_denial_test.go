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
	"reflect"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW318EmptyLegacyIssueAuthorization(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_ACCESS", "on")
	type outcome struct {
		Allowed bool
		Status  int
		Body    string
	}
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			// Scoped BeginWorkspaceRead has a canonical root-page budget. Keep
			// that constructor's real matching workspace below the budget,
			// rather than shrinking M2's unrelated large-data witnesses.
			small := scaleP03Fixture(t, repo)
			f := scaleP21M2Setup(t, repo)
			workspace := f.data.Workspace.URLKey
			public := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records",
				map[string]any{"title": "SW318 public issue", "teamId": f.public}, workspace, http.StatusCreated)
			metadata, found := repo.WorkspaceMetadata(workspace)
			if !found || len(metadata.Issues) != 0 {
				t.Fatal("ordinary literal source unexpectedly has issue records")
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
				t.Fatal("M2 explicit private issue grant missing")
			}
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw318_feature", "", nil, func(data *domain.Bootstrap) error {
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
			users := []domain.User{small.data.Viewer}
			for _, principal := range f.principals {
				users = append(users, principal.user)
			}
			for _, user := range users {
				_, token, err := repo.Login(t.Context(), user.Email, "p03-fixture-password")
				if err != nil {
					t.Fatal(err)
				}
				tokens[user.ID] = token
			}
			pairs, visibleDenials, carriedIssues, nativeAllows := 0, 0, 0, 0
			legacySQL, boundedSQL, increasedPairs := 0, 0, 0
			for _, row := range []struct {
				name, method, path, issueID, mode string
				small, native, realtime           bool
			}{
				{"permissions-get", http.MethodGet, "/api/issues/" + public.ID + "/permissions", public.ID, "", false, false, false},
				{"permissions-head", http.MethodHead, "/api/issues/" + public.ID + "/permissions", public.ID, "", false, false, false},
				{"suggestions-get", http.MethodGet, "/api/issues/" + public.ID + "/suggestions", public.ID, "", false, false, false},
				{"suggestions-head", http.MethodHead, "/api/issues/" + public.ID + "/suggestions", public.ID, "", false, false, false},
				{"comment-actions-head", http.MethodHead, "/api/issues/" + public.ID + "/comment-actions", public.ID, "", false, false, false},
				{"private-explicit-grant", http.MethodGet, "/api/issues/" + private.ID + "/permissions", private.ID, "", false, false, false},
				{"identifier", http.MethodGet, "/api/issues/" + public.Identifier + "/permissions", public.ID, "", false, false, false},
				{"missing", http.MethodGet, "/api/issues/" + idgen.New("sw318-missing-") + "/permissions", "", "", false, false, false},
				{"issue-detail-header", http.MethodGet, "/api/issues/" + public.ID + "/permissions", public.ID, "header", false, false, false},
				{"authorization-context", http.MethodGet, "/api/issues/" + public.ID + "/permissions", public.ID, "authorization", false, false, false},
				{"issue-record-mutations", http.MethodGet, "/api/issues/" + public.ID + "/permissions", public.ID, "mutations", false, false, false},
				{"query-access-cache", http.MethodGet, "/api/issues/" + public.ID + "/permissions", public.ID, "cache", false, false, false},
				{"scoped-workspace-read", http.MethodGet, "/api/issues/absent/permissions", "", "scope", true, false, false},
				{"general-bootstrap-context", http.MethodHead, "/api/issues/absent/permissions", "", "general", true, false, false},
				{"native-alias-public", http.MethodGet, "/api/issue-records/" + public.ID + "/permissions", public.ID, "", false, true, false},
				{"native-alias-private-grant", http.MethodGet, "/api/issue-records/" + private.ID + "/permissions", private.ID, "", false, true, false},
				{"native-realtime", http.MethodGet, "/api/realtime/events", "", "", false, true, true},
			} {
				t.Run(row.name, func(t *testing.T) {
					visit := func(t *testing.T, user domain.User, key *domain.APIKey) {
						var want outcome
						beforeSQL := 0
						for _, source := range []*store.SQLiteStore{repo, f.probe} {
							request := f.literalRequest(user, key, nil)
							request.Method, request.URL.Path = row.method, row.path
							selectedWorkspace := workspace
							if row.small {
								selectedWorkspace = small.data.Workspace.URLKey
								request.Header.Set("X-Workspace-Key", selectedWorkspace)
							}
							request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokens[user.ID]})
							request, release := beginRequestResourceReads(request)
							service := &server{store: source}
							closeSource := func() {}
							switch row.mode {
							case "header":
								request.Header.Set("X-Flow-Projection", "issue-detail")
							case "mutations":
								request = request.WithContext(store.WithIssueRecordMutations(request.Context(), public.ID))
							case "cache":
								if _, _, err := service.requestIssueQueryAccess(request); err != nil {
									release()
									t.Fatal(err)
								}
							case "authorization":
								// Obtain real native authorization inputs and ownership,
								// then verify those existing HTTP markers do not select
								// native authorization for a legacy suffix.
								request.URL.Path = "/api/issue-records/" + public.ID + "/permissions"
								data, err := service.issueRecordAuthorizationData(request)
								if err != nil {
									release()
									t.Fatal(err)
								}
								if len(data.Issues) != 0 {
									carriedIssues++
								}
								request = request.WithContext(context.WithValue(request.Context(), issueRecordAuthorizationContext{}, data))
								request.URL.Path = row.path
							case "scope", "general":
								if row.mode == "general" && source.BoundedMetadataEnabled() {
									read, err := source.BeginGeneralBootstrapRead(request.Context(), selectedWorkspace, user.ID, key)
									if err != nil {
										release()
										t.Fatal(err)
									}
									request = request.WithContext(read.Context(request.Context()))
									closeSource = read.Close
								} else {
									ctx := store.WithMetadataRecords(request.Context(), store.MetadataRecordScope{Fields: []string{"teams", "teamSettings", "workspaceSettings"}})
									ctx, closeRead, err := source.BeginWorkspaceRead(ctx, selectedWorkspace)
									if err != nil {
										release()
										t.Fatal(err)
									}
									request, closeSource = request.WithContext(ctx), closeRead
								}
							}
							visible := false
							if row.issueID != "" {
								witness := f.literalRequest(user, key, nil)
								witness.Header.Set("X-Workspace-Key", selectedWorkspace)
								data, access, err := service.requestIssueQueryAccess(witness)
								if err != nil {
									closeSource()
									release()
									t.Fatal(err)
								}
								query := store.IssueRecordQuery{Workspace: selectedWorkspace, Archived: "all"}
								query.Access, query.AllowedTeamIDs = service.issueRecordAccess(witness, data, access)
								ids, err := source.VisibleIssueRecordIDs(witness.Context(), query, []string{row.issueID})
								if err != nil {
									closeSource()
									release()
									t.Fatal(err)
								}
								visible = ids[row.issueID]
							}
							if source.BorrowedWorkspaceReadHasIssues(request.Context(), selectedWorkspace) {
								closeSource()
								release()
								t.Fatal("supported constructor unexpectedly supplied nonempty Issues")
							}
							statements := 0
							request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { statements++ }))
							allowed, handled := service.boundedEmptyLegacyIssueDenial(request, selectedWorkspace)
							if allowed || handled != (source.BoundedMetadataEnabled() && !row.native) || statements != 0 {
								closeSource()
								release()
								t.Fatal("helper source/flag/native boundary changed", allowed, handled, statements)
							}
							recorder := httptest.NewRecorder()
							allowed = service.authorizeWorkspaceRequest(recorder, request, user)
							if allowed {
								recorder.WriteHeader(http.StatusNoContent)
							}
							got := outcome{allowed, recorder.Code, recorder.Body.String()}
							expected := row.native && (row.realtime || visible)
							if allowed != expected || !allowed && recorder.Code != http.StatusForbidden {
								closeSource()
								release()
								t.Fatalf("exact authorization predicate changed: expected=%t got=%+v SQL-visible=%t", expected, got, visible)
							}
							if !row.native && recorder.Body.String() != "{\"error\":\"This resource is outside your teams\"}\n" {
								closeSource()
								release()
								t.Fatal("legacy route did not retain ordinary resource denial", got)
							}
							if !source.BoundedMetadataEnabled() {
								want, beforeSQL = got, statements
								if !row.native && visible {
									visibleDenials++
								}
							} else {
								pairs++
								legacySQL, boundedSQL = legacySQL+beforeSQL, boundedSQL+statements
								if statements > beforeSQL {
									increasedPairs++
								}
								if !reflect.DeepEqual(want, got) {
									closeSource()
									release()
									t.Fatalf("literal/bounded outcomes differ: literal=%+v bounded=%+v", want, got)
								}
								if allowed {
									nativeAllows++
								}
								if request.Context().Value(pagedMetadataHandoffKey{}) != nil {
									closeSource()
									release()
									t.Fatal("bounded request built a complete paged handoff")
								}
							}
							closeSource()
							release()
							release()
							if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
								t.Fatal("bounded request leaked work or decoded whole metadata")
							}
						}
					}
					if row.small {
						visit(t, small.data.Viewer, nil)
					} else {
						f.matrix(t, visit)
					}
				})
			}
			if visibleDenials == 0 || carriedIssues == 0 || nativeAllows == 0 {
				t.Fatal("table lacks a visible denied target, nonempty native marker, or positive native control", visibleDenials, carriedIssues, nativeAllows)
			}
			t.Logf("SW318 exact authorization pairs=%d SQL literal/bounded=%d/%d increased-pairs=%d visible-denials=%d native-allows=%d", pairs, legacySQL, boundedSQL, increasedPairs, visibleDenials, nativeAllows)
		})
	}
}

func TestScaleP21SW318LegacyIssueBoundary(t *testing.T) {
	type request struct{ method, path string }
	for _, row := range []request{
		{http.MethodGet, "/api/issues/id/permissions"}, {http.MethodHead, "/api/issues/id/permissions"},
		{http.MethodGet, "/api/issues/id/suggestions"}, {http.MethodHead, "/api/issues/id/suggestions"},
		{http.MethodHead, "/api/issues/id/comment-actions"},
	} {
		if !scaleP21SW318EmptyLegacyIssueRequest(httptest.NewRequest(row.method, row.path, nil)) {
			t.Fatal("exact five-shape boundary lost", row)
		}
	}
	for _, row := range []request{
		{http.MethodPost, "/api/issues/id/permissions"}, {http.MethodGet, "/api/issues/id/comment-actions"},
		{http.MethodGet, "/api/issue-records/id/permissions"}, {http.MethodHead, "/api/issue-records/id/suggestions"},
		{http.MethodGet, "/api/issues//permissions"}, {http.MethodGet, "/api/issues/id/permissions/"},
		{http.MethodGet, "/api/issues/id/permissions/child"}, {http.MethodGet, "/api/issues/id"},
		{http.MethodGet, "/api/realtime/events"}, {http.MethodGet, "/api/issue-suggestions/id"},
	} {
		if scaleP21SW318EmptyLegacyIssueRequest(httptest.NewRequest(row.method, row.path, nil)) {
			t.Fatal("helper captured another authorization contract", row)
		}
	}
	text := func(node ast.Node) string {
		var output bytes.Buffer
		if err := format.Node(&output, token.NewFileSet(), node); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}
	function := func(filename, name string) *ast.FuncDecl {
		parsed, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range parsed.Decls {
			if fn, ok := declaration.(*ast.FuncDecl); ok && fn.Name.Name == name {
				return fn
			}
		}
		t.Fatal("missing production function", filename, name)
		return nil
	}
	helper := function("scale_p21_sw318_legacy_issue_denial.go", "boundedEmptyLegacyIssueDenial")
	if len(helper.Body.List) != 3 ||
		text(helper.Body.List[0]) != "if !s.store.BoundedMetadataEnabled() || !scaleP21SW318EmptyLegacyIssueRequest(r) || isIssueRecordsRequest(r) {\n\treturn false, false\n}" ||
		text(helper.Body.List[1]) != "if s.store.BorrowedWorkspaceReadHasIssues(r.Context(), workspace) {\n\treturn false, false\n}" ||
		text(helper.Body.List[2]) != "return false, true" {
		t.Fatal("helper must decline every matching nonempty source and native request before exact empty-source denial")
	}
	resource := function("auth.go", "resourceAllowedLegacy")
	hooks, fallback := 0, false
	for _, statement := range resource.Body.List {
		if branch, ok := statement.(*ast.IfStmt); ok && branch.Init != nil &&
			text(branch.Init) == "allowed, handled := s.boundedEmptyLegacyIssueDenial(r, workspace)" {
			hooks++
			if text(branch.Cond) != "handled" || branch.Else != nil || len(branch.Body.List) != 1 || text(branch.Body.List[0]) != "return allowed" || fallback {
				t.Fatal("helper moved after projection or lost handled-only return")
			}
		}
		if declaration, ok := statement.(*ast.DeclStmt); ok && text(declaration) == "var data domain.Bootstrap" {
			fallback = true
		}
	}
	if hooks != 1 || !fallback {
		t.Fatal("helper not mounted exactly once before projection", hooks, fallback)
	}
	// Native aliases select the existing SQL authorization source. Realtime's
	// current selector is path-only; no header/context exception is inferred.
	if text(function("issue_records_api.go", "isIssueRecordsRequest").Body) != "{\n\tif pagedRealtimeRequest(r) {\n\t\treturn true\n\t}\n\treturn r.URL.Path == \"/api/issue-records\" || strings.HasPrefix(r.URL.Path, \"/api/issue-records/\")\n}" ||
		text(function("paged_realtime.go", "pagedRealtimeRequest").Body) != "{\n\treturn strings.HasPrefix(r.URL.Path, \"/api/realtime/\")\n}" {
		t.Fatal("native selector changed; audit legacy suffix overlap")
	}
	nativeFallback := 0
	ast.Inspect(resource.Body, func(node ast.Node) bool {
		if branch, ok := node.(*ast.IfStmt); ok && text(branch.Cond) == "isIssueRecordsRequest(r)" &&
			text(branch.Body) == "{\n\tdata, err = s.issueRecordAuthorizationData(r)\n\tok = err == nil\n}" {
			nativeFallback++
		}
		return true
	})
	if nativeFallback != 1 {
		t.Fatal("excluded native request lost its existing SQL fallback", nativeFallback)
	}
}
