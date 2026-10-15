package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
)

func TestCapSearchTextCountsCharacters(t *testing.T) {
	if got := capSearchText("数据同步接口", 4); got != "数据同步" {
		t.Fatalf("capped = %q", got)
	}
	if got := capSearchText("short", 0); got != "short" {
		t.Fatalf("uncapped = %q", got)
	}
	if got := capSearchText("short", 100); got != "short" {
		t.Fatalf("under the cap = %q", got)
	}
}

func TestSearchIndexCapAppliesToIssuesAndDocuments(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "flow.db")
	open := func(limit int) *SQLiteStore {
		t.Helper()
		s, err := OpenDatabase(DatabaseConfig{Driver: "sqlite", Path: path, FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 1, SearchIndexMaxChars: limit})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	indexed := func(s *SQLiteStore, query string, args ...any) string {
		t.Helper()
		var content string
		if err := s.db.QueryRowContext(ctx, query, args...).Scan(&content); err != nil {
			t.Fatal(err)
		}
		return content
	}
	issueText := func(s *SQLiteStore) string {
		return indexed(s, `SELECT content FROM issue_search_documents WHERE workspace_key=? AND issue_id=?`, "test-workspace", "issue_long")
	}
	documentText := func(s *SQLiteStore) string {
		return indexed(s, `SELECT content FROM metadata_search_documents WHERE workspace_key=? AND field='documents' AND record_key=?`, "test-workspace", "document_long")
	}
	body := "needle-start " + strings.Repeat("数据 filler ", 400) + " needle-tail"

	s := open(1000)
	if err := s.MutateWithAggregate(ctx, "issue.created", nil, func(data *domain.Bootstrap) (string, error) {
		issue := data.Issues[0]
		issue.ID, issue.Identifier, issue.Number, issue.Title, issue.Description = "issue_long", "TST-990", 990, "Long issue", body
		data.Issues = append([]domain.Issue{issue}, data.Issues...)
		return issue.ID, nil
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.MutateWorkspace(ctx, "test-workspace", "document.created", "document_long", nil, func(data *domain.Bootstrap) error {
		data.Documents = append(data.Documents, domain.Document{ID: "document_long", SlugID: "long", Title: "Long document", Content: body, TeamIDs: []string{}, ProjectIDs: []string{}, SubscriberIDs: []string{}, CreatedAt: now, UpdatedAt: now, Revisions: []domain.DocumentRevision{}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"issue": issueText(s), "document": documentText(s)} {
		if utf8.RuneCountInString(text) > 1000 || !strings.Contains(text, "needle-start") || strings.Contains(text, "needle-tail") {
			t.Fatalf("capped %s index holds %d characters (tail indexed: %v)", name, utf8.RuneCountInString(text), strings.Contains(text, "needle-tail"))
		}
	}
	s.Close()

	// Lifting the cap re-indexes the full text.
	s = open(0)
	if text := issueText(s); !strings.Contains(text, "needle-tail") {
		t.Fatalf("uncapped issue index holds %d characters", utf8.RuneCountInString(text))
	}
	if text := documentText(s); !strings.Contains(text, "needle-tail") {
		t.Fatalf("uncapped document index holds %d characters", utf8.RuneCountInString(text))
	}
	s.Close()

	// Lowering it again truncates what is stored, and later writes stay capped.
	s = open(1500)
	defer s.Close()
	if text := issueText(s); utf8.RuneCountInString(text) != 1500 {
		t.Fatalf("issue index after lowering the cap holds %d characters", utf8.RuneCountInString(text))
	}
	if text := documentText(s); utf8.RuneCountInString(text) != 1500 {
		t.Fatalf("document index after lowering the cap holds %d characters", utf8.RuneCountInString(text))
	}
	if err := s.MutateWorkspace(ctx, "test-workspace", "document.updated", "document_long", nil, func(data *domain.Bootstrap) error {
		for index := range data.Documents {
			if data.Documents[index].ID == "document_long" {
				data.Documents[index].Content = "edited " + body
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if text := documentText(s); utf8.RuneCountInString(text) != 1500 || !strings.Contains(text, "edited") {
		t.Fatalf("edited document index holds %d characters", utf8.RuneCountInString(text))
	}
}
