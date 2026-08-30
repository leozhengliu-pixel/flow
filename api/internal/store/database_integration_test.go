//go:build integration

package store

import (
	"context"
	"os"
	"testing"

	"flow/api/internal/domain"
)

func TestExternalDatabaseWorkspaceRoundTrip(t *testing.T) {
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || databaseURL == "" {
		t.Skip("external database configuration is not set")
	}
	repository, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	const workspaceKey = "integration-workspace"
	_ = repository.DeleteWorkspace(context.Background(), workspaceKey)
	created, err := repository.CreateWorkspace(context.Background(), "Integration Workspace", workspaceKey, "us")
	if err != nil {
		t.Fatal(err)
	}
	if created.Workspace.URLKey != workspaceKey || len(created.Teams) != 1 || len(created.Issues) != 0 {
		t.Fatalf("unexpected workspace: %#v", created)
	}
	if err := repository.MutateWorkspace(context.Background(), workspaceKey, "integration.updated", created.Workspace.ID, nil, func(data *domain.Bootstrap) error {
		data.Workspace.Name = "Persisted Integration Workspace"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	loaded, ok := reopened.BootstrapFor(workspaceKey)
	if !ok || loaded.Workspace.Name != "Persisted Integration Workspace" {
		t.Fatalf("workspace was not persisted: ok=%v workspace=%#v", ok, loaded.Workspace)
	}
}
