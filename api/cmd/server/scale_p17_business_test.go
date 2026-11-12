package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP17DocumentBusinessDifferential(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, mode := range []string{"off", "on", "shadow", "selected"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FLOW_DOCUMENT_RECORDS", mode)
			t.Setenv("FLOW_TRASH_RECORDS", mode)
			if mode == "selected" {
				t.Setenv("FLOW_DOCUMENT_RECORDS", "on")
				t.Setenv("FLOW_TRASH_RECORDS", "on")
				t.Setenv("FLOW_DOCUMENT_RECORD_VERIFY", "off")
			}
			db := newScaleP04Database(t)
			handler := db.handler(t, true)
			project := requestJSON[domain.Project](t, handler, "POST", "/api/projects", map[string]any{"name": "P17 project", "teamIds": []string{"team_test"}}, http.StatusCreated)
			body := strings.Repeat("P17 body & history <tag> \\ \"\n", 12000)
			document := requestJSON[domain.Document](t, handler, "POST", "/api/documents", map[string]any{"title": "P17 original", "content": body, "projectIds": []string{project.ID}, "subscriberIds": []string{db.users[0].ID, db.users[1].ID}}, http.StatusCreated)
			if err := db.repo.MutateWorkspace(t.Context(), db.key, "p17.preferences", "", nil, func(data *domain.Bootstrap) error {
				preferences := defaultPreferences(db.users[1].ID)
				preferences.Email.Enabled, preferences.Desktop.Enabled = true, true
				data.NotificationPreferences[db.users[1].ID] = preferences
				data.PushSubscriptions = append(data.PushSubscriptions, domain.PushSubscription{ID: "p17-push", UserID: db.users[1].ID, Enabled: true})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			requestJSON[any](t, handler, "PUT", "/api/favorites/document/"+document.ID, nil, http.StatusOK)
			requestJSON[any](t, handler, "PUT", "/api/subscriptions/document/"+document.ID, nil, http.StatusOK)
			root := requestJSON[domain.Comment](t, handler, "POST", "/api/documents/"+document.ID+"/comments", map[string]any{"body": "P17 root", "anchorId": "p17-anchor"}, http.StatusCreated)
			requestJSON[domain.Comment](t, handler, "POST", "/api/documents/"+document.ID+"/comments", map[string]any{"body": "P17 reply", "parentId": root.ID}, http.StatusCreated)
			requestJSON[domain.ThreadSubscription](t, handler, "PUT", "/api/documents/"+document.ID+"/comments/"+root.ID+"/subscription", map[string]string{"state": "subscribed"}, http.StatusOK)
			for _, id := range []string{"p17-update-a", "p17-update-b"} {
				if _, err := db.repo.AppendDocumentCollaborationUpdate(t.Context(), db.key, store.DocumentCollaborationUpdate{ID: id, DocumentID: document.ID, Data: []byte(id), CreatedAt: time.Now().UTC()}); err != nil {
					t.Fatal(err)
				}
			}
			comparisons, differences := store.DocumentRecordVerificationCounts()
			saved := requestJSON[domain.Document](t, handler, "PATCH", "/api/documents/"+document.ID, map[string]any{"title": "P17 renamed", "content": body + "edited", "contentState": "AQID", "expectedContentVersion": 0, "documentUpdateIds": []string{"p17-update-a"}}, http.StatusOK)
			if saved.ContentState != "AQID" || saved.ContentVersion != 1 || len(saved.Revisions) != 2 || saved.Revisions[1].Content != body {
				t.Fatal("save lost its collaboration state or original history")
			}
			updates, err := db.repo.DocumentCollaborationUpdates(t.Context(), db.key, document.ID)
			if err != nil || len(updates) != 1 || updates[0].ID != "p17-update-b" {
				t.Fatalf("compaction: %v", err)
			}
			stale := requestJSON[domain.Document](t, handler, "PATCH", "/api/documents/"+document.ID, map[string]any{"contentState": "BAUG", "expectedContentVersion": 0, "documentUpdateIds": []string{"p17-update-b"}}, http.StatusOK)
			if stale.ContentState != saved.ContentState || stale.ContentVersion != saved.ContentVersion {
				t.Fatal("stale compaction replaced a newer base")
			}
			requestJSON[any](t, handler, "PATCH", "/api/documents/"+document.ID, map[string]any{"title": "conflict", "expectedVersion": -1}, http.StatusConflict)
			permissions := requestJSON[[]domain.DocumentPermission](t, handler, "PUT", "/api/documents/"+document.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "workspace", "role": "editor"}}}, http.StatusOK)
			if len(permissions) != 2 {
				t.Fatal("permission replacement lost the canonical owner")
			}
			before, _ := db.repo.WorkspaceMetadata(db.key)
			favoriteCount, subscriptionCount := len(before.Favorites), len(before.Subscriptions)
			requestJSON[any](t, handler, "DELETE", "/api/documents/"+document.ID, nil, http.StatusNoContent)
			deleted := requestJSON[domain.Document](t, handler, "GET", "/api/documents/"+document.SlugID, nil, http.StatusOK)
			if deleted.DeletedAt == nil || deleted.Content != saved.Content || len(deleted.Revisions) != 2 {
				t.Fatal("old link did not retain the deleted document and history")
			}
			data, _ := db.repo.WorkspaceMetadata(db.key)
			entry := slices.IndexFunc(data.Trash, func(entry domain.TrashEntry) bool { return entry.ResourceID == document.ID })
			if entry < 0 || data.Trash[entry].Links == nil {
				t.Fatal("deletion did not retain restore associations")
			}
			requestJSON[domain.Document](t, handler, "POST", "/api/trash/"+data.Trash[entry].ID+"/restore", nil, http.StatusOK)
			after, _ := db.repo.WorkspaceMetadata(db.key)
			if len(after.Favorites) != favoriteCount || len(after.Subscriptions) != subscriptionCount || after.Documents[0].ID != document.ID {
				t.Fatal("restore lost favorites, subscriptions or prepend order")
			}
			projectIndex := slices.IndexFunc(after.Projects, func(item domain.Project) bool { return item.ID == project.ID })
			if projectIndex < 0 || !slices.ContainsFunc(after.Projects[projectIndex].Resources, func(item domain.ProjectResource) bool { return item.ID == document.ID }) {
				t.Fatal("restore lost project resources")
			}
			comments := requestJSON[[]domain.Comment](t, handler, "GET", "/api/documents/"+document.ID+"/comments", nil, http.StatusOK)
			if len(comments) != 2 || len(after.ThreadSubscriptions) == 0 {
				t.Fatal("restore lost comment threads or subscriptions")
			}
			complete, ok := db.repo.BootstrapFor(db.key)
			if !ok || !slices.ContainsFunc(complete.Notifications, func(item domain.Notification) bool {
				return item.SourceID == document.ID && item.RecipientID == db.users[1].ID
			}) || !slices.ContainsFunc(complete.NotificationDeliveries, func(item domain.NotificationDelivery) bool {
				return item.Channel == "push" && item.RecipientID == db.users[1].ID
			}) {
				t.Fatal("document mutation lost notification or push delivery intents")
			}
			restored := requestJSON[domain.Document](t, handler, "POST", "/api/documents/"+document.ID+"/restore/"+saved.Revisions[1].ID, nil, http.StatusOK)
			if restored.Content != body || restored.ContentState != "" || restored.CollaborationID == "" {
				t.Fatal("history restore lost its body or collaboration generation")
			}
			updates, err = db.repo.DocumentCollaborationUpdates(t.Context(), db.key, document.ID)
			if err != nil || len(updates) != 0 {
				t.Fatal("history restore retained the replaced collaboration log")
			}
			list := requestJSON[[]domain.Document](t, handler, "GET", "/api/documents?q=P17&archived=all", nil, http.StatusOK)
			if !slices.ContainsFunc(list, func(item domain.Document) bool {
				return item.ID == document.ID && item.Content == body && len(item.Revisions) == 3
			}) {
				t.Fatal("legacy list omitted full content/history")
			}
			newComparisons, newDifferences := store.DocumentRecordVerificationCounts()
			if (mode == "on" || mode == "shadow") && (newComparisons <= comparisons || newDifferences != differences) {
				t.Fatal("document callbacks did not pass the differential oracle")
			}
			raw, _ := json.Marshal(restored)
			t.Logf("mode=%s oracle comparisons=%d response bytes=%d", mode, newComparisons-comparisons, len(raw))
		})
	}
}
