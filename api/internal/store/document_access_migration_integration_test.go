//go:build integration

package store

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// TestExternalDatabaseDocumentDefaultAccessMigration runs the document access
// migration against MySQL or Postgres (FLOW_TEST_DATABASE_DRIVER/URL).
func TestExternalDatabaseDocumentDefaultAccessMigration(t *testing.T) {
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
	ctx := context.Background()
	const workspaceKey = "integration-document-access"
	repository := open()
	_ = repository.DeleteWorkspace(ctx, workspaceKey)
	created, err := repository.CreateWorkspace(ctx, "Document Access", workspaceKey, "us")
	if err != nil {
		t.Fatal(err)
	}
	team, creator := created.Teams[0].ID, created.Viewer
	at := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	owner := domain.DocumentPermission{ID: "document_permission_owner", DocumentID: "doc_team_owner", SubjectType: "user", SubjectID: creator.ID, Role: "owner", CreatedAt: at, UpdatedAt: at}
	viewer := domain.DocumentPermission{ID: "document_permission_team_" + team, DocumentID: "doc_team_viewer", SubjectType: "team", SubjectID: team, Role: "viewer", CreatedAt: at, UpdatedAt: at}
	document := func(id string, teams []string, permissions []domain.DocumentPermission) domain.Document {
		return domain.Document{ID: id, SlugID: id, Title: id, Creator: creator, TeamIDs: teams, ProjectIDs: []string{}, SubscriberIDs: []string{}, Revisions: []domain.DocumentRevision{}, CreatedAt: at, UpdatedAt: at, Permissions: permissions}
	}
	if err := repository.MutateWorkspace(ctx, workspaceKey, "document.created", "doc_team_owner", nil, func(next *domain.Bootstrap) error {
		next.Documents = append(next.Documents,
			document("doc_team_owner", []string{team}, []domain.DocumentPermission{owner}),
			document("doc_team_viewer", []string{team}, []domain.DocumentPermission{viewer}),
			document("doc_no_team", []string{}, nil),
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=?`, documentDefaultAccessMigrationVersion); err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}

	repository = open()
	defer func() {
		_ = repository.DeleteWorkspace(ctx, workspaceKey)
		repository.Close()
	}()
	data, ok := repository.BootstrapFor(workspaceKey)
	if !ok {
		t.Fatal("workspace missing after reopen")
	}
	grants := func(id string) []string {
		index := slices.IndexFunc(data.Documents, func(item domain.Document) bool { return item.ID == id })
		if index < 0 {
			t.Fatalf("document %s missing", id)
		}
		result := []string{}
		for _, permission := range data.Documents[index].Permissions {
			result = append(result, permission.SubjectType+":"+permission.SubjectID+"="+permission.Role)
		}
		return result
	}
	want := map[string][]string{
		"doc_team_owner":  {"user:" + creator.ID + "=owner", "team:" + team + "=editor"},
		"doc_team_viewer": {"team:" + team + "=viewer", "user:" + creator.ID + "=owner"},
		"doc_no_team":     {"user:" + creator.ID + "=owner", "workspace:=editor"},
	}
	for id, expected := range want {
		if got := grants(id); !slices.Equal(got, expected) {
			t.Errorf("%s grants = %v, want %v", id, got, expected)
		}
	}
	applied, err := repository.migrationApplied(ctx, documentDefaultAccessMigrationVersion)
	if err != nil || !applied {
		t.Fatalf("migration not recorded: %v %v", applied, err)
	}
}
