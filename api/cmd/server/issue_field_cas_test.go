package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestIssueFieldCASOnTwoServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peers.db")
	var handlers []http.Handler
	for range 2 {
		repo, err := store.OpenSQLiteTestFixture(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { repo.Close() })
		handlers = append(handlers, newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true}))
	}
	first, second := handlers[0], handlers[1]
	data := requestJSON[domain.Bootstrap](t, first, "GET", "/api/bootstrap", nil, 200)
	team := data.Teams[0].ID
	state := func(kind string) string {
		at := slices.IndexFunc(data.States, func(state domain.WorkflowState) bool { return state.Type == kind && !state.Reserved })
		if at < 0 {
			t.Fatalf("missing %s state", kind)
		}
		return data.States[at].ID
	}
	requestJSON[domain.TeamSettings](t, first, "PATCH", "/api/teams/"+team+"/settings", map[string]any{"autoCloseParents": true}, 200)
	parent := requestJSON[domain.Issue](t, first, "POST", "/api/issues", map[string]any{"title": "Parent", "teamId": team, "stateId": state("unstarted")}, 201)
	child := requestJSON[domain.Issue](t, first, "POST", "/api/issues", map[string]any{"title": "Child", "teamId": team, "stateId": state("unstarted"), "parentId": parent.ID}, 201)
	parent = requestJSON[domain.Issue](t, second, "GET", "/api/issue-records/"+parent.ID, nil, 200)
	requestJSON[domain.Issue](t, first, "PATCH", "/api/issues/"+child.ID, map[string]any{"stateId": state("completed"), "expectedVersion": child.Version}, 200)
	updated := requestJSON[domain.Issue](t, second, "PATCH", "/api/issues/"+parent.ID, map[string]any{"priority": 2, "expectedVersion": parent.Version}, 200)
	if updated.Version != parent.Version+2 || updated.State.Type != "completed" || updated.Priority != 2 {
		t.Fatalf("disjoint parent patch: %+v", updated)
	}
	requestJSON[any](t, second, "PATCH", "/api/issues/"+parent.ID, map[string]any{"stateId": state("unstarted"), "expectedVersion": parent.Version}, 409)
	requestJSON[any](t, first, "PATCH", "/api/issue-records/"+parent.ID, map[string]any{"priority": 3, "expectedVersion": parent.Version}, 409)
	// Changing a field back still conflicts with a tab that predates both edits.
	requestJSON[domain.Issue](t, first, "PATCH", "/api/issues/"+parent.ID, map[string]any{"priority": parent.Priority, "expectedVersion": updated.Version}, 200)
	requestJSON[any](t, second, "PATCH", "/api/issues/"+parent.ID, map[string]any{"priority": 4, "expectedVersion": parent.Version}, 409)
	requestJSON[any](t, second, "PATCH", "/api/issues/"+parent.ID, map[string]any{"title": "Future", "expectedVersion": updated.Version + 100}, 409)
}
