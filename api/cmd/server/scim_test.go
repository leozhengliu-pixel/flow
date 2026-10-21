package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestIdentityRoleMappingSupportsClaimsAndDefaults(t *testing.T) {
	provider := domain.IdentityProvider{RoleClaim: "groups", RoleMapping: map[string]string{"staff": "admin"}, DefaultRole: "member"}
	if got := identityRole(provider, map[string]any{"groups": []any{"staff"}}); got != "admin" {
		t.Fatalf("mapped IdP role = %q", got)
	}
	if got := identityRole(provider, map[string]any{}); got != "member" {
		t.Fatalf("default IdP role = %q", got)
	}
}

func TestSCIMProvisioningSupportsExternalIdentifiersWithoutEmail(t *testing.T) {
	handler, repository := enterpriseTestServer(t)
	seed, _ := repository.BootstrapFor("test-workspace")
	if err := repository.MutateWorkspace(t.Context(), "test-workspace", "scim.test.configured", "workspace", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.SCIMRoleMapping = map[string]string{"employee": "guest"}
		data.WorkspaceSettings.SCIMTeamGroupMapping = map[string]string{seed.Teams[0].ID: "engineering"}
		data.WorkspaceSettings.SCIMDefaultRole = "member"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	token := requestJSON[store.SCIMToken](t, handler, http.MethodPost, "/api/scim/tokens?workspace=test-workspace", map[string]any{"name": "integration"}, http.StatusCreated)
	if token.Secret == "" || token.ID == "" {
		t.Fatalf("SCIM token did not return one-time secret: %#v", token)
	}

	create := scimRequest(t, handler, http.MethodPost, "/scim/v2/test-workspace/Users", token.Secret, map[string]any{
		"externalId":  "employee-10042",
		"userName":    "e10042",
		"displayName": "张三",
		"roles":       []map[string]string{{"value": "employee"}},
	}, http.StatusCreated)
	var created map[string]any
	if err := json.Unmarshal(create, &created); err != nil {
		t.Fatal(err)
	}
	if created["externalId"] != "employee-10042" || created["userName"] != "e10042" || created["active"] != true {
		t.Fatalf("unexpected SCIM user: %#v", created)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("SCIM user id missing")
	}
	teamGroup := scimRequest(t, handler, http.MethodPost, "/scim/v2/test-workspace/Groups", token.Secret, map[string]any{
		"externalId": "idp-engineering", "displayName": "engineering", "members": []map[string]string{{"value": "employee-10042"}},
	}, http.StatusCreated)
	var teamResource map[string]any
	if err := json.Unmarshal(teamGroup, &teamResource); err != nil || teamResource["displayName"] != "engineering" {
		t.Fatalf("SCIM team group=%#v err=%v", teamResource, err)
	}
	teamMembers, err := repository.ListTeamMembers(t.Context(), seed.Workspace.ID)
	if err != nil || !slices.ContainsFunc(teamMembers, func(item domain.TeamMember) bool { return item.TeamID == seed.Teams[0].ID && item.UserID == id }) {
		t.Fatalf("SCIM group did not add Flow team membership: members=%#v err=%v", teamMembers, err)
	}
	teamGroupID := teamResource["id"].(string)
	scimRequest(t, handler, http.MethodPatch, "/scim/v2/test-workspace/Groups/"+teamGroupID, token.Secret, map[string]any{
		"displayName": "engineering", "members": []map[string]string{},
	}, http.StatusOK)
	teamMembers, err = repository.ListTeamMembers(t.Context(), seed.Workspace.ID)
	if err != nil || slices.ContainsFunc(teamMembers, func(item domain.TeamMember) bool { return item.TeamID == seed.Teams[0].ID && item.UserID == id }) {
		t.Fatalf("SCIM group removal did not revoke managed Flow team membership: members=%#v err=%v", teamMembers, err)
	}
	adminGroup := scimRequest(t, handler, http.MethodPost, "/scim/v2/test-workspace/Groups", token.Secret, map[string]any{
		"externalId": "role-admins", "displayName": "flow-admins", "members": []map[string]string{{"value": id}},
	}, http.StatusCreated)
	var adminResource map[string]any
	if err := json.Unmarshal(adminGroup, &adminResource); err != nil || adminResource["displayName"] != "flow-admins" {
		t.Fatalf("SCIM admin group=%#v err=%v", adminResource, err)
	}
	scimRequest(t, handler, http.MethodPost, "/scim/v2/test-workspace/Groups", token.Secret, map[string]any{
		"externalId": "role-owners", "displayName": "flow-owners", "members": []map[string]string{{"value": id}},
	}, http.StatusCreated)
	data, _ := repository.BootstrapFor("test-workspace")
	members, _ := repository.ListMembers(t.Context(), data.Workspace.ID)
	if member := findWorkspaceMember(members, id); member == nil || member.Role != "owner" {
		t.Fatalf("latest SCIM role group did not win: %#v", member)
	}

	list := scimRequest(t, handler, http.MethodGet, "/scim/v2/test-workspace/Users?filter=userName%20eq%20%22e10042%22", token.Secret, nil, http.StatusOK)
	var listed struct {
		TotalResults int              `json:"totalResults"`
		Resources    []map[string]any `json:"Resources"`
	}
	if err := json.Unmarshal(list, &listed); err != nil {
		t.Fatal(err)
	}
	if listed.TotalResults != 1 || len(listed.Resources) != 1 {
		t.Fatalf("SCIM filter/list failed: %#v", listed)
	}

	patchBody := map[string]any{"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"}, "Operations": []map[string]any{{"op": "replace", "path": "active", "value": false}}}
	updated := scimRequest(t, handler, http.MethodPatch, "/scim/v2/test-workspace/Users/"+id, token.Secret, patchBody, http.StatusOK)
	var updatedResource map[string]any
	_ = json.Unmarshal(updated, &updatedResource)
	if updatedResource["active"] != false {
		t.Fatalf("SCIM patch did not deactivate user: %#v", updatedResource)
	}
	get := scimRequest(t, handler, http.MethodGet, "/scim/v2/test-workspace/Users/"+id, token.Secret, nil, http.StatusOK)
	var fetched map[string]any
	_ = json.Unmarshal(get, &fetched)
	if fetched["active"] != false {
		t.Fatalf("SCIM user was not persisted after refresh: %#v", fetched)
	}

	requestJSON[any](t, handler, http.MethodDelete, "/api/scim/tokens/"+token.ID+"?workspace=test-workspace", nil, http.StatusNoContent)
	_ = scimRequest(t, handler, http.MethodGet, "/scim/v2/test-workspace/Users", token.Secret, nil, http.StatusUnauthorized)
}

func TestSCIMRoleGroupsUseWorkspaceSettingsWithoutBrowserSession(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "scim-authenticated.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	workspaceID := repository.Bootstrap().Workspace.ID
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir()})
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, httpServer.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	if err := repository.MutateWorkspace(t.Context(), "test-workspace", "scim.test.authenticated", "workspace", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.SCIMRoleGroups = map[string]string{"owner": "Owners"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	token := authRequest[store.SCIMToken](t, admin, http.MethodPost, httpServer.URL+"/api/scim/tokens?workspace=test-workspace", map[string]any{"name": "scim"}, "", http.StatusCreated)
	resource := scimRequest(t, handler, http.MethodPost, "/scim/v2/test-workspace/Groups", token.Secret, map[string]any{"externalId": "role-owner", "displayName": "Owners"}, http.StatusCreated)
	groupID := map[string]any{}
	if err := json.Unmarshal(resource, &groupID); err != nil {
		t.Fatal(err)
	}
	group, err := repository.SCIMGroup(t.Context(), workspaceID, groupID["id"].(string))
	if err != nil || group.Role != "owner" {
		t.Fatalf("SCIM role group was not mapped for unauthenticated provisioning: group=%#v err=%v", group, err)
	}
}

func scimRequest(t *testing.T, handler http.Handler, method, path, token string, body any, wantStatus int) []byte {
	t.Helper()
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/scim+json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != wantStatus {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, response.Code, wantStatus, response.Body.String())
	}
	return response.Body.Bytes()
}

func findWorkspaceMember(values []domain.WorkspaceMember, userID string) *domain.WorkspaceMember {
	for index := range values {
		if values[index].User.ID == userID {
			return &values[index]
		}
	}
	return nil
}

// scimFixture is a workspace with SCIM enabled and a token.
type scimFixture struct {
	handler    http.Handler
	repository *store.SQLiteStore
	seed       domain.Bootstrap
	token      string
}

func newSCIMFixture(t *testing.T) scimFixture {
	t.Helper()
	handler, repository := enterpriseTestServer(t)
	seed, _ := repository.BootstrapFor("test-workspace")
	token := requestJSON[store.SCIMToken](t, handler, http.MethodPost, "/api/scim/tokens?workspace=test-workspace", map[string]any{"name": "idp"}, http.StatusCreated)
	return scimFixture{handler: handler, repository: repository, seed: seed, token: token.Secret}
}

// call sends a SCIM request (body may be a raw JSON string) and decodes
// the JSON response.
func (f scimFixture) call(t *testing.T, method, path string, body any, wantStatus int) map[string]any {
	t.Helper()
	var payload []byte
	switch typed := body.(type) {
	case nil:
	case string:
		payload = []byte(typed)
	default:
		payload, _ = json.Marshal(typed)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+f.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/scim+json")
	}
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, req)
	if response.Code != wantStatus {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, response.Code, wantStatus, response.Body.String())
	}
	if response.Code == http.StatusNoContent {
		return nil
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/scim+json") {
		t.Fatalf("%s %s content type %q", method, path, contentType)
	}
	result := map[string]any{}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("%s %s: %v body=%s", method, path, err, response.Body.String())
	}
	return result
}

func scimResources(t *testing.T, list map[string]any) []map[string]any {
	t.Helper()
	raw, ok := list["Resources"].([]any)
	if !ok {
		t.Fatalf("ListResponse without Resources: %v", list)
	}
	result := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		result = append(result, item.(map[string]any))
	}
	return result
}

func requireSCIMError(t *testing.T, body map[string]any, status, scimType string) {
	t.Helper()
	schemas, _ := body["schemas"].([]any)
	if len(schemas) != 1 || schemas[0] != scimMessageError || body["status"] != status || (scimType != "" && body["scimType"] != scimType) {
		t.Fatalf("SCIM error = %v, want status %q scimType %q", body, status, scimType)
	}
}

func filterQuery(expression string) string { return url.QueryEscape(expression) }

func workspaceMemberStatus(t *testing.T, f scimFixture, userID string) (string, string) {
	t.Helper()
	members, err := f.repository.ListMembers(t.Context(), f.seed.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	member := findWorkspaceMember(members, userID)
	if member == nil {
		t.Fatalf("user %s is not a workspace member", userID)
	}
	return member.Status, member.Role
}

// Microsoft Entra ID provisioning: the validation probe, user create and
// PATCH shapes (capitalised ops, filtered email path, "False" strings,
// extension attributes), group create with members PATCHes and the
// excludedAttributes=members lookups, then DELETE and re-create.
func TestSCIMEntraProvisioningSequence(t *testing.T) {
	f := newSCIMFixture(t)
	probe := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Users?filter="+filterQuery(`userName eq "1f8b3c1e-5d2a-4a5c-9a4e-0c7b2f9d6e11"`), nil, http.StatusOK)
	if probe["totalResults"] != float64(0) || len(scimResources(t, probe)) != 0 || probe["schemas"].([]any)[0] != scimMessageListResponse {
		t.Fatalf("validation probe = %v", probe)
	}
	groupProbe := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Groups?excludedAttributes=members&filter="+filterQuery(`displayName eq "9d0b2f7e-unknown"`), nil, http.StatusOK)
	if groupProbe["totalResults"] != float64(0) {
		t.Fatalf("group probe = %v", groupProbe)
	}

	createBody := `{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User","urn:ietf:params:scim:schemas:extension:enterprise:2.0:User"],
		"externalId":"0a21f0f2-8d2a-4f8e-bf98-7bd55b1a4a8b","userName":"Test_User_ab6490ee@testuser.com","active":true,
		"displayName":"Test User","emails":[{"primary":true,"type":"work","value":"Test_User_fd0ea19b@testuser.com"}],
		"meta":{"resourceType":"User"},"name":{"formatted":"givenName familyName","familyName":"familyName","givenName":"givenName"},
		"roles":[],"urn:ietf:params:scim:schemas:extension:enterprise:2.0:User":{"employeeNumber":"123","department":"Eng"}}`
	created := f.call(t, http.MethodPost, "/scim/v2/test-workspace/Users", createBody, http.StatusCreated)
	userID, _ := created["id"].(string)
	name, _ := created["name"].(map[string]any)
	emails, _ := created["emails"].([]any)
	if userID == "" || created["externalId"] != "0a21f0f2-8d2a-4f8e-bf98-7bd55b1a4a8b" || name["givenName"] != "givenName" || name["familyName"] != "familyName" || len(emails) != 1 || emails[0].(map[string]any)["value"] != "test_user_fd0ea19b@testuser.com" {
		t.Fatalf("created user = %v", created)
	}
	if meta, _ := created["meta"].(map[string]any); meta["resourceType"] != "User" || !strings.HasSuffix(meta["location"].(string), "/scim/v2/test-workspace/Users/"+userID) || meta["created"] == nil {
		t.Fatalf("user meta = %v", created["meta"])
	}
	requireSCIMError(t, f.call(t, http.MethodPost, "/scim/v2/test-workspace/Users", createBody, http.StatusConflict), "409", "uniqueness")

	byExternal := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Users?filter="+filterQuery(`externalId eq "0a21f0f2-8d2a-4f8e-bf98-7bd55b1a4a8b"`), nil, http.StatusOK)
	if byExternal["totalResults"] != float64(1) || scimResources(t, byExternal)[0]["id"] != userID {
		t.Fatalf("externalId lookup = %v", byExternal)
	}

	patched := f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Users/"+userID, `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[
		{"op":"Replace","path":"emails[type eq \"work\"].value","value":"updatedEmail@microsoft.com"},
		{"op":"Replace","path":"name.familyName","value":"updatedFamilyName"},
		{"op":"Add","path":"urn:ietf:params:scim:schemas:extension:enterprise:2.0:User:department","value":"Research"},
		{"op":"Add","path":"title","value":"Engineer"}]}`, http.StatusOK)
	if patched["emails"].([]any)[0].(map[string]any)["value"] != "updatedemail@microsoft.com" || patched["name"].(map[string]any)["familyName"] != "updatedFamilyName" || patched["name"].(map[string]any)["givenName"] != "givenName" {
		t.Fatalf("Entra attribute PATCH = %v", patched)
	}
	disabled := f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Users/"+userID, `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"Replace","path":"active","value":"False"}]}`, http.StatusOK)
	if disabled["active"] != false {
		t.Fatalf("active \"False\" = %v", disabled)
	}
	if status, _ := workspaceMemberStatus(t, f, userID); status != "suspended" {
		t.Fatalf("deactivated member status = %q", status)
	}
	enabled := f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Users/"+userID, `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","value":{"active":true,"displayName":"Renamed User","name.givenName":"Given"}}]}`, http.StatusOK)
	if enabled["active"] != true || enabled["displayName"] != "Renamed User" || enabled["name"].(map[string]any)["givenName"] != "Given" {
		t.Fatalf("no-path replace = %v", enabled)
	}
	if status, _ := workspaceMemberStatus(t, f, userID); status != "active" {
		t.Fatalf("reactivated member status = %q", status)
	}
	requireSCIMError(t, f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Users/"+userID, `{"Operations":[{"op":"Replace","path":"active","value":"maybe"}]}`, http.StatusBadRequest), "400", "invalidValue")
	requireSCIMError(t, f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Users/"+userID, `{"Operations":[{"op":"move","path":"active","value":true}]}`, http.StatusBadRequest), "400", "invalidSyntax")

	group := f.call(t, http.MethodPost, "/scim/v2/test-workspace/Groups", `{"schemas":["urn:ietf:params:scim:schemas:core:2.0:Group"],"externalId":"8aa1a0c0-c4c3-4bc0-b4a5-2ef676900159","displayName":"Entra Group","members":[]}`, http.StatusCreated)
	groupID := group["id"].(string)
	requireSCIMError(t, f.call(t, http.MethodPost, "/scim/v2/test-workspace/Groups", `{"displayName":"entra group"}`, http.StatusConflict), "409", "uniqueness")
	found := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Groups?excludedAttributes=members&filter="+filterQuery(`displayName eq "Entra Group"`), nil, http.StatusOK)
	if resources := scimResources(t, found); found["totalResults"] != float64(1) || resources[0]["id"] != groupID || resources[0]["members"] != nil || resources[0]["externalId"] == nil {
		t.Fatalf("group lookup = %v", found)
	}
	f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Groups/"+groupID, `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"Add","path":"members","value":[{"value":"`+userID+`"}]}]}`, http.StatusOK)
	withMember := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Groups?filter="+filterQuery(`members[value eq "`+userID+`"]`), nil, http.StatusOK)
	if withMember["totalResults"] != float64(1) {
		t.Fatalf("members filter = %v", withMember)
	}
	// Adding the same member again is idempotent.
	again := f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Groups/"+groupID, `{"Operations":[{"op":"Add","path":"members","value":[{"value":"`+userID+`"}]}]}`, http.StatusOK)
	if members, _ := again["members"].([]any); len(members) != 1 || members[0].(map[string]any)["value"] != userID {
		t.Fatalf("repeated add = %v", again)
	}
	requireSCIMError(t, f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Groups/"+groupID, `{"Operations":[{"op":"Add","path":"members","value":[{"value":"usr_missing"}]}]}`, http.StatusBadRequest), "400", "invalidValue")
	removed := f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Groups/"+groupID, `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"Remove","path":"members","value":[{"value":"`+userID+`"}]}]}`, http.StatusOK)
	if members, _ := removed["members"].([]any); len(members) != 0 {
		t.Fatalf("member remove = %v", removed)
	}
	renamed := f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Groups/"+groupID, `{"Operations":[{"op":"Replace","path":"displayName","value":"Entra Group Renamed"}]}`, http.StatusOK)
	if renamed["displayName"] != "Entra Group Renamed" {
		t.Fatalf("group rename = %v", renamed)
	}
	single := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Groups/"+groupID+"?excludedAttributes=members", nil, http.StatusOK)
	if _, has := single["members"]; has || single["displayName"] != "Entra Group Renamed" {
		t.Fatalf("group get without members = %v", single)
	}

	f.call(t, http.MethodDelete, "/scim/v2/test-workspace/Users/"+userID, nil, http.StatusNoContent)
	requireSCIMError(t, f.call(t, http.MethodGet, "/scim/v2/test-workspace/Users/"+userID, nil, http.StatusNotFound), "404", "")
	if status, _ := workspaceMemberStatus(t, f, userID); status != "suspended" {
		t.Fatalf("deleted member status = %q (accounts are suspended, not removed)", status)
	}
	restored := f.call(t, http.MethodPost, "/scim/v2/test-workspace/Users", createBody, http.StatusCreated)
	if restored["id"] != userID || restored["active"] != true {
		t.Fatalf("re-created user = %v, want the same account %s restored", restored, userID)
	}
	f.call(t, http.MethodDelete, "/scim/v2/test-workspace/Groups/"+groupID, nil, http.StatusNoContent)
	f.call(t, http.MethodGet, "/scim/v2/test-workspace/Groups/"+groupID, nil, http.StatusNotFound)
}

// Okta provisioning: userName lookups with startIndex/count, create without
// externalId, PUT, PATCH active false, and group push to a mapped team with
// member add/remove and the no-path rename Okta sends.
func TestSCIMOktaProvisioningSequence(t *testing.T) {
	f := newSCIMFixture(t)
	teamID := f.seed.Teams[0].ID
	if err := f.repository.MutateWorkspace(t.Context(), "test-workspace", "scim.test.team", "workspace", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.SCIMTeamGroupMapping = map[string]string{teamID: "Okta Engineering"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	lookup := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Users?filter="+filterQuery(`userName eq "okta.user@example.com"`)+"&startIndex=1&count=100", nil, http.StatusOK)
	if lookup["totalResults"] != float64(0) || lookup["startIndex"] != float64(1) || lookup["itemsPerPage"] != float64(0) {
		t.Fatalf("Okta lookup = %v", lookup)
	}
	created := f.call(t, http.MethodPost, "/scim/v2/test-workspace/Users", `{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"okta.user@example.com",
		"name":{"givenName":"Okta","familyName":"User"},"emails":[{"primary":true,"value":"okta.user@example.com","type":"work"}],
		"displayName":"Okta User","locale":"en-US","groups":[],"password":"ignored","active":true}`, http.StatusCreated)
	userID := created["id"].(string)
	if _, has := created["externalId"]; has || created["userName"] != "okta.user@example.com" {
		t.Fatalf("Okta create = %v (no externalId was sent)", created)
	}
	if status, role := workspaceMemberStatus(t, f, userID); status != "active" || role != "member" {
		t.Fatalf("provisioned member = %s/%s", status, role)
	}
	// userName is compared case-insensitively.
	found := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Users?filter="+filterQuery(`userName eq "OKTA.USER@example.com"`)+"&startIndex=1&count=100", nil, http.StatusOK)
	if found["totalResults"] != float64(1) || scimResources(t, found)[0]["id"] != userID {
		t.Fatalf("case-insensitive userName lookup = %v", found)
	}
	put := f.call(t, http.MethodPut, "/scim/v2/test-workspace/Users/"+userID, `{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"id":"`+userID+`","userName":"okta.user@example.com",
		"name":{"givenName":"Okta","familyName":"Renamed"},"emails":[{"primary":true,"value":"okta.user@example.com","type":"work"}],"displayName":"Okta Renamed","active":true}`, http.StatusOK)
	if put["name"].(map[string]any)["familyName"] != "Renamed" || put["displayName"] != "Okta Renamed" {
		t.Fatalf("Okta PUT = %v", put)
	}
	other := f.call(t, http.MethodPost, "/scim/v2/test-workspace/Users", `{"userName":"other@example.com","emails":[{"value":"other@example.com","primary":true}]}`, http.StatusCreated)
	requireSCIMError(t, f.call(t, http.MethodPut, "/scim/v2/test-workspace/Users/"+other["id"].(string), `{"userName":"okta.user@example.com"}`, http.StatusConflict), "409", "uniqueness")

	group := f.call(t, http.MethodPost, "/scim/v2/test-workspace/Groups", `{"schemas":["urn:ietf:params:scim:schemas:core:2.0:Group"],"displayName":"Okta Engineering","members":[{"value":"`+userID+`","display":"okta.user@example.com"}]}`, http.StatusCreated)
	groupID := group["id"].(string)
	onTeam := func() bool {
		members, err := f.repository.ListTeamMembers(t.Context(), f.seed.Workspace.ID)
		if err != nil {
			t.Fatal(err)
		}
		return slices.ContainsFunc(members, func(item domain.TeamMember) bool { return item.TeamID == teamID && item.UserID == userID })
	}
	if !onTeam() {
		t.Fatal("group push did not add the team membership")
	}
	f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Groups/"+groupID, `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"remove","path":"members[value eq \"`+userID+`\"]"}]}`, http.StatusOK)
	if onTeam() {
		t.Fatal("member filter remove did not remove the SCIM team membership")
	}
	f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Groups/"+groupID, `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"add","path":"members","value":[{"value":"`+userID+`","display":"okta.user@example.com"}]}]}`, http.StatusOK)
	if !onTeam() {
		t.Fatal("member add did not restore the team membership")
	}
	renamed := f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Groups/"+groupID, `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","value":{"id":"`+groupID+`","displayName":"Okta Engineering"}}]}`, http.StatusOK)
	if renamed["displayName"] != "Okta Engineering" || len(renamed["members"].([]any)) != 1 {
		t.Fatalf("Okta no-path group replace = %v", renamed)
	}

	deactivated := f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Users/"+userID, `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","value":{"active":false}}]}`, http.StatusOK)
	if deactivated["active"] != false {
		t.Fatalf("Okta deactivate = %v", deactivated)
	}
	if status, _ := workspaceMemberStatus(t, f, userID); status != "suspended" {
		t.Fatalf("Okta deactivated member status = %q", status)
	}

	// Okta lists groups (role groups, linked groups and teams to import) in pages.
	f.call(t, http.MethodPost, "/scim/v2/test-workspace/Groups", `{"displayName":"flow-admins"}`, http.StatusCreated)
	all := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Groups?startIndex=1&count=100", nil, http.StatusOK)
	total := int(all["totalResults"].(float64))
	if total < 2 || len(scimResources(t, all)) != total {
		t.Fatalf("group list = %v", all)
	}
	for _, resource := range scimResources(t, all) {
		if resource["id"] == teamID {
			t.Fatalf("a team linked to a SCIM group is listed twice: %v", all)
		}
	}
	page := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Groups?startIndex=2&count=1", nil, http.StatusOK)
	if page["totalResults"] != float64(total) || page["startIndex"] != float64(2) || page["itemsPerPage"] != float64(1) || scimResources(t, page)[0]["id"] != scimResources(t, all)[1]["id"] {
		t.Fatalf("group page = %v", page)
	}
}

// An unlinked team is listed as a group so a directory can find it by name;
// the first write links it through a SCIM group whose externalId is the team.
func TestSCIMTeamsAreLinkedOnFirstGroupWrite(t *testing.T) {
	f := newSCIMFixture(t)
	team := f.seed.Teams[0]
	user := f.call(t, http.MethodPost, "/scim/v2/test-workspace/Users", map[string]any{"userName": "linked@example.com"}, http.StatusCreated)
	userID := user["id"].(string)
	found := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Groups?excludedAttributes=members&filter="+filterQuery(`displayName eq "`+team.Name+`"`), nil, http.StatusOK)
	if found["totalResults"] != float64(1) || scimResources(t, found)[0]["id"] != team.ID {
		t.Fatalf("team lookup = %v", found)
	}
	teamResource := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Groups/"+team.ID, nil, http.StatusOK)
	if teamResource["meta"].(map[string]any)["resourceType"] != "Group" {
		t.Fatalf("team resource = %v", teamResource)
	}
	f.call(t, http.MethodDelete, "/scim/v2/test-workspace/Groups/"+team.ID, nil, http.StatusNotFound)
	linked := f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Groups/"+team.ID, map[string]any{"Operations": []map[string]any{{"op": "add", "path": "members", "value": []map[string]string{{"value": userID}}}}}, http.StatusOK)
	if linked["externalId"] != team.ID || len(linked["members"].([]any)) != 1 {
		t.Fatalf("linked group = %v", linked)
	}
	members, _ := f.repository.ListTeamMembers(t.Context(), f.seed.Workspace.ID)
	index := slices.IndexFunc(members, func(item domain.TeamMember) bool { return item.TeamID == team.ID && item.UserID == userID })
	if index < 0 || members[index].Origin != domain.TeamMemberOriginSCIM {
		t.Fatalf("linked team membership = %v", members)
	}
	again := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Groups?filter="+filterQuery(`displayName eq "`+team.Name+`"`), nil, http.StatusOK)
	if again["totalResults"] != float64(1) || scimResources(t, again)[0]["id"] != linked["id"] {
		t.Fatalf("after linking = %v", again)
	}
	// Unlinking (deleting the group) keeps the member on the team as an
	// ordinary membership.
	f.call(t, http.MethodDelete, "/scim/v2/test-workspace/Groups/"+team.ID, nil, http.StatusNoContent)
	members, _ = f.repository.ListTeamMembers(t.Context(), f.seed.Workspace.ID)
	index = slices.IndexFunc(members, func(item domain.TeamMember) bool { return item.TeamID == team.ID && item.UserID == userID })
	if index < 0 || members[index].ManagedSource == "scim" {
		t.Fatalf("unlinked team membership = %v", members)
	}
}

func TestSCIMUserPagingFiltersAndProjection(t *testing.T) {
	f := newSCIMFixture(t)
	for index := 1; index <= 5; index++ {
		f.call(t, http.MethodPost, "/scim/v2/test-workspace/Users", map[string]any{"userName": fmt.Sprintf("pager%d@example.com", index), "displayName": fmt.Sprintf("Pager %d", index), "active": index != 3}, http.StatusCreated)
	}
	page := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Users?startIndex=2&count=2", nil, http.StatusOK)
	resources := scimResources(t, page)
	if page["totalResults"] != float64(5) || page["itemsPerPage"] != float64(2) || resources[0]["userName"] != "pager2@example.com" || resources[1]["userName"] != "pager3@example.com" {
		t.Fatalf("unfiltered page = %v", page)
	}
	scanned := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Users?filter="+filterQuery(`displayName sw "pager" and active eq true`)+"&startIndex=2&count=2", nil, http.StatusOK)
	resources = scimResources(t, scanned)
	if scanned["totalResults"] != float64(4) || len(resources) != 2 || resources[0]["userName"] != "pager2@example.com" || resources[1]["userName"] != "pager4@example.com" {
		t.Fatalf("filtered page = %v", scanned)
	}
	counted := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Users?count=0", nil, http.StatusOK)
	if counted["totalResults"] != float64(5) || len(scimResources(t, counted)) != 0 {
		t.Fatalf("count=0 = %v", counted)
	}
	either := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Users?filter="+filterQuery(`userName eq "pager1@example.com" or emails[type eq "work"].value eq "pager5@example.com"`), nil, http.StatusOK)
	if either["totalResults"] != float64(2) {
		t.Fatalf("or filter = %v", either)
	}
	projected := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Users?attributes=userName,name.givenName&filter="+filterQuery(`userName eq "pager1@example.com"`), nil, http.StatusOK)
	resource := scimResources(t, projected)[0]
	if resource["userName"] != "pager1@example.com" || resource["id"] == nil || resource["schemas"] == nil || resource["emails"] != nil || resource["meta"] != nil {
		t.Fatalf("attributes projection = %v", resource)
	}
	excluded := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Users?excludedAttributes=emails,meta&filter="+filterQuery(`userName eq "pager1@example.com"`), nil, http.StatusOK)
	if resource := scimResources(t, excluded)[0]; resource["emails"] != nil || resource["meta"] != nil || resource["userName"] == nil {
		t.Fatalf("excludedAttributes projection = %v", resource)
	}
	for _, bad := range []string{`userName eq`, `userName xx "a"`, `(userName eq "a"`, `active gt true`} {
		requireSCIMError(t, f.call(t, http.MethodGet, "/scim/v2/test-workspace/Users?filter="+filterQuery(bad), nil, http.StatusBadRequest), "400", "invalidFilter")
		requireSCIMError(t, f.call(t, http.MethodGet, "/scim/v2/test-workspace/Groups?filter="+filterQuery(bad), nil, http.StatusBadRequest), "400", "invalidFilter")
	}
}

func TestSCIMDiscoveryEndpoints(t *testing.T) {
	f := newSCIMFixture(t)
	config := f.call(t, http.MethodGet, "/scim/v2/test-workspace/ServiceProviderConfig", nil, http.StatusOK)
	schemes := config["authenticationSchemes"].([]any)
	if config["filter"].(map[string]any)["supported"] != true || config["bulk"].(map[string]any)["supported"] != false || config["etag"].(map[string]any)["supported"] != false || schemes[0].(map[string]any)["type"] != "oauthbearertoken" || config["meta"].(map[string]any)["resourceType"] != "ServiceProviderConfig" {
		t.Fatalf("ServiceProviderConfig = %v", config)
	}
	types := f.call(t, http.MethodGet, "/scim/v2/test-workspace/ResourceTypes", nil, http.StatusOK)
	if types["totalResults"] != float64(2) {
		t.Fatalf("ResourceTypes = %v", types)
	}
	groupType := f.call(t, http.MethodGet, "/scim/v2/test-workspace/ResourceTypes/Group", nil, http.StatusOK)
	if groupType["endpoint"] != "/Groups" || groupType["schema"] != scimSchemaGroup {
		t.Fatalf("Group resource type = %v", groupType)
	}
	schemas := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Schemas", nil, http.StatusOK)
	if schemas["totalResults"] != float64(2) {
		t.Fatalf("Schemas = %v", schemas)
	}
	user := f.call(t, http.MethodGet, "/scim/v2/test-workspace/Schemas/"+scimSchemaUser, nil, http.StatusOK)
	attributes := map[string]bool{}
	for _, item := range user["attributes"].([]any) {
		attributes[item.(map[string]any)["name"].(string)] = true
	}
	for _, name := range []string{"userName", "name", "displayName", "active", "emails"} {
		if !attributes[name] {
			t.Fatalf("User schema lacks %s: %v", name, user)
		}
	}
	requireSCIMError(t, f.call(t, http.MethodGet, "/scim/v2/test-workspace/Schemas/urn:example:unknown", nil, http.StatusNotFound), "404", "")
	f.token = "wrong"
	requireSCIMError(t, f.call(t, http.MethodGet, "/scim/v2/test-workspace/Schemas", nil, http.StatusUnauthorized), "401", "")
}

// Linking an existing account by email keeps its workspace role, and
// profile PATCHes do not reset a role the IdP assigned.
func TestSCIMLinksExistingAccountsAndKeepsRoles(t *testing.T) {
	f := newSCIMFixture(t)
	viewer := f.seed.Viewer
	_, roleBefore := workspaceMemberStatus(t, f, viewer.ID)
	linked := f.call(t, http.MethodPost, "/scim/v2/test-workspace/Users", map[string]any{"userName": viewer.Email, "externalId": "emp-1", "displayName": viewer.DisplayName}, http.StatusCreated)
	if linked["id"] != viewer.ID {
		t.Fatalf("existing account was not linked: %v", linked)
	}
	if _, role := workspaceMemberStatus(t, f, viewer.ID); role != roleBefore {
		t.Fatalf("linking changed role %s -> %s", roleBefore, role)
	}
	admin := f.call(t, http.MethodPost, "/scim/v2/test-workspace/Users", map[string]any{"userName": "role@example.com", "roles": []map[string]string{{"value": "admin"}}}, http.StatusCreated)
	adminID := admin["id"].(string)
	f.call(t, http.MethodPatch, "/scim/v2/test-workspace/Users/"+adminID, map[string]any{"Operations": []map[string]any{{"op": "replace", "path": "displayName", "value": "Role Holder"}}}, http.StatusOK)
	if _, role := workspaceMemberStatus(t, f, adminID); role != "admin" {
		t.Fatalf("profile PATCH reset role to %s", role)
	}
}
