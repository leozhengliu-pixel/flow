package store

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
)

func TestNestedIssueQuery(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	data := repository.Bootstrap()
	testNestedIssueQueryContract(t, repository, data.Workspace.URLKey, data.Workspace.ID, data.Issues[0])
}

// testNestedIssueQueryContract runs on SQLite and (integration) MySQL/Postgres.
//
//	P1 todo            C1 progress (child of P1)   G1 done (child of C1)
//	C2 todo (P1)       CH todo, hidden team (P1)    P2 todo, other project
//	C3 progress (P2)   F0..F149 todo                PL todo, last page
//	CL progress (PL), sorts first in its status
func testNestedIssueQueryContract(t *testing.T, repository *SQLiteStore, workspace, workspaceID string, base domain.Issue) {
	t.Helper()
	ctx := context.Background()
	team := base.Team
	hidden := team
	hidden.ID = "nest-hidden-team"
	todo := domain.WorkflowState{ID: "nest-todo", Name: "Todo", Type: "unstarted"}
	progress := domain.WorkflowState{ID: "nest-progress", Name: "In Progress", Type: "started"}
	done := domain.WorkflowState{ID: "nest-done", Name: "Done", Type: "completed"}
	projectA, projectB := &domain.ProjectSummary{ID: "nest-project-a"}, &domain.ProjectSummary{ID: "nest-project-b"}
	issue := func(id string, state domain.WorkflowState, sort float64, parent string) domain.Issue {
		item := base
		item.ID, item.Identifier, item.Title = id, "NEST-"+id, id
		item.Team, item.State, item.SortOrder, item.Project = team, state, sort, projectA
		item.Labels, item.Permissions, item.ParentID, item.SubIssueIDs, item.ArchivedAt = nil, nil, nil, nil, nil
		if parent != "" {
			item.ParentID = &parent
		}
		return item
	}
	p1 := issue("p1", todo, 1, "")
	p1.Labels = []domain.IssueLabel{{ID: "nest-label"}}
	c1, g1, c2 := issue("c1", progress, 5, "p1"), issue("g1", done, 1, "c1"), issue("c2", todo, 3, "p1")
	ch := issue("ch", todo, 2, "p1")
	ch.Team = hidden
	p2 := issue("p2", todo, 4, "")
	p2.Project = projectB
	c3 := issue("c3", progress, 2, "p2")
	issues := []domain.Issue{p1, c1, g1, c2, ch, p2, c3}
	for i := 0; i < 150; i++ {
		issues = append(issues, issue(fmt.Sprintf("f%03d", i), todo, float64(10+i), ""))
	}
	issues = append(issues, issue("pl", todo, 500, ""), issue("cl", progress, 0, "pl"))
	if err := repository.ImportIssues(ctx, workspace, issues); err != nil {
		t.Fatal(err)
	}
	access := &IssueRecordAccess{UserID: "nest-viewer", WorkspaceID: workspaceID, VisibleTeamIDs: []string{team.ID}}
	scope := IssueFilter{Field: "project", Values: []string{projectA.ID}}
	query := func(nested bool, groupBy string, filter IssueFilter) IssueRecordQuery {
		return IssueRecordQuery{Workspace: workspace, Access: access, Filter: filter, GroupBy: groupBy, Nested: nested, Summary: true}
	}
	counts := func(q IssueRecordQuery) map[string]int64 {
		t.Helper()
		groups, err := repository.QueryIssueGroups(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		result := map[string]int64{}
		for _, group := range groups {
			result[group.Value] = group.Count
		}
		return result
	}
	ids := func(items []domain.Issue) []string {
		result := []string{}
		for _, item := range items {
			result = append(result, item.ID)
		}
		return result
	}
	page := func(q IssueRecordQuery, group, cursor string) IssueRecordPage {
		t.Helper()
		q.GroupValue, q.Cursor, q.Limit = &group, cursor, 100
		result, err := repository.QueryIssueRecords(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	fillers := func(from, to int) []string {
		result := []string{}
		for i := from; i < to; i++ {
			result = append(result, fmt.Sprintf("f%03d", i))
		}
		return result
	}

	// Without nested the rows stay in their own groups (unchanged contract).
	if got := counts(query(false, "status", scope)); got[todo.ID] != 153 || got[progress.ID] != 3 || got[done.ID] != 1 {
		t.Fatalf("flat counts %v", got)
	}
	flat := page(query(false, "status", scope), progress.ID, "")
	if !slices.Equal(ids(flat.Items), []string{"cl", "c3", "c1"}) || flat.Descendants != nil {
		t.Fatalf("flat page %v %v", ids(flat.Items), ids(flat.Descendants))
	}

	// Nested: children count in their top-level row's group; the hidden
	// team's child is neither counted nor returned.
	if got := counts(query(true, "status", scope)); len(got) != 2 || got[todo.ID] != 156 || got[progress.ID] != 1 {
		t.Fatalf("nested counts %v", got)
	}
	first := page(query(true, "status", scope), todo.ID, "")
	if want := append([]string{"p1"}, fillers(0, 99)...); !slices.Equal(ids(first.Items), want) || !first.HasMore {
		t.Fatalf("nested first page %v", ids(first.Items))
	}
	if got := ids(first.Descendants); !slices.Equal(got, []string{"g1", "c2", "c1"}) {
		t.Fatalf("nested descendants %v", got)
	}
	second := page(query(true, "status", scope), todo.ID, first.NextCursor)
	if want := append(fillers(99, 150), "pl"); !slices.Equal(ids(second.Items), want) || second.HasMore {
		t.Fatalf("nested second page %v", ids(second.Items))
	}
	// A child that sorts first in another group comes with its parent's page.
	if got := ids(second.Descendants); !slices.Equal(got, []string{"cl"}) {
		t.Fatalf("descendants across pages and groups %v", got)
	}
	// A child whose parent is outside the result stays a row of its own group.
	nestedProgress := page(query(true, "status", scope), progress.ID, "")
	if !slices.Equal(ids(nestedProgress.Items), []string{"c3"}) || len(nestedProgress.Descendants) != 0 {
		t.Fatalf("nested progress %v %v", ids(nestedProgress.Items), ids(nestedProgress.Descendants))
	}
	totalQuery := query(true, "status", scope)
	totalQuery.IncludeTotal = true
	if total := page(totalQuery, todo.ID, "").Total; total != 152 {
		t.Fatalf("nested total counts top-level rows: %d", total)
	}

	// A filtered-out middle level: its child becomes top-level in its own group.
	open := IssueFilter{And: []IssueFilter{scope, {Field: "statusType", Operator: "notIn", Values: []string{"started"}}}}
	if got := counts(query(true, "status", open)); len(got) != 2 || got[todo.ID] != 153 || got[done.ID] != 1 {
		t.Fatalf("filtered middle level counts %v", got)
	}
	if got := page(query(true, "status", open), done.ID, ""); !slices.Equal(ids(got.Items), []string{"g1"}) {
		t.Fatalf("filtered middle level %v", ids(got.Items))
	}
	if got := page(query(true, "status", open), todo.ID, ""); !slices.Equal(ids(got.Descendants), []string{"c2"}) {
		t.Fatalf("filtered middle level descendants %v", ids(got.Descendants))
	}

	// Other groupings: none, an attribute (label) and a column.
	if got := counts(query(true, "none", scope)); got["all"] != 157 || len(got) != 1 {
		t.Fatalf("nested none %v", got)
	}
	if got := counts(query(true, "label", scope)); got["nest-label"] != 4 || got[""] != 153 {
		t.Fatalf("nested label groups %v", got)
	}
	if got := counts(query(true, "priority", scope)); got[fmt.Sprint(base.Priority)] != 157 {
		t.Fatalf("nested priority groups %v", got)
	}
	if got := counts(query(true, "customer", scope)); got[""] != 157 || len(got) != 1 {
		t.Fatalf("nested customer groups %v", got)
	}
	// Filters with relation subqueries apply to the parent check and every level.
	labelled := IssueFilter{And: []IssueFilter{scope, {Field: "labels", Operator: "notIn", Values: []string{"nest-label"}}}}
	if got := counts(query(true, "status", labelled)); got[todo.ID] != 153 || got[progress.ID] != 3 || got[done.ID] != 0 {
		t.Fatalf("nested label filter counts %v", got)
	}

	// Admin access sees the private team's child, nested under its parent.
	admin := query(true, "status", scope)
	admin.Access = nil
	if got := counts(admin); got[todo.ID] != 157 {
		t.Fatalf("admin nested counts %v", got)
	}
	if got := ids(page(admin, todo.ID, "").Descendants); !slices.Equal(got, []string{"g1", "ch", "c2", "c1"}) {
		t.Fatalf("admin descendants %v", got)
	}
}
