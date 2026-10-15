package store

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
)

func TestSearchDocumentIDsMatchesEveryOrAnyTermAnywhere(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "documents.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	workspace := repo.Bootstrap().Workspace.URLKey
	for _, document := range []domain.Document{
		{ID: "doc-uat", Title: "智能印控平台 UAT 方案", Content: "## 准出标准\n所有 P0 缺陷关闭"},
		{ID: "doc-apart", Title: "Notes", Content: "UAT 安排见下。\n\n其余内容……智能印控的范围另行说明。"},
		{ID: "doc-partial", Title: "印控 overview", Content: "智能印控平台介绍"},
		{ID: "doc-english", Title: "Release checklist", Content: "The UAT exit criteria: zero BLOCKER bugs."},
	} {
		insertSearchMetadata(t, repo, workspace, "documents", document.ID, document)
	}
	insertSearchMetadata(t, repo, workspace, "projects", "project-uat", domain.Project{ID: "project-uat", Name: "UAT 智能印控"})
	search := func(matchAll bool, terms ...string) []string {
		t.Helper()
		ids, err := repo.SearchDocumentIDs(context.Background(), workspace, terms, matchAll, 50)
		if err != nil {
			t.Fatal(err)
		}
		slices.Sort(ids)
		return ids
	}
	// Two- and three-character Chinese terms (LIKE and trigram paths), not adjacent; projects never match.
	if got := search(true, "智能印控", "UAT"); !slices.Equal(got, []string{"doc-apart", "doc-uat"}) {
		t.Fatalf("all Chinese and English terms = %v", got)
	}
	if got := search(true, "印控", "准出"); !slices.Equal(got, []string{"doc-uat"}) {
		t.Fatalf("two-character terms = %v", got)
	}
	if got := search(true, "exit", "blocker"); !slices.Equal(got, []string{"doc-english"}) {
		t.Fatalf("case-insensitive English terms = %v", got)
	}
	if got := search(true, "智能印控", "missingterm"); len(got) != 0 {
		t.Fatalf("every term required, got %v", got)
	}
	if got := search(false, "智能印控", "missingterm"); !slices.Equal(got, []string{"doc-apart", "doc-partial", "doc-uat"}) {
		t.Fatalf("any term = %v", got)
	}
	if got := search(true, " ", ""); len(got) != 0 {
		t.Fatalf("blank terms = %v", got)
	}
}
