package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestStaticFilesCacheHeaders(t *testing.T) {
	staticPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(staticPath, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staticPath, "assets", "app-123.js"), []byte("console.log('app')"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staticPath, "index.html"), []byte("<html><body>Flow</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := (&server{staticPath: staticPath}).withStaticFiles(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("static handler unexpectedly delegated to API")
	}))

	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/assets/app-123.js", nil))
	if asset.Code != http.StatusOK || asset.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("asset response status=%d cache-control=%q", asset.Code, asset.Header().Get("Cache-Control"))
	}

	spa := httptest.NewRecorder()
	handler.ServeHTTP(spa, httptest.NewRequest(http.MethodGet, "/integration-workspace/issue/INT-1", nil))
	if spa.Code != http.StatusOK || spa.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("SPA response status=%d cache-control=%q", spa.Code, spa.Header().Get("Cache-Control"))
	}
}

func TestStaticFilesServeTutorialClipsAndKeepDirectoriesAsRoutes(t *testing.T) {
	staticPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(staticPath, "tour"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staticPath, "tour", "inbox.gif"), []byte("GIF89a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staticPath, "index.html"), []byte("<html><body>Flow</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := (&server{staticPath: staticPath}).withStaticFiles(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("static handler unexpectedly delegated to API")
	}))

	clip := httptest.NewRecorder()
	handler.ServeHTTP(clip, httptest.NewRequest(http.MethodGet, "/tour/inbox.gif", nil))
	if clip.Code != http.StatusOK || clip.Header().Get("Content-Type") != "image/gif" || clip.Header().Get("Cache-Control") != "public, max-age=86400" {
		t.Fatalf("tutorial clip status=%d type=%q cache-control=%q", clip.Code, clip.Header().Get("Content-Type"), clip.Header().Get("Cache-Control"))
	}
	// A workspace keyed "tour" opens the app, not a directory listing.
	for _, path := range []string{"/tour", "/tour/"} {
		page := httptest.NewRecorder()
		handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, path, nil))
		if page.Code != http.StatusOK || page.Header().Get("Cache-Control") != "no-cache" || page.Body.String() != "<html><body>Flow</body></html>" {
			t.Fatalf("%s status=%d cache-control=%q body=%q", path, page.Code, page.Header().Get("Cache-Control"), page.Body.String())
		}
	}
}
