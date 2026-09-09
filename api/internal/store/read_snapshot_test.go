package store

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
)

func TestWorkspaceReadKeepsOutlineAndBodiesConsistent(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "snapshot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	repo.db.SetMaxOpenConns(2)
	initial := repo.Bootstrap()
	key := initial.Workspace.URLKey
	issue := initial.Issues[0]
	ctx, release, err := repo.BeginWorkspaceRead(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	outline, err := repo.BootstrapOutline(ctx, key, initial.Viewer.ID)
	if err != nil || len(outline.Issues) == 0 {
		t.Fatalf("outline: %v", err)
	}
	err = repo.MutateWorkspace(WithIssueRecordMutations(t.Context(), issue.ID), key, "issue.updated", issue.ID, nil, func(data *domain.Bootstrap) error {
		for i := range data.Issues {
			if data.Issues[i].ID == issue.ID {
				data.Issues[i].Description = "Written after the export started"
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	err = repo.WalkIssueRecords(ctx, IssueRecordQuery{Workspace: key, Archived: "all"}, func(current domain.Issue) error {
		if current.ID == issue.ID {
			found = true
			if current.Description != issue.Description {
				t.Fatal("stream mixed an old outline with a new body")
			}
		}
		return nil
	})
	if err != nil || !found {
		t.Fatalf("stream: found=%v error=%v", found, err)
	}
	if err := repo.WalkContentRecords(ctx, key, "comment", func(_ string, raw json.RawMessage) error {
		var comment domain.Comment
		return json.Unmarshal(raw, &comment)
	}); err != nil {
		t.Fatal(err)
	}
	release()
	updated, err := repo.IssueRecord(t.Context(), key, issue.ID)
	if err != nil || updated.Description != "Written after the export started" {
		t.Fatalf("concurrent mutation was not committed: %v", err)
	}
}
