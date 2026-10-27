//go:build integration

package store

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// TestExternalDatabaseDocumentInheritedScopeMigration runs the inherited
// document scope migration against MySQL or Postgres
// (FLOW_TEST_DATABASE_DRIVER/URL) over enough issues and documents for
// several issue lookups and write batches.
func TestExternalDatabaseDocumentInheritedScopeMigration(t *testing.T) {
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || databaseURL == "" {
		t.Skip("external database configuration is not set")
	}
	previous := documentScopeMigrationBatch
	documentScopeMigrationBatch = 200
	t.Cleanup(func() { documentScopeMigrationBatch = previous })
	open := func() *SQLiteStore {
		repository, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2})
		if err != nil {
			t.Fatal(err)
		}
		return repository
	}
	ctx := context.Background()
	const workspaceKey = "integration-document-scope"
	const count = 650
	repository := open()
	_ = repository.DeleteWorkspace(ctx, workspaceKey)
	created, err := repository.CreateWorkspace(ctx, "Document Scope", workspaceKey, "us")
	if err != nil {
		t.Fatal(err)
	}
	public, creator := created.Teams[0], created.Viewer
	private := domain.Team{ID: "team_scope_private", Key: "PRV", Name: "Private", Private: true}
	at := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := repository.MutateWorkspace(ctx, workspaceKey, "document.created", private.ID, nil, func(next *domain.Bootstrap) error {
		next.Teams = append(next.Teams, private)
		next.Projects = append(next.Projects, domain.Project{ID: "proj_scope", SlugID: "proj-scope-000000000000", Name: "Scope", TeamIDs: []string{private.ID}, MemberIDs: []string{}, LabelIDs: []string{}, CreatedAt: at, UpdatedAt: at})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	issues := make([]domain.Issue, count)
	teamOf := func(index int) domain.Team {
		if index%2 == 0 {
			return private
		}
		return public
	}
	for index := range issues {
		id := fmt.Sprintf("scope-issue-%04d", index)
		issues[index] = domain.Issue{ID: id, Identifier: fmt.Sprintf("SCOPE-%d", index+1), Title: id, Team: teamOf(index), State: created.States[0], CreatedAt: at, UpdatedAt: at}
	}
	if err := repository.ImportIssues(ctx, workspaceKey, issues); err != nil {
		t.Fatal(err)
	}
	grant := func(id string) []domain.DocumentPermission {
		return []domain.DocumentPermission{
			{ID: "document_permission_workspace_", DocumentID: id, SubjectType: "workspace", Role: "editor", CreatedAt: at, UpdatedAt: at},
			{ID: "document_permission_" + creator.ID, DocumentID: id, SubjectType: "user", SubjectID: creator.ID, Role: "owner", CreatedAt: at, UpdatedAt: at},
		}
	}
	if err := repository.MutateWorkspace(ctx, workspaceKey, "document.created", "scope-doc-0000", nil, func(next *domain.Bootstrap) error {
		for index := range count {
			id := fmt.Sprintf("scope-doc-%04d", index)
			projects := []string{}
			if index%5 == 0 {
				projects = []string{"proj_scope"}
			}
			next.Documents = append(next.Documents, domain.Document{ID: id, SlugID: id, Title: id, Creator: creator, IssueID: issues[index].ID, ProjectIDs: projects, TeamIDs: []string{}, SubscriberIDs: []string{}, Revisions: []domain.DocumentRevision{}, CreatedAt: at, UpdatedAt: at, Version: 2, Permissions: grant(id)})
		}
		next.Documents = append(next.Documents, domain.Document{ID: "scope-doc-project", SlugID: "scope-doc-project", Title: "project", Creator: creator, ProjectIDs: []string{"proj_scope"}, TeamIDs: []string{public.ID}, SubscriberIDs: []string{}, Revisions: []domain.DocumentRevision{}, CreatedAt: at, UpdatedAt: at, Version: 2, Permissions: grant("scope-doc-project")})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=?`, documentInheritedScopeMigrationVersion); err != nil {
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
	check := func(stage string) {
		t.Helper()
		data, ok := repository.BootstrapFor(workspaceKey)
		if !ok {
			t.Fatalf("%s: workspace missing", stage)
		}
		if !slices.ContainsFunc(data.Projects, func(project domain.Project) bool {
			return project.ID == "proj_scope" && slices.Equal(project.TeamIDs, []string{private.ID})
		}) {
			t.Fatalf("%s: project missing: %+v", stage, data.Projects)
		}
		byID := map[string]domain.Document{}
		for _, document := range data.Documents {
			byID[document.ID] = document
		}
		for index := range count {
			document := byID[fmt.Sprintf("scope-doc-%04d", index)]
			want := []string{teamOf(index).ID}
			if index%5 == 0 && teamOf(index).ID != private.ID {
				want = append(want, private.ID)
			}
			if document.IssueTeamID != teamOf(index).ID || !slices.Equal(document.InheritedTeamIDs, want) {
				t.Fatalf("%s: %s scope %v / %q, want %v / %q", stage, document.ID, document.InheritedTeamIDs, document.IssueTeamID, want, teamOf(index).ID)
			}
			if document.Version != 2 || !document.UpdatedAt.Equal(at) || len(document.Permissions) != 2 || len(document.TeamIDs) != 0 {
				t.Fatalf("%s: %s changed beyond its scope: %+v", stage, document.ID, document)
			}
		}
		project := byID["scope-doc-project"]
		if !slices.Equal(project.InheritedTeamIDs, []string{private.ID}) || project.IssueTeamID != "" || !slices.Equal(project.TeamIDs, []string{public.ID}) {
			t.Fatalf("%s: project document %+v", stage, project)
		}
	}
	check("after migration")
	applied, err := repository.migrationApplied(ctx, documentInheritedScopeMigrationVersion)
	if err != nil || !applied {
		t.Fatalf("migration not recorded: %v %v", applied, err)
	}
	// Idempotent: a second run finds nothing to change.
	live, trashed, err := repository.migrateWorkspaceDocumentScope(ctx, workspaceKey)
	if err != nil || live+trashed != 0 {
		t.Fatalf("second run changed %d+%d documents: %v", live, trashed, err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	repository = open()
	check("after reopen")
}
