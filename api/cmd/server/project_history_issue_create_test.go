package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// A project whose start date lies decades back keeps a weekly progress
// history of more than a thousand points per series. A second issue created
// in such a project failed with 500: the create read the whole project,
// histories included, beside the scoped progress rebuild's own copy, and the
// rebase of the stored project row admitted its encoding work as if every
// empty omitempty field of every history point were written (about 400 times
// the record's encoded size). Together they exceeded the whole work budget.
func TestIssueCreateInProjectWithDecadesOfProgressHistory(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "on")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_SYNC_V2", "on")
	repo, err := store.OpenDatabase(platformDatabaseFixtureScaleDatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if !repo.BoundedMetadataEnabled() {
		t.Fatal("bounded metadata is off")
	}
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	const workspace = "decades"
	call := func(method, path string, input any, want int, output any) {
		t.Helper()
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Workspace-Key", workspace)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: status %d, want %d: %.300s", method, path, w.Code, want, w.Body.String())
		}
		if output != nil {
			if err := json.Unmarshal(w.Body.Bytes(), output); err != nil {
				t.Fatal(err)
			}
		}
	}
	call(http.MethodPost, "/api/workspaces", map[string]any{"name": "Decades", "urlKey": workspace, "region": "us"}, http.StatusCreated, nil)
	var data domain.Bootstrap
	r := httptest.NewRequest(http.MethodGet, "/api/bootstrap?projection=project-list", nil)
	r.Header.Set("X-Workspace-Key", workspace)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("bootstrap: %d %.300s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || len(data.Teams) == 0 {
		t.Fatalf("bootstrap teams: %v", err)
	}
	team := data.Teams[0].ID
	day := func(offset int) string { return time.Now().UTC().AddDate(0, 0, offset).Format("2006-01-02") }
	var projects []domain.Project
	for index, offset := range []int{-20 * 365, -10} {
		milestones := []map[string]any{}
		for m := range 12 {
			milestones = append(milestones, map[string]any{"name": fmt.Sprintf("Milestone %d.%d", index, m), "targetDate": day(offset + 9 + m*6)})
		}
		dependencies := []string{}
		for _, project := range projects {
			dependencies = append(dependencies, project.ID)
		}
		var project domain.Project
		call(http.MethodPost, "/api/projects", map[string]any{
			"name": fmt.Sprintf("Timeline project %d", index), "teamIds": []string{team},
			"startDate": day(offset), "targetDate": day(offset + 90), "milestoneDetails": milestones, "dependencyIds": dependencies,
		}, http.StatusCreated, &project)
		if len(project.Milestones) != 12 {
			t.Fatalf("project %d has %d milestones", index, len(project.Milestones))
		}
		projects = append(projects, project)
	}
	// The first issue in a project creates its history; each later one
	// rebases the project row that now holds it.
	var longProjectIssue domain.Issue
	for index := range 6 {
		project := projects[index%len(projects)]
		var issue domain.Issue
		call(http.MethodPost, "/api/issues", map[string]any{
			"teamId": team, "title": fmt.Sprintf("Timeline issue %d", index),
			"projectId": project.ID, "projectMilestoneId": project.Milestones[index%12].ID,
		}, http.StatusCreated, &issue)
		if issue.Project == nil || issue.Project.ID != project.ID {
			t.Fatalf("issue %d is not in its project", index)
		}
		if index == 0 {
			longProjectIssue = issue
		}
	}
	var stored domain.Project
	call(http.MethodGet, "/api/projects/"+projects[0].ID, nil, http.StatusOK, &stored)
	if len(stored.ProgressHistory) < 1000 {
		t.Fatalf("the long project keeps %d progress history points", len(stored.ProgressHistory))
	}

	// The former selection needed over 100 MiB to read the history even for
	// a title-only edit. All five persisted histories must survive the edit.
	issueURL := "/api/issues/" + longProjectIssue.ID
	var edited domain.Issue
	func() {
		defer repo.SetMetadataWorkLimitForTesting(16 << 20)()
		call(http.MethodPatch, issueURL, map[string]any{"title": "Renamed issue"}, http.StatusOK, &edited)
	}()
	if edited.Title != "Renamed issue" || edited.Project == nil || edited.Project.ID != projects[0].ID {
		t.Fatal("edit changed the issue's project or did not save the title")
	}
	var unchanged domain.Project
	call(http.MethodGet, "/api/projects/"+projects[0].ID, nil, http.StatusOK, &unchanged)
	for _, pair := range [][2][]domain.ProjectProgressHistoryPoint{
		{stored.IssueCountHistory, unchanged.IssueCountHistory}, {stored.ScopeHistory, unchanged.ScopeHistory},
		{stored.CompletedScopeHistory, unchanged.CompletedScopeHistory}, {stored.InProgressScopeHistory, unchanged.InProgressScopeHistory},
		{stored.ProgressHistory, unchanged.ProgressHistory},
	} {
		if !slices.Equal(pair[0], pair[1]) {
			t.Fatal("title-only edit changed progress history")
		}
	}
	call(http.MethodPatch, issueURL, map[string]any{"projectId": ""}, http.StatusOK, &edited)
	var moved domain.Project
	call(http.MethodGet, "/api/projects/"+projects[0].ID, nil, http.StatusOK, &moved)
	last := len(stored.ScopeHistory) - 1
	if len(moved.ScopeHistory) != len(stored.ScopeHistory) ||
		!slices.Equal(moved.ScopeHistory[:last], stored.ScopeHistory[:last]) ||
		moved.ScopeHistory[last].ScopeCount != stored.ScopeHistory[last].ScopeCount-1 {
		t.Fatal("removing an issue did not update only the last scope point")
	}

	// A create the whole budget cannot admit is refused with 503 and no
	// Retry-After, never 500, and leaves the workspace writable.
	input := map[string]any{"teamId": team, "title": "Refused issue", "projectId": projects[0].ID, "projectMilestoneId": projects[0].Milestones[1].ID}
	restore := repo.SetMetadataWorkLimitForTesting(16 << 20)
	raw, _ := json.Marshal(input)
	r = httptest.NewRequest(http.MethodPost, "/api/issues", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Workspace-Key", workspace)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	restore()
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "" {
		t.Fatalf("refused create: %d Retry-After %q: %.300s", w.Code, w.Header().Get("Retry-After"), w.Body.String())
	}
	call(http.MethodPost, "/api/issues", input, http.StatusCreated, nil)
}

// A write the metadata work budget refuses outright is not a server fault:
// it answers 503 without Retry-After (repeating it unchanged is refused the
// same way), while a contended refusal stays the retryable 503.
func TestMutationMetadataWorkRefusalIsUnavailable(t *testing.T) {
	for _, err := range []error{store.ErrMetadataWorkBudget, fmt.Errorf("rebase project: %w", store.ErrMetadataWorkBudget)} {
		w := httptest.NewRecorder()
		respondMutation(w, err, http.StatusCreated, nil)
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "" {
			t.Fatalf("budget refusal: %d Retry-After %q", w.Code, w.Header().Get("Retry-After"))
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["error"] != metadataWorkRefusedMessage {
			t.Fatalf("budget refusal body: %s", w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	respondMutation(w, store.ErrDatabaseWriteBusy, http.StatusCreated, nil)
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "2" {
		t.Fatalf("contended refusal: %d Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}
}
