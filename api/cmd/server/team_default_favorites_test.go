package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestTeamDefaultFavoritesHTTPWorkflow(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "defaults.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	request := func(method, path string, body any, target any) {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("X-Workspace-Key", data.Workspace.URLKey)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s: HTTP %d %s", method, path, w.Code, w.Body.String())
		}
		if target != nil && json.Unmarshal(w.Body.Bytes(), target) != nil {
			t.Fatalf("invalid response: %s", w.Body.String())
		}
	}
	var saved map[string]any
	request(http.MethodPut, "/api/teams/"+data.Teams[0].ID+"/default-favorites", map[string]any{"items": []map[string]string{{"resourceType": "issue", "resourceId": data.Issues[0].ID}}}, &saved)
	if len(saved["items"].([]any)) != 1 {
		t.Fatal("default favorite was not saved")
	}
	var preferences map[string]any
	request(http.MethodGet, "/api/resource-preferences", nil, &preferences)
	if len(preferences["favorites"].([]any)) != 1 {
		t.Fatal("default favorite was not merged into preferences")
	}
	delete := httptest.NewRequest(http.MethodDelete, "/api/favorites/issue/"+data.Issues[0].ID, nil)
	delete.Header.Set("X-Workspace-Key", data.Workspace.URLKey)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, delete)
	if response.Code != http.StatusNoContent {
		t.Fatalf("remove default favorite: HTTP %d", response.Code)
	}
	request(http.MethodGet, "/api/resource-preferences", nil, &preferences)
	if len(preferences["favorites"].([]any)) != 0 {
		t.Fatal("hidden default favorite was restored")
	}
}

func TestTeamDefaultFavoritesAcceptsLegacyUIFieldsAndOwnsMetadata(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "defaults.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	path := "/api/teams/" + data.Teams[0].ID + "/default-favorites"
	wanted := []map[string]any{
		{"resourceType": "project", "resourceId": data.Projects[0].ID, "id": "local:project", "workspaceKey": "wrong", "teamId": "wrong", "createdAt": "", "updatedAt": "", "position": 99},
		{"resourceType": "team", "resourceId": data.Teams[0].ID, "id": "local:team", "createdAt": "", "updatedAt": ""},
	}
	for i := range 2 {
		raw, _ := json.Marshal(map[string]any{"items": wanted})
		r := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(raw))
		r.Header.Set("X-Workspace-Key", data.Workspace.URLKey)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("save %d: HTTP %d %s", i, w.Code, w.Body.String())
		}
		var result struct {
			Items []domain.TeamDefaultFavorite `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Items) != 2 {
			t.Fatalf("saved %d items", len(result.Items))
		}
		for index, item := range result.Items {
			if item.ID == wanted[index]["id"] || item.WorkspaceKey != data.Workspace.URLKey || item.TeamID != data.Teams[0].ID || item.CreatedAt.IsZero() || item.UpdatedAt.IsZero() || item.Position != float64(index) {
				t.Fatalf("client metadata was trusted: %+v", item)
			}
		}
	}
	for _, body := range []string{`{"items":`, `{"items":"wrong"}`, `{"items":null}`, `{}`, `{"items":[{"resourceType":"project","resourceId":12}]}`} {
		r := httptest.NewRequest(http.MethodPut, path, bytes.NewBufferString(body))
		r.Header.Set("X-Workspace-Key", data.Workspace.URLKey)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var result map[string]string
		if w.Code != http.StatusBadRequest || json.Unmarshal(w.Body.Bytes(), &result) != nil || result["error"] == "" {
			t.Fatalf("expected one valid JSON error for %s: %d %s", body, w.Code, w.Body.String())
		}
		saved, err := repo.ListTeamDefaultFavorites(t.Context(), data.Workspace.URLKey, data.Teams[0].ID)
		if err != nil || len(saved) != 2 {
			t.Fatalf("invalid input changed configuration: %v %+v", err, saved)
		}
	}
}
