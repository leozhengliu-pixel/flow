package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
)

func TestIssueListRebuildPassReprojectsOldRowsOnce(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "list.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	data := repo.Bootstrap()
	issue := data.Issues[0]
	issue.ID, issue.Identifier, issue.Title = "list-old", "FLOW-900", "Old row"
	issue.Description = "has body"
	issue.SubscriberIDs = []string{"user-a"}
	issue.Reactions = map[string][]string{"+1": {"u1", "u2"}}
	if err := repo.ImportIssues(ctx, data.Workspace.URLKey, []domain.Issue{issue}); err != nil {
		t.Fatal(err)
	}
	ws := data.Workspace.URLKey
	stale := issueListProjection(issue)
	stale.HasContent, stale.SubscriberIDs, stale.ReactionCounts = false, []string{}, nil
	rawStale, _ := json.Marshal(stale)
	setList := func(raw []byte) {
		t.Helper()
		if _, err := repo.db.ExecContext(ctx, `UPDATE issue_records SET list_data=? WHERE workspace_key=? AND id=?`, raw, ws, issue.ID); err != nil {
			t.Fatal(err)
		}
	}
	read := func() domain.Issue {
		t.Helper()
		var raw []byte
		if err := repo.db.QueryRowContext(ctx, `SELECT list_data FROM issue_records WHERE workspace_key=? AND id=?`, ws, issue.ID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var got domain.Issue
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	setList(rawStale)
	if _, err := repo.db.ExecContext(ctx, `DELETE FROM issue_list_migrations WHERE workspace_key=?`, ws+issueListRebuildSuffix); err != nil {
		t.Fatal(err)
	}
	if err := repo.migrateIssueListProjection(ctx); err != nil {
		t.Fatal(err)
	}
	got := read()
	if !got.HasContent || len(got.SubscriberIDs) != 1 || got.ReactionCounts["+1"] != 2 || got.Description != "" {
		t.Fatalf("row not rebuilt: %+v", got)
	}
	// Second run is a no-op: the stale row stays untouched.
	setList(rawStale)
	if err := repo.migrateIssueListProjection(ctx); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.HasContent || len(got.SubscriberIDs) != 0 {
		t.Fatalf("rebuild pass ran twice: %+v", got)
	}
}
