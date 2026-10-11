//go:build integration

package store

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Released-date filters on MySQL / Postgres: the same boundaries and offsets
// as the SQLite tests, through the external database's collation and binds.
func TestExternalReleasedDateFilter(t *testing.T) {
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || databaseURL == "" {
		t.Skip("isolated external database required")
	}
	repo, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	workspace := "release-filter-integration"
	_ = repo.DeleteWorkspace(ctx, workspace)
	data, err := repo.CreateWorkspace(ctx, "Release filter test", workspace, "us")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{"rf-1", "rf-2", "rf-3"}
	issues := make([]domain.Issue, len(ids))
	for i, id := range ids {
		issues[i] = domain.Issue{ID: id, Identifier: id, Title: id, Team: data.Teams[0], State: data.States[0], CreatedAt: time.Now(), UpdatedAt: time.Now()}
	}
	if err := repo.ImportIssues(ctx, workspace, issues); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := repo.MutateWorkspace(ctx, workspace, "release.created", "", nil, func(next *domain.Bootstrap) error {
		next.Releases = append(next.Releases, domain.Release{ID: "rf-release", SlugID: "rf-release", Name: "1.0", Status: "released", PipelineID: "rf-pipeline", IssueIDs: []string{"rf-1", "rf-2"}, ReleasedAt: &at})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	match := func(node IssueFilter) []string {
		t.Helper()
		page, err := repo.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, Filter: IssueFilter{And: []IssueFilter{{Field: "id", Values: ids}, node}}, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, item := range page.Items {
			got = append(got, item.ID)
		}
		slices.Sort(got)
		return got
	}
	for _, test := range []struct {
		node IssueFilter
		want []string
	}{
		{IssueFilter{Field: "releasedAt", Operator: "after", Values: []string{"2026-10-01T11:59:59.999Z"}}, []string{"rf-1", "rf-2"}},
		{IssueFilter{Field: "releasedAt", Operator: "after", Values: []string{"2026-10-01T20:00:00+08:00"}}, []string{}},
		{IssueFilter{Field: "releasedAt", Operator: "before", Values: []string{"2026-10-01T20:00:01+08:00"}}, []string{"rf-1", "rf-2"}},
		{IssueFilter{Field: "releasedAt", Operator: "isEmpty"}, []string{"rf-3"}},
		{IssueFilter{Field: "releasePipeline", Values: []string{"rf-pipeline"}}, []string{"rf-1", "rf-2"}},
	} {
		if got := match(test.node); !slices.Equal(got, test.want) {
			t.Fatalf("%s %+v: got %v want %v", driver, test.node, got, test.want)
		}
	}
}
