package main

import (
	"encoding/json"
	"flow/api/internal/domain"
	"flow/api/internal/store"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssueDetailBootstrapOmitsUnrelatedBodiesWithoutChangingStoredData(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "projection.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	body := strings.Repeat("project-private-body ", 50000)
	if err = repo.MutateWorkspace(t.Context(), data.Workspace.URLKey, "project.updated", data.Projects[0].ID, nil, func(data *domain.Bootstrap) error { data.Projects[0].Description = body; return nil }); err != nil {
		t.Fatal(err)
	}
	handler := newHandler(&server{store: repo, authDisabled: true, uploadPath: t.TempDir()})
	for _, path := range []string{"/api/bootstrap", "/api/issue-records/bootstrap"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("X-Flow-Projection", "issue-detail")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var result domain.Bootstrap
		if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if !result.ResourceDetailsOmitted || len(result.Projects) == 0 || result.Projects[0].Description != "" || len(result.Comments) > 0 {
			t.Fatal("detail projection omitted picker metadata or leaked bodies")
		}
		if strings.Contains(w.Body.String(), "project-private-body") {
			t.Fatal("raw snapshot bypassed projection")
		}
	}
	stored := repo.Bootstrap()
	if stored.Projects[0].Description != body {
		t.Fatal("projection modified stored project")
	}
}
