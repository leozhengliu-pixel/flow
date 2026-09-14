package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func insertSearchMetadata(t testing.TB, repo *SQLiteStore, workspace, field, id string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.db.ExecContext(context.Background(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,0,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET data=excluded.data`, workspace, field, id, raw); err != nil {
		t.Fatal(err)
	}
	if field == "apiKeys" {
		if err = upsertAPIKeyLookup(context.Background(), repo.db, workspace, id, apiKeyLookupHashFromRecord(raw)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMetadataSearchUsesBoundedIndexedShellsAndTracksDirectWrites(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	ctx := context.Background()
	policy, access, err := repo.IssueQueryAccess(ctx, data.Workspace.URLKey, data.Viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "indexed-project", Name: "Unique landing plan", Description: strings.Repeat("padding ", 10000) + " NeedleAtEnd", TeamIDs: []string{data.Teams[0].ID}, DescriptionRevisions: []domain.ProjectDescriptionRevision{{Description: strings.Repeat("revision", 10000)}}}
	insertSearchMetadata(t, repo, data.Workspace.URLKey, "projects", project.ID, project)
	q := SearchMetadataQuery{Scope: IssueRecordQuery{Workspace: data.Workspace.URLKey, Access: &access}, Types: map[string]bool{"project": true}, Terms: []string{"NeedleAtEnd"}, Limit: 10}
	got, err := repo.SearchMetadata(ctx, policy, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Projects) != 1 || got.Projects[0].ID != project.ID || len(got.Projects[0].Description) > 2048 || len(got.Projects[0].DescriptionRevisions) > 0 {
		t.Fatalf("not a bounded indexed shell: %+v", got.Projects)
	}
	project.Description = "Replacement searchabletext"
	insertSearchMetadata(t, repo, data.Workspace.URLKey, "projects", project.ID, project)
	got, err = repo.SearchMetadata(ctx, policy, q)
	if err != nil || len(got.Projects) != 0 {
		t.Fatal("old terms survived update", err)
	}
	q.Terms = []string{"searchabletext"}
	got, err = repo.SearchMetadata(ctx, policy, q)
	if err != nil || len(got.Projects) != 1 {
		t.Fatal("new terms missing", err)
	}
	if _, err := repo.db.ExecContext(ctx, `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key=?`, q.Scope.Workspace, project.ID); err != nil {
		t.Fatal(err)
	}
	got, err = repo.SearchMetadata(ctx, policy, q)
	if err != nil || len(got.Projects) != 0 {
		t.Fatal("deleted result retained", err)
	}
	var count int
	if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metadata_search_documents WHERE workspace_key=? AND field='projects' AND record_key=?`, q.Scope.Workspace, project.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("deleted index retained", err)
	}
}

func TestMetadataSearchUpdatesWithoutDatabaseTriggers(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "search-no-triggers.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	data := repo.Bootstrap()
	for _, name := range []string{"metadata_records_search_insert", "metadata_records_search_update", "metadata_records_search_delete"} {
		if _, err := repo.db.ExecContext(ctx, `DROP TRIGGER IF EXISTS `+name); err != nil {
			t.Fatal(err)
		}
	}
	created := false
	if err := repo.MutateWorkspace(ctx, data.Workspace.URLKey, "search.test", "project-no-trigger", nil, func(next *domain.Bootstrap) error {
		next.Projects = append(next.Projects, domain.Project{ID: "project-no-trigger", Name: "No trigger searchable name", Summary: "Incremental metadata", TeamIDs: []string{next.Teams[0].ID}})
		created = true
		return nil
	}); err != nil || !created {
		t.Fatalf("mutate=%v created=%v", err, created)
	}
	policy, access, err := repo.IssueQueryAccess(ctx, data.Workspace.URLKey, data.Viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := repo.SearchMetadata(ctx, policy, SearchMetadataQuery{Scope: IssueRecordQuery{Workspace: data.Workspace.URLKey, Access: &access}, Types: map[string]bool{"project": true}, Terms: []string{"searchable"}, Limit: 10})
	if err != nil || len(result.Projects) != 1 || result.Projects[0].ID != "project-no-trigger" {
		t.Fatalf("insert was not indexed without trigger: %+v %v", result.Projects, err)
	}
	if err := repo.MutateWorkspace(ctx, data.Workspace.URLKey, "search.test", "project-no-trigger", nil, func(next *domain.Bootstrap) error {
		for i := range next.Projects {
			if next.Projects[i].ID == "project-no-trigger" {
				next.Projects[i].Name = "Renamed searchable record"
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err = repo.SearchMetadata(ctx, policy, SearchMetadataQuery{Scope: IssueRecordQuery{Workspace: data.Workspace.URLKey, Access: &access}, Types: map[string]bool{"project": true}, Terms: []string{"no trigger"}, Limit: 10})
	if err != nil || len(result.Projects) != 0 {
		t.Fatalf("stale search content survived update: %+v %v", result.Projects, err)
	}
	result, err = repo.SearchMetadata(ctx, policy, SearchMetadataQuery{Scope: IssueRecordQuery{Workspace: data.Workspace.URLKey, Access: &access}, Types: map[string]bool{"project": true}, Terms: []string{"renamed"}, Limit: 10})
	if err != nil || len(result.Projects) != 1 {
		t.Fatalf("updated search content missing: %+v %v", result.Projects, err)
	}
	if err := repo.MutateWorkspace(ctx, data.Workspace.URLKey, "search.test", "project-no-trigger", nil, func(next *domain.Bootstrap) error {
		next.Projects = slices.DeleteFunc(next.Projects, func(project domain.Project) bool { return project.ID == "project-no-trigger" })
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err = repo.SearchMetadata(ctx, policy, SearchMetadataQuery{Scope: IssueRecordQuery{Workspace: data.Workspace.URLKey, Access: &access}, Types: map[string]bool{"project": true}, Terms: []string{"renamed"}, Limit: 10})
	if err != nil || len(result.Projects) != 0 {
		t.Fatalf("deleted search content survived: %+v %v", result.Projects, err)
	}
}

func TestMetadataSearchFiltersBeforeLimitAndChecksPrivateScopes(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	ctx := context.Background()
	policy, _ := repo.IssueAccessMetadata(ctx, data.Workspace.URLKey)
	policy.Viewer = data.Viewer
	policy.ViewerRole = "member"
	access := IssueRecordAccess{UserID: data.Viewer.ID, WorkspaceID: data.Workspace.ID, VisibleTeamIDs: []string{data.Teams[0].ID}}
	for i := 0; i < 80; i++ {
		project := domain.Project{ID: fmt.Sprintf("candidate-%03d", i), Name: "Search candidate", Status: domain.ProjectStatus{Type: "started"}, TeamIDs: []string{"hidden"}, UpdatedAt: time.Now().UTC()}
		if i == 79 {
			project.TeamIDs = []string{data.Teams[0].ID}
			project.Lead = &data.Viewer
		}
		insertSearchMetadata(t, repo, data.Workspace.URLKey, "projects", project.ID, project)
	}
	q := SearchMetadataQuery{Scope: IssueRecordQuery{Workspace: data.Workspace.URLKey, Access: &access, Filter: IssueFilter{And: []IssueFilter{{Field: "statusType", Values: []string{"started"}}, {Field: "assigneeId", Values: []string{data.Viewer.ID}}}}}, Types: map[string]bool{"project": true}, Terms: []string{"candidate"}, Limit: 1}
	got, err := repo.SearchMetadata(ctx, policy, q)
	if err != nil || len(got.Projects) != 1 || got.Projects[0].ID != "candidate-079" {
		t.Fatalf("filters applied after candidate cap: %+v %v", got.Projects, err)
	}
	q.Scope.Filter = IssueFilter{}
	got, err = repo.SearchMetadata(ctx, policy, q)
	if err != nil || len(got.Projects) != 1 || got.Projects[0].ID != "candidate-079" {
		t.Fatal("hidden projects consumed result limit or leaked", err)
	}
	q.Terms = nil
	q.Recent = nil
	got, err = repo.SearchMetadata(ctx, policy, q)
	if err != nil || len(got.Projects) != 0 {
		t.Fatal("empty search scanned candidate catalog", err)
	}
	q.Terms = []string{" ", ""}
	got, err = repo.SearchMetadata(ctx, policy, q)
	if err != nil || len(got.Projects) != 0 {
		t.Fatal("whitespace search scanned candidate catalog", err)
	}
	q.Recent = []domain.RecentResource{{ResourceType: "project", ResourceID: "candidate-000"}, {ResourceType: "project", ResourceID: "candidate-079"}}
	got, err = repo.SearchMetadata(ctx, policy, q)
	if err != nil || len(got.Projects) != 1 {
		t.Fatal("recents bypassed permission", err)
	}
}

func TestCappedSearchHitINNestsLimitOffINOperand(t *testing.T) {
	got := cappedSearchHitIN("i.id", "issue_id", "SELECT issue_id FROM search_hits LIMIT ?")
	want := "i.id IN (SELECT issue_id FROM (SELECT issue_id FROM search_hits LIMIT ?) capped_hits)"
	if got != want {
		t.Fatalf("cappedSearchHitIN=%q want %q", got, want)
	}
}

func TestEmptyIssueSearchDoesNotScanOrFailOnCorruptRows(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	ctx := context.Background()
	hit := data.Issues[0]
	hit.ID = "flow-valid"
	hit.Identifier = "FLOW-1"
	hit.Title = "Flow valid"
	poison := hit
	poison.ID = "flow-poison"
	poison.Identifier = "FLOW-2"
	poison.Title = "Flow poison"
	if err := repo.ImportIssues(ctx, data.Workspace.URLKey, []domain.Issue{hit, poison}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE issue_records SET data='invalid-json',list_data='invalid-json' WHERE workspace_key=? AND id=?`, data.Workspace.URLKey, poison.ID); err != nil {
		t.Fatal(err)
	}
	q := IssueRecordQuery{Workspace: data.Workspace.URLKey, Archived: "all"}
	if err := repo.SearchIssueCandidateTerms(ctx, q, nil, nil, 100, func(domain.Issue) error {
		t.Fatal("blank terms scanned issue catalog")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SearchIssueCandidates(ctx, q, "   ", nil, 100, func(domain.Issue) error {
		t.Fatal("whitespace terms scanned issue catalog")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	seen := []string{}
	if err := repo.SearchIssueCandidates(ctx, q, "Flow", nil, 100, func(issue domain.Issue) error {
		seen = append(seen, issue.ID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != hit.ID {
		t.Fatalf("Flow search re-read or included corrupt rows: %v", seen)
	}
}

func TestIssueSearchUnionDeduplicatesAndKeepsIndexedFilters(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	ctx := context.Background()
	one := data.Issues[0]
	one.ID = "candidate-one"
	one.Identifier = "CAND-1"
	one.Title = "Union alpha beta"
	one.Description = strings.Repeat("description", 10000)
	one.Priority = 1
	two := one
	two.ID = "candidate-two"
	two.Identifier = "CAND-2"
	two.Priority = 2
	if err := repo.ImportIssues(ctx, data.Workspace.URLKey, []domain.Issue{one, two}); err != nil {
		t.Fatal(err)
	}
	q := IssueRecordQuery{Workspace: data.Workspace.URLKey, Filter: IssueFilter{Field: "priority", Values: []string{"1"}}, Archived: "all"}
	seen := []domain.Issue{}
	err = repo.SearchIssueCandidateTerms(ctx, q, []string{"alpha", "beta"}, nil, 1, func(issue domain.Issue) error { seen = append(seen, issue); return nil })
	if err != nil || len(seen) != 1 || seen[0].ID != one.ID || seen[0].Description != "" || !seen[0].IsSummary {
		t.Fatalf("bad bounded union: %+v %v", seen, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := repo.SearchIssueCandidateTerms(cancelled, q, []string{"alpha"}, nil, 1, func(domain.Issue) error { t.Fatal("canceled query produced result"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not propagated: %v", err)
	}
}

func TestMetadataSearchHonorsDocumentACLBeforeReturningTitles(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	data := repo.Bootstrap()
	policy, _ := repo.IssueAccessMetadata(ctx, data.Workspace.URLKey)
	policy.Viewer = data.Viewer
	policy.ViewerRole = "member"
	access := IssueRecordAccess{UserID: data.Viewer.ID, WorkspaceID: data.Workspace.ID, VisibleTeamIDs: []string{data.Teams[0].ID}}
	for _, document := range []domain.Document{
		{ID: "denied-unscoped", Title: "ACLneedle denied", Permissions: []domain.DocumentPermission{{SubjectType: "user", SubjectID: "other", Role: "owner"}}},
		{ID: "denied-team", Title: "ACLneedle team denied", TeamIDs: []string{data.Teams[0].ID}, Permissions: []domain.DocumentPermission{{SubjectType: "user", SubjectID: "other", Role: "viewer"}}},
		{ID: "shared-private", Title: "ACLneedle shared", TeamIDs: []string{"private"}, Permissions: []domain.DocumentPermission{{SubjectType: "user", SubjectID: data.Viewer.ID, Role: "viewer"}}},
		{ID: "unscoped", Title: "ACLneedle public"},
	} {
		insertSearchMetadata(t, repo, data.Workspace.URLKey, "documents", document.ID, document)
	}
	q := SearchMetadataQuery{Scope: IssueRecordQuery{Workspace: data.Workspace.URLKey, Access: &access}, Types: map[string]bool{"document": true}, Terms: []string{"ACLneedle"}, Limit: 100}
	result, err := repo.SearchMetadata(ctx, policy, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Documents) != 2 {
		t.Fatalf("document ACL bypass: %+v", result.Documents)
	}
	for _, document := range result.Documents {
		if strings.HasPrefix(document.ID, "denied") {
			t.Fatal("private title leaked")
		}
	}
	q.Scope.AllowedTeamIDs = []string{data.Teams[0].ID}
	result, err = repo.SearchMetadata(ctx, policy, q)
	if err != nil || len(result.Documents) != 1 || result.Documents[0].ID != "unscoped" {
		t.Fatalf("API key narrowed team scope not enforced: %+v %v", result.Documents, err)
	}
}

func TestMetadataSearchNegativeFiltersDoNotMatchUnrelatedTypesOrMissingDates(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	data := repo.Bootstrap()
	policy, access, err := repo.IssueQueryAccess(ctx, data.Workspace.URLKey, data.Viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, project := range []domain.Project{
		{ID: "negative-unassigned", Name: "Negativeneedle unassigned", TeamIDs: []string{data.Teams[0].ID}},
		{ID: "negative-assigned", Name: "Negativeneedle assigned", TeamIDs: []string{data.Teams[0].ID}, Lead: &data.Viewer, CreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
	} {
		insertSearchMetadata(t, repo, data.Workspace.URLKey, "projects", project.ID, project)
	}
	insertSearchMetadata(t, repo, data.Workspace.URLKey, "documents", "negative-document", domain.Document{ID: "negative-document", Title: "Negativeneedle document"})
	insertSearchMetadata(t, repo, data.Workspace.URLKey, "users", "negative-person", domain.User{ID: "negative-person", DisplayName: "Negativeneedle person", Active: true})
	q := SearchMetadataQuery{Scope: IssueRecordQuery{Workspace: data.Workspace.URLKey, Access: &access}, Types: map[string]bool{"project": true, "document": true, "member": true}, Terms: []string{"Negativeneedle"}, Limit: 100}
	for _, filter := range []IssueFilter{{Field: "assigneeId", Operator: "isNot", Values: []string{"none"}}, {Field: "createdAt", Operator: "before", Values: []string{"2026-01-01"}}} {
		q.Scope.Filter = filter
		result, err := repo.SearchMetadata(ctx, policy, q)
		if err != nil || len(result.Projects) != 1 || result.Projects[0].ID != "negative-assigned" || len(result.Documents) != 0 || len(result.Users) != 0 {
			t.Fatalf("unknown field/missing date consumed negative filter: projects=%+v documents=%+v users=%+v err=%v", result.Projects, result.Documents, result.Users, err)
		}
	}
	q.Scope.Filter = IssueFilter{Field: "assigneeId", Operator: "isNot", Values: []string{data.Viewer.ID}}
	result, err := repo.SearchMetadata(ctx, policy, q)
	if err != nil || len(result.Projects) != 1 || result.Projects[0].ID != "negative-unassigned" {
		t.Fatal("negative assignee should include an unassigned project", err)
	}
	q.Scope.Filter = IssueFilter{Field: "title", Operator: "isNot", Values: []string{"Negativeneedle document"}}
	result, err = repo.SearchMetadata(ctx, policy, q)
	if err != nil || len(result.Documents) != 0 {
		t.Fatal("negative document title checked the project name column", err)
	}
}

func BenchmarkIndexedMetadataSearchLargeCatalog(b *testing.B) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(b.TempDir(), "search.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	ctx := context.Background()
	for i := 0; i < 10000; i++ {
		name := fmt.Sprintf("Routine project %d", i)
		if i == 9999 {
			name = "Distinctive needle project"
		}
		insertSearchMetadata(b, repo, data.Workspace.URLKey, "projects", fmt.Sprintf("benchmark-%d", i), domain.Project{Name: name, ID: fmt.Sprintf("benchmark-%d", i), Description: strings.Repeat("body ", 100), TeamIDs: []string{data.Teams[0].ID}})
	}
	policy, access, err := repo.IssueQueryAccess(ctx, data.Workspace.URLKey, data.Viewer.ID)
	if err != nil {
		b.Fatal(err)
	}
	for _, term := range []string{"", "Distinctive needle"} {
		b.Run(fmt.Sprintf("query-%q", term), func(b *testing.B) {
			q := SearchMetadataQuery{Scope: IssueRecordQuery{Workspace: data.Workspace.URLKey, Access: &access}, Types: map[string]bool{"project": true}, Limit: 40}
			if term != "" {
				q.Terms = []string{term}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := repo.SearchMetadata(ctx, policy, q)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestMetadataSearchRepairReconcilesCompletedAndResumesCheckpoint(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "repair.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	data := repo.Bootstrap()
	insertSearchMetadata(t, repo, data.Workspace.URLKey, "projects", "repair-project", domain.Project{ID: "repair-project", Name: "Repairable content"})
	// Simulate a stale row left by a prior completed migration.
	if _, err := repo.db.ExecContext(ctx, `UPDATE metadata_search_documents SET content='old content' WHERE workspace_key=? AND field='projects' AND record_key=?`, data.Workspace.URLKey, "repair-project"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE metadata_search_migration_checkpoints SET completed=1,last_record_key='repair-project' WHERE workspace_key=? AND field='projects'`, data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	if err := repo.migrateMetadataSearchIndex(ctx); err != nil {
		t.Fatal(err)
	}
	var content string
	if err := repo.db.QueryRowContext(ctx, `SELECT content FROM metadata_search_documents WHERE workspace_key=? AND field='projects' AND record_key=?`, data.Workspace.URLKey, "repair-project").Scan(&content); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "Repairable content") {
		t.Fatalf("stale index was not repaired: %q", content)
	}
	// A non-zero checkpoint must resume from its key and still reconcile the tail.
	insertSearchMetadata(t, repo, data.Workspace.URLKey, "projects", "repair-tail", domain.Project{ID: "repair-tail", Name: "Tail content"})
	if _, err := repo.db.ExecContext(ctx, `DELETE FROM metadata_search_documents WHERE workspace_key=? AND field='projects' AND record_key=?`, data.Workspace.URLKey, "repair-tail"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE metadata_search_migration_checkpoints SET completed=0,last_record_key='repair-project' WHERE workspace_key=? AND field='projects'`, data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	if err := repo.migrateMetadataSearchIndex(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metadata_search_documents WHERE workspace_key=? AND field='projects' AND record_key=?`, data.Workspace.URLKey, "repair-tail").Scan(&count); err != nil || count != 1 {
		t.Fatalf("checkpoint retry did not restore row: %d %v", count, err)
	}
}

func TestMetadataSearchRollbackAndSameContentAvoidIndexWrites(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "atomic.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	data := repo.Bootstrap()
	insertSearchMetadata(t, repo, data.Workspace.URLKey, "projects", "atomic-project", domain.Project{ID: "atomic-project", Name: "Stable searchable"})
	tx, err := repo.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(domain.Project{ID: "rolled-back", Name: "Should not be searchable"})
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,0,?)`, data.Workspace.URLKey, "projects", "rolled-back", raw); err != nil {
		t.Fatal(err)
	}
	if err := syncMetadataSearchDocument(ctx, tx, data.Workspace.URLKey, "projects", "rolled-back", raw); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metadata_search_documents WHERE workspace_key=? AND field='projects' AND record_key=?`, data.Workspace.URLKey, "atomic-project").Scan(&count); err != nil || count != 1 {
		t.Fatalf("rollback removed search row: %d %v", count, err)
	}
	if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metadata_search_documents WHERE workspace_key=? AND field='projects' AND record_key=?`, data.Workspace.URLKey, "rolled-back").Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled-back transaction left search row: %d %v", count, err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=data WHERE workspace_key=? AND field='projects' AND record_key=?`, data.Workspace.URLKey, "atomic-project"); err != nil {
		t.Fatal(err)
	}
	var content string
	if err := repo.db.QueryRowContext(ctx, `SELECT content FROM metadata_search_documents WHERE workspace_key=? AND field='projects' AND record_key=?`, data.Workspace.URLKey, "atomic-project").Scan(&content); err != nil || !strings.Contains(content, "Stable searchable") {
		t.Fatalf("same-content update changed index: %q %v", content, err)
	}
}
