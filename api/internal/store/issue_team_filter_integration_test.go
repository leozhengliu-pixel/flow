//go:build integration

package store

import (
	"os"
	"testing"
)

func TestExternalTeamFilterQueries(t *testing.T) {
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || databaseURL == "" {
		t.Skip("isolated external database required")
	}
	repo, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	workspace := "team-filter-integration"
	data, err := repo.CreateWorkspace(t.Context(), "Team filter test", workspace, "us")
	if err != nil {
		t.Fatal(err)
	}
	defer repo.DeleteWorkspace(t.Context(), workspace)
	testTeamFilterQueries(t, repo, data)
}
