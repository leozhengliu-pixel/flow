package store

import (
	"context"
	"encoding/json"
	"flow/api/internal/domain"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestListProjectionAndAttachmentSearchIndexes(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "indexes.db")
	repo, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	issue := data.Issues[0]
	issue.ID = "indexed-read"
	issue.Identifier = "IDX-42"
	issue.Description = "Distinctive全文查询 " + string(make([]byte, 1<<16))
	issue.Attachments = []domain.Attachment{{ID: "indexed-file", URL: "/uploads/bounded.png"}}
	if err := repo.ImportIssues(ctx, data.Workspace.URLKey, []domain.Issue{issue}); err != nil {
		t.Fatal(err)
	}
	q := IssueRecordQuery{Workspace: data.Workspace.URLKey, Filter: IssueFilter{Field: "id", Values: []string{issue.ID}}, Summary: true}
	page, err := repo.QueryIssueRecords(ctx, q)
	if err != nil || len(page.Items) != 1 {
		t.Fatal(err)
	}
	if !page.Items[0].IsSummary || page.Items[0].Description != "" {
		t.Fatal("list includes description")
	}
	visible, err := repo.IssueAttachmentVisible(ctx, q, "/uploads/bounded.png")
	if err != nil || !visible {
		t.Fatalf("attachment index %v %v", visible, err)
	}
	q.AllowedTeamIDs = []string{}
	visible, err = repo.IssueAttachmentVisible(ctx, q, "/uploads/bounded.png")
	if err != nil || visible {
		t.Fatal("attachment permission leak")
	}
	q.AllowedTeamIDs = nil
	for _, term := range []string{"IDX-42", "全文查询"} {
		found := false
		err := repo.SearchIssueCandidates(ctx, q, term, nil, 10, func(item domain.Issue) error { found = found || item.ID == issue.ID; return nil })
		if err != nil || !found {
			t.Fatalf("search %s found=%v err=%v", term, found, err)
		}
	}
}

func TestIssueHistoryPagesPreserveAllRecords(t *testing.T) {
	ctx := context.Background()
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	id := data.Issues[0].ID
	tx, err := repo.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 255; i++ {
		comment := domain.Comment{ID: fmt.Sprintf("history-%04d", i), User: data.Viewer, Body: "comment", CreatedAt: time.Date(2026, 9, 1, 0, 0, i, 0, time.UTC)}
		if err := writeContentRecord(ctx, tx, data.Workspace.URLKey, "comment", id, comment); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	cursor := ""
	seen := map[string]bool{}
	for {
		page, err := repo.IssueHistoryPage(ctx, data.Workspace.URLKey, id, cursor, "-")
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Comments) > 100 {
			t.Fatal("history page unbounded")
		}
		for _, item := range page.Comments {
			if seen[item.ID] {
				t.Fatal("duplicate history item")
			}
			seen[item.ID] = true
		}
		if page.CommentsCursor == "" {
			break
		}
		cursor = page.CommentsCursor
	}
	if len(seen) != 255 {
		t.Fatalf("missing comments: %d", len(seen))
	}
}

func TestSnapshotCloneDoesNotAliasMutableFields(t *testing.T) {
	data := domain.Bootstrap{Issues: []domain.Issue{{ID: "one", Description: "immutable", Reactions: map[string][]string{"heart": {"user"}}}}, Settings: map[string]any{"nested": map[string]any{"items": []any{"before"}}}}
	clone := cloneBootstrap(data)
	clone.Issues[0].Reactions["heart"][0] = "changed"
	clone.Settings["nested"].(map[string]any)["items"].([]any)[0] = "changed"
	if data.Issues[0].Reactions["heart"][0] != "user" || data.Settings["nested"].(map[string]any)["items"].([]any)[0] != "before" {
		t.Fatal("clone changed original")
	}
	before, _ := json.Marshal(data)
	after, _ := json.Marshal(clone)
	if string(before) == string(after) {
		t.Fatal("clone did not mutate")
	}
}
