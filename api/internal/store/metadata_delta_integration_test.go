//go:build integration

package store

import (
	"context"
	"os"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Metadata-only writes persist as deltas; after a reopen the external
// database must hold the same state the full write would have produced.
func TestExternalMetadataDeltaRoundTrip(t *testing.T) {
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || databaseURL == "" {
		t.Skip("external database configuration is not set")
	}
	ctx := context.Background()
	const workspaceKey = "delta-workspace"
	open := func() *SQLiteStore {
		repository, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2})
		if err != nil {
			t.Fatal(err)
		}
		return repository
	}
	repository := open()
	_ = repository.DeleteWorkspace(ctx, workspaceKey)
	created, err := repository.CreateWorkspace(ctx, "Delta Workspace", workspaceKey, "us")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	steps := []struct {
		event  string
		mutate func(*domain.Bootstrap)
	}{
		{"view.created", func(data *domain.Bootstrap) {
			data.SavedViews = append(data.SavedViews, domain.SavedView{ID: "view_a", Name: "A", Resource: "issues", Scope: "workspace", CreatedAt: now, UpdatedAt: now}, domain.SavedView{ID: "view_b", Name: "B", Resource: "issues", Scope: "workspace", CreatedAt: now, UpdatedAt: now})
		}},
		{"view.updated", func(data *domain.Bootstrap) { data.SavedViews[0].Name = "A2" }},
		{"view.deleted", func(data *domain.Bootstrap) { data.SavedViews = data.SavedViews[1:] }},
		{"project_display_default.updated", func(data *domain.Bootstrap) { data.ProjectDisplayDefault = []byte(`{"layout":"board"}`) }},
		{"workspace.settings_updated", func(data *domain.Bootstrap) { data.Workspace.Name = "Delta Renamed" }},
		{"team.archived", func(data *domain.Bootstrap) {
			data.Teams[0].ArchivedAt = &now
			data.AuditLog = append([]domain.AuditLogEntry{{ID: "audit_delta", Action: "deleted", ResourceType: "team", ResourceID: data.Teams[0].ID, CreatedAt: now}}, data.AuditLog...)
		}},
	}
	for _, step := range steps {
		if err := repository.MutateWorkspace(ctx, workspaceKey, step.event, "", nil, func(data *domain.Bootstrap) error {
			step.mutate(data)
			return nil
		}); err != nil {
			t.Fatalf("%s: %v", step.event, err)
		}
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := open()
	defer reopened.Close()
	loaded, ok := reopened.WorkspaceMetadata(workspaceKey)
	if !ok {
		t.Fatal("workspace missing after reopen")
	}
	if loaded.Workspace.Name != "Delta Renamed" || len(loaded.SavedViews) != 1 || loaded.SavedViews[0].ID != "view_b" || string(loaded.ProjectDisplayDefault) != `{"layout":"board"}` {
		t.Fatalf("delta writes were not persisted: name=%q views=%v display=%s", loaded.Workspace.Name, loaded.SavedViews, loaded.ProjectDisplayDefault)
	}
	if len(loaded.AuditLog) == 0 || loaded.AuditLog[0].ID != "audit_delta" || loaded.Teams[0].ArchivedAt == nil || loaded.Teams[0].ID != created.Teams[0].ID {
		t.Fatalf("team archive delta lost order or data: audit=%v team=%+v", loaded.AuditLog, loaded.Teams[0])
	}
	_ = reopened.DeleteWorkspace(ctx, workspaceKey)
}
