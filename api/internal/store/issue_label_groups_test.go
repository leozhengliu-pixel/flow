package store

import (
	"errors"
	"slices"
	"testing"

	"flow/api/internal/domain"
)

// A: L1 L2 (unstarted)   B: L1 (unstarted)   C: L3 (started)   D: none (completed)
// E: no label, sub-issue of A (started)
func labelGroupingFixture(t *testing.T) (*SQLiteStore, IssueRecordQuery) {
	t.Helper()
	repo, q, base := issuePointReadFixture(t)
	q.Access = nil
	todo := domain.WorkflowState{ID: "lg-todo", Name: "Todo", Type: "unstarted"}
	progress := domain.WorkflowState{ID: "lg-progress", Name: "In Progress", Type: "started"}
	done := domain.WorkflowState{ID: "lg-done", Name: "Done", Type: "completed"}
	issue := func(id string, state domain.WorkflowState, labels ...string) domain.Issue {
		item := base
		item.ID, item.Identifier, item.Title, item.State = id, "LG-"+id, id, state
		item.Labels, item.ParentID, item.SubIssueIDs = nil, nil, nil
		for _, label := range labels {
			item.Labels = append(item.Labels, domain.IssueLabel{ID: label})
		}
		return item
	}
	a, b, c, d, e := issue("lg-a", todo, "L1", "L2"), issue("lg-b", todo, "L1"), issue("lg-c", progress, "L3"), issue("lg-d", done), issue("lg-e", progress)
	parent := a.ID
	e.ParentID = &parent
	if err := repo.ImportIssues(t.Context(), q.Workspace, []domain.Issue{a, b, c, d, e}); err != nil {
		t.Fatal(err)
	}
	q.Filter = IssueFilter{Field: "id", Values: []string{"lg-a", "lg-b", "lg-c", "lg-d", "lg-e"}}
	return repo, q
}

func TestLabelGroupingIsMultiMembership(t *testing.T) {
	repo, q := labelGroupingFixture(t)
	counts := func(query IssueRecordQuery) map[string]int64 {
		t.Helper()
		groups, err := repo.QueryIssueGroups(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]int64{}
		for _, group := range groups {
			got[group.Value] = group.Count
		}
		return got
	}
	rows := func(query IssueRecordQuery, value string) []string {
		t.Helper()
		query.GroupValue = &value
		page, err := repo.QueryIssueRecords(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, item := range page.Items {
			ids = append(ids, item.ID)
		}
		slices.Sort(ids)
		return ids
	}
	equal := func(name string, got, want map[string]int64) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: got %v want %v", name, got, want)
		}
		for value, count := range want {
			if got[value] != count {
				t.Fatalf("%s: got %v want %v", name, got, want)
			}
		}
	}

	labels := q
	labels.GroupBy = "labels"
	equal("labels", counts(labels), map[string]int64{"L1": 2, "L2": 1, "L3": 1, "": 2})
	for value, want := range map[string][]string{"L1": {"lg-a", "lg-b"}, "L2": {"lg-a"}, "": {"lg-d", "lg-e"}} {
		if got := rows(labels, value); !slices.Equal(got, want) {
			t.Fatalf("labels %q: got %v want %v", value, got, want)
		}
	}

	// One label group's labels: an issue without any of them is in "No label".
	group := labels
	group.GroupLabelIDs = []string{"L1", "L2"}
	equal("label group", counts(group), map[string]int64{"L1": 2, "L2": 1, "": 3})
	if got := rows(group, ""); !slices.Equal(got, []string{"lg-c", "lg-d", "lg-e"}) {
		t.Fatalf("label group none: %v", got)
	}
	if got := rows(group, "L3"); len(got) != 0 {
		t.Fatalf("a label outside the group has no rows: %v", got)
	}

	// Nested: a sub-issue counts in each of its top-level row's label groups.
	nested := group
	nested.Nested = true
	equal("nested label group", counts(nested), map[string]int64{"L1": 3, "L2": 2, "": 2})
	walked, err := repo.nestedIssueGroupsByWalk(t.Context(), nested)
	if err != nil {
		t.Fatal(err)
	}
	moved, err := repo.nestedIssueGroupsByMoves(t.Context(), nested)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(walked, moved) {
		t.Fatalf("nested walk %v and corrected counts %v differ", walked, moved)
	}

	statusType := q
	statusType.GroupBy = "statusType"
	equal("status type", counts(statusType), map[string]int64{"unstarted": 2, "started": 2, "completed": 1})
	if got := rows(statusType, "started"); !slices.Equal(got, []string{"lg-c", "lg-e"}) {
		t.Fatalf("status type started: %v", got)
	}

	// Label metadata for scoping and ordering.
	if err := repo.MutateWorkspace(WithColdWork(t.Context(), "workspace fixture"), q.Workspace, "test.labels", "", nil, func(d *domain.Bootstrap) error {
		d.LabelGroups = append(d.LabelGroups, domain.LabelGroup{ID: "LG", Name: "Stage", ResourceType: "issue"})
		d.Labels = append(d.Labels, domain.IssueLabel{ID: "L1", Name: "One", GroupID: "LG"}, domain.IssueLabel{ID: "L2", Name: "Two", GroupID: "LG", Scope: "team-x"},
			domain.IssueLabel{ID: "L3", Name: "Three"}, domain.IssueLabel{ID: "LP", Name: "Project", GroupID: "LG", ResourceType: "project"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if ids, err := repo.LabelGroupIssueLabelIDs(t.Context(), q.Workspace, "LG"); err != nil || !slices.Equal(ids, []string{"L1", "L2"}) {
		t.Fatalf("label group labels %v %v", ids, err)
	}
	got, err := repo.IssueGroupLabels(t.Context(), q.Workspace, []string{"L1", "L2", "L3", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]IssueGroupLabel{"L1": {Name: "One", GroupID: "LG", GroupName: "Stage"}, "L2": {Name: "Two", Scope: "team-x", GroupID: "LG", GroupName: "Stage"}, "L3": {Name: "Three"}}; len(got) != len(want) || got["L1"] != want["L1"] || got["L2"] != want["L2"] || got["L3"] != want["L3"] {
		t.Fatalf("label metadata %+v want %+v", got, want)
	}

	// The label limit is bounded.
	tooMany := labels
	tooMany.GroupLabelIDs = make([]string, MaxIssueGroupLabels+1)
	if _, err := repo.flatIssueGroupsBySQL(t.Context(), tooMany); !errors.Is(err, ErrIssueQuery) {
		t.Fatalf("unbounded label group accepted: %v", err)
	}
}
