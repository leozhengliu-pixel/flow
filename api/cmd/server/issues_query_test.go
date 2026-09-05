package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestIssueQuerySupportsNestedFiltersAndCursors(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Issues) < 2 {
		t.Skip("fixture has fewer than two issues")
	}
	first := bootstrap.Issues[0]
	filter := map[string]any{"and": []any{
		map[string]any{"field": "teamId", "operator": "is", "value": first.Team.ID},
		map[string]any{"or": []any{
			map[string]any{"field": "priority", "operator": "is", "value": first.Priority},
			map[string]any{"field": "status", "operator": "is", "value": first.State.Type},
		}},
	}}
	encoded, _ := json.Marshal(filter)
	query := url.Values{"filter": []string{string(encoded)}, "archived": []string{"all"}, "limit": []string{"1"}}
	page := requestJSON[issueQueryResponse](t, handler, http.MethodGet, "/api/issues?"+query.Encode(), nil, http.StatusOK)
	if page.Total < 1 || len(page.Items) != 1 || !page.HasMore {
		t.Fatalf("first query page = total %d items %d more %v", page.Total, len(page.Items), page.HasMore)
	}
	if page.NextCursor == "" {
		t.Fatal("query omitted next cursor")
	}
	next := requestJSON[issueQueryResponse](t, handler, http.MethodGet, "/api/issues?"+url.Values{"filter": []string{string(encoded)}, "archived": []string{"all"}, "limit": []string{"1"}, "cursor": []string{page.NextCursor}}.Encode(), nil, http.StatusOK)
	if len(next.Items) > 1 || (len(next.Items) == 1 && next.Items[0].ID == page.Items[0].ID) {
		t.Fatalf("cursor returned duplicate item: first=%v next=%v", page.Items[0].ID, next.Items)
	}
	bad := requestJSON[map[string]any](t, handler, http.MethodGet, "/api/issues?filter=%7B", nil, http.StatusBadRequest)
	if bad == nil {
		t.Fatal("expected malformed filter error")
	}
}

func TestIssueQueryAcceptsSavedViewValueObjects(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Issues) == 0 {
		t.Skip("fixture has no issues")
	}
	filter := `{"and":[{"field":"status","operator":"is","values":[{"id":"` + bootstrap.Issues[0].State.ID + `","label":"state"}]}]}`
	page := requestJSON[issueQueryResponse](t, handler, http.MethodGet, "/api/issues?archived=all&filter="+url.QueryEscape(filter), nil, http.StatusOK)
	if page.Total == 0 {
		t.Fatalf("saved view value object did not match state %q", bootstrap.Issues[0].State.ID)
	}
}
