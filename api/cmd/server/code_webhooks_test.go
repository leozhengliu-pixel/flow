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
	"strings"
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

func TestGitLabMergeRequestWebhookAndConnectionProbe(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	gitlab := requestJSON[domain.IntegrationConnection](t, handler, http.MethodPut, "/api/integrations/gitlab?workspace=test-workspace", map[string]any{
		"name": "self-hosted", "config": map[string]string{"apiToken": "glpat-configured", "webhookSecret": "hook-secret"},
	}, http.StatusOK)
	seed := repository.Bootstrap()
	if len(seed.Issues) == 0 || len(seed.Users) < 2 {
		t.Fatal("seed must include issues and users")
	}
	payload := []byte(fmt.Sprintf(`{"object_kind":"merge_request","event_type":"merge_request","user":{"username":"dependabot"},"project":{"path_with_namespace":"acme/platform/api","web_url":"https://gitlab.example.com/acme/platform/api"},"object_attributes":{"id":44001,"iid":27,"title":"Fix checkout %s","description":"Please review","url":"https://gitlab.example.com/acme/platform/api/-/merge_requests/27","state":"opened","action":"open","source_branch":"feature/checkout","target_branch":"main","last_commit":{"id":"abc"}},"reviewers":[{"username":"%s"}]}`, seed.Issues[0].Identifier, strings.SplitN(seed.Users[1].Email, "@", 2)[0]))
	request := func(eventID string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/integrations/gitlab/webhook?workspace=test-workspace", bytes.NewReader(payload))
		req.Header.Set("X-Gitlab-Token", "hook-secret")
		req.Header.Set("X-Gitlab-Event-UUID", eventID)
		return req
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("gitlab-delivery-27"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("GitLab webhook status=%d body=%s", rec.Code, rec.Body.String())
	}
	bootstrap := repository.Bootstrap()
	if len(bootstrap.Reviews) == 0 {
		t.Fatal("GitLab webhook did not create a review")
	}
	review := bootstrap.Reviews[0]
	if review.Provider != "gitlab" || review.ExternalID != "44001" || review.Number != 27 || review.RepositoryOwner != "acme" || review.RepositoryName != "platform/api" || review.BaseBranch != "main" || review.HeadBranch != "feature/checkout" {
		t.Fatalf("GitLab merge request fields were not projected: %#v", review)
	}
	if len(review.Events) == 0 || review.Events[len(review.Events)-1].Type != "opened" {
		t.Fatalf("GitLab merge request action was not normalized: %#v", review.Events)
	}
	if len(review.ReviewerIDs) != 1 || review.ReviewerIDs[0] != seed.Users[1].ID {
		t.Fatalf("GitLab reviewers were not resolved: %#v", review.ReviewerIDs)
	}
	if !slices.Contains(review.IssueIDs, seed.Issues[0].ID) {
		t.Fatalf("GitLab merge request was not linked to the issue: %#v", review.IssueIDs)
	}
	count := len(bootstrap.Notifications)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request("gitlab-delivery-27"))
	if rec.Code != http.StatusAccepted || len(repository.Bootstrap().Notifications) != count {
		t.Fatalf("GitLab webhook retry was not idempotent: status=%d notifications=%d", rec.Code, len(repository.Bootstrap().Notifications))
	}

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/user" || r.Header.Get("PRIVATE-TOKEN") != "glpat-test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"username":"flow-bot"}`))
	}))
	defer api.Close()
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/integrations/gitlab/"+gitlab.ID+"/test?workspace=test-workspace", map[string]string{"token": "glpat-test", "host": api.URL}, http.StatusOK)
	if status := repository.Bootstrap().IntegrationConnections[0].LastTestStatus; status != "ready" {
		t.Fatalf("successful GitLab connection test was not persisted: %q", status)
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/integrations/gitlab/"+gitlab.ID+"/test?workspace=test-workspace", map[string]string{"token": "bad-token", "host": api.URL}, http.StatusBadGateway)
	if status := repository.Bootstrap().IntegrationConnections[0].LastTestStatus; status != "error" {
		t.Fatalf("failed GitLab connection test was not persisted: %q", status)
	}
}
