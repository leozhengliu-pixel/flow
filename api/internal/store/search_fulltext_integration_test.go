//go:build integration

package store

import (
	"context"
	"os"
	"testing"

	"flow/api/internal/domain"
)

// English words lose most ngram tokens to InnoDB's default stopwords ("a",
// "i", "at", "in" …); the search indexes must find them anyway, including
// after an index built by an earlier release is rebuilt on startup.
func TestExternalSearchIndexesMatchEnglishWords(t *testing.T) {
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || databaseURL == "" {
		t.Skip("isolated external database required")
	}
	open := func() *SQLiteStore {
		t.Helper()
		repo, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2})
		if err != nil {
			t.Fatal(err)
		}
		return repo
	}
	ctx := context.Background()
	workspace := "search-english-words"
	repo := open()
	_ = repo.DeleteWorkspace(ctx, workspace)
	data, err := repo.CreateWorkspace(ctx, "English search test", workspace, "us")
	if err != nil {
		repo.Close()
		t.Fatal(err)
	}
	issue := domain.Issue{ID: "english-issue", Identifier: "ENG-901", Title: "UAT sign-off", Description: "Billing data migration in staging", Team: data.Teams[0]}
	if err := repo.ImportIssues(ctx, workspace, []domain.Issue{issue}); err != nil {
		repo.Close()
		t.Fatal(err)
	}
	project := domain.Project{ID: "english-project", Name: "Staging data plan", Description: "UAT audit", TeamIDs: []string{data.Teams[0].ID}, Status: domain.ProjectStatus{Type: "started"}}
	if err := repo.MutateWorkspace(ctx, workspace, "search.integration", project.ID, nil, func(next *domain.Bootstrap) error {
		next.Projects = append(next.Projects, project)
		return nil
	}); err != nil {
		repo.Close()
		t.Fatal(err)
	}
	issueTerms := []string{"UAT", "data migration", "staging", "billing"}
	projectTerms := []string{"data", "UAT", "staging", "plan"}
	found := func(repo *SQLiteStore) (issues, projects []string) {
		t.Helper()
		q := IssueRecordQuery{Workspace: workspace}
		for _, term := range issueTerms {
			hit := false
			if err := repo.SearchIssueCandidates(ctx, q, term, nil, 10, func(item domain.Issue) error { hit = hit || item.ID == issue.ID; return nil }); err != nil {
				t.Fatal(err)
			}
			if hit {
				issues = append(issues, term)
			}
		}
		for _, term := range projectTerms {
			result, err := repo.SearchMetadata(ctx, data, SearchMetadataQuery{Scope: q, Types: map[string]bool{"project": true}, Terms: []string{term}, Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Projects) == 1 {
				projects = append(projects, term)
			}
		}
		return issues, projects
	}
	expect := func(repo *SQLiteStore, label string) {
		t.Helper()
		issues, projects := found(repo)
		if len(issues) != len(issueTerms) || len(projects) != len(projectTerms) {
			t.Fatalf("%s: issue search found %q of %q; global search found %q of %q", label, issues, issueTerms, projects, projectTerms)
		}
	}
	expect(repo, "new index")
	if driver != "mysql" {
		repo.Close()
		return
	}
	// Recreate the indexes the way earlier releases built them, with the
	// server's default stopwords, and forget the rebuild.
	conn, err := repo.db.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`SET SESSION innodb_ft_enable_stopword=ON`,
		`DROP INDEX issue_search_fulltext ON issue_search_documents`,
		`CREATE FULLTEXT INDEX issue_search_fulltext ON issue_search_documents(content) WITH PARSER ngram`,
		`DROP INDEX metadata_search_fulltext ON metadata_search_documents`,
		`CREATE FULLTEXT INDEX metadata_search_fulltext ON metadata_search_documents(content) WITH PARSER ngram`,
		`DELETE FROM schema_migrations WHERE version IN (1004,1005)`,
	} {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			t.Fatal(statement, err)
		}
	}
	conn.Close()
	if issues, projects := found(repo); len(issues) == len(issueTerms) || len(projects) == len(projectTerms) {
		t.Fatalf("legacy index unexpectedly matched every word: %q %q", issues, projects)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo = open()
	defer repo.Close()
	expect(repo, "rebuilt index")
}
