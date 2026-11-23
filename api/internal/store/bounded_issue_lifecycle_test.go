package store

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestBoundedIssueLifecycleTransaction(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	fixture := scaleP09Database(t)
	workspace := fixture.data.Workspace.URLKey
	actor := fixture.data.Viewer
	issue := domain.Issue{ID: "bounded-delete", Identifier: "DEL-123", Number: 123,
		Team: fixture.data.Teams[0], State: fixture.data.States[0], Creator: actor,
		Title: "Delete transaction", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Version: 1}
	err := fixture.MutateWorkspace(t.Context(), workspace, "issue-delete.fixture", "", nil, func(data *domain.Bootstrap) error {
		data.Issues = append(data.Issues, issue)
		data.Comments[issue.ID] = []domain.Comment{{ID: "delete-transaction-comment", Body: "Retained after rollback", CreatedAt: issue.CreatedAt}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := fixture.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	counts := func() (issues, trash, comments int) {
		t.Helper()
		if err := fixture.db.QueryRowContext(t.Context(), `SELECT
			(SELECT COUNT(*) FROM issue_records WHERE workspace_key=? AND id=?),
			(SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field='trash'),
			(SELECT COUNT(*) FROM workspace_content_records WHERE workspace_key=? AND kind='comment' AND resource_id=?)`,
			workspace, issue.ID, workspace, workspace, issue.ID).Scan(&issues, &trash, &comments); err != nil {
			t.Fatal(err)
		}
		return issues, trash, comments
	}
	events := scaleP21SW63EventIDs(t, fixture.SQLiteStore, workspace)
	installed := view.metadataResidency.logical[workspace]
	callbacks, webhooks, realtime := 0, 0, 0
	view.SetWebhookSink(func(string, domain.DomainEvent) { webhooks++ })
	view.SetRealtimeSink(func(string, domain.RealtimeEvent) { realtime++ })
	write := func(ctx context.Context) error {
		var closes []func()
		defer func() {
			for _, close := range slices.Backward(closes) {
				close()
			}
		}()
		ctx = WithMetadataOutputOwner(ctx, func(close func()) bool {
			closes = append(closes, close)
			return true
		})
		ctx = WithIssueRecordMutations(ContextWithActor(ctx, actor), issue.ID)
		return view.DeleteIssueScoped(ctx, workspace, IssueDeleteSelection{ID: issue.ID, Principal: actor.ID}, map[string]string{"id": issue.ID},
			func(data domain.Bootstrap, target domain.Issue) bool {
				return data.ViewerRole == "owner" && target.ID == issue.ID
			},
			func(data *domain.Bootstrap) error {
				callbacks++
				data.Trash = []domain.TrashEntry{{ID: "delete-transaction-trash", ResourceType: "issue", ResourceID: issue.ID, DeletedAt: issue.CreatedAt}}
				data.Issues = nil
				delete(data.Comments, issue.ID)
				return nil
			})
	}
	checkUnchanged := func() {
		t.Helper()
		issues, trash, comments := counts()
		if issues != 1 || trash != 0 || comments != 1 || !maps.Equal(events, scaleP21SW63EventIDs(t, fixture.SQLiteStore, workspace)) ||
			view.metadataResidency.logical[workspace] != installed || webhooks != 0 || realtime != 0 || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("aborted deletion changed stored rows, published, or retained work")
		}
	}

	// The retained policy predates the role downgrade, but the writer reads
	// current membership and the selected issue under its own transaction.
	setRole := func(role string) {
		t.Helper()
		if _, err := fixture.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET role=? WHERE workspace_id=? AND user_id=?`,
			role, fixture.data.Workspace.ID, actor.ID); err != nil {
			t.Fatal(err)
		}
	}
	setRole("member")
	if err := write(t.Context()); !errors.Is(err, ErrAuthForbidden) || callbacks != 0 {
		t.Fatalf("stale authorization reached the callback: %v", err)
	}
	checkUnchanged()
	setRole("owner")

	prior := fixture.db.commitHooks.Load()
	defer fixture.db.commitHooks.Store(prior)
	failure := errors.New("reject issue deletion commit")
	fixture.db.commitHooks.Store(&commitHooks{beforeCommit: func(tx *sqlTx) { tx.writeErr = failure }})
	if err := write(t.Context()); !errors.Is(err, failure) || callbacks != 1 {
		t.Fatalf("commit failure did not reach the caller: %v", err)
	}
	checkUnchanged()

	attempts, issueReads := 0, 0
	fixture.db.commitHooks.Store(&commitHooks{beforeCommit: func(tx *sqlTx) {
		attempts++
		if attempts == 1 {
			tx.writeErr = ErrConcurrentWrite
		}
	}})
	ctx := WithSQLStatementObserver(t.Context(), func(query string) {
		if strings.HasPrefix(query, "SELECT") && strings.Contains(query, "FROM issue_records") {
			issueReads++
			if !strings.Contains(query, "id IN") && !strings.Contains(query, "id=?") && !strings.Contains(query, "id = ?") {
				t.Error("issue deletion read outside the selected issue IDs or parent IDs")
			}
		}
	})
	slots, accessors := ColdSlotAcquisitionsForTesting(), ColdAccessorReadsForTesting()
	if err := write(ctx); err != nil || attempts != 2 || callbacks != 3 || webhooks != 1 || realtime != 1 {
		t.Fatalf("retried deletion did not publish once: err=%v commits=%d callbacks=%d webhooks=%d realtime=%d", err, attempts, callbacks, webhooks, realtime)
	}
	fixture.db.commitHooks.Store(prior)
	issues, trash, comments := counts()
	if issues != 0 || trash != 1 || comments != 0 || len(scaleP21SW63EventIDs(t, fixture.SQLiteStore, workspace)) != len(events)+1 ||
		view.metadataResidency.logical[workspace] == installed || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("retried deletion did not commit one complete postimage")
	}
	if issueReads == 0 || issueReads > 30 || ColdSlotAcquisitionsForTesting() != slots || ColdAccessorReadsForTesting() != accessors {
		t.Fatalf("issue deletion exceeded its selected read path: issue queries=%d", issueReads)
	}
}
