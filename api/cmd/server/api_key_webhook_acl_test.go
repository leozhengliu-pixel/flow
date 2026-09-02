package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestAPIKeyGranularScopesAndRotation(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "api-key-acl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap?workspace=test-workspace", nil, http.StatusOK)
	teamID := bootstrap.Teams[0].ID
	created := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/api-keys?workspace=test-workspace", map[string]any{
		"name": "issue importer", "scopes": []string{"create_issues"}, "teamIds": []string{teamID}, "teamRestriction": "selected",
	}, http.StatusCreated)
	key, ok := created["key"].(map[string]any)
	if !ok || key["teamRestriction"] != "selected" {
		t.Fatalf("created key omitted selected restriction: %#v", created)
	}
	secret, ok := created["secret"].(string)
	if !ok || secret == "" {
		t.Fatalf("created key did not return one-time secret: %#v", created)
	}
	rotated := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/api-keys/"+key["id"].(string)+"/rotate-secret?workspace=test-workspace", nil, http.StatusOK)
	if rotated["secret"] == secret {
		t.Fatal("rotating an API key reused its old secret")
	}
	rotatedKey := rotated["key"].(map[string]any)
	if rotatedKey["teamRestriction"] != "selected" {
		t.Fatalf("rotation changed key restriction: %#v", rotatedKey)
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/api-keys/"+key["id"].(string)+"/rotate-secret?workspace=test-workspace", nil, http.StatusOK)
}

func TestAPIKeyScopeAuthorization(t *testing.T) {
	issueCreate := httptest.NewRequest(http.MethodPost, "/api/issues", nil)
	if apiKeyAllowsRequest(issueCreate, domain.APIKey{Scopes: []string{"read"}}) {
		t.Fatal("read-only key can create an issue")
	}
	if !apiKeyAllowsRequest(issueCreate, domain.APIKey{Scopes: []string{"create_issues"}}) {
		t.Fatal("create_issues key cannot create an issue")
	}
	update := httptest.NewRequest(http.MethodPatch, "/api/issues/issue_1", nil)
	if apiKeyAllowsRequest(update, domain.APIKey{Scopes: []string{"create_issues"}}) {
		t.Fatal("create_issues key can update an issue")
	}
	adminRead := httptest.NewRequest(http.MethodGet, "/api/webhooks", nil)
	if apiKeyAllowsRequest(adminRead, domain.APIKey{Scopes: []string{"read"}}) {
		t.Fatal("read-only key can list admin webhooks")
	}
	if !apiKeyAllowsRequest(adminRead, domain.APIKey{Scopes: []string{"admin"}}) {
		t.Fatal("admin key cannot list webhooks")
	}
}

func TestWebhookSecretLifecycleAndResourceTypes(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "webhook-acl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	created := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/webhooks?workspace=test-workspace", map[string]any{
		"name": "all events", "url": "https://example.test/hook", "resourceTypes": []string{"issues", "comments", "attachments", "documents", "reactions", "projects", "project_updates", "cycles", "labels", "users", "issue_sla", "initiatives", "customers", "customer_requests", "releases", "milestones", "relations"},
	}, http.StatusCreated)
	item := created
	if item["secret"] == "" {
		t.Fatal("webhook create did not return one-time secret")
	}
	id := item["id"].(string)
	rotated := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/webhooks/"+id+"/rotate-secret?workspace=test-workspace", nil, http.StatusOK)
	if rotated["secret"] == "" || rotated["secret"] == item["secret"] {
		t.Fatal("webhook secret rotation did not issue a new secret")
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/webhooks/"+id+"/revoke-secret?workspace=test-workspace", nil, http.StatusNoContent)
	items := requestJSON[[]domain.Webhook](t, handler, http.MethodGet, "/api/webhooks?workspace=test-workspace", nil, http.StatusOK)
	if len(items) != 1 || items[0].SecretPrefix != "" {
		t.Fatalf("webhook secret material leaked after revocation: %#v", items)
	}
}
