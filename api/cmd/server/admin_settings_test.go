package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

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

func TestWebhookFailureEventsPersistedAndListed(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "webhook-failures.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	service := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	handler := newHandler(service)

	failures := make(chan struct{}, 4)
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream unavailable"))
		select {
		case failures <- struct{}{}:
		default:
		}
	}))
	defer destination.Close()

	seed, _ := repository.BootstrapFor("test-workspace")
	created := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/webhooks?workspace=test-workspace", map[string]any{
		"name": "failing hook", "url": destination.URL, "resourceTypes": []string{"issues"}, "teamIds": []string{}, "enabled": true,
	}, http.StatusCreated)
	webhookID, _ := created["id"].(string)
	if webhookID == "" {
		t.Fatalf("missing webhook id: %#v", created)
	}

	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues?workspace=test-workspace", map[string]any{
		"title": "trigger delivery", "teamId": seed.Teams[0].ID,
	}, http.StatusCreated)
	if issue.ID == "" {
		t.Fatal("issue was not created")
	}

	deadline := time.After(3 * time.Second)
	select {
	case <-failures:
	case <-deadline:
		t.Fatal("timed out waiting for failing webhook delivery")
	}

	var listed []domain.WebhookFailureEvent
	deadline = time.After(3 * time.Second)
	for {
		listed = requestJSON[[]domain.WebhookFailureEvent](t, handler, http.MethodGet, "/api/webhooks/"+webhookID+"/failures?workspace=test-workspace", nil, http.StatusOK)
		if len(listed) > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for persisted webhook failure")
		case <-time.After(50 * time.Millisecond):
		}
	}
	if listed[0].WebhookID != webhookID {
		t.Fatalf("unexpected webhook id: %#v", listed[0])
	}
	if listed[0].ExecutionID == "" || listed[0].URL != destination.URL {
		t.Fatalf("incomplete failure event: %#v", listed[0])
	}
	if listed[0].HTTPStatus == nil || *listed[0].HTTPStatus != http.StatusBadGateway {
		t.Fatalf("http status = %#v", listed[0].HTTPStatus)
	}
	if listed[0].ResponseOrError != "upstream unavailable" {
		t.Fatalf("responseOrError = %q", listed[0].ResponseOrError)
	}

	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap?workspace=test-workspace", nil, http.StatusOK)
	if len(bootstrap.WebhookFailureEvents) != 0 {
		t.Fatalf("bootstrap should omit failure events, got %#v", bootstrap.WebhookFailureEvents)
	}

	requestJSON[any](t, handler, http.MethodDelete, "/api/webhooks/"+webhookID+"?workspace=test-workspace", nil, http.StatusNoContent)
	requestJSON[any](t, handler, http.MethodGet, "/api/webhooks/"+webhookID+"/failures?workspace=test-workspace", nil, http.StatusNotFound)
}
