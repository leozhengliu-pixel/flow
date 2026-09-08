package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestSparseIssueAttributesFilterGroupAndResumeMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "attributes.db")
	repository, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	data := repository.Bootstrap()
	workspace := data.Workspace.URLKey
	base := data.Issues[0]
	rows := make([]domain.Issue, 620)
	for i := range rows {
		issue := base
		issue.ID, issue.Identifier = fmt.Sprintf("attribute-%04d", i), fmt.Sprintf("ATTR-%d", i)
		issue.State.ID = "attribute-state"
		issue.ProjectMilestoneID, issue.DueDate, issue.Estimate, issue.CompletedAt, issue.Labels = nil, nil, nil, nil, nil
		if i%2 == 0 {
			milestone, due, estimate := "milestone-scale", "2026-09-08", 12.0
			completed := time.Date(2026, 9, 8, 1, 0, 0, 0, time.UTC)
			issue.ProjectMilestoneID, issue.DueDate, issue.Estimate, issue.CompletedAt = &milestone, &due, &estimate, &completed
			issue.Labels = []domain.IssueLabel{{ID: "first-label"}, {ID: "second-label"}}
		}
		rows[i] = issue
	}
	if err := repository.ImportIssues(ctx, workspace, rows); err != nil {
		t.Fatal(err)
	}
	check := func(repository *SQLiteStore) {
		t.Helper()
		for _, test := range []struct {
			filter IssueFilter
			want   int64
		}{
			{IssueFilter{Field: "projectMilestoneId", Values: []string{"milestone-scale"}}, 310},
			{IssueFilter{Field: "projectMilestoneId", Values: []string{""}}, 310},
			{IssueFilter{Field: "dueDate", Operator: "before", Values: []string{"2026-09-09"}}, 310},
			{IssueFilter{Field: "completedAt", Operator: "after", Values: []string{"2026-09-08"}}, 310},
			{IssueFilter{Field: "estimate", Operator: "gt", Values: []string{"9"}}, 310},
			{IssueFilter{Field: "estimate", Operator: "isEmpty"}, 310},
		} {
			page, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, Filter: IssueFilter{And: []IssueFilter{{Field: "status", Values: []string{"attribute-state"}}, test.filter}}, IncludeTotal: true, Limit: 1})
			if err != nil || page.Total != test.want {
				t.Fatalf("%+v: total=%d err=%v", test.filter, page.Total, err)
			}
		}
		for _, groupBy := range []string{"label", "milestone", "estimate"} {
			query := IssueRecordQuery{Workspace: workspace, GroupBy: groupBy, Filter: IssueFilter{Field: "status", Values: []string{"attribute-state"}}}
			groups, err := repository.QueryIssueGroups(ctx, query)
			if err != nil || len(groups) != 2 {
				t.Fatalf("%s groups: %+v %v", groupBy, groups, err)
			}
			for _, group := range groups {
				if group.Count != 310 {
					t.Fatalf("truncated group: %+v", group)
				}
				query.GroupValue, query.IncludeTotal, query.Limit = &group.Value, true, 1
				page, err := repository.QueryIssueRecords(ctx, query)
				if err != nil || page.Total != group.Count {
					t.Fatalf("%s count differs from pages: %+v %v", groupBy, page, err)
				}
			}
		}
	}
	check(repository)
	summary, err := repository.QueryIssueRecordSummary(ctx, IssueRecordQuery{Workspace: workspace, Filter: IssueFilter{Field: "status", Values: []string{"attribute-state"}}})
	if err != nil || summary.Total != 620 || summary.Milestones["milestone-scale"].Total != 310 || summary.Labels["first-label"].Total != 310 || summary.Labels["second-label"].Total != 310 {
		t.Fatalf("summary counted pages or only the first label: %+v %v", summary, err)
	}
	// Simulate an interrupted upgrade that committed its first checkpoint.
	if _, err := repository.db.ExecContext(ctx, `DELETE FROM issue_attribute_records WHERE workspace_key=? AND issue_id>?`, workspace, "attribute-0249"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.ExecContext(ctx, `UPDATE issue_attribute_migrations SET last_id=?,complete=0 WHERE workspace_key=?`, "attribute-0249", workspace); err != nil {
		t.Fatal(err)
	}
	repository.Close()
	repository, err = OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	check(repository)
}

func TestMetadataMutationNeverHydratesOrRewritesIssuePayloads(t *testing.T) {
	ctx := context.Background()
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	data := repository.Bootstrap()
	workspace := data.Workspace.URLKey
	id := data.Issues[0].ID
	// A sentinel payload makes any accidental full hydration fail immediately.
	if _, err := repository.db.ExecContext(ctx, `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, []byte("not-json"), workspace, id); err != nil {
		t.Fatal(err)
	}
	err = repository.MutateWorkspace(ctx, workspace, "view.created", "view", nil, func(next *domain.Bootstrap) error {
		if len(next.Issues) != 0 || len(next.Comments) != 0 {
			t.Fatal("metadata callback received complete collections")
		}
		next.SavedViews = append(next.SavedViews, domain.SavedView{ID: "view", Name: "Scale view"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := repository.db.QueryRowContext(ctx, `SELECT data FROM issue_records WHERE workspace_key=? AND id=?`, workspace, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if string(raw) != "not-json" {
		t.Fatal("metadata mutation overwrote issue records")
	}
	if err := repository.SetLastWorkspace(ctx, data.Viewer.ID, workspace); err != nil {
		t.Fatal(err)
	}
}
