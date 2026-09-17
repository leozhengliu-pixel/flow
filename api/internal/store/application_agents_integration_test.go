//go:build integration

package store

import (
	"os"
	"testing"
)

func TestExternalDatabaseApplicationSchema(t *testing.T) {
	driver, url := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || url == "" {
		t.Skip("external database not configured")
	}
	for attempt := 0; attempt < 2; attempt++ {
		repo, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: url, MaxOpenConns: 4, MaxIdleConns: 2})
		if err != nil {
			t.Fatalf("startup %d: %v", attempt, err)
		}
		if err = repo.ensureApplicationAgents(t.Context()); err != nil {
			t.Fatalf("repeat schema: %v", err)
		}
		for _, table := range []string{"application_installations", "application_agent_tasks", "application_agent_activities"} {
			var count int
			if err = repo.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
				t.Fatal(err)
			}
		}
		if err = repo.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
