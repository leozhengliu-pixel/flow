package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// TestScaleDocumentUpdateWorkBudget: with bounded metadata on, an allowed
// PATCH /api/documents/{id} that finds the process work budget held by
// concurrent work is not answered 500. When the work ends while the edit
// waits, the edit is made (200); when the budget stays held past the wait,
// the answer is the retryable 503 + Retry-After and nothing is written.
func TestScaleDocumentUpdateWorkBudget(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_DOCUMENT_RECORDS", "off")
	for _, repo := range scaleServerTestStores(t) {
		f := setupPolicyDifferential(t, repo)
		workspace, id := f.data.Workspace.URLKey, idgen.New("work-budget-document-")
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.work_budget_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Documents = append(data.Documents, domain.Document{ID: id, Title: "Before", Content: strings.Repeat("body ", 2000), Creator: data.Viewer, Version: 1, TeamIDs: []string{f.public}})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		service := &server{store: f.probe, realtime: newRealtimeHub(), uploadPath: t.TempDir()}
		edit := func(title string) *httptest.ResponseRecorder {
			t.Helper()
			request := f.literalRequest(f.data.Viewer, nil, map[string]any{"title": title})
			request.Method, request.URL.Path = http.MethodPatch, "/api/documents/"+id
			request.SetPathValue("id", id)
			ctx, closeOutput := beginMetadataOutputReads(request.Context())
			defer closeOutput()
			response := httptest.NewRecorder()
			service.updateDocument(response, request.WithContext(ctx))
			return response
		}
		// Concurrent work holds all but 1 MiB of the budget: the writer's
		// first admission under its lock is refused for want of room.
		hold := func() func() {
			t.Helper()
			room := f.probe.MetadataWorkLimitForTesting() - f.probe.MetadataWorkUsedForTesting()
			release, err := f.probe.HoldMetadataWorkForTesting(room - 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			return release
		}
		events := func() int {
			t.Helper()
			items, err := repo.Events(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			return len(items)
		}

		release := hold()
		timer := time.AfterFunc(300*time.Millisecond, release)
		response := edit("After the work ended")
		timer.Stop()
		release()
		if response.Code != http.StatusOK {
			t.Fatalf("an allowed edit while concurrent work held the budget = %d %s, want 200", response.Code, response.Body.String())
		}
		if document := mustDocument(t, repo, workspace, id); document.Title != "After the work ended" || document.Version != 2 {
			t.Fatalf("the waiting edit was not written: %q version %d", document.Title, document.Version)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}

		before := events()
		restoreWait := store.SetMetadataWorkRetryWaitForTesting(200 * time.Millisecond)
		release = hold()
		response = edit("Never written")
		release()
		restoreWait()
		if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") == "" {
			t.Fatalf("an edit the held budget never admitted = %d %q %s, want 503 with Retry-After", response.Code, response.Header().Get("Retry-After"), response.Body.String())
		}
		if document := mustDocument(t, repo, workspace, id); document.Title != "After the work ended" || document.Version != 2 || events() != before {
			t.Fatalf("a refused edit wrote: %q version %d", document.Title, document.Version)
		}
		// The webhook preflight runs before the document writer's retry loop.
		f.probe.SetWebhookSink(func(string, domain.DomainEvent) {})
		defer f.probe.SetWebhookSink(nil)
		release = holdLoadErrorsWork(t, f.probe, 32<<10)
		response = edit("After webhook preflight")
		release()
		if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") == "" || events() != before {
			t.Fatalf("held webhook preflight returned %d, want 503 with Retry-After and no event", response.Code)
		}
		if response := edit("After webhook preflight"); response.Code != http.StatusOK || events() != before+1 {
			t.Fatalf("released webhook preflight returned %d, want 200 and one event", response.Code)
		}
		if used := f.probe.MetadataWorkUsedForTesting(); used != 0 {
			t.Fatalf("the edits left %d bytes of work admitted", used)
		}
	}
}
