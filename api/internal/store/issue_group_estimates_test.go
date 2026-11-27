package store

import (
	"context"
	"database/sql"
	"maps"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
)

func TestIssueGroupEstimatePoints(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	data := repository.Bootstrap()
	testIssueGroupEstimatePointsContract(t, repository, data.Workspace.URLKey, data.Workspace.ID, data.Issues[0])
}

// testIssueGroupEstimatePointsContract runs on SQLite and (integration)
// MySQL/Postgres.
//
//	est-fib   Fibonacci, zero not allowed, unestimated count 1
//	est-zero  Exponential, zero allowed, unestimated count 0
//	est-none  estimates not used
//	est-child inherits est-zero's estimation (its own scale is off)
//	est-secret private Fibonacci team the outsider cannot read
//
//	f1 fib 3 todo L1 L2   f2 fib - todo L1      f3 fib 0 progress
//	z1 zero 0 todo L2 p4  z2 zero - progress p4 z3 zero 8 progress
//	n1 none 5 todo L1 p4  n2 none - done        c1 child 2 todo
//	s1 secret 5 todo L1
func testIssueGroupEstimatePointsContract(t *testing.T, repository *SQLiteStore, workspace, workspaceID string, base domain.Issue) {
	t.Helper()
	ctx := context.Background()
	off := false
	teams := map[string]domain.TeamSettings{
		"est-fib":    {EstimateType: "fibonacci"},
		"est-zero":   {EstimateType: "exponential", EstimateAllowZero: true, EstimateCountUnestimated: &off},
		"est-none":   {EstimateType: "notUsed"},
		"est-child":  {EstimateType: "notUsed", InheritIssueEstimation: true, ParentTeamID: "est-zero"},
		"est-secret": {EstimateType: "fibonacci"},
	}
	if err := repository.MutateWorkspace(WithColdWork(ctx, "workspace fixture"), workspace, "test.teams", "", nil, func(d *domain.Bootstrap) error {
		if d.TeamSettings == nil {
			d.TeamSettings = map[string]domain.TeamSettings{}
		}
		for _, id := range []string{"est-fib", "est-zero", "est-none", "est-child", "est-secret"} {
			settings := teams[id]
			settings.TeamID = id
			d.Teams = append(d.Teams, domain.Team{ID: id, Key: "E" + id[4:7], Name: id, Private: id == "est-secret"})
			d.TeamSettings[id] = settings
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	todo := domain.WorkflowState{ID: "est-todo", Name: "Todo", Type: "unstarted"}
	progress := domain.WorkflowState{ID: "est-progress", Name: "In Progress", Type: "started"}
	done := domain.WorkflowState{ID: "est-done", Name: "Done", Type: "completed"}
	estimate := func(value float64) *float64 { return &value }
	ids := []string{}
	issue := func(id, team string, points *float64, state domain.WorkflowState, priority int, labels ...string) domain.Issue {
		item := base
		item.ID, item.Identifier, item.Title, item.State, item.Priority, item.Estimate = "est-"+id, "EST-"+id, id, state, priority, points
		item.Team = domain.Team{ID: team, Key: "E" + team[4:7], Name: team}
		item.Labels, item.Permissions, item.ParentID, item.SubIssueIDs, item.ArchivedAt, item.Project = nil, nil, nil, nil, nil, nil
		for _, label := range labels {
			item.Labels = append(item.Labels, domain.IssueLabel{ID: label})
		}
		ids = append(ids, item.ID)
		return item
	}
	issues := []domain.Issue{
		issue("f1", "est-fib", estimate(3), todo, 0, "est-L1", "est-L2"), issue("f2", "est-fib", nil, todo, 0, "est-L1"), issue("f3", "est-fib", estimate(0), progress, 0),
		issue("z1", "est-zero", estimate(0), todo, 4, "est-L2"), issue("z2", "est-zero", nil, progress, 4), issue("z3", "est-zero", estimate(8), progress, 0),
		issue("n1", "est-none", estimate(5), todo, 4, "est-L1"), issue("n2", "est-none", nil, done, 0), issue("c1", "est-child", estimate(2), todo, 0),
		issue("s1", "est-secret", estimate(5), todo, 0, "est-L1"),
	}
	if err := repository.ImportIssues(ctx, workspace, issues); err != nil {
		t.Fatal(err)
	}
	scope := IssueFilter{Field: "id", Values: ids}
	outsider := &IssueRecordAccess{UserID: "est-outsider", WorkspaceID: workspaceID, VisibleTeamIDs: []string{"est-fib", "est-zero", "est-none", "est-child"}}
	check := func(name string, query IssueRecordQuery, want map[string]float64) {
		t.Helper()
		query.Workspace = workspace
		if query.Filter.Field == "" && len(query.Filter.And) == 0 {
			query.Filter = scope
		}
		got, err := repository.QueryIssueGroupEstimatePoints(ctx, query)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !maps.Equal(got, want) {
			t.Fatalf("%s: got %v want %v", name, got, want)
		}
	}

	// Mixed teams in one group; a group of only non-estimating teams (done) has no total.
	check("status", IssueRecordQuery{GroupBy: "status"}, map[string]float64{todo.ID: 3 + 1 + 0 + 2 + 5, progress.ID: 1 + 0 + 8})
	check("status outsider", IssueRecordQuery{GroupBy: "status", Access: outsider}, map[string]float64{todo.ID: 3 + 1 + 0 + 2, progress.ID: 1 + 0 + 8})
	check("none", IssueRecordQuery{GroupBy: "none", Access: outsider}, map[string]float64{"all": 15})
	// A group whose estimating issues add up to 0 still has a total.
	check("priority", IssueRecordQuery{GroupBy: "priority"}, map[string]float64{"0": 3 + 1 + 1 + 8 + 2 + 5, "4": 0})
	check("team", IssueRecordQuery{GroupBy: "team"}, map[string]float64{"est-fib": 5, "est-zero": 8, "est-child": 2, "est-secret": 5})
	check("estimate", IssueRecordQuery{GroupBy: "estimate"}, map[string]float64{"": 1, "0": 1, "2": 2, "3": 3, "5": 5, "8": 8})
	// Labels are multi-membership: f1 counts under both of its labels.
	check("labels", IssueRecordQuery{GroupBy: "labels"}, map[string]float64{"est-L1": 3 + 1 + 5, "est-L2": 3, "": 1 + 0 + 8 + 2})
	check("labels outsider", IssueRecordQuery{GroupBy: "labels", Access: outsider}, map[string]float64{"est-L1": 3 + 1, "est-L2": 3, "": 1 + 0 + 8 + 2})
	check("label group", IssueRecordQuery{GroupBy: "labels", GroupLabelIDs: []string{"est-L2"}}, map[string]float64{"est-L2": 3, "": 1 + 1 + 0 + 8 + 2 + 5})
	cases := []IssueGroupCase{{Value: "started", Filter: IssueFilter{Field: "statusType", Values: []string{"started"}}}}
	check("cases", IssueRecordQuery{GroupBy: "cases", GroupCases: cases, Access: outsider}, map[string]float64{"started": 9, "": 6})
	check("customer", IssueRecordQuery{GroupBy: "customer", Access: outsider}, map[string]float64{"": 15})
	// Group values do not narrow the totals; nested queries count every issue in its own group.
	value := todo.ID
	check("group value", IssueRecordQuery{GroupBy: "status", GroupValue: &value, Nested: true, Access: outsider}, map[string]float64{todo.ID: 6, progress.ID: 9})

	// Settings changes apply right away (totals are not cached by issue revision).
	if err := repository.MutateWorkspace(WithColdWork(ctx, "workspace fixture"), workspace, "test.teams", "", nil, func(d *domain.Bootstrap) error {
		settings := d.TeamSettings["est-fib"]
		settings.EstimateCountUnestimated = &off
		d.TeamSettings["est-fib"] = settings
		settings = d.TeamSettings["est-zero"]
		settings.EstimateType = "notUsed"
		d.TeamSettings["est-zero"] = settings
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// f2 and f3 now count 0; est-zero and est-child (inheriting) stop counting.
	check("status after settings change", IssueRecordQuery{GroupBy: "status", Access: outsider}, map[string]float64{todo.ID: 3, progress.ID: 0})

	if _, err := repository.QueryIssueGroupEstimatePoints(ctx, IssueRecordQuery{Workspace: workspace, GroupBy: "unknown"}); err == nil {
		t.Fatal("unsupported grouping accepted")
	}
}

func TestIssueEstimateSettingsMatchWebRules(t *testing.T) {
	on, off := true, false
	value := func(text string) sql.NullString { return sql.NullString{String: text, Valid: true} }
	for _, test := range []struct {
		settings issueEstimateSettings
		estimate sql.NullString
		want     float64
	}{
		{issueEstimateSettings{estimateType: "fibonacci"}, sql.NullString{}, 1},
		{issueEstimateSettings{estimateType: "fibonacci", countUnestimated: &on}, sql.NullString{}, 1},
		{issueEstimateSettings{estimateType: "fibonacci", countUnestimated: &off}, sql.NullString{}, 0},
		{issueEstimateSettings{estimateType: "fibonacci"}, value("0"), 1},
		{issueEstimateSettings{estimateType: "fibonacci", allowZero: true}, value("0"), 0},
		{issueEstimateSettings{estimateType: "fibonacci"}, value("-1"), 1},
		{issueEstimateSettings{estimateType: "fibonacci"}, value("2.5"), 2.5},
	} {
		if got := test.settings.points(test.estimate); got != test.want {
			t.Fatalf("%+v %v: got %v want %v", test.settings, test.estimate, got, test.want)
		}
	}
	// A cycle of inheriting teams ends at the repeated team's own settings.
	settings := map[string]issueEstimateSettings{
		"a": {estimateType: "notUsed", inherit: true, parentTeamID: "b"},
		"b": {estimateType: "tShirt", inherit: true, parentTeamID: "a"},
		"c": {estimateType: "flow", inherit: true, parentTeamID: "missing"},
	}
	if got, _ := resolveIssueEstimateSettings(settings, "a", map[string]bool{}); got.estimateType != "notUsed" {
		t.Fatalf("cycle resolved to %+v", got)
	}
	if got, _ := resolveIssueEstimateSettings(settings, "c", map[string]bool{}); got.estimateType != "flow" {
		t.Fatalf("missing parent resolved to %+v", got)
	}
	if _, ok := resolveIssueEstimateSettings(settings, "missing", map[string]bool{}); ok {
		t.Fatal("a team without settings resolved")
	}
}
