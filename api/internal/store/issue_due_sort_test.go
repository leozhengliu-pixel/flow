package store

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestIssueDueDateSortUsesSLAAndPaginatesMissingDatesLast(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "due-sort.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	testIssueDueDateSort(t, repo, repo.Bootstrap())
}

func testIssueDueDateSort(t *testing.T, repo *SQLiteStore, data domain.Bootstrap) {
	t.Helper()
	start := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	issues := make([]domain.Issue, 6)
	for index := range issues {
		issues[index] = domain.Issue{ID: fmt.Sprintf("due-%d", index), Identifier: fmt.Sprintf("DUE-%d", index), Number: index + 1, Title: "Due sort", Team: data.Teams[0], State: domain.WorkflowState{ID: "due-state", Type: "unstarted", Name: "Todo", TeamID: data.Teams[0].ID}, CreatedAt: start, UpdatedAt: start}
	}
	date := "2026-11-03"
	issues[0].DueDate, issues[4].DueDate = &date, &date
	manual := start.Add(36 * time.Hour)
	issues[3].SLABreachesAt = &manual
	issues[5].SLABreachesAt = &manual
	issues[5].State.Type = "canceled"
	if err := repo.ImportIssues(t.Context(), data.Workspace.URLKey, issues); err != nil {
		t.Fatal(err)
	}
	if err := repo.MutateWorkspace(t.Context(), data.Workspace.URLKey, "test.sla_sort", "", nil, func(current *domain.Bootstrap) error {
		current.Settings["sla"] = map[string]any{"enabled": true}
		current.IssueSLAs = []domain.IssueSLA{
			{ID: "sort-sla", IssueID: issues[0].ID, DueAt: start.Add(72 * time.Hour), Status: "active"},
			{ID: "removed-sla", IssueID: issues[2].ID, DueAt: start.Add(time.Hour), Status: "removed"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	filter := IssueFilter{Field: "id", Values: []string{issues[0].ID, issues[1].ID, issues[2].ID, issues[3].ID, issues[4].ID, issues[5].ID}}
	for _, test := range []struct {
		direction string
		want      []string
	}{
		{"asc", []string{"due-4", "due-3", "due-0", "due-1", "due-2", "due-5"}},
		{"desc", []string{"due-0", "due-3", "due-4", "due-5", "due-2", "due-1"}},
	} {
		query := IssueRecordQuery{Workspace: data.Workspace.URLKey, Filter: filter, Sort: "dueDate", Direction: test.direction, Limit: 2, Cursors: true, Access: &IssueRecordAccess{UserID: data.Viewer.ID, WorkspaceID: data.Workspace.ID, VisibleTeamIDs: []string{data.Teams[0].ID}}}
		pages := []IssueRecordPage{}
		got := []string{}
		for {
			page, err := repo.QueryIssueRecords(t.Context(), query)
			if err != nil {
				t.Fatalf("%s: %v", test.direction, err)
			}
			pages = append(pages, page)
			got = append(got, pageIDs(page)...)
			if !page.HasMore {
				break
			}
			query.Cursor = page.EndCursor
		}
		if !slices.Equal(got, test.want) {
			t.Fatalf("%s due order = %v, want %v", test.direction, got, test.want)
		}
		query.Reverse = true
		for index := len(pages) - 1; index > 0; index-- {
			query.Cursor = pages[index].StartCursor
			previous, err := repo.QueryIssueRecords(t.Context(), query)
			if err != nil || !slices.Equal(pageIDs(previous), pageIDs(pages[index-1])) {
				t.Fatalf("%s backward page %d = %v, want %v: %v", test.direction, index, pageIDs(previous), pageIDs(pages[index-1]), err)
			}
		}
	}
	if err := repo.MutateWorkspace(t.Context(), data.Workspace.URLKey, "sla_settings.updated", "", nil, func(current *domain.Bootstrap) error {
		current.Settings["sla"] = map[string]any{"enabled": false}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	page, err := repo.QueryIssueRecords(t.Context(), IssueRecordQuery{Workspace: data.Workspace.URLKey, Filter: filter, Sort: "dueDate", Direction: "asc", Limit: 10})
	if err != nil || !slices.Equal(pageIDs(page), []string{"due-0", "due-4", "due-1", "due-2", "due-3", "due-5"}) {
		t.Fatalf("disabled SLA changed date ordering: %v %v", pageIDs(page), err)
	}
}
