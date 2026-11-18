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

func TestScaleP21SW327EscapedResourceAuthorization(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Run("shared-shape-and-literal-ref", func(t *testing.T) {
		for _, row := range []struct {
			path, pattern, ref string
			count              int
		}{
			{"/api/projects/a%2Fb/updates", "/api/projects/{id}/updates", "a", 5},
			{"/%61pi/%70rojects/a%2Fb/%75pdates", "/api/projects/{id}/updates", "a", 5},
			{"/api/projects/a%252Fb", "/api/projects/{id}", "a%2Fb", 3},
			{"/api/projects/%2F", "/api/projects/{id}", "", 2},
			{"/api/projects/%2Fa/updates", "/api/projects/{id}/updates", "", 5},
		} {
			request := httptest.NewRequest(http.MethodGet, row.path, nil)
			parts, matched := scaleP21EscapedRouteParts(request, row.pattern)
			ref := ""
			if len(parts) >= 3 {
				ref = parts[2]
			}
			if !matched || ref != row.ref || len(parts) != row.count {
				t.Fatal("shape helper changed literal decoded parts", row, parts, matched)
			}
		}
		for _, row := range []struct{ path, pattern string }{
			{"/api/projects/a/updates", "/api/projects/{id...}"},
			{"/api/projects/a", "/api/projects/{$}"},
			{"/api/projects//updates", "/api/projects/{id}/updates"},
			{"/api/projects/a/updates/", "/api/projects/{id}/updates"},
			{"/api/projects%2Fa/b/updates", "/api/projects/{id}/updates"},
			{"/api/projects/a/updates/diff-preview/child", "/api/projects/{id}/updates/diff-preview"},
		} {
			if _, matched := scaleP21EscapedRouteParts(httptest.NewRequest(http.MethodGet, row.path, nil), row.pattern); matched {
				t.Fatal("shape helper accepted another escaped boundary", row)
			}
		}
		for _, path := range []string{"/api/projects/a%2Fb", "/api/initiatives/a%2Frelations%2Fx/updates/diff-preview", "/api/projects/a%2Fb/history", "/api/initiatives/a%2Fb/history"} {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				_, ref, selected := scaleP21SW255ResourceAuthorizationRequest(httptest.NewRequest(method, path, nil))
				if !selected || ref != "a" {
					t.Fatal("point classifier did not preserve decoded first ref", method, path, ref, selected)
				}
			}
			for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete} {
				if _, _, selected := scaleP21SW255ResourceAuthorizationRequest(httptest.NewRequest(method, path, nil)); selected {
					t.Fatal("escaped point classifier captured a write", method, path)
				}
			}
		}
		for _, path := range []string{"/api/initiatives/a/relations/x", "/api/teams/a%2Fb/members", "/api/issue-labels/a%2Fb", "/api/issues/a%2Fb/permissions"} {
			if _, _, selected := scaleP21SW255ResourceAuthorizationRequest(httptest.NewRequest(http.MethodGet, path, nil)); selected {
				t.Fatal("SW327 expanded another consumer/P16 route", path)
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
			foreign := scaleP03Fixture(t, repo)
			f := scaleP21M2Setup(t, repo)
			workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw327-")
			p, q, hidden := prefix+"public", prefix+"other-public", prefix+"private"
			i, j, privateInitiative := prefix+"initiative", prefix+"other-initiative", prefix+"private-initiative"
			oldProject, oldInitiative := prefix+"old-project", prefix+"old-initiative"
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
			pr, ph, reversed := prefix+"relation", prefix+"hidden-relation", prefix+"reverse-relation"
			ir, ih, ireverse := prefix+"initiative-relation", prefix+"hidden-initiative-relation", prefix+"reverse-initiative-relation"
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw327_seed", "", nil, func(data *domain.Bootstrap) error {
				data.Projects = append(data.Projects,
					domain.Project{ID: p, PreviousSlugIDs: []string{oldProject}, TeamIDs: []string{f.public}},
					domain.Project{ID: q, TeamIDs: []string{f.public}},
					domain.Project{ID: hidden, TeamIDs: []string{privateTeam}},
					// Full wildcard visibility deliberately opposes the decoded prefix.
					domain.Project{ID: p + "/hidden", TeamIDs: []string{privateTeam}},
					domain.Project{ID: hidden + "/visible", TeamIDs: []string{f.public}},
					domain.Project{ID: "/", TeamIDs: []string{privateTeam}},
				)
				data.Initiatives = append(data.Initiatives,
					domain.Initiative{ID: i, PreviousSlugIDs: []string{oldInitiative}, ContributingTeamIDs: []string{f.public}, ProjectIDs: []string{p}},
					domain.Initiative{ID: j, ContributingTeamIDs: []string{f.public}, ProjectIDs: []string{q}},
					domain.Initiative{ID: privateInitiative, LeadTeamID: privateTeam, ProjectIDs: []string{hidden}},
					domain.Initiative{ID: i + "/hidden", LeadTeamID: privateTeam, ProjectIDs: []string{hidden}},
					domain.Initiative{ID: privateInitiative + "/visible", ContributingTeamIDs: []string{f.public}, ProjectIDs: []string{p}},
					domain.Initiative{ID: "/", LeadTeamID: privateTeam, ProjectIDs: []string{hidden}},
				)
				data.ProjectRelations = append(data.ProjectRelations,
					domain.ProjectRelation{ID: pr, ProjectID: p, RelatedProjectID: q, Type: "related"},
					domain.ProjectRelation{ID: ph, ProjectID: p, RelatedProjectID: hidden, Type: "related"},
					domain.ProjectRelation{ID: reversed, ProjectID: q, RelatedProjectID: p, Type: "related"},
				)
				data.InitiativeRelations = append(data.InitiativeRelations,
					domain.InitiativeRelation{ID: ir, InitiativeID: i, RelatedInitiativeID: j, Type: "related"},
					domain.InitiativeRelation{ID: ih, InitiativeID: i, RelatedInitiativeID: privateInitiative, Type: "related"},
					domain.InitiativeRelation{ID: ireverse, InitiativeID: j, RelatedInitiativeID: i, Type: "related"},
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
			for _, kind := range []string{"projects", "initiatives"} {
				for _, tail := range []string{"", "/relations", "/updates", "/updates/diff-preview"} {
					mux.HandleFunc("GET /api/"+kind+"/{id}"+tail, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
				}
			}
			pairs, successes, refusals, legacySQL, boundedSQL := 0, 0, 0, 0, 0
			for index, row := range []struct {
				name, kind, ref, tail, method, decision string
				foreign, reload, encoded                bool
			}{
				{"project-first-ref", "projects", p + "/hidden", "", http.MethodGet, "public", false, false, false},
				{"project-private-prefix", "projects", hidden + "/visible", "/updates/diff-preview", http.MethodHead, "private", false, false, false},
				{"project-old-prefix", "projects", oldProject + "/missing", "/updates", http.MethodGet, "public", false, false, true},
				{"project-relation-list-shape", "projects", p + "/hidden", "/relations", http.MethodHead, "public", false, false, false},
				{"project-relation-witness", "projects", p + "/relations/" + pr, "/updates/diff-preview", http.MethodGet, "public", false, false, false},
				{"project-related-private", "projects", p + "/relations/" + ph, "", http.MethodHead, "private", false, true, false},
				{"project-reversed-relation", "projects", p + "/relations/" + reversed, "/updates", http.MethodGet, "deny", false, false, false},
				{"project-raw-alias-relation", "projects", oldProject + "/relations/" + pr, "", http.MethodHead, "deny", false, false, false},
				{"project-missing-prefix", "projects", prefix + "missing/visible", "", http.MethodGet, "deny", false, false, false},
				{"project-trimmed-root", "projects", "/", "", http.MethodHead, "membership", false, false, false},
				{"project-empty-first-ref", "projects", "/visible", "/updates/diff-preview", http.MethodGet, "deny", false, false, false},
				{"initiative-first-ref", "initiatives", i + "/hidden", "", http.MethodHead, "public", false, false, false},
				{"initiative-private-prefix", "initiatives", privateInitiative + "/visible", "/updates", http.MethodGet, "private", false, false, false},
				{"initiative-old-prefix", "initiatives", oldInitiative + "/missing", "/updates/diff-preview", http.MethodHead, "public", false, false, false},
				{"initiative-relation-witness", "initiatives", i + "/relations/" + ir, "", http.MethodGet, "public", false, true, false},
				{"initiative-related-private", "initiatives", i + "/relations/" + ih, "/updates/diff-preview", http.MethodHead, "private", false, false, false},
				{"initiative-reversed-relation", "initiatives", i + "/relations/" + ireverse, "", http.MethodGet, "deny", false, false, false},
				{"initiative-missing-prefix", "initiatives", prefix + "missing/visible", "/relations", http.MethodHead, "deny", false, false, false},
				{"initiative-trimmed-root", "initiatives", "/", "", http.MethodGet, "membership", false, false, false},
				{"foreign-project", "projects", p + "/hidden", "/updates/diff-preview", http.MethodHead, "deny", true, false, false},
			} {
				t.Run(row.name, func(t *testing.T) {
					t.Setenv("FLOW_RECORD_ACCESS", []string{"off", "shadow", "on"}[index%3])
					f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
						var want outcome
						beforeSQL := 0
						for _, source := range []*store.SQLiteStore{repo, f.probe} {
							if source.BoundedMetadataEnabled() {
								if err := source.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
									t.Fatal(err)
								}
								if row.reload {
									if err := source.ReloadWorkspace(t.Context(), workspace); err != nil {
										t.Fatal(err)
									}
								}
							}
							request := f.literalRequest(user, key, nil)
							path := "/api/" + row.kind + "/" + url.PathEscape(row.ref) + row.tail
							if row.encoded {
								path = strings.Replace(path, "/api/projects/", "/%61pi/%70rojects/", 1)
							}
							var err error
							request.URL, err = url.Parse(path)
							if err != nil {
								t.Fatal(err)
							}
							request.Method = row.method
							if row.foreign {
								request.Header.Set("X-Workspace-Key", foreign.data.Workspace.URLKey)
							}
							request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokens[user.ID]})
							request, release := beginRequestResourceReads(request)
							service := &server{store: source}
							if !source.BoundedMetadataEnabled() {
								if _, handled := service.boundedPointResourceAuthorization(request, workspace, user.ID); handled {
									release()
									t.Fatal("escaped adapter changed flag-off behavior")
								}
							}
							statements := 0
							request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { statements++ }))
							recorder := httptest.NewRecorder()
							allowed := service.authorizeWorkspaceRequest(recorder, request, user)
							pattern := ""
							if allowed {
								_, pattern = mux.Handler(request)
								mux.ServeHTTP(recorder, request)
							}
							got := outcome{allowed, recorder.Code, recorder.Body.String(), pattern}
							guest := user.ID == f.principals[3].user.ID
							selected := key != nil && apiKeyTeamRestrictionSelected(*key)
							required := false
							switch row.decision {
							case "membership":
								required = true
							case "public":
								required = !selected || len(key.TeamIDs) != 0
							case "private":
								required = (user.ID == f.principals[1].user.ID || user.ID == f.principals[4].user.ID) && !selected
							}
							if row.foreign || row.kind == "initiatives" && guest {
								required = false
							}
							expectedPattern := "GET /api/" + row.kind + "/{id}" + row.tail
							leafMatches := recorder.Code == http.StatusNoContent && pattern == expectedPattern
							if row.name == "project-trimmed-root" || row.name == "initiative-trimmed-root" {
								// GoMux rejects "/" as a single wildcard after unescaping.
								expectedPattern = ""
								leafMatches = recorder.Code == http.StatusNotFound && recorder.Body.String() == "404 page not found\n" && pattern == ""
							}
							if allowed != required || allowed && !leafMatches || !allowed && recorder.Code != http.StatusForbidden {
								release()
								t.Fatalf("required=%t got=%+v expectedPattern=%s", required, got, expectedPattern)
							}
							if !source.BoundedMetadataEnabled() {
								want, beforeSQL = got, statements
							} else {
								pairs++
								legacySQL, boundedSQL = legacySQL+beforeSQL, boundedSQL+statements
								if !reflect.DeepEqual(want, got) {
									release()
									t.Fatalf("escaped policy differs: literal=%+v bounded=%+v", want, got)
								}
								if allowed {
									successes++
								} else {
									refusals++
								}
								if request.Context().Value(pagedMetadataHandoffKey{}) != nil {
									release()
									t.Fatal("escaped auth created a full paged handoff")
								}
							}
							release()
							release()
							if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
								t.Fatal("escaped auth leaked work or decoded complete metadata")
							}
						}
					})
				})
			}
			if pairs != 400 || successes == 0 || refusals == 0 {
				t.Fatal("escaped matrix lost required controls", pairs, successes, refusals)
			}
			t.Logf("SW327 escaped policy pairs=%d SQL literal/bounded=%d/%d success/refusal=%d/%d", pairs, legacySQL, boundedSQL, successes, refusals)
		})
	}
}
