package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

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
