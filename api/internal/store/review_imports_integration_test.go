//go:build integration

package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestExternalDatabaseReviewImportState(t *testing.T) {
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || databaseURL == "" {
		t.Skip("external database configuration is not set")
	}
	repository, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	workspace := fmt.Sprintf("review-import-%d", time.Now().UnixNano())
	defer func() {
		_, _ = repository.db.ExecContext(context.Background(), `DELETE FROM review_import_items WHERE workspace_key=?`, workspace)
		_, _ = repository.db.ExecContext(context.Background(), `DELETE FROM review_import_state WHERE workspace_key=?`, workspace)
	}()
	checkReviewImportStore(t, repository, workspace)
}
