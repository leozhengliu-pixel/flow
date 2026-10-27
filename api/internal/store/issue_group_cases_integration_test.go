//go:build integration

package store

import (
	"context"
	"os"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Several-value status groups and case groupings on MySQL / Postgres: the
// CASE index expression, its nested carry and the IN group condition.
func TestExternalIssueGroupCases(t *testing.T) {
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
	workspace := "group-cases-integration"
	_ = repo.DeleteWorkspace(ctx, workspace)
	data, err := repo.CreateWorkspace(ctx, "Group cases test", workspace, "us")
	if err != nil {
		t.Fatal(err)
	}
	defer repo.DeleteWorkspace(ctx, workspace)
	now := time.Now().UTC()
	testIssueGroupCasesContract(t, repo, workspace, domain.Issue{Team: data.Teams[0], State: data.States[0], Creator: data.Viewer, CreatedAt: now, UpdatedAt: now})
}
