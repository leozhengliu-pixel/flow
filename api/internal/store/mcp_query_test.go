package store

import (
	"fmt"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestMCPQueryDoesNotDecodeUnrelatedIssuesOrEditorState(t *testing.T) {
	repo, q, target := issuePointReadFixture(t)
	other := target
	other.ID, other.Identifier = "unrelated-mcp-issue", "POINT-99"
	if err := repo.ImportIssues(t.Context(), q.Workspace, []domain.Issue{other}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(t.Context(), `UPDATE issue_records SET data='invalid-json',list_data='invalid-json' WHERE workspace_key=? AND id=?`, q.Workspace, other.ID); err != nil {
		t.Fatal(err)
	}
	q.Summary, q.IncludeDescription = true, true
	q.Filter = IssueFilter{Field: "id", Values: []string{target.ID}}
	q.SearchText = "Detailed body"
	q.Limit = 1
	page, err := repo.QueryIssueRecords(t.Context(), q)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("bounded query read an unrelated payload: %v", err)
	}
	if page.Items[0].Description != target.Description || page.Items[0].DocumentContent != nil || page.Items[0].DescriptionState != "" {
		t.Fatal("description projection mismatch")
	}
	// Explicit field selection must not even parse the heavyweight data column.
	if _, err := repo.db.ExecContext(t.Context(), `UPDATE issue_records SET data='invalid-json' WHERE workspace_key=? AND id=?`, q.Workspace, target.ID); err != nil {
		t.Fatal(err)
	}
	q.IncludeDescription = false
	page, err = repo.QueryIssueRecords(t.Context(), q)
	if err != nil || len(page.Items) != 1 || page.Items[0].Description != "" {
		t.Fatal("summary queried editor payload", err)
	}
}

func TestMCPDescriptionPagesHaveByteBudget(t *testing.T) {
	repo, q, target := issuePointReadFixture(t)
	issues := make([]domain.Issue, 9)
	ids := []string{}
	for i := range issues {
		issues[i] = target
		issues[i].ID = fmt.Sprintf("large-%d", i)
		issues[i].Identifier = fmt.Sprintf("BIG-%d", i+1)
		issues[i].Description = strings.Repeat("x", 512<<10)
		ids = append(ids, issues[i].ID)
	}
	if err := repo.ImportIssues(t.Context(), q.Workspace, issues); err != nil {
		t.Fatal(err)
	}
	q.Filter = IssueFilter{Field: "id", Values: ids}
	q.Summary, q.IncludeDescription, q.Limit = true, true, 250
	seen := 0
	for {
		page, err := repo.QueryIssueRecords(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) < 1 || len(page.Items) > 7 {
			t.Fatalf("oversized description page: %d items", len(page.Items))
		}
		seen += len(page.Items)
		if !page.HasMore {
			break
		}
		q.Cursor = page.NextCursor
		if seen > 9 {
			t.Fatal("cursor failed to advance")
		}
	}
	if seen != 9 {
		t.Fatal("byte limit lost results")
	}
}
