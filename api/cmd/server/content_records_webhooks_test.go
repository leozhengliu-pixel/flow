package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestContentRecordWebhookListDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	db := newScaleP04Database(t)
	data, ok := db.repo.BootstrapFor(db.key)
	if !ok {
		t.Fatal("workspace fixture missing")
	}
	const hookID = "content-order-hook"
	if err := db.repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "webhooks"), db.key, "webhook.updated", hookID, nil, func(data *domain.Bootstrap) error {
		data.Webhooks = append(data.Webhooks, domain.Webhook{ID: hookID, Name: "Grouped content", URL: "https://example.test/hook", CreatorID: data.Viewer.ID})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	bounded, err := db.repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), db.key)
	if err != nil {
		t.Fatal(err)
	}
	read := func(source *store.SQLiteStore) string {
		t.Helper()
		ctx := context.WithValue(t.Context(), authUserContextKey{}, data.Viewer)
		r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/webhooks", nil)
		r.Header.Set("X-Workspace-Key", db.key)
		w := httptest.NewRecorder()
		scaleP21M3Call(&server{store: source}, w, r, (*server).listWebhooks)
		if w.Code != http.StatusOK {
			t.Fatalf("bounded=%t: status=%d body=%s", source.BoundedMetadataEnabled(), w.Code, w.Body.String())
		}
		return strings.TrimSpace(w.Body.String())
	}
	if off, on := read(db.repo), read(bounded); off != on || !strings.Contains(off, hookID) {
		t.Fatalf("webhooks before malformed row: off=%s on=%s", off, on)
	}
	scaleP21SW197Exec(t, db, `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data) VALUES(?,'comment','unrelated','malformed','2026-01-01',?)`, db.key, []byte(`{`))
	defer scaleP21SW197Exec(t, db, `DELETE FROM workspace_content_records WHERE workspace_key=? AND kind='comment' AND resource_id='unrelated' AND id='malformed'`, db.key)
	if off, on := read(db.repo), read(bounded); off != "[]" || on != off {
		t.Fatalf("webhooks after malformed row: off=%s on=%s", off, on)
	}
}
