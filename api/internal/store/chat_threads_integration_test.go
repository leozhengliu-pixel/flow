//go:build integration

package store

import (
	"os"
	"testing"
)

// The chat thread, initiative binding, project set and intake request
// storage on PostgreSQL and MySQL, opened twice so the migrations run on a database that already
// has them.
func TestExternalDatabaseChatThreadsAndProjectSets(t *testing.T) {
	driver, url := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || url == "" {
		t.Skip("external database not configured")
	}
	for attempt := 0; attempt < 2; attempt++ {
		repo, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: url, MaxOpenConns: 4, MaxIdleConns: 2})
		if err != nil {
			t.Fatalf("startup %d: %v", attempt, err)
		}
		exerciseChatThreadStore(t, repo)
		exerciseChatIntakeRequestStore(t, repo)
		if err = repo.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
