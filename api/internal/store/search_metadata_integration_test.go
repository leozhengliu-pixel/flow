//go:build integration

package store

import (
	"context"
	"os"
	"testing"

	"flow/api/internal/domain"
)

func TestExternalMetadataSearchIndexLifecycle(t *testing.T) {
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || databaseURL == "" {
		t.Skip("isolated external database required")
	}
	repo, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	workspace := "search-index-integration"
	_ = repo.DeleteWorkspace(ctx, workspace)
	data, err := repo.CreateWorkspace(ctx, "Search index test", workspace, "us")
	if err != nil {
		repo.Close()
		t.Fatal(err)
	}
	project := domain.Project{ID: "indexed-project", Name: "NeedleProject", TeamIDs: []string{data.Teams[0].ID}, Status: domain.ProjectStatus{Type: "started"}}
	insertSearchMetadata(t, repo, workspace, "projects", project.ID, project)
	q := SearchMetadataQuery{Scope: IssueRecordQuery{Workspace: workspace, Filter: IssueFilter{Field: "statusType", Values: []string{"started"}}}, Types: map[string]bool{"project": true}, Terms: []string{"NeedleProject"}, Limit: 10}
	result, err := repo.SearchMetadata(ctx, data, q)
	if err != nil || len(result.Projects) != 1 {
		repo.Close()
		t.Fatalf("insert index missing: %+v %v", result.Projects, err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	project.Name = "ChangedNeedle"
	insertSearchMetadata(t, repo, workspace, "projects", project.ID, project)
	result, err = repo.SearchMetadata(ctx, data, q)
	if err != nil || len(result.Projects) != 0 {
		t.Fatalf("update retained old index: %+v %v", result.Projects, err)
	}
	q.Terms = []string{"ChangedNeedle"}
	result, err = repo.SearchMetadata(ctx, data, q)
	if err != nil || len(result.Projects) != 1 {
		t.Fatalf("update index missing: %+v %v", result.Projects, err)
	}
	if _, err := repo.db.ExecContext(ctx, `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key=?`, workspace, project.ID); err != nil {
		t.Fatal(err)
	}
	result, err = repo.SearchMetadata(ctx, data, q)
	if err != nil || len(result.Projects) != 0 {
		t.Fatalf("delete index retained: %+v %v", result.Projects, err)
	}
}
