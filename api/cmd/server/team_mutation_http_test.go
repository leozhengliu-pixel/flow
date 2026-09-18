package main

import (
	"net/http"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestCreateTeamWithParentCopiesSettingsAndWorkflowStates(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, authDisabled: true, uploadPath: t.TempDir()})

	parent := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams", map[string]any{"name": "Parent", "key": "PCR"}, http.StatusCreated)
	requestJSON[domain.TeamSettings](t, handler, http.MethodPatch, "/api/teams/"+parent.ID+"/settings", map[string]any{"estimateType": "fibonacci"}, http.StatusOK)
	requestJSON[domain.WorkflowState](t, handler, http.MethodPost, "/api/teams/"+parent.ID+"/states", map[string]any{"name": "Copied status", "type": "started"}, http.StatusCreated)
	parentStates := requestJSON[[]domain.WorkflowState](t, handler, http.MethodGet, "/api/teams/"+parent.ID+"/states", nil, http.StatusOK)
	if len(parentStates) == 0 {
		t.Fatal("parent team has no workflow states to copy")
	}

	child := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams", map[string]any{"name": "Child", "key": "CCL", "parentTeamId": parent.ID}, http.StatusCreated)

	settings := requestJSON[domain.TeamSettings](t, handler, http.MethodGet, "/api/teams/"+child.ID+"/settings", nil, http.StatusOK)
	data := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if settings.ParentTeamID != parent.ID || data.TeamSettings[child.ID].ParentTeamID != parent.ID {
		t.Fatalf("child parentTeamId was not persisted: settings=%#v bootstrap=%#v", settings, data.TeamSettings[child.ID])
	}
	if !settings.InheritIssueEstimation || !settings.InheritWorkflowStatuses || !settings.InheritProjectStatuses || !settings.InheritCycles {
		t.Fatalf("child did not inherit parent flags: %#v", settings)
	}
	if settings.EstimateType != "fibonacci" {
		t.Fatalf("child did not inherit parent estimate: %#v", settings)
	}

	copiedNames := map[string]bool{}
	copied := 0
	for _, state := range data.States {
		if state.TeamID != child.ID {
			continue
		}
		copied++
		copiedNames[state.Name] = true
	}
	if copied == 0 {
		t.Fatal("child team has no copied workflow states")
	}
	for _, state := range parentStates {
		if !copiedNames[state.Name] {
			t.Fatalf("child is missing copied workflow state %q", state.Name)
		}
	}
	if !copiedNames["Copied status"] {
		t.Fatal("child is missing the parent-specific workflow state")
	}
}

func TestPatchTeamSettingsParentOnlyInheritsConfiguration(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, authDisabled: true, uploadPath: t.TempDir()})

	parent := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams", map[string]any{"name": "Parent", "key": "PCH"}, http.StatusCreated)
	child := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams", map[string]any{"name": "Child", "key": "CPH"}, http.StatusCreated)
	requestJSON[domain.TeamSettings](t, handler, http.MethodPatch, "/api/teams/"+parent.ID+"/settings", map[string]any{"estimateType": "fibonacci"}, http.StatusOK)

	requestJSON[domain.TeamSettings](t, handler, http.MethodPatch, "/api/teams/"+child.ID+"/settings", map[string]any{"parentTeamId": parent.ID}, http.StatusOK)
	settings := requestJSON[domain.TeamSettings](t, handler, http.MethodGet, "/api/teams/"+child.ID+"/settings", nil, http.StatusOK)
	if settings.ParentTeamID != parent.ID || !settings.InheritIssueEstimation || !settings.InheritWorkflowStatuses || !settings.InheritProjectStatuses || !settings.InheritCycles || settings.EstimateType != "fibonacci" {
		t.Fatalf("parent-only settings patch did not inherit configuration: %#v", settings)
	}
	data := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if persisted := data.TeamSettings[child.ID]; persisted.ParentTeamID != parent.ID || !persisted.InheritIssueEstimation || persisted.EstimateType != "fibonacci" {
		t.Fatalf("inherited settings were not persisted: %#v", persisted)
	}
}

func TestUpdateTeamNameWithoutSubTeamActionSucceeds(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, authDisabled: true, uploadPath: t.TempDir()})

	team := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams", map[string]any{"name": "Original", "key": "RNM"}, http.StatusCreated)
	updated := requestJSON[domain.Team](t, handler, http.MethodPatch, "/api/workspaces/test-workspace/teams/"+team.ID, map[string]any{"name": "Renamed"}, http.StatusOK)
	if updated.Name != "Renamed" {
		t.Fatalf("rename response = %#v", updated)
	}
	data := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	found := false
	for _, item := range data.Teams {
		if item.ID == team.ID {
			found = true
			if item.Name != "Renamed" {
				t.Fatalf("renamed team was not persisted: %#v", item)
			}
		}
	}
	if !found {
		t.Fatal("renamed team missing from bootstrap")
	}
}
