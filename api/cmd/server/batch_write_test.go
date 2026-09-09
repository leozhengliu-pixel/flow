package main

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestBatchUpdatesDeduplicateIDsAndSkipIdenticalRetries(t *testing.T) {
	for _, path := range []string{"/api/issues/batch", "/api/issue-records/batch"} {
		t.Run(path, func(t *testing.T) {
			repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			s := &server{store: repo, authDisabled: true, uploadPath: t.TempDir()}
			handler := newHandler(s)
			defer s.stopDeliveryScheduler(context.Background())
			issue := requestJSON[domain.Issue](t, handler, "POST", "/api/issue-records", map[string]string{"title": "Before"}, 201)
			events := 0
			repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
				if event.Type == "issue.batch_updated" {
					events++
				}
			})
			input := map[string]any{"issueIds": []string{issue.ID, issue.ID}, "update": map[string]string{"title": "After"}}
			result := requestJSON[[]domain.Issue](t, handler, "POST", path, input, 200)
			if len(result) != 1 || result[0].Version != issue.Version+1 || events != 1 {
				t.Fatalf("duplicate IDs caused multiple writes: result=%+v events=%d", result, events)
			}
			retried := requestJSON[[]domain.Issue](t, handler, "POST", path, input, 200)
			if len(retried) != 1 || retried[0].Version != result[0].Version || !retried[0].UpdatedAt.Equal(result[0].UpdatedAt) || events != 1 {
				t.Fatal("identical retry generated another version or event")
			}
			ids := make([]string, 1001)
			for i := range ids {
				ids[i] = issue.ID
			}
			requestJSON[any](t, handler, http.MethodPost, path, map[string]any{"issueIds": ids, "update": map[string]string{"title": "Too many"}}, 400)
			saved := requestJSON[domain.Issue](t, handler, "GET", "/api/issue-records/"+issue.ID, nil, 200)
			if saved.Title != "After" {
				t.Fatal("oversized batch partially committed")
			}
		})
	}
}
