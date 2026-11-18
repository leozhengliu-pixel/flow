package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// SW491 compares the actual authorization middleware (and the bare
// resourceAllowed decision) for every bounded fallback decision class,
// literal (flag off) against bounded warm and bounded SQL-reloaded sources:
// status, body, headers and the selected mux leaf must be byte-identical.
func TestScaleP21SW491FallbackAuthorizationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	type outcome struct {
		Allowed  bool
		Resource bool
		Status   int
		Body     string
		Header   http.Header
		Pattern  string
	}
	f := scaleP21SW10Setup(t)
	repo := f.repository
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw491-")
	foreign := scaleP03Fixture(t, repo)
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, foreign.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	privateTeam, childTeam := "", ""
	data, found := repo.WorkspaceMetadata(workspace)
	if !found {
		t.Fatal("M workspace missing")
	}
	for _, project := range data.Projects {
		if project.ID == "m2-unrelated" && len(project.TeamIDs) == 1 {
			privateTeam = project.TeamIDs[0]
		}
	}
	for id, settings := range data.TeamSettings {
		if settings.ParentTeamID == privateTeam && privateTeam != "" {
			childTeam = id
		}
	}
	if privateTeam == "" || childTeam == "" {
		t.Fatal("M private/child team missing")
	}
	publicKey := prefix + "PUB"
	ids := map[string]string{
		"pub-project": prefix + "pub-project", "child-project": prefix + "child-project", "unscoped-project": prefix + "unscoped-project",
		"archived-project": prefix + "archived-project", "pub-initiative": prefix + "pub-initiative", "private-initiative": prefix + "private-initiative",
		"unscoped-initiative": prefix + "unscoped-initiative", "retired-team": prefix + "retired-team",
	}
	archived := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw491_seed", "", nil, func(data *domain.Bootstrap) error {
		for index := range data.Teams {
			if data.Teams[index].ID == f.public {
				data.Teams[index].Key = publicKey
			}
		}
		data.Teams = append(data.Teams, domain.Team{ID: ids["retired-team"], Key: ids["retired-team"], RetiredAt: &archived})
		data.Projects = append(data.Projects,
			domain.Project{ID: ids["pub-project"], SlugID: prefix + "pub-current", PreviousSlugIDs: []string{prefix + "pub-old"}, TeamIDs: []string{f.public}},
			domain.Project{ID: ids["child-project"], TeamIDs: []string{childTeam}},
			domain.Project{ID: ids["unscoped-project"], TeamIDs: []string{}},
			domain.Project{ID: ids["archived-project"], TeamIDs: []string{f.public}, ArchivedAt: &archived},
		)
		data.Initiatives = append(data.Initiatives,
			domain.Initiative{ID: ids["pub-initiative"], SlugID: prefix + "init-current", PreviousSlugIDs: []string{prefix + "init-old"}, ProjectIDs: []string{ids["pub-project"]}, ContributingTeamIDs: []string{f.public}},
			domain.Initiative{ID: ids["private-initiative"], LeadTeamID: privateTeam},
			domain.Initiative{ID: ids["unscoped-initiative"], ProjectIDs: []string{}, ContributingTeamIDs: []string{}},
		)
		data.ProjectRelations = append(data.ProjectRelations,
			domain.ProjectRelation{ID: prefix + "prel-private", ProjectID: ids["pub-project"], RelatedProjectID: "m2-unrelated", Type: "blocks"},
			domain.ProjectRelation{ID: prefix + "prel-public", ProjectID: ids["pub-project"], RelatedProjectID: ids["unscoped-project"], Type: "blocks"},
		)
		data.InitiativeRelations = append(data.InitiativeRelations,
			domain.InitiativeRelation{ID: prefix + "irel-private", InitiativeID: ids["pub-initiative"], RelatedInitiativeID: ids["private-initiative"], Type: "blocks"},
			domain.InitiativeRelation{ID: prefix + "irel-public", InitiativeID: ids["pub-initiative"], RelatedInitiativeID: ids["unscoped-initiative"], Type: "blocks"},
		)
		data.Reviews = append(data.Reviews,
			domain.CodeReview{ID: prefix + "review-open", SlugID: prefix + "review-open-slug", Provider: "github", Title: "open"},
			domain.CodeReview{ID: prefix + "review-hidden", SlugID: prefix + "review-hidden-slug", Provider: "github", Title: "hidden", IssueIDs: []string{prefix + "missing-issue"}},
		)
		data.Trash = append(data.Trash, domain.TrashEntry{ID: prefix + "trash", ResourceType: "project", ResourceID: prefix + "trashed-project", Title: "trashed", Payload: json.RawMessage(`{}`), DeletedAt: archived, ExpiresAt: archived.AddDate(1, 0, 0)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.reader.key = workspace
	type principal struct {
		name string
		user domain.User
	}
	principals := []principal{}
	for _, item := range f.principals {
		principals = append(principals, principal{item.name, item.user})
	}
	_, suspended := scaleP03Join(t, f.scaleP03PlatformFixture, "sw491-suspended", idgen.New("sw491-user-")+"@example.test", "member", f.public)
	principals = append(principals, principal{"suspended", suspended}, principal{"outsider", foreign.data.Viewer})
	tokens := map[string]string{}
	for _, item := range principals {
		_, token, err := repo.Login(t.Context(), item.user.Email, "p03-fixture-password")
		if err != nil {
			t.Fatal(item.name, err)
		}
		tokens[item.user.ID] = token
	}
	if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, suspended.ID); err != nil {
		t.Fatal(err)
	}
	type row struct {
		name, method, pattern, path, body string
	}
	esc := url.PathEscape
	rows := []row{
		// Membership: the literal default and collection roots.
		{"default-settings-read", http.MethodGet, "GET /api/webhooks", "/api/webhooks", ""},
		{"default-admin-write", http.MethodDelete, "DELETE /api/workspaces/{workspaceKey}/members/{userId}", "/api/workspaces/" + workspace + "/members/" + prefix + "member", ""},
		{"workspace-root-write", http.MethodPatch, "PATCH /api/workspaces/{workspaceKey}", "/api/workspaces/" + workspace, `{"name":"x"}`},
		{"teams-root", http.MethodGet, "GET /api/teams", "/api/teams", ""},
		{"cycles-root", http.MethodGet, "GET /api/cycles", "/api/cycles", ""},
		{"releases-root", http.MethodGet, "GET /api/releases", "/api/releases", ""},
		{"reviews-root", http.MethodGet, "GET /api/reviews", "/api/reviews", ""},
		{"dashboards-root", http.MethodGet, "GET /api/dashboards", "/api/dashboards", ""},
		{"favorites-short", http.MethodGet, "GET /api/favorites", "/api/favorites", ""},
		{"escaped-default", http.MethodGet, "GET /api/webhooks", "/%61pi/%77ebhooks", ""},
		// Non-guest roots.
		{"customers-root", http.MethodGet, "GET /api/customers", "/api/customers", ""},
		{"customers-root-put", http.MethodPut, "PUT /api/customers", "/api/customers", `{}`},
		{"customer-requests-root", http.MethodGet, "GET /api/customer-requests", "/api/customer-requests", ""},
		// Project and initiative points (method-independent point predicate).
		{"project-delete-public", http.MethodDelete, "DELETE /api/projects/{id}", "/api/projects/" + ids["pub-project"], ""},
		{"project-delete-current-slug", http.MethodDelete, "DELETE /api/projects/{id}", "/api/projects/" + prefix + "pub-current", ""},
		{"project-delete-old-slug", http.MethodDelete, "DELETE /api/projects/{id}", "/api/projects/" + prefix + "pub-old", ""},
		{"project-delete-private", http.MethodDelete, "DELETE /api/projects/{id}", "/api/projects/m2-unrelated", ""},
		{"project-delete-child", http.MethodDelete, "DELETE /api/projects/{id}", "/api/projects/" + ids["child-project"], ""},
		{"project-delete-unscoped", http.MethodDelete, "DELETE /api/projects/{id}", "/api/projects/" + ids["unscoped-project"], ""},
		{"project-delete-archived", http.MethodDelete, "DELETE /api/projects/{id}", "/api/projects/" + ids["archived-project"], ""},
		{"project-delete-trashed", http.MethodDelete, "DELETE /api/projects/{id}", "/api/projects/" + prefix + "trashed-project", ""},
		{"project-delete-missing", http.MethodDelete, "DELETE /api/projects/{id}", "/api/projects/" + prefix + "missing", ""},
		{"project-comment-public", http.MethodPost, "POST /api/projects/{id}/comments", "/api/projects/" + ids["pub-project"] + "/comments", `{"body":"x"}`},
		{"project-comment-private", http.MethodPost, "POST /api/projects/{id}/comments", "/api/projects/m2-unrelated/comments", `{"body":"x"}`},
		{"project-subscription", http.MethodPut, "PUT /api/projects/{id}/comments/{commentId}/subscription", "/api/projects/" + ids["pub-project"] + "/comments/c/subscription", ""},
		{"project-milestone-delete", http.MethodDelete, "DELETE /api/projects/{id}/milestones/{milestoneId}", "/api/projects/" + ids["unscoped-project"] + "/milestones/m", ""},
		{"project-escaped-id", http.MethodDelete, "DELETE /api/projects/{id}", "/api/projects/" + esc(ids["pub-project"]+"/x"), ""},
		{"project-escaped-prefix", http.MethodPost, "POST /api/projects/{id}/comments", "/%61pi/projects/" + ids["pub-project"] + "/%63omments", `{}`},
		{"project-relation-patch-private", http.MethodPatch, "PATCH /api/projects/{id}/relations/{relationId}", "/api/projects/" + ids["pub-project"] + "/relations/" + prefix + "prel-private", `{"type":"x"}`},
		{"project-relation-delete-public", http.MethodDelete, "DELETE /api/projects/{id}/relations/{relationId}", "/api/projects/" + ids["pub-project"] + "/relations/" + prefix + "prel-public", ""},
		{"project-relation-slug-mismatch", http.MethodDelete, "DELETE /api/projects/{id}/relations/{relationId}", "/api/projects/" + prefix + "pub-current/relations/" + prefix + "prel-public", ""},
		{"project-relation-missing", http.MethodDelete, "DELETE /api/projects/{id}/relations/{relationId}", "/api/projects/" + ids["pub-project"] + "/relations/" + prefix + "missing", ""},
		{"initiative-delete-public", http.MethodDelete, "DELETE /api/initiatives/{id}", "/api/initiatives/" + ids["pub-initiative"], ""},
		{"initiative-delete-old-slug", http.MethodDelete, "DELETE /api/initiatives/{id}", "/api/initiatives/" + prefix + "init-old", ""},
		{"initiative-delete-private", http.MethodDelete, "DELETE /api/initiatives/{id}", "/api/initiatives/" + ids["private-initiative"], ""},
		{"initiative-update-unscoped", http.MethodPost, "POST /api/initiatives/{id}/updates", "/api/initiatives/" + ids["unscoped-initiative"] + "/updates", `{}`},
		{"initiative-update-missing", http.MethodPost, "POST /api/initiatives/{id}/updates", "/api/initiatives/" + prefix + "missing/updates", `{}`},
		{"initiative-relation-private", http.MethodDelete, "DELETE /api/initiatives/{id}/relations/{relationId}", "/api/initiatives/" + ids["pub-initiative"] + "/relations/" + prefix + "irel-private", ""},
		{"initiative-relation-public", http.MethodPatch, "PATCH /api/initiatives/{id}/relations/{relationId}", "/api/initiatives/" + ids["pub-initiative"] + "/relations/" + prefix + "irel-public", `{}`},
		// Team rows and team content.
		{"team-settings-public", http.MethodGet, "GET /api/teams/{id}/settings", "/api/teams/" + f.public + "/settings", ""},
		{"team-settings-key", http.MethodGet, "GET /api/teams/{id}/settings", "/api/teams/" + publicKey + "/settings", ""},
		{"team-settings-key-lower", http.MethodGet, "GET /api/teams/{id}/settings", "/api/teams/" + strings.ToLower(publicKey) + "/settings", ""},
		{"team-settings-private", http.MethodGet, "GET /api/teams/{id}/settings", "/api/teams/" + privateTeam + "/settings", ""},
		{"team-state-child", http.MethodPatch, "PATCH /api/teams/{id}/states/{stateId}", "/api/teams/" + childTeam + "/states/s", `{}`},
		{"team-template-retired", http.MethodDelete, "DELETE /api/teams/{id}/templates/{templateId}", "/api/teams/" + ids["retired-team"] + "/templates/t", ""},
		{"team-detail-missing", http.MethodGet, "GET /api/teams/{id}", "/api/teams/" + prefix + "missing", ""},
		{"team-escaped", http.MethodGet, "GET /api/teams/{id}/settings", "/api/%74eams/" + esc(f.public) + "/%73ettings", ""},
		{"team-resource-post-public", http.MethodPost, "POST /api/teams/{id}/resources", "/api/teams/" + f.public + "/resources", `{}`},
		{"team-resource-post-private", http.MethodPost, "POST /api/teams/{id}/resources", "/api/teams/" + privateTeam + "/resources", `{}`},
		{"team-section-delete-child", http.MethodDelete, "DELETE /api/teams/{id}/resource-sections/{sectionId}", "/api/teams/" + childTeam + "/resource-sections/s", ""},
		{"workspace-team-public", http.MethodPatch, "PATCH /api/workspaces/{workspaceKey}/teams/{teamId}", "/api/workspaces/" + workspace + "/teams/" + f.public, `{}`},
		{"workspace-team-private", http.MethodDelete, "DELETE /api/workspaces/{workspaceKey}/teams/{teamId}", "/api/workspaces/" + workspace + "/teams/" + privateTeam, ""},
		{"workspace-team-member", http.MethodPut, "PUT /api/workspaces/{workspaceKey}/teams/{teamId}/members/{userId}", "/api/workspaces/" + workspace + "/teams/" + strings.ToLower(publicKey) + "/members/u", `{}`},
		{"workspace-team-missing", http.MethodPatch, "PATCH /api/workspaces/{workspaceKey}/teams/{teamId}", "/api/workspaces/" + workspace + "/teams/" + prefix + "missing", `{}`},
		{"workspace-foreign-path", http.MethodPatch, "PATCH /api/workspaces/{workspaceKey}/teams/{teamId}", "/api/workspaces/" + foreign.data.Workspace.URLKey + "/teams/" + f.public, `{}`},
		// Embedded review favorite.
		{"review-favorite-open", http.MethodPatch, "PATCH /api/reviews/{id}", "/api/reviews/" + prefix + "review-open", `{"favorite":true}`},
		{"review-favorite-slug", http.MethodPatch, "PATCH /api/reviews/{id}", "/api/reviews/" + prefix + "review-open-slug", `{"favorite":false}`},
		{"review-favorite-hidden", http.MethodPatch, "PATCH /api/reviews/{id}", "/api/reviews/" + prefix + "review-hidden", `{"favorite":true}`},
		{"review-favorite-missing", http.MethodPatch, "PATCH /api/reviews/{id}", "/api/reviews/" + prefix + "missing", `{"favorite":true}`},
	}
	mux := http.NewServeMux()
	registered := map[string]bool{}
	for _, item := range rows {
		if registered[item.pattern] {
			continue
		}
		registered[item.pattern] = true
		pattern := item.pattern
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			values := map[string]string{}
			for _, name := range []string{"id", "relationId", "commentId", "milestoneId", "workspaceKey", "teamId", "userId", "stateId", "templateId", "sectionId"} {
				if value := r.PathValue(name); value != "" {
					values[name] = value
				}
			}
			w.Header().Set("X-SW491-Leaf", pattern)
			writeJSON(w, http.StatusOK, values)
		})
	}
	pairs, successes, refusals, legacySQL, boundedSQL := 0, 0, 0, 0, 0
	allowedBy := map[string]bool{}
	for index, item := range rows {
		t.Run(item.name, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_ACCESS", []string{"off", "shadow", "on"}[index%3])
			for _, phase := range []string{"warm", "reloaded"} {
				t.Run(phase, func(t *testing.T) {
					for _, who := range principals {
						for _, credential := range []struct {
							name string
							key  *domain.APIKey
						}{
							{"session", nil},
							{"unrestricted", &domain.APIKey{ID: "m2-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "all"}},
							{"selected-empty", &domain.APIKey{ID: "m2-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{}}},
							{"selected-set", &domain.APIKey{ID: "m2-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
						} {
							t.Run(who.name+"/"+credential.name, func(t *testing.T) {
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
									build := func() (*http.Request, func(), *projectionHold) {
										request := f.literalRequest(who.user, credential.key, nil)
										parsed, err := url.Parse(item.path)
										if err != nil {
											t.Fatal(err)
										}
										request.URL, request.Method = parsed, item.method
										request.Body = http.NoBody
										if item.body != "" {
											request.Body = io.NopCloser(strings.NewReader(item.body))
											request.ContentLength = int64(len(item.body))
										}
										request.SetPathValue("workspaceKey", "")
										request.SetPathValue("workspace", "")
										request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokens[who.user.ID]})
										hold := &projectionHold{gate: newMetadataReadGate(64, 64, time.Minute, 0)}
										request = request.WithContext(context.WithValue(request.Context(), projectionHoldKey{}, hold))
										request, release := beginRequestResourceReads(request)
										return request, release, hold
									}
									service := &server{store: source}
									statements := 0
									// The bare decision with the wrapper's explicit arguments.
									bare, releaseBare, bareHold := build()
									resource := service.resourceAllowed(bare, workspace, who.user.ID)
									releaseBare()
									if bareHold.held {
										<-bareHold.gate.slots
										if source.BoundedMetadataEnabled() {
											t.Fatal("bounded decision took a whole-workspace projection slot")
										}
									}
									request, release, hold := build()
									if !source.BoundedMetadataEnabled() {
										if _, handled := service.boundedFallbackAuthorization(request, workspace, who.user.ID); handled {
											release()
											t.Fatal("fallback adapter changed the flag-off source")
										}
									}
									request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { statements++ }))
									recorder := httptest.NewRecorder()
									allowed := service.authorizeWorkspaceRequest(recorder, request, who.user)
									pattern := ""
									if allowed {
										_, pattern = mux.Handler(request)
										mux.ServeHTTP(recorder, request)
									}
									if hold.held {
										<-hold.gate.slots
										if source.BoundedMetadataEnabled() {
											release()
											t.Fatal("bounded middleware took a whole-workspace projection slot")
										}
									}
									got := outcome{allowed, resource, recorder.Code, recorder.Body.String(), recorder.Header().Clone(), pattern}
									if allowed && (recorder.Code != http.StatusOK || pattern != item.pattern) || !allowed && recorder.Code != http.StatusForbidden && recorder.Code != http.StatusNotFound {
										release()
										t.Fatalf("unexpected middleware result %+v", got)
									}
									if !source.BoundedMetadataEnabled() {
										want, beforeSQL = got, statements
									} else {
										pairs++
										legacySQL, boundedSQL = legacySQL+beforeSQL, boundedSQL+statements
										if !reflect.DeepEqual(want, got) {
											release()
											t.Fatalf("literal/bounded authorization differs:\nliteral=%+v\nbounded=%+v", want, got)
										}
										if allowed {
											successes++
											allowedBy[item.name+"/"+who.name+"/"+credential.name] = true
										} else {
											refusals++
										}
										if request.Context().Value(pagedMetadataHandoffKey{}) != nil {
											release()
											t.Fatal("bounded authorization created a complete paged handoff")
										}
									}
									release()
									if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
										t.Fatal("bounded authorization leaked work or decoded complete metadata")
									}
								}
							})
						}
					}
				})
			}
		})
	}
	// Independent policy controls, so equality cannot hold vacuously.
	for _, required := range []struct {
		key     string
		allowed bool
	}{
		{"default-settings-read/admin/session", true},
		{"default-settings-read/member-private-outsider/session", false},
		{"teams-root/member-private-outsider/session", true},
		{"teams-root/guest-explicit/session", true},
		{"default-settings-read/suspended/session", false},
		{"default-settings-read/outsider/session", false},
		{"customers-root/guest-explicit/session", false},
		{"customers-root/member-private-outsider/session", true},
		{"project-delete-public/member-private-outsider/session", true},
		{"project-delete-private/member-private-outsider/session", false},
		{"project-delete-private/sub-team-inheritance/session", true},
		{"project-delete-public/member-private-outsider/selected-empty", false},
		{"project-delete-old-slug/member-private-outsider/session", true},
		{"project-delete-missing/admin/session", false},
		{"project-delete-trashed/admin/session", false},
		{"project-relation-delete-public/member-private-outsider/session", true},
		{"project-relation-patch-private/member-private-outsider/session", false},
		{"initiative-update-unscoped/guest-explicit/session", false},
		{"team-settings-private/admin-private-outsider/session", true},
		{"team-resource-post-private/admin-private-outsider/session", false},
		{"team-resource-post-private/admin/session", true},
		{"workspace-team-private/admin-private-outsider/selected-set", false},
		{"workspace-team-public/admin-private-outsider/selected-set", true},
		{"review-favorite-open/member-private-outsider/session", true},
		{"review-favorite-hidden/member-private-outsider/session", false},
	} {
		if allowedBy[required.key] != required.allowed {
			t.Error("independent policy control failed", required.key, required.allowed)
		}
	}
	if want := len(rows) * 2 * len(principals) * 4; pairs != want || successes == 0 || refusals == 0 {
		t.Fatal("SW491 table lost cells", pairs, want, successes, refusals)
	}
	t.Logf("SW491 fallback policy pairs=%d SQL literal/bounded=%d/%d success/refusal=%d/%d", pairs, legacySQL, boundedSQL, successes, refusals)
}
