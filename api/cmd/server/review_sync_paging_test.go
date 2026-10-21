package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"flow/api/internal/domain"
)

// gitlabPagingUpstream is a GitLab whose merge request diffs endpoint, like
// some self-managed instances, answers HTTP 500 for per_page above 30.
type gitlabPagingUpstream struct {
	t           *testing.T
	server      *httptest.Server
	mu          sync.Mutex
	diffs       int      // number of changed files served by /diffs
	diffsStatus int      // when set, /diffs always answers this status
	changes     bool     // serve the pre-15.7 /changes endpoint instead of /diffs
	requests    []string // "path?query" of every request
}

func newGitLabPagingUpstream(t *testing.T, diffs int) *gitlabPagingUpstream {
	u := &gitlabPagingUpstream{t: t, diffs: diffs}
	u.server = httptest.NewServer(http.HandlerFunc(u.serve))
	t.Cleanup(u.server.Close)
	return u
}

func (u *gitlabPagingUpstream) serve(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	u.requests = append(u.requests, r.URL.Path+"?"+r.URL.RawQuery)
	diffs, status, changes := u.diffs, u.diffsStatus, u.changes
	u.mu.Unlock()
	if r.Header.Get("PRIVATE-TOKEN") != "glpat-secret" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "401 Unauthorized"})
		return
	}
	base := "/api/v4/projects/group%2Fapp/merge_requests/3"
	switch r.URL.EscapedPath() {
	case base:
		writeJSON(w, http.StatusOK, map[string]any{"title": "T", "state": "opened", "sha": "head1"})
	case base + "/diffs":
		if changes {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "404 Not Found"})
			return
		}
		if status != 0 {
			writeJSON(w, status, map[string]string{"message": "upstream says no"})
			return
		}
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		if perPage == 0 {
			perPage = 20
		}
		if perPage > 30 {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "500 Internal Server Error"})
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		writeJSON(w, http.StatusOK, gitlabPagingDiffs(diffs, page, perPage))
	case base + "/changes":
		writeJSON(w, http.StatusOK, map[string]any{"changes": gitlabPagingDiffs(2, 1, 100)})
	case base + "/discussions":
		writeJSON(w, http.StatusOK, []any{})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "404 Not Found " + r.URL.Path})
	}
}

func gitlabPagingDiffs(total, page, perPage int) []map[string]any {
	out := []map[string]any{}
	for index := (page - 1) * perPage; index < total && index < page*perPage; index++ {
		path := fmt.Sprintf("src/file%03d.go", index)
		out = append(out, map[string]any{"old_path": path, "new_path": path, "diff": "@@ -1 +1 @@\n-a\n+b\n"})
	}
	return out
}

func (u *gitlabPagingUpstream) remote() reviewRemote {
	provider := &reviewProvider{base: u.server.URL + "/api/v4", token: "glpat-secret", provider: domain.CodeHostGitLab, allowLocal: true, client: u.server.Client()}
	return newClassicReviewRemote(provider, domain.CodeReview{RepositoryOwner: "group", RepositoryName: "app"})
}

func (u *gitlabPagingUpstream) queries(suffix string) []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := []string{}
	for _, request := range u.requests {
		if path, query, _ := strings.Cut(request, "?"); strings.HasSuffix(path, suffix) {
			out = append(out, query)
		}
	}
	return out
}

func assertDistinctFiles(t *testing.T, snapshot reviewSnapshot, want int) {
	t.Helper()
	seen := map[string]bool{}
	for _, file := range snapshot.Files {
		if seen[file.Path] {
			t.Fatalf("duplicate file %s", file.Path)
		}
		seen[file.Path] = true
	}
	for index := range want {
		if !seen[fmt.Sprintf("src/file%03d.go", index)] {
			t.Fatalf("missing file %d of %d (got %d)", index, want, len(snapshot.Files))
		}
	}
	if len(snapshot.Files) != want {
		t.Fatalf("files = %d, want %d", len(snapshot.Files), want)
	}
}

var gitlabPagingReview = domain.CodeReview{Provider: domain.CodeHostGitLab, RepositoryOwner: "group", RepositoryName: "app", Number: 3}

func TestFetchGitLabReviewSnapshotPagingSmallMergeRequestSurvivesDiffsPageLimit(t *testing.T) {
	upstream := newGitLabPagingUpstream(t, 3)
	snapshot, err := fetchGitLabReviewSnapshot(t.Context(), upstream.remote(), gitlabPagingReview)
	if err != nil {
		t.Fatalf("a tiny merge request must sync: %v", err)
	}
	assertDistinctFiles(t, snapshot, 3)
	for _, query := range upstream.queries("/diffs") {
		perPage, _ := strconv.Atoi(mustQueryValue(t, query, "per_page"))
		if perPage < 1 || perPage > 30 {
			t.Fatalf("diffs requested with per_page=%d (query %q)", perPage, query)
		}
	}
}

func originalPaging(raw string) bool {
	values, err := url.ParseQuery(raw)
	return err == nil && values.Get("per_page") == "100" && values.Get("page") == "1" && len(values) == 2
}

func mustQueryValue(t *testing.T, rawQuery, key string) string {
	t.Helper()
	for _, pair := range strings.Split(rawQuery, "&") {
		if name, value, _ := strings.Cut(pair, "="); name == key {
			return value
		}
	}
	t.Fatalf("query %q has no %s", rawQuery, key)
	return ""
}

func TestFetchGitLabReviewSnapshotPagingReadsEveryDiffPageOnce(t *testing.T) {
	for _, total := range []int{75, 60, 30, 20, 0} {
		t.Run(fmt.Sprintf("%d files", total), func(t *testing.T) {
			upstream := newGitLabPagingUpstream(t, total)
			snapshot, err := fetchGitLabReviewSnapshot(t.Context(), upstream.remote(), gitlabPagingReview)
			if err != nil {
				t.Fatal(err)
			}
			assertDistinctFiles(t, snapshot, total)
			seenPages := map[string]bool{}
			for _, query := range upstream.queries("/diffs") {
				page := mustQueryValue(t, query, "page")
				if seenPages[page] {
					t.Fatalf("page %s requested twice: %v", page, upstream.queries("/diffs"))
				}
				seenPages[page] = true
			}
			if len(seenPages) > total/20+2 {
				t.Fatalf("paging did not stop: %v", upstream.queries("/diffs"))
			}
		})
	}
}

func TestFetchGitLabReviewSnapshotPagingBudgetNeverReportsATruncatedListAsComplete(t *testing.T) {
	// Far more files than the page budget can read.
	upstream := newGitLabPagingUpstream(t, 100000)
	snapshot, err := fetchGitLabReviewSnapshot(t.Context(), upstream.remote(), gitlabPagingReview)
	if err == nil {
		t.Fatalf("a truncated diff list was reported as complete with %d files (Complete=%v)", len(snapshot.Files), snapshot.Complete)
	}
	if !strings.Contains(err.Error(), "merge request diffs") {
		t.Fatalf("error should name the diffs: %v", err)
	}
	if got := len(upstream.queries("/diffs")); got < 2 || got > 200 {
		t.Fatalf("diff pages requested = %d", got)
	}
}

func TestFetchGitLabReviewSnapshotPagingKeepsOlderGitLabChangesFallback(t *testing.T) {
	upstream := newGitLabPagingUpstream(t, 0)
	upstream.changes = true
	snapshot, err := fetchGitLabReviewSnapshot(t.Context(), upstream.remote(), gitlabPagingReview)
	if err != nil {
		t.Fatal(err)
	}
	assertDistinctFiles(t, snapshot, 2)
	if len(upstream.queries("/changes")) != 1 {
		t.Fatalf("changes requests = %v", upstream.queries("/changes"))
	}
}

func TestFetchGitLabReviewSnapshotPagingSurfacesUpstreamFailures(t *testing.T) {
	upstream := newGitLabPagingUpstream(t, 5)
	upstream.diffsStatus = http.StatusInternalServerError
	snapshot, err := fetchGitLabReviewSnapshot(t.Context(), upstream.remote(), gitlabPagingReview)
	if err == nil || len(snapshot.Files) != 0 {
		t.Fatalf("a failing diffs endpoint must fail the sync, got files=%d err=%v", len(snapshot.Files), err)
	}
	if reviewRemoteStatus(err) != http.StatusInternalServerError || !strings.Contains(err.Error(), "merge request diffs") || !strings.Contains(err.Error(), "upstream says no") {
		t.Fatalf("error lost its upstream reason: %v", err)
	}
	if strings.Contains(err.Error(), "glpat-secret") {
		t.Fatalf("error leaks the credential: %v", err)
	}

	// An auth error is propagated as-is and never read as an empty file list.
	provider := &reviewProvider{base: upstream.server.URL + "/api/v4", token: "glpat-wrong", provider: domain.CodeHostGitLab, allowLocal: true, client: upstream.server.Client()}
	remote := newClassicReviewRemote(provider, domain.CodeReview{RepositoryOwner: "group", RepositoryName: "app"})
	_, err = fetchGitLabReviewSnapshot(t.Context(), remote, gitlabPagingReview)
	if reviewRemoteStatus(err) != http.StatusUnauthorized || strings.Contains(err.Error(), "glpat-wrong") {
		t.Fatalf("auth error = %v", err)
	}
}

func TestFetchGitLabReviewSnapshotPagingKeepsOtherEndpointsPageSize(t *testing.T) {
	upstream := newGitLabPagingUpstream(t, 1)
	if _, err := fetchGitLabReviewSnapshot(t.Context(), upstream.remote(), gitlabPagingReview); err != nil {
		t.Fatal(err)
	}
	if got := upstream.queries("/discussions"); len(got) != 1 || !originalPaging(got[0]) {
		t.Fatalf("discussions queries = %v", got)
	}

	var mu sync.Mutex
	githubQueries := map[string][]string{}
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		githubQueries[r.URL.Path] = append(githubQueries[r.URL.Path], r.URL.RawQuery)
		mu.Unlock()
		if r.URL.Path == "/repos/o/r/pulls/4" {
			writeJSON(w, http.StatusOK, map[string]any{"title": "T", "state": "open"})
			return
		}
		writeJSON(w, http.StatusOK, []any{})
	}))
	defer github.Close()
	provider := &reviewProvider{base: github.URL, token: "ghp-test", provider: domain.CodeHostGitHub, allowLocal: true, client: github.Client()}
	remote := newClassicReviewRemote(provider, domain.CodeReview{RepositoryOwner: "o", RepositoryName: "r"})
	if _, err := fetchGitHubReviewSnapshot(t.Context(), remote, domain.CodeReview{Provider: domain.CodeHostGitHub, RepositoryOwner: "o", RepositoryName: "r", Number: 4}); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"/pulls/4/files", "/pulls/4/comments", "/issues/4/comments", "/pulls/4/reviews"} {
		if got := githubQueries["/repos/o/r"+suffix]; len(got) != 1 || !originalPaging(got[0]) {
			t.Fatalf("github %s queries = %v", suffix, got)
		}
	}
}

func TestFetchGitLabReviewSnapshotPagingPreservesExistingQuery(t *testing.T) {
	var mu sync.Mutex
	seen := []string{}
	pages := map[string]int{"1": 100, "2": 100, "3": 5}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.RawQuery)
		mu.Unlock()
		items := make([]map[string]any, pages[r.URL.Query().Get("page")])
		for index := range items {
			items[index] = map[string]any{"id": index}
		}
		writeJSON(w, http.StatusOK, items)
	}))
	defer upstream.Close()
	provider := &reviewProvider{base: upstream.URL, token: "t", provider: domain.CodeHostGitLab, allowLocal: true, client: upstream.Client()}
	remote := newClassicReviewRemote(provider, domain.CodeReview{RepositoryOwner: "group", RepositoryName: "app"})
	items, err := fetchPages[map[string]any](t.Context(), remote, "/things?state=opened&scope=a%26b&page=9&per_page=7", 10)
	if err != nil || len(items) != 205 {
		t.Fatalf("items=%d err=%v", len(items), err)
	}
	if len(seen) != 3 {
		t.Fatalf("requests = %v", seen)
	}
	for index, raw := range seen {
		values, err := url.ParseQuery(raw)
		if err != nil || values.Get("state") != "opened" || values.Get("scope") != "a&b" || values.Get("per_page") != "100" || values.Get("page") != strconv.Itoa(index+1) || len(values["page"]) != 1 || len(values["per_page"]) != 1 {
			t.Fatalf("query %q = %v (%v)", raw, values, err)
		}
	}
}
