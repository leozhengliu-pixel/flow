package store

import (
	"context"
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
