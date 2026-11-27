package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"flow/api/internal/domain"
)

// Saved views and dashboards by ID, their create roots and their
// owner/body checks. Views follow the literal projection (team scope, own
// personal views, exact-ID visible project) before viewAllowed; dashboards
// follow the store's settings filter before dashboardVisible.
func viewDashboardAuthorizationTable(t *testing.T, part, parts int) {
	t.Helper()
	fx := harnessSetup(t, func(fx *harnessFixture, data *domain.Bootstrap) {
		p := fx.prefix
		member, admin := fx.f.principals[2].user.ID, fx.f.principals[1].user.ID
		view := func(id, slug, scope, team, owner, project string) domain.SavedView {
			return domain.SavedView{ID: id, SlugID: slug, Name: id, Resource: "issues", Scope: scope, TeamID: team, OwnerID: owner, ProjectID: project, View: "list", Filters: json.RawMessage(`{}`), Display: json.RawMessage(`{}`), Insights: json.RawMessage(`{}`)}
		}
		data.SavedViews = append(data.SavedViews,
			view(p+"view-workspace", p+"view-workspace-slug", "workspace", "", admin, ""),
			view(p+"view-team-public", "", "team", fx.public, admin, ""),
			view(p+"view-team-private", "", "team", fx.privateTeam, admin, ""),
			view(p+"view-team-child", "", "team", fx.childTeam, admin, ""),
			view(p+"view-team-retired", "", "team", fx.ids["retired-team"], admin, ""),
			view(p+"view-team-empty", "", "team", "", admin, ""),
			view(p+"view-personal-member", "", "personal", "", member, ""),
			view(p+"view-personal-admin", "", "personal", "", admin, ""),
			view(p+"view-project-public", "", "workspace", "", member, fx.ids["pub-project"]),
			view(p+"view-project-private", "", "workspace", "", member, "m2-unrelated"),
			view(p+"view-project-slug", "", "workspace", "", member, p+"pub-current"),
			view(p+"view-project-missing", "", "workspace", "", member, p+"missing-project"),
			view(p+"view-dup", "", "personal", "", admin, ""),
			view(p+"view-dup", "", "workspace", "", admin, ""),
			view(p+"view-slug-clash", p+"view-workspace", "team", fx.privateTeam, admin, ""),
		)
		dashboard := func(id, owner, visibility string, teams ...string) map[string]any {
			if teams == nil {
				teams = []string{}
			}
			return map[string]any{"id": id, "name": id, "ownerId": owner, "visibility": visibility, "teamIds": teams, "hideFilters": false, "widgets": []any{}, "subscriberIds": []any{}}
		}
		data.Settings[dashboardsSettingsKey] = []any{
			dashboard(p+"dash-workspace", admin, "workspace"),
			dashboard(p+"dash-private-member", member, "private"),
			dashboard(p+"dash-private-admin", admin, "private"),
			dashboard(p+"dash-team-public", admin, "team", fx.public),
			dashboard(p+"dash-team-private", admin, "team", fx.privateTeam),
			dashboard(p+"dash-team-mixed", member, "team", fx.privateTeam, fx.public),
			dashboard(p+"dash-dup", admin, "private"),
			dashboard(p+"dash-dup", member, "workspace"),
		}
	})
	p, esc := fx.prefix, url.PathEscape
	member := fx.f.principals[2].user.ID
	rows := []harnessRow{}
	for _, ref := range []string{"view-workspace", "view-workspace-slug", "view-team-public", "view-team-private", "view-team-child", "view-team-retired", "view-team-empty",
		"view-personal-member", "view-personal-admin", "view-project-public", "view-project-private", "view-project-slug", "view-project-missing", "view-dup", "view-missing"} {
		rows = append(rows,
			harnessRow{"delete-" + ref, http.MethodDelete, "DELETE /api/views/{id}", "/api/views/" + esc(p+ref), ""},
			harnessRow{"share-" + ref, http.MethodPost, "POST /api/views/{id}/share", "/api/views/" + esc(p+ref) + "/share", ""},
		)
	}
	rows = append(rows,
		harnessRow{"unshare-team-public", http.MethodDelete, "DELETE /api/views/{id}/share", "/api/views/" + p + "view-team-public/share", ""},
		harnessRow{"patch-plain", http.MethodPatch, "PATCH /api/views/{id}", "/api/views/" + p + "view-workspace", `{"name":"x"}`},
		harnessRow{"patch-owner", http.MethodPatch, "PATCH /api/views/{id}", "/api/views/" + p + "view-project-public", `{"ownerId":"` + p + `other"}`},
		harnessRow{"patch-team-private", http.MethodPatch, "PATCH /api/views/{id}", "/api/views/" + p + "view-personal-member", `{"teamId":"` + fx.privateTeam + `"}`},
		harnessRow{"patch-project-private", http.MethodPatch, "PATCH /api/views/{id}", "/api/views/" + p + "view-personal-member", `{"projectId":"m2-unrelated"}`},
		harnessRow{"patch-project-old-slug", http.MethodPatch, "PATCH /api/views/{id}", "/api/views/" + p + "view-personal-member", `{"projectId":"` + p + `pub-old"}`},
		harnessRow{"patch-invalid-json", http.MethodPatch, "PATCH /api/views/{id}", "/api/views/" + p + "view-personal-member", `{`},
		harnessRow{"slug-clash", http.MethodDelete, "DELETE /api/views/{id}", "/api/views/" + p + "view-workspace", ""},
		harnessRow{"escaped-prefix", http.MethodDelete, "DELETE /api/views/{id}", "/%61pi/%76iews/" + p + "view-team-public", ""},
		harnessRow{"create-plain", http.MethodPost, "POST /api/views", "/api/views", `{"name":"x"}`},
		harnessRow{"create-team-private", http.MethodPost, "POST /api/views", "/api/views", `{"teamId":"` + fx.privateTeam + `"}`},
		harnessRow{"create-team-key", http.MethodPost, "POST /api/views", "/api/views", `{"teamId":"` + fx.publicKey + `"}`},
		harnessRow{"create-project-slug", http.MethodPost, "POST /api/views", "/api/views", `{"projectId":"` + p + `pub-current"}`},
		harnessRow{"create-project-private", http.MethodPost, "POST /api/views", "/api/views", `{"projectId":"m2-unrelated"}`},
		harnessRow{"create-invalid", http.MethodPost, "POST /api/views", "/api/views", `[`},
	)
	for _, ref := range []string{"dash-workspace", "dash-private-member", "dash-private-admin", "dash-team-public", "dash-team-private", "dash-team-mixed", "dash-dup", "dash-missing"} {
		rows = append(rows,
			harnessRow{"results-" + ref, http.MethodGet, "GET /api/dashboards/{id}/results", "/api/dashboards/" + p + ref + "/results", ""},
			harnessRow{"delete-" + ref, http.MethodDelete, "DELETE /api/dashboards/{id}", "/api/dashboards/" + p + ref, ""},
		)
	}
	rows = append(rows,
		harnessRow{"dash-export", http.MethodGet, "GET /api/dashboards/{id}/export", "/api/dashboards/" + p + "dash-team-private/export", ""},
		harnessRow{"dash-preview", http.MethodPost, "POST /api/dashboards/{id}/preview", "/api/dashboards/" + p + "dash-team-mixed/preview", `{}`},
		harnessRow{"dash-share", http.MethodPost, "POST /api/dashboards/{id}/share", "/api/dashboards/" + p + "dash-workspace/share", ""},
		harnessRow{"dash-unshare", http.MethodDelete, "DELETE /api/dashboards/{id}/share", "/api/dashboards/" + p + "dash-private-member/share", ""},
		harnessRow{"dash-subscribe", http.MethodPut, "PUT /api/dashboards/{id}/subscription", "/api/dashboards/" + p + "dash-workspace/subscription", ""},
		harnessRow{"dash-unsubscribe", http.MethodDelete, "DELETE /api/dashboards/{id}/subscription", "/api/dashboards/" + p + "dash-workspace/subscription", ""},
		harnessRow{"dash-patch-plain", http.MethodPatch, "PATCH /api/dashboards/{id}", "/api/dashboards/" + p + "dash-private-member", `{"name":"x"}`},
		harnessRow{"dash-patch-owner", http.MethodPatch, "PATCH /api/dashboards/{id}", "/api/dashboards/" + p + "dash-private-member", `{"ownerId":"` + member + `x"}`},
		harnessRow{"dash-patch-team-private", http.MethodPatch, "PATCH /api/dashboards/{id}", "/api/dashboards/" + p + "dash-team-mixed", `{"teamIds":["` + fx.privateTeam + `"]}`},
		harnessRow{"dash-patch-invalid", http.MethodPatch, "PATCH /api/dashboards/{id}", "/api/dashboards/" + p + "dash-team-mixed", `{"teamIds":7}`},
		harnessRow{"dash-create-plain", http.MethodPost, "POST /api/dashboards", "/api/dashboards", `{"name":"x"}`},
		harnessRow{"dash-create-team-private", http.MethodPost, "POST /api/dashboards", "/api/dashboards", `{"teamIds":["` + fx.privateTeam + `"]}`},
		harnessRow{"dash-create-invalid", http.MethodPost, "POST /api/dashboards", "/api/dashboards", `nope`},
	)
	fx.runPart(t, "SW493", rows, []harnessControl{
		{"delete-view-workspace/admin/session", true},
		{"delete-view-workspace/member-private-outsider/session", false},
		{"share-view-project-public/member-private-outsider/session", true},
		{"delete-view-team-private/member-private-outsider/session", false},
		{"delete-view-personal-member/member-private-outsider/session", true},
		{"delete-view-personal-member/admin/session", false},
		{"delete-view-project-private/member-private-outsider/session", false},
		{"delete-view-missing/admin/session", false},
		{"create-plain/guest-explicit/session", false},
		{"create-plain/member-private-outsider/session", true},
		{"create-team-private/member-private-outsider/session", false},
		{"results-dash-workspace/member-private-outsider/session", true},
		{"results-dash-workspace/guest-explicit/session", false},
		{"results-dash-private-member/member-private-outsider/session", true},
		{"results-dash-private-member/admin/session", true},
		{"results-dash-team-private/member-private-outsider/session", false},
		{"results-dash-team-mixed/member-private-outsider/session", true},
		{"delete-dash-private-member/member-private-outsider/session", true},
		{"delete-dash-team-mixed/member-private-outsider/session", true},
		{"delete-dash-workspace/member-private-outsider/session", false},
		{"dash-create-team-private/member-private-outsider/session", false},
	}, part, parts)
}

// The view dashboard authorization table runs in 2 top-level tests (every other row each) so the
// CI shards spread it: on PostgreSQL the whole table took 14 minutes.
func TestViewDashboardAuthorizationDifferential(t *testing.T) {
	viewDashboardAuthorizationTable(t, 0, 2)
}

func TestViewDashboardAuthorizationDifferentialPart2(t *testing.T) {
	viewDashboardAuthorizationTable(t, 1, 2)
}
