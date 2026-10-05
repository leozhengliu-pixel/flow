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
	"net/url"
	"testing"

	"flow/api/internal/domain"
)

// Linear's "Merged" agent session state: a pull request linked to the
// session's issue merged. The task row is stamped once, only for issues that
// have a session, and both the indexed and in-memory filters report it.
func TestReviewMergeMarksAgentSessionsMerged(t *testing.T) {
	s, handler, data, app := applicationFixture(t)
	workspace := data.Workspace.URLKey
	delegate := func(title string) domain.Issue {
		issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": title, "teamId": data.Teams[0].ID}, http.StatusCreated)
		issue = requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issue-records/"+issue.ID, map[string]any{"delegateId": app.UserID}, http.StatusOK)
		if issue.AgentSessionID == "" {
			t.Fatalf("delegation did not start a session: %+v", issue)
		}
		return issue
	}
	merged := func() map[string]bool {
		states, err := s.store.WorkspaceAgentSessionStates(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		result := map[string]bool{}
		for id, state := range states {
			if state == "merged" {
				result[id] = true
			}
		}
		return result
	}
	query := func(path, filter string) []domain.Issue {
		raw := requestJSON[json.RawMessage](t, handler, http.MethodGet, path+"?limit=100&filter="+url.QueryEscape(filter), nil, http.StatusOK)
		var page struct {
			Items []domain.Issue `json:"items"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			t.Fatal(err)
		}
		return page.Items
	}

	// Webhook: GitHub reports the pull request merged.
	viaWebhook := delegate("Agent webhook work")
	plain := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "No agent here", "teamId": data.Teams[0].ID}, http.StatusCreated)
	requestJSON[domain.IntegrationConnection](t, handler, http.MethodPut, "/api/integrations/github?workspace="+workspace, map[string]any{"name": "acme", "config": map[string]string{"organization": "acme", "webhookSecret": "secret"}}, http.StatusOK)
	payload := []byte(fmt.Sprintf(`{"action":"closed","number":77,"pull_request":{"id":9977,"title":"Fix %s and %s","body":"","html_url":"https://github.com/acme/store/pull/77","state":"closed","merged":true,"user":{"login":"dev"},"base":{"ref":"main"},"head":{"ref":"fix"}},"repository":{"full_name":"acme/store"}}`, viaWebhook.Identifier, plain.Identifier))
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write(payload)
	for range 2 { // a redelivery is idempotent
		req := httptest.NewRequest(http.MethodPost, "/api/integrations/github/webhook?workspace="+workspace, bytes.NewReader(payload))
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		req.Header.Set("X-GitHub-Delivery", "delivery-9977")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("webhook status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
	if got := merged(); len(got) != 1 || !got[viaWebhook.AgentSessionID] {
		t.Fatalf("webhook merge sessions: %+v", got)
	}

	// Manual status change on a local review.
	viaPatch, pending := delegate("Agent patch work"), delegate("Agent still working")
	if err := s.store.MutateWorkspace(t.Context(), workspace, "review.local_fixture", "", nil, func(data *domain.Bootstrap) error {
		data.Reviews[len(data.Reviews)-1].Provider, data.Reviews[len(data.Reviews)-1].Status = "", "open"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	review := s.store.Bootstrap().Reviews
	local := review[len(review)-1]
	requestJSON[domain.CodeReview](t, handler, http.MethodPatch, "/api/reviews/"+local.ID, map[string]any{"issueIds": []string{viaPatch.ID, plain.ID}}, http.StatusOK)
	requestJSON[domain.CodeReview](t, handler, http.MethodPatch, "/api/reviews/"+local.ID, map[string]string{"status": "merged"}, http.StatusOK)
	if got := merged(); len(got) != 2 || !got[viaWebhook.AgentSessionID] || !got[viaPatch.AgentSessionID] {
		t.Fatalf("manual merge sessions: %+v", got)
	}

	// Indexed (paged) and in-memory filters agree; reads decorate "merged".
	for _, path := range []string{"/api/issue-records", "/api/issues"} {
		items := query(path, `{"field":"agentSessionState","values":["merged"]}`)
		ids := map[string]string{}
		for _, item := range items {
			ids[item.ID] = item.AgentSessionState
		}
		if len(ids) != 2 || ids[viaWebhook.ID] == "" || ids[viaPatch.ID] == "" {
			t.Fatalf("%s merged filter: %+v", path, ids)
		}
		if path == "/api/issue-records" && (ids[viaWebhook.ID] != "merged" || ids[viaPatch.ID] != "merged") {
			t.Fatalf("merged sessions must read as merged: %+v", ids)
		}
		active := query(path, `{"field":"agentSessionState","values":["pending","active","awaitingInput"]}`)
		if len(active) != 1 || active[0].ID != pending.ID {
			t.Fatalf("%s active filter: %+v", path, active)
		}
	}
}
