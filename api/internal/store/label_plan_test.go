package store

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// TestLabelDrivenQueryMatchesGenericPlan compares the label-index-driven row
// source with the same filter compiled without the optimisation (an OR
// wrapper is not drivable). It walks every page for every sort and covers
// visibility, nesting, archived states, multi-label filters and grouping.
func TestLabelDrivenQueryMatchesGenericPlan(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	runLabelEquivalenceSuite(t, repository, "label-equiv")
}

// TestLabelDrivenSourceThresholds checks both selectivity thresholds without
// importing 10k issues: the decision only reads issue_label_records.
func TestLabelDrivenSourceThresholds(t *testing.T) {
	ctx := context.Background()
	repository, workspace := perfWorkspace(t)
	key := workspace.Workspace.URLKey
	team := workspace.Teams[0].ID
	insertLabelRows := func(label string, count int) {
		for start := 0; start < count; start += 500 {
			end := min(start+500, count)
			tuples := []string{}
			args := []any{}
			for i := start; i < end; i++ {
				tuples = append(tuples, "(?,?,?)")
				args = append(args, key, fmt.Sprintf("threshold_issue_%d", i), label)
			}
			if _, err := repository.db.ExecContext(ctx, `INSERT INTO issue_label_records(workspace_key,issue_id,label_id) VALUES `+strings.Join(tuples, ","), args...); err != nil {
				t.Fatal(err)
			}
		}
	}
	insertLabelRows("label_threshold_10999", 9999)
	if _, _, _, driven := repository.labelDrivenIssueSource(ctx, IssueRecordQuery{Workspace: key, TeamIDs: []string{team}, Filter: IssueFilter{Field: "labels", Values: []string{"label_threshold_10999"}}}); !driven {
		t.Fatal("just below the team threshold must drive")
	}
	insertLabelRows("label_threshold_10001", 10001)
	if _, _, _, driven := repository.labelDrivenIssueSource(ctx, IssueRecordQuery{Workspace: key, TeamIDs: []string{team}, Filter: IssueFilter{Field: "labels", Values: []string{"label_threshold_10001"}}}); driven {
		t.Fatal("above the team threshold must fall back")
	}
	insertLabelRows("label_threshold_workspace_2001", 2001)
	if _, _, _, driven := repository.labelDrivenIssueSource(ctx, IssueRecordQuery{Workspace: key, Filter: IssueFilter{Field: "labels", Values: []string{"label_threshold_workspace_2001"}}}); driven {
		t.Fatal("above the workspace threshold must fall back")
	}
	insertLabelRows("label_threshold_workspace_2000", 2000)
	if _, _, _, driven := repository.labelDrivenIssueSource(ctx, IssueRecordQuery{Workspace: key, Filter: IssueFilter{Field: "labels", Values: []string{"label_threshold_workspace_2000"}}}); !driven {
		t.Fatal("at the workspace threshold must drive")
	}
}

type labelEquivalenceCase struct {
	name      string
	workspace string
	filter    IssueFilter
	teams     []string
	access    *IssueRecordAccess
	allowed   []string
	nested    bool
	archived  string
	groupBy   string
	groupVal  *string
	groupVals []string
	sort      string
	direction string
}

func runLabelEquivalenceSuite(t *testing.T, repository *SQLiteStore, prefix string) {
	t.Helper()
	smallKey, small := seedLabelEquivalenceWorkspace(t, repository, prefix+"-small", false)
	thresholdKey, threshold := seedLabelEquivalenceWorkspace(t, repository, prefix+"-threshold", true)
	selective := small.Labels[0].ID
	other := small.Labels[1].ID
	grouped := small.Labels[2].ID
	state0 := small.States[0].ID
	state1 := small.States[1].ID

	admin := &IssueRecordAccess{Admin: true, WorkspaceAdmin: true, VisibleTeamIDs: []string{"team_equiv_a", "team_equiv_private", "team_equiv_archived"}, ArchivedTeamIDs: []string{"team_equiv_archived"}}
	member := &IssueRecordAccess{UserID: small.Viewer.ID, WorkspaceID: small.Workspace.ID, VisibleTeamIDs: []string{"team_equiv_a"}, ArchivedTeamIDs: []string{"team_equiv_archived"}}

	label := func(ids ...string) IssueFilter {
		return IssueFilter{Field: "labels", Values: ids}
	}
	cases := []labelEquivalenceCase{
		{name: "team-selective", filter: label(selective), teams: []string{"team_equiv_a"}},
		{name: "workspace-selective", filter: label(selective)},
		{name: "multi-label-in", filter: label(selective, other)},
		{name: "multi-label-includes-all", filter: IssueFilter{Field: "labels", Operator: "includesall", Values: []string{selective, other}}},
		{name: "grouped-label", filter: label(grouped)},
		{name: "archived-default", filter: label(selective), archived: ""},
		{name: "archived-true", filter: label(selective), archived: "true"},
		{name: "archived-all", filter: label(selective), archived: "all"},
		{name: "member", filter: label(selective), access: member},
		{name: "member-with-archived-team", filter: label(selective), access: admin},
		{name: "allowed-teams", filter: label(selective), allowed: []string{"team_equiv_a"}},
		{name: "nested", filter: label(selective), teams: []string{"team_equiv_a"}, nested: true},
		{name: "group-status-value", filter: label(selective), teams: []string{"team_equiv_a"}, groupBy: "status", groupVal: &state0},
		{name: "group-status-values", filter: label(selective), teams: []string{"team_equiv_a"}, groupBy: "status", groupVals: []string{state0, state1}},
		{name: "group-priority", filter: label(selective), teams: []string{"team_equiv_a"}, groupBy: "priority"},
	}
	for _, sortName := range []string{"sortOrder", "priority", "createdAt", "updatedAt", "title", "customerCount", "customerImportantCount", "customerRevenue"} {
		for _, direction := range []string{"asc", "desc"} {
			cases = append(cases, labelEquivalenceCase{name: "sort-" + sortName + "-" + direction, filter: label(selective), teams: []string{"team_equiv_a"}, sort: sortName, direction: direction})
		}
	}
	for _, test := range cases {
		query := IssueRecordQuery{Workspace: smallKey, Filter: test.filter, TeamIDs: test.teams, Access: test.access, AllowedTeamIDs: test.allowed, Nested: test.nested, Archived: test.archived, GroupBy: test.groupBy, GroupValue: test.groupVal, GroupValues: test.groupVals, Sort: test.sort, Direction: test.direction}
		assertLabelEquivalence(t, repository, query)
	}
	// Broad labels at both scopes exercise the fallback threshold.
	broad := threshold.Labels[len(threshold.Labels)-2].ID
	assertLabelEquivalence(t, repository, IssueRecordQuery{Workspace: thresholdKey, Filter: label(broad)})
	assertLabelEquivalence(t, repository, IssueRecordQuery{Workspace: thresholdKey, Filter: label(broad), TeamIDs: []string{"team_equiv_a"}})
}

func assertLabelEquivalence(t *testing.T, repository *SQLiteStore, base IssueRecordQuery) {
	t.Helper()
	ctx := context.Background()
	const pageSize = 7
	driven := base
	driven.IncludeTotal, driven.Limit = true, pageSize
	generic := driven
	generic.Filter = IssueFilter{Or: []IssueFilter{base.Filter}}
	drivenIDs, genericIDs := []string{}, []string{}
	for {
		first, err := repository.QueryIssueRecords(ctx, driven)
		if err != nil {
			t.Fatalf("%v driven: %v", base.Filter, err)
		}
		second, err := repository.QueryIssueRecords(ctx, generic)
		if err != nil {
			t.Fatalf("%v generic: %v", base.Filter, err)
		}
		if first.Total != second.Total {
			t.Fatalf("%v total %d != %d", base.Filter, first.Total, second.Total)
		}
		if first.HasMore != second.HasMore {
			t.Fatalf("%v hasMore %v != %v", base.Filter, first.HasMore, second.HasMore)
		}
		for i := range first.Items {
			if i >= len(second.Items) || first.Items[i].ID != second.Items[i].ID {
				t.Fatalf("%v page item %d differs: %v vs %v", base.Filter, i, issueIDs(first.Items), issueIDs(second.Items))
			}
		}
		if len(first.Descendants) > 0 || len(second.Descendants) > 0 {
			if !slices.Equal(issueIDs(first.Descendants), issueIDs(second.Descendants)) {
				t.Fatalf("%v descendants differ", base.Filter)
			}
		}
		drivenIDs = append(drivenIDs, issueIDs(first.Items)...)
		genericIDs = append(genericIDs, issueIDs(second.Items)...)
		if !first.HasMore {
			break
		}
		driven.Cursor, generic.Cursor = first.NextCursor, second.NextCursor
		if driven.Cursor == "" || generic.Cursor == "" {
			t.Fatalf("%v missing next cursor", base.Filter)
		}
	}
	if !slices.Equal(drivenIDs, genericIDs) {
		t.Fatalf("%v pages differ: %v vs %v", base.Filter, drivenIDs, genericIDs)
	}
	seen := map[string]bool{}
	for _, id := range drivenIDs {
		if seen[id] {
			t.Fatalf("%v returned duplicate row %s: %v", base.Filter, id, drivenIDs)
		}
		seen[id] = true
	}
}

func issueIDs(issues []domain.Issue) []string {
	result := make([]string, len(issues))
	for i, issue := range issues {
		result[i] = issue.ID
	}
	return result
}

func seedLabelEquivalenceWorkspace(t *testing.T, repository *SQLiteStore, key string, threshold bool) (string, domain.Bootstrap) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	created, err := repository.CreateWorkspaceWith(ctx, "Label equivalence", key, "us", func(data *domain.Bootstrap) error {
		data.Teams = []domain.Team{
			{ID: "team_equiv_a", Name: "A", Key: "EQA", Color: "#5E6AD2", CreatedAt: &now},
			{ID: "team_equiv_private", Name: "Private", Key: "EQP", Color: "#5E6AD2", Private: true, CreatedAt: &now},
			{ID: "team_equiv_archived", Name: "Archived", Key: "EQZ", Color: "#5E6AD2", ArchivedAt: &now, CreatedAt: &now},
		}
		data.Labels = append(data.Labels,
			domain.IssueLabel{ID: "label_equiv_selective", Name: "Selective", Color: "#4EA7FC", Scope: "Workspace", ResourceType: "issue", GroupID: "group_equiv", CreatedAt: now},
			domain.IssueLabel{ID: "label_equiv_other", Name: "Other", Color: "#BB87FC", Scope: "Workspace", ResourceType: "issue", CreatedAt: now},
			domain.IssueLabel{ID: "label_equiv_grouped", Name: "Grouped", Color: "#18B99A", Scope: "Workspace", ResourceType: "issue", GroupID: "group_equiv", CreatedAt: now},
			domain.IssueLabel{ID: "label_equiv_broad", Name: "Broad", Color: "#F2994A", Scope: "Workspace", ResourceType: "issue", CreatedAt: now})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	teams := created.Teams
	issues := []domain.Issue{}
	add := func(id string, index int, team domain.Team, labels []domain.IssueLabel, archived bool) {
		issue := domain.Issue{
			ID: id, Identifier: fmt.Sprintf("EQ-%s-%d", key, index), Number: index + 1,
			Title: fmt.Sprintf("Equivalence issue %d", index), Priority: index % 5, SortOrder: float64(index),
			Team: team, State: created.States[index%len(created.States)], Creator: created.Viewer,
			Labels: labels, CreatedAt: now.Add(-time.Duration(index) * time.Hour), UpdatedAt: now.Add(-time.Duration(index) * time.Minute),
		}
		if archived {
			at := now.Add(time.Hour)
			issue.ArchivedAt = &at
		}
		issues = append(issues, issue)
	}
	limit := 60
	if threshold {
		limit = 2100
	}
	for i := 0; i < limit; i++ {
		team := teams[i%2]
		if i%10 == 0 {
			team = teams[2]
		}
		labels := []domain.IssueLabel{}
		if threshold {
			labels = append(labels, created.Labels[3])
			if i < 40 {
				labels = append(labels, created.Labels[0])
			}
		} else {
			if i%4 == 0 {
				labels = append(labels, created.Labels[0])
			}
			if i%3 == 0 {
				labels = append(labels, created.Labels[1])
			}
			if i%7 == 0 {
				labels = append(labels, created.Labels[2])
			}
		}
		add(fmt.Sprintf("%s_issue_%03d", key, i), i, team, labels, !threshold && i%10 == 0)
	}
	if !threshold {
		// Parent and children share the selective label so Nested has work.
		parent := issues[1]
		parent.Labels = append(parent.Labels, created.Labels[0])
		issues[1] = parent
		for i := 2; i <= 5; i++ {
			issues[i].ParentID = &parent.ID
			issues[i].Labels = append(issues[i].Labels, created.Labels[0])
		}
		shared := domain.Issue{ID: key + "_shared", Identifier: "EQ-SHARED", Title: "Shared private issue", Team: teams[1], State: created.States[0], Creator: created.Viewer, Labels: []domain.IssueLabel{created.Labels[0]}, CreatedAt: now, UpdatedAt: now,
			Permissions: []domain.IssuePermission{{ID: "perm_" + key, IssueID: key + "_shared", SubjectType: "user", SubjectID: created.Viewer.ID, Role: "viewer"}}}
		issues = append(issues, shared)
		child := domain.Issue{ID: key + "_shared_child", Identifier: "EQ-SHARED-C", Title: "Shared child", Team: teams[1], State: created.States[0], Creator: created.Viewer, Labels: []domain.IssueLabel{created.Labels[0]}, ParentID: &shared.ID, CreatedAt: now, UpdatedAt: now}
		issues = append(issues, child)
		hidden := domain.Issue{ID: key + "_private_hidden", Identifier: "EQ-HIDDEN", Title: "Private hidden issue", Team: teams[1], State: created.States[0], Creator: created.Viewer, Labels: []domain.IssueLabel{created.Labels[0]}, CreatedAt: now, UpdatedAt: now}
		issues = append(issues, hidden)
	}
	for start := 0; start < len(issues); start += 500 {
		if err := repository.ImportIssues(ctx, key, issues[start:min(start+500, len(issues))]); err != nil {
			t.Fatal(err)
		}
	}
	return key, repository.Bootstrap()
}
