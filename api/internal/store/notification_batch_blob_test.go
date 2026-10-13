package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// A notification batch update (mark read, archive, snooze…) must keep the
// content record a BLOB: SQLite's json_set returns TEXT, and a TEXT row made
// the bootstrap stream fail while scanning notifications, which cut the
// /api/bootstrap response short mid-JSON. Rows already stored as TEXT must
// still be readable.
func TestNotificationBatchKeepsContentRecordsReadable(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "blob.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	workspace, viewer := data.Workspace.URLKey, data.Viewer.ID
	now := time.Now().UTC()
	err = repo.MutateWorkspace(t.Context(), workspace, "test.notifications", "", nil, func(d *domain.Bootstrap) error {
		d.Notifications = append(d.Notifications,
			domain.Notification{ID: "notification_blob_a", RecipientID: viewer, Type: "issueComment", SourceType: "issue", SourceID: "issue_x", Actor: d.Viewer, CreatedAt: now, UpdatedAt: now},
			domain.Notification{ID: "notification_blob_b", RecipientID: viewer, Type: "issueComment", SourceType: "issue", SourceID: "issue_y", Actor: d.Viewer, CreatedAt: now, UpdatedAt: now},
		)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.BatchNotificationRecords(t.Context(), workspace, viewer, domain.NotificationBatchInput{Action: "markRead", IDs: []string{"notification_blob_a"}}, nil); err != nil {
		t.Fatal(err)
	}
	var kind string
	if err := repo.db.QueryRowContext(t.Context(), `SELECT typeof(data) FROM workspace_content_records WHERE workspace_key=? AND kind='notification' AND id='notification_blob_a'`, workspace).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind != "blob" {
		t.Fatalf("batch update stored data as %s, want blob", kind)
	}
	// A row left as TEXT by an earlier version still streams.
	if _, err := repo.db.ExecContext(t.Context(), `UPDATE workspace_content_records SET data=CAST(data AS TEXT) WHERE workspace_key=? AND kind='notification' AND id='notification_blob_b'`, workspace); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	err = repo.WalkContentRecords(t.Context(), workspace, "notification", func(_ string, raw json.RawMessage) error {
		var item domain.Notification
		if err := json.Unmarshal(raw, &item); err != nil {
			return err
		}
		seen[item.ID] = true
		if item.ID == "notification_blob_a" && item.ReadAt == nil {
			t.Error("markRead was not applied")
		}
		return nil
	})
	if err != nil || !seen["notification_blob_a"] || !seen["notification_blob_b"] {
		t.Fatalf("walk: %v %v", err, seen)
	}
}
