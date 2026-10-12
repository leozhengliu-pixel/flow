package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Attaching a file from a URL must not reach loopback or private addresses
// (SSRF), directly or through a redirect.
func TestAttachmentFromURLRejectsInternalAddresses(t *testing.T) {
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
	path := "/api/issues/" + bootstrap.Issues[0].ID + "/attachments/from-url"

	reached := false
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		_, _ = w.Write([]byte("secret"))
	}))
	defer internal.Close()

	for _, target := range []string{
		internal.URL + "/file.txt",                 // loopback
		"http://169.254.169.254/latest/meta-data/", // cloud metadata (link-local)
		"http://10.0.0.1/file.txt",                 // private network
	} {
		requestJSON[map[string]any](t, handler, http.MethodPost, path, map[string]any{"url": target}, http.StatusBadGateway)
	}
	if reached {
		t.Fatal("the internal server was fetched")
	}
	// Credentials in the URL are refused before any request is made.
	requestJSON[map[string]any](t, handler, http.MethodPost, path, map[string]any{"url": "http://user:pass@example.com/file.txt"}, http.StatusBadRequest)
}

// The endpoint is reachable through the issue-records alias and stores the
// downloaded file (the fetch client is swapped to allow the test server).
func TestAttachmentFromURLStoresDownloadedFile(t *testing.T) {
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
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hello"))
	}))
	defer source.Close()
	previous := attachmentFetchClient
	attachmentFetchClient = func() *http.Client { return source.Client() }
	defer func() { attachmentFetchClient = previous }()
	created := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/issues/"+bootstrap.Issues[0].ID+"/attachments/from-url", map[string]any{"url": source.URL + "/notes.txt"}, http.StatusCreated)
	if created["id"] == nil && created["attachment"] == nil {
		t.Fatalf("unexpected response: %#v", created)
	}
}
