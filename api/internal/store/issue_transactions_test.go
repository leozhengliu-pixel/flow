package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestIssueRecordTransactionsPreserveScopeAndRollback(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	initial := repo.Bootstrap()
	workspace := initial.Workspace.URLKey
	base := initial.Issues[0]
	var hooks []domain.DomainEvent
	var realtime []domain.RealtimeEvent
	repo.SetWebhookSink(func(_ string, event domain.DomainEvent) { hooks = append(hooks, event) })
	repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = append(realtime, event) })
	create := func(id string, parent *string) domain.Issue {
		t.Helper()
		err := repo.MutateWorkspaceWithAggregate(WithIssueRecordMutations(ctx), workspace, "issue.created", map[string]any{"parentId": parent}, func(data *domain.Bootstrap) (string, error) {
			for _, existing := range data.Issues {
				if existing.ID == base.ID {
					t.Fatal("create hydrated an unrelated issue")
				}
			}
			issue := base
			issue.ID = id
			issue.Identifier = "TX-" + id
			issue.Number = data.NextIssueNumber
			issue.Version = 1
			issue.ParentID = parent
			issue.SubIssueIDs = []string{}
			issue.CreatedAt = time.Now().UTC()
			issue.UpdatedAt = issue.CreatedAt
			data.Issues = append(data.Issues, issue)
			data.Activities[id] = []domain.ActivityEvent{{ID: "event-" + id, Type: "issue.created", Actor: initial.Viewer, CreatedAt: issue.CreatedAt}}
			data.Comments[id] = []domain.Comment{{ID: "comment-" + id, Body: "Original", User: initial.Viewer, CreatedAt: issue.CreatedAt}}
			data.Notifications = append(data.Notifications, domain.Notification{ID: "notification-" + id, IssueID: id, RecipientID: initial.Viewer.ID, Actor: initial.Viewer, CreatedAt: issue.CreatedAt, UpdatedAt: issue.CreatedAt})
			data.NotificationDeliveries = append(data.NotificationDeliveries, domain.NotificationDelivery{ID: "delivery-" + id, NotificationID: "notification-" + id, RecipientID: initial.Viewer.ID, Channel: "email", Status: "pending", CreatedAt: issue.CreatedAt, UpdatedAt: issue.CreatedAt})
			return id, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		issue, err := repo.IssueRecord(ctx, workspace, id)
		if err != nil {
			t.Fatal(err)
		}
		return issue
	}
	parent := create("parent", nil)
	child := create("child", &parent.ID)
	if child.Number <= parent.Number {
		t.Fatal("creation reused the issue sequence")
	}
	if len(hooks) != 2 || len(realtime) != 2 || realtime[1].AggregateID != child.ID {
		t.Fatal("committed creation did not publish the entity")
	}
	metadata, _ := repo.WorkspaceMetadata(workspace)
	counts, err := repo.issueCollectionCounts(ctx)
	if err != nil || len(metadata.Issues) != 0 || len(metadata.Comments) != 0 || counts[workspace] != len(initial.Issues)+2 {
		t.Fatalf("metadata no longer bounded: %+v %v", counts, err)
	}
	expected := parent.Version
	updated, err := repo.UpdateIssueRecord(ctx, workspace, parent.ID, &expected, IssueMutationScope{IncludeFamily: true}, func(data *domain.Bootstrap, issue *domain.Issue) error {
		if len(data.Issues) != 2 || !slices.ContainsFunc(data.Issues, func(v domain.Issue) bool { return v.ID == child.ID }) {
			t.Fatal("mutation lost the immediate family or loaded unrelated rows")
		}
		if len(data.NotificationDeliveries) != 1 {
			t.Fatal("mutation did not load the target delivery")
		}
		issue.Title = "Updated parent"
		data.Activities[issue.ID] = []domain.ActivityEvent{{ID: "updated-parent", Type: "issue.updated", Actor: initial.Viewer, CreatedAt: time.Now().UTC()}}
		return nil
	})
	if err != nil || updated.Version != expected+1 || updated.Title != "Updated parent" {
		t.Fatalf("update: %+v %v", updated, err)
	}
	beforeEvents := len(hooks)
	_, err = repo.UpdateIssueRecord(ctx, workspace, parent.ID, &expected, IssueMutationScope{}, func(*domain.Bootstrap, *domain.Issue) error { t.Fatal("stale callback ran"); return nil })
	if !errors.Is(err, ErrIssueVersion) {
		t.Fatalf("stale update: %v", err)
	}
	rejected := errors.New("reject transaction")
	_, err = repo.UpdateIssueRecord(ctx, workspace, parent.ID, nil, IssueMutationScope{}, func(_ *domain.Bootstrap, issue *domain.Issue) error { issue.Title = "Must roll back"; return rejected })
	if !errors.Is(err, rejected) || len(hooks) != beforeEvents {
		t.Fatal("rejected mutation published an event")
	}
	saved, err := repo.IssueRecord(ctx, workspace, parent.ID)
	if err != nil || saved.Title != updated.Title || saved.Version != updated.Version {
		t.Fatal("rejected mutation changed persisted state")
	}
	err = repo.MutateWorkspace(WithIssueRecordMutations(ctx, child.ID), workspace, "comment.updated", child.ID, nil, func(data *domain.Bootstrap) error {
		if slices.ContainsFunc(data.Issues, func(v domain.Issue) bool { return v.ID == base.ID }) {
			t.Fatal("comment mutation hydrated unrelated issue")
		}
		data.Comments[child.ID][0].Body = "Edited only child"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	comments, _, err := repo.IssueContent(ctx, workspace, child.ID)
	if err != nil || len(comments) != 1 || comments[0].Body != "Edited only child" {
		t.Fatalf("comment persistence: %+v %v", comments, err)
	}
	parentComments, history, err := repo.IssueContent(ctx, workspace, parent.ID)
	if err != nil || len(parentComments) != 1 || parentComments[0].Body != "Original" || len(history) != 2 {
		t.Fatal("scoped comment mutation damaged other content")
	}
	err = repo.MutateWorkspace(WithIssueRecordMutations(ctx, parent.ID), workspace, "issue.deleted", parent.ID, nil, func(data *domain.Bootstrap) error {
		data.Issues = slices.DeleteFunc(data.Issues, func(v domain.Issue) bool { return v.ID == parent.ID })
		for i := range data.Issues {
			if data.Issues[i].ID == child.ID {
				data.Issues[i].ParentID = nil
			}
		}
		delete(data.Comments, parent.ID)
		delete(data.Activities, parent.ID)
		data.Notifications = slices.DeleteFunc(data.Notifications, func(n domain.Notification) bool { return n.IssueID == parent.ID })
		data.NotificationDeliveries = slices.DeleteFunc(data.NotificationDeliveries, func(d domain.NotificationDelivery) bool { return d.ID == "delivery-parent" })
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.IssueRecord(ctx, workspace, parent.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted parent remains: %v", err)
	}
	saved, err = repo.IssueRecord(ctx, workspace, child.ID)
	if err != nil || saved.ParentID != nil {
		t.Fatal("child reference was not detached")
	}
	counts, err = repo.issueCollectionCounts(ctx)
	if err != nil || counts[workspace] != len(initial.Issues)+1 {
		t.Fatal("deleted issue was not removed from collection counts")
	}
	var raw []byte
	if err = repo.db.QueryRowContext(ctx, "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var stored domain.Bootstrap
	if err = json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.Issues) != 0 || len(stored.Comments) != 0 || len(stored.Notifications) != 0 {
		t.Fatal("transaction wrote collections back to metadata")
	}
}

func TestNotificationDeliveryTransactionsSelectDueRowsAndRollback(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	data := repo.Bootstrap()
	key := data.Workspace.URLKey
	issue := data.Issues[0]
	now := time.Now().UTC()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	err = repo.MutateWorkspace(ctx, key, "notification.created", "delivery-test", nil, func(data *domain.Bootstrap) error {
		data.Notifications = append(data.Notifications, domain.Notification{ID: "due-notification", IssueID: issue.ID, RecipientID: data.Viewer.ID, Actor: data.Viewer, CreatedAt: past, UpdatedAt: past})
		data.NotificationDeliveries = append(data.NotificationDeliveries, domain.NotificationDelivery{ID: "due", NotificationID: "due-notification", Channel: "email", Status: "failed", NextAttemptAt: &past, CreatedAt: past}, domain.NotificationDelivery{ID: "future", NotificationID: "due-notification", Channel: "email", Status: "failed", NextAttemptAt: &future, CreatedAt: past})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !repo.HasNotificationDeliveries(ctx, key) {
		t.Fatal("delivery availability ignored row storage")
	}
	snapshot, err := repo.NotificationDeliverySnapshot(ctx, key, []string{"failed"}, now)
	if err != nil || len(snapshot.NotificationDeliveries) != 1 || snapshot.NotificationDeliveries[0].ID != "due" || len(snapshot.Issues) != 1 || snapshot.Issues[0].ID != issue.ID {
		t.Fatalf("due selection: %+v %v", snapshot.NotificationDeliveries, err)
	}
	rejected := errors.New("delivery rejected")
	err = repo.MutateNotificationDelivery(ctx, key, "due", func(d *domain.NotificationDelivery) error { d.Status = "sent"; return rejected })
	if !errors.Is(err, rejected) {
		t.Fatal(err)
	}
	snapshot, err = repo.NotificationDeliverySnapshot(ctx, key, []string{"failed"}, now)
	if err != nil || len(snapshot.NotificationDeliveries) != 1 {
		t.Fatal("failed delivery mutation was not rolled back")
	}
	err = repo.MutateNotificationDelivery(ctx, key, "due", func(d *domain.NotificationDelivery) error {
		d.Status = "sent"
		d.Attempts++
		d.DeliveredAt = &now
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = repo.NotificationDeliverySnapshot(ctx, key, []string{"sent"}, now)
	if err != nil || len(snapshot.NotificationDeliveries) != 1 || snapshot.NotificationDeliveries[0].Attempts != 1 || snapshot.NotificationDeliveries[0].DeliveredAt == nil {
		t.Fatal("delivery result was not persisted")
	}
	if err = repo.MutateNotificationDelivery(ctx, key, "missing", func(*domain.NotificationDelivery) error { return nil }); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing delivery: %v", err)
	}
}
