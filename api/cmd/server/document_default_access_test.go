package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// TestDocumentDefaultAccessMigrationRoles checks what the startup migration
// that gives existing documents the default grants means for access: team
// members can edit a team document that only its creator could edit before,
// a chosen team role is kept, and the creator and admins remain owners.
func TestDocumentDefaultAccessMigrationRoles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	repository, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	data, ok := repository.BootstrapFor("test-workspace")
	if !ok || len(data.Teams) == 0 || len(data.Users) == 0 {
		t.Fatal("fixture workspace has no teams or users")
	}
	creator, team := data.Users[0], data.Teams[0].ID
	created := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	document := func(id string, teams []string, permissions []domain.DocumentPermission) domain.Document {
		return domain.Document{ID: id, SlugID: id, Title: id, Creator: creator, TeamIDs: teams, ProjectIDs: []string{}, SubscriberIDs: []string{}, Revisions: []domain.DocumentRevision{}, CreatedAt: created, UpdatedAt: created, Permissions: permissions}
	}
	ownerOnly := []domain.DocumentPermission{{ID: "document_permission_owner", DocumentID: "doc_owner_only", SubjectType: "user", SubjectID: creator.ID, Role: "owner", CreatedAt: created, UpdatedAt: created}}
	teamViewer := []domain.DocumentPermission{{ID: "document_permission_team_" + team, DocumentID: "doc_team_viewer", SubjectType: "team", SubjectID: team, Role: "viewer", CreatedAt: created, UpdatedAt: created}}
	if err := repository.MutateWorkspace(context.Background(), "test-workspace", "document.created", "doc_owner_only", nil, func(next *domain.Bootstrap) error {
		next.Documents = append(next.Documents,
			document("doc_owner_only", []string{team}, ownerOnly),
			document("doc_team_viewer", []string{team}, teamViewer),
			document("doc_legacy", []string{team}, nil),
			document("doc_unscoped", []string{}, nil),
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE name='default document access grants'`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	repository, err = store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	data, _ = repository.BootstrapFor("test-workspace")

	documentNamed := func(id string) domain.Document {
		index := slices.IndexFunc(data.Documents, func(item domain.Document) bool { return item.ID == id })
		if index < 0 {
			t.Fatalf("document %s missing", id)
		}
		return data.Documents[index]
	}
	as := func(userID, role string, teams ...string) domain.Bootstrap {
		view := data
		view.Viewer = domain.User{ID: userID}
		view.ViewerRole = role
		view.TeamMembers = nil
		for _, teamID := range teams {
			view.TeamMembers = append(view.TeamMembers, domain.TeamMember{TeamID: teamID, UserID: userID, Role: "member"})
		}
		return view
	}
	s := &server{}
	member := as("user_team_member", "member", team)
	outsider := as("user_outsider", "member")
	cases := []struct {
		name, document, want string
		view                 domain.Bootstrap
	}{
		{"team member edits a team document", "doc_owner_only", "editor", member},
		{"team member edits a legacy team document", "doc_legacy", "editor", member},
		{"chosen team role is kept", "doc_team_viewer", "viewer", member},
		{"other members keep no access to a team document", "doc_owner_only", "none", outsider},
		{"a document without teams keeps its access", "doc_unscoped", "none", outsider},
		{"creator stays owner", "doc_legacy", "owner", as(creator.ID, "member")},
		{"admin stays owner", "doc_owner_only", "owner", as("user_admin", "admin")},
	}
	for _, item := range cases {
		if got := documentRole(s, item.view, documentNamed(item.document)); got != item.want {
			t.Errorf("%s: role = %q, want %q", item.name, got, item.want)
		}
	}
}
