package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type contentPage[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor"`
	HasMore    bool   `json:"hasMore"`
	Total      int    `json:"total"`
}

func newContentFeatureHandler(t *testing.T) (http.Handler, *store.SQLiteStore) {
	t.Helper()
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	return newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true}), repository
}

func TestDashboardLifecycleResultsShareSubscriptionAndExport(t *testing.T) {
	handler, repository := newContentFeatureHandler(t)
	defer repository.Close()
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	dashboard := requestJSON[domain.Dashboard](t, handler, http.MethodPost, "/api/dashboards", map[string]any{
		"name": "Delivery overview", "visibility": "workspace",
		"widgets": []map[string]any{{"type": "issue_count", "title": "All issues"}, {"type": "status_breakdown", "title": "By status", "width": 2}},
	}, http.StatusCreated)
	if dashboard.OwnerID != bootstrap.Viewer.ID || len(dashboard.Widgets) != 2 || dashboard.Widgets[0].ID == "" {
		t.Fatalf("invalid dashboard: %#v", dashboard)
	}
	sanitized := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if sanitized.Settings[dashboardsSettingsKey] != nil || sanitized.Settings[feedSettingsKey] != nil {
		t.Fatal("permission-scoped dashboard data leaked through bootstrap settings")
	}
	page := requestJSON[contentPage[domain.Dashboard]](t, handler, http.MethodGet, "/api/dashboards?limit=1", nil, http.StatusOK)
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("dashboard page = %#v", page)
	}
	result := requestJSON[struct {
		Results []domain.DashboardWidgetResult `json:"results"`
	}](t, handler, http.MethodGet, "/api/dashboards/"+dashboard.ID+"/results", nil, http.StatusOK)
	if len(result.Results) != 2 {
		t.Fatalf("dashboard results = %#v", result)
	}
	dashboard = requestJSON[domain.Dashboard](t, handler, http.MethodPut, "/api/dashboards/"+dashboard.ID+"/subscription", nil, http.StatusOK)
	if len(dashboard.SubscriberIDs) != 1 || dashboard.SubscriberIDs[0] != bootstrap.Viewer.ID {
		t.Fatalf("subscription not persisted: %#v", dashboard.SubscriberIDs)
	}
	dashboard = requestJSON[domain.Dashboard](t, handler, http.MethodPost, "/api/dashboards/"+dashboard.ID+"/share", nil, http.StatusOK)
	if dashboard.ShareToken == "" || dashboard.SharedAt == nil {
		t.Fatal("share token was not created")
	}
	shared := requestJSON[struct {
		Dashboard domain.Dashboard `json:"dashboard"`
	}](t, handler, http.MethodGet, "/api/shared/dashboards/"+dashboard.ShareToken, nil, http.StatusOK)
	if shared.Dashboard.ID != dashboard.ID {
		t.Fatalf("shared dashboard = %#v", shared)
	}
	exportRequest := httptest.NewRequest(http.MethodGet, "/api/dashboards/"+dashboard.ID+"/export", nil)
	exportResponse := httptest.NewRecorder()
	handler.ServeHTTP(exportResponse, exportRequest)
	if exportResponse.Code != http.StatusOK || exportResponse.Header().Get("Content-Type") != "text/csv; charset=utf-8" || exportResponse.Body.Len() == 0 {
		t.Fatalf("dashboard export: status=%d headers=%v body=%q", exportResponse.Code, exportResponse.Header(), exportResponse.Body.String())
	}
	requestJSON[domain.Dashboard](t, handler, http.MethodDelete, "/api/dashboards/"+dashboard.ID+"/share", nil, http.StatusOK)
	requestJSON[any](t, handler, http.MethodDelete, "/api/dashboards/"+dashboard.ID, nil, http.StatusNoContent)
}

func TestPostsMeetingsAndFeedPersist(t *testing.T) {
	handler, repository := newContentFeatureHandler(t)
	defer repository.Close()
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	post := requestJSON[domain.Post](t, handler, http.MethodPost, "/api/posts", map[string]any{"title": "Launch note", "body": "Release is ready", "teamIds": []string{bootstrap.Teams[0].ID}}, http.StatusCreated)
	if post.ID == "" || post.CreatorID != bootstrap.Viewer.ID {
		t.Fatalf("post = %#v", post)
	}
	meeting := requestJSON[domain.Meeting](t, handler, http.MethodPost, "/api/meetings", map[string]any{"title": "Launch review", "startsAt": time.Now().UTC().Add(time.Hour), "durationMinutes": 45, "teamIds": []string{bootstrap.Teams[0].ID}, "issueIds": []string{bootstrap.Issues[0].ID}}, http.StatusCreated)
	if meeting.ID == "" || len(meeting.IssueIDs) != 1 {
		t.Fatalf("meeting = %#v", meeting)
	}
	feed := requestJSON[contentPage[domain.FeedItem]](t, handler, http.MethodGet, "/api/feed?limit=10", nil, http.StatusOK)
	if feed.Total != 2 || feed.Items[0].ResourceType != "meeting" || feed.Items[1].ResourceType != "post" {
		t.Fatalf("feed = %#v", feed)
	}
	meetings := requestJSON[contentPage[domain.Meeting]](t, handler, http.MethodGet, "/api/meetings", nil, http.StatusOK)
	posts := requestJSON[contentPage[domain.Post]](t, handler, http.MethodGet, "/api/posts", nil, http.StatusOK)
	if meetings.Total != 1 || posts.Total != 1 {
		t.Fatalf("persistence failed: meetings=%#v posts=%#v", meetings, posts)
	}
}

func TestSemanticSearchFacetsAndFilterSuggestions(t *testing.T) {
	handler, repository := newContentFeatureHandler(t)
	defer repository.Close()
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	query := bootstrap.Issues[0].Team.Name
	result := requestJSON[struct {
		Results []semanticResult                        `json:"results"`
		Facets  map[string][]domain.SemanticSearchFacet `json:"facets"`
		Total   int                                     `json:"total"`
	}](t, handler, http.MethodGet, "/api/search/semantic?q="+url.QueryEscape(query)+"&types=issue", nil, http.StatusOK)
	if result.Total == 0 || len(result.Results[0].MatchedTerms) == 0 || len(result.Facets["team"]) == 0 {
		t.Fatalf("semantic result = %#v", result)
	}
	suggestions := requestJSON[[]domain.FilterSuggestion](t, handler, http.MethodGet, "/api/search/filter-suggestions?field=status", nil, http.StatusOK)
	if len(suggestions) == 0 || suggestions[0].Field != "status" || suggestions[0].Count == 0 {
		t.Fatalf("suggestions = %#v", suggestions)
	}
}
