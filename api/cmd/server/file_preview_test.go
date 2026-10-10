package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestParseFilePreviewURLAcceptsBlobLinks(t *testing.T) {
	cases := []struct {
		raw      string
		provider string
		prefix   string
		segments string
		start    int
		end      int
	}{
		{"https://github.com/golang/go/blob/master/README.md#L1-L10", "github", "https://github.com/golang/go", "master/README.md", 1, 10},
		{"https://www.github.com/golang/go/blob/master/src/net/http/server.go#L42", "github", "https://github.com/golang/go", "master/src/net/http/server.go", 42, 42},
		{"http://github.com/a-b/repo.js/blob/v1.2.3/dir/file%20name.ts#L20C3-L5C1", "github", "https://github.com/a-b/repo.js", "v1.2.3/dir/file name.ts", 5, 20},
		{"https://github.com/o/r/blob/main/a.txt?plain=1", "github", "https://github.com/o/r", "main/a.txt", 0, 0},
		{"https://gitlab.com/group/sub/project/-/blob/main/src/app.rb#L10-20", "gitlab", "https://gitlab.com/group/sub/project", "main/src/app.rb", 10, 20},
		{"https://git.example.com/team/project/-/blob/feature/x/lib/a.go", "gitlab", "https://git.example.com/team/project", "feature/x/lib/a.go", 0, 0},
	}
	for _, tc := range cases {
		target, err := parseFilePreviewURL(tc.raw)
		if err != nil {
			t.Fatalf("%s rejected: %v", tc.raw, err)
		}
		if target.Provider != tc.provider || target.Prefix != tc.prefix || strings.Join(target.Segments, "/") != tc.segments || target.StartLine != tc.start || target.EndLine != tc.end {
			t.Fatalf("%s parsed as %s", tc.raw, target)
		}
	}
}

func TestParseFilePreviewURLRejectsSSRFAndMalformedLinks(t *testing.T) {
	for _, raw := range []string{
		"",
		"github.com/golang/go/blob/master/README.md",
		"ftp://github.com/golang/go/blob/master/README.md",
		"javascript:alert(1)//github.com/a/b/blob/c/d",
		"https://evil.example/golang/go/blob/master/README.md",
		"https://github.com.evil.example/golang/go/blob/master/README.md",
		"https://api.github.com/repos/golang/go/contents/README.md",
		"https://raw.githubusercontent.com/golang/go/master/README.md",
		"https://user:pass@github.com/golang/go/blob/master/README.md",
		"https://github.com@169.254.169.254/golang/go/blob/master/README.md",
		"https://github.com:8443/golang/go/blob/master/README.md",
		"https://github.com/golang/go/tree/master/src",
		"https://github.com/golang/go/blob/master",
		"https://github.com/golang/go/pull/1",
		"https://github.com/-bad/go/blob/master/README.md",
		"https://github.com/golang/../blob/master/README.md",
		"https://github.com/golang/go/blob/master/../../../etc/passwd",
		"https://github.com/golang/go/blob/master/%2e%2e/secret",
		"https://github.com/golang/go/blob/master/a%2Fb",
		"https://github.com/golang/go/blob/master/a%5Cb",
		"https://github.com/golang/go/blob/master/a%00b",
		"https://github.com/golang/go/blob/master/" + strings.Repeat("a/", 1100),
		"http://gitlab.example.com/team/project/-/blob/main/a.go",
		"https://gitlab.example.com/project/-/blob/main/a.go",
		"https://gitlab.example.com/team/project/-/blob/main",
		"https://gitlab.example.com/team/../-/blob/main/a.go",
		"https://169.254.169.254/latest/meta-data/-/blob/main/a",
	} {
		target, err := parseFilePreviewURL(raw)
		if raw == "https://169.254.169.254/latest/meta-data/-/blob/main/a" {
			// Parses as a GitLab link, but a host no GitLab connection names is never contacted (see the handler test).
			if err != nil || target.Provider != "gitlab" {
				t.Fatalf("%s: %v %s", raw, err, target)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%q accepted as %s", raw, target)
		}
	}
}

func TestParseFilePreviewLines(t *testing.T) {
	for fragment, want := range map[string][2]int{
		"":           {0, 0},
		"L7":         {7, 7},
		"L7-L9":      {7, 9},
		"L7-9":       {7, 9},
		"L9-L7":      {7, 9},
		"L3C2-L4C10": {3, 4},
		"L0":         {0, 0},
		"readme":     {0, 0},
		"L1-":        {0, 0},
		"L99999999":  {0, 0},
	} {
		start, end := parseFilePreviewLines(fragment)
		if start != want[0] || end != want[1] {
			t.Errorf("#%s = %d-%d, want %d-%d", fragment, start, end, want[0], want[1])
		}
	}
}

func TestFilePreviewSliceRangesAndLimits(t *testing.T) {
	content := "one\r\ntwo\nthree\nfour\n"
	lines, first, last, total, truncated, err := filePreviewSlice(content, 2, 3)
	if err != nil || strings.Join(lines, ",") != "two,three" || first != 2 || last != 3 || total != 4 || truncated {
		t.Fatalf("range: %v %d %d %d %v %v", lines, first, last, total, truncated, err)
	}
	if lines, first, last, _, _, _ = filePreviewSlice(content, 3, 99); strings.Join(lines, ",") != "three,four" || first != 3 || last != 4 {
		t.Fatalf("clamped end: %v %d-%d", lines, first, last)
	}
	if _, _, _, _, _, err = filePreviewSlice(content, 9, 10); err != errFilePreviewOutOfRange {
		t.Fatalf("range past the end: %v", err)
	}
	long := strings.Repeat("x\n", filePreviewMaxLines+50)
	lines, first, last, total, truncated, _ = filePreviewSlice(long, 0, 0)
	if len(lines) != filePreviewMaxLines || first != 1 || last != filePreviewMaxLines || total != filePreviewMaxLines+50 || !truncated {
		t.Fatalf("long file: %d lines %d-%d of %d truncated=%v", len(lines), first, last, total, truncated)
	}
	lines, _, _, _, truncated, _ = filePreviewSlice(strings.Repeat("é", filePreviewMaxLineLength), 0, 0)
	if !truncated || len(lines[0]) > filePreviewMaxLineLength || !strings.HasSuffix(lines[0], "é") {
		t.Fatalf("long line not cut on a rune boundary: %d bytes truncated=%v", len(lines[0]), truncated)
	}
	if lines, _, _, total, _, err = filePreviewSlice("", 0, 0); len(lines) != 0 || total != 0 || err != nil {
		t.Fatalf("empty file: %v %d %v", lines, total, err)
	}
}

func TestFilePreviewLanguage(t *testing.T) {
	for file, want := range map[string]string{"src/main.go": "go", "a/b.tsx": "typescript", "Dockerfile": "dockerfile", "Makefile": "makefile", "README.md": "markdown", "x.unknown": "", "config.yml": "yaml"} {
		if got := filePreviewLanguage(file); got != want {
			t.Errorf("%s = %q, want %q", file, got, want)
		}
	}
}

// fakeGitHub serves the two GitHub hosts the preview reads from: the REST
// contents API (private files, token required) and raw.githubusercontent.com
// (public files).
type fakeGitHub struct {
	*httptest.Server
	mu       sync.Mutex
	requests []string
	auth     []string
}

const fakeGitHubToken = "ghs_preview_secret_token"

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	fake := &fakeGitHub{}
	fake.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		fake.requests = append(fake.requests, r.URL.RequestURI())
		fake.auth = append(fake.auth, r.Header.Get("Authorization"))
		fake.mu.Unlock()
		path := r.URL.EscapedPath()
		switch {
		case path == "/raw/golang/go/master/README.md":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("# The Go Programming Language\n\nGo is an open source programming language\nline 4\nline 5\n"))
		case path == "/raw/acme/app/feature/login/src/auth.go":
			// raw.githubusercontent.com resolves branch names containing slashes.
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("package auth\n"))
		case path == "/raw/acme/big/main/huge.txt":
			_, _ = w.Write([]byte(strings.Repeat("a", filePreviewMaxBytes+10)))
		case path == "/raw/acme/bin/main/logo.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("\x89PNG\r\n"))
		case path == "/raw/acme/bin/main/blob.dat":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("abc\x00def"))
		case path == "/raw/acme/limited/main/a.txt":
			w.WriteHeader(http.StatusTooManyRequests)
		case path == "/api/repos/acme/private/contents/src/secret.go" && r.URL.Query().Get("ref") == "main":
			if r.Header.Get("Authorization") != "Bearer "+fakeGitHubToken || r.Header.Get("Accept") != "application/vnd.github.raw" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte("package secret\n\nconst answer = 42\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found","token":"` + r.Header.Get("Authorization") + `"}`))
		}
	}))
	t.Cleanup(fake.Close)
	return fake
}

func useFakeGitHub(t *testing.T, fake *fakeGitHub) {
	t.Helper()
	api, raw, client := githubFilePreviewAPIBase, githubFilePreviewRawBase, filePreviewHTTPClient
	githubFilePreviewAPIBase, githubFilePreviewRawBase = fake.URL+"/api", fake.URL+"/raw"
	filePreviewHTTPClient = func() *http.Client {
		return &http.Client{Timeout: 5 * time.Second, CheckRedirect: filePreviewSameHostRedirect}
	}
	filePreviewCache.reset()
	t.Cleanup(func() {
		githubFilePreviewAPIBase, githubFilePreviewRawBase, filePreviewHTTPClient = api, raw, client
		filePreviewCache.reset()
	})
}

func filePreviewRequest(t *testing.T, handler http.Handler, link string) (*httptest.ResponseRecorder, filePreviewResponse, map[string]string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/integrations/file-preview?workspace=test-workspace&url="+url.QueryEscape(link), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var preview filePreviewResponse
	var failure map[string]string
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
			t.Fatalf("decode preview: %v %s", err, rec.Body.String())
		}
	} else {
		_ = json.Unmarshal(rec.Body.Bytes(), &failure)
	}
	return rec, preview, failure
}

func connectFakeGitHub(t *testing.T, s *server, token string) {
	t.Helper()
	err := s.store.MutateWorkspace(context.Background(), "test-workspace", "integration.connected", "github-test", map[string]string{"provider": "github"}, func(data *domain.Bootstrap) error {
		data.IntegrationConnections = append(data.IntegrationConnections, domain.IntegrationConnection{ID: "github-test", Provider: "github", Name: "GitHub", Status: "connected", OAuthAccessToken: token, Config: map[string]string{}, Scopes: []string{}, Channels: []string{}})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFilePreviewPublicGitHubFileWithRange(t *testing.T) {
	_, handler, _ := gitlabTestServer(t)
	fake := newFakeGitHub(t)
	useFakeGitHub(t, fake)

	rec, preview, _ := filePreviewRequest(t, handler, "https://github.com/golang/go/blob/master/README.md#L1-L3")
	if rec.Code != http.StatusOK {
		t.Fatalf("public file: %d %s", rec.Code, rec.Body.String())
	}
	if preview.Provider != "github" || preview.Repo != "golang/go" || preview.Path != "README.md" || preview.Ref != "master" || preview.Language != "markdown" || preview.StartLine != 1 || preview.EndLine != 3 || preview.TotalLines != 5 || preview.Truncated || len(preview.Lines) != 3 || preview.Lines[0] != "# The Go Programming Language" {
		t.Fatalf("preview: %#v", preview)
	}
	if preview.HTMLURL != "https://github.com/golang/go/blob/master/README.md#L1-L3" {
		t.Fatalf("htmlUrl = %s", preview.HTMLURL)
	}
	if !strings.Contains(rec.Header().Get("Cache-Control"), "max-age=60") {
		t.Fatalf("cache header: %q", rec.Header().Get("Cache-Control"))
	}
	// A second request (another range of the same file) is served from the cache.
	before := len(fake.requests)
	if rec, preview, _ = filePreviewRequest(t, handler, "https://github.com/golang/go/blob/master/README.md#L5"); rec.Code != http.StatusOK || len(preview.Lines) != 1 || preview.Lines[0] != "line 5" {
		t.Fatalf("cached range: %d %#v", rec.Code, preview)
	}
	if len(fake.requests) != before {
		t.Fatalf("cached file fetched again: %v", fake.requests)
	}
	if rec, _, failure := filePreviewRequest(t, handler, "https://github.com/golang/go/blob/master/README.md#L40"); rec.Code != http.StatusRequestedRangeNotSatisfiable || failure["code"] != "out_of_range" {
		t.Fatalf("range past the end: %d %v", rec.Code, failure)
	}
	// Branch names with slashes.
	if rec, preview, _ = filePreviewRequest(t, handler, "https://github.com/acme/app/blob/feature/login/src/auth.go"); rec.Code != http.StatusOK || preview.Lines[0] != "package auth" {
		t.Fatalf("slash ref: %d %#v", rec.Code, preview)
	}
}

func TestFilePreviewPrivateRepoUsesIntegrationTokenWithoutLeakingIt(t *testing.T) {
	s, handler, _ := gitlabTestServer(t)
	fake := newFakeGitHub(t)
	useFakeGitHub(t, fake)
	link := "https://github.com/acme/private/blob/main/src/secret.go#L3"

	rec, _, failure := filePreviewRequest(t, handler, link)
	if rec.Code != http.StatusNotFound || failure["code"] != "not_connected" {
		t.Fatalf("private file without GitHub: %d %s", rec.Code, rec.Body.String())
	}
	for _, header := range fake.auth {
		if header != "" {
			t.Fatalf("anonymous fetch sent credentials: %q", header)
		}
	}

	connectFakeGitHub(t, s, fakeGitHubToken)
	filePreviewCache.reset()
	rec, preview, _ := filePreviewRequest(t, handler, link)
	if rec.Code != http.StatusOK || preview.Repo != "acme/private" || preview.Language != "go" || len(preview.Lines) != 1 || preview.Lines[0] != "const answer = 42" || preview.StartLine != 3 {
		t.Fatalf("private file with GitHub: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), fakeGitHubToken) {
		t.Fatal("response leaked the GitHub token")
	}
	// Public files still come through when the token cannot see them, and the
	// anonymous raw host never receives the token.
	filePreviewCache.reset()
	fake.mu.Lock()
	fake.requests, fake.auth = nil, nil
	fake.mu.Unlock()
	if rec, _, _ = filePreviewRequest(t, handler, "https://github.com/golang/go/blob/master/README.md"); rec.Code != http.StatusOK {
		t.Fatalf("public file with GitHub connected: %d %s", rec.Code, rec.Body.String())
	}
	for index, request := range fake.requests {
		if strings.HasPrefix(request, "/raw/") && fake.auth[index] != "" {
			t.Fatalf("raw host received credentials: %s %q", request, fake.auth[index])
		}
	}
	// A file the token cannot read: "no access", with no token in the error.
	rec, _, failure = filePreviewRequest(t, handler, "https://github.com/acme/private/blob/main/src/missing.go")
	if rec.Code != http.StatusNotFound || failure["code"] != "no_access" || strings.Contains(rec.Body.String(), fakeGitHubToken) {
		t.Fatalf("missing file with GitHub: %d %s", rec.Code, rec.Body.String())
	}
}

func TestFilePreviewRejectsLargeBinaryAndRateLimitedFiles(t *testing.T) {
	_, handler, _ := gitlabTestServer(t)
	fake := newFakeGitHub(t)
	useFakeGitHub(t, fake)
	for link, want := range map[string][2]any{
		"https://github.com/acme/big/blob/main/huge.txt":      {http.StatusRequestEntityTooLarge, "too_large"},
		"https://github.com/acme/bin/blob/main/logo.png":      {http.StatusUnsupportedMediaType, "unsupported"},
		"https://github.com/acme/bin/blob/main/blob.dat":      {http.StatusUnsupportedMediaType, "unsupported"},
		"https://github.com/acme/limited/blob/main/a.txt":     {http.StatusTooManyRequests, "rate_limited"},
		"https://example.com/acme/app/blob/main/a.txt":        {http.StatusBadRequest, "invalid_url"},
		"https://github.com/acme/app/blob/main/../../x":       {http.StatusBadRequest, "invalid_url"},
		"https://169.254.169.254/team/project/-/blob/main/a":  {http.StatusNotFound, "not_connected"},
		"https://gitlab.internal/team/project/-/blob/main/a1": {http.StatusNotFound, "not_connected"},
	} {
		before := len(fake.requests)
		rec, _, failure := filePreviewRequest(t, handler, link)
		if rec.Code != want[0].(int) || failure["code"] != want[1].(string) {
			t.Fatalf("%s: %d %s, want %v", link, rec.Code, rec.Body.String(), want)
		}
		if want[1] == "invalid_url" || want[1] == "not_connected" {
			if len(fake.requests) != before {
				t.Fatalf("%s reached the network: %v", link, fake.requests[before:])
			}
		}
	}
}

func TestFilePreviewGitLabUsesConnectedInstanceToken(t *testing.T) {
	enableConnectorSecrets(t)
	_, handler, _ := gitlabTestServer(t)
	fake := newFakeGitLab(t)
	filePreviewCache.reset()
	t.Cleanup(filePreviewCache.reset)
	var seenToken string
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Delegate token checks to the fake GitLab, and serve one file.
		if r.URL.EscapedPath() == "/api/v4/projects/team%2Fproject/repository/files/src%2Fapp.rb/raw" && r.URL.Query().Get("ref") == "main" {
			seenToken = r.Header.Get("PRIVATE-TOKEN")
			if seenToken != "glpat-fake-api" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte("puts 1\nputs 2\nputs 3\n"))
			return
		}
		fake.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(files.Close)
	requestJSON[gitlabConnectResponse](t, handler, http.MethodPut, "/api/integrations/gitlab?workspace=test-workspace", map[string]any{"name": "GitLab", "config": map[string]string{"apiToken": "glpat-fake-api", "host": files.URL}}, http.StatusOK)

	rec, preview, _ := filePreviewRequest(t, handler, files.URL+"/team/project/-/blob/main/src/app.rb#L2-3")
	if rec.Code != http.StatusOK || preview.Provider != "gitlab" || preview.Repo != "team/project" || preview.Language != "ruby" || strings.Join(preview.Lines, "|") != "puts 2|puts 3" || preview.HTMLURL != files.URL+"/team/project/-/blob/main/src/app.rb#L2-3" {
		t.Fatalf("gitlab preview: %d %s", rec.Code, rec.Body.String())
	}
	if seenToken != "glpat-fake-api" || strings.Contains(rec.Body.String(), "glpat") {
		t.Fatalf("token handling: seen=%q body=%s", seenToken, rec.Body.String())
	}
}
