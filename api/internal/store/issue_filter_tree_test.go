package store

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Saved views' advanced filters compile to nested and/or/not trees; labels
// "include all of" is one indexed EXISTS per label.
func TestIssueQueryLabelsIncludeAllAndNestedTrees(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	data := repository.Bootstrap()
	workspace := data.Workspace.URLKey
	labelA, labelB := domain.IssueLabel{ID: "tree-label-a", Name: "A"}, domain.IssueLabel{ID: "tree-label-b", Name: "B"}
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -20)
	fixtures := []struct {
		labels   []domain.IssueLabel
		priority int
		started  *time.Time
	}{
		{[]domain.IssueLabel{labelA, labelB}, 1, &now}, // tree-0
		{[]domain.IssueLabel{labelA}, 2, &old},         // tree-1
		{[]domain.IssueLabel{labelB}, 1, nil},          // tree-2
		{nil, 3, nil},                                  // tree-3
	}
	for i, fixture := range fixtures {
		issue := data.Issues[0]
		issue.ID = fmt.Sprintf("tree-%d", i)
		issue.Identifier = fmt.Sprintf("TREE-%d", i)
		issue.State.ID = "tree-state"
		issue.Labels = fixture.labels
		issue.Priority = fixture.priority
		issue.StartedAt = fixture.started
		if err := repository.ImportIssues(ctx, workspace, []domain.Issue{issue}); err != nil {
			t.Fatal(err)
		}
	}
	scope := IssueFilter{Field: "status", Values: []string{"tree-state"}}
	ids := func(filter IssueFilter) []string {
		t.Helper()
		page, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, Filter: IssueFilter{And: []IssueFilter{scope, filter}}, Limit: 50})
		if err != nil {
			t.Fatalf("query %#v: %v", filter, err)
		}
		result := []string{}
		for _, issue := range page.Items {
			result = append(result, issue.ID)
		}
		slices.Sort(result)
		return result
	}
	expect := func(name string, filter IssueFilter, want ...string) {
		t.Helper()
		if got := ids(filter); !slices.Equal(got, want) {
			t.Fatalf("%s: got %v want %v", name, got, want)
		}
	}
	expect("include all of", IssueFilter{Field: "labels", Operator: "includesAll", Values: []string{labelA.ID, labelB.ID}}, "tree-0")
	expect("exclude if all", IssueFilter{Field: "labels", Operator: "excludesAll", Values: []string{labelA.ID, labelB.ID}}, "tree-1", "tree-2", "tree-3")
	expect("include any of", IssueFilter{Field: "labels", Operator: "in", Values: []string{labelA.ID, labelB.ID}}, "tree-0", "tree-1", "tree-2")
	expect("no labels", IssueFilter{Field: "labels", Operator: "in", Values: []string{""}}, "tree-3")
	// (labels include A AND priority urgent) OR NOT (labels include any of A, B)
	expect("nested and/or/not", IssueFilter{Or: []IssueFilter{
		{And: []IssueFilter{{Field: "labels", Values: []string{labelA.ID}}, {Field: "priority", Values: []string{"1"}}}},
		{Not: &IssueFilter{Field: "labels", Values: []string{labelA.ID, labelB.ID}}},
	}}, "tree-0", "tree-3")
	// top AND (group OR (nested AND)): started after a week ago OR (label B AND priority urgent)
	expect("three levels", IssueFilter{And: []IssueFilter{
		{Field: "priority", Operator: "notin", Values: []string{"3"}},
		{Or: []IssueFilter{
			{Field: "startedAt", Operator: "after", Values: []string{now.AddDate(0, 0, -7).Format(time.RFC3339)}},
			{And: []IssueFilter{{Field: "labels", Operator: "includesAll", Values: []string{labelB.ID}}, {Field: "priority", Values: []string{"1"}}}},
		}},
	}}, "tree-0", "tree-2")
	if _, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, Filter: IssueFilter{Field: "priority", Operator: "includesAll", Values: []string{"1"}}}); err == nil {
		t.Fatal("includesAll is only defined for multi-valued relations")
	}
}
