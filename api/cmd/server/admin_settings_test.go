package main

import (
	"net/http"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestWebhookCRUDAndValidation(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/webhooks", map[string]any{"name": "Bad", "url": "javascript:alert(1)", "resourceTypes": []string{"issues"}}, http.StatusBadRequest)
	created := requestJSON[domain.Webhook](t, handler, http.MethodPost, "/api/webhooks", map[string]any{"name": "Deploy hook", "url": "https://example.com/flow", "resourceTypes": []string{"issues", "projects"}, "teamIds": []string{}, "enabled": true}, http.StatusCreated)
	if created.ID == "" || created.Name != "Deploy hook" {
		t.Fatalf("invalid webhook: %#v", created)
	}
	updated := requestJSON[domain.Webhook](t, handler, http.MethodPatch, "/api/webhooks/"+created.ID, map[string]any{"enabled": false, "name": "Updated hook"}, http.StatusOK)
	if updated.Enabled || updated.Name != "Updated hook" {
		t.Fatalf("webhook update was not applied: %#v", updated)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/webhooks/"+created.ID, nil, http.StatusNoContent)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Webhooks) != 0 {
		t.Fatalf("deleted webhook survived bootstrap: %#v", bootstrap.Webhooks)
	}
}
