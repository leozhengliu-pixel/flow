//go:build integration

package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestExternalDatabaseSCIM runs the SCIM store checks against MySQL or Postgres
// (FLOW_TEST_DATABASE_DRIVER / FLOW_TEST_DATABASE_URL), each in its own workspace.
func TestExternalDatabaseSCIM(t *testing.T) {
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || databaseURL == "" {
		t.Skip("external database configuration is not set")
	}
	repository, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	checks := []struct {
		name  string
		check func(*testing.T, *SQLiteStore, string)
	}{
		{"user queries", checkSCIMUserQueries},
		{"delete and restore", checkSCIMDeleteAndRestore},
		{"group member patches", checkSCIMGroupMemberPatches},
	}
	for index, item := range checks {
		t.Run(item.name, func(t *testing.T) {
			ctx := context.Background()
			key := fmt.Sprintf("integration-scim-%d-%d", index, time.Now().UnixNano())
			created, err := repository.CreateWorkspace(ctx, "SCIM", key, "us")
			if err != nil {
				t.Fatal(err)
			}
			defer repository.DeleteWorkspace(ctx, key)
			item.check(t, repository, created.Workspace.ID)
		})
	}
}
