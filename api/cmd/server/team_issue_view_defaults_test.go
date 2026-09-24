package main

import (
	"net/http"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestTeamIssueViewDefaultsRoundTrip(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "view-defaults.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	teamID := repository.Bootstrap().Teams[0].ID
	path := "/api/teams/" + teamID + "/settings"
	saved := requestJSON[domain.TeamSettings](t, handler, http.MethodPatch, path, map[string]any{"issueViewDefaults": map[string]any{"board": map[string]any{"layout": "board", "grouping": "priority"}}}, http.StatusOK)
	if string(saved.IssueViewDefaults["board"]) == "" {
		t.Fatalf("default not stored: %#v", saved.IssueViewDefaults)
	}
	requestJSON[map[string]any](t, handler, http.MethodPatch, path, map[string]any{"issueViewDefaults": map[string]any{"unknown": map[string]any{}}}, http.StatusBadRequest)
	cleared := requestJSON[domain.TeamSettings](t, handler, http.MethodPatch, path, map[string]any{"issueViewDefaults": map[string]any{"board": nil}}, http.StatusOK)
	if _, ok := cleared.IssueViewDefaults["board"]; ok {
		t.Fatalf("default not cleared: %#v", cleared.IssueViewDefaults)
	}
}
