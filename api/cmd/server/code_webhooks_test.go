package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestGitHubPullRequestWebhookCreatesReviewAndInboxNotification(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	requestJSON[domain.IntegrationConnection](t, handler, http.MethodPut, "/api/integrations/github?workspace=test-workspace", map[string]any{"name": "acme", "config": map[string]string{"organization": "acme", "webhookSecret": "secret"}}, http.StatusOK)
	seed := repository.Bootstrap()
	if len(seed.Issues) == 0 {
		t.Fatal("seed must include an issue")
	}
	payload := []byte(fmt.Sprintf(`{"action":"review_requested","number":15,"pull_request":{"id":9915,"title":"Fix checkout %s","body":"Please review","html_url":"https://github.com/acme/store/pull/15","state":"open","user":{"login":"dependabot"},"base":{"ref":"main"},"head":{"ref":"dependabot/fix","sha":"abc"}},"repository":{"full_name":"acme/store"}}`, seed.Issues[0].Identifier))
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write(payload)
	request := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/integrations/github/webhook?workspace=test-workspace", bytes.NewReader(payload))
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		req.Header.Set("X-GitHub-Delivery", "delivery-9915")
		return req
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request())
	if rec.Code != http.StatusAccepted {
		t.Fatalf("webhook status=%d body=%s", rec.Code, rec.Body.String())
	}
	var result map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	bootstrap := repository.Bootstrap()
	if len(bootstrap.Reviews) == 0 || bootstrap.Reviews[0].ExternalID != "9915" || !slices.ContainsFunc(bootstrap.Notifications, func(item domain.Notification) bool { return item.ReviewID == bootstrap.Reviews[0].ID }) {
		t.Fatalf("webhook projection incomplete: reviews=%#v notifications=%#v", bootstrap.Reviews, bootstrap.Notifications)
	}
	if !slices.Contains(bootstrap.Reviews[0].IssueIDs, seed.Issues[0].ID) || !slices.ContainsFunc(bootstrap.Activities[seed.Issues[0].ID], func(item domain.ActivityEvent) bool { return item.Type == "issue.review_linked" }) {
		t.Fatalf("webhook did not associate the referenced issue: review=%#v activities=%#v", bootstrap.Reviews[0], bootstrap.Activities[seed.Issues[0].ID])
	}
	notificationCount := len(bootstrap.Notifications)
	// GitHub retries the same delivery; the event must not create another notification.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request())
	if rec.Code != http.StatusAccepted || len(repository.Bootstrap().Notifications) != notificationCount {
		t.Fatalf("webhook retry was not idempotent: status=%d notifications=%d", rec.Code, len(repository.Bootstrap().Notifications))
	}
}
