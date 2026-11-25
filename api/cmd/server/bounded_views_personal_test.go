package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

var generatedSavedViewSlugSuffix = regexp.MustCompile(`-view-[0-9]{19}-[0123456789abcdefghjkmnpqrstvwxyz]{10}`)

func normalizeSavedViewCreate(out sw530Outcome) sw530Outcome {
	normalize := func(value string) string {
		return generatedSavedViewSlugSuffix.ReplaceAllString(value, "-view-<stamp>")
	}
	out.Body = normalize(out.Body)
	for _, entries := range [][]string{out.Rows, out.Events, out.Realtime, out.Webhooks} {
		for i := range entries {
			entries[i] = normalize(entries[i])
		}
	}
	return out
}

func TestBoundedSavedViewDifferential(t *testing.T) {
	t.Setenv("FLOW_API_RATE_LIMIT", "off")
	t.Setenv("FLOW_RECORD_ACCESS", "on")
	x := sw530Setup(t)
	owner, member, guest, child := x.principals[0], x.principals[2], x.principals[3], x.principals[4]
	fx := x.fx
	const editable = "p3-editable"
	const editableSlug = "p3-editable-slug"
	err := fx.repo.MutateWorkspace(t.Context(), x.workspace, "view.updated", "", nil, func(data *domain.Bootstrap) error {
		for i := range data.APIKeys {
			if data.APIKeys[i].CreatorID == owner.user.ID || data.APIKeys[i].CreatorID == member.user.ID {
				data.APIKeys[i].TeamRestriction = "selected"
				data.APIKeys[i].TeamIDs = []string{fx.f.public}
			}
		}
		view := func(id, slug, scope, teamID, projectID, ownerID, token string) domain.SavedView {
			return domain.SavedView{ID: id, SlugID: slug, Name: id, Scope: scope, TeamID: teamID, ProjectID: projectID,
				OwnerID: ownerID, ShareToken: token, Resource: "issues", Icon: "CustomView", Color: "#bec2c8", View: "all",
				Filters: json.RawMessage("[]"), Display: json.RawMessage("{}"), Insights: json.RawMessage("{}")}
		}
		data.SavedViews = append(data.SavedViews,
			view(editable, editableSlug, "workspace", "", "", owner.user.ID, "p3-public"),
			view("p3-personal", "", "personal", "", fx.ids["pub-project"], member.user.ID, ""),
			view("p3-private", "", "team", fx.privateTeam, "", owner.user.ID, "p3-team"),
			view("p3-private-project", "", "workspace", "", "m2-unrelated", owner.user.ID, "p3-project"),
			view("p3-child", "", "team", fx.childTeam, "", child.user.ID, ""),
			view("share", "", "workspace", "", "", owner.user.ID, ""),
			view("p3-revoked-view", "", "workspace", "", "", owner.user.ID, "p3-revoked"),
			// The first ID-or-slug occurrence wins over a later exact ID.
			view("p3-first", "p3-collision", "workspace", "", "", owner.user.ID, "p3-first-token"),
			view("p3-collision", "", "workspace", "", "", owner.user.ID, "p3-first-token"),
			view("p3-duplicate", "", "workspace", "", "", owner.user.ID, ""),
			view("p3-duplicate", "", "workspace", "", "", owner.user.ID, ""),
		)
		data.Favorites = []domain.Favorite{
			{ID: "p3-favorite-other", UserID: member.user.ID, ResourceType: "view", ResourceID: editable, Position: -99},
			{ID: "p3-favorite-one", UserID: owner.user.ID, ResourceType: "view", ResourceID: "other-view", Position: -12},
			{ID: "p3-favorite-two", UserID: owner.user.ID, ResourceType: "view", ResourceID: "other-view-2", Position: -12},
			{ID: "p3-favorite-three", UserID: owner.user.ID, ResourceType: "view", ResourceID: "other-view-3", Position: -13},
			{ID: "p3-favorite-folder", UserID: owner.user.ID, ResourceType: "view", ResourceID: "other-view-4", FolderID: "folder", Position: -50},
			{ID: "p3-favorite-personal", UserID: member.user.ID, ResourceType: "view", ResourceID: "p3-personal", Position: -15},
		}
		data.Subscriptions = []domain.Subscription{
			{ID: "p3-subscription-one", UserID: owner.user.ID, ResourceType: "view", ResourceID: editable},
			{ID: "p3-subscription-two", UserID: member.user.ID, ResourceType: "view", ResourceID: editable},
			{ID: "p3-subscription-keep", UserID: member.user.ID, ResourceType: "view", ResourceID: "other-view"},
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	w, timedOut := x.call(x.legacy.handler, http.MethodDelete, "/api/views/p3-revoked-view/share", "", owner, false)
	if timedOut || w.Code != http.StatusNoContent {
		t.Fatalf("fixture share revocation returned %d", w.Code)
	}
	if err := fx.repo.ReloadWorkspace(t.Context(), x.workspace); err != nil {
		t.Fatal(err)
	}
	data, ok := fx.repo.BootstrapFor(x.workspace)
	if !ok {
		t.Fatal("saved-view fixture workspace missing")
	}
	x.baseline, err = json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	x.probeStale = true

	check := func(t *testing.T, request sw530Request, status int) {
		t.Helper()
		if !scaleP21RouteHot(request.pattern) {
			t.Fatal("saved-view route is COLD")
		}
		views, slots := x.totalViews, x.totalSlots
		outcomes := x.outcomes(t, request, []string{"legacy", "warm", "reloaded"})
		if outcomes["legacy"].Status == http.StatusCreated {
			for mode, outcome := range outcomes {
				outcomes[mode] = normalizeSavedViewCreate(outcome)
			}
		}
		if want := outcomes["legacy"]; want.TimedOut || status != 0 && want.Status != status {
			t.Errorf("legacy status = %d, want %d (timed out: %t)", want.Status, status, want.TimedOut)
		}
		for _, mode := range []string{"warm", "reloaded"} {
			if outcomes[mode].TimedOut {
				t.Errorf("%s timed out", mode)
			} else if diff, _, _ := sw530Differences(outcomes["legacy"], outcomes[mode]); diff != "" {
				t.Errorf("%s differed in %s (status %d)", mode, diff, outcomes[mode].Status)
			}
		}
		if x.totalViews != views || x.totalSlots != slots || len(x.skeletalBy[request.pattern]) != 0 {
			t.Error("saved-view route used COLD or a legacy accessor")
		}
	}

	type testCase struct {
		name, method, ref, body string
		principal               *sw530Principal
		key                     bool
		want                    int
	}
	for _, tc := range []testCase{
		{"create-workspace", "POST", "", `{"name":"New view"}`, owner, false, 201},
		{"create-project", "POST", "", `{"name":"Project view","projectId":"` + fx.ids["pub-project"] + `","favorite":true}`, owner, true, 201},
		{"create-inherited-team", "POST", "", `{"name":"Team view","scope":"team","teamId":"` + fx.childTeam + `"}`, child, false, 201},
		{"create-pulse-override", "POST", "", `{"name":"Pulse view","resource":"pulse","scope":"workspace","ownerId":"missing","favorite":true}`, member, false, 201},
		{"create-filter-tree", "POST", "", `{"name":"With filters","filters":[{"tree":{"conjunction":"and","items":[{"conjunction":"or","items":[{"field":"priority"}]}]}}]}`, owner, false, 201},
		{"create-invalid-filter", "POST", "", `{"name":"Invalid filter","filters":[{"tree":{"conjunction":"and","items":[{"conjunction":"xor","items":[{"field":"priority"}]}]}}]}`, owner, false, 400},
		{"create-project-alias-invalid", "POST", "", `{"name":"Project view","projectId":"` + fx.prefix + `pub-old"}`, owner, false, 400},
		{"create-private-team-denied", "POST", "", `{"name":"Denied","teamId":"` + fx.privateTeam + `"}`, member, true, 403},
		{"create-guest-denied", "POST", "", `{"name":"Denied"}`, guest, false, 403},
		{"create-invalid-json", "POST", "", `{`, owner, false, 403},
		{"patch-slug", "PATCH", editableSlug, `{"name":"Renamed","scope":"personal","favorite":true}`, owner, false, 200},
		{"patch-unchanged", "PATCH", editable, `{}`, owner, false, 200},
		{"patch-favorite-add", "PATCH", editable, `{"favorite":true}`, owner, false, 200},
		{"patch-other-owner-denied", "PATCH", editable, `{"favorite":false}`, member, false, 403},
		{"patch-favorite-remove", "PATCH", "p3-personal", `{"favorite":false}`, member, false, 200},
		{"patch-owner-denied", "PATCH", "p3-personal", `{"ownerId":"` + owner.user.ID + `"}`, member, false, 403},
		{"patch-member-favorite", "PATCH", "p3-personal", `{"favorite":true}`, member, false, 200},
		{"patch-private-key-denied", "PATCH", "p3-private", `{"name":"Denied"}`, owner, true, 403},
		{"patch-duplicate", "PATCH", "p3-duplicate", `{"name":"First only"}`, owner, false, 200},
		{"delete-all-preferences", "DELETE", editableSlug, "", owner, false, 204},
		{"delete-slug-precedence", "DELETE", "p3-collision", "", owner, false, 204},
		{"delete-share-id", "DELETE", "share", "", owner, false, 204},
		{"delete-missing-denied", "DELETE", "p3-missing", "", owner, false, 403},
		{"delete-other-personal-denied", "DELETE", "p3-personal", "", owner, false, 403},
		{"lookup-public", "GET", "p3-public", "", nil, false, 200},
		{"lookup-head", "HEAD", "p3-public", "", nil, false, 200},
		{"lookup-first-token", "GET", "p3-first-token", "", nil, false, 200},
		{"lookup-private-team", "GET", "p3-team", "", owner, false, 404},
		{"lookup-private-project", "GET", "p3-project", "", owner, false, 404},
		{"lookup-revoked", "GET", "p3-revoked", "", nil, false, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, pattern := "/api/views", "POST /api/views"
			switch tc.method {
			case "PATCH", "DELETE":
				pattern, path = tc.method+" /api/views/{id}", path+"/"+url.PathEscape(tc.ref)
			case "GET", "HEAD":
				pattern = "GET /api/shared/views/{token}"
				path = "/api/shared/views/" + url.PathEscape(tc.ref) + "?workspace=" + url.QueryEscape(x.workspace)
			}
			request := sw530Request{pattern, tc.method, path, tc.body, tc.principal, tc.key}
			check(t, request, tc.want)
		})
	}
	for _, pattern := range []string{"POST /api/views", "PATCH /api/views/{id}", "DELETE /api/views/{id}", "GET /api/shared/views/{token}"} {
		method, path := x.bind(pattern)
		body := x.body(pattern, method)
		for _, principal := range x.principals {
			for _, bearer := range []bool{false, true} {
				request := sw530Request{pattern, method, path, body, principal, bearer}
				t.Run("roster/"+request.label(), func(t *testing.T) { check(t, request, 0) })
			}
		}
	}
	if held := store.ColdSlotsHeldForTesting(); held != 0 {
		t.Errorf("%d COLD slots remain held", held)
	}
}
