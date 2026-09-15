package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
