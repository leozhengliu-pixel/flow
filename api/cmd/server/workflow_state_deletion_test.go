package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestWorkflowDeletionRequiresValidReplacementAndPreservesStateRules(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "workflow-delete.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	teamID := repo.Bootstrap().Teams[0].ID
	base := "/api/teams/" + teamID + "/states"
	old := requestJSON[domain.WorkflowState](t, handler, http.MethodPost, base, map[string]any{"name": "Disposable", "type": "started"}, http.StatusCreated)
	states := requestJSON[[]domain.WorkflowState](t, handler, http.MethodGet, base, nil, http.StatusOK)
	next := states[slices.IndexFunc(states, func(state domain.WorkflowState) bool { return state.Type == "started" && state.ID != old.ID })]
	path := base + "/" + old.ID
	for _, replacement := range []string{old.ID, "missing", "other-team-state"} {
		response := requestJSON[map[string]string](t, handler, http.MethodDelete, path, map[string]any{"replacementStateId": replacement}, http.StatusBadRequest)
		if response["error"] != "replacementStateId must be another status in this team" {
			t.Fatalf("deletion hid actionable validation error: %v", response)
		}
	}
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issue-records", map[string]any{"title": "Used status deletion", "teamId": teamID, "stateId": old.ID}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodDelete, path, map[string]any{}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodDelete, path, map[string]any{"replacementStateId": next.ID}, http.StatusNoContent)
	updated := requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+issue.ID, nil, http.StatusOK)
	if updated.State.ID != next.ID || updated.Version != issue.Version+1 {
		t.Fatalf("replacement not persisted: state=%s version=%d", updated.State.ID, updated.Version)
	}
	requestJSON[any](t, handler, http.MethodDelete, path, map[string]any{"replacementStateId": next.ID}, http.StatusNotFound)
	// The last ordinary state of a type, default, and reserved state cannot be removed.
	for _, state := range states {
		if state.ID == old.ID {
			continue
		}
		requestJSON[any](t, handler, http.MethodDelete, base+"/"+state.ID, map[string]any{"replacementStateId": next.ID}, http.StatusBadRequest)
	}
	empty := requestJSON[domain.WorkflowState](t, handler, http.MethodPost, base, map[string]any{"name": "Unused", "type": "started"}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodDelete, base+"/"+empty.ID, nil, http.StatusNoContent)
}
