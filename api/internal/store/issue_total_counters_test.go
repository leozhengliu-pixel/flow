package store

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// TestIssueTotalUsesMaintainedCounters covers the fast total path: a plain
// workspace or team count is answered by issue_scope_counts, not COUNT(*).
func TestIssueTotalUsesMaintainedCounters(t *testing.T) {
	ctx := context.Background()
	repository, workspace := perfWorkspace(t)
	key := workspace.Workspace.URLKey
	issues := make([]domain.Issue, 120)
	for i := range issues {
		issues[i] = perfIssue(workspace, i)
		if i%6 == 0 {
			at := time.Now().UTC()
			issues[i].ArchivedAt = &at
		}
	}
	if err := repository.ImportIssues(ctx, key, issues); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		archived string
		want     int64
	}{{"", 100}, {"false", 100}, {"true", 20}, {"all", 120}} {
		page, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: key, Archived: test.archived, IncludeTotal: true, Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != test.want {
			t.Fatalf("archived=%q total=%d want %d", test.archived, page.Total, test.want)
		}
	}
	total, ok, err := repository.issueRecordTotal(ctx, IssueRecordQuery{Workspace: key})
	if err != nil || !ok || total != 100 {
		t.Fatalf("workspace counters total=%d ok=%v err=%v", total, ok, err)
	}
	total, ok, err = repository.issueRecordTotal(ctx, IssueRecordQuery{Workspace: key, TeamIDs: []string{workspace.Teams[0].ID}, Archived: "all"})
	if err != nil || !ok || total != 120 {
		t.Fatalf("team counters total=%d ok=%v err=%v", total, ok, err)
	}
	if _, ok, err := repository.issueRecordTotal(ctx, IssueRecordQuery{Workspace: key, Filter: IssueFilter{Field: "priority", Values: []string{"1"}}}); err != nil || ok {
		t.Fatalf("filtered query must not use the counters (ok=%v err=%v)", ok, err)
	}
}

// TestIssueTotalNestedMatchesCount covers nested lists: children whose parent
// also matches are not rows of their own, so the plain counters cannot answer
// the total.
func TestIssueTotalNestedMatchesCount(t *testing.T) {
	ctx := context.Background()
	repository, workspace := perfWorkspace(t)
	key := workspace.Workspace.URLKey
	issues := []domain.Issue{}
	parent := perfIssue(workspace, 1)
	parent.ID, parent.Identifier, parent.Title = "nested-parent", "NEST-1", "Nested parent"
	parent.SubIssueIDs = []string{"nested-child-1", "nested-child-2"}
	issues = append(issues, parent)
	for i, id := range parent.SubIssueIDs {
		child := perfIssue(workspace, 10+i)
		child.ID, child.Identifier, child.Title = id, "NEST-C"+string(rune('1'+i)), "Nested child"
		child.ParentID = &parent.ID
		issues = append(issues, child)
	}
	if err := repository.ImportIssues(ctx, key, issues); err != nil {
		t.Fatal(err)
	}
	query := IssueRecordQuery{Workspace: key, TeamIDs: []string{workspace.Teams[0].ID}, Nested: true, IncludeTotal: true, Limit: 10}
	page, err := repository.QueryIssueRecords(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 {
		t.Fatalf("nested total=%d want 1 (the parent only)", page.Total)
	}
	want, err := repository.countIssueRecordsSQL(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != want {
		t.Fatalf("nested total %d != count %d", page.Total, want)
	}
	if _, ok, err := repository.issueRecordTotal(ctx, query); err != nil || ok {
		t.Fatalf("nested query must fall back to COUNT(*) (ok=%v err=%v)", ok, err)
	}
}

// TestIssueTotalHidesArchivedTeams covers deleted teams in their restore
// window: they stay hidden from admins, so a workspace total must subtract
// their counters and a team total for one of them is zero.
func TestIssueTotalHidesArchivedTeams(t *testing.T) {
	ctx := context.Background()
	repository, workspace := perfWorkspace(t)
	key := workspace.Workspace.URLKey
	archivedTeam := domain.Team{ID: "team_archived_window", Name: "Gone", Key: "GON"}
	now := time.Now().UTC()
	archivedTeam.ArchivedAt = &now
	if err := repository.MutateWorkspace(ctx, key, "team.archived", archivedTeam.ID, nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, archivedTeam)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	workspace = repository.Bootstrap()
	issues := []domain.Issue{}
	for i := 0; i < 3; i++ {
		issue := perfIssue(workspace, i)
		issue.Team = workspace.Teams[0]
		issues = append(issues, issue)
	}
	for i := 0; i < 2; i++ {
		issue := perfIssue(workspace, 10+i)
		issue.Team = archivedTeam
		issues = append(issues, issue)
	}
	if err := repository.ImportIssues(ctx, key, issues); err != nil {
		t.Fatal(err)
	}
	access := &IssueRecordAccess{Admin: true, WorkspaceAdmin: true, ArchivedTeamIDs: []string{archivedTeam.ID}}
	for _, test := range []struct {
		name  string
		query IssueRecordQuery
		want  int64
	}{
		{"workspace", IssueRecordQuery{Workspace: key, Access: access}, 3},
		{"archived-team", IssueRecordQuery{Workspace: key, Access: access, TeamIDs: []string{archivedTeam.ID}}, 0},
		{"both-teams", IssueRecordQuery{Workspace: key, Access: access, TeamIDs: []string{workspace.Teams[0].ID, archivedTeam.ID}}, 3},
	} {
		query := test.query
		query.IncludeTotal, query.Limit = true, 10
		page, err := repository.QueryIssueRecords(ctx, query)
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		if page.Total != test.want {
			t.Fatalf("%s total=%d want %d", test.name, page.Total, test.want)
		}
		fast, ok, err := repository.issueRecordTotal(ctx, query)
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		if ok && fast != test.want {
			t.Fatalf("%s fast total=%d want %d", test.name, fast, test.want)
		}
	}
}

// TestIssueTotalFallsBackForIssueIDs covers IssueIDs without
// RestrictToIssueIDs: issueRecordWhere still filters by them.
func TestIssueTotalFallsBackForIssueIDs(t *testing.T) {
	ctx := context.Background()
	repository, workspace := perfWorkspace(t)
	key := workspace.Workspace.URLKey
	issues := []domain.Issue{perfIssue(workspace, 1), perfIssue(workspace, 2), perfIssue(workspace, 3)}
	if err := repository.ImportIssues(ctx, key, issues); err != nil {
		t.Fatal(err)
	}
	query := IssueRecordQuery{Workspace: key, IssueIDs: []string{issues[0].ID, issues[1].ID}, IncludeTotal: true, Limit: 10}
	if _, ok, err := repository.issueRecordTotal(ctx, query); err != nil || ok {
		t.Fatalf("IssueIDs query must fall back to COUNT(*) (ok=%v err=%v)", ok, err)
	}
	page, err := repository.QueryIssueRecords(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	want, err := repository.countIssueRecordsSQL(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != want || page.Total != 2 {
		t.Fatalf("total=%d count=%d want 2", page.Total, want)
	}
}

type totalEquivalenceCase struct {
	name   string
	fields []string
	apply  func(*IssueRecordQuery)
}

// TestIssueRecordTotalMatchesCountSQL checks the counter fast path against
// the plain COUNT(*) the paged query falls back to for every predicate
// IssueRecordQuery carries. It also fails when a field is added to
// IssueRecordQuery without being classified and covered.
func TestIssueRecordTotalMatchesCountSQL(t *testing.T) {
	ctx := context.Background()
	repository, workspace := totalEquivalenceWorkspace(t)
	key := workspace.Workspace.URLKey
	teamA, teamB := workspace.Teams[0].ID, workspace.Teams[1].ID
	teamArchived, teamPrivate := workspace.Teams[2].ID, workspace.Teams[3].ID
	state0, state1 := workspace.States[0].ID, workspace.States[1].ID
	labelA := workspace.Labels[0].ID
	projectID, releaseID := "project_eq", "release_eq"
	admin := &IssueRecordAccess{Admin: true, WorkspaceAdmin: true, VisibleTeamIDs: []string{teamA, teamB, teamArchived, teamPrivate}}
	adminWithArchived := &IssueRecordAccess{Admin: true, WorkspaceAdmin: true, VisibleTeamIDs: []string{teamA, teamB, teamArchived, teamPrivate}, ArchivedTeamIDs: []string{teamArchived}}
	member := &IssueRecordAccess{UserID: workspace.Viewer.ID, WorkspaceID: workspace.Workspace.ID, VisibleTeamIDs: []string{teamA, teamB}, ArchivedTeamIDs: []string{teamArchived}}

	cases := []totalEquivalenceCase{
		{name: "baseline", fields: []string{"Workspace"}, apply: func(*IssueRecordQuery) {}},
		{name: "nested", fields: []string{"Nested"}, apply: func(q *IssueRecordQuery) { q.Nested = true }},
		{name: "search-text", fields: []string{"SearchText"}, apply: func(q *IssueRecordQuery) { q.SearchText = "Needle" }},
		{name: "text", fields: []string{"Text"}, apply: func(q *IssueRecordQuery) { q.Text = "EQ-" }},
		{name: "filter", fields: []string{"Filter"}, apply: func(q *IssueRecordQuery) {
			q.Filter = IssueFilter{Field: "priority", Values: []string{"1"}}
		}},
		{name: "filter-label", fields: []string{"Filter"}, apply: func(q *IssueRecordQuery) {
			q.Filter = IssueFilter{Field: "labels", Values: []string{labelA}}
		}},
		{name: "team", fields: []string{"TeamIDs"}, apply: func(q *IssueRecordQuery) { q.TeamIDs = []string{teamA} }},
		{name: "project", fields: []string{"ProjectIDs"}, apply: func(q *IssueRecordQuery) { q.ProjectIDs = []string{projectID} }},
		{name: "state", fields: []string{"StateIDs"}, apply: func(q *IssueRecordQuery) { q.StateIDs = []string{state0} }},
		{name: "release", fields: []string{"ReleaseIDs"}, apply: func(q *IssueRecordQuery) { q.ReleaseIDs = []string{releaseID} }},
		{name: "issue-ids", fields: []string{"IssueIDs"}, apply: func(q *IssueRecordQuery) { q.IssueIDs = []string{"issue_eq_01", "issue_eq_02"} }},
		{name: "restrict-issue-ids", fields: []string{"IssueIDs", "RestrictToIssueIDs"}, apply: func(q *IssueRecordQuery) {
			q.IssueIDs = []string{"issue_eq_01", "issue_eq_02"}
			q.RestrictToIssueIDs = true
		}},
		{name: "archived-false", fields: []string{"Archived"}, apply: func(q *IssueRecordQuery) { q.Archived = "false" }},
		{name: "archived-true", fields: []string{"Archived"}, apply: func(q *IssueRecordQuery) { q.Archived = "true" }},
		{name: "archived-all", fields: []string{"Archived"}, apply: func(q *IssueRecordQuery) { q.Archived = "all" }},
		{name: "access-admin", fields: []string{"Access"}, apply: func(q *IssueRecordQuery) { q.Access = admin }},
		{name: "access-member", fields: []string{"Access"}, apply: func(q *IssueRecordQuery) { q.Access = member }},
		{name: "access-admin-archived-team", fields: []string{"Access"}, apply: func(q *IssueRecordQuery) { q.Access = adminWithArchived }},
		{name: "allowed-teams", fields: []string{"AllowedTeamIDs"}, apply: func(q *IssueRecordQuery) { q.AllowedTeamIDs = []string{teamA, teamB} }},
		{name: "group-by", fields: []string{"GroupBy"}, apply: func(q *IssueRecordQuery) { q.GroupBy = "status" }},
		{name: "group-value", fields: []string{"GroupBy", "GroupValue"}, apply: func(q *IssueRecordQuery) {
			q.GroupBy, q.GroupValue = "status", &state0
		}},
		{name: "group-values", fields: []string{"GroupBy", "GroupValues"}, apply: func(q *IssueRecordQuery) {
			q.GroupBy, q.GroupValues = "status", []string{state0, state1}
		}},
		{name: "group-cases", fields: []string{"GroupBy", "GroupCases"}, apply: func(q *IssueRecordQuery) {
			q.GroupBy = "cases"
			q.GroupCases = []IssueGroupCase{{Value: "urgent", Filter: IssueFilter{Field: "priority", Values: []string{"1"}}}}
		}},
		{name: "archived-team-filter", fields: []string{"TeamIDs", "Access"}, apply: func(q *IssueRecordQuery) {
			q.TeamIDs, q.Access = []string{teamArchived}, adminWithArchived
		}},
		{name: "workspace-subtracts-archived-team", fields: []string{"Access"}, apply: func(q *IssueRecordQuery) { q.Access = adminWithArchived }},
	}

	covered := map[string]bool{}
	for _, test := range cases {
		query := IssueRecordQuery{Workspace: key, IncludeTotal: true, Limit: 10}
		test.apply(&query)
		if err := repository.applyIssueRecordScope(ctx, &query); err != nil {
			t.Fatalf("%s: scope: %v", test.name, err)
		}
		want, err := repository.countIssueRecordsSQL(ctx, query)
		if err != nil {
			t.Fatalf("%s: count: %v", test.name, err)
		}
		fast, ok, err := repository.issueRecordTotal(ctx, query)
		if err != nil {
			t.Fatalf("%s: fast total: %v", test.name, err)
		}
		if ok && fast != want {
			t.Fatalf("%s: fast total %d != count %d", test.name, fast, want)
		}
		page, err := repository.QueryIssueRecords(ctx, query)
		if err != nil {
			t.Fatalf("%s: page: %v", test.name, err)
		}
		if page.Total != want {
			t.Fatalf("%s: page total %d != count %d", test.name, page.Total, want)
		}
		for _, field := range test.fields {
			covered[field] = true
		}
	}

	// Classify every field so a new predicate cannot silently skip the
	// equivalence check.
	predicateFields := map[string]bool{
		"Workspace": true, "Filter": true, "TeamIDs": true, "ProjectIDs": true,
		"StateIDs": true, "ReleaseIDs": true, "IssueIDs": true, "RestrictToIssueIDs": true,
		"AllowedTeamIDs": true, "Access": true, "Text": true, "SearchText": true,
		"Archived": true, "GroupBy": true, "GroupValue": true, "GroupValues": true,
		"GroupCases": true, "Nested": true,
	}
	nonPredicateFields := map[string]bool{
		"Sort": true, "Direction": true, "Cursor": true, "Limit": true,
		"IncludeTotal": true, "Summary": true, "IncludeDescription": true,
		"Reverse": true, "Cursors": true,
	}
	typ := reflect.TypeOf(IssueRecordQuery{})
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if predicateFields[name] == nonPredicateFields[name] {
			t.Fatalf("IssueRecordQuery.%s is not classified as a predicate or non-predicate field", name)
		}
	}
	for field := range predicateFields {
		if !covered[field] {
			t.Fatalf("predicate field %s has no total-equivalence case", field)
		}
	}
}

func totalEquivalenceWorkspace(t *testing.T) (*SQLiteStore, domain.Bootstrap) {
	t.Helper()
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	now := time.Now().UTC()
	created, err := repository.CreateWorkspaceWith(context.Background(), "Total equivalence", "perf-total-equivalence", "us", func(data *domain.Bootstrap) error {
		data.Teams = []domain.Team{
			{ID: "team_eq_a", Name: "A", Key: "EQA", Color: "#5E6AD2", CreatedAt: &now},
			{ID: "team_eq_b", Name: "B", Key: "EQB", Color: "#5E6AD2", CreatedAt: &now},
			{ID: "team_eq_archived", Name: "Archived", Key: "EQZ", Color: "#5E6AD2", CreatedAt: &now, ArchivedAt: &now},
			{ID: "team_eq_private", Name: "Private", Key: "EQP", Color: "#5E6AD2", CreatedAt: &now, Private: true},
		}
		data.Labels = append(data.Labels,
			domain.IssueLabel{ID: "label_eq_a", Name: "Equivalence A", Color: "#4EA7FC", Scope: "Workspace", ResourceType: "issue", CreatedAt: now},
			domain.IssueLabel{ID: "label_eq_b", Name: "Equivalence B", Color: "#BB87FC", Scope: "Workspace", ResourceType: "issue", CreatedAt: now})
		data.Projects = append(data.Projects, domain.Project{ID: "project_eq", Name: "Equivalence Project", SlugID: "equivalence-project", Color: "#5E6AD2", Status: data.ProjectStatuses[0], TeamIDs: []string{"team_eq_a"}, CreatedAt: now, UpdatedAt: now})
		data.Cycles = append(data.Cycles, domain.Cycle{ID: "cycle_eq", Number: 1, Name: "Equivalence Cycle", TeamID: "team_eq_a", StartsAt: now, EndsAt: now.AddDate(0, 0, 14), Status: "current", CreatedAt: now, UpdatedAt: now})
		data.Releases = append(data.Releases, domain.Release{ID: "release_eq", Name: "Equivalence Release", IssueIDs: []string{"issue_eq_01", "issue_eq_02"}, CreatedAt: now, UpdatedAt: now})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	key := created.Workspace.URLKey
	teams := created.Teams
	issues := make([]domain.Issue, 0, 40)
	for i := 0; i < 30; i++ {
		issue := domain.Issue{
			ID: fmt.Sprintf("issue_eq_%02d", i), Identifier: fmt.Sprintf("EQ-%d", i+1), Number: i + 1,
			Title: "Equivalence issue", Priority: i % 5, SortOrder: float64(i),
			Team: teams[i%2], State: created.States[i%3], Creator: created.Viewer,
			Labels: []domain.IssueLabel{created.Labels[i%2]}, SubscriberIDs: []string{created.Viewer.ID},
			CreatedAt: now.Add(-time.Duration(i) * time.Hour), UpdatedAt: now.Add(-time.Duration(i) * time.Minute),
		}
		if i == 5 {
			issue.Title = "Needle equivalence issue"
		}
		if i%2 == 0 {
			issue.Project = &domain.ProjectSummary{ID: "project_eq", Name: "Equivalence Project", Color: "#5E6AD2"}
		}
		if i%3 == 0 {
			cycle := "cycle_eq"
			issue.CycleID = &cycle
		}
		if i%7 == 0 {
			archived := now.Add(time.Hour)
			issue.ArchivedAt = &archived
		}
		if i >= 20 {
			parent := "issue_eq_01"
			issue.ParentID = &parent
		}
		issues = append(issues, issue)
	}
	for i := 0; i < 2; i++ {
		issue := domain.Issue{ID: fmt.Sprintf("issue_archived_%d", i), Identifier: fmt.Sprintf("ARC-%d", i+1), Number: 100 + i, Title: "Archived team issue", Team: teams[2], State: created.States[0], Creator: created.Viewer, CreatedAt: now, UpdatedAt: now}
		issues = append(issues, issue)
	}
	issues = append(issues, domain.Issue{ID: "issue_private_0", Identifier: "PRV-1", Number: 200, Title: "Private team issue", Team: teams[3], State: created.States[0], Creator: created.Viewer, CreatedAt: now, UpdatedAt: now})
	if err := repository.ImportIssues(context.Background(), key, issues); err != nil {
		t.Fatal(err)
	}
	return repository, repository.Bootstrap()
}
