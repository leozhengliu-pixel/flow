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
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW315TeamMembershipAuthorization(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_ACCESS", "off")
	t.Run("four-family-boundary-and-literal-resolver", func(t *testing.T) {
		for _, row := range []struct{ method, path string }{
			{http.MethodGet, "/api/teams/team/members"},
			{http.MethodHead, "/api/teams/team/members"},
			{http.MethodPut, "/api/teams/team/members/user"},
			{http.MethodDelete, "/api/teams/team/members/user"},
			{http.MethodPost, "/api/teams/team/members/sync"},
		} {
			if ref, selected := scaleP21SW315TeamMembershipAuthorizationRequest(httptest.NewRequest(row.method, row.path, nil)); !selected || ref != "team" {
				t.Fatal("registered membership family not selected", row)
			}
		}
		for _, row := range []struct{ method, path string }{
			{http.MethodPost, "/api/teams/team/members"}, {http.MethodPut, "/api/teams/team/members"},
			{http.MethodGet, "/api/teams/team/members/user"}, {http.MethodHead, "/api/teams/team/members/user"},
			{http.MethodPatch, "/api/teams/team/members/user"}, {http.MethodPost, "/api/teams/team/members/user"},
			{http.MethodGet, "/api/teams/team/members/"}, {http.MethodGet, "/api/teams//members"},
			{http.MethodPut, "/api/teams/team/members/"}, {http.MethodDelete, "/api/teams/team/members/user/extra"},
			{http.MethodPost, "/api/teams/team/members/sync/"}, {http.MethodGet, "/api/teams/team/resources"},
			{http.MethodGet, "/api/teams/team/settings"}, {http.MethodGet, "/api/workspaces/workspace/teams/team/members"},
		} {
			if _, selected := scaleP21SW315TeamMembershipAuthorizationRequest(httptest.NewRequest(row.method, row.path, nil)); selected {
				t.Fatal("membership hook captured another predicate", row)
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
		hooks, literalResolvers, literalRowChecks := 0, 0, 0
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Name.Name != "resourceAllowedLegacy" {
				continue
			}
			for _, statement := range function.Body.List {
				branch, ok := statement.(*ast.IfStmt)
				if !ok || branch.Init == nil || text(branch.Init) != "allowed, handled := s.boundedTeamMembershipAuthorization(r, workspace, userID)" {
					continue
				}
				hooks++
				if text(branch.Cond) != "handled" || branch.Else != nil || len(branch.Body.List) != 1 || text(branch.Body.List[0]) != "return allowed" {
					t.Fatal("membership hook lost explicit arguments or handled-only return")
				}
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				clause, ok := node.(*ast.CaseClause)
				if !ok || len(clause.List) != 1 || text(clause.List[0]) != `"teams"` {
					return true
				}
				for _, statement := range clause.Body {
					if text(statement) == "teamID := s.store.ResolveTeamID(workspace, parts[2])" {
						literalResolvers++
					}
					branch, ok := statement.(*ast.IfStmt)
					if ok && strings.HasPrefix(text(branch.Cond), "!teamRowAllowed(teamID) || ") {
						literalRowChecks++
					}
				}
				return true
			})
		}
		if hooks != 1 || literalResolvers != 1 || literalRowChecks != 1 {
			t.Fatal("mounted hook or literal team-row boundary changed", hooks, literalResolvers, literalRowChecks)
		}
	})

	type outcome struct {
		Allowed bool
		Status  int
		Body    string
		Route   string
	}
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			other := scaleP03Fixture(t, repo)
			f := scaleP21M2Setup(t, repo)
			workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw315-")
			data, found := repo.WorkspaceMetadata(workspace)
			if !found {
				t.Fatal("membership fixture missing")
			}
			private, child, publicKey := "", "", prefix+"PUBLIC"
			for _, team := range data.Teams {
				if strings.HasPrefix(team.ID, "m2-private-") {
					private = team.ID
				}
			}
			for id, settings := range data.TeamSettings {
				if settings.ParentTeamID == private {
					child = id
				}
			}
			if private == "" || child == "" || private == child || publicKey == f.public {
				t.Fatal("membership fixture lost private/inherited/ID-key distinctions")
			}
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw315_seed", "", nil, func(data *domain.Bootstrap) error {
				for index := range data.Teams {
					if data.Teams[index].ID == f.public {
						data.Teams[index].Key = publicKey
					}
				}
				data.Teams = append(data.Teams, domain.Team{ID: prefix + "visible-alias", Key: private})
				data.TeamSettings[prefix+"visible-alias"] = domain.TeamSettings{}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if repo.ResolveTeamID(workspace, publicKey) != f.public || repo.ResolveTeamID(workspace, private) != private {
				t.Fatal("literal resolver did not retain ID-before-key precedence")
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
			targetUser := f.principals[2].user.ID
			pairs, legacySQL, boundedSQL, successes := 0, 0, 0, 0
			families := map[string]int{}
			for _, row := range []struct {
				name, method, ref, tail string
				foreign, missing        bool
				direct                  string
			}{
				{name: "public-list", method: http.MethodGet, ref: f.public, tail: "/members"},
				{name: "public-head", method: http.MethodHead, ref: f.public, tail: "/members"},
				{name: "private-list", method: http.MethodGet, ref: private, tail: "/members"},
				{name: "private-put", method: http.MethodPut, ref: private, tail: "/members/" + targetUser},
				{name: "public-delete", method: http.MethodDelete, ref: f.public, tail: "/members/" + targetUser},
				{name: "private-sync", method: http.MethodPost, ref: private, tail: "/members/sync"},
				{name: "inherited-list", method: http.MethodGet, ref: child, tail: "/members"},
				{name: "existing-key-resolution", method: http.MethodGet, ref: publicKey, tail: "/members"},
				{name: "private-ID-before-visible-key", method: http.MethodGet, ref: private, tail: "/members"},
				{name: "missing", method: http.MethodGet, ref: prefix + "missing", tail: "/members", missing: true},
				{name: "case-changed-ID", method: http.MethodGet, ref: strings.ToUpper(f.public), tail: "/members", missing: true},
				{name: "foreign-workspace", method: http.MethodGet, ref: f.public, tail: "/members", foreign: true},
				{name: "explicit-workspace", method: http.MethodGet, ref: f.public, tail: "/members", direct: "workspace"},
				{name: "explicit-viewer", method: http.MethodGet, ref: f.public, tail: "/members", direct: "viewer"},
			} {
				t.Run(row.name, func(t *testing.T) {
					f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
						var want outcome
						oldSQL := 0
						for _, source := range []*store.SQLiteStore{repo, f.probe} {
							request := f.literalRequest(user, key, map[string]any{"role": "member", "members": []string{targetUser}})
							request.Method = row.method
							path := "/api/teams/" + row.ref + row.tail
							var err error
							request.URL, err = url.Parse(path)
							if err != nil {
								t.Fatal(err)
							}
							if row.foreign || row.direct == "workspace" {
								request.Header.Set("X-Workspace-Key", other.data.Workspace.URLKey)
							}
							if row.direct == "viewer" {
								request = request.WithContext(context.WithValue(request.Context(), authUserContextKey{}, other.data.Viewer))
							}
							request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokens[user.ID]})
							beforeWorkspace, beforeUser := workspaceKey(request), authUser(request).ID
							request, release := beginRequestResourceReads(request)
							service := &server{store: source}
							if !source.BoundedMetadataEnabled() {
								if _, handled := service.boundedTeamMembershipAuthorization(request, workspace, user.ID); handled {
									release()
									t.Fatal("membership hook changed literal flag-off")
								}
							}
							statements := 0
							request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { statements++ }))
							recorder := httptest.NewRecorder()
							got := outcome{}
							mux := http.NewServeMux()
							for _, pattern := range []string{
								"GET /api/teams/{id}/members", "PUT /api/teams/{id}/members/{userId}",
								"DELETE /api/teams/{id}/members/{userId}", "POST /api/teams/{id}/members/sync",
							} {
								mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
									got.Route = r.Pattern
									if r.PathValue("id") != row.ref {
										t.Fatal("actual route lost its raw team reference")
									}
									w.WriteHeader(http.StatusNoContent)
								})
							}
							if row.direct != "" {
								got.Allowed = service.resourceAllowedLegacy(request, workspace, user.ID)
								if !got.Allowed {
									writeError(recorder, http.StatusForbidden, "This resource is outside your teams")
								}
							} else {
								got.Allowed = service.authorizeWorkspaceRequest(recorder, request, user)
							}
							if got.Allowed {
								mux.ServeHTTP(recorder, request)
							}
							got.Status, got.Body = recorder.Code, recorder.Body.String()
							selectedEmpty := key != nil && apiKeyTeamRestrictionSelected(*key) && len(key.TeamIDs) == 0
							selectedOther := key != nil && apiKeyTeamRestrictionSelected(*key) && !slices.Contains(key.TeamIDs, repo.ResolveTeamID(workspace, row.ref))
							if (row.foreign || row.missing || selectedEmpty || selectedOther) && got.Allowed {
								release()
								t.Fatalf("missing/foreign/selected membership reference widened: %+v", got)
							}
							unrestricted := key == nil || !apiKeyTeamRestrictionSelected(*key)
							if row.name == "private-list" && user.ID == f.principals[0].user.ID && unrestricted && !got.Allowed {
								release()
								t.Fatal("admin outside private team lost directory membership access")
							}
							if row.name == "private-ID-before-visible-key" && user.ID == f.principals[2].user.ID && got.Allowed {
								release()
								t.Fatal("inaccessible exact ID fell through to a visible key alias")
							}
							if got.Allowed && (got.Status != http.StatusNoContent || got.Route == "") {
								release()
								t.Fatal("successful authorization never reached an actual membership route", got)
							}
							if row.direct != "" && (workspaceKey(request) != beforeWorkspace || authUser(request).ID != beforeUser) {
								release()
								t.Fatal("explicit wrapper arguments changed the containing request")
							}
							if !source.BoundedMetadataEnabled() {
								want, oldSQL = got, statements
								if got.Allowed {
									successes++
									families[row.method]++
								}
							} else {
								pairs++
								legacySQL, boundedSQL = legacySQL+oldSQL, boundedSQL+statements
								if !reflect.DeepEqual(want, got) {
									release()
									t.Fatalf("membership authorization differs: literal=%+v bounded=%+v", want, got)
								}
								if request.Context().Value(pagedMetadataHandoffKey{}) != nil || request.Context().Value(teamResourcesReadKey{}) != nil {
									release()
									t.Fatal("boolean membership hook advertised complete or content output")
								}
							}
							release()
							release()
							if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
								t.Fatal("membership point authorization retained work or hydrated metadata")
							}
						}
					})
				})
			}
			if successes == 0 || families[http.MethodGet] == 0 || families[http.MethodHead] == 0 ||
				families[http.MethodPut] == 0 || families[http.MethodDelete] == 0 || families[http.MethodPost] == 0 {
				t.Fatal("membership table did not exercise every real route family successfully", families)
			}
			cancelled, cancel := context.WithCancel(t.Context())
			cancel()
			request := httptest.NewRequest(http.MethodGet, "/api/teams/"+f.public+"/members", nil).WithContext(cancelled)
			request, release := beginRequestResourceReads(request)
			if allowed, handled := (&server{store: f.probe}).boundedTeamMembershipAuthorization(request, workspace, f.data.Viewer.ID); allowed || !handled {
				t.Fatal("cancelled membership authorization succeeded or escaped its exact boundary")
			}
			release()
			release()
			if f.probe.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("cancelled membership authorization leaked work")
			}
			t.Logf("membership authorization pairs=%d SQL legacy/bounded=%d/%d", pairs, legacySQL, boundedSQL)
		})
	}
}
