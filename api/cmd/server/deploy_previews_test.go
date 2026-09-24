package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestDeployPreviewsFromGitHubDeploymentStatusAndAPI(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	requestJSON[domain.IntegrationConnection](t, handler, http.MethodPut, "/api/integrations/github?workspace=test-workspace", map[string]any{"name": "acme", "config": map[string]string{"organization": "acme", "webhookSecret": "secret"}}, http.StatusOK)
	seed := repository.Bootstrap()
	send := func(event, delivery string, payload []byte) *httptest.ResponseRecorder {
		mac := hmac.New(sha256.New, []byte("secret"))
		_, _ = mac.Write(payload)
		req := httptest.NewRequest(http.MethodPost, "/api/integrations/github/webhook?workspace=test-workspace", bytes.NewReader(payload))
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		req.Header.Set("X-GitHub-Delivery", delivery)
		req.Header.Set("X-GitHub-Event", event)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("%s webhook status=%d body=%s", event, rec.Code, rec.Body.String())
		}
		return rec
	}
	send("pull_request", "pr-1", []byte(fmt.Sprintf(`{"action":"opened","number":7,"pull_request":{"id":707,"title":"Fix %s","html_url":"https://github.com/acme/store/pull/7","state":"open","user":{"login":"ada"},"base":{"ref":"main"},"head":{"ref":"ada/fix","sha":"abc"}},"repository":{"full_name":"acme/store"}}`, seed.Issues[0].Identifier)))
	deployment := func(state, delivery string) {
		send("deployment_status", delivery, []byte(`{"deployment":{"environment":"Preview","ref":"ada/fix","sha":"abc"},"deployment_status":{"state":"`+state+`","environment_url":"https://store-git-ada-fix.vercel.app","log_url":"https://vercel.com/acme/store/logs"},"repository":{"full_name":"acme/store"}}`))
	}
	deployment("in_progress", "deploy-1")
	review := repository.Bootstrap().Reviews[0]
	if len(review.Previews) != 1 || review.Previews[0].State != "building" {
		t.Fatalf("expected a building preview, got %#v", review.Previews)
	}
	deployment("success", "deploy-2")
	bootstrap := repository.Bootstrap()
	review = bootstrap.Reviews[0]
	if len(review.Previews) != 1 || review.Previews[0].State != "ready" || review.Previews[0].URL != "https://store-git-ada-fix.vercel.app" {
		t.Fatalf("expected one ready preview, got %#v", review.Previews)
	}
	if !slices.ContainsFunc(bootstrap.Activities[seed.Issues[0].ID], func(item domain.ActivityEvent) bool { return item.Type == "issue.preview_ready" }) {
		t.Fatalf("expected a preview_ready activity on the linked issue")
	}

	// Other CI reports through the API; environments are separate previews.
	updated := requestJSON[domain.CodeReview](t, handler, http.MethodPut, "/api/reviews/"+review.ID+"/previews", map[string]any{"environment": "Storybook", "url": "https://storybook.example.com", "state": "ready", "provider": "netlify"}, http.StatusOK)
	if len(updated.Previews) != 2 {
		t.Fatalf("expected two previews, got %#v", updated.Previews)
	}
	requestJSON[map[string]any](t, handler, http.MethodPut, "/api/reviews/"+review.ID+"/previews", map[string]any{"environment": "Bad", "url": "javascript:alert(1)", "state": "ready"}, http.StatusBadRequest)
	requestJSON[map[string]any](t, handler, http.MethodPut, "/api/reviews/"+review.ID+"/previews", map[string]any{"environment": "Bad", "url": "https://x.dev", "state": "exploded"}, http.StatusBadRequest)
}
