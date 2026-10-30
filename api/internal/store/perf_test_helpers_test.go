package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func perfWorkspace(t *testing.T) (*SQLiteStore, domain.Bootstrap) {
	t.Helper()
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	created, err := repository.CreateWorkspaceWith(context.Background(), "Perf", "perf-regression", "us", nil)
	if err != nil {
		t.Fatal(err)
	}
	return repository, created
}

func perfIssue(workspace domain.Bootstrap, index int) domain.Issue {
	return domain.Issue{
		ID: fmt.Sprintf("issue_perf_%03d", index), Identifier: fmt.Sprintf("PERF-%d", index+1), Number: index + 1,
		Title: "Perf issue", SortOrder: float64(index), Priority: index % 5,
		Team: workspace.Teams[0], State: workspace.States[0], Creator: workspace.Viewer,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
}
