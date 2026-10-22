package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// scimCall sends a SCIM request with extra headers and returns the recorder.
func (f scimFixture) raw(t *testing.T, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+f.token)
	if body != "" {
		req.Header.Set("Content-Type", "application/scim+json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, req)
	return response
}

func scimAuditEntries(t *testing.T, repository *store.SQLiteStore, resourceType string) []domain.AuditLogEntry {
	t.Helper()
	data, _ := repository.BootstrapFor("test-workspace")
	entries := []domain.AuditLogEntry{}
	for _, entry := range data.AuditLog {
		if entry.ResourceType == resourceType && entry.Metadata["source"] == "scim" {
			entries = append(entries, entry)
		}
	}
	return entries
}

func scimAuditActions(entries []domain.AuditLogEntry, resourceID string) []string {
	actions := []string{}
	for _, entry := range entries {
		if resourceID == "" || entry.ResourceID == resourceID {
			actions = append(actions, entry.Action)
		}
	}
	slices.Reverse(actions) // oldest first
	return actions
}

func TestSCIMETagsAndPreconditions(t *testing.T) {
	f := newSCIMFixture(t)
	created := f.raw(t, http.MethodPost, "/scim/v2/test-workspace/Users", `{"userName":"etag@example.test","externalId":"etag-1"}`, nil)
	if created.Code != http.StatusCreated || !strings.HasPrefix(created.Header().Get("ETag"), `W/"`) {
		t.Fatalf("create status=%d etag=%q", created.Code, created.Header().Get("ETag"))
	}
	var user map[string]any
	_ = json.Unmarshal(created.Body.Bytes(), &user)
	id, version := user["id"].(string), created.Header().Get("ETag")
	if user["meta"].(map[string]any)["version"] != version {
		t.Fatalf("meta.version %v != ETag %s", user["meta"], version)
	}
	if got := f.raw(t, http.MethodGet, "/scim/v2/test-workspace/Users/"+id, "", map[string]string{"If-None-Match": version}); got.Code != http.StatusNotModified || got.Body.Len() != 0 {
		t.Fatalf("If-None-Match current = %d %s", got.Code, got.Body.String())
	}
	if got := f.raw(t, http.MethodGet, "/scim/v2/test-workspace/Users/"+id, "", map[string]string{"If-None-Match": `W/"other"`}); got.Code != http.StatusOK || got.Header().Get("ETag") != version {
		t.Fatalf("If-None-Match stale = %d", got.Code)
	}
	patch := `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","path":"displayName","value":"Etag One"}]}`
	updated := f.raw(t, http.MethodPatch, "/scim/v2/test-workspace/Users/"+id, patch, map[string]string{"If-Match": version})
	if updated.Code != http.StatusOK || updated.Header().Get("ETag") == version {
		t.Fatalf("If-Match current = %d etag %q", updated.Code, updated.Header().Get("ETag"))
	}
	stale := f.raw(t, http.MethodPatch, "/scim/v2/test-workspace/Users/"+id, patch, map[string]string{"If-Match": version})
	var body map[string]any
	_ = json.Unmarshal(stale.Body.Bytes(), &body)
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("If-Match stale = %d %s", stale.Code, stale.Body.String())
	}
	requireSCIMError(t, body, "412", "")
	if got := f.raw(t, http.MethodPut, "/scim/v2/test-workspace/Users/"+id, `{"userName":"etag@example.test"}`, map[string]string{"If-Match": `W/"nope"`}); got.Code != http.StatusPreconditionFailed {
		t.Fatalf("PUT stale = %d", got.Code)
	}
	if got := f.raw(t, http.MethodDelete, "/scim/v2/test-workspace/Users/"+id, "", map[string]string{"If-Match": version}); got.Code != http.StatusPreconditionFailed {
		t.Fatalf("DELETE stale = %d", got.Code)
	}
	if got := f.raw(t, http.MethodPatch, "/scim/v2/test-workspace/Users/"+id, patch, map[string]string{"If-Match": "*"}); got.Code != http.StatusOK {
		t.Fatalf("If-Match * = %d", got.Code)
	}

	group := f.raw(t, http.MethodPost, "/scim/v2/test-workspace/Groups", `{"displayName":"ETag group","members":[{"value":"`+id+`"}]}`, nil)
	groupVersion := group.Header().Get("ETag")
	var groupBody map[string]any
	_ = json.Unmarshal(group.Body.Bytes(), &groupBody)
	groupID := groupBody["id"].(string)
	if got := f.raw(t, http.MethodGet, "/scim/v2/test-workspace/Groups/"+groupID+"?excludedAttributes=members", "", map[string]string{"If-None-Match": groupVersion}); got.Code != http.StatusNotModified {
		t.Fatalf("group If-None-Match = %d", got.Code)
	}
	removeMember := `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"remove","path":"members[value eq \"` + id + `\"]"}]}`
	if got := f.raw(t, http.MethodPatch, "/scim/v2/test-workspace/Groups/"+groupID, removeMember, map[string]string{"If-Match": groupVersion}); got.Code != http.StatusOK || got.Header().Get("ETag") == groupVersion {
		t.Fatalf("group If-Match = %d", got.Code)
	}
	if got := f.raw(t, http.MethodDelete, "/scim/v2/test-workspace/Groups/"+groupID, "", map[string]string{"If-Match": groupVersion}); got.Code != http.StatusPreconditionFailed {
		t.Fatalf("group DELETE stale = %d", got.Code)
	}
}

func TestSCIMSortUsersAndGroups(t *testing.T) {
	f := newSCIMFixture(t)
	for _, user := range []struct{ userName, family, external, department string }{
		{"b@example.test", "Zeta", "ext-2", "Sales"},
		{"a@example.test", "Young", "ext-3", ""},
		{"c@example.test", "Alpha", "", "Eng"},
	} {
		body := map[string]any{"userName": user.userName, "name": map[string]any{"givenName": "G", "familyName": user.family}}
		if user.external != "" {
			body["externalId"] = user.external
		}
		if user.department != "" {
			body[scimSchemaEnterpriseUser] = map[string]any{"department": user.department}
		}
		f.call(t, http.MethodPost, "/scim/v2/test-workspace/Users", body, http.StatusCreated)
	}
	order := func(query, attribute string) []string {
		list := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Users?"+query, nil, http.StatusOK)
		values := []string{}
		for _, resource := range scimResources(t, list) {
			switch attribute {
			case "familyName":
				values = append(values, resource["name"].(map[string]any)["familyName"].(string))
			default:
				values = append(values, resource["userName"].(string))
			}
		}
		return values
	}
	cases := []struct {
		query, attribute string
		want             []string
	}{
		{"sortBy=userName", "", []string{"a@example.test", "b@example.test", "c@example.test"}},
		{"sortBy=userName&sortOrder=descending", "", []string{"c@example.test", "b@example.test", "a@example.test"}},
		{"sortBy=name.familyName", "familyName", []string{"Alpha", "Young", "Zeta"}},
		{"sortBy=name.familyName&sortOrder=descending&count=2", "familyName", []string{"Zeta", "Young"}},
		{"sortBy=name.familyName&startIndex=2&count=1", "familyName", []string{"Young"}},
		// externalId: the user without one sorts last ascending, first descending.
		{"sortBy=externalId", "", []string{"b@example.test", "a@example.test", "c@example.test"}},
		{"sortBy=externalId&sortOrder=descending", "", []string{"c@example.test", "a@example.test", "b@example.test"}},
		{"sortBy=" + scimSchemaEnterpriseUser + ":department", "", []string{"c@example.test", "b@example.test", "a@example.test"}},
		{"sortBy=userName&sortOrder=descending&filter=" + filterQuery(`userName ne "b@example.test"`), "", []string{"c@example.test", "a@example.test"}},
		{"sortBy=meta.created&sortOrder=descending", "", []string{"c@example.test", "a@example.test", "b@example.test"}},
	}
	for _, item := range cases {
		if got := order(item.query, item.attribute); !slices.Equal(got, item.want) {
			t.Errorf("%s = %v, want %v", item.query, got, item.want)
		}
	}
	for _, name := range []string{"Beta", "alpha", "Gamma"} {
		f.call(t, http.MethodPost, "/scim/v2/test-workspace/Groups", map[string]any{"displayName": name}, http.StatusCreated)
	}
	groups := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Groups?excludedAttributes=members&sortBy=displayName&sortOrder=descending&filter="+filterQuery(`displayName sw "a" or displayName sw "b" or displayName sw "g"`), nil, http.StatusOK)
	names := []string{}
	for _, resource := range scimResources(t, groups) {
		names = append(names, resource["displayName"].(string))
	}
	if !slices.Equal(names, []string{"Gamma", "Beta", "alpha"}) {
		t.Fatalf("groups sorted = %v", names)
	}
}

func TestSCIMBulk(t *testing.T) {
	f := newSCIMFixture(t)
	request := `{"schemas":["urn:ietf:params:scim:api:messages:2.0:BulkRequest"],"failOnErrors":5,"Operations":[
		{"method":"POST","path":"/Groups","bulkId":"g1","data":{"displayName":"Bulk group","members":[{"value":"bulkId:u1"},{"value":"bulkId:u2"}]}},
		{"method":"POST","path":"/Users","bulkId":"u1","data":{"userName":"bulk1@example.test","externalId":"bulk-1"}},
		{"method":"POST","path":"/Users","bulkId":"u2","data":{"userName":"bulk2@example.test"}},
		{"method":"PATCH","path":"/Users/bulkId:u2","data":{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","path":"title","value":"Lead"}]}},
		{"method":"POST","path":"/Users","bulkId":"dup","data":{"userName":"bulk1@example.test"}},
		{"method":"DELETE","path":"/Users/does-not-exist"},
		{"method":"PUT","path":"/Users/bulkId:dup","data":{"userName":"x@example.test"}}
	]}`
	response := f.call(t, http.MethodPost, "/scim/v2/test-workspace/Bulk", request, http.StatusOK)
	if response["schemas"].([]any)[0] != scimMessageBulkResponse {
		t.Fatalf("bulk response = %v", response)
	}
	operations := response["Operations"].([]any)
	statuses := []string{}
	for _, item := range operations {
		statuses = append(statuses, item.(map[string]any)["status"].(string))
	}
	if !slices.Equal(statuses, []string{"201", "201", "201", "200", "409", "404", "409"}) {
		t.Fatalf("bulk statuses = %v (%v)", statuses, operations)
	}
	group := operations[0].(map[string]any)
	if group["bulkId"] != "g1" || !strings.Contains(group["location"].(string), "/Groups/") || !strings.HasPrefix(group["version"].(string), `W/"`) {
		t.Fatalf("bulk group result = %v", group)
	}
	groupResource := f.call(t, http.MethodGet, strings.TrimPrefix(group["location"].(string), "http://example.com"), nil, http.StatusOK)
	if members, _ := groupResource["members"].([]any); len(members) != 2 {
		t.Fatalf("bulk group members = %v", groupResource)
	}
	duplicate := operations[4].(map[string]any)["response"].(map[string]any)
	if duplicate["scimType"] != "uniqueness" {
		t.Fatalf("duplicate response = %v", duplicate)
	}
	user2 := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Users?filter="+filterQuery(`userName eq "bulk2@example.test"`), nil, http.StatusOK)
	if scimResources(t, user2)[0]["title"] != "Lead" {
		t.Fatalf("bulk PATCH by bulkId = %v", user2)
	}

	stopping := f.call(t, http.MethodPost, "/scim/v2/test-workspace/Bulk", `{"schemas":["urn:ietf:params:scim:api:messages:2.0:BulkRequest"],"failOnErrors":1,"Operations":[
		{"method":"DELETE","path":"/Users/missing"},
		{"method":"POST","path":"/Users","bulkId":"never","data":{"userName":"never@example.test"}}]}`, http.StatusOK)
	if len(stopping["Operations"].([]any)) != 1 {
		t.Fatalf("failOnErrors=1 ran %v", stopping)
	}
	cycle := f.call(t, http.MethodPost, "/scim/v2/test-workspace/Bulk", `{"schemas":["urn:ietf:params:scim:api:messages:2.0:BulkRequest"],"Operations":[
		{"method":"POST","path":"/Groups","bulkId":"a","data":{"displayName":"Cycle A","members":[{"value":"bulkId:b"}]}},
		{"method":"POST","path":"/Groups","bulkId":"b","data":{"displayName":"Cycle B","members":[{"value":"bulkId:a"}]}}]}`, http.StatusOK)
	for _, item := range cycle["Operations"].([]any) {
		if item.(map[string]any)["status"] != "409" {
			t.Fatalf("cycle = %v", cycle)
		}
	}
	operationsJSON := make([]string, scimBulkMaxOperations+1)
	for index := range operationsJSON {
		operationsJSON[index] = `{"method":"DELETE","path":"/Users/x"}`
	}
	requireSCIMError(t, f.call(t, http.MethodPost, "/scim/v2/test-workspace/Bulk", `{"Operations":[`+strings.Join(operationsJSON, ",")+`]}`, http.StatusRequestEntityTooLarge), "413", "")
	requireSCIMError(t, f.call(t, http.MethodPost, "/scim/v2/test-workspace/Bulk", `{"Operations":[{"method":"POST","path":"/Users","bulkId":"big","data":{"userName":"`+strings.Repeat("x", scimBulkMaxPayload)+`"}}]}`, http.StatusRequestEntityTooLarge), "413", "")
	requireSCIMError(t, f.call(t, http.MethodPost, "/scim/v2/test-workspace/Bulk", `{"Operations":[{"method":"POST","path":"/Users","data":{"userName":"a@b.c"}}]}`, http.StatusBadRequest), "400", "invalidSyntax")
	f.token = "wrong"
	requireSCIMError(t, f.call(t, http.MethodPost, "/scim/v2/test-workspace/Bulk", `{"Operations":[]}`, http.StatusUnauthorized), "401", "")
}

func TestSCIMEnterpriseExtensionAndProfile(t *testing.T) {
	f := newSCIMFixture(t)
	created := f.call(t, http.MethodPost, "/scim/v2/test-workspace/Users", `{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User","urn:ietf:params:scim:schemas:extension:enterprise:2.0:User"],
		"userName":"ent@example.test","externalId":"ent-1","title":"Staff Engineer","preferredLanguage":"en-US","locale":"en-US","timezone":"Europe/Berlin",
		"phoneNumbers":[{"value":"+1 555 0100","type":"work","primary":true}],
		"addresses":[{"type":"work","streetAddress":"1 Main St","locality":"Springfield","country":"US"}],
		"urn:ietf:params:scim:schemas:extension:enterprise:2.0:User":{"employeeNumber":"E-42","costCenter":"CC-7","organization":"Acme","division":"R&D","department":"Platform","manager":{"value":"mgr-1","displayName":"Boss"}}}`, http.StatusCreated)
	id := created["id"].(string)
	enterprise, _ := created[scimSchemaEnterpriseUser].(map[string]any)
	if created["title"] != "Staff Engineer" || created["timezone"] != "Europe/Berlin" || enterprise["employeeNumber"] != "E-42" || enterprise["manager"].(map[string]any)["value"] != "mgr-1" || len(created["schemas"].([]any)) != 2 {
		t.Fatalf("created = %v", created)
	}
	data, _ := f.repository.BootstrapFor("test-workspace")
	if settings := data.UserSettings[id]; settings.JobTitle != "Staff Engineer" || settings.Timezone != "Europe/Berlin" {
		t.Fatalf("profile settings = %+v", settings)
	}
	// Microsoft Entra ID shapes: filtered multi-valued paths, extension paths, manager as a bare id.
	patched := f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Users/"+id, `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[
		{"op":"Replace","path":"phoneNumbers[type eq \"mobile\"].value","value":"+1 555 0199"},
		{"op":"Replace","path":"phoneNumbers[type eq \"work\"].value","value":"+1 555 0101"},
		{"op":"Replace","path":"addresses[type eq \"work\"].locality","value":"Shelbyville"},
		{"op":"Replace","path":"urn:ietf:params:scim:schemas:extension:enterprise:2.0:User:department","value":"Infra"},
		{"op":"Add","path":"urn:ietf:params:scim:schemas:extension:enterprise:2.0:User:manager","value":"mgr-2"},
		{"op":"Replace","path":"title","value":"Principal Engineer"},
		{"op":"Remove","path":"locale"}]}`, http.StatusOK)
	phones := patched["phoneNumbers"].([]any)
	enterprise = patched[scimSchemaEnterpriseUser].(map[string]any)
	if len(phones) != 2 || phones[0].(map[string]any)["value"] != "+1 555 0101" || phones[1].(map[string]any)["type"] != "mobile" ||
		patched["addresses"].([]any)[0].(map[string]any)["locality"] != "Shelbyville" || patched["addresses"].([]any)[0].(map[string]any)["streetAddress"] != "1 Main St" ||
		enterprise["department"] != "Infra" || enterprise["costCenter"] != "CC-7" || enterprise["manager"].(map[string]any)["value"] != "mgr-2" || patched["locale"] != nil {
		t.Fatalf("patched = %v", patched)
	}
	// Okta: no-path replace with the extension object; manager removed with an empty value.
	okta := f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Users/"+id, `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[
		{"op":"replace","value":{"urn:ietf:params:scim:schemas:extension:enterprise:2.0:User":{"employeeNumber":"E-43","manager":{"value":""}}}}]}`, http.StatusOK)
	enterprise = okta[scimSchemaEnterpriseUser].(map[string]any)
	if enterprise["employeeNumber"] != "E-43" || enterprise["department"] != nil || enterprise["manager"] != nil {
		t.Fatalf("okta extension replace = %v", okta)
	}
	byDepartment := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Users?filter="+filterQuery(scimSchemaEnterpriseUser+`:employeeNumber eq "E-43" and title co "principal"`), nil, http.StatusOK)
	if byDepartment["totalResults"] != float64(1) {
		t.Fatalf("extension filter = %v", byDepartment)
	}
	data, _ = f.repository.BootstrapFor("test-workspace")
	if data.UserSettings[id].JobTitle != "Principal Engineer" {
		t.Fatalf("job title = %q", data.UserSettings[id].JobTitle)
	}
	// PUT replaces the profile.
	put := f.call(t, http.MethodPut, "/scim/v2/test-workspace/Users/"+id, `{"userName":"ent@example.test","externalId":"ent-1"}`, http.StatusOK)
	if put["title"] != nil || put["phoneNumbers"] != nil || put[scimSchemaEnterpriseUser] != nil || len(put["schemas"].([]any)) != 1 {
		t.Fatalf("PUT profile = %v", put)
	}
	entries := scimAuditEntries(t, f.repository, scimAuditMemberResource)
	var updated *domain.AuditLogEntry
	for index := range entries {
		if entries[index].Action == "updated" && updated == nil {
			updated = &entries[index]
		}
	}
	if updated == nil || !slices.Contains(updated.Metadata["changedFields"].([]string), "title") {
		t.Fatalf("profile update audit = %+v", entries)
	}
	raw, _ := json.Marshal(updated.Metadata)
	if strings.Contains(string(raw), "Principal") || strings.Contains(string(raw), "555") {
		t.Fatalf("audit metadata records attribute values: %s", raw)
	}
}

func TestSCIMDeprovisionRemovesFromWorkspace(t *testing.T) {
	f := newSCIMFixture(t)
	team := f.seed.Teams[0]
	requestJSON[domain.WorkspaceSettings](t, f.handler, http.MethodPatch, "/api/workspace/preferences?workspace=test-workspace", map[string]any{"scimDeprovisionAction": "remove", "scimTeamGroupMapping": map[string]string{team.ID: "Engineering"}}, http.StatusOK)
	requestJSON[any](t, f.handler, http.MethodPatch, "/api/workspace/preferences?workspace=test-workspace", map[string]any{"scimDeprovisionAction": "delete"}, http.StatusBadRequest)
	user := f.call(t, http.MethodPost, "/scim/v2/test-workspace/Users", map[string]any{"userName": "leaver@example.test", "externalId": "leaver"}, http.StatusCreated)
	id := user["id"].(string)
	group := f.call(t, http.MethodPost, "/scim/v2/test-workspace/Groups", map[string]any{"displayName": "Engineering", "members": []map[string]string{{"value": id}}}, http.StatusCreated)
	onTeam := func() bool {
		members, _ := f.repository.ListTeamMembers(t.Context(), f.seed.Workspace.ID)
		return slices.ContainsFunc(members, func(item domain.TeamMember) bool { return item.UserID == id && item.TeamID == team.ID })
	}
	isMember := func() bool {
		members, _ := f.repository.ListMembers(t.Context(), f.seed.Workspace.ID)
		return findWorkspaceMember(members, id) != nil
	}
	if !onTeam() || !isMember() {
		t.Fatal("provisioned user not on the mapped team")
	}
	// active false removes the member but keeps the SCIM resource.
	inactive := f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Users/"+id, `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","path":"active","value":false}]}`, http.StatusOK)
	if inactive["active"] != false || isMember() || onTeam() {
		t.Fatalf("deactivate with remove: active=%v member=%v team=%v", inactive["active"], isMember(), onTeam())
	}
	if got := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Users/"+id, nil, http.StatusOK); got["active"] != false {
		t.Fatalf("removed user resource = %v", got)
	}
	// Re-sending active false, and group pushes, do not bring them back.
	f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Users/"+id, `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","path":"displayName","value":"Leaver"}]}`, http.StatusOK)
	f.call(t, http.MethodPut, "/scim/v2/test-workspace/Groups/"+group["id"].(string), map[string]any{"displayName": "Engineering", "members": []map[string]string{{"value": id}}}, http.StatusOK)
	if isMember() || onTeam() {
		t.Fatal("removed user came back without being reactivated")
	}
	restored := f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Users/"+id, `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","path":"active","value":true}]}`, http.StatusOK)
	if restored["active"] != true || !isMember() || !onTeam() {
		t.Fatalf("reactivate: active=%v member=%v team=%v", restored["active"], isMember(), onTeam())
	}
	// DELETE removes; creating the same externalId again rejoins cleanly.
	f.call(t, http.MethodDelete, "/scim/v2/test-workspace/Users/"+id, nil, http.StatusNoContent)
	f.call(t, http.MethodGet, "/scim/v2/test-workspace/Users/"+id, nil, http.StatusNotFound)
	if isMember() || onTeam() {
		t.Fatal("DELETE with remove kept the membership")
	}
	if account, err := f.repository.SCIMUser(t.Context(), f.seed.Workspace.ID, id); err == nil {
		t.Fatalf("deleted SCIM user still listed: %+v", account)
	}
	again := f.call(t, http.MethodPost, "/scim/v2/test-workspace/Users", map[string]any{"userName": "leaver@example.test", "externalId": "leaver"}, http.StatusCreated)
	if again["id"] != id || !isMember() {
		t.Fatalf("re-provision = %v member=%v", again, isMember())
	}
	if status, _ := workspaceMemberStatus(t, f, id); status != "active" {
		t.Fatalf("re-provisioned status = %q", status)
	}
	actions := scimAuditActions(scimAuditEntries(t, f.repository, scimAuditMemberResource), id)
	for _, want := range []string{"provisioned", "removed", "updated", "restored", "deprovisioned"} {
		if !slices.Contains(actions, want) {
			t.Fatalf("audit actions %v lack %s", actions, want)
		}
	}
	if actions[len(actions)-1] != "restored" {
		t.Fatalf("re-provision audit = %v", actions)
	}
	data, _ := f.repository.BootstrapFor("test-workspace")
	if !slices.ContainsFunc(data.AuditLog, func(entry domain.AuditLogEntry) bool {
		return entry.ResourceType == "scim_settings" && entry.Action == "deprovision_action_changed" && entry.Metadata["after"] == "remove"
	}) {
		t.Fatal("deprovisioning setting change not audited")
	}
	if !slices.ContainsFunc(data.AuditLog, func(entry domain.AuditLogEntry) bool {
		return entry.ResourceType == teamMemberAuditResource && entry.Action == domain.TeamMemberRemoved && entry.Metadata["source"] == "scim" && entry.Metadata["reason"] == teamMemberReasonWorkspaceMemberRemoved
	}) {
		t.Fatal("team membership removal on deprovisioning not audited")
	}
}

func TestSCIMDeprovisionSuspendIsDefault(t *testing.T) {
	f := newSCIMFixture(t)
	user := f.call(t, http.MethodPost, "/scim/v2/test-workspace/Users", map[string]any{"userName": "stay@example.test", "externalId": "stay"}, http.StatusCreated)
	id := user["id"].(string)
	f.call(t, http.MethodDelete, "/scim/v2/test-workspace/Users/"+id, nil, http.StatusNoContent)
	if status, _ := workspaceMemberStatus(t, f, id); status != "suspended" {
		t.Fatalf("default deprovision status = %q", status)
	}
	entries := scimAuditEntries(t, f.repository, scimAuditMemberResource)
	if len(entries) == 0 || entries[0].Action != "deprovisioned" || entries[0].Metadata["deprovisionAction"] != "suspend" || entries[0].BotActor == nil || entries[0].BotActor.Type != "scim" {
		t.Fatalf("deprovision audit = %+v", entries)
	}
}

func TestSCIMAuditsGroupsAndRoles(t *testing.T) {
	f := newSCIMFixture(t)
	user := f.call(t, http.MethodPost, "/scim/v2/test-workspace/Users", map[string]any{"userName": "role@example.test"}, http.StatusCreated)
	id := user["id"].(string)
	admins := f.call(t, http.MethodPost, "/scim/v2/test-workspace/Groups", map[string]any{"displayName": "flow-admins", "members": []map[string]string{{"value": id}}}, http.StatusCreated)
	adminsID := admins["id"].(string)
	f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Groups/"+adminsID, `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","path":"displayName","value":"Former admins"}]}`, http.StatusOK)
	f.call(t, http.MethodDelete, "/scim/v2/test-workspace/Groups/"+adminsID, nil, http.StatusNoContent)
	groupActions := scimAuditActions(scimAuditEntries(t, f.repository, scimAuditGroupResource), adminsID)
	if !slices.Equal(groupActions, []string{"created", "members_changed", "updated", "deleted"}) {
		t.Fatalf("group audit = %v", groupActions)
	}
	roles := []string{}
	for _, entry := range slices.Backward(scimAuditEntries(t, f.repository, scimAuditMemberResource)) {
		if entry.Action == "role_changed" && entry.ResourceID == id {
			roles = append(roles, fmt.Sprint(entry.Metadata["roleBefore"], "->", entry.Metadata["roleAfter"]))
		}
	}
	if !slices.Equal(roles, []string{"member->admin", "admin->member"}) {
		t.Fatalf("role audit = %v", roles)
	}
}

// Findings of the scim2-tester compliance run: attributes read back as
// written (externalId added and removed, displayName removed, the full
// name, emails with primary false, manager $ref), POST .search and SCIM
// errors for unknown paths.
func TestSCIMAttributesReadBackAsWritten(t *testing.T) {
	f := newSCIMFixture(t)
	created := f.call(t, http.MethodPost, "/scim/v2/test-workspace/Users", `{"userName":"rt@example.test","name":{"givenName":"R","familyName":"T","middleName":"M","honorificPrefix":"Dr.","honorificSuffix":"Jr."},
		"emails":[{"value":"rt@example.test","type":"work","primary":false}],"addresses":[{"type":"work","locality":"X","primary":false}]}`, http.StatusCreated)
	id := created["id"].(string)
	if created["displayName"] != nil || created["externalId"] != nil || created["name"].(map[string]any)["honorificSuffix"] != "Jr." ||
		created["emails"].([]any)[0].(map[string]any)["primary"] != false || created["addresses"].([]any)[0].(map[string]any)["primary"] != false {
		t.Fatalf("created = %v", created)
	}
	patch := func(operations string) map[string]any {
		return f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Users/"+id, `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[`+operations+`]}`, http.StatusOK)
	}
	added := patch(`{"op":"add","path":"externalId","value":"rt-ext"},{"op":"add","path":"displayName","value":"RT"},
		{"op":"add","path":"urn:ietf:params:scim:schemas:extension:enterprise:2.0:User:manager","value":{"value":"m","$ref":"https://idp.example/Users/m","displayName":"Boss"}}`)
	if added["externalId"] != "rt-ext" || added["displayName"] != "RT" || added[scimSchemaEnterpriseUser].(map[string]any)["manager"].(map[string]any)["$ref"] != "https://idp.example/Users/m" {
		t.Fatalf("added = %v", added)
	}
	if found := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Users/rt-ext", nil, http.StatusOK); found["id"] != id {
		t.Fatalf("lookup by new externalId = %v", found)
	}
	f.call(t, http.MethodPost, "/scim/v2/test-workspace/Users", map[string]any{"userName": "other-rt@example.test", "externalId": "taken"}, http.StatusCreated)
	requireSCIMError(t, f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Users/"+id, `{"Operations":[{"op":"replace","path":"externalId","value":"taken"}]}`, http.StatusConflict), "409", "uniqueness")
	removed := patch(`{"op":"remove","path":"externalId"},{"op":"remove","path":"displayName"},{"op":"remove","path":"name"}`)
	if removed["externalId"] != nil || removed["displayName"] != nil || removed["name"] != nil || removed["userName"] != "rt@example.test" {
		t.Fatalf("removed = %v", removed)
	}
	members, _ := f.repository.ListMembers(t.Context(), f.seed.Workspace.ID)
	if member := findWorkspaceMember(members, id); member == nil || member.User.DisplayName == "" {
		t.Fatalf("account display name after removal = %+v", member)
	}

	group := f.call(t, http.MethodPost, "/scim/v2/test-workspace/Groups", map[string]any{"displayName": "RT group", "externalId": "g-ext"}, http.StatusCreated)
	cleared := f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Groups/"+group["id"].(string), `{"Operations":[{"op":"remove","path":"externalId"}]}`, http.StatusOK)
	if cleared["externalId"] != nil {
		t.Fatalf("group externalId removal = %v", cleared)
	}

	search := f.call(t, http.MethodPost, "/scim/v2/test-workspace/Users/.search", `{"schemas":["urn:ietf:params:scim:api:messages:2.0:SearchRequest"],"filter":"userName eq \"rt@example.test\"","attributes":["userName"],"count":5}`, http.StatusOK)
	if resources := scimResources(t, search); search["totalResults"] != float64(1) || len(resources[0]) != 3 {
		t.Fatalf("search = %v", search)
	}
	requireSCIMError(t, f.call(t, http.MethodGet, "/scim/v2/test-workspace/Nope", nil, http.StatusNotFound), "404", "")
	requireSCIMError(t, f.call(t, http.MethodPost, "/scim/v2/test-workspace/ServiceProviderConfig", `{}`, http.StatusMethodNotAllowed), "405", "")
}

// Members provisioned by SCIM are locked in Flow while SCIM is on: role,
// name, email and status change only in the identity provider.
func TestSCIMManagedMembersAreLocked(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	server := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer server.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, server.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	token := authRequest[store.SCIMToken](t, admin, http.MethodPost, server.URL+"/api/scim/tokens", map[string]any{"name": "idp"}, "test-workspace", http.StatusCreated)
	scim := func(method, path string, body any, want int) map[string]any {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+token.Secret)
		req.Header.Set("Content-Type", "application/scim+json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		result := map[string]any{}
		_ = json.NewDecoder(response.Body).Decode(&result)
		if response.StatusCode != want {
			t.Fatalf("%s %s = %d %v", method, path, response.StatusCode, result)
		}
		return result
	}
	managed := scim(http.MethodPost, "/scim/v2/test-workspace/Users", map[string]any{"userName": "managed@example.test", "externalId": "m-1"}, http.StatusCreated)
	id := managed["id"].(string)
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, server.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	if member := findWorkspaceMember(bootstrap.Members, id); member == nil || member.ManagedBy != "scim" {
		t.Fatalf("managed marker = %+v", member)
	}
	if member := findWorkspaceMember(bootstrap.Members, bootstrap.Viewer.ID); member == nil || member.ManagedBy != "" {
		t.Fatalf("admin marked managed: %+v", member)
	}
	base := server.URL + "/api/workspaces/test-workspace/members/" + id
	for _, call := range []struct {
		method, url string
		body        any
	}{
		{http.MethodPatch, base, map[string]string{"role": "admin"}},
		{http.MethodPatch, base, map[string]string{"displayName": "Renamed"}},
		{http.MethodPatch, base, map[string]string{"email": "other@example.test"}},
		{http.MethodPost, base + "/suspend", nil},
		{http.MethodDelete, base, nil},
	} {
		refused := authRequest[map[string]any](t, admin, call.method, call.url, call.body, "", http.StatusForbidden)
		if refused["code"] != "SCIM_MANAGED" || !strings.Contains(refused["error"].(string), "identity provider") {
			t.Fatalf("%s %s = %v", call.method, call.url, refused)
		}
	}
	// SCIM-managed team memberships stay protected.
	team := bootstrap.Teams[0]
	authRequest[domain.WorkspaceSettings](t, admin, http.MethodPatch, server.URL+"/api/workspace/preferences", map[string]any{"scimTeamGroupMapping": map[string]string{team.ID: "Eng"}}, "test-workspace", http.StatusOK)
	scim(http.MethodPost, "/scim/v2/test-workspace/Groups", map[string]any{"displayName": "Eng", "members": []map[string]string{{"value": id}}}, http.StatusCreated)
	authRequest[any](t, admin, http.MethodPut, server.URL+"/api/workspaces/test-workspace/teams/"+team.ID+"/members/"+id, map[string]any{"member": false}, "", http.StatusConflict)
	// SCIM itself still suspends.
	scim(http.MethodPatch, "/scim/v2/test-workspace/Users/"+id, map[string]any{"Operations": []map[string]any{{"op": "replace", "path": "active", "value": false}}}, http.StatusOK)
	authRequest[any](t, admin, http.MethodPost, base+"/resume", nil, "", http.StatusForbidden)
	// Turning SCIM off unlocks the member.
	authRequest[domain.WorkspaceSettings](t, admin, http.MethodPatch, server.URL+"/api/workspace/preferences", map[string]any{"scimEnabled": false}, "test-workspace", http.StatusOK)
	bootstrap = authRequest[domain.Bootstrap](t, admin, http.MethodGet, server.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	if member := findWorkspaceMember(bootstrap.Members, id); member == nil || member.ManagedBy != "" {
		t.Fatalf("marker with SCIM off = %+v", member)
	}
	authRequest[any](t, admin, http.MethodPost, base+"/resume", nil, "", http.StatusNoContent)
	authRequest[domain.WorkspaceMember](t, admin, http.MethodPatch, base, map[string]string{"role": "guest"}, "", http.StatusOK)
	// Revoking every token also unlocks, even with SCIM on.
	authRequest[domain.WorkspaceSettings](t, admin, http.MethodPatch, server.URL+"/api/workspace/preferences", map[string]any{"scimEnabled": true}, "test-workspace", http.StatusOK)
	authRequest[any](t, admin, http.MethodPost, base+"/suspend", nil, "", http.StatusForbidden)
	authRequest[any](t, admin, http.MethodDelete, server.URL+"/api/scim/tokens/"+token.ID, nil, "test-workspace", http.StatusNoContent)
	authRequest[any](t, admin, http.MethodPost, base+"/suspend", nil, "", http.StatusNoContent)
	audit := authRequest[[]domain.AuditLogEntry](t, admin, http.MethodGet, server.URL+"/api/workspace/audit-log?resourceType=scim_settings", nil, "test-workspace", http.StatusOK)
	if len(audit) < 2 {
		t.Fatalf("SCIM settings audit = %+v", audit)
	}
}
