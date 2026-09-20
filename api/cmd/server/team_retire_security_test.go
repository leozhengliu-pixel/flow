package main

import (
	"net/http"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestIssueBatchMoveTeamAndSharingGate(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, authDisabled: true, uploadPath: t.TempDir()})

	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	team := bootstrap.Teams[0]
	other := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams", map[string]any{
		"name": "Move Target", "key": "MOV", "private": false,
	}, http.StatusCreated)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "Retire move coverage", "teamId": team.ID,
	}, http.StatusCreated)

	moved := requestJSON[[]domain.Issue](t, handler, http.MethodPost, "/api/issues/batch", map[string]any{
		"issueIds": []string{issue.ID},
		"update":   map[string]any{"teamId": other.ID},
	}, http.StatusOK)
	if len(moved) != 1 || moved[0].Team.ID != other.ID {
		t.Fatalf("expected issue moved to %s, got %#v", other.ID, moved)
	}

	requestJSON[any](t, handler, http.MethodPost, "/api/issues/"+moved[0].ID+"/share", map[string]any{}, http.StatusBadRequest)

	requestJSON[domain.TeamSettings](t, handler, http.MethodPatch, "/api/teams/"+other.ID+"/settings", map[string]any{
		"issueSharingEnabled": true,
	}, http.StatusOK)
	shared := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/issues/"+moved[0].ID+"/share", map[string]any{}, http.StatusOK)
	if shared["token"] == nil || shared["token"] == "" {
		t.Fatalf("expected share token, got %#v", shared)
	}
}
