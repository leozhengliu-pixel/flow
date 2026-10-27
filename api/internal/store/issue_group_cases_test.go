package store

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
)

func TestIssueGroupValuesAndCases(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	data := repository.Bootstrap()
	testIssueGroupCasesContract(t, repository, data.Workspace.URLKey, data.Issues[0])
}

// testIssueGroupCasesContract runs on SQLite and (integration) MySQL/Postgres.
//
//	a1 parent-done (completed, team A)   b1 child-done (completed, team B workflow)
//	a2 urgent todo                       b2 todo, child of a2, low priority
//	a3 todo                              b3 progress
func testIssueGroupCasesContract(t *testing.T, repository *SQLiteStore, workspace string, base domain.Issue) {
	t.Helper()
	ctx := context.Background()
	parentDone := domain.WorkflowState{ID: "grp-parent-done", Name: "Done", Type: "completed"}
	childDone := domain.WorkflowState{ID: "grp-child-done", Name: "Done", Type: "completed"}
	todo := domain.WorkflowState{ID: "grp-todo", Name: "Todo", Type: "unstarted"}
	progress := domain.WorkflowState{ID: "grp-progress", Name: "In Progress", Type: "started"}
	project := &domain.ProjectSummary{ID: "grp-project"}
	issue := func(id string, state domain.WorkflowState, priority int, sort float64, parent string) domain.Issue {
		item := base
		item.ID, item.Identifier, item.Title = "grp-"+id, "GRP-"+id, id
		item.State, item.Priority, item.SortOrder, item.Project = state, priority, sort, project
		item.Labels, item.Permissions, item.ParentID, item.SubIssueIDs, item.ArchivedAt = nil, nil, nil, nil, nil
		if parent != "" {
			value := "grp-" + parent
			item.ParentID = &value
		}
		return item
	}
	issues := []domain.Issue{issue("a1", parentDone, 0, 1, ""), issue("b1", childDone, 0, 2, ""), issue("a2", todo, 1, 3, ""), issue("b2", todo, 4, 4, "a2"), issue("a3", todo, 0, 5, ""), issue("b3", progress, 0, 6, "")}
	if err := repository.ImportIssues(ctx, workspace, issues); err != nil {
		t.Fatal(err)
	}
	scope := IssueFilter{Field: "project", Values: []string{project.ID}}
	ids := func(page IssueRecordPage) []string {
		result := []string{}
		for _, item := range page.Items {
			result = append(result, item.ID)
		}
		return result
	}
	counts := func(query IssueRecordQuery) []IssueRecordGroup {
		t.Helper()
		groups, err := repository.QueryIssueGroups(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		return groups
	}

	// A status two workflows share pages as one group, with stable cursors.
	merged := IssueRecordQuery{Workspace: workspace, Filter: scope, GroupBy: "status", GroupValues: []string{parentDone.ID, childDone.ID}, Limit: 1, Summary: true}
	first, err := repository.QueryIssueRecords(ctx, merged)
	if err != nil || !first.HasMore || !slices.Equal(ids(first), []string{"grp-a1"}) {
		t.Fatalf("merged first page = %v %v %v", ids(first), first.HasMore, err)
	}
	merged.Cursor = first.NextCursor
	second, err := repository.QueryIssueRecords(ctx, merged)
	if err != nil || second.HasMore || !slices.Equal(ids(second), []string{"grp-b1"}) {
		t.Fatalf("merged second page = %v %v %v", ids(second), second.HasMore, err)
	}
	other := merged
	other.GroupValues = []string{childDone.ID}
	if _, err := repository.QueryIssueRecords(ctx, other); !errors.Is(err, ErrIssueQuery) {
		t.Fatalf("a cursor of another group = %v", err)
	}
	if _, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, GroupBy: "label", GroupValues: []string{"x"}}); !errors.Is(err, ErrIssueQuery) {
		t.Fatalf("group values on an attribute grouping = %v", err)
	}
	value := parentDone.ID
	if _, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, GroupBy: "status", GroupValue: &value, GroupValues: []string{value}}); !errors.Is(err, ErrIssueQuery) {
		t.Fatalf("group value and values = %v", err)
	}
	// Groups ignore a selected group (as for a single value).
	if groups := counts(IssueRecordQuery{Workspace: workspace, Filter: scope, GroupBy: "status", GroupValues: []string{todo.ID}}); len(groups) != 4 {
		t.Fatalf("status groups = %v", groups)
	}

	// Cases: the first matching case wins, in the cases' order.
	cases := []IssueGroupCase{
		{Value: "focus-completed", Filter: IssueFilter{Field: "statusType", Values: []string{"completed"}}},
		{Value: "focus-urgent", Filter: IssueFilter{Field: "priority", Values: []string{"1"}}},
		{Value: "focus-active", Filter: IssueFilter{Field: "statusType", Values: []string{"started"}}},
		{Value: "focus-todo", Filter: IssueFilter{Field: "status", Values: []string{todo.ID}}},
	}
	query := IssueRecordQuery{Workspace: workspace, Filter: scope, GroupBy: "cases", GroupCases: cases, Summary: true}
	want := []IssueRecordGroup{{Value: "focus-completed", Count: 2}, {Value: "focus-urgent", Count: 1}, {Value: "focus-active", Count: 1}, {Value: "focus-todo", Count: 2}}
	if groups := counts(query); !slices.Equal(groups, want) {
		t.Fatalf("case groups = %v", groups)
	}
	page := func(value string, nested bool) []string {
		t.Helper()
		q := query
		q.GroupValue, q.Nested = &value, nested
		result, err := repository.QueryIssueRecords(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return ids(result)
	}
	if got := page("focus-completed", false); !slices.Equal(got, []string{"grp-a1", "grp-b1"}) {
		t.Fatalf("completed case = %v", got)
	}
	if got := page("focus-todo", false); !slices.Equal(got, []string{"grp-b2", "grp-a3"}) {
		t.Fatalf("todo case = %v", got)
	}
	if got := page("unknown", false); len(got) != 0 {
		t.Fatalf("unknown case = %v", got)
	}
	// Rows no case matches are the "" group.
	partial := query
	partial.GroupCases = cases[:2]
	if groups := counts(partial); !slices.Equal(groups, []IssueRecordGroup{{Value: "focus-completed", Count: 2}, {Value: "focus-urgent", Count: 1}, {Value: "", Count: 3}}) {
		t.Fatalf("partial case groups = %v", groups)
	}
	partial.GroupValue = new(string)
	if result, err := repository.QueryIssueRecords(ctx, partial); err != nil || !slices.Equal(ids(result), []string{"grp-b2", "grp-a3", "grp-b3"}) {
		t.Fatalf("no case page = %v %v", ids(result), err)
	}
	// Nested: a sub-issue counts, and pages, in its top-level row's case.
	nested := query
	nested.Nested = true
	if groups := counts(nested); !slices.Equal(groups, []IssueRecordGroup{{Value: "focus-completed", Count: 2}, {Value: "focus-urgent", Count: 2}, {Value: "focus-active", Count: 1}, {Value: "focus-todo", Count: 1}}) {
		t.Fatalf("nested case groups = %v", groups)
	}
	if got := page("focus-todo", true); !slices.Equal(got, []string{"grp-a3"}) {
		t.Fatalf("nested todo case = %v", got)
	}
	// Invalid cases are rejected.
	for _, invalid := range [][]IssueGroupCase{nil, {{Value: "", Filter: scope}}, {{Value: "x", Filter: scope}, {Value: "x", Filter: scope}}, {{Value: "x", Filter: IssueFilter{Field: "nope", Values: []string{"1"}}}}} {
		bad := query
		bad.GroupCases = invalid
		if _, err := repository.QueryIssueGroups(ctx, bad); !errors.Is(err, ErrIssueQuery) {
			t.Fatalf("invalid cases %v = %v", invalid, err)
		}
	}
}
