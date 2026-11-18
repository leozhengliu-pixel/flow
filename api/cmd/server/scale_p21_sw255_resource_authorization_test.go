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

func TestScaleP21SW255PointResourceAuthorization(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Run("exact-source-resource-boundary", func(t *testing.T) {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			for _, kind := range []string{"projects", "initiatives"} {
				for _, tail := range []string{"", "/relations", "/updates", "/updates/diff-preview", "/history"} {
					request := httptest.NewRequest(method, "/api/"+kind+"/old-slug"+tail, nil)
					gotKind, ref, handled := scaleP21SW255ResourceAuthorizationRequest(request)
					if !handled || gotKind != kind || ref != "old-slug" {
						t.Fatal("source detail/subview not selected", method, kind, tail)
					}
				}
			}
		}
		for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete, http.MethodOptions} {
			for _, path := range []string{"/api/projects/x", "/api/initiatives/x/relations", "/api/projects/x/updates"} {
				if _, _, handled := scaleP21SW255ResourceAuthorizationRequest(httptest.NewRequest(method, path, nil)); handled {
					t.Fatal("point authorization captured another method", method, path)
				}
			}
		}
		for _, path := range []string{
			"/api/projects", "/api/initiatives", "/api/projects/", "/api/projects/x/",
			"/api/projects/x/relations/hidden", "/api/initiatives/x/relations/hidden",
			"/api/initiatives/x/updates/not-diff-preview",
			"/api/documents/x", "/api/projects//updates", "/api//projects/x",
		} {
			if _, _, handled := scaleP21SW255ResourceAuthorizationRequest(httptest.NewRequest(http.MethodGet, path, nil)); handled {
				t.Fatal("point authorization bypassed a different route predicate", path)
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
					text(branch.Init) != "allowed, handled := s.boundedPointResourceAuthorization(r, workspace, userID)" {
					continue
				}
				hooks++
				if text(branch.Cond) != "handled" || branch.Else != nil || len(branch.Body.List) != 1 ||
					text(branch.Body.List[0]) != "return allowed" {
					t.Fatal("point authorization lost its explicit binding or handled-only return")
				}
			}
		}
		if hooks != 1 {
			t.Fatal("point authorization hook missing or duplicated", hooks)
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
			workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw255-")
			metadata, found := repo.WorkspaceMetadata(workspace)
			if !found {
				t.Fatal("fixture workspace missing")
			}
			private, child := "", ""
			for _, item := range metadata.Projects {
				if item.ID == "m2-unrelated" && len(item.TeamIDs) == 1 {
					private = item.TeamIDs[0]
				}
			}
			for team, settings := range metadata.TeamSettings {
				if settings.ParentTeamID == private {
					child = team
				}
			}
			if private == "" || child == "" {
				t.Fatal("existing M private/inherited team fixture missing")
			}
			project, initiative := prefix+"project", prefix+"initiative"
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw255_seed", "", nil, func(data *domain.Bootstrap) error {
				data.Projects = append(data.Projects,
					domain.Project{ID: project, SlugID: prefix + "project-current", PreviousSlugIDs: []string{prefix + "project-old", prefix + "project-collision"}, TeamIDs: []string{f.public}, DependencyIDs: []string{"m2-unrelated"}},
					domain.Project{ID: prefix + "private-exact", SlugID: prefix + "project-collision", TeamIDs: []string{private}},
					domain.Project{ID: prefix + "inherited", TeamIDs: []string{child}},
					domain.Project{ID: prefix + "unscoped", TeamIDs: []string{}},
				)
				data.Initiatives = append(data.Initiatives,
					domain.Initiative{ID: initiative, SlugID: prefix + "initiative-current", PreviousSlugIDs: []string{prefix + "initiative-old", prefix + "initiative-collision"}, ProjectIDs: []string{project}, ContributingTeamIDs: []string{f.public}},
					domain.Initiative{ID: prefix + "private-initiative", SlugID: prefix + "initiative-collision", LeadTeamID: private, ProjectIDs: []string{"m2-unrelated"}},
					domain.Initiative{ID: prefix + "unscoped-initiative", ProjectIDs: []string{}, ContributingTeamIDs: []string{}},
				)
				data.ProjectRelations = append(data.ProjectRelations,
					domain.ProjectRelation{ID: prefix + "visible-relation", ProjectID: project, RelatedProjectID: prefix + "unscoped", Type: "related"},
					domain.ProjectRelation{ID: prefix + "hidden-relation", ProjectID: project, RelatedProjectID: "m2-unrelated", Type: "related"},
				)
				data.InitiativeRelations = append(data.InitiativeRelations,
					domain.InitiativeRelation{ID: prefix + "hidden-relation", InitiativeID: initiative, RelatedInitiativeID: prefix + "private-initiative", Type: "related"},
				)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			metadata, found = repo.WorkspaceMetadata(workspace)
			if !found || !slices.ContainsFunc(metadata.ProjectRelations, func(item domain.ProjectRelation) bool {
				return item.ID == prefix+"hidden-relation" && item.ProjectID == project && item.RelatedProjectID == "m2-unrelated"
			}) || !slices.ContainsFunc(metadata.InitiativeRelations, func(item domain.InitiativeRelation) bool {
				return item.ID == prefix+"hidden-relation" && item.InitiativeID == initiative && item.RelatedInitiativeID == prefix+"private-initiative"
			}) {
				t.Fatal("hidden relation rows absent from the literal source")
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
			for _, row := range []struct {
				name, kind, ref, tail, mode string
				foreign, missing            bool
				public, unscoped, collision bool
			}{
				{name: "project-detail", kind: "projects", ref: project, mode: "off", public: true},
				{name: "project-old-slug", kind: "projects", ref: prefix + "project-old", mode: "shadow", public: true},
				{name: "project-exact-before-old", kind: "projects", ref: prefix + "project-collision", mode: "off", collision: true},
				{name: "initiative-old-slug", kind: "initiatives", ref: prefix + "initiative-old", mode: "off", public: true},
				{name: "initiative-exact-before-old", kind: "initiatives", ref: prefix + "initiative-collision", mode: "shadow", collision: true},
				{name: "project-hidden-relations", kind: "projects", ref: project, tail: "/relations", mode: "on", public: true},
				{name: "initiative-hidden-relations", kind: "initiatives", ref: initiative, tail: "/relations", mode: "off", public: true},
				{name: "project-old-updates", kind: "projects", ref: prefix + "project-old", tail: "/updates", mode: "shadow", public: true},
				{name: "initiative-old-updates", kind: "initiatives", ref: prefix + "initiative-old", tail: "/updates", mode: "on", public: true},
				{name: "private-project", kind: "projects", ref: "m2-unrelated", mode: "on"},
				{name: "private-initiative", kind: "initiatives", ref: prefix + "private-initiative", tail: "/updates", mode: "off"},
				{name: "inherited-project", kind: "projects", ref: prefix + "inherited", tail: "/relations", mode: "shadow"},
				{name: "unscoped-initiative", kind: "initiatives", ref: prefix + "unscoped-initiative", mode: "shadow", unscoped: true},
				{name: "unscoped-project", kind: "projects", ref: prefix + "unscoped", mode: "off", unscoped: true},
				{name: "missing-project", kind: "projects", ref: prefix + "missing", tail: "/updates", mode: "off", missing: true},
				{name: "missing-initiative", kind: "initiatives", ref: prefix + "missing", tail: "/relations", mode: "shadow", missing: true},
				{name: "foreign-project", kind: "projects", ref: project, mode: "on", foreign: true},
				{name: "foreign-initiative", kind: "initiatives", ref: initiative, tail: "/updates", mode: "off", foreign: true},
			} {
				t.Run(row.name, func(t *testing.T) {
					t.Setenv("FLOW_RECORD_ACCESS", row.mode)
					for _, method := range []string{http.MethodGet, http.MethodHead} {
						t.Run(method, func(t *testing.T) {
							f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
								guest := user.ID == f.principals[3].user.ID
								selectedEmpty := key != nil && apiKeyTeamRestrictionSelected(*key) && len(key.TeamIDs) == 0
								var want outcome
								legacyStatements := 0
								for _, source := range []*store.SQLiteStore{repo, f.probe} {
									request := f.literalRequest(user, key, nil)
									request.Method = method
									var err error
									request.URL, err = url.Parse("/api/" + row.kind + "/" + row.ref + row.tail)
									if err != nil {
										t.Fatal(err)
									}
									request.SetPathValue("id", row.ref)
									if row.foreign {
										request.Header.Set("X-Workspace-Key", other.data.Workspace.URLKey)
									}
									request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokens[user.ID]})
									request, release := beginRequestResourceReads(request)
									service := &server{store: source}
									if !source.BoundedMetadataEnabled() {
										if _, handled := service.boundedPointResourceAuthorization(request, workspace, user.ID); handled {
											release()
											t.Fatal("point helper changed the literal OFF path")
										}
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
									required := -1
									if row.foreign || row.missing || row.kind == "initiatives" && guest {
										required = 0
									} else if row.public {
										required = 1
										if selectedEmpty {
											required = 0
										}
									} else if row.unscoped && row.kind == "projects" {
										required = 1
									} else if row.collision && user.ID == f.principals[1].user.ID &&
										key != nil && apiKeyTeamRestrictionSelected(*key) && slices.Contains(key.TeamIDs, f.public) {
										required = 0
									} else if row.collision && user.ID == f.principals[2].user.ID && key == nil {
										required = 1
									}
									if required >= 0 && allowed != (required == 1) || !allowed && recorder.Code != http.StatusForbidden {
										release()
										t.Fatalf("required point authorization=%d got=%+v", required, got)
									}
									if !source.BoundedMetadataEnabled() {
										want = got
										legacyStatements = statements
									} else {
										recordSQL(row.mode+" "+method+" "+row.name, legacyStatements, statements)
										if !reflect.DeepEqual(want, got) {
											release()
											t.Fatalf("point/subview authorization differs: literal=%+v bounded=%+v", want, got)
										}
										if request.Context().Value(pagedMetadataHandoffKey{}) != nil {
											release()
											t.Fatal("point authorization created a complete paged handoff")
										}
									}
									release()
									release()
									if source.BoundedMetadataEnabled() &&
										(source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
										t.Fatal("point authorization leaked work or hydrated complete metadata")
									}
								}
							})
						})
					}
				})
			}
		})
	}
}
