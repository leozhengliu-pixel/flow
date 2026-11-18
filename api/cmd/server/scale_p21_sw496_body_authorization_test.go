package main

import (
	"maps"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"flow/api/internal/domain"
)

// SW496: request-body predicates under projects, initiatives and the
// document create root, including the escaped relation-ID PATCH whose
// decoded segments select the project/initiative PATCH body predicate.
func TestScaleP21SW496BodyAuthorizationDifferential(t *testing.T) {
	fx := scaleP21SW491Setup(t, func(fx *scaleP21SW491Fixture, data *domain.Bootstrap) {
		data.Customers = append(data.Customers, domain.Customer{ID: fx.prefix + "cust", Name: "c"})
	})
	fx.credentials = append(fx.credentials,
		scaleP21SW491Credential{"app-unscoped", &domain.APIKey{ID: "m2-app", Actor: "app", Scopes: []string{"read", "write"}, TeamRestriction: "all"}},
		scaleP21SW491Credential{"app-initiatives", &domain.APIKey{ID: "m2-app", Actor: "app", Scopes: []string{"read", "write", "initiative:write"}, TeamRestriction: "all"}},
	)
	p, ids, esc := fx.prefix, fx.ids, url.PathEscape
	member, admin, guest := fx.f.principals[2].user.ID, fx.f.principals[1].user.ID, fx.f.principals[3].user.ID
	suspended, outsider := fx.principals[5].user.ID, fx.principals[6].user.ID
	projectBodies := map[string]string{
		"empty": `{}`, "invalid": `{`, "team-private": `{"teamIds":["` + fx.privateTeam + `"]}`, "team-public": `{"teamIds":["` + fx.public + `"]}`,
		"dependency-slug": `{"dependencyIds":["` + p + `pub-old"]}`, "dependency-private": `{"dependencyIds":["m2-unrelated"]}`,
		"lead-member": `{"leadId":"` + member + `"}`, "lead-admin": `{"leadId":"` + admin + `"}`, "lead-guest": `{"leadId":"` + guest + `"}`,
		"lead-suspended": `{"leadId":"` + suspended + `"}`, "lead-outsider": `{"leadId":"` + outsider + `"}`,
		"members":           `{"memberIds":["` + member + `","` + admin + `"]}`,
		"initiative-public": `{"initiatives":["` + ids["pub-initiative"] + `"]}`, "initiative-private": `{"initiatives":["` + ids["private-initiative"] + `"]}`,
		"initiative-unscoped": `{"initiatives":["` + ids["unscoped-initiative"] + `"]}`, "initiative-slug": `{"initiatives":["` + p + `init-old"]}`,
		"customer": `{"customers":["` + p + `cust"]}`, "customer-missing": `{"customers":["` + p + `gone"]}`,
	}
	rows := []scaleP21SW491Row{}
	for _, name := range slices.Sorted(maps.Keys(projectBodies)) {
		body := projectBodies[name]
		rows = append(rows,
			scaleP21SW491Row{"project-create-" + name, http.MethodPost, "POST /api/projects", "/api/projects", body},
			scaleP21SW491Row{"project-patch-" + name, http.MethodPatch, "PATCH /api/projects/{id}", "/api/projects/" + ids["pub-project"], body},
		)
	}
	rows = append(rows,
		scaleP21SW491Row{"project-patch-private", http.MethodPatch, "PATCH /api/projects/{id}", "/api/projects/m2-unrelated", `{}`},
		scaleP21SW491Row{"project-patch-old-slug", http.MethodPatch, "PATCH /api/projects/{id}", "/api/projects/" + p + "pub-old", `{}`},
		scaleP21SW491Row{"project-comment-patch", http.MethodPatch, "PATCH /api/projects/{id}/comments/{commentId}", "/api/projects/" + ids["pub-project"] + "/comments/c", `{"teamIds":["` + fx.privateTeam + `"]}`},
		scaleP21SW491Row{"project-milestone-patch", http.MethodPatch, "PATCH /api/projects/{id}/milestones/{milestoneId}", "/api/projects/" + ids["child-project"] + "/milestones/m", `{}`},
		scaleP21SW491Row{"project-resource-patch", http.MethodPatch, "PATCH /api/projects/{id}/resources/{resourceId}", "/api/projects/" + ids["unscoped-project"] + "/resources/r", `{}`},
		scaleP21SW491Row{"project-update-patch", http.MethodPatch, "PATCH /api/projects/{id}/updates/{updateId}", "/api/projects/" + ids["pub-project"] + "/updates/u", `x`},
		scaleP21SW491Row{"project-relation-public", http.MethodPost, "POST /api/projects/{id}/relations", "/api/projects/" + ids["pub-project"] + "/relations", `{"relatedProjectId":"` + ids["unscoped-project"] + `"}`},
		scaleP21SW491Row{"project-relation-private", http.MethodPost, "POST /api/projects/{id}/relations", "/api/projects/" + ids["pub-project"] + "/relations", `{"relatedProjectId":"m2-unrelated"}`},
		scaleP21SW491Row{"project-relation-invalid", http.MethodPost, "POST /api/projects/{id}/relations", "/api/projects/" + ids["pub-project"] + "/relations", `{"relatedProjectId":1}`},
		scaleP21SW491Row{"project-relation-escaped-patch", http.MethodPatch, "PATCH /api/projects/{id}/relations/{relationId}", "/api/projects/" + esc(ids["pub-project"]+"/x") + "/relations/" + p + "prel-public", `{}`},
	)
	initiativeBodies := map[string]string{
		"empty": `{}`, "invalid": `{`, "lead-private": `{"leadTeamId":"` + fx.privateTeam + `"}`, "lead-public": `{"leadTeamId":"` + fx.public + `"}`,
		"contributing-private": `{"contributingTeamIds":["` + fx.privateTeam + `"]}`, "projects-slug": `{"projectIds":["` + p + `pub-current"]}`,
		"projects-private": `{"projectIds":["m2-unrelated"]}`, "parents-private": `{"parentInitiativeIds":["` + ids["private-initiative"] + `"]}`,
		"parents-public": `{"parentInitiativeIds":["` + ids["pub-initiative"] + `"]}`, "favorite": `{"favorite":true}`,
	}
	for _, name := range slices.Sorted(maps.Keys(initiativeBodies)) {
		body := initiativeBodies[name]
		rows = append(rows,
			scaleP21SW491Row{"initiative-create-" + name, http.MethodPost, "POST /api/initiatives", "/api/initiatives", body},
			scaleP21SW491Row{"initiative-patch-" + name, http.MethodPatch, "PATCH /api/initiatives/{id}", "/api/initiatives/" + ids["pub-initiative"], body},
		)
	}
	rows = append(rows,
		scaleP21SW491Row{"initiative-patch-private-favorite", http.MethodPatch, "PATCH /api/initiatives/{id}", "/api/initiatives/" + ids["private-initiative"], `{"favorite":false}`},
		scaleP21SW491Row{"initiative-comment-patch", http.MethodPatch, "PATCH /api/initiatives/{id}/comments/{commentId}", "/api/initiatives/" + ids["unscoped-initiative"] + "/comments/c", `{}`},
		scaleP21SW491Row{"initiative-resource-patch", http.MethodPatch, "PATCH /api/initiatives/{id}/resources/{resourceId}", "/api/initiatives/" + p + "init-old/resources/r", `{}`},
		scaleP21SW491Row{"initiative-update-patch", http.MethodPatch, "PATCH /api/initiatives/{id}/updates/{updateId}", "/api/initiatives/" + ids["private-initiative"] + "/updates/u", `{}`},
		scaleP21SW491Row{"initiative-relation-public", http.MethodPost, "POST /api/initiatives/{id}/relations", "/api/initiatives/" + ids["pub-initiative"] + "/relations", `{"relatedInitiativeId":"` + ids["unscoped-initiative"] + `"}`},
		scaleP21SW491Row{"initiative-relation-private", http.MethodPost, "POST /api/initiatives/{id}/relations", "/api/initiatives/" + ids["pub-initiative"] + "/relations", `{"relatedInitiativeId":"` + ids["private-initiative"] + `"}`},
		scaleP21SW491Row{"initiative-relation-escaped-patch", http.MethodPatch, "PATCH /api/initiatives/{id}/relations/{relationId}", "/api/initiatives/" + esc(ids["pub-initiative"]+"/x") + "/relations/" + p + "irel-public", `{}`},
		scaleP21SW491Row{"document-create-empty", http.MethodPost, "POST /api/documents", "/api/documents", `{}`},
		scaleP21SW491Row{"document-create-team", http.MethodPost, "POST /api/documents", "/api/documents", `{"teamIds":[" ` + fx.public + ` "]}`},
		scaleP21SW491Row{"document-create-team-private", http.MethodPost, "POST /api/documents", "/api/documents", `{"teamIds":["` + fx.privateTeam + `"]}`},
		scaleP21SW491Row{"document-create-project", http.MethodPost, "POST /api/documents", "/api/documents", `{"projectIds":[" ` + p + `pub-old "]}`},
		scaleP21SW491Row{"document-create-project-private", http.MethodPost, "POST /api/documents", "/api/documents", `{"projectIds":["m2-unrelated"]}`},
		scaleP21SW491Row{"document-create-issue", http.MethodPost, "POST /api/documents", "/api/documents", `{"issueId":" ` + p + `missing-issue "}`},
		scaleP21SW491Row{"document-create-invalid", http.MethodPost, "POST /api/documents", "/api/documents", `{"teamIds":"x"}`},
	)
	fx.run(t, "SW496", rows, []scaleP21SW491Control{
		{"project-create-empty/member-private-outsider/session", true},
		{"project-create-team-private/member-private-outsider/session", false},
		{"project-create-dependency-slug/member-private-outsider/session", true},
		{"project-create-dependency-private/member-private-outsider/session", false},
		{"project-create-lead-suspended/member-private-outsider/session", true},
		{"project-create-lead-outsider/member-private-outsider/session", false},
		{"project-create-initiative-private/member-private-outsider/session", false},
		{"project-create-initiative-slug/admin/session", false},
		{"project-create-customer-missing/admin/session", false},
		{"project-create-invalid/admin/session", false},
		{"project-patch-empty/member-private-outsider/session", true},
		{"project-patch-private/member-private-outsider/session", false},
		{"project-relation-private/member-private-outsider/session", false},
		{"project-relation-public/member-private-outsider/session", true},
		{"initiative-create-empty/guest-explicit/session", false},
		{"initiative-create-empty/member-private-outsider/session", true},
		{"initiative-create-lead-private/member-private-outsider/session", false},
		{"initiative-patch-favorite/member-private-outsider/app-unscoped", false},
		{"initiative-patch-favorite/member-private-outsider/app-initiatives", true},
		{"document-create-empty/member-private-outsider/session", true},
		{"document-create-team-private/member-private-outsider/session", false},
	})
}
