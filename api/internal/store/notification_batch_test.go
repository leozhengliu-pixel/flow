package store

import (
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// The default notification listing keeps read rows, and marking one unread
// clears readAt through the batch endpoint the inbox uses for "mark unread".
func TestNotificationBatchMarkUnreadClearsReadAt(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "notification-batch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	workspace, viewer := data.Workspace.URLKey, data.Viewer.ID
	now := time.Now().UTC()
	readAt := now.Add(-time.Hour)
	err = repo.MutateWorkspace(t.Context(), workspace, "test.notifications", "", nil, func(d *domain.Bootstrap) error {
		d.Notifications = append(d.Notifications,
			domain.Notification{ID: "notification_read", RecipientID: viewer, Type: "issueComment", SourceType: "issue", SourceID: "issue_x", Actor: d.Viewer, ReadAt: &readAt, CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: readAt},
			domain.Notification{ID: "notification_unread", RecipientID: viewer, Type: "issueComment", SourceType: "issue", SourceID: "issue_y", Actor: d.Viewer, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)},
		)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	page, err := repo.QueryNotifications(t.Context(), NotificationQuery{Workspace: workspace, UserID: viewer})
	if err != nil {
		t.Fatal(err)
	}
	readListed := false
	for _, notification := range page.Notifications {
		if notification.ID == "notification_read" && notification.ReadAt != nil {
			readListed = true
		}
	}
	if !readListed {
		t.Fatal("default listing dropped the read notification")
	}

	updated, err := repo.BatchNotificationRecords(t.Context(), workspace, viewer, domain.NotificationBatchInput{Action: "markUnread", IDs: []string{"notification_read"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if updated != 1 {
		t.Fatalf("markUnread updated %d notifications, want 1", updated)
	}

	page, err = repo.QueryNotifications(t.Context(), NotificationQuery{Workspace: workspace, UserID: viewer})
	if err != nil {
		t.Fatal(err)
	}
	for _, notification := range page.Notifications {
		if notification.ID == "notification_read" && notification.ReadAt != nil {
			t.Fatalf("markUnread left readAt at %s", notification.ReadAt)
		}
	}
}
