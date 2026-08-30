package main

import (
	"net/http"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestReviewLifecycleAndCodeConnections(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Reviews) == 0 || len(bootstrap.Issues) < 2 || len(bootstrap.Users) < 2 {
		t.Fatal("seed must expose a review, issues, and users")
	}
	review := bootstrap.Reviews[0]
	updated := requestJSON[domain.CodeReview](t, handler, http.MethodPatch, "/api/reviews/"+review.ID, map[string]any{
		"reviewerIds": []string{bootstrap.Users[1].ID}, "issueIds": []string{bootstrap.Issues[1].ID}, "favorite": true, "branchState": "upToDate",
	}, http.StatusOK)
	if len(updated.ReviewerIDs) != 1 || updated.ReviewerIDs[0] != bootstrap.Users[1].ID || len(updated.IssueIDs) != 1 || !updated.Favorite || updated.BranchState != "upToDate" {
		t.Fatalf("review properties did not persist: %#v", updated)
	}
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	linkedActivities := bootstrap.Activities[bootstrap.Issues[1].ID]
	if len(linkedActivities) == 0 || linkedActivities[len(linkedActivities)-1].Type != "issue.review_linked" || linkedActivities[len(linkedActivities)-1].Metadata["reviewId"] != review.ID {
		t.Fatalf("review link activity missing: %#v", linkedActivities)
	}
	updated = requestJSON[domain.CodeReview](t, handler, http.MethodPatch, "/api/reviews/"+review.ID, map[string]any{"issueIds": []string{}}, http.StatusOK)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	unlinkedActivities := bootstrap.Activities[bootstrap.Issues[1].ID]
	if len(unlinkedActivities) == 0 || unlinkedActivities[len(unlinkedActivities)-1].Type != "issue.review_unlinked" {
		t.Fatalf("review unlink activity missing: %#v", unlinkedActivities)
	}
	updated = requestJSON[domain.CodeReview](t, handler, http.MethodPatch, "/api/reviews/"+review.ID, map[string]any{"issueIds": []string{bootstrap.Issues[1].ID}}, http.StatusOK)
	updated = requestJSON[domain.CodeReview](t, handler, http.MethodPost, "/api/reviews/"+review.ID+"/submit", map[string]string{"decision": "approve", "body": "Ready to merge"}, http.StatusOK)
	if updated.Status != "approved" || len(updated.Events) < 3 || updated.Events[len(updated.Events)-1].Type != "approved" {
		t.Fatalf("review approval did not persist: %#v", updated)
	}
	updated = requestJSON[domain.CodeReview](t, handler, http.MethodPatch, "/api/reviews/"+review.ID, map[string]string{"status": "merged"}, http.StatusOK)
	if updated.MergedAt == nil || updated.Status != "merged" {
		t.Fatalf("review merge did not set completion state: %#v", updated)
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/reviews/"+review.ID+"/submit", map[string]string{"decision": "approve"}, http.StatusConflict)

	github := requestJSON[domain.IntegrationConnection](t, handler, http.MethodPut, "/api/integrations/github", map[string]any{"name": "heliumlabz", "config": map[string]string{"organization": "heliumlabz"}}, http.StatusOK)
	gitlab := requestJSON[domain.IntegrationConnection](t, handler, http.MethodPut, "/api/integrations/gitlab", map[string]any{"name": "GitLab", "config": map[string]string{"apiToken": "glpat-secret-1234", "host": "https://gitlab.example.com"}}, http.StatusOK)
	if github.ID == gitlab.ID || gitlab.Config["tokenHint"] != "1234" || gitlab.SecretHash != "" {
		t.Fatalf("code connections were not safely persisted: github=%#v gitlab=%#v", github, gitlab)
	}
	connections := requestJSON[[]domain.IntegrationConnection](t, handler, http.MethodGet, "/api/integrations", nil, http.StatusOK)
	if len(connections) != 2 || connections[1].Config["apiToken"] != "" || connections[1].SecretHash != "" {
		t.Fatalf("integration list leaked credentials or lost connections: %#v", connections)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/integrations/gitlab/"+gitlab.ID, nil, http.StatusNoContent)
	connections = requestJSON[[]domain.IntegrationConnection](t, handler, http.MethodGet, "/api/integrations", nil, http.StatusOK)
	if len(connections) != 1 || connections[0].Provider != "github" {
		t.Fatalf("connection-specific disconnect failed: %#v", connections)
	}
}
