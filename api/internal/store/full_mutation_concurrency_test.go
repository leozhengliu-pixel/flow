package store

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// A mutation that still loads and replaces the whole issue and content
// collections (any event outside the metadata-only allowlist) must keep the
// records other servers commit meanwhile through their own per-record
// transactions: a created issue, an updated issue, a new comment and an
// archived issue, with their versions.
func TestFullMutationKeepsRecordsCommittedOnAnotherServer(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second := pair[0], pair[1]
			ctx := t.Context()
			workspace, template := peerWorkspace(t, second)
			stamp := time.Now().UnixNano()
			updated := createPeerIssue(t, second, workspace, fmt.Sprintf("peer-update-%d", stamp), template)
			commented := createPeerIssue(t, second, workspace, fmt.Sprintf("peer-comment-%d", stamp), template)
			archived := createPeerIssue(t, second, workspace, fmt.Sprintf("peer-archive-%d", stamp), template)
			renamed := createPeerIssue(t, second, workspace, fmt.Sprintf("peer-rename-%d", stamp), template)
			removed := createPeerIssue(t, second, workspace, fmt.Sprintf("peer-remove-%d", stamp), template)

			release, wait := pausedMutation(t, func(callback func(*domain.Bootstrap) error) error {
				return first.MutateWorkspace(ctx, workspace, "test.full_rename", renamed.ID, nil, callback)
			}, func(data *domain.Bootstrap) error {
				index := slices.IndexFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == renamed.ID })
				if index < 0 {
					return fmt.Errorf("full mutation did not load the issue collection")
				}
				data.Issues[index].Title = "Renamed by the full mutation"
				data.Issues[index].Version++
				data.Issues = slices.DeleteFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == removed.ID })
				delete(data.Activities, removed.ID)
				return nil
			})

			created := createPeerIssue(t, second, workspace, fmt.Sprintf("peer-create-%d", stamp), template)
			expected := updated.Version
			updated, err := second.UpdateIssueRecord(ctx, workspace, updated.ID, &expected, IssueMutationScope{}, func(_ *domain.Bootstrap, issue *domain.Issue) error {
				issue.Title = "Updated on the other server"
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			commentID := fmt.Sprintf("peer-comment-body-%d", stamp)
			if err := second.MutateWorkspace(WithIssueRecordMutations(ctx, commented.ID), workspace, "comment.created", commented.ID, nil, func(data *domain.Bootstrap) error {
				data.Comments[commented.ID] = append(data.Comments[commented.ID], domain.Comment{ID: commentID, Body: "From the other server", User: data.Viewer, CreatedAt: time.Now().UTC()})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			expected = archived.Version
			archived, err = second.UpdateIssueRecord(ctx, workspace, archived.ID, &expected, IssueMutationScope{}, func(_ *domain.Bootstrap, issue *domain.Issue) error {
				now := time.Now().UTC()
				issue.ArchivedAt = &now
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			release()
			if _, err := wait(); err != nil {
				t.Fatal(err)
			}

			for _, want := range []domain.Issue{created, updated, archived} {
				got, err := first.IssueRecord(ctx, workspace, want.ID)
				if err != nil {
					t.Fatalf("%s was deleted: %v", want.ID, err)
				}
				if got.Version != want.Version || got.Title != want.Title || (got.ArchivedAt == nil) != (want.ArchivedAt == nil) {
					t.Fatalf("%s reverted: got version %d %q archived=%v, want version %d %q archived=%v", want.ID, got.Version, got.Title, got.ArchivedAt != nil, want.Version, want.Title, want.ArchivedAt != nil)
				}
			}
			comments, _, err := first.IssueContent(ctx, workspace, commented.ID)
			if err != nil || !slices.ContainsFunc(comments, func(comment domain.Comment) bool { return comment.ID == commentID }) {
				t.Fatalf("comment from the other server was deleted: %d %v", len(comments), err)
			}
			got, err := first.IssueRecord(ctx, workspace, renamed.ID)
			if err != nil || got.Title != "Renamed by the full mutation" || got.Version != renamed.Version+1 {
				t.Fatalf("full mutation's own change: %+v %v", got, err)
			}
			if _, err := first.IssueRecord(ctx, workspace, removed.ID); err == nil {
				t.Fatal("the issue the full mutation deleted remains")
			}
			if total, err := first.issueCollectionCounts(ctx); err != nil || total[workspace] != countIssueRows(t, first, workspace) {
				t.Fatalf("issue count %d, rows %d (%v)", total[workspace], countIssueRows(t, first, workspace), err)
			}
		})
	}
}

// When the other server changed a record the full mutation changes too, the
// full mutation runs again on the fresh records instead of overwriting them.
func TestFullMutationRetriesWhenItsRecordChangedOnAnotherServer(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second := pair[0], pair[1]
			ctx := t.Context()
			workspace, template := peerWorkspace(t, second)
			target := createPeerIssue(t, second, workspace, fmt.Sprintf("peer-contended-%d", time.Now().UnixNano()), template)
			release, wait := pausedMutation(t, func(callback func(*domain.Bootstrap) error) error {
				return first.MutateWorkspace(ctx, workspace, "test.full_priority", target.ID, nil, callback)
			}, func(data *domain.Bootstrap) error {
				index := slices.IndexFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == target.ID })
				if index < 0 {
					return fmt.Errorf("issue missing from the full snapshot")
				}
				data.Issues[index].Priority = 1
				data.Issues[index].Version++
				return nil
			})
			expected := target.Version
			updated, err := second.UpdateIssueRecord(ctx, workspace, target.ID, &expected, IssueMutationScope{}, func(_ *domain.Bootstrap, issue *domain.Issue) error {
				issue.Title = "Concurrent title"
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			release()
			calls, err := wait()
			if err != nil {
				t.Fatal(err)
			}
			got, err := first.IssueRecord(ctx, workspace, target.ID)
			if err != nil || got.Title != "Concurrent title" || got.Priority != 1 || got.Version != updated.Version+1 {
				t.Fatalf("contended issue: %+v %v (calls %d)", got, err, calls)
			}
			if calls != 2 {
				t.Fatalf("callback ran %d times, want 2 (once on the stale snapshot, once on the fresh one)", calls)
			}
		})
	}
}
