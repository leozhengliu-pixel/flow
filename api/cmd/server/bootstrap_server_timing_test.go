package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"testing"

	"flow/api/internal/store"
)

func TestBootstrapReportsServerTiming(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	for _, path := range []string{"/api/bootstrap"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status %d: %s", path, recorder.Code, recorder.Body.String())
		}
		if got := recorder.Header().Get("Server-Timing"); !regexp.MustCompile(`^bootstrap;dur=\d+\.\d+$`).MatchString(got) {
			t.Fatalf("%s Server-Timing = %q", path, got)
		}
	}
}
