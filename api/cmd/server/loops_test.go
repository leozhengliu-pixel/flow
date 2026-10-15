package main

import (
	"net/http"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestLoopLifecycle(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	created := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{
		"name": "Daily issue triage", "icon": "Automation", "color": "#d9b84b", "triggerType": "issue", "triggerConfig": map[string]any{"action": "created"}, "instructions": "Assign new issues to the right team.", "allowChangesOutsideTrigger": true, "status": "published",
	}, http.StatusCreated)
	if created.ID == "" || created.Name != "Daily issue triage" || created.Icon != "Automation" || created.Color != "#d9b84b" || created.TriggerType != "issue" || !created.Enabled {
		t.Fatalf("loop create failed: %#v", created)
	}
	if created.OwnerID == "" {
		t.Fatalf("loop create missing owner: %#v", created)
	}
	updated := requestJSON[domain.Loop](t, handler, http.MethodPatch, "/api/loops/"+created.ID, map[string]any{"enabled": false, "instructions": "Updated instructions.", "ownerId": created.OwnerID, "trustedSourceKeys": []string{"issueSource:email-trusted"}}, http.StatusOK)
	if updated.Enabled || updated.Instructions != "Updated instructions." || updated.OwnerID != created.OwnerID || len(updated.TrustedSourceKeys) != 1 {
		t.Fatalf("loop update failed: %#v", updated)
	}
	listed := requestJSON[[]domain.Loop](t, handler, http.MethodGet, "/api/loops", nil, http.StatusOK)
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("loop list failed: %#v", listed)
	}
	// Loops carry a `<name>-<12 hex>` slug; the loop page resolves it, a stale name prefix, or the id.
	slug := domain.SlugWithSuffix("Daily issue triage", domain.DerivedSlugSuffix(created.ID))
	if created.SlugID != slug || listed[0].SlugID != slug {
		t.Fatalf("loop slug = %q / %q, want %q", created.SlugID, listed[0].SlugID, slug)
	}
	for _, ref := range []string{slug, "renamed-" + domain.SlugSuffix(slug), created.ID} {
		if got := requestJSON[domain.Loop](t, handler, http.MethodGet, "/api/loops/"+ref, nil, http.StatusOK); got.ID != created.ID {
			t.Fatalf("GET /api/loops/%s = %q", ref, got.ID)
		}
	}
	requestJSON[map[string]any](t, handler, http.MethodGet, "/api/loops/daily-issue-triage-000000000000", nil, http.StatusNotFound)
	requestJSON[[]domain.LoopRun](t, handler, http.MethodGet, "/api/loops/"+slug+"/runs", nil, http.StatusOK)
	requestJSON[map[string]any](t, handler, http.MethodDelete, "/api/loops/"+created.ID, nil, http.StatusNoContent)
	listed = requestJSON[[]domain.Loop](t, handler, http.MethodGet, "/api/loops", nil, http.StatusOK)
	if len(listed) != 0 {
		t.Fatalf("loop delete failed: %#v", listed)
	}
}
