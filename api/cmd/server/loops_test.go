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
		"name": "Daily issue triage", "icon": "Automation", "color": "#d9b84b", "triggerType": "issue", "triggerConfig": map[string]any{"action": "created"}, "instructions": "Assign new issues to the right team.", "allowChangesOutsideTrigger": true,
	}, http.StatusCreated)
	if created.ID == "" || created.Name != "Daily issue triage" || created.Icon != "Automation" || created.Color != "#d9b84b" || created.TriggerType != "issue" || !created.Enabled {
		t.Fatalf("loop create failed: %#v", created)
	}
	updated := requestJSON[domain.Loop](t, handler, http.MethodPatch, "/api/loops/"+created.ID, map[string]any{"enabled": false, "instructions": "Updated instructions."}, http.StatusOK)
	if updated.Enabled || updated.Instructions != "Updated instructions." {
		t.Fatalf("loop update failed: %#v", updated)
	}
	listed := requestJSON[[]domain.Loop](t, handler, http.MethodGet, "/api/loops", nil, http.StatusOK)
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("loop list failed: %#v", listed)
	}
	requestJSON[map[string]any](t, handler, http.MethodDelete, "/api/loops/"+created.ID, nil, http.StatusNoContent)
	listed = requestJSON[[]domain.Loop](t, handler, http.MethodGet, "/api/loops", nil, http.StatusOK)
	if len(listed) != 0 {
		t.Fatalf("loop delete failed: %#v", listed)
	}
}
