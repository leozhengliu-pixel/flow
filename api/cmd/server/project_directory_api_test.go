package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestProjectDirectoryAPIAndBootstrapProjection(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	page := requestJSON[store.ProjectRecordPage](t, handler, http.MethodGet, "/api/projects?limit=1&includeTotal=true", nil, http.StatusOK)
	if page.Total < 1 || len(page.Items) != 1 || page.Items[0].Description != "" {
		t.Fatalf("project directory=%#v", page)
	}
	requestJSON[map[string]any](t, handler, http.MethodGet, "/api/projects?filter=%5B%7B%22field%22%3A%22unknown%22%2C%22operator%22%3A%22is%22%2C%22values%22%3A%5B%22x%22%5D%7D%5D", nil, http.StatusBadRequest)

	request := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	request.Header.Set("X-Flow-Projection", "project-list")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("bootstrap status=%d body=%s", response.Code, response.Body.String())
	}
	var data domain.Bootstrap
	if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if !data.IssueCollectionPaged || len(data.Issues) != 0 || len(data.Projects) != 0 {
		t.Fatalf("project bootstrap leaked details: issues=%d projects=%#v", len(data.Issues), data.Projects)
	}
}

func TestPlanningBootstrapScopesIssueDetails(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	path := "/api/bootstrap"
	initial := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, path, nil, http.StatusOK)
	team := initial.Teams[0].ID
	project := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{
		"name": "Planning", "teamIds": []string{team}, "description": "Project description",
	}, http.StatusCreated)
	initiative := requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/initiatives", map[string]any{
		"name": "Roadmap", "projectIds": []string{project.ID},
	}, http.StatusCreated)
	requestJSON[domain.ProjectUpdate](t, handler, http.MethodPost, "/api/projects/"+project.ID+"/updates", map[string]any{
		"body": "Project update", "health": "onTrack",
	}, http.StatusCreated)
	requestJSON[domain.InitiativeUpdate](t, handler, http.MethodPost, "/api/initiatives/"+initiative.ID+"/updates", map[string]any{
		"body": "Initiative update", "health": "onTrack",
	}, http.StatusCreated)
	makeIssue := func(name, projectID string) domain.Issue {
		issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{
			"title": name, "description": name + " body", "teamId": team, "projectId": projectID,
		}, http.StatusCreated)
		requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/issues/"+issue.ID+"/comments", map[string]string{"body": name + " comment"}, http.StatusCreated)
		return issue
	}
	other := makeIssue("Other issue", "")
	focus := makeIssue("Project issue", project.ID)
	favorite := makeIssue("Favorite issue", "")
	requestJSON[domain.Favorite](t, handler, http.MethodPut, "/api/favorites/issue/"+favorite.ID, nil, http.StatusOK)
	full := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, path, nil, http.StatusOK)
	issueIDs := func(issues []domain.Issue) []string {
		ids := make([]string, 0, len(issues))
		for _, issue := range issues {
			ids = append(ids, issue.ID)
		}
		return ids
	}
	for _, test := range []struct {
		name       string
		projection string
		focusBody  bool
	}{
		{"project", "project-detail:previous-name-" + domain.SlugSuffix(project.SlugID), true},
		{"initiative", "initiative-detail", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Header.Set("X-Flow-Projection", test.projection)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			var projected domain.Bootstrap
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &projected) != nil {
				t.Fatalf("planning bootstrap status=%d", response.Code)
			}
			if projected.IssueCollectionPaged || !slices.Equal(issueIDs(full.Issues), issueIDs(projected.Issues)) {
				t.Fatal("planning bootstrap changed the complete issue directory or its ordering")
			}
			for _, issue := range projected.Issues {
				if issue.ID != other.ID && issue.ID != focus.ID && issue.ID != favorite.ID {
					continue
				}
				detailed := issue.ID == favorite.ID || issue.ID == focus.ID && test.focusBody
				if issue.IsSummary == detailed || (issue.Description != "") != detailed ||
					(len(projected.Comments[issue.ID]) > 0) != detailed || (len(projected.Activities[issue.ID]) > 0) != detailed {
					t.Fatal("planning bootstrap returned the wrong issue detail or history scope")
				}
			}
			index := slices.IndexFunc(projected.Projects, func(item domain.Project) bool { return item.ID == project.ID })
			if index < 0 || projected.Projects[index].Description != project.Description ||
				len(projected.ProjectUpdates[project.ID]) != 1 || len(projected.InitiativeUpdates[initiative.ID]) != 1 {
				t.Fatal("planning bootstrap omitted project or initiative details")
			}
			if strings.Contains(response.Body.String(), other.Description) {
				t.Fatal("unrelated issue body was serialized")
			}
		})
	}

	t.Run("export", func(t *testing.T) {
		job := domain.ExportJob{ID: "planning-export", UserID: full.Viewer.ID, Format: "json", Status: "completed"}
		if err := repository.MutateWorkspace(t.Context(), full.Workspace.URLKey, "test.seed", "", nil, func(data *domain.Bootstrap) error {
			data.ExportJobs = append(data.ExportJobs, job)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/exports/"+job.ID+"/download", nil)
		req.Header.Set("X-Flow-Projection", "initiative-detail")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		var exported domain.Bootstrap
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &exported) != nil {
			t.Fatalf("workspace export status=%d", response.Code)
		}
		if !slices.ContainsFunc(exported.Issues, func(issue domain.Issue) bool {
			return issue.ID == other.ID && issue.Description == other.Description
		}) || len(exported.Comments[other.ID]) == 0 {
			t.Fatal("workspace export lost issue details")
		}
	})
}

func TestProjectListBootstrapStreamsFavoriteIssueHistory(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	data := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	const description = "Favorite detail"
	favorite := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "Favorite", "description": description, "teamId": data.Teams[0].ID,
	}, http.StatusCreated)
	other := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "Other", "teamId": data.Teams[0].ID,
	}, http.StatusCreated)
	requestJSON[domain.Favorite](t, handler, http.MethodPut, "/api/favorites/issue/"+favorite.ID, nil, http.StatusOK)
	comment := requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/issues/"+favorite.ID+"/comments", map[string]string{"body": "First favorite comment"}, http.StatusCreated)
	requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/issues/"+other.ID+"/comments", map[string]string{"body": "Other comment"}, http.StatusCreated)
	projected := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap?projection=project-list", nil, http.StatusOK)
	if len(projected.Issues) != 1 || projected.Issues[0].ID != favorite.ID || projected.Issues[0].Description != description || projected.Issues[0].IsSummary {
		t.Fatal("project-list bootstrap changed the favorite issue body")
	}
	history := projected.Comments[favorite.ID]
	if len(history) != 1 || history[0].ID != comment.ID || len(projected.Activities[favorite.ID]) == 0 {
		t.Fatal("project-list bootstrap omitted favorite history")
	}
	if len(projected.Comments[other.ID]) != 0 || len(projected.Activities[other.ID]) != 0 {
		t.Fatal("project-list bootstrap included unrelated history")
	}
}
