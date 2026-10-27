package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestStaticFilesMissingAssetsAre404NotTheApp(t *testing.T) {
	staticPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(staticPath, "index.html"), []byte("<html><body>Flow</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := (&server{staticPath: staticPath}).withStaticFiles(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("static handler unexpectedly delegated to API")
	}))

	// A stale client asking for an old chunk hash must see the failure.
	for _, path := range []string{"/assets/definitely-not-here-12345.js", "/assets/index-old.CSS", "/fonts/gone.woff2", "/favicon-old.svg", "/haier/agent/chunk.mjs"} {
		missing := httptest.NewRecorder()
		handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, path, nil))
		if missing.Code != http.StatusNotFound || missing.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s status=%d body=%q", path, missing.Code, missing.Body.String())
		}
	}
	// Extensionless routes still open the app, even under a workspace keyed "assets".
	for _, path := range []string{"/haier/agent", "/assets/issue/AST-1", "/fonts", "/acme/project/v1.2-release"} {
		page := httptest.NewRecorder()
		handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, path, nil))
		if page.Code != http.StatusOK || page.Body.String() != "<html><body>Flow</body></html>" {
			t.Fatalf("%s status=%d body=%q", path, page.Code, page.Body.String())
		}
	}
}

func TestStaticFilesCompressTextAssetsOnly(t *testing.T) {
	staticPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(staticPath, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := strings.Repeat("console.log('flow application code');\n", 500)
	html := "<html><body>" + strings.Repeat("Flow ", 500) + "</body></html>"
	files := map[string]string{
		"assets/app-123.js":   script,
		"assets/app-123.css":  strings.Repeat("a{color:red}", 500),
		"assets/logo-123.svg": "<svg>" + strings.Repeat("<path/>", 500) + "</svg>",
		"assets/pic-123.png":  strings.Repeat("png", 500),
		"sw.js":               "self.skipWaiting()",
		"index.html":          html,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(staticPath, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	handler := (&server{staticPath: staticPath}).withStaticFiles(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("static handler unexpectedly delegated to API")
	}))
	get := func(path, accept string, header ...string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if accept != "" {
			r.Header.Set("Accept-Encoding", accept)
		}
		for i := 0; i+1 < len(header); i += 2 {
			r.Header.Set(header[i], header[i+1])
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	decode := func(w *httptest.ResponseRecorder) string {
		reader, err := gzip.NewReader(w.Body)
		if err != nil {
			t.Fatal(err)
		}
		out, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}

	for path, want := range map[string]string{"/assets/app-123.js": script, "/assets/app-123.css": files["assets/app-123.css"], "/assets/logo-123.svg": files["assets/logo-123.svg"], "/": html, "/acme/issues": html} {
		w := get(path, "gzip, br")
		if w.Code != http.StatusOK || w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("Vary") != "Accept-Encoding" || w.Header().Get("Content-Length") != "" {
			t.Fatalf("%s status=%d headers=%v", path, w.Code, w.Header())
		}
		if decode(w) != want {
			t.Fatalf("%s compressed body corrupt", path)
		}
	}
	// Cache policy is unchanged by compression.
	if w := get("/assets/app-123.js", "gzip"); w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("asset cache-control=%q", w.Header().Get("Cache-Control"))
	}
	if w := get("/", "gzip"); w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("index cache-control=%q", w.Header().Get("Cache-Control"))
	}
	if w := get("/sw.js", "gzip"); w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("sw cache-control=%q", w.Header().Get("Cache-Control"))
	}
	// No gzip support, binary types and ranges are served as-is.
	if w := get("/assets/app-123.js", "identity"); w.Header().Get("Content-Encoding") != "" || w.Body.String() != script {
		t.Fatalf("identity response encoded: %v", w.Header())
	}
	if w := get("/assets/pic-123.png", "gzip"); w.Header().Get("Content-Encoding") != "" || w.Body.String() != files["assets/pic-123.png"] {
		t.Fatalf("png response encoded: %v", w.Header())
	}
	if w := get("/assets/app-123.js", "gzip", "Range", "bytes=0-9"); w.Code != http.StatusPartialContent || w.Header().Get("Content-Encoding") != "" || w.Body.String() != script[:10] {
		t.Fatalf("range status=%d headers=%v", w.Code, w.Header())
	}
	// Revalidation still answers 304 without a body or encoding.
	first := get("/assets/app-123.js", "gzip")
	if w := get("/assets/app-123.js", "gzip", "If-Modified-Since", first.Header().Get("Last-Modified")); w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatalf("revalidation status=%d body=%d", w.Code, w.Body.Len())
	}
	// 404s for missing assets stay plain.
	if w := get("/assets/gone-1.js", "gzip"); w.Code != http.StatusNotFound {
		t.Fatalf("missing asset status=%d", w.Code)
	}
}
