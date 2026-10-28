package store

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// nestedCountShape is a generated workspace: teams (the last one private to
// the test viewer), their workflow states, projects, users and labels.
type nestedCountShape struct {
	teams    []domain.Team
	states   map[string][]domain.WorkflowState
	projects []string
	users    []string
	labels   []string
}

// nestedCountIssues generates n issues shaped like a large workspace: skewed
// team sizes, mostly completed work, about a fifth sub-issues (most in their
// parent's team and project, often in another status) and a few deeper
// levels, some archived.
func nestedCountIssues(rng *rand.Rand, base domain.Issue, prefix string, n int) ([]domain.Issue, nestedCountShape) {
	shape := nestedCountShape{states: map[string][]domain.WorkflowState{}}
	types := []string{"backlog", "unstarted", "started", "started", "completed", "canceled"}
	for k := 0; k < 8; k++ {
		team := base.Team
		team.ID, team.Key = fmt.Sprintf("%steam-%d", prefix, k), fmt.Sprintf("NC%d", k)
		shape.teams = append(shape.teams, team)
		for j, kind := range types {
			shape.states[team.ID] = append(shape.states[team.ID], domain.WorkflowState{ID: fmt.Sprintf("%sstate-%d-%d", prefix, k, j), Name: kind, Type: kind, TeamID: team.ID})
		}
	}
	for k := 0; k < max(3, n/375); k++ {
		shape.projects = append(shape.projects, fmt.Sprintf("%sproject-%d", prefix, k))
	}
	for k := 0; k < max(4, min(60, n/100)); k++ {
		shape.users = append(shape.users, fmt.Sprintf("%suser-%d", prefix, k))
	}
	for k := 0; k < max(3, min(30, n/200)); k++ {
		shape.labels = append(shape.labels, fmt.Sprintf("%slabel-%d", prefix, k))
	}
	pick := func(weights []int) int {
		total := 0
		for _, w := range weights {
			total += w
		}
		r := rng.Intn(total)
		for i, w := range weights {
			if r < w {
				return i
			}
			r -= w
		}
		return 0
	}
	teamWeights, stateWeights := []int{30, 20, 15, 7, 7, 7, 7, 7}, []int{15, 12, 12, 8, 45, 8}
	issues := make([]domain.Issue, 0, n)
	var tops, children []int
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		item := base
		item.ID, item.Number = fmt.Sprintf("%sissue-%06d", prefix, i), i+1
		item.Identifier, item.Title = fmt.Sprintf("NC-%s%d", prefix, i+1), fmt.Sprintf("Issue %d", i)
		item.Labels, item.Permissions, item.ParentID, item.SubIssueIDs, item.ArchivedAt, item.Project, item.Assignee = nil, nil, nil, nil, nil, nil, nil
		item.SortOrder, item.Priority = rng.Float64()*1e6, rng.Intn(5)
		item.CreatedAt, item.UpdatedAt = now.Add(time.Duration(i)*time.Minute), now.Add(time.Duration(i)*time.Minute)
		item.Team = shape.teams[pick(teamWeights)]
		if rng.Intn(100) < 55 {
			item.Project = &domain.ProjectSummary{ID: shape.projects[rng.Intn(len(shape.projects))]}
		}
		if rng.Intn(100) < 75 {
			user := base.Creator
			user.ID = shape.users[rng.Intn(len(shape.users))]
			item.Assignee = &user
		}
		if rng.Intn(100) < 40 {
			item.Labels = []domain.IssueLabel{{ID: shape.labels[rng.Intn(len(shape.labels))]}}
		}
		parent := -1
		switch roll := rng.Intn(100); {
		case roll < 22 && len(tops) > 0:
			parent = tops[rng.Intn(len(tops))]
		case roll < 28 && len(children) > 0:
			parent = children[rng.Intn(len(children))]
		}
		state := shape.states[item.Team.ID][pick(stateWeights)]
		if parent >= 0 {
			up := issues[parent]
			item.ParentID = &up.ID
			if rng.Intn(100) < 90 {
				item.Team = up.Team
			}
			if rng.Intn(100) < 70 {
				item.Project = up.Project
			}
			state = shape.states[item.Team.ID][pick(stateWeights)]
			if rng.Intn(100) < 60 && up.Team.ID == item.Team.ID {
				state = up.State
			}
			children = append(children, i)
		} else {
			tops = append(tops, i)
		}
		item.State = state
		if rng.Intn(100) < 10 {
			archived := item.CreatedAt
			item.ArchivedAt = &archived
		}
		issues = append(issues, item)
	}
	return issues, shape
}

func importNestedCountIssues(t testing.TB, repository *SQLiteStore, workspace string, issues []domain.Issue) {
	t.Helper()
	for start := 0; start < len(issues); start += 1000 {
		if err := repository.ImportIssues(context.Background(), workspace, issues[start:min(start+1000, len(issues))]); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNestedIssueCountsMatchWalk(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	data := repository.Bootstrap()
	testNestedCountParity(t, repository, data.Workspace.URLKey, data.Workspace.ID, data.Issues[0])
}

// testNestedCountParity checks nested counts against the full walk on a
// generated workspace, for every grouping, access, scope and archived mode,
// and again after writes that change the trees: re-parenting, team moves,
// archiving, status changes and an issue shared into a private team.
func testNestedCountParity(t *testing.T, repository *SQLiteStore, workspace, workspaceID string, base domain.Issue) {
	t.Helper()
	ctx := context.Background()
	rng := rand.New(rand.NewSource(7))
	issues, shape := nestedCountIssues(rng, base, "ncp-", 600)
	importNestedCountIssues(t, repository, workspace, issues)
	// Customer requests: some issues ask for several customers (one unknown),
	// one customer more than once.
	for i := 0; i < 120; i++ {
		issue := issues[rng.Intn(len(issues))].ID
		customer := []string{"ncp-customer-a", "ncp-customer-b", "ncp-customer-gone"}[rng.Intn(3)]
		if _, err := repository.db.ExecContext(ctx, `INSERT INTO customer_request_filter_records(workspace_key,request_id,issue_id,customer_id,important,project_id) VALUES(?,?,?,?,0,'')`, workspace, fmt.Sprintf("ncp-request-%d", i), issue, customer); err != nil {
			t.Fatal(err)
		}
	}
	for _, customer := range []string{"ncp-customer-a", "ncp-customer-b"} {
		if _, err := repository.db.ExecContext(ctx, `INSERT INTO customer_filter_records(workspace_key,customer_id,owner_id,status_value,tier_value,revenue,customer_size) VALUES(?,?,'','','',0,0)`, workspace, customer); err != nil {
			t.Fatal(err)
		}
	}
	visible := []string{}
	for _, team := range shape.teams[:7] {
		visible = append(visible, team.ID)
	}
	private := shape.teams[7].ID
	member := &IssueRecordAccess{UserID: "ncp-viewer", WorkspaceID: workspaceID, VisibleTeamIDs: visible}
	cases := []IssueGroupCase{
		{Value: "urgent", Filter: IssueFilter{Field: "priority", Values: []string{"1"}}},
		{Value: "started", Filter: IssueFilter{Field: "statusType", Values: []string{"started"}}},
		{Value: "labelled or mine", Filter: IssueFilter{Or: []IssueFilter{{Field: "labels", Operator: "isNotEmpty"}, {Field: "assignee", Values: []string{shape.users[1]}}}}},
	}
	scopes := []IssueRecordQuery{
		// Terms the in-memory count combines: OR and NOT of status, label,
		// assignee and priority terms, labels without a label, and status ids.
		{TeamIDs: []string{shape.teams[0].ID}, Filter: IssueFilter{Or: []IssueFilter{{Field: "status", Values: []string{"started"}}, {Not: &IssueFilter{Field: "labels", Values: []string{shape.labels[0], ""}}}}}},
		{TeamIDs: []string{shape.teams[0].ID, private}, Filter: IssueFilter{And: []IssueFilter{{Field: "assigneeId", Operator: "notIn", Values: []string{shape.users[0]}}, {Field: "labels", Operator: "includesAll", Values: []string{shape.labels[1]}}}}},
		{TeamIDs: []string{shape.teams[1].ID}, StateIDs: []string{shape.states[shape.teams[1].ID][0].ID, shape.states[shape.teams[1].ID][2].ID}, Filter: IssueFilter{Field: "labels", Operator: "isEmpty"}},

		{},
		{TeamIDs: []string{shape.teams[0].ID}},
		{TeamIDs: []string{shape.teams[0].ID, shape.teams[3].ID}},
		{TeamIDs: []string{shape.teams[0].ID, private}},
		{ProjectIDs: []string{shape.projects[0]}},
		{Filter: IssueFilter{Field: "assignee", Values: []string{shape.users[0]}}},
		{TeamIDs: []string{shape.teams[0].ID}, Filter: IssueFilter{Field: "statusType", Operator: "notIn", Values: []string{"completed", "canceled"}}},
		{Filter: IssueFilter{Field: "labels", Operator: "notIn", Values: []string{shape.labels[0]}}},
		{Filter: IssueFilter{Or: []IssueFilter{{Field: "priority", Values: []string{"1", "2"}}, {Field: "labels", Values: []string{shape.labels[1]}}}}},
	}
	groupings := []string{"status", "priority", "assignee", "team", "project", "label", "estimate", "customer", "cases", "none"}
	moved, forestCounts, memberCounts := 0, 0, 0
	compare := func(step string, groupings []string, scopes []IssueRecordQuery) {
		t.Helper()
		moved, forestCounts, memberCounts = 0, 0, 0
		defer func() {
			// Nesting moved rows between groups in most combinations, and
			// the in-memory and member counter counts served many of them.
			if moved < 10 || forestCounts < 10 || memberCounts < 4 {
				t.Fatalf("%s: nested counts equal the flat ones in all but %d combinations; %d in-memory, %d member counter counts", step, moved, forestCounts, memberCounts)
			}
		}()
		for _, access := range []*IssueRecordAccess{nil, member} {
			for _, scope := range scopes {
				for _, archived := range []string{"false", "all"} {
					for _, groupBy := range groupings {
						q := scope
						q.Workspace, q.Access, q.Archived, q.GroupBy, q.Nested = workspace, access, archived, groupBy, true
						if groupBy == "cases" {
							q.GroupCases = cases
						}
						got, err := repository.QueryIssueGroups(ctx, q)
						if err != nil {
							t.Fatalf("%s %+v: %v", step, q, err)
						}
						want, err := repository.nestedIssueGroupsByWalk(ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						if !slices.Equal(got, want) {
							t.Fatalf("%s: nested %s counts (access %v, %+v)\n got %v\nwant %v", step, groupBy, access != nil, scope, got, want)
						}
						// Small results take the walk; the corrected flat counts
						// must agree for every query (with or without the team
						// restriction a team scope makes redundant).
						for _, counted := range []IssueRecordQuery{q, withoutRedundantIssueAccess(q)} {
							corrected, err := repository.nestedIssueGroupsByMoves(ctx, counted)
							if err != nil {
								t.Fatal(err)
							}
							if !slices.Equal(corrected, want) {
								t.Fatalf("%s: corrected %s counts (access %v, %+v)\n got %v\nwant %v", step, groupBy, counted.Access != nil, scope, corrected, want)
							}
						}
						// The in-memory count (issue_forest_groups.go), wherever
						// its plan covers the query, nested and flat.
						for _, counted := range []IssueRecordQuery{q, withoutRedundantIssueAccess(q)} {
							forest, handled, err := repository.issueForestGroups(ctx, counted, true)
							if err != nil {
								t.Fatal(err)
							}
							if handled {
								forestCounts++
								if !slices.Equal(forest, want) {
									t.Fatalf("%s: in-memory nested %s counts (access %v, %+v)\n got %v\nwant %v", step, groupBy, counted.Access != nil, scope, forest, want)
								}
							}
						}
						// Flat counts (counters, member counters, in memory) match
						// the SQL count, with or without a redundant team restriction.
						q.Nested = false
						flat, err := repository.QueryIssueGroups(ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						reference, err := repository.flatIssueGroupsBySQL(ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						if !slices.Equal(flat, reference) {
							t.Fatalf("%s: flat %s counts (access %v, %+v)\n got %v\nwant %v", step, groupBy, access != nil, scope, flat, reference)
						}
						for _, counted := range []IssueRecordQuery{q, withoutRedundantIssueAccess(q)} {
							forest, handled, err := repository.issueForestGroups(ctx, counted, false)
							if err != nil {
								t.Fatal(err)
							}
							if handled && !slices.Equal(forest, reference) {
								t.Fatalf("%s: in-memory flat %s counts (access %v, %+v)\n got %v\nwant %v", step, groupBy, counted.Access != nil, scope, forest, reference)
							}
							member, handled, err := repository.memberIssueGroupsFromStats(ctx, counted)
							if err != nil {
								t.Fatal(err)
							}
							if handled {
								memberCounts++
								if !slices.Equal(member, reference) {
									t.Fatalf("%s: member counter %s counts (%+v)\n got %v\nwant %v", step, groupBy, scope, member, reference)
								}
							}
						}
						if !slices.Equal(flat, got) {
							moved++
						}
					}
				}
			}
		}
	}
	compare("initial", groupings, scopes)

	byID := map[string]int{}
	for i, issue := range issues {
		byID[issue.ID] = i
	}
	// A top-level issue with sub-issues, and another top-level issue in
	// another team and status.
	var parent, other int
	for i := range issues {
		if issues[i].ParentID != nil {
			if up := byID[*issues[i].ParentID]; issues[up].ParentID == nil && issues[up].ArchivedAt == nil {
				parent = up
				break
			}
		}
	}
	for i := range issues {
		if issues[i].ParentID == nil && issues[i].ArchivedAt == nil && issues[i].Team.ID != issues[parent].Team.ID && issues[i].State.Type != issues[parent].State.Type {
			other = i
			break
		}
	}
	write := func(step string, change func(*domain.Issue), index int) {
		t.Helper()
		change(&issues[index])
		issues[index].Version++
		if err := repository.ImportIssues(ctx, workspace, []domain.Issue{issues[index]}); err != nil {
			t.Fatal(err)
		}
		compare(step, []string{"status", "label", "customer", "cases", "priority", "assignee", "none"}, scopes[:7])
	}
	write("re-parent", func(issue *domain.Issue) { issue.ParentID = &issues[other].ID }, parent)
	write("status change", func(issue *domain.Issue) { issue.State = shape.states[issues[other].Team.ID][1] }, other)
	write("team move", func(issue *domain.Issue) {
		issue.Team = shape.teams[7]
		issue.State = shape.states[private][2]
	}, other)
	write("shared into private team", func(issue *domain.Issue) {
		issue.Permissions = []domain.IssuePermission{{ID: "ncp-share", IssueID: issue.ID, SubjectType: "user", SubjectID: member.UserID, Role: "viewer"}}
	}, other)
	write("back to top level", func(issue *domain.Issue) { issue.ParentID = nil }, parent)
	write("archive", func(issue *domain.Issue) {
		archived := time.Now().UTC()
		issue.ArchivedAt = &archived
	}, parent)
}
