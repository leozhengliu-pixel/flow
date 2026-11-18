package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// SW495: favorite and subscription references of every resource kind,
// including application credentials whose missing scopes empty a kind.
func TestScaleP21SW495ReferenceAuthorizationDifferential(t *testing.T) {
	fx := scaleP21SW491Setup(t, func(fx *scaleP21SW491Fixture, data *domain.Bootstrap) {
		p := fx.prefix
		archived := fx.archived
		member, admin := fx.f.principals[2].user.ID, fx.f.principals[1].user.ID
		start := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
		data.Labels = append(data.Labels,
			domain.IssueLabel{ID: p + "label-workspace", Name: "w", Scope: "workspace"},
			domain.IssueLabel{ID: p + "label-empty-scope", Name: "e"},
			domain.IssueLabel{ID: p + "label-public", Name: "pub", Scope: fx.public},
			domain.IssueLabel{ID: p + "label-private", Name: "priv", Scope: fx.privateTeam},
			domain.IssueLabel{ID: p + "label-archived", Name: "arch", Scope: "Workspace", ArchivedAt: &archived},
		)
		data.Cycles = append(data.Cycles,
			domain.Cycle{ID: p + "cycle-public", Number: 1, Name: "a", TeamID: fx.public, StartsAt: start, EndsAt: start.AddDate(0, 0, 7)},
			domain.Cycle{ID: p + "cycle-private", Number: 2, Name: "b", TeamID: fx.privateTeam, StartsAt: start, EndsAt: start.AddDate(0, 0, 7)},
		)
		data.SavedViews = append(data.SavedViews,
			domain.SavedView{ID: p + "view-public", SlugID: p + "view-public-slug", Name: "v", Resource: "issues", Scope: "team", TeamID: fx.public, OwnerID: admin, View: "list", Filters: json.RawMessage(`{}`), Display: json.RawMessage(`{}`), Insights: json.RawMessage(`{}`)},
			domain.SavedView{ID: p + "view-personal", Name: "v", Resource: "issues", Scope: "personal", OwnerID: member, View: "list", Filters: json.RawMessage(`{}`), Display: json.RawMessage(`{}`), Insights: json.RawMessage(`{}`)},
		)
		data.Settings[dashboardsSettingsKey] = []any{
			map[string]any{"id": p + "dash-private", "name": "d", "ownerId": admin, "visibility": "private", "teamIds": []any{}, "widgets": []any{}, "subscriberIds": []any{}},
			map[string]any{"id": p + "dash-team", "name": "d", "ownerId": admin, "visibility": "team", "teamIds": []any{fx.public}, "widgets": []any{}, "subscriberIds": []any{}},
		}
		data.ReleasePipelines = append(data.ReleasePipelines,
			domain.ReleasePipeline{ID: p + "pipe-public", Name: "a", TeamIDs: []string{fx.public}},
			domain.ReleasePipeline{ID: p + "pipe-private", Name: "b", TeamIDs: []string{fx.privateTeam}},
		)
		data.Releases = append(data.Releases,
			domain.Release{ID: p + "rel-public", Name: "a", PipelineID: p + "pipe-public"},
			domain.Release{ID: p + "rel-private", Name: "b", PipelineID: p + "pipe-private"},
			domain.Release{ID: p + "rel-loose", Name: "c"},
			domain.Release{ID: p + "rel-linked-public", Name: "d", ProjectIDs: []string{fx.ids["pub-project"]}},
			domain.Release{ID: p + "rel-linked-unscoped", Name: "e", ProjectIDs: []string{fx.ids["unscoped-project"]}},
			domain.Release{ID: p + "rel-linked-private", Name: "f", ProjectIDs: []string{"m2-unrelated"}},
		)
		data.Customers = append(data.Customers, domain.Customer{ID: p + "cust", Name: "c"})
		data.CustomerRequests = append(data.CustomerRequests,
			domain.CustomerRequest{ID: p + "cr", CustomerID: p + "cust", Body: "x"},
			domain.CustomerRequest{ID: p + "cr-private", CustomerID: p + "cust", ProjectID: "m2-unrelated", Body: "x"},
			domain.CustomerRequest{ID: p + "cr-public-project", CustomerID: p + "cust", ProjectID: fx.ids["pub-project"], Body: "x"},
			domain.CustomerRequest{ID: p + "cr-unscoped-project", CustomerID: p + "cust", ProjectID: fx.ids["unscoped-project"], Body: "x"},
		)
		data.Asks = append(data.Asks, domain.Ask{ID: p + "ask-public", TeamID: fx.public}, domain.Ask{ID: p + "ask-private", TeamID: fx.privateTeam})
		data.Documents = append(data.Documents,
			domain.Document{ID: p + "doc-open", SlugID: p + "doc-open-slug", Title: "open", TeamIDs: []string{}, Content: "x"},
			domain.Document{ID: p + "doc-public", Title: "public", TeamIDs: []string{fx.public}, Content: "x"},
			domain.Document{ID: p + "doc-private", Title: "private", TeamIDs: []string{fx.privateTeam}, Content: "x"},
		)
	})
	appKey := func(scopes ...string) *domain.APIKey {
		return &domain.APIKey{ID: "m2-app", Actor: "app", Scopes: scopes, TeamRestriction: "all"}
	}
	fx.credentials = append(fx.credentials,
		scaleP21SW491Credential{"app-unscoped", appKey("read", "write")},
		scaleP21SW491Credential{"app-scoped", appKey("read", "write", "customer:read", "initiative:read", "release:read")},
	)
	p, esc := fx.prefix, url.PathEscape
	refs := []struct{ kind, id string }{
		{"issue", p + "missing-issue"},
		{"team", fx.public}, {"team", fx.privateTeam}, {"team", fx.ids["retired-team"]}, {"team", fx.publicKey},
		{"label", p + "label-workspace"}, {"label", p + "label-empty-scope"}, {"label", p + "label-public"}, {"label", p + "label-private"}, {"label", p + "label-archived"},
		{"release_pipeline", p + "pipe-public"}, {"release_pipeline", p + "pipe-private"},
		{"review", p + "review-open"}, {"review", p + "review-hidden-slug"},
		{"project", fx.ids["pub-project"]}, {"project", p + "pub-old"}, {"project", "m2-unrelated"}, {"project", fx.ids["unscoped-project"]}, {"project", fx.ids["child-project"]},
		{"initiative", fx.ids["pub-initiative"]}, {"initiative", fx.ids["private-initiative"]}, {"initiative", fx.ids["unscoped-initiative"]},
		{"view", p + "view-public"}, {"view", p + "view-public-slug"}, {"view", p + "view-personal"},
		{"dashboard", p + "dash-private"}, {"dashboard", p + "dash-team"},
		{"cycle", p + "cycle-public"}, {"cycle", p + "cycle-private"},
		{"release", p + "rel-public"}, {"release", p + "rel-private"}, {"release", p + "rel-loose"},
		{"release", p + "rel-linked-public"}, {"release", p + "rel-linked-unscoped"}, {"release", p + "rel-linked-private"},
		{"document", p + "doc-open"}, {"document", p + "doc-open-slug"}, {"document", p + "doc-public"}, {"document", p + "doc-private"}, {"document", p + "doc-missing"},
		{"customer", p + "cust"}, {"customer_request", p + "cr"}, {"customer_request", p + "cr-private"},
		{"customer_request", p + "cr-public-project"}, {"customer_request", p + "cr-unscoped-project"},
		{"ask", p + "ask-public"}, {"ask", p + "ask-private"},
		{"unknown", p + "x"},
	}
	rows := []scaleP21SW491Row{}
	for _, ref := range refs {
		name := ref.kind + "-" + ref.id
		rows = append(rows,
			scaleP21SW491Row{"favorite-" + name, http.MethodPut, "PUT /api/favorites/{type}/{id}", "/api/favorites/" + ref.kind + "/" + esc(ref.id), ""},
			scaleP21SW491Row{"unsubscribe-" + name, http.MethodDelete, "DELETE /api/subscriptions/{type}/{id}", "/api/subscriptions/" + ref.kind + "/" + esc(ref.id), ""},
		)
	}
	rows = append(rows,
		scaleP21SW491Row{"favorite-patch", http.MethodPatch, "PATCH /api/favorites/{type}/{id}", "/api/favorites/view/" + p + "view-personal", `{"folderId":"f"}`},
		scaleP21SW491Row{"favorite-delete", http.MethodDelete, "DELETE /api/favorites/{type}/{id}", "/api/favorites/dashboard/" + p + "dash-private", ""},
		scaleP21SW491Row{"subscribe", http.MethodPut, "PUT /api/subscriptions/{type}/{id}", "/api/subscriptions/cycle/" + p + "cycle-public", ""},
		scaleP21SW491Row{"kind-case", http.MethodPut, "PUT /api/favorites/{type}/{id}", "/api/favorites/%20Project%20/" + fx.ids["pub-project"], ""},
		scaleP21SW491Row{"escaped-id", http.MethodPut, "PUT /api/favorites/{type}/{id}", "/api/favorites/project/" + esc(fx.ids["pub-project"]+"/x"), ""},
	)
	fx.run(t, "SW495", rows, []scaleP21SW491Control{
		{"favorite-team-" + fx.public + "/member-private-outsider/session", true},
		{"favorite-team-" + fx.privateTeam + "/member-private-outsider/session", false},
		{"favorite-label-" + p + "label-private/member-private-outsider/session", false},
		{"favorite-label-" + p + "label-public/member-private-outsider/session", true},
		{"favorite-label-" + p + "label-archived/admin/session", false},
		{"favorite-project-" + p + "pub-old/member-private-outsider/session", true},
		{"favorite-view-" + p + "view-personal/member-private-outsider/session", true},
		{"favorite-view-" + p + "view-personal/admin/session", false},
		{"favorite-dashboard-" + p + "dash-private/member-private-outsider/session", false},
		{"favorite-cycle-" + p + "cycle-private/member-private-outsider/session", false},
		{"favorite-release-" + p + "rel-public/member-private-outsider/session", true},
		{"favorite-release-" + p + "rel-public/member-private-outsider/app-unscoped", false},
		{"favorite-release-" + p + "rel-public/member-private-outsider/app-scoped", true},
		{"favorite-customer-" + p + "cust/member-private-outsider/app-unscoped", false},
		{"favorite-customer-" + p + "cust/member-private-outsider/session", true},
		{"favorite-document-" + p + "doc-open/member-private-outsider/session", true},
		{"favorite-document-" + p + "doc-private/member-private-outsider/session", false},
		{"favorite-unknown-" + p + "x/admin/session", false},
		{"favorite-project-" + fx.ids["unscoped-project"] + "/member-private-outsider/session", true},
		{"favorite-project-" + fx.ids["unscoped-project"] + "/member-private-outsider/selected-set", false},
		{"favorite-label-" + p + "label-public/member-private-outsider/selected-empty", false},
		{"favorite-release-" + p + "rel-linked-public/member-private-outsider/selected-set", true},
		{"favorite-release-" + p + "rel-linked-unscoped/member-private-outsider/selected-set", false},
	})
}
