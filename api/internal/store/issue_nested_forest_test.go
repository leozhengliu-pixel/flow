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

func TestNestedIssueMovesFromIndexMatchWalk(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	data := repository.Bootstrap()
	testNestedIndexMovesParity(t, repository, data.Workspace.URLKey, data.Workspace.ID, data.Issues[0])
}

// testNestedIndexMovesParity checks the moves read from the tree index
// against the full walk on trees the generated workspaces lack: a chain
// deeper than the walk goes, a parent cycle, a missing parent, an archived
// or other-team middle row and an issue shared out of a hidden team, for
// every archived mode and the status, team and access terms the index covers.
func testNestedIndexMovesParity(t *testing.T, repository *SQLiteStore, workspace, workspaceID string, base domain.Issue) {
	t.Helper()
	ctx := context.Background()
	stamp := time.Now().UnixNano()
	teamA, teamB := base.Team, base.Team
	teamA.ID, teamB.ID = fmt.Sprintf("edge-a-%d", stamp), fmt.Sprintf("edge-b-%d", stamp)
	states := []domain.WorkflowState{{ID: fmt.Sprintf("edge-s0-%d", stamp), Type: "started"}, {ID: fmt.Sprintf("edge-s1-%d", stamp), Type: "unstarted"}}
	issues := []domain.Issue{}
	add := func(id, parent string, team domain.Team, state, priority int, archived bool) {
		issue := base
		issue.ID, issue.Identifier, issue.Title = fmt.Sprintf("edge-%d-%s", stamp, id), fmt.Sprintf("EDGE-%d", len(issues)+1), id
		issue.Number, issue.Version, issue.Team, issue.State, issue.Priority = 900000+len(issues), 1, team, states[state], priority
		issue.Labels, issue.Permissions, issue.SubIssueIDs, issue.Project, issue.Assignee, issue.ParentID, issue.ArchivedAt = nil, nil, nil, nil, nil, nil, nil
		if parent != "" {
			full := fmt.Sprintf("edge-%d-%s", stamp, parent)
			if parent == "missing" {
				full = parent
			}
			issue.ParentID = &full
		}
		if archived {
			at := base.CreatedAt
			issue.ArchivedAt = &at
		}
		issues = append(issues, issue)
	}
	// A chain of 40 under a top-level row, alternating status.
	add("chain-0", "", teamA, 0, 1, false)
	for k := 1; k < 40; k++ {
		add(fmt.Sprintf("chain-%d", k), fmt.Sprintf("chain-%d", k-1), teamA, k%2, k%4, false)
	}
	add("cycle-0", "cycle-1", teamA, 0, 1, false)
	add("cycle-1", "cycle-0", teamA, 1, 2, false)
	add("cycle-2", "cycle-1", teamA, 1, 3, false)
	add("orphan", "missing", teamA, 1, 1, false)
	add("orphan-child", "orphan", teamA, 0, 2, false)
	add("archived-0", "", teamA, 0, 1, false)
	add("archived-1", "archived-0", teamA, 1, 2, true)
	add("archived-2", "archived-1", teamA, 1, 3, false)
	add("archived-3", "archived-1", teamA, 0, 3, true)
	add("team-0", "", teamA, 0, 1, false)
	add("team-1", "team-0", teamB, 1, 2, false)
	add("team-2", "team-1", teamA, 1, 3, false)
	add("shared-0", "", teamB, 1, 0, false)
	add("shared-1", "shared-0", teamB, 0, 4, false)
	add("shared-2", "shared-1", teamA, 1, 4, false)
	// A status recorded with a second status type (its workflow state
	// changed type): MySQL cannot turn status type terms into status ids.
	add("retyped-0", "", teamA, 0, 1, false)
	add("retyped-1", "retyped-0", teamA, 1, 2, false)
	issues[len(issues)-1].State.Type = "started"
	member := fmt.Sprintf("edge-viewer-%d", stamp)
	for i := range issues {
		if issues[i].Title == "shared-0" {
			issues[i].Permissions = []domain.IssuePermission{{ID: fmt.Sprintf("edge-share-%d", stamp), IssueID: issues[i].ID, SubjectType: "user", SubjectID: member, Role: "viewer"}}
		}
	}
	importNestedCountIssues(t, repository, workspace, issues)

	accesses := []*IssueRecordAccess{nil,
		{UserID: member, WorkspaceID: workspaceID, Admin: true, ArchivedTeamIDs: []string{teamB.ID}},
		{UserID: member, WorkspaceID: workspaceID, VisibleTeamIDs: []string{teamA.ID}},
		{UserID: member, WorkspaceID: workspaceID, VisibleTeamIDs: []string{teamA.ID, teamB.ID}, ArchivedTeamIDs: []string{teamB.ID}},
	}
	active := IssueFilter{Field: "statusType", Operator: "notIn", Values: []string{"completed", "canceled", "unstarted"}}
	scopes := []IssueRecordQuery{{}, {TeamIDs: []string{teamA.ID}}, {TeamIDs: []string{teamA.ID, teamB.ID}}, {StateIDs: []string{states[1].ID}}, {AllowedTeamIDs: []string{teamA.ID}}, {AllowedTeamIDs: []string{}},
		{Filter: active},
		{TeamIDs: []string{teamA.ID}, Filter: IssueFilter{Field: "statusType", Values: []string{"unstarted"}}},
		{Filter: IssueFilter{Field: "status", Values: []string{states[1].ID}}},
		{Filter: IssueFilter{Not: &IssueFilter{Field: "priority", Values: []string{"1"}}}},
		{Filter: IssueFilter{Or: []IssueFilter{{Field: "priority", Values: []string{"2", "3"}}, active}}},
	}
	moved := 0
	for _, access := range accesses {
		for _, scope := range scopes {
			for _, archived := range []string{"false", "true", "all"} {
				for _, groupBy := range []string{"status", "priority"} {
					q := scope
					q.Workspace, q.Access, q.Archived, q.GroupBy, q.Nested = workspace, access, archived, groupBy, true
					// MySQL reads status type terms through the counters, which
					// hold the retyped status: those queries take the moves
					// query, which keeps the rows no top-level row reaches (the
					// cycle, the chain's far end) in their own groups.
					statusType := strings.Contains(fmt.Sprint(scope.Filter), "statusType")
					_, handled, err := repository.nestedIssueMovesFromIndex(ctx, q)
					if err != nil || !handled && !(statusType && repository.dialect == "mysql") {
						t.Fatalf("%+v: index moves not used (%v)", q, err)
					}
					if !handled {
						continue
					}
					got, err := repository.nestedIssueGroupsByMoves(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					want, err := repository.nestedIssueGroupsByWalk(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					if !slices.Equal(got, want) {
						t.Fatalf("%s counts (access %+v, archived %s, %+v)\n got %v\nwant %v", groupBy, access, archived, scope, got, want)
					}
					flat, err := repository.flatIssueGroups(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					// The counters (deleted teams and negated terms included)
					// agree with counting the rows.
					if reference := countIssueRowsByGroup(t, repository, q); !slices.Equal(flat, reference) {
						t.Fatalf("flat %s counts (access %+v, archived %s, %+v)\n got %v\nwant %v", groupBy, access, archived, scope, flat, reference)
					}
					if !slices.Equal(flat, got) {
						moved++
					}
				}
			}
		}
	}
	if moved < 20 {
		t.Fatalf("nested counts differ from the flat ones in only %d combinations", moved)
	}

	// The in-memory count on the same trees, for its groupings and with
	// label, OR and case terms: nested against the walk, flat against SQL.
	cases := []IssueGroupCase{{Value: "high", Filter: IssueFilter{Field: "priority", Operator: "lte", Values: []string{"2"}}}, {Value: "started", Filter: IssueFilter{Field: "statusType", Values: []string{"started"}}}}
	inMemory := 0
	for _, access := range accesses {
		for _, scope := range append(scopes, IssueRecordQuery{TeamIDs: []string{teamA.ID}, Filter: IssueFilter{Or: []IssueFilter{{Field: "labels", Operator: "isEmpty"}, {Field: "priority", Values: []string{"3"}}}}}) {
			for _, archived := range []string{"false", "all"} {
				for _, groupBy := range []string{"status", "priority", "team", "assignee", "label", "cases", "none"} {
					q := scope
					q.Workspace, q.Access, q.Archived, q.GroupBy, q.Nested = workspace, access, archived, groupBy, true
					if groupBy == "cases" {
						q.GroupCases = cases
					}
					got, handled, err := repository.issueForestGroups(ctx, q, true)
					if err != nil {
						t.Fatal(err)
					}
					// MySQL evaluates status type terms per status on the
					// counters, which hold the retyped status: those decline.
					statusType := strings.Contains(fmt.Sprint(scope.Filter), "statusType") || groupBy == "cases"
					if !handled && !(statusType && repository.dialect == "mysql") {
						t.Fatalf("%+v: in-memory count not used", q)
					}
					if !handled {
						continue
					}
					inMemory++
					want, err := repository.nestedIssueGroupsByWalk(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					if !slices.Equal(got, want) {
						t.Fatalf("in-memory nested %s counts (access %+v, archived %s, %+v)\n got %v\nwant %v", groupBy, access, archived, scope, got, want)
					}
					q.Nested = false
					flat, _, err := repository.issueForestGroups(ctx, q, false)
					if err != nil {
						t.Fatal(err)
					}
					if reference, err := repository.flatIssueGroupsBySQL(ctx, q); err != nil || !slices.Equal(flat, reference) {
						t.Fatalf("in-memory flat %s counts (access %+v, archived %s, %+v)\n got %v\nwant %v (%v)", groupBy, access, archived, scope, flat, reference, err)
					}
				}
			}
		}
	}
	if inMemory < 100 {
		t.Fatalf("the in-memory count served only %d combinations", inMemory)
	}
}

// countIssueRowsByGroup counts the query's rows by its status or priority
// grouping in SQL, without the counters.
func countIssueRowsByGroup(t *testing.T, repository *SQLiteStore, q IssueRecordQuery) []IssueRecordGroup {
	t.Helper()
	q.Nested = false
	where, args, err := issueRecordWhere(q)
	if err != nil {
		t.Fatal(err)
	}
	column, err := issueGroupColumn(q.GroupBy)
	if err != nil {
		t.Fatal(err)
	}
	prefix, prefixArgs := issueAccessCTE(q)
	rows, err := repository.db.QueryContext(t.Context(), prefix+"SELECT i."+column+",COUNT(*) FROM issue_records i WHERE "+where+" GROUP BY i."+column+" ORDER BY i."+column, append(prefixArgs, args...)...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	groups := []IssueRecordGroup{}
	for rows.Next() {
		var group IssueRecordGroup
		if err := rows.Scan(&group.Value, &group.Count); err != nil {
			t.Fatal(err)
		}
		groups = append(groups, group)
	}
	return groups
}
