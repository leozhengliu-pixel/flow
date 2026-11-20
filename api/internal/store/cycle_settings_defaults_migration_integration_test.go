//go:build integration

package store

import (
	"context"
	"os"
	"testing"
)

// TestExternalDatabaseCycleSettingsDefaultsMigration runs the cycle settings
// defaults migration against MySQL or Postgres
// (FLOW_TEST_DATABASE_DRIVER/URL).
func TestExternalDatabaseCycleSettingsDefaultsMigration(t *testing.T) {
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || databaseURL == "" {
		t.Skip("external database configuration is not set")
	}
	open := func() *SQLiteStore {
		repository, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2})
		if err != nil {
			t.Fatal(err)
		}
		return repository
	}
	const workspace = "integration-cycle-defaults"
	ctx := context.Background()
	create := func() *SQLiteStore {
		repository := open()
		_ = repository.DeleteWorkspace(ctx, workspace)
		if _, err := repository.CreateWorkspace(ctx, "Cycle Defaults", workspace, "us"); err != nil {
			t.Fatal(err)
		}
		return repository
	}
	t.Cleanup(func() {
		repository := open()
		_ = repository.DeleteWorkspace(ctx, workspace)
		repository.Close()
	})
	cycleSettingsDefaultsScenario(t, workspace, create, open)
}
