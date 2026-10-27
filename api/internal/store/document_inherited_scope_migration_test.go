package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type scopeFixture struct {
	issueID, issueTeam string
	otherTeam          string
	created            time.Time
	creator            domain.User
}

// seedDocumentScopeFixture writes documents in the shape stored before
// inherited scope existed (links, no derived fields) plus a few that must
// stay as they are, then marks the migration as not yet applied.
func seedDocumentScopeFixture(t *testing.T, repo *SQLiteStore, workspace string) scopeFixture {
	t.Helper()
	ctx := context.Background()
	data, ok := repo.BootstrapFor(workspace)
	if !ok || len(data.Users) == 0 {
		t.Fatal("fixture workspace missing")
	}
	fixture := scopeFixture{created: time.Date(2025, 5, 6, 7, 8, 9, 0, time.UTC), creator: data.Users[0]}
	if err := repo.db.QueryRowContext(ctx, `SELECT id,team_id FROM issue_records WHERE workspace_key=? ORDER BY id LIMIT 1`, workspace).Scan(&fixture.issueID, &fixture.issueTeam); err != nil {
		t.Fatalf("fixture issue: %v", err)
	}
	fixture.otherTeam = "team_scope_private"
	// The grants a document has today: the workspace and its creator.
	grants := []domain.DocumentPermission{
		{ID: "document_permission_workspace_", SubjectType: "workspace", Role: "editor", CreatedAt: fixture.created, UpdatedAt: fixture.created},
		{ID: "document_permission_" + fixture.creator.ID, SubjectType: "user", SubjectID: fixture.creator.ID, Role: "owner", CreatedAt: fixture.created, UpdatedAt: fixture.created},
	}
	document := func(id, issueID string, projects, teams []string) domain.Document {
		permissions := slices.Clone(grants)
		permissions[0].DocumentID, permissions[1].DocumentID = id, id
		return domain.Document{ID: id, SlugID: id, Title: id, Creator: fixture.creator, IssueID: issueID, ProjectIDs: projects, TeamIDs: teams, SubscriberIDs: []string{}, Revisions: []domain.DocumentRevision{}, CreatedAt: fixture.created, UpdatedAt: fixture.created, Version: 4, Permissions: permissions}
	}
	current := document("doc_scope_current", fixture.issueID, []string{}, []string{})
	current.IssueTeamID, current.InheritedTeamIDs = fixture.issueTeam, []string{fixture.issueTeam}
	trashed, err := json.Marshal(document("doc_scope_trashed", fixture.issueID, []string{"proj_scope"}, []string{}))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MutateWorkspace(ctx, workspace, "document.created", "doc_scope_issue", nil, func(next *domain.Bootstrap) error {
		if !slices.ContainsFunc(next.Teams, func(team domain.Team) bool { return team.ID == fixture.otherTeam }) {
			next.Teams = append(next.Teams, domain.Team{ID: fixture.otherTeam, Key: "SCP", Name: "Scope private", Private: true})
		}
		next.Projects = append(next.Projects, domain.Project{ID: "proj_scope", SlugID: "proj-scope-000000000000", Name: "Scope", TeamIDs: []string{fixture.otherTeam, "team_deleted", fixture.issueTeam}, MemberIDs: []string{}, LabelIDs: []string{}, CreatedAt: fixture.created, UpdatedAt: fixture.created})
		next.Documents = append(next.Documents,
			document("doc_scope_issue", fixture.issueID, []string{}, []string{}),
			document("doc_scope_project", "", []string{"proj_scope"}, []string{}),
			document("doc_scope_both", fixture.issueID, []string{"proj_scope", "proj_missing"}, []string{}),
			document("doc_scope_own_team", fixture.issueID, []string{}, []string{fixture.otherTeam}),
			document("doc_scope_unlinked", "", []string{}, []string{}),
			document("doc_scope_issue_gone", "issue_gone", []string{}, []string{}),
			current,
		)
		next.Trash = append(next.Trash, domain.TrashEntry{ID: "trash_scope_doc", ResourceType: "document", ResourceID: "doc_scope_trashed", Title: "Trashed", Payload: trashed, DeletedBy: fixture.creator, DeletedAt: fixture.created, ExpiresAt: fixture.created.AddDate(0, 0, 30)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The link handlers derive the fields; a seeded legacy document has none.
	if raw := storedRecord(t, repo, workspace, "documents", "doc_scope_issue"); json.Valid(raw) && containsJSONKey(raw, "inheritedTeamIds") {
		t.Fatalf("legacy document stored with inherited teams: %s", raw)
	}
	if _, err := repo.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=?`, documentInheritedScopeMigrationVersion); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func containsJSONKey(raw []byte, key string) bool {
	fields := map[string]json.RawMessage{}
	_ = json.Unmarshal(raw, &fields)
	_, ok := fields[key]
	return ok
}

func storedRecord(t *testing.T, repo *SQLiteStore, workspace, field, key string) []byte {
	t.Helper()
	var raw []byte
	if err := repo.db.QueryRowContext(context.Background(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, field, key).Scan(&raw); err != nil {
		t.Fatalf("read %s record %s: %v", field, key, err)
	}
	return raw
}

func scopeRecords(t *testing.T, repo *SQLiteStore, workspace string) map[string][]byte {
	t.Helper()
	rows, err := repo.db.QueryContext(context.Background(), `SELECT field,record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND field IN ('documents','trash','projects')`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string][]byte{}
	for rows.Next() {
		var field, key string
		var raw []byte
		if err := rows.Scan(&field, &key, &raw); err != nil {
			t.Fatal(err)
		}
		result[field+"/"+key] = raw
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

type scopeWant struct {
	inherited []string
	issueTeam string
	teams     []string
}

// checkDocumentScope verifies the stored and loaded documents carry the
// derived scope and kept their grants, own teams, version and update time.
func checkDocumentScope(t *testing.T, repo *SQLiteStore, workspace string, fixture scopeFixture) {
	t.Helper()
	want := map[string]scopeWant{
		"doc_scope_issue":   {inherited: []string{fixture.issueTeam}, issueTeam: fixture.issueTeam},
		"doc_scope_project": {inherited: []string{fixture.otherTeam, fixture.issueTeam}},
		// Issue team first, each team once, unknown project and deleted team skipped.
		"doc_scope_both": {inherited: []string{fixture.issueTeam, fixture.otherTeam}, issueTeam: fixture.issueTeam},
		// The document's own teams stay as chosen.
		"doc_scope_own_team":   {inherited: []string{fixture.issueTeam}, issueTeam: fixture.issueTeam, teams: []string{fixture.otherTeam}},
		"doc_scope_unlinked":   {},
		"doc_scope_issue_gone": {},
		"doc_scope_current":    {inherited: []string{fixture.issueTeam}, issueTeam: fixture.issueTeam},
	}
	data, _ := repo.BootstrapFor(workspace)
	for id, expected := range want {
		var stored domain.Document
		if err := json.Unmarshal(storedRecord(t, repo, workspace, "documents", id), &stored); err != nil {
			t.Fatal(err)
		}
		index := slices.IndexFunc(data.Documents, func(document domain.Document) bool { return document.ID == id })
		if index < 0 {
			t.Fatalf("%s missing from memory", id)
		}
		for source, document := range map[string]domain.Document{"stored": stored, "loaded": data.Documents[index]} {
			if !slices.Equal(document.InheritedTeamIDs, expected.inherited) || document.IssueTeamID != expected.issueTeam {
				t.Errorf("%s %s scope = %v / %q, want %v / %q", source, id, document.InheritedTeamIDs, document.IssueTeamID, expected.inherited, expected.issueTeam)
			}
			if teams := expected.teams; !slices.Equal(document.TeamIDs, append([]string{}, teams...)) {
				t.Errorf("%s %s teams = %v, want %v", source, id, document.TeamIDs, teams)
			}
			if got := grantsOf(document.Permissions); !slices.Equal(got, []string{"document_permission_workspace_|workspace:=editor", "document_permission_" + fixture.creator.ID + "|user:" + fixture.creator.ID + "=owner"}) {
				t.Errorf("%s %s grants changed: %+v", source, id, document.Permissions)
			}
			if document.Version != 4 || !document.UpdatedAt.Equal(fixture.created) {
				t.Errorf("%s %s version/updatedAt changed: %d %v", source, id, document.Version, document.UpdatedAt)
			}
		}
	}
	var entry domain.TrashEntry
	if err := json.Unmarshal(storedRecord(t, repo, workspace, "trash", "trash_scope_doc"), &entry); err != nil {
		t.Fatal(err)
	}
	var trashed domain.Document
	if err := json.Unmarshal(entry.Payload, &trashed); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(trashed.InheritedTeamIDs, []string{fixture.issueTeam, fixture.otherTeam}) || trashed.IssueTeamID != fixture.issueTeam || trashed.Title != "doc_scope_trashed" || trashed.Version != 4 || len(trashed.Permissions) != 2 {
		t.Fatalf("trashed document = %+v", trashed)
	}
}

func TestDocumentInheritedScopeMigration(t *testing.T) {
	previous := documentScopeMigrationBatch
	documentScopeMigrationBatch = 2 // several document batches and issue lookups
	t.Cleanup(func() { documentScopeMigrationBatch = previous })
	path := filepath.Join(t.TempDir(), "flow.db")
	repo, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture := seedDocumentScopeFixture(t, repo, accessWorkspace)
	unchanged := map[string][]byte{}
	for _, id := range []string{"doc_scope_unlinked", "doc_scope_issue_gone", "doc_scope_current"} {
		unchanged[id] = storedRecord(t, repo, accessWorkspace, "documents", id)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { repo.Close() }()
	checkDocumentScope(t, repo, accessWorkspace, fixture)
	for id, raw := range unchanged {
		if got := storedRecord(t, repo, accessWorkspace, "documents", id); string(got) != string(raw) {
			t.Errorf("%s rewritten although its scope was already right:\n%s\n%s", id, raw, got)
		}
	}
	applied, err := repo.migrationApplied(context.Background(), documentInheritedScopeMigrationVersion)
	if err != nil || !applied {
		t.Fatalf("migration not recorded: %v %v", applied, err)
	}

	// Running it again, or reopening, rewrites nothing.
	records := scopeRecords(t, repo, accessWorkspace)
	if err := repo.migrateDocumentInheritedScope(context.Background()); err != nil {
		t.Fatal(err)
	}
	if again := scopeRecords(t, repo, accessWorkspace); !recordsEqual(records, again) {
		t.Fatal("second migration run rewrote records")
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	if again := scopeRecords(t, repo, accessWorkspace); !recordsEqual(records, again) {
		t.Fatal("reopening rewrote records")
	}
	checkDocumentScope(t, repo, accessWorkspace, fixture)
}

func TestDerivedDocumentScopeKeepsRecordedIssueTeam(t *testing.T) {
	data := domain.Bootstrap{Teams: []domain.Team{{ID: "team_a"}, {ID: "team_b"}}}
	// An issue that is not found keeps the recorded team, as the server does.
	inherited, issueTeam, changed := derivedDocumentScope(data, documentScopeLinks{IssueID: "issue_archived_elsewhere", IssueTeamID: "team_b", InheritedTeamIDs: []string{"team_b"}}, map[string]string{})
	if changed || issueTeam != "team_b" || !slices.Equal(inherited, []string{"team_b"}) {
		t.Fatalf("got %v %q %v", inherited, issueTeam, changed)
	}
	// A document detached from its issue drops the issue team.
	inherited, issueTeam, changed = derivedDocumentScope(data, documentScopeLinks{IssueTeamID: "team_b", InheritedTeamIDs: []string{"team_b"}}, nil)
	if !changed || issueTeam != "" || len(inherited) != 0 {
		t.Fatalf("got %v %q %v", inherited, issueTeam, changed)
	}
}
