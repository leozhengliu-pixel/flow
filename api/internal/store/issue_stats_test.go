package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAssigneeASTCountsUseCountersWithoutLosingScope(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	data := repository.Bootstrap()
	ctx := context.Background()
	q := IssueRecordQuery{Workspace: data.Workspace.URLKey, GroupBy: "status", Filter: IssueFilter{And: []IssueFilter{{}, {Field: "assigneeId", Values: []string{data.Viewer.ID}}}}}
	groups, handled, err := repository.issueGroupsFromStats(ctx, q)
	if err != nil || !handled {
		t.Fatalf("common My Issues query missed counters: %v handled=%v", err, handled)
	}
	var count int64
	for _, group := range groups {
		count += group.Count
	}
	page, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: q.Workspace, Filter: q.Filter, IncludeTotal: true})
	if err != nil || page.Total != count {
		t.Fatalf("assignee counts differ from query: %d %d %v", count, page.Total, err)
	}
	q.Filter.And = append(q.Filter.And, IssueFilter{Field: "assignee", Values: []string{"unrelated-user"}})
	groups, handled, err = repository.issueGroupsFromStats(ctx, q)
	if err != nil || !handled || len(groups) != 0 {
		t.Fatalf("AND scope intersection widened: %+v %v", groups, err)
	}
	q.TeamIDs = []string{"unrelated-team"}
	_, handled, _ = repository.issueGroupsFromStats(ctx, q)
	if handled {
		t.Fatal("combined team and assignee query used counters without that dimension")
	}
	q.TeamIDs = nil
	q.Access = &IssueRecordAccess{UserID: "restricted", VisibleTeamIDs: []string{}}
	_, handled, _ = repository.issueGroupsFromStats(ctx, q)
	if handled {
		t.Fatal("private-team permissions were bypassed by counters")
	}
}
