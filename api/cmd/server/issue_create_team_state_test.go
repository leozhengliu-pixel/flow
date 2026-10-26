package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// A new issue's status must belong to the target team's effective workflow: its own
// statuses, the parent's when it inherits them, or the workspace statuses otherwise.
func TestCreateIssueRejectsStatusOutsideTeamWorkflow(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	before := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	workspaceTodo := before.States[slices.IndexFunc(before.States, func(state domain.WorkflowState) bool {
		return state.TeamID == "" && state.Type == "unstarted"
	})]

	// A custom workflow replaces the workspace statuses for that team.
	parent := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams", map[string]any{"name": "Custom flow", "key": "CFW"}, http.StatusCreated)
	ready := requestJSON[domain.WorkflowState](t, handler, http.MethodPost, "/api/teams/"+parent.ID+"/states", map[string]any{"name": "Ready", "type": "unstarted"}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Workspace todo", "teamId": parent.ID, "stateId": workspaceTodo.ID}, http.StatusBadRequest)
	created := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Team status", "teamId": parent.ID, "stateId": ready.ID}, http.StatusCreated)
	if created.State.ID != ready.ID || created.Team.ID != parent.ID {
		t.Fatalf("created issue = team %q state %q, want %q/%q", created.Team.ID, created.State.ID, parent.ID, ready.ID)
	}

	// An inheriting sub-team uses the parent's workflow; its own copies and other teams' statuses are rejected.
	child := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams", map[string]any{"name": "Custom child", "key": "CFC", "parentTeamId": parent.ID}, http.StatusCreated)
	data := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if !data.TeamSettings[child.ID].InheritWorkflowStatuses {
		t.Fatalf("child does not inherit workflow statuses: %#v", data.TeamSettings[child.ID])
	}
	copyIndex := slices.IndexFunc(data.States, func(state domain.WorkflowState) bool { return state.TeamID == child.ID })
	if copyIndex < 0 {
		t.Fatal("child team has no copied workflow states")
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Child copy", "teamId": child.ID, "stateId": data.States[copyIndex].ID}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Workspace todo", "teamId": child.ID, "stateId": workspaceTodo.ID}, http.StatusBadRequest)
	inherited := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Parent status", "teamId": child.ID, "stateId": ready.ID}, http.StatusCreated)
	if inherited.State.ID != ready.ID {
		t.Fatalf("inherited issue state = %q, want %q", inherited.State.ID, ready.ID)
	}

	// A team on the workspace workflow rejects a custom team's status.
	plainIndex := slices.IndexFunc(data.Teams, func(team domain.Team) bool {
		return !slices.ContainsFunc(data.States, func(state domain.WorkflowState) bool { return state.TeamID == team.ID }) && !data.TeamSettings[team.ID].InheritWorkflowStatuses
	})
	if plainIndex < 0 {
		t.Fatal("fixture needs a team on the workspace workflow")
	}
	plain := data.Teams[plainIndex]
	requestJSON[any](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Custom status", "teamId": plain.ID, "stateId": ready.ID}, http.StatusBadRequest)
	requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Workspace todo", "teamId": plain.ID, "stateId": workspaceTodo.ID}, http.StatusCreated)
}
