package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestStorageMaintenancePurgesTrashPastTheRetentionWindow(t *testing.T) {
	previous := trashRetention
	trashRetention = 7 * 24 * time.Hour
	t.Cleanup(func() { trashRetention = previous })
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	handler := newHandler(s)
	ctx := context.Background()

	document := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"title": "Old plan"}, http.StatusCreated)
	requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/documents/"+document.ID+"/comments", map[string]any{"body": "Thread"}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodDelete, "/api/documents/"+document.ID, nil, http.StatusNoContent)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if bootstrap.TrashRetentionDays != 7 {
		t.Fatalf("bootstrap trash retention = %d days, want 7", bootstrap.TrashRetentionDays)
	}
	entry := bootstrap.Trash[slices.IndexFunc(bootstrap.Trash, func(item domain.TrashEntry) bool { return item.ResourceID == document.ID })]
	if want := entry.DeletedAt.Add(7 * 24 * time.Hour); !entry.ExpiresAt.Equal(want) {
		t.Fatalf("new trash entry expires %s, want %s", entry.ExpiresAt, want)
	}

	now := time.Now().UTC()
	if err := repository.MutateWorkspace(ctx, "test-workspace", "test.trash", "trash", nil, func(data *domain.Bootstrap) error {
		for index := range data.Trash {
			if data.Trash[index].ID == entry.ID {
				// Deleted ten days ago under the old 30-day window.
				data.Trash[index].DeletedAt = now.Add(-10 * 24 * time.Hour)
				data.Trash[index].ExpiresAt = now.Add(20 * 24 * time.Hour)
			}
		}
		for i := 0; i < 2*trashPurgeBatch+50; i++ {
			payload, _ := json.Marshal(map[string]string{"id": fmt.Sprintf("old_issue_%d", i)})
			data.Trash = append(data.Trash, domain.TrashEntry{ID: fmt.Sprintf("trash_old_%d", i), ResourceType: "issue", ResourceID: fmt.Sprintf("old_issue_%d", i), Title: "Old", Payload: payload, DeletedAt: now.Add(-40 * 24 * time.Hour), ExpiresAt: now.Add(-10 * 24 * time.Hour)})
		}
		data.Trash = append(data.Trash, domain.TrashEntry{ID: "trash_recent", ResourceType: "issue", ResourceID: "recent_issue", Title: "Recent", Payload: json.RawMessage(`{}`), DeletedAt: now.Add(-24 * time.Hour), ExpiresAt: now.Add(29 * 24 * time.Hour)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	purged, err := s.purgeExpiredTrash(ctx, "test-workspace", now)
	if err != nil {
		t.Fatal(err)
	}
	if want := 2*trashPurgeBatch + 51; purged != want {
		t.Fatalf("purged %d entries, want %d", purged, want)
	}
	data, _ := repository.WorkspaceMetadataFields("test-workspace", "trash")
	if len(data.Trash) != 1 || data.Trash[0].ID != "trash_recent" {
		t.Fatalf("trash after purge = %#v", data.Trash)
	}
	if comments, _, err := repository.IssueContent(ctx, "test-workspace", document.ID); err != nil || len(comments) != 0 {
		t.Fatalf("purge left the deleted document's comments: %#v err=%v", comments, err)
	}
	if again, err := s.purgeExpiredTrash(ctx, "test-workspace", now); err != nil || again != 0 {
		t.Fatalf("second purge = %d (%v)", again, err)
	}
}

func TestRealtimeCursorFromBeforeRetentionAsksForResync(t *testing.T) {
	ctx := context.Background()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	newHandler(s)
	var lastEvent string
	repository.SetRealtimeSink(func(workspace string, event domain.RealtimeEvent) {
		lastEvent = event.ID
		s.realtime.publish(workspace, event)
	})
	if err := repository.MutateWorkspace(ctx, "test-workspace", "label.created", "label_old", nil, func(data *domain.Bootstrap) error {
		data.Labels = append(data.Labels, domain.IssueLabel{ID: "label_old", Name: "Old"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if lastEvent == "" {
		t.Fatal("no realtime event was published")
	}
	// Retention removes the event from the log; a client that was offline
	// since then reconnects with it as its cursor.
	if _, err := s.pruneDomainEvents(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if events, err := repository.Events(ctx, "label_old"); err != nil || len(events) != 0 {
		t.Fatalf("events left after pruning: %d (%v)", len(events), err)
	}
	// A restarted server no longer knows the cursor either: the client
	// reloads the workspace instead of missing what changed.
	fresh := newRealtimeHub()
	channel, unsubscribe := fresh.subscribeSince("test-workspace", lastEvent)
	defer unsubscribe()
	select {
	case event := <-channel:
		if event.Type != "workspace.resync_required" {
			t.Fatalf("first event = %s, want workspace.resync_required", event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("no resync was requested")
	}
}
