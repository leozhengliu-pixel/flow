//go:build integration

package store

import (
	"context"
	"os"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Nested sub-issue queries on MySQL / Postgres: the recursive descendant and
// count queries, re-aliased parent checks and access prefix on each dialect.
func TestExternalNestedIssueQuery(t *testing.T) {
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
	workspace := "nested-issue-integration"
	_ = repo.DeleteWorkspace(ctx, workspace)
	data, err := repo.CreateWorkspace(ctx, "Nested issue test", workspace, "us")
	if err != nil {
		t.Fatal(err)
	}
	defer repo.DeleteWorkspace(ctx, workspace)
	now := time.Now().UTC()
	testNestedIssueQueryContract(t, repo, workspace, data.Workspace.ID, domain.Issue{Team: data.Teams[0], State: data.States[0], Creator: data.Viewer, CreatedAt: now, UpdatedAt: now})
}

// Nested counts (corrected flat counts, the walk for small results and the
// tree index) agree with the full walk on MySQL / Postgres, before and after
// writes.
func TestExternalNestedIssueCounts(t *testing.T) {
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
	workspace := "nested-count-integration"
	_ = repo.DeleteWorkspace(ctx, workspace)
	data, err := repo.CreateWorkspace(ctx, "Nested count test", workspace, "us")
	if err != nil {
		t.Fatal(err)
	}
	defer repo.DeleteWorkspace(ctx, workspace)
	now := time.Now().UTC()
	testNestedCountParity(t, repo, workspace, data.Workspace.ID, domain.Issue{Team: data.Teams[0], State: data.States[0], Creator: data.Viewer, CreatedAt: now, UpdatedAt: now})
}
