//go:build integration

package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestExternalLabelDrivenQueryMatchesGenericPlan runs the same driven-vs-
// generic equivalence suite against MySQL or PostgreSQL.
func TestExternalLabelDrivenQueryMatchesGenericPlan(t *testing.T) {
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || databaseURL == "" {
		t.Skip("external database configuration is not set")
	}
	repository, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	prefix := fmt.Sprintf("label-equiv-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = repository.DeleteWorkspace(context.Background(), prefix+"-small")
		_ = repository.DeleteWorkspace(context.Background(), prefix+"-threshold")
	})
	runLabelEquivalenceSuite(t, repository, prefix)
}
