package main

import (
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// pinIssueFixtureAges puts each arm on the same side of the creator's
// three-minute activity window. Start from the original fixture each time
// so backdating preserves creation order and fresh issues start at reset.
func pinIssueFixtureAges(fresh bool, ids ...string) func(*domain.Bootstrap) {
	return func(data *domain.Bootstrap) {
		now := time.Now().UTC()
		for index := range data.Issues {
			issue := &data.Issues[index]
			if len(ids) != 0 && !slices.Contains(ids, issue.ID) {
				continue
			}
			if fresh {
				issue.CreatedAt = now
			} else {
				issue.CreatedAt = issue.CreatedAt.Add(-time.Hour)
			}
		}
	}
}

func pinCreatedIssueAges(t *testing.T, repo *store.SQLiteStore, workspace string, fresh bool, ids ...string) {
	t.Helper()
	ctx := store.WithIssueRecordMutations(workspaceFixtureContext(t.Context()), ids...)
	pin := pinIssueFixtureAges(fresh, ids...)
	if err := repo.MutateWorkspace(ctx, workspace, "test.issue_ages", "", nil, func(data *domain.Bootstrap) error {
		for _, id := range ids {
			if _, err := issueByID(data, id); err != nil {
				return err
			}
		}
		pin(data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
