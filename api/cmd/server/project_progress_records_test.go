package main

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// progressGraphVisible mirrors the web gate (shouldShowProgressGraph in
// web/src/components/project-detail/project-progress-data.ts).
func progressGraphVisible(project domain.Project) bool {
	if project.StartDate == nil || *project.StartDate == "" || project.Status.Type == "planned" {
		return false
	}
	if len(project.ScopeHistory) == 0 || len(project.ScopeHistory) != len(project.CompletedScopeHistory) {
		return false
	}
	for _, point := range project.ScopeHistory {
		if point.Value > 0 {
			return true
		}
	}
	return false
}

func projectFromStore(t *testing.T, repository *store.SQLiteStore, id string) domain.Project {
	t.Helper()
	data, ok := repository.WorkspaceMetadata("test-workspace")
	if !ok {
		t.Fatal("workspace missing")
	}
	for _, project := range data.Projects {
		if project.ID == id {
			return project
		}
	}
	t.Fatalf("project %s missing", id)
	return domain.Project{}
}

// Issue records live outside the workspace snapshot, so the record-backed
// issue paths (/api/issue-records, used by paged clients and by rerouted
// legacy /api/issues writes) must keep the touched project's progress history
// current without loading the workspace.
func TestRecordIssueWritesMaintainProjectProgress(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	project := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "Graph", "teamIds": []string{"team_test"}, "statusId": "ps_progress"}, http.StatusCreated)
	if len(projectFromStore(t, repository, project.ID).ScopeHistory) != 0 {
		t.Fatal("a project without issues should not have a history yet")
	}
	first := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issue-records", map[string]any{"title": "Scope one", "teamId": "team_test", "projectId": project.ID}, http.StatusCreated)
	requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Scope two", "teamId": "team_test", "projectId": project.ID, "estimate": 3}, http.StatusCreated)
	stored := projectFromStore(t, repository, project.ID)
	if len(stored.ScopeHistory) == 0 || stored.ScopeHistory[len(stored.ScopeHistory)-1].ScopeCount != 2 {
		t.Fatalf("issue creation did not build the project history: %+v", stored.ScopeHistory)
	}

	// Setting a start date rebuilds the weekly grid from that date.
	start := time.Now().UTC().AddDate(0, 0, -15).Format("2006-01-02")
	requestJSON[domain.Project](t, handler, http.MethodPatch, "/api/projects/"+project.ID, map[string]any{"startDate": start}, http.StatusOK)
	stored = projectFromStore(t, repository, project.ID)
	if len(stored.ScopeHistory) != 4 || len(stored.CompletedScopeHistory) != 4 || stored.ScopeHistory[0].Date.Format("2006-01-02") != start {
		t.Fatalf("start date did not rebuild the weekly history: %+v", stored.ScopeHistory)
	}
	if !progressGraphVisible(stored) {
		t.Fatalf("progress graph gate rejects the rebuilt history: %+v", stored)
	}
	if last := stored.ScopeHistory[3]; last.ScopeCount != 2 || last.Value != 4 {
		t.Fatalf("today's scope = %+v, want 2 issues / 4 points", last)
	}

	// Completing an issue adjusts today's point on the record path.
	done := ""
	for _, state := range requestJSON[[]domain.WorkflowState](t, handler, http.MethodGet, "/api/teams/team_test/states", nil, http.StatusOK) {
		if state.Type == "completed" && done == "" {
			done = state.ID
		}
	}
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issue-records/"+first.ID, map[string]any{"stateId": done}, http.StatusOK)
	stored = projectFromStore(t, repository, project.ID)
	completed := stored.CompletedScopeHistory[len(stored.CompletedScopeHistory)-1]
	if completed.CompletedIssueCount != 1 || completed.Value != 1 || stored.CompletedScopeHistory[0].Value != 0 {
		t.Fatalf("completing an issue gave completed history %+v", stored.CompletedScopeHistory)
	}
	// Moving the other issue out of the project removes it from today's scope.
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+first.ID, map[string]any{"projectId": ""}, http.StatusOK)
	stored = projectFromStore(t, repository, project.ID)
	if last := stored.ScopeHistory[len(stored.ScopeHistory)-1]; last.ScopeCount != 1 || last.Value != 3 {
		t.Fatalf("scope after moving an issue out = %+v", last)
	}
	if len(stored.ScopeHistory) != len(stored.CompletedScopeHistory) || !progressGraphVisible(stored) {
		t.Fatalf("histories out of step: %d scope, %d completed", len(stored.ScopeHistory), len(stored.CompletedScopeHistory))
	}
}

// Workspaces upgraded from builds that never maintained record-backed
// progress get a history for every project with issues when they start.
func TestStartupBackfillsMissingProjectProgress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	repository, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	start := time.Now().UTC().AddDate(0, 0, -8).Format("2006-01-02")
	project := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "Legacy", "teamIds": []string{"team_test"}, "statusId": "ps_progress", "startDate": start}, http.StatusCreated)
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	// Attach an existing issue to the project behind the store's back, as an
	// older build would have left it: issue row updated, no history.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE issue_records SET project_id=?, data=json_set(data,'$.project',json(?)) WHERE workspace_key='test-workspace' AND id='issue_1'`, project.ID, `{"id":"`+project.ID+`","name":"Legacy","color":"#000000"}`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	repository, err = store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	stored := projectFromStore(t, repository, project.ID)
	if len(stored.ScopeHistory) != 3 || !progressGraphVisible(stored) {
		t.Fatalf("startup did not backfill the project history: %+v", stored.ScopeHistory)
	}
}
