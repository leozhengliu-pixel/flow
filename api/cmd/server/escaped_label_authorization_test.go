package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW329EscapedLabelAuthorization(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Run("finite-escaped-label-boundary", func(t *testing.T) {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			for _, path := range []string{
				"/api/issue-labels/id", "/%61pi/%69ssue-labels/%69d",
				"/api/issue-labels/id%2Fchildren", "/api/issue-labels/id%2fchildren",
				"/api/issue-labels/%2Fid", "/api/issue-labels/id%2F",
				"/api/issue-labels/%2F", "/api/issue-labels/id%252Fchildren",
			} {
				if !scaleP21SW256IssueLabelPointRequest(httptest.NewRequest(method, path, nil)) {
					t.Fatal("registered escaped label shape not selected", method, path)
				}
			}
		}
		for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete, http.MethodOptions} {
			for _, path := range []string{"/api/issue-labels/id", "/api/issue-labels/id%2Fchildren"} {
				if scaleP21SW256IssueLabelPointRequest(httptest.NewRequest(method, path, nil)) {
					t.Fatal("label selector captured another method", method, path)
				}
			}
		}
		for _, path := range []string{
			"/api/issue-labels", "/api/issue-labels/", "/api/issue-labels//",
			"/api/issue-labels/id/", "/api/issue-labels/id/children",
			"/api/issue-labels%2Fextra/id", "/api%2Fextra/issue-labels/id",
			"/api/issues/id", "/api/projects/id", "/api/issue-label-groups/id",
		} {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				if scaleP21SW256IssueLabelPointRequest(httptest.NewRequest(method, path, nil)) {
					t.Fatal("label selector captured another route", method, path)
				}
			}
		}
	})
	type outcome struct {
		Allowed bool
		Status  int
		Body    string
		Pattern string
		Ref     string
	}
	f := scaleP21SW10Setup(t)
	repo := f.repository
	t.Run(f.reader.config.Driver, func(t *testing.T) {
		foreign := scaleP03Fixture(t, repo)
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, foreign.data.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw329-")
		label, slash := prefix+"label", prefix+"label/children"
		privateTeam := ""
		data, found := repo.WorkspaceMetadata(workspace)
		if !found {
			t.Fatal("M workspace missing")
		}
		for _, project := range data.Projects {
			if project.ID == "m2-unrelated" && len(project.TeamIDs) == 1 {
				privateTeam = project.TeamIDs[0]
			}
		}
		if privateTeam == "" {
			t.Fatal("M private team missing")
		}
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw329_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Labels = append(data.Labels,
				domain.IssueLabel{ID: label, Name: "SW329 workspace label", Scope: "workspace"},
				// Scope opposes the decoded prefix, but neither is an auth predicate.
				domain.IssueLabel{ID: slash, Name: "SW329 private slash label", Scope: privateTeam},
				domain.IssueLabel{ID: "/" + label, Name: "SW329 leading slash", Scope: privateTeam},
				domain.IssueLabel{ID: label + "/", Name: "SW329 trailing slash", Scope: "workspace"},
				domain.IssueLabel{ID: "/", Name: "SW329 slash root", Scope: privateTeam},
				domain.IssueLabel{ID: label + "%2Fchildren", Name: "SW329 literal percent", Scope: "workspace"},
			)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		f.reader.key = workspace
		tokens := map[string]string{}
		for _, principal := range f.principals {
			_, token, err := repo.Login(t.Context(), principal.user.Email, "p03-fixture-password")
			if err != nil {
				t.Fatal(err)
			}
			tokens[principal.user.ID] = token
		}
		leafRef := ""
		mux := http.NewServeMux()
		// The existing directory-reader proof owns lookup and visibility. This
		// leaf verifies policy routing retains the entire exact label ID.
		mux.HandleFunc("GET /api/issue-labels/{id}", func(w http.ResponseWriter, r *http.Request) {
			leafRef = r.PathValue("id")
			w.WriteHeader(http.StatusNoContent)
		})
		pairs, successes, refusals, legacySQL, boundedSQL := 0, 0, 0, 0, 0
		for index, row := range []struct {
			name, ref string
			encoded   bool
			foreign   bool
		}{
			{"present-exact", label, false, false},
			{"present-url-alias", label, true, false},
			{"private-full-slash-id", slash, false, false},
			{"private-slash-url-alias", slash, true, false},
			{"leading-slash", "/" + label, false, false},
			{"trailing-slash", label + "/", false, false},
			{"trimmed-root", "/", false, false},
			{"literal-percent-no-second-unescape", label + "%2Fchildren", false, false},
			{"missing-exact", prefix + "missing", false, false},
			{"missing-slash", prefix + "missing/children", false, false},
			{"foreign-workspace", slash, true, true},
		} {
			t.Run(row.name, func(t *testing.T) {
				t.Setenv("FLOW_RECORD_ACCESS", []string{"off", "shadow", "on"}[index%3])
				for _, method := range []string{http.MethodGet, http.MethodHead} {
					t.Run(method, func(t *testing.T) {
						for _, phase := range []string{"warm", "reloaded"} {
							t.Run(phase, func(t *testing.T) {
								f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
									var want outcome
									beforeSQL := 0
									for _, source := range []*store.SQLiteStore{repo, f.probe} {
										if source.BoundedMetadataEnabled() {
											if err := source.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
												t.Fatal(err)
											}
										}
										if phase == "reloaded" {
											scaleP21SW201ReloadSource(t, f.reader, source)
										}
										request := f.literalRequest(user, key, nil)
										path := "/api/issue-labels/" + url.PathEscape(row.ref)
										if row.encoded {
											path = "/%61pi/%69ssue-labels/" + strings.ReplaceAll(url.PathEscape(row.ref), "s", "%73")
											path = strings.ReplaceAll(path, "%2F", "%2f")
										}
										var err error
										request.URL, err = url.Parse(path)
										if err != nil {
											t.Fatal(err)
										}
										request.Method = method
										if row.foreign {
											request.Header.Set("X-Workspace-Key", foreign.data.Workspace.URLKey)
										}
										request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokens[user.ID]})
										request, release := beginRequestResourceReads(request)
										service := &server{store: source}
										if !source.BoundedMetadataEnabled() {
											if _, handled := service.boundedIssueLabelPointAuthorization(request, workspace, user.ID); handled {
												release()
												t.Fatal("label adapter changed flag-off behavior")
											}
										}
										statements := 0
										request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { statements++ }))
										recorder := httptest.NewRecorder()
										leafRef = ""
										allowed := service.authorizeWorkspaceRequest(recorder, request, user)
										pattern := ""
										if allowed {
											_, pattern = mux.Handler(request)
											mux.ServeHTTP(recorder, request)
										}
										got := outcome{allowed, recorder.Code, recorder.Body.String(), pattern, leafRef}
										required := !row.foreign
										leafMatches := recorder.Code == http.StatusNoContent && recorder.Body.Len() == 0 && pattern == "GET /api/issue-labels/{id}" && leafRef == row.ref
										if row.name == "trimmed-root" {
											// GoMux rejects "/" as a single wildcard after unescaping.
											leafMatches = recorder.Code == http.StatusNotFound && recorder.Body.String() == "404 page not found\n" && pattern == "" && leafRef == ""
										}
										if allowed != required || allowed && !leafMatches || !allowed && recorder.Code != http.StatusForbidden {
											release()
											t.Fatalf("membership requires=%t full ID=%q got=%+v", required, row.ref, got)
										}
										if !source.BoundedMetadataEnabled() {
											want, beforeSQL = got, statements
										} else {
											pairs++
											legacySQL, boundedSQL = legacySQL+beforeSQL, boundedSQL+statements
											if !reflect.DeepEqual(want, got) {
												release()
												t.Fatalf("escaped label policy differs: literal=%+v bounded=%+v", want, got)
											}
											if allowed {
												successes++
												if request.Context().Value(issueRequestAccessKey{}) == nil {
													release()
													t.Fatal("label auth did not reuse existing query access")
												}
											} else {
												refusals++
											}
											if request.Context().Value(pagedMetadataHandoffKey{}) != nil {
												release()
												t.Fatal("escaped label auth created a complete paged handoff")
											}
										}
										release()
										release()
										if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
											t.Fatal("escaped label auth leaked work or decoded complete metadata")
										}
									}
								})
							})
						}
					})
				}
			})
		}
		if pairs != 880 || successes != 800 || refusals != 80 {
			t.Fatal("escaped label M table lost required controls", pairs, successes, refusals)
		}
		t.Logf("SW329 escaped label policy pairs=%d SQL literal/bounded=%d/%d success/refusal=%d/%d", pairs, legacySQL, boundedSQL, successes, refusals)
	})
}
