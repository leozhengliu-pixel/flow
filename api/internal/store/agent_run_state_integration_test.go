//go:build integration

package store

import (
	"os"
	"testing"
)

// TestExternalDatabaseAgentRunState runs the per-run state tables on MySQL or PostgreSQL
// (FLOW_TEST_DATABASE_DRIVER / FLOW_TEST_DATABASE_URL) through two store instances, as two API servers use them.
func TestExternalDatabaseAgentRunState(t *testing.T) {
	driver, url := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || url == "" {
		t.Skip("external database not configured")
	}
	open := func() *SQLiteStore {
		repo, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: url, MaxOpenConns: 8, MaxIdleConns: 4})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { repo.Close() })
		return repo
	}
	first, second := open(), open()
	if err := first.createAgentRunStateSchema(t.Context()); err != nil {
		t.Fatalf("repeat schema: %v", err)
	}
	exerciseAgentRunState(t, first, second)
}
