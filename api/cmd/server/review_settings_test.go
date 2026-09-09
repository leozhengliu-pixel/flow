package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"flow/api/internal/domain"
)

func TestReviewMergeUsesConfiguredMethodAndChecksProviderResult(t *testing.T) {
	methods := make(chan string, 2)
	var success atomic.Bool
	success.Store(true)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || r.URL.Path != "/repos/owner/repo/pulls/9/merge" || r.Header.Get("Authorization") != "Bearer local-test-token" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		methods <- body["merge_method"]
		_ = json.NewEncoder(w).Encode(map[string]bool{"merged": success.Load()})
	}))
	defer upstream.Close()
	data := domain.Bootstrap{IntegrationConnections: []domain.IntegrationConnection{{Provider: "github", OAuthAccessToken: "local-test-token", Status: "connected", Config: map[string]string{"apiUrl": upstream.URL, "organization": "owner"}}}}
	review := domain.CodeReview{Provider: "github", RepositoryOwner: "owner", RepositoryName: "repo", Number: 9}
	s := server{authDisabled: true}
	if err := s.syncReviewStatus(t.Context(), data, review, "merged", mergeMethod("Rebase and merge")); err != nil {
		t.Fatal(err)
	}
	if method := <-methods; method != "rebase" {
		t.Fatalf("merge preference ignored: %s", method)
	}
	success.Store(false)
	if err := s.syncReviewStatus(t.Context(), data, review, "merged", "squash"); err == nil {
		t.Fatal("upstream refused merge but Flow treated it as success")
	}
}

func TestExternalReviewCannotPretendToMergeWithoutCredentials(t *testing.T) {
	s := server{authDisabled: true}
	review := domain.CodeReview{Provider: "github", RepositoryOwner: "owner", RepositoryName: "repo", Number: 1}
	if err := s.syncReviewStatus(t.Context(), domain.Bootstrap{}, review, "merged", "squash"); err == nil {
		t.Fatal("missing integration must not succeed")
	}
	data := domain.Bootstrap{IntegrationConnections: []domain.IntegrationConnection{{Provider: "github", Status: "connected"}}}
	t.Setenv("FLOW_INTEGRATION_GITHUB_ACCESS_TOKEN", "")
	if err := s.syncReviewStatus(t.Context(), data, review, "merged", "squash"); err == nil {
		t.Fatal("missing token must not succeed")
	}
}
