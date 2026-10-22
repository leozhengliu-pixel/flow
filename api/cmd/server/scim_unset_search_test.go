package main

import (
	"net/http"
	"slices"
	"strconv"
	"testing"
)

// PATCH remove of active unsets the attribute (the resource stops reporting
// it) without changing the member's state: an active user is not
// deprovisioned and a suspended one is not restored. Writing active again
// brings it back.
func TestSCIMPatchRemoveActive(t *testing.T) {
	f := newSCIMFixture(t)
	base := "/scim/v2/test-workspace/Users/"
	created := f.call(t, http.MethodPost, "/scim/v2/test-workspace/Users", `{"userName":"unset@example.test","externalId":"unset-1"}`, http.StatusCreated)
	id := created["id"].(string)
	patch := func(operations string) map[string]any {
		return f.call(t, http.MethodPatch, base+id, `{"Operations":[`+operations+`]}`, http.StatusOK)
	}
	removed := patch(`{"op":"remove","path":"active"}`)
	if _, present := removed["active"]; present {
		t.Fatalf("removed = %v", removed)
	}
	if status, _ := workspaceMemberStatus(t, f, id); status == "suspended" || status == "removed" {
		t.Fatalf("active user status after remove = %q", status)
	}
	if got := f.call(t, http.MethodGet, base+id, nil, http.StatusOK); got["active"] != nil || got["userName"] != "unset@example.test" {
		t.Fatalf("get = %v", got)
	}
	// An unrelated PATCH or a PUT without active keeps it unset.
	patch(`{"op":"replace","path":"title","value":"Dev"}`)
	if got := f.call(t, http.MethodPut, base+id, `{"userName":"unset@example.test","externalId":"unset-1"}`, http.StatusOK); got["active"] != nil {
		t.Fatalf("put without active = %v", got)
	}
	if restored := patch(`{"op":"replace","path":"active","value":true}`); restored["active"] != true {
		t.Fatalf("restored = %v", restored)
	}

	// A suspended user stays suspended.
	patch(`{"op":"replace","path":"active","value":false}`)
	suspended := patch(`{"op":"remove","path":"active"}`)
	if _, present := suspended["active"]; present {
		t.Fatalf("suspended+removed = %v", suspended)
	}
	if status, _ := workspaceMemberStatus(t, f, id); status != "suspended" {
		t.Fatalf("suspended user status after remove = %q", status)
	}
	if back := patch(`{"op":"add","path":"active","value":"False"}`); back["active"] != false {
		t.Fatalf("explicit false = %v", back)
	}

	entries := scimAuditEntries(t, f.repository, scimAuditMemberResource)
	unset := 0
	for _, entry := range entries {
		if entry.ResourceID == id && entry.Metadata["activeUnset"] == true {
			unset++
		}
	}
	if actions := scimAuditActions(entries, id); unset != 2 || slices.Contains(actions, "removed") {
		t.Fatalf("audit actions = %v, activeUnset entries = %d", actions, unset)
	}
}

// POST /.search searches Users and Groups together.
func TestSCIMRootSearch(t *testing.T) {
	f := newSCIMFixture(t)
	root := "/scim/v2/test-workspace/.search"
	for _, name := range []string{"carol", "alice", "bob"} {
		f.call(t, http.MethodPost, "/scim/v2/test-workspace/Users", map[string]any{"userName": name + "@search.test", "displayName": "User " + name}, http.StatusCreated)
	}
	for _, name := range []string{"Zeta", "Alpha"} {
		f.call(t, http.MethodPost, "/scim/v2/test-workspace/Groups", map[string]any{"displayName": name + " group"}, http.StatusCreated)
	}
	search := func(body string) (map[string]any, []map[string]any) {
		result := f.call(t, http.MethodPost, root, `{"schemas":["urn:ietf:params:scim:api:messages:2.0:SearchRequest"],`+body+`}`, http.StatusOK)
		return result, scimResources(t, result)
	}
	types := func(resources []map[string]any) []string {
		result := []string{}
		for _, resource := range resources {
			result = append(result, resource["meta"].(map[string]any)["resourceType"].(string))
		}
		return result
	}
	names := func(resources []map[string]any) []string {
		result := []string{}
		for _, resource := range resources {
			if name, ok := resource["displayName"].(string); ok {
				result = append(result, name)
			}
		}
		return result
	}
	mixed := `"filter":"displayName co \"User \" or displayName co \" group\""`

	// Default order: Users by userName, then Groups by displayName.
	list, all := search(mixed + `,"count":100`)
	if list["totalResults"] != float64(5) || list["schemas"].([]any)[0] != scimMessageListResponse {
		t.Fatalf("list = %v", list)
	}
	if !slices.Equal(types(all), []string{"User", "User", "User", "Group", "Group"}) || !slices.Equal(names(all), []string{"User alice", "User bob", "User carol", "Alpha group", "Zeta group"}) {
		t.Fatalf("default order = %v %v", types(all), names(all))
	}
	for _, resource := range all {
		schemas := resource["schemas"].([]any)
		want := scimSchemaUser
		if resource["meta"].(map[string]any)["resourceType"] == "Group" {
			want = scimSchemaGroup
		}
		if schemas[0] != want {
			t.Fatalf("schemas = %v for %v", schemas, resource["meta"])
		}
	}

	// sortBy orders across both types, and paging walks it without gaps.
	_, sorted := search(mixed + `,"sortBy":"displayName","sortOrder":"descending","count":100`)
	want := []string{"Zeta group", "User carol", "User bob", "User alice", "Alpha group"}
	if !slices.Equal(names(sorted), want) {
		t.Fatalf("descending = %v", names(sorted))
	}
	paged := []string{}
	for start := 1; start <= 5; start += 2 {
		page, resources := search(mixed + `,"sortBy":"displayName","sortOrder":"descending","startIndex":` + strconv.Itoa(start) + `,"count":2`)
		if page["totalResults"] != float64(5) || page["startIndex"] != float64(start) {
			t.Fatalf("page = %v", page)
		}
		paged = append(paged, names(resources)...)
	}
	if !slices.Equal(paged, want) {
		t.Fatalf("paged = %v", paged)
	}

	// A filter that only Groups match.
	if _, groups := search(`"filter":"displayName ew \" group\"","count":10`); !slices.Equal(names(groups), []string{"Alpha group", "Zeta group"}) || !slices.Equal(types(groups), []string{"Group", "Group"}) {
		t.Fatalf("groups = %v", groups)
	}
	if _, only := search(`"filter":"displayName eq \"Alpha group\"","attributes":["displayName"]`); len(only) != 1 || only[0]["userName"] != nil || only[0]["displayName"] != "Alpha group" || only[0]["id"] == nil {
		t.Fatalf("attributes = %v", only)
	}
	if _, excluded := search(`"filter":"userName eq \"alice@search.test\"","excludedAttributes":["emails","name"]`); len(excluded) != 1 || excluded[0]["emails"] != nil || excluded[0]["userName"] != "alice@search.test" {
		t.Fatalf("excludedAttributes = %v", excluded)
	}
	if empty, none := search(`"filter":"userName eq \"nobody\"","count":0`); empty["totalResults"] != float64(0) || len(none) != 0 {
		t.Fatalf("empty = %v", empty)
	}
	requireSCIMError(t, f.call(t, http.MethodPost, root, `{"filter":"displayName bogus"}`, http.StatusBadRequest), "400", "invalidFilter")
}
