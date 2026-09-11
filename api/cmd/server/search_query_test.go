package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestSearchFiltersSortAndArchiveRunBeforeCandidateLimit(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	handler := newHandler(&server{store: repo, authDisabled: true})
	base := data.Issues[0]
	base.ID = "search-old"
	base.Identifier = "FIND-1"
	base.Title = "Query needle old"
	base.Description = ""
	base.CreatedAt = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	base.UpdatedAt = base.CreatedAt
	base.State.Type = "started"
	base.Assignee = &data.Viewer
	base.Creator = data.Viewer
	newer := base
	newer.ID = "search-new"
	newer.Identifier = "FIND-2"
	newer.Title = "Query needle new"
	newer.CreatedAt = base.CreatedAt.AddDate(1, 0, 0)
	newer.UpdatedAt = newer.CreatedAt
	unassigned := newer
	unassigned.ID = "search-unassigned"
	unassigned.Identifier = "FIND-3"
	unassigned.Assignee = nil
	archived := newer
	archived.ID = "search-archived"
	archived.Identifier = "FIND-4"
	archived.ArchivedAt = &newer.CreatedAt
	if err := repo.ImportIssues(context.Background(), data.Workspace.URLKey, []domain.Issue{base, newer, unassigned, archived}); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"/api/search", "/api/search/semantic"} {
		params := url.Values{"q": {"Query needle"}, "types": {"issue"}, "sort": {"createdAt"}, "limit": {"1"}, "statusType": {"started"}, "assigneeId": {data.Viewer.ID}, "creatorId": {data.Viewer.ID}, "createdAfter": {"2025-12-31"}}
		response := requestJSON[domain.SearchResponse](t, handler, http.MethodGet, endpoint+"?"+params.Encode(), nil, 200)
		if len(response.Results) != 1 || response.Results[0].ID != newer.ID || response.Results[0].State == nil || !strings.Contains(response.Results[0].URL, "/issue/FIND-2/") {
			t.Fatalf("server filters/sort/link failed: %+v", response.Results)
		}
		params.Del("createdAfter")
		params.Del("assigneeId")
		params.Del("creatorId")
		params.Set("limit", "100")
		filter, _ := json.Marshal(store.IssueFilter{Or: []store.IssueFilter{{Field: "assigneeId", Values: []string{"none"}}, {Field: "createdAt", Operator: "before", Values: []string{"2025-12-31"}}}})
		params.Set("filter", string(filter))
		response = requestJSON[domain.SearchResponse](t, handler, http.MethodGet, endpoint+"?"+params.Encode(), nil, 200)
		if len(response.Results) != 2 {
			t.Fatalf("OR/unassigned filter not applied: %+v", response.Results)
		}
		params.Del("filter")
		params.Set("includeArchived", "true")
		response = requestJSON[domain.SearchResponse](t, handler, http.MethodGet, endpoint+"?"+params.Encode(), nil, 200)
		if len(response.Results) != 4 {
			t.Fatalf("includeArchived ignored: %+v", response.Results)
		}
	}
}

func TestSearchProjectLeadStatusAndUncachedRoute(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	handler := newHandler(&server{store: repo, authDisabled: true})
	project := domain.Project{ID: "search-project", SlugID: "measured-project-route", Name: "Cobaltneedle", Lead: &data.Viewer, Status: domain.ProjectStatus{ID: "ps-started", Name: "In progress", Type: "started"}, TeamIDs: []string{data.Teams[0].ID}}
	if err := repo.MutateWorkspace(context.Background(), data.Workspace.URLKey, "project.created", project.ID, nil, func(next *domain.Bootstrap) error { next.Projects = append(next.Projects, project); return nil }); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"/api/search", "/api/search/semantic"} {
		params := url.Values{"q": {"Cobaltneedle"}, "types": {"project"}, "assigneeId": {data.Viewer.ID}, "statusType": {"started"}}
		response := requestJSON[domain.SearchResponse](t, handler, http.MethodGet, endpoint+"?"+params.Encode(), nil, 200)
		if len(response.Results) != 1 || response.Results[0].ProjectStatus == nil || response.Results[0].URL != "/"+data.Workspace.URLKey+"/project/measured-project-route/overview" {
			t.Fatalf("project route/status/lead mismatch: %+v", response.Results)
		}
		params.Set("assigneeId", "none")
		response = requestJSON[domain.SearchResponse](t, handler, http.MethodGet, endpoint+"?"+params.Encode(), nil, 200)
		if len(response.Results) != 0 {
			t.Fatal("unassigned project filter ignored")
		}
	}
}

func TestEmptySemanticSearchDoesNotEnumerateIssues(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	handler := newHandler(&server{store: repo, authDisabled: true})
	response := requestJSON[domain.SearchResponse](t, handler, http.MethodGet, "/api/search/semantic?q=", nil, 200)
	if len(response.Results) != 0 {
		t.Fatal("empty semantic search enumerated workspace")
	}
}

func TestSearchFlowTermAndEmptyQuerySkipCatalog(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	handler := newHandler(&server{store: repo, authDisabled: true})
	issue := data.Issues[0]
	issue.ID = "flow-search-hit"
	issue.Identifier = "FLOW-42"
	issue.Title = "Flow query target"
	issue.Description = "Indexed Flow description"
	if err := repo.ImportIssues(context.Background(), data.Workspace.URLKey, []domain.Issue{issue}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SearchIssueCandidates(context.Background(), store.IssueRecordQuery{Workspace: data.Workspace.URLKey, Archived: "all"}, "  ", nil, 10, func(domain.Issue) error {
		t.Fatal("empty candidate search walked issues")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"/api/search?q=Flow&types=issue", "/api/search/semantic?q=Flow&types=issue"} {
		response := requestJSON[domain.SearchResponse](t, handler, http.MethodGet, endpoint, nil, 200)
		found := false
		for _, result := range response.Results {
			if result.ID == issue.ID && result.Type == "issue" {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s missing Flow hit: %+v", endpoint, response.Results)
		}
	}
	empty := requestJSON[domain.SearchResponse](t, handler, http.MethodGet, "/api/search?q=%20%20&types=issue", nil, 200)
	if len(empty.Results) != 0 {
		t.Fatalf("whitespace query scanned catalog: %+v", empty.Results)
	}
	requestJSON[domain.SearchResponse](t, handler, http.MethodGet, "/api/search", nil, 200)
	requestJSON[domain.SearchResponse](t, handler, http.MethodGet, "/api/search/semantic?q=%20", nil, 200)
}

func TestCancelledSearchDoesNotReportUnavailable(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	service := &server{store: repo, authDisabled: true}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, path := range []string{"/api/search?q=Flow&types=issue", "/api/search/semantic?q=Flow&types=issue"} {
		r := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
		w := httptest.NewRecorder()
		if strings.Contains(path, "semantic") {
			service.semanticSearch(w, r)
		} else {
			service.searchWorkspace(w, r)
		}
		if w.Code == http.StatusInternalServerError && strings.Contains(w.Body.String(), "Could not query issues") {
			t.Fatalf("cancelled %s reported unavailable: %s", path, w.Body.String())
		}
	}
}

func TestSearchAndMCPKeepIdenticalAPIKeyTeamBoundaries(t *testing.T) {
	repo, actor, ctx := newMCPToolTestContext(t)
	data := repo.Bootstrap()
	service := &server{store: repo}
	actor.APIKey.TeamRestriction = "selected"
	actor.APIKey.TeamIDs = []string{data.Teams[0].ID}
	ctx = context.WithValue(ctx, apiKeyContextKey{}, actor.APIKey)
	visible := data.Issues[0]
	visible.ID = "scoped-visible"
	visible.Identifier = "SCOPED-1"
	visible.Title = "Scopedneedle visible"
	visible.Team = data.Teams[0]
	visible.Project = nil
	hidden := visible
	hidden.ID = "scoped-hidden"
	hidden.Identifier = "SCOPED-2"
	hidden.Title = "Scopedneedle hidden"
	hidden.Team = domain.Team{ID: "restricted-search-team", Name: "Restricted", Key: "RST"}
	if err := repo.MutateWorkspace(ctx, actor.WorkspaceKey, "team.created", hidden.Team.ID, nil, func(next *domain.Bootstrap) error { next.Teams = append(next.Teams, hidden.Team); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := repo.ImportIssues(ctx, actor.WorkspaceKey, []domain.Issue{visible, hidden}); err != nil {
		t.Fatal(err)
	}
	q, err := service.mcpIssueQuery(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	seen := []string{}
	if err := repo.SearchIssueCandidates(ctx, q, "Scopedneedle", nil, 100, func(issue domain.Issue) error { seen = append(seen, issue.ID); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != visible.ID {
		t.Fatalf("MCP key boundary leaked: %v", seen)
	}
	for _, handler := range []http.HandlerFunc{service.searchWorkspace, service.semanticSearch} {
		r := httptest.NewRequest("GET", "/api/search?q=Scopedneedle&types=issue", nil).WithContext(ctx)
		w := httptest.NewRecorder()
		handler(w, r)
		var result domain.SearchResponse
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || len(result.Results) != 1 || result.Results[0].ID != visible.ID {
			t.Fatalf("HTTP key boundary differs: %d %s", w.Code, w.Body.String())
		}
	}
	if err := repo.RecordRecent(ctx, data.Workspace.ID, data.Viewer.ID, "issue", hidden.ID); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/search?types=issue", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	service.searchWorkspace(w, r)
	var result domain.SearchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(result.Results) != 0 || len(result.Recent) != 0 {
		t.Fatalf("revoked recent metadata leaked: %s", w.Body.String())
	}
}

func TestSearchRejectsUnknownFiltersConsistentlyAcrossResourceTypes(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	handler := newHandler(&server{store: repo, authDisabled: true})
	filter, _ := json.Marshal(store.IssueFilter{Field: "notAField", Operator: "isNot", Values: []string{"x"}})
	for _, kind := range []string{"issue", "project", "document"} {
		for _, endpoint := range []string{"/api/search", "/api/search/semantic"} {
			params := url.Values{"q": {"needle"}, "types": {kind}, "filter": {string(filter)}}
			requestJSON[any](t, handler, "GET", endpoint+"?"+params.Encode(), nil, 400)
		}
	}
}
