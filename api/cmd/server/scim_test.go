package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
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
		data.WorkspaceSettings.Plan = "enterprise"
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
		"externalId": "role-admins", "displayName": "linear-admins", "members": []map[string]string{{"value": id}},
	}, http.StatusCreated)
	var adminResource map[string]any
	if err := json.Unmarshal(adminGroup, &adminResource); err != nil || adminResource["displayName"] != "linear-admins" {
		t.Fatalf("SCIM admin group=%#v err=%v", adminResource, err)
	}
	scimRequest(t, handler, http.MethodPost, "/scim/v2/test-workspace/Groups", token.Secret, map[string]any{
		"externalId": "role-owners", "displayName": "linear-owners", "members": []map[string]string{{"value": id}},
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
