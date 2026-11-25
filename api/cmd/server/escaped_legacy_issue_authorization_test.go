package main

import (
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

func TestScaleP21SW331EscapedLegacyIssueAuthorization(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_ACCESS", "on")
	t.Run("escaped-shape-and-decoded-precedence", func(t *testing.T) {
		for _, row := range []struct {
			method, path, tail, ref string
			parts                   int
		}{
			{http.MethodGet, "/api/issues/a%2Fb/permissions", "permissions", "a", 5},
			{http.MethodHead, "/%61pi/%69ssues/a%2Fb/%70ermissions", "permissions", "a", 5},
			{http.MethodGet, "/api/issues/a%252Fb/suggestions", "suggestions", "a%2Fb", 4},
			{http.MethodHead, "/api/issues/%2F/sug%67estions", "suggestions", "", 5},
			{http.MethodHead, "/api/issues/a%2Fcomment-actions/comment-actions", "comment-actions", "a", 5},
			{http.MethodGet, "/api/issues/a%2Fcomment-actions/permissions", "permissions", "a", 5},
		} {
			request := httptest.NewRequest(row.method, row.path, nil)
			parts, matched := scaleP21EscapedRouteParts(request, "/api/issues/{id}/"+row.tail)
			if !matched || len(parts) != row.parts || parts[2] != row.ref || !scaleP21SW318EmptyLegacyIssueRequest(request) {
				t.Fatal("escaped shape lost literal trimmed/decoded precedence", row, parts, matched)
			}
		}
		for _, row := range []struct{ method, path string }{
			{http.MethodGet, "/api/issues/a%2Fb/comment-actions"},
			{http.MethodPost, "/api/issues/a%2Fb/permissions"},
			{http.MethodPatch, "/api/issues/a%2Fb/suggestions"},
			{http.MethodGet, "/api/issue-records/a%2Fb/permissions"},
			{http.MethodHead, "/api/realtime/events"},
			{http.MethodGet, "/api/issues//permissions"},
			{http.MethodGet, "/api/issues/a%2Fb/permissions/"},
			{http.MethodGet, "/api/issues/a%2Fb/permissions/extra"},
			{http.MethodGet, "/api/issues%2Fa/b/permissions"},
			{http.MethodGet, "/api/issue-suggestions/a%2Fb"},
		} {
			if scaleP21SW318EmptyLegacyIssueRequest(httptest.NewRequest(row.method, row.path, nil)) {
				t.Fatal("escaped selector captured another source/method/route contract", row)
			}
		}
	})
	type outcome struct {
		Allowed       bool
		Status        int
		Body, Pattern string
	}
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			// Reuse SW318's small, real borrowed-source fixture. M2's unrelated
			// large metadata witnesses remain unchanged.
			small := scaleP03Fixture(t, repo)
			f := scaleP21M2Setup(t, repo)
			workspace := f.data.Workspace.URLKey
			public := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records",
				map[string]any{"title": "SW331 escaped issue", "teamId": f.public}, workspace, http.StatusCreated)
			data, found := repo.WorkspaceMetadata(workspace)
			if !found || len(data.Issues) != 0 {
				t.Fatal("ordinary source is no longer SW318's empty-Issues origin")
			}
			privateTeam := ""
			for _, project := range data.Projects {
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
				t.Fatal("existing explicit private guest grant missing")
			}
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw331_feature", "", nil, func(data *domain.Bootstrap) error {
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
			mux := http.NewServeMux()
			for _, prefix := range []string{"/api/issues/", "/api/issue-records/"} {
				for _, tail := range []string{"permissions", "suggestions", "comment-actions"} {
					mux.HandleFunc("GET "+prefix+"{id}/"+tail, func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(http.StatusNoContent)
					})
				}
			}
			rows := []struct {
				name, method, path, issueID, source   string
				native, editor, missingRoute, foreign bool
			}{
				{name: "decoded-public-prefix", method: http.MethodGet, path: "/api/issues/" + public.ID + "%2Fhidden/permissions", issueID: public.ID},
				{name: "encoded-fixed-tokens", method: http.MethodHead, path: "/%61pi/%69ssues/" + public.ID + "%2Fhidden/%70ermissions", issueID: public.ID},
				{name: "decoded-private-prefix", method: http.MethodGet, path: "/api/issues/" + private.ID + "%2Fvisible/suggestions", issueID: private.ID},
				{name: "leading-decoded-slash", method: http.MethodGet, path: "/api/issues/%2F" + public.ID + "/permissions"},
				{name: "slash-only-ref", method: http.MethodHead, path: "/api/issues/%2F/permissions"},
				{name: "double-escaped-missing", method: http.MethodGet, path: "/api/issues/%252F/suggestions"},
				{name: "encoded-suggestions-token", method: http.MethodHead, path: "/api/issues/" + public.ID + "/sug%67estions", issueID: public.ID},
				{name: "identifier-plus-slash", method: http.MethodGet, path: "/api/issues/" + url.PathEscape(public.Identifier) + "%2Fignored/permissions", issueID: public.ID},
				{name: "legacy-comment-actions-head", method: http.MethodHead, path: "/api/issues/" + public.ID + "%2Fignored/comment-actions", issueID: public.ID},
				{name: "foreign-workspace", method: http.MethodGet, path: "/api/issues/" + public.ID + "%2Fignored/permissions", foreign: true},
				{name: "native-escaped-alias", method: http.MethodGet, path: "/%61pi/%69ssue-records/" + public.ID + "%2Fignored/permissions", issueID: public.ID, native: true},
				{name: "rewrite-inside-reference", method: http.MethodGet, path: "/api/issues/" + public.ID + "%2Fcomment-actions/permissions", issueID: public.ID, native: true, editor: true, missingRoute: true},
				{name: "scoped-empty-source", method: http.MethodGet, path: "/api/issues/" + idgen.New("sw331-missing-") + "%2Ftail/permissions", source: "scope"},
				{name: "general-empty-source", method: http.MethodHead, path: "/api/issues/%2F/permissions", source: "general"},
			}
			pairs, visibleDenials, nativeAllows, rewrittenMissingAllows := 0, 0, 0, 0
			legacySQL, boundedSQL, maxLegacy, maxBounded, increasedPairs := 0, 0, 0, 0, 0
			for _, row := range rows {
				t.Run(row.name, func(t *testing.T) {
					visit := func(t *testing.T, user domain.User, key *domain.APIKey) {
						var want outcome
						beforeSQL := 0
						for _, source := range []*store.SQLiteStore{repo, f.probe} {
							func() {
								request := f.literalRequest(user, key, nil)
								request.Method = row.method
								var err error
								request.URL, err = url.Parse(row.path)
								if err != nil {
									t.Fatal(err)
								}
								selectedWorkspace := workspace
								if row.source != "" || row.foreign {
									selectedWorkspace = small.data.Workspace.URLKey
									request.Header.Set("X-Workspace-Key", selectedWorkspace)
								}
								request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokens[user.ID]})
								request, release := beginRequestResourceReads(request)
								defer release()
								service := &server{store: source}
								if row.source == "general" && source.BoundedMetadataEnabled() {
									read, err := source.BeginGeneralBootstrapRead(request.Context(), selectedWorkspace, user.ID, key)
									if err != nil {
										t.Fatal(err)
									}
									defer read.Close()
									request = request.WithContext(read.Context(request.Context()))
								} else if row.source != "" {
									ctx := store.WithMetadataRecords(request.Context(), store.MetadataRecordScope{Fields: []string{"teams", "teamSettings", "workspaceSettings"}})
									ctx, closeRead, err := source.BeginWorkspaceRead(ctx, selectedWorkspace)
									if err != nil {
										t.Fatal(err)
									}
									defer closeRead()
									request = request.WithContext(ctx)
								}
								visible, nativeAllowed := false, false
								if row.issueID != "" && !row.foreign {
									witness := f.literalRequest(user, key, nil)
									data, access, err := service.requestIssueQueryAccess(witness)
									if err != nil {
										t.Fatal(err)
									}
									query := store.IssueRecordQuery{Workspace: workspace, Archived: "all"}
									query.Access, query.AllowedTeamIDs = service.issueRecordAccess(witness, data, access)
									ids, err := source.VisibleIssueRecordIDs(witness.Context(), query, []string{row.issueID})
									if err != nil {
										t.Fatal(err)
									}
									visible, nativeAllowed = ids[row.issueID], ids[row.issueID]
									if row.editor && visible {
										witness = request.Clone(request.Context())
										location := *request.URL
										location.Path, location.RawPath = "/api/issue-records/"+row.issueID+"/comment-actions", ""
										witness.URL = &location
										headers, err := service.issueRecordAuthorizationData(witness)
										if err != nil {
											t.Fatal(err)
										}
										nativeAllowed = false
										for _, issue := range headers.Issues {
											if issue.ID == row.issueID {
												nativeAllowed = issuePermissionRank(issueRole(service, headers, issue)) >= issuePermissionRank("editor")
											}
										}
									}
								}
								if source.BorrowedWorkspaceReadHasIssues(request.Context(), selectedWorkspace) {
									t.Fatal("supported constructor changed SW318's source origin")
								}
								statements := 0
								request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { statements++ }))
								recorder := httptest.NewRecorder()
								actual := request
								allowed, pattern := false, ""
								handler := boundedLegacyIssueWrites(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
									actual = r
									native := isIssueRecordsRequest(r)
									if native != row.native {
										t.Fatal("real pre-auth rewrite/native source precedence changed", native, row.native)
									}
									startSQL := statements
									shortcut, handled := service.boundedEmptyLegacyIssueDenial(r, selectedWorkspace)
									expectedHandled := source.BoundedMetadataEnabled() && scaleP21SW318EmptyLegacyIssueRequest(r) && !native
									if shortcut || handled != expectedHandled || statements != startSQL {
										t.Fatal("pure escaped denial changed source/flag/native boundary", shortcut, handled, expectedHandled)
									}
									allowed = service.authorizeWorkspaceRequest(w, r, user)
									if allowed {
										_, pattern = mux.Handler(r)
										mux.ServeHTTP(w, r)
									}
								}))
								handler.ServeHTTP(recorder, request)
								got := outcome{allowed, recorder.Code, recorder.Body.String(), pattern}
								required := row.native && nativeAllowed
								expectedStatus := http.StatusForbidden
								if required {
									expectedStatus = http.StatusNoContent
									if row.missingRoute {
										expectedStatus = http.StatusNotFound
									}
								}
								if allowed != required || recorder.Code != expectedStatus || required && row.missingRoute && pattern != "" {
									t.Fatalf("required escaped/native result changed: required=%t status=%d got=%+v visible=%t", required, expectedStatus, got, visible)
								}
								if !row.native && !row.foreign && got.Body != "{\"error\":\"This resource is outside your teams\"}\n" {
									t.Fatal("ordinary empty-Issues path lost its exact resource refusal", got)
								}
								if !source.BoundedMetadataEnabled() {
									want, beforeSQL = got, statements
									if !row.native && visible {
										visibleDenials++
									}
								} else {
									pairs++
									legacySQL, boundedSQL = legacySQL+beforeSQL, boundedSQL+statements
									maxLegacy, maxBounded = max(maxLegacy, beforeSQL), max(maxBounded, statements)
									if statements > beforeSQL {
										increasedPairs++
									}
									if allowed {
										nativeAllows++
										if row.missingRoute {
											rewrittenMissingAllows++
										}
									}
									if !reflect.DeepEqual(want, got) {
										t.Fatalf("escaped legacy authorization differs: literal=%+v bounded=%+v", want, got)
									}
									if actual.Context().Value(pagedMetadataHandoffKey{}) != nil {
										t.Fatal("escaped denial built a complete paged handoff")
									}
									t.Logf("case=%s SQL literal/bounded=%d/%d increase=%d", row.name, beforeSQL, statements, statements-beforeSQL)
								}
							}()
							if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
								t.Fatal("escaped legacy issue authorization leaked work or fully decoded", source.MetadataWorkUsedForTesting(), source.WorkspaceMetadataDecodes())
							}
						}
					}
					if row.source != "" {
						visit(t, small.data.Viewer, nil)
					} else {
						f.matrix(t, visit)
					}
				})
			}
			if pairs != 242 || visibleDenials == 0 || nativeAllows == 0 || rewrittenMissingAllows == 0 {
				t.Fatal("escaped table lost M/source/positive-native controls", pairs, visibleDenials, nativeAllows, rewrittenMissingAllows)
			}
			t.Logf("SW331 pairs=%d SQL literal/bounded=%d/%d max=%d/%d increased-pairs=%d visible-denials=%d native-allows=%d", pairs, legacySQL, boundedSQL, maxLegacy, maxBounded, increasedPairs, visibleDenials, nativeAllows)
		})
	}
}
