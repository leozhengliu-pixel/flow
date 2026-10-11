package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Workspace templates (teamId is only their default team property) and team templates are listed and edited through
// their own endpoints's workspace and team template settings.
func TestIssueTemplateWorkspaceAndTeamOwnership(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	team := bootstrap.Teams[0]
	ids := func(templates []domain.IssueTemplate) []string {
		result := []string{}
		for _, template := range templates {
			result = append(result, template.ID)
		}
		return result
	}

	workspaceTemplate := requestJSON[domain.IssueTemplate](t, handler, http.MethodPost, "/api/issue-templates", map[string]any{"name": "Workspace bug", "teamId": team.ID}, http.StatusCreated)
	teamTemplate := requestJSON[domain.IssueTemplate](t, handler, http.MethodPost, "/api/teams/"+team.ID+"/templates", map[string]any{"name": "Team bug"}, http.StatusCreated)

	workspaceList := ids(requestJSON[[]domain.IssueTemplate](t, handler, http.MethodGet, "/api/issue-templates", nil, http.StatusOK))
	if !slices.Contains(workspaceList, workspaceTemplate.ID) || slices.Contains(workspaceList, teamTemplate.ID) {
		t.Fatalf("workspace list should hold only workspace templates: %v", workspaceList)
	}
	teamList := ids(requestJSON[[]domain.IssueTemplate](t, handler, http.MethodGet, "/api/teams/"+team.ID+"/templates", nil, http.StatusOK))
	if !slices.Contains(teamList, teamTemplate.ID) || slices.Contains(teamList, workspaceTemplate.ID) {
		t.Fatalf("team list should hold only the team's templates: %v", teamList)
	}

	// A workspace template with this team as its default team is not editable as one of the team's templates.
	requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/teams/"+team.ID+"/templates/"+workspaceTemplate.ID, map[string]any{"name": "Hijacked"}, http.StatusNotFound)
	renamed := requestJSON[domain.IssueTemplate](t, handler, http.MethodPatch, "/api/teams/"+team.ID+"/templates/"+teamTemplate.ID, map[string]any{"name": "Team bug v2"}, http.StatusOK)
	if renamed.Name != "Team bug v2" {
		t.Fatalf("team template was not renamed: %#v", renamed)
	}

	// Narrowing a workspace template's visibility to the team moves it to the team's list and endpoints.
	narrowed := requestJSON[domain.IssueTemplate](t, handler, http.MethodPost, "/api/issue-templates", map[string]any{"name": "Narrowed", "visibilityTeamId": team.ID}, http.StatusCreated)
	teamList = ids(requestJSON[[]domain.IssueTemplate](t, handler, http.MethodGet, "/api/teams/"+team.ID+"/templates", nil, http.StatusOK))
	if !slices.Contains(teamList, narrowed.ID) {
		t.Fatalf("narrowed workspace template should be listed for its team: %v", teamList)
	}
	requestJSON[domain.IssueTemplate](t, handler, http.MethodPatch, "/api/teams/"+team.ID+"/templates/"+narrowed.ID, map[string]any{"name": "Narrowed v2"}, http.StatusOK)
	requestJSON[any](t, handler, http.MethodDelete, "/api/teams/"+team.ID+"/templates/"+narrowed.ID, nil, http.StatusNoContent)
}
