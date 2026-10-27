package store

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Group counts cached by one server follow writes another server commits:
// re-parenting, status changes, team moves, archiving, an issue shared with a
// viewer and a customer request. A change that bypasses the issue writers
// keeps the cached count, so the cache is what serves the repeated reads.
func TestIssueGroupCountsFollowWritesOnAnotherServer(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second := pair[0], pair[1]
			ctx := t.Context()
			workspace, template := peerWorkspace(t, second)
			data, ok := second.WorkspaceMetadata(workspace)
			if !ok {
				t.Fatal("workspace metadata missing")
			}
			states := data.States
			if len(states) < 2 {
				t.Fatalf("fixture needs two states: %d", len(states))
			}
			// The team issues move to, which the viewer below does not see.
			other := template.Team
			other.ID = "count-other-team"
			stamp := time.Now().UnixNano()
			template.State = states[0]
			parent := createPeerIssue(t, second, workspace, fmt.Sprintf("count-parent-%d", stamp), template)
			template.State = states[1]
			child := createPeerIssue(t, second, workspace, fmt.Sprintf("count-child-%d", stamp), template)

			// A viewer who sees the template's team only.
			member := &IssueRecordAccess{UserID: "count-viewer", WorkspaceID: data.Workspace.ID, VisibleTeamIDs: []string{template.Team.ID}}
			queries := []IssueRecordQuery{
				{Workspace: workspace, Archived: "false", GroupBy: "status", Nested: true},
				{Workspace: workspace, Archived: "false", GroupBy: "status", Nested: true, TeamIDs: []string{template.Team.ID}, Access: member},
				{Workspace: workspace, Archived: "false", GroupBy: "status", Nested: true, Access: member},
				{Workspace: workspace, Archived: "false", GroupBy: "status"},
			}
			read := func() [][]IssueRecordGroup {
				t.Helper()
				result := [][]IssueRecordGroup{}
				for _, q := range queries {
					groups, err := first.QueryIssueGroups(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					result = append(result, groups)
				}
				return result
			}
			check := func(step string, previous [][]IssueRecordGroup) [][]IssueRecordGroup {
				t.Helper()
				got := read()
				for index, q := range queries {
					want, err := first.flatIssueGroups(ctx, withoutRedundantIssueAccess(q))
					if q.Nested {
						want, err = first.nestedIssueGroupsByWalk(ctx, q)
					}
					if err != nil {
						t.Fatal(err)
					}
					if !slices.Equal(got[index], want) {
						t.Fatalf("%s: query %d served %v, want %v", step, index, got[index], want)
					}
				}
				if previous != nil && slices.EqualFunc(got, previous, slices.Equal) {
					t.Fatalf("%s: counts did not change: %v", step, got)
				}
				return got
			}
			update := func(id string, scope IssueMutationScope, change func(*domain.Issue)) {
				t.Helper()
				current, err := second.IssueRecord(ctx, workspace, id)
				if err != nil {
					t.Fatal(err)
				}
				expected := current.Version
				if _, err := second.UpdateIssueRecord(ctx, workspace, id, &expected, scope, func(_ *domain.Bootstrap, issue *domain.Issue) error {
					change(issue)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			counts := check("initial", nil)

			// A change that skips the issue writers (and so the revision) is
			// not seen: the counts come from the cache.
			if _, err := second.db.ExecContext(ctx, `UPDATE issue_records SET state_id=? WHERE workspace_key=? AND id=?`, states[0].ID, workspace, child.ID); err != nil {
				t.Fatal(err)
			}
			if got := read(); !slices.EqualFunc(got, counts, slices.Equal) {
				t.Fatalf("uncached read: %v, cached %v", got, counts)
			}
			if _, err := second.db.ExecContext(ctx, `UPDATE issue_records SET state_id=? WHERE workspace_key=? AND id=?`, states[1].ID, workspace, child.ID); err != nil {
				t.Fatal(err)
			}

			update(child.ID, IssueMutationScope{IncludeFamily: true, NewParentID: parent.ID}, func(issue *domain.Issue) { issue.ParentID = &parent.ID })
			counts = check("re-parent", counts)
			update(parent.ID, IssueMutationScope{}, func(issue *domain.Issue) { issue.State = states[1] })
			counts = check("status change", counts)
			update(parent.ID, IssueMutationScope{}, func(issue *domain.Issue) { issue.Team = other })
			counts = check("team move", counts)
			update(parent.ID, IssueMutationScope{}, func(issue *domain.Issue) {
				issue.Permissions = []domain.IssuePermission{{ID: fmt.Sprintf("count-share-%d", stamp), IssueID: issue.ID, SubjectType: "user", SubjectID: member.UserID, Role: "viewer"}}
			})
			counts = check("shared with the viewer", counts)
			update(parent.ID, IssueMutationScope{}, func(issue *domain.Issue) {
				archived := time.Now().UTC()
				issue.ArchivedAt = &archived
			})
			check("archive", counts)

			// Customer requests advance the revision too.
			before, err := first.issueCountRevision(ctx, workspace)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := second.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if err := writeCustomerFilterRecord(ctx, tx, workspace, "customerRequests", fmt.Sprintf("count-request-%d", stamp), []byte(fmt.Sprintf(`{"id":"count-request-%d","issueId":%q,"customerId":"count-customer"}`, stamp, child.ID))); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if after, err := first.issueCountRevision(ctx, workspace); err != nil || after <= before {
				t.Fatalf("customer request kept revision %d (was %d): %v", after, before, err)
			}
		})
	}
}
