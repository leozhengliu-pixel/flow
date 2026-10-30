package store

import (
	"context"
	"testing"

	"flow/api/internal/domain"
)

// TestLabelDrivenQueryMatchesGenericPlan checks the label-index-driven row
// source against the same filter compiled without the optimisation (an OR
// wrapper is not drivable). Ordering, keyset pagination and totals must match.
func TestLabelDrivenQueryMatchesGenericPlan(t *testing.T) {
	ctx := context.Background()
	repository, workspace := perfWorkspace(t)
	key := workspace.Workspace.URLKey
	selective := workspace.Labels[0]
	empty := workspace.Labels[1]
	issues := make([]domain.Issue, 0, 120)
	for i := 0; i < 120; i++ {
		issue := perfIssue(workspace, i)
		issue.Priority = i % 5
		if i%25 == 0 {
			issue.Labels = []domain.IssueLabel{selective}
		} else if i%3 == 0 {
			issue.Labels = []domain.IssueLabel{workspace.Labels[2]}
		}
		issues = append(issues, issue)
	}
	if err := repository.ImportIssues(ctx, key, issues); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		filter IssueFilter
	}{
		{"selective", IssueFilter{Field: "labels", Values: []string{selective.ID}}},
		{"empty", IssueFilter{Field: "labels", Values: []string{empty.ID}}},
		{"selective-and-priority", IssueFilter{And: []IssueFilter{
			{Field: "labels", Values: []string{selective.ID}},
			{Field: "priority", Values: []string{"1", "2"}},
		}}},
	}
	for _, test := range cases {
		driven := IssueRecordQuery{Workspace: key, Filter: test.filter, TeamIDs: []string{workspace.Teams[0].ID}, Sort: "updatedAt", Direction: "desc", Limit: 20, IncludeTotal: true}
		generic := driven
		generic.Filter = IssueFilter{Or: []IssueFilter{test.filter}}
		first, err := repository.QueryIssueRecords(ctx, driven)
		if err != nil {
			t.Fatalf("%s driven: %v", test.name, err)
		}
		second, err := repository.QueryIssueRecords(ctx, generic)
		if err != nil {
			t.Fatalf("%s generic: %v", test.name, err)
		}
		if first.Total != second.Total || len(first.Items) != len(second.Items) {
			t.Fatalf("%s totals/items differ: %d/%d vs %d/%d", test.name, first.Total, len(first.Items), second.Total, len(second.Items))
		}
		for i := range first.Items {
			if first.Items[i].ID != second.Items[i].ID {
				t.Fatalf("%s item %d: %s != %s", test.name, i, first.Items[i].ID, second.Items[i].ID)
			}
		}
		if test.name == "empty" && first.Total != 0 {
			t.Fatalf("empty label total=%d", first.Total)
		}
	}
}
