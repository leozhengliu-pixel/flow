package main

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestDeletedTeamIsRestorableThenPurged(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	srv := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	handler := newHandler(srv)
	team := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams", map[string]any{"name": "Growth", "key": "GRO"}, http.StatusCreated)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Owned by growth", "teamId": team.ID}, http.StatusCreated)

	requestJSON[any](t, handler, http.MethodDelete, "/api/workspaces/test-workspace/teams/"+team.ID, nil, http.StatusNoContent)
	hidden := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if slices.ContainsFunc(hidden.Teams, func(item domain.Team) bool { return item.ID == team.ID }) || slices.ContainsFunc(hidden.Issues, func(item domain.Issue) bool { return item.ID == issue.ID }) {
		t.Fatalf("deleted team or its issue is still visible: teams=%v issues=%d", slices.ContainsFunc(hidden.Teams, func(item domain.Team) bool { return item.ID == team.ID }), len(hidden.Issues))
	}
	page := requestJSON[store.IssueRecordPage](t, handler, http.MethodGet, "/api/issue-records?teamId="+team.ID, nil, http.StatusOK)
	if len(page.Items) != 0 {
		t.Fatalf("issue query returned %d issues from a deleted team", len(page.Items))
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Blocked", "teamId": team.ID}, http.StatusBadRequest)

	deleted := requestJSON[[]deletedTeam](t, handler, http.MethodGet, "/api/workspaces/test-workspace/deleted-teams", nil, http.StatusOK)
	if len(deleted) != 1 || deleted[0].ID != team.ID || deleted[0].IssueCount != 1 || deleted[0].PurgeAt.Sub(*deleted[0].ArchivedAt) != teamRestoreWindow {
		t.Fatalf("deleted teams = %#v", deleted)
	}

	requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/deleted-teams/"+team.ID+"/restore", nil, http.StatusOK)
	restored := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if !slices.ContainsFunc(restored.Teams, func(item domain.Team) bool { return item.ID == team.ID && item.ArchivedAt == nil }) {
		t.Fatal("restored team is not visible")
	}
	if page := requestJSON[store.IssueRecordPage](t, handler, http.MethodGet, "/api/issue-records?teamId="+team.ID, nil, http.StatusOK); len(page.Items) != 1 {
		t.Fatalf("restored team has %d issues, want 1", len(page.Items))
	}

	requestJSON[any](t, handler, http.MethodDelete, "/api/workspaces/test-workspace/teams/"+team.ID, nil, http.StatusNoContent)
	if err := srv.purgeExpiredTeams(context.Background(), "test-workspace", time.Now().Add(teamRestoreWindow-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if deleted := requestJSON[[]deletedTeam](t, handler, http.MethodGet, "/api/workspaces/test-workspace/deleted-teams", nil, http.StatusOK); len(deleted) != 1 {
		t.Fatal("team was purged before its restoration window ended")
	}
	if err := srv.purgeExpiredTeams(context.Background(), "test-workspace", time.Now().Add(teamRestoreWindow+time.Hour)); err != nil {
		t.Fatal(err)
	}
	if deleted := requestJSON[[]deletedTeam](t, handler, http.MethodGet, "/api/workspaces/test-workspace/deleted-teams", nil, http.StatusOK); len(deleted) != 0 {
		t.Fatal("expired team was not purged")
	}
	data, _ := repository.WorkspaceMetadata("test-workspace")
	if slices.ContainsFunc(data.Teams, func(item domain.Team) bool { return item.ID == team.ID }) {
		t.Fatal("purged team is still stored")
	}
}

func TestLastActiveTeamCannotBeDeleted(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	for _, team := range bootstrap.Teams[1:] {
		requestJSON[any](t, handler, http.MethodDelete, "/api/workspaces/test-workspace/teams/"+team.ID, nil, http.StatusNoContent)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/workspaces/test-workspace/teams/"+bootstrap.Teams[0].ID, nil, http.StatusConflict)
}
