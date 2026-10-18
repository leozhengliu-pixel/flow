package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	appconfig "flow/api/internal/config"
	"flow/api/internal/store"
)

// The skeleton registers every coding route; until a track implements a
// handler it answers 501. Runner-facing routes skip browser authentication
// (they check the sandbox token themselves); web routes still require it.
func TestCodingRoutesAreRegistered(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "coding-routes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	s := &server{store: repository, uploadPath: t.TempDir()}
	s.initCoding(appconfig.CodingConfig{Provider: appconfig.CodingProviderDisabled})
	if s.codingEnabled() || s.coding.sandbox.Name() != appconfig.CodingProviderDisabled || len(s.coding.hosts) != 2 {
		t.Fatalf("disabled coding runtime: %+v", s.coding)
	}
	handler := newHandler(s)
	for _, test := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/internal/coding-runtime/bootstrap", http.StatusNotImplemented},
		{http.MethodPost, "/internal/llm-proxy/anthropic/v1/messages", http.StatusNotImplemented},
		{http.MethodPost, "/api/integrations/github-app/webhook", http.StatusNotImplemented},
		{http.MethodGet, "/api/coding/sessions", http.StatusUnauthorized},
	} {
		request := httptest.NewRequest(test.method, test.path, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != test.want {
			t.Errorf("%s %s: got %d want %d (%s)", test.method, test.path, recorder.Code, test.want, recorder.Body.String())
		}
	}
}
