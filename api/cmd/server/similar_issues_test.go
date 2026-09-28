package main

import (
	"net/http"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestRankSimilarIssuesFoldsSynonymsAndSkipsLinked(t *testing.T) {
	target := domain.Issue{ID: "t", Title: "Login page broken on Safari", Description: "Users cannot sign in", Relations: []domain.IssueRelation{{RelatedIssueID: "linked"}}}
	candidates := []domain.Issue{
		{ID: "dup", Title: "Login screen defect in Safari", State: domain.WorkflowState{Type: "unstarted"}},
		{ID: "linked", Title: "Login page broken on Safari", State: domain.WorkflowState{Type: "unstarted"}},
		{ID: "canceled", Title: "Login page broken on Safari", State: domain.WorkflowState{Type: "canceled"}},
		{ID: "other", Title: "Billing export totals", State: domain.WorkflowState{Type: "started"}},
		{ID: "cjk", Title: "导出账单金额错误", State: domain.WorkflowState{Type: "started"}},
	}
	results := rankSimilarIssues(target, candidates, 5, 0.2)
	if len(results) != 1 || results[0].Issue.ID != "dup" {
		t.Fatalf("expected only the synonym match, got %#v", results)
	}

	cjk := rankSimilarIssues(domain.Issue{ID: "x", Title: "账单金额错误"}, candidates, 5, 0.2)
	if len(cjk) != 1 || cjk[0].Issue.ID != "cjk" {
		t.Fatalf("expected CJK bigram match, got %#v", cjk)
	}
}

func TestSimilarIssuesEndpoint(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "similar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	target := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issue-records", map[string]any{"title": "Checkout button unresponsive on mobile", "description": "Tapping checkout does nothing"}, http.StatusCreated)
	match := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issue-records", map[string]any{"title": "Mobile checkout button does not respond"}, http.StatusCreated)
	_ = requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issue-records", map[string]any{"title": "Rename workspace settings tab"}, http.StatusCreated)
	got := requestJSON[struct {
		Results []similarIssueResult `json:"results"`
	}](t, handler, http.MethodGet, "/api/issue-records/"+target.ID+"/similar", nil, http.StatusOK)
	if len(got.Results) != 1 || got.Results[0].Issue.ID != match.ID {
		t.Fatalf("expected the checkout issue, got %#v", got.Results)
	}
}
