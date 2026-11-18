package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// SW491 covers every bounded fallback decision class of the first closure:
// membership, non-guest roots, project/initiative points and relation IDs,
// team rows/content and embedded review favorites.
func TestScaleP21SW491FallbackAuthorizationDifferential(t *testing.T) {
	fx := scaleP21SW491Setup(t, nil)
	workspace, prefix, ids, foreign := fx.workspace, fx.prefix, fx.ids, fx.foreign
	privateTeam, childTeam, publicKey := fx.privateTeam, fx.childTeam, fx.publicKey
	f := struct{ public string }{fx.public}
	esc := url.PathEscape
	rows := []scaleP21SW491Row{
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
	controls := []scaleP21SW491Control{
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
	}
	fx.run(t, "SW491", rows, controls)
}
