package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	if err := repository.MutateWorkspace(t.Context(), "test-workspace", "scim.test.configured", "workspace", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.SCIMRoleMapping = map[string]string{"employee": "guest"}
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
