package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
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
	q.Recent = []domain.RecentResource{{ResourceType: "project", ResourceID: "candidate-000"}, {ResourceType: "project", ResourceID: "candidate-079"}}
	got, err = repo.SearchMetadata(ctx, policy, q)
	if err != nil || len(got.Projects) != 1 {
		t.Fatal("recents bypassed permission", err)
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
