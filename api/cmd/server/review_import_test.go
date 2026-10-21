package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// fakeImportHost is a GitHub (classic token) or GitLab code host with
// mutable pull/merge requests, for the review import.
type fakeImportHost struct {
	t        *testing.T
	gitlab   bool
	server   *httptest.Server
	mu       sync.Mutex
	pulls    map[int]map[string]any // by number
	paths    []string               // GET request URIs
	headers  map[string]string      // added to every answer
	limited  string                 // "METHOD path" answered with a rate limit
	failing  map[string]int         // path suffix answered with a status
	repos    []map[string]any
	notFound map[int]bool
	// listing, when set, is called (unlocked) before a request list is answered.
	listing func()
}

func newFakeImportHost(t *testing.T, gitlab bool) *fakeImportHost {
	f := &fakeImportHost{t: t, gitlab: gitlab, pulls: map[int]map[string]any{}, headers: map[string]string{}, notFound: map[int]bool{}, failing: map[string]int{}}
	// A wide token: hundreds of repositories the import must never enumerate.
	for index := range 300 {
		if gitlab {
			f.repos = append(f.repos, map[string]any{"path_with_namespace": fmt.Sprintf("wide/project-%d", index)})
		} else {
			f.repos = append(f.repos, map[string]any{"full_name": fmt.Sprintf("acme/repo-%d", index)})
		}
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

// open adds (or reopens) a request with the code host's list shape.
func (f *fakeImportHost) open(number int, title, branch string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339)
	if f.gitlab {
		f.pulls[number] = map[string]any{"id": 5000 + number, "iid": number, "title": title, "description": "", "state": "opened", "web_url": fmt.Sprintf("https://gitlab.intranet.example/group/sub/app/-/merge_requests/%d", number),
			"source_branch": branch, "target_branch": "main", "sha": "sha-" + branch, "author": map[string]any{"username": "alice", "name": "Alice"}, "reviewers": []any{}, "created_at": now, "updated_at": now}
		return
	}
	f.pulls[number] = map[string]any{"id": 9000 + number, "number": number, "title": title, "body": "Body " + title, "state": "open", "html_url": fmt.Sprintf("https://github.com/acme/store/pull/%d", number),
		"user": map[string]any{"login": "alice"}, "base": map[string]any{"ref": "main"}, "head": map[string]any{"ref": branch, "sha": "sha-" + branch}, "requested_reviewers": []any{}, "created_at": now, "updated_at": now}
}

// push moves a request's head to a new commit.
func (f *fakeImportHost) push(number int, sha string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pull := f.pulls[number]
	if f.gitlab {
		pull["sha"] = sha
	} else {
		pull["head"] = map[string]any{"ref": pull["head"].(map[string]any)["ref"], "sha": sha}
	}
	pull["updated_at"] = time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
}

// finish closes (or merges) a request.
func (f *fakeImportHost) finish(number int, merged bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pull := f.pulls[number]
	switch {
	case f.gitlab && merged:
		pull["state"] = "merged"
	case f.gitlab:
		pull["state"] = "closed"
	default:
		pull["state"], pull["merged"] = "closed", merged
		if merged {
			pull["merged_at"] = time.Now().UTC().Format(time.RFC3339)
		}
	}
}

func (f *fakeImportHost) requested(fragment string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, path := range f.paths {
		if strings.Contains(path, fragment) {
			count++
		}
	}
	return count
}

// requestedPath counts requests of exactly path (no query).
func (f *fakeImportHost) requestedPath(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, item := range f.paths {
		if before, _, _ := strings.Cut(item, "?"); before == path {
			count++
		}
	}
	return count
}

func (f *fakeImportHost) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.paths)
}

func (f *fakeImportHost) serve(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("Authorization") == "Bearer local-test-token"
	if f.gitlab {
		token = r.Header.Get("PRIVATE-TOKEN") == "glpat-test"
	}
	if !token {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "Bad credentials"})
		return
	}
	repo, list, single := "/repos/acme/store", "/pulls", "/pulls/"
	if f.gitlab {
		repo, list, single = "/api/v4/projects/group%2Fsub%2Fapp", "/merge_requests", "/merge_requests/"
	}
	path := r.URL.EscapedPath()
	f.mu.Lock()
	if r.Method == http.MethodGet {
		f.paths = append(f.paths, r.URL.RequestURI())
	}
	listing := f.listing
	f.mu.Unlock()
	if listing != nil && path == repo+list {
		listing()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for key, value := range f.headers {
		w.Header().Set(key, value)
	}
	if f.limited == r.Method+" "+path {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(30*time.Minute).Unix(), 10))
		writeJSON(w, http.StatusForbidden, map[string]string{"message": "API rate limit exceeded"})
		return
	}
	for suffix, status := range f.failing {
		if strings.HasSuffix(path, suffix) {
			writeJSON(w, status, map[string]string{"message": "Internal Server Error"})
			return
		}
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	paged := func(items []map[string]any) {
		if perPage == 0 {
			perPage = 30
		}
		start := min(len(items), max(page-1, 0)*perPage)
		writeJSON(w, http.StatusOK, items[start:min(len(items), start+perPage)])
	}
	numbers := []int{}
	for number := range f.pulls {
		numbers = append(numbers, number)
	}
	slices.Sort(numbers)
	open := []map[string]any{}
	for _, number := range numbers {
		if state := f.pulls[number]["state"]; state == "open" || state == "opened" {
			open = append(open, f.pulls[number])
		}
	}
	switch {
	case !f.gitlab && path == "/user/repos", f.gitlab && path == "/api/v4/projects":
		paged(f.repos)
	case path == repo+list && r.Method == http.MethodGet:
		if state := r.URL.Query().Get("state"); state != "open" && state != "opened" {
			f.t.Errorf("listed requests in state %q", state)
		}
		paged(open)
	case strings.HasPrefix(path, repo+single) && !strings.Contains(strings.TrimPrefix(path, repo+single), "/"):
		number, _ := strconv.Atoi(strings.TrimPrefix(path, repo+single))
		pull, ok := f.pulls[number]
		if !ok || f.notFound[number] {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
			return
		}
		writeJSON(w, http.StatusOK, pull)
	case strings.HasPrefix(path, repo+single) && r.Method == http.MethodGet:
		// Files, comments, reviews, diffs and discussions of the full sync.
		writeJSON(w, http.StatusOK, []any{})
	case strings.HasPrefix(path, repo+"/issues/"):
		writeJSON(w, http.StatusOK, []any{})
	case path == "/graphql":
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{"reviewThreads": map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": []any{}}}}}})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found " + r.Method + " " + path})
	}
}

// importRecorder counts the review import's workspace writes and realtime events.
type importRecorder struct {
	mu     sync.Mutex
	events []string
}

func (r *importRecorder) sink(workspace string, event domain.RealtimeEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event.Type)
}

func (r *importRecorder) count(types ...string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, item := range r.events {
		if len(types) == 0 || slices.Contains(types, item) {
			count++
		}
	}
	return count
}

func (r *importRecorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = nil
}

// newReviewImportServer is a workspace with a classic connection to fake,
// with the import allowed on the server (FLOW_REVIEW_IMPORT=on) but not yet
// turned on for the workspace.
func newReviewImportServer(t *testing.T, fake *fakeImportHost, config map[string]string) (http.Handler, *server, *store.SQLiteStore) {
	t.Helper()
	t.Setenv("FLOW_REVIEW_IMPORT", "on")
	t.Setenv("FLOW_REVIEW_IMPORT_INTERVAL", "")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	connection := domain.IntegrationConnection{ID: "github-import", Provider: "github", Name: "GitHub", Status: "connected", OAuthAccessToken: "local-test-token",
		Config: map[string]string{"apiUrl": fake.server.URL, "organization": "acme"}, Scopes: []string{}, Channels: []string{}}
	if fake.gitlab {
		connection = domain.IntegrationConnection{ID: "gitlab-import", Provider: "gitlab", Name: "GitLab", Status: "connected", OAuthAccessToken: "glpat-test",
			Config: map[string]string{"apiUrl": fake.server.URL + "/api/v4"}, Scopes: []string{}, Channels: []string{}}
	}
	for key, value := range config {
		connection.Config[key] = value
	}
	if err := repository.MutateWorkspace(t.Context(), "test-workspace", "review.import_fixture", "", nil, func(data *domain.Bootstrap) error {
		data.IntegrationConnections = append(data.IntegrationConnections, connection)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	limits := defaultReviewImportLimits
	limits.Writes, limits.WriteDelay = 200, 0
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true, reviewImportLimits: &limits}
	return newHandler(s), s, repository
}

// approveImport turns the import on for the workspace with these repositories.
func approveImport(t *testing.T, handler http.Handler, provider string, repositories ...string) reviewImportStatus {
	t.Helper()
	items := []reviewImportRepository{}
	for _, name := range repositories {
		items = append(items, reviewImportRepository{Provider: provider, FullName: name})
	}
	return requestJSON[reviewImportStatus](t, handler, http.MethodPatch, "/api/reviews/import", map[string]any{"enabled": true, "repositories": items}, http.StatusOK)
}

func importedReviews(repository *store.SQLiteStore, provider string) map[int][]domain.CodeReview {
	result := map[int][]domain.CodeReview{}
	for _, review := range repository.Bootstrap().Reviews {
		if review.Provider == provider && review.RepositoryOwner != "example" {
			result[review.Number] = append(result[review.Number], review)
		}
	}
	return result
}

func TestReviewImportCreatesOpenPullRequestsAndSkipsUnchangedOnes(t *testing.T) {
	fake := newFakeImportHost(t, false)
	for number := 1; number <= 40; number++ {
		fake.open(number, fmt.Sprintf("Change %d", number), fmt.Sprintf("change-%d", number))
	}
	fake.open(41, "TST-1 Fix the checkout", "tst-1-fix")
	handler, _, repository := newReviewImportServer(t, fake, nil)
	recorder := &importRecorder{}
	repository.SetRealtimeSink(recorder.sink)
	approveImport(t, handler, "github", "acme/store")

	result := requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	if result.Repositories != 1 || result.Imported != 41 || result.Updated != 0 || result.Synced != 41 || len(result.Errors) != 0 || result.Trigger != "manual" {
		t.Fatalf("first import = %+v", result)
	}
	if fake.requested("/user/repos") != 0 || fake.requested("acme/repo-") != 0 {
		t.Fatalf("the import enumerated the token's repositories: %v", fake.paths)
	}
	// One coalesced write and realtime event for the whole run, then one per synced review.
	if got := recorder.count("review.imported"); got != 1 {
		t.Fatalf("review.imported events = %d, want 1", got)
	}
	if got := recorder.count("review.synced"); got != 41 {
		t.Fatalf("review.synced events = %d, want 41", got)
	}
	reviews := importedReviews(repository, "github")
	if len(reviews) != 41 {
		t.Fatalf("imported reviews = %d", len(reviews))
	}
	linked := reviews[41][0]
	if linked.ExternalID != "9041" || linked.RepositoryOwner != "acme" || linked.RepositoryName != "store" || linked.Status != "open" || linked.HeadBranch != "tst-1-fix" ||
		linked.URL != "https://github.com/acme/store/pull/41" || linked.Author.ID != "github:alice" || linked.Description != "Body TST-1 Fix the checkout" || linked.SyncedAt == nil {
		t.Fatalf("imported review = %+v", linked)
	}
	issue := repository.Bootstrap().Issues[slices.IndexFunc(repository.Bootstrap().Issues, func(issue domain.Issue) bool { return issue.Identifier == "TST-1" })]
	if !slices.Contains(linked.IssueIDs, issue.ID) {
		t.Fatalf("issue not linked from the title: %v", linked.IssueIDs)
	}

	// Nothing changed on the code host: no workspace write, no realtime
	// event, no full sync.
	recorder.reset()
	events, err := repository.Events(t.Context(), "review-import")
	if err != nil {
		t.Fatal(err)
	}
	files := fake.requested("/files")
	again := requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	if again.Imported != 0 || again.Updated != 0 || again.Closed != 0 || again.Synced != 0 || again.Writes != 0 || again.SkippedUnchanged != 41 || again.Scanned != 41 {
		t.Fatalf("unchanged re-import = %+v", again)
	}
	if got := recorder.count(); got != 0 {
		t.Fatalf("an unchanged import published %d realtime events: %v", got, recorder.events)
	}
	if after, _ := repository.Events(t.Context(), "review-import"); len(after) != len(events) {
		t.Fatalf("an unchanged import wrote review.imported")
	}
	if fake.requested("/files") != files {
		t.Fatal("an unchanged pull request was synced again")
	}
	for number, items := range importedReviews(repository, "github") {
		if len(items) != 1 {
			t.Fatalf("PR #%d has %d reviews", number, len(items))
		}
	}
	status := requestJSON[reviewImportStatus](t, handler, http.MethodGet, "/api/reviews/import", nil, http.StatusOK)
	if status.Last == nil || status.Last.SkippedUnchanged != 41 || status.Running || !status.Active || !status.Available || !status.Enabled || status.NextRunAt == nil ||
		len(status.Repositories) != 1 || status.Repositories[0].FullName != "acme/store" {
		t.Fatalf("status = %+v", status)
	}

	// A new head commit: that review only is updated and synced again.
	fake.push(7, "sha-new")
	pushed := requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	if pushed.Synced != 1 || pushed.Updated != 0 || pushed.Writes != 1 || fake.requested("/pulls/7/files") != 2 || fake.requested("/pulls/8/files") != 1 {
		t.Fatalf("pushed = %+v", pushed)
	}
}

func TestReviewImportUpdatesClosedAndMergedPullRequests(t *testing.T) {
	fake := newFakeImportHost(t, false)
	for number := 1; number <= 4; number++ {
		fake.open(number, fmt.Sprintf("Change %d", number), fmt.Sprintf("change-%d", number))
	}
	handler, _, repository := newReviewImportServer(t, fake, nil)
	approveImport(t, handler, "github", "acme/store")
	requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)

	fake.finish(1, true)
	fake.finish(2, false)
	fake.mu.Lock()
	fake.pulls[3]["title"] = "Change 3, renamed"
	fake.pulls[3]["draft"] = true
	fake.pulls[3]["updated_at"] = time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
	fake.mu.Unlock()
	result := requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	if result.Imported != 0 || result.Closed != 2 || result.Updated != 3 || result.SkippedUnchanged != 1 {
		t.Fatalf("second import = %+v", result)
	}
	reviews := importedReviews(repository, "github")
	if merged := reviews[1][0]; merged.Status != "merged" || merged.MergedAt == nil || merged.ClosedAt != nil {
		t.Fatalf("merged = %+v", merged)
	}
	if closed := reviews[2][0]; closed.Status != "closed" || closed.ClosedAt == nil {
		t.Fatalf("closed = %+v", closed)
	}
	if renamed := reviews[3][0]; renamed.Title != "Change 3, renamed" || !renamed.Draft || renamed.Status != "open" {
		t.Fatalf("edited = %+v", renamed)
	}
	// Closed requests are read once; a reopened one is open again.
	fake.open(2, "Change 2", "change-2")
	mergedReads := fake.requestedPath("/repos/acme/store/pulls/1")
	result = requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	if result.Updated != 1 || importedReviews(repository, "github")[2][0].Status != "open" || fake.requestedPath("/repos/acme/store/pulls/1") != mergedReads {
		t.Fatalf("reopen = %+v; requests %v", result, fake.paths)
	}
}

func TestReviewImportAndWebhooksShareOneReview(t *testing.T) {
	fake := newFakeImportHost(t, false)
	fake.open(1, "Change 1", "change-1")
	handler, _, repository := newReviewImportServer(t, fake, map[string]string{"webhookSecret": "secret"})
	approveImport(t, handler, "github", "acme/store")
	opened := func(number int, delivery string) {
		fake.mu.Lock()
		pull := fake.pulls[number]
		fake.mu.Unlock()
		signedGitHubWebhook(t, handler, "pull_request", delivery, map[string]any{"action": "opened", "number": number, "repository": map[string]any{"full_name": "acme/store"},
			"sender": map[string]any{"login": "alice"}, "pull_request": pull})
	}
	// Webhook first, then the import.
	opened(1, "d-1")
	result := requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	if result.Imported != 0 || len(importedReviews(repository, "github")[1]) != 1 {
		t.Fatalf("import duplicated a webhook review: %+v", result)
	}
	// Import first, then the webhook.
	fake.open(2, "Change 2", "change-2")
	requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	opened(2, "d-2")
	if items := importedReviews(repository, "github")[2]; len(items) != 1 {
		t.Fatalf("webhook duplicated an imported review: %d", len(items))
	}
	// Both at once.
	for number := 3; number <= 8; number++ {
		fake.open(number, fmt.Sprintf("Change %d", number), fmt.Sprintf("change-%d", number))
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/reviews/import", nil))
			if rec.Code != http.StatusOK && rec.Code != http.StatusConflict {
				t.Errorf("import: %d %s", rec.Code, rec.Body.String())
			}
		}()
		go func() {
			defer wg.Done()
			opened(number, fmt.Sprintf("d-%d", number))
		}()
		wg.Wait()
	}
	requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	reviews := importedReviews(repository, "github")
	for number := 1; number <= 8; number++ {
		if len(reviews[number]) != 1 {
			t.Fatalf("PR #%d has %d reviews", number, len(reviews[number]))
		}
	}
}

func TestReviewImportReadsOnlyApprovedRepositoriesOfAWideConnection(t *testing.T) {
	// GitLab without an organization filter, a token reaching 300 projects.
	fake := newFakeImportHost(t, true)
	fake.open(3, "Rename math", "rename")
	handler, _, repository := newReviewImportServer(t, fake, nil)

	// Turned on with nothing approved: nothing is read or imported.
	status := requestJSON[reviewImportStatus](t, handler, http.MethodPatch, "/api/reviews/import", map[string]any{"enabled": true}, http.StatusOK)
	if !status.Active || len(status.Repositories) != 0 {
		t.Fatalf("status = %+v", status)
	}
	result := requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	if result.Repositories != 0 || result.Imported != 0 || fake.total() != 0 || len(importedReviews(repository, "gitlab")) != 0 {
		t.Fatalf("an empty approval imported: %+v %v", result, fake.paths)
	}

	// One approved project: only it is read.
	approveImport(t, handler, "gitlab", "https://gitlab.intranet.example/group/sub/app/-/merge_requests")
	fake.open(4, "Add docs", "docs")
	fake.open(5, "Experiment", "experiment")
	result = requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	if result.Repositories != 1 || result.Imported != 3 || len(result.Errors) != 0 {
		t.Fatalf("gitlab import = %+v (%v)", result, fake.paths)
	}
	for _, path := range fake.paths {
		if !strings.HasPrefix(path, "/api/v4/projects/group%2Fsub%2Fapp/") {
			t.Fatalf("read outside the approved project: %s", path)
		}
	}
	review := importedReviews(repository, "gitlab")[3][0]
	if review.RepositoryOwner != "group" || review.RepositoryName != "sub/app" || review.ExternalID != "5003" || review.BaseBranch != "main" || review.Author.ID != "gitlab:alice" {
		t.Fatalf("gitlab review = %+v", review)
	}
	fake.finish(3, true)
	fake.finish(4, false)
	fake.mu.Lock()
	delete(fake.pulls, 5) // deleted on GitLab
	fake.mu.Unlock()
	result = requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	reviews := importedReviews(repository, "gitlab")
	if result.Closed != 3 || reviews[3][0].Status != "merged" || reviews[4][0].Status != "closed" || reviews[5][0].Status != "closed" {
		t.Fatalf("gitlab states = %+v: %s %s %s", result, reviews[3][0].Status, reviews[4][0].Status, reviews[5][0].Status)
	}

	// Approvals are validated.
	requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/reviews/import", map[string]any{"repositories": []map[string]string{{"provider": "gitlab", "repository": "no-slash"}}}, http.StatusBadRequest)
	requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/reviews/import", map[string]any{"repositories": []map[string]string{{"provider": "github", "repository": "acme/store"}}}, http.StatusBadRequest)
}

func TestReviewImportBacksOffAFailingMergeRequestAndContinues(t *testing.T) {
	fake := newFakeImportHost(t, true)
	for number := 1; number <= 3; number++ {
		fake.open(number, fmt.Sprintf("Change %d", number), fmt.Sprintf("change-%d", number))
	}
	fake.failing["/merge_requests/2/diffs"] = http.StatusInternalServerError
	handler, s, repository := newReviewImportServer(t, fake, nil)
	approveImport(t, handler, "gitlab", "group/sub/app")

	result := requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	if result.Imported != 3 || result.Synced != 2 || result.Failed != 1 || len(result.Failures) != 1 || result.Failures[0].BackoffUntil == nil ||
		time.Until(*result.Failures[0].BackoffUntil) < 10*time.Minute || !strings.Contains(result.Failures[0].Target, "#2") {
		t.Fatalf("first import = %+v", result)
	}
	reviews := importedReviews(repository, "gitlab")
	if reviews[1][0].SyncedAt == nil || reviews[3][0].SyncedAt == nil || reviews[3][0].SyncError != "" {
		t.Fatal("one failing merge request blocked the others")
	}
	status := requestJSON[reviewImportStatus](t, handler, http.MethodGet, "/api/reviews/import", nil, http.StatusOK)
	if len(status.Backoffs) != 1 || status.Backoffs[0].BackoffUntil == nil || !strings.Contains(status.Backoffs[0].Message, "500") {
		t.Fatalf("backoff not reported: %+v", status.Backoffs)
	}
	diffs := fake.requested("/merge_requests/2/diffs")

	// Not retried before the backoff expires, by Sync now or the scheduler.
	again := requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	if fake.requested("/merge_requests/2/diffs") != diffs || again.BackedOff != 1 || again.Failed != 0 || again.Writes != 0 {
		t.Fatalf("retried during backoff: %+v", again)
	}
	s.runReviewImportTick(t.Context(), time.Now().Add(time.Hour))
	if fake.requested("/merge_requests/2/diffs") != diffs {
		t.Fatal("the scheduler retried during backoff")
	}

	// Once it expires, it is retried and the next backoff is longer.
	items, err := repository.ReviewImportItems(t.Context(), "test-workspace", "gitlab:group/sub/app")
	if err != nil {
		t.Fatal(err)
	}
	item := items["gitlab:group/sub/app#2"]
	item.BackoffUntil = time.Now().Add(-time.Second)
	if err := repository.SaveReviewImportItem(t.Context(), "test-workspace", item); err != nil {
		t.Fatal(err)
	}
	retried := requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	if fake.requested("/merge_requests/2/diffs") == diffs || retried.Failed != 1 || retried.Failures[0].Failures != 2 || time.Until(*retried.Failures[0].BackoffUntil) < 25*time.Minute {
		t.Fatalf("retry = %+v", retried)
	}
	// Recovered: synced, backoff cleared.
	delete(fake.failing, "/merge_requests/2/diffs")
	items, _ = repository.ReviewImportItems(t.Context(), "test-workspace", "gitlab:group/sub/app")
	item = items["gitlab:group/sub/app#2"]
	item.BackoffUntil = time.Now().Add(-time.Second)
	_ = repository.SaveReviewImportItem(t.Context(), "test-workspace", item)
	recovered := requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	status = requestJSON[reviewImportStatus](t, handler, http.MethodGet, "/api/reviews/import", nil, http.StatusOK)
	if recovered.Synced != 1 || len(status.Backoffs) != 0 {
		t.Fatalf("recovery = %+v, backoffs %+v", recovered, status.Backoffs)
	}
}

func TestReviewImportRunsOnOneReplicaAtATime(t *testing.T) {
	fake := newFakeImportHost(t, false)
	fake.open(1, "Change 1", "change-1")
	handler, first, repository := newReviewImportServer(t, fake, nil)
	approveImport(t, handler, "github", "acme/store")
	// A second replica on the same database.
	second := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true, reviewImportLimits: first.reviewImportLimits}

	entered, release := make(chan struct{}), make(chan struct{})
	var once, released sync.Once
	t.Cleanup(func() { released.Do(func() { close(release) }) })
	fake.listing = func() {
		once.Do(func() {
			close(entered)
			<-release
		})
	}
	done := make(chan error, 1)
	go func() {
		_, err := first.runReviewImport(t.Context(), "test-workspace", "schedule")
		done <- err
	}()
	<-entered
	if _, err := second.runReviewImport(t.Context(), "test-workspace", "manual"); !errors.Is(err, errReviewImportRunning) {
		t.Fatalf("second replica ran while the first held the lease: %v", err)
	}
	second.runReviewImportTick(t.Context(), time.Now().Add(time.Hour))
	if lists := fake.requestedPath("/repos/acme/store/pulls"); lists != 1 {
		t.Fatalf("lists = %d", lists)
	}
	status := requestJSON[reviewImportStatus](t, newHandler(second), http.MethodGet, "/api/reviews/import", nil, http.StatusOK)
	if !status.Running {
		t.Fatal("status does not show the running import")
	}
	released.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(importedReviews(repository, "github")[1]) != 1 {
		t.Fatal("the first replica's run did not finish")
	}
	// The lease is released: the second replica runs next.
	if _, err := second.runReviewImport(t.Context(), "test-workspace", "manual"); err != nil {
		t.Fatal(err)
	}
}

func TestReviewImportBoundsARunAndResumesAtTheCursor(t *testing.T) {
	fake := newFakeImportHost(t, false)
	fake.open(1, "Change 1", "change-1")
	handler, s, _ := newReviewImportServer(t, fake, nil)
	limits := *s.reviewImportLimits
	limits.Repositories = 2
	s.reviewImportLimits = &limits
	// Five approved repositories: the fake has only acme/store, so the
	// others fail (and back off) — each run reads two.
	approveImport(t, handler, "github", "acme/store", "acme/a", "acme/b", "acme/c", "acme/d")
	first := requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	if !first.Partial || fake.requestedPath("/repos/acme/store/pulls") != 1 || fake.requestedPath("/repos/acme/a/pulls") != 1 || fake.requestedPath("/repos/acme/b/pulls") != 0 {
		t.Fatalf("first run = %+v %v", first, fake.paths)
	}
	second := requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	if !second.Partial || fake.requestedPath("/repos/acme/b/pulls") != 1 || fake.requestedPath("/repos/acme/c/pulls") != 1 || fake.requestedPath("/repos/acme/store/pulls") != 1 {
		t.Fatalf("second run did not resume at the cursor: %+v %v", second, fake.paths)
	}
}

func TestReviewImportStopsAtTheRateLimit(t *testing.T) {
	fake := newFakeImportHost(t, false)
	fake.open(1, "Change 1", "change-1")
	fake.headers["X-RateLimit-Remaining"] = "12"
	fake.headers["X-RateLimit-Reset"] = strconv.FormatInt(time.Now().Add(20*time.Minute).Unix(), 10)
	handler, _, _ := newReviewImportServer(t, fake, nil)
	approveImport(t, handler, "github", "acme/store")
	result := requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	if result.RateLimitedUntil == nil || time.Until(*result.RateLimitedUntil) < 15*time.Minute || fake.requested("/files") != 0 {
		t.Fatalf("a nearly spent budget must stop the run before the full syncs: %+v %v", result, fake.paths)
	}
	// Sync now waits until the reset; so does the schedule.
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusTooManyRequests)
	status := requestJSON[reviewImportStatus](t, handler, http.MethodGet, "/api/reviews/import", nil, http.StatusOK)
	if status.NextRunAt == nil || time.Until(*status.NextRunAt) < 15*time.Minute || status.RateLimitedUntil == nil {
		t.Fatalf("status = %+v", status)
	}

	fake = newFakeImportHost(t, false)
	fake.open(1, "Change 1", "change-1")
	fake.limited = "GET /repos/acme/store/pulls"
	handler, _, _ = newReviewImportServer(t, fake, nil)
	approveImport(t, handler, "github", "acme/store")
	result = requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	if result.RateLimitedUntil == nil || result.Imported != 0 {
		t.Fatalf("a rate limited answer must stop the run: %+v", result)
	}
}

func TestReviewImportSchedulerRunsDueWorkspaces(t *testing.T) {
	fake := newFakeImportHost(t, false)
	fake.open(1, "Change 1", "change-1")
	handler, s, repository := newReviewImportServer(t, fake, nil)
	now := time.Now()
	s.runReviewImportTick(t.Context(), now)
	if fake.total() != 0 {
		t.Fatal("the scheduler ran for a workspace that did not turn the import on")
	}
	approveImport(t, handler, "github", "acme/store")
	s.runReviewImportTick(t.Context(), time.Now())
	if len(importedReviews(repository, "github")) != 1 {
		t.Fatal("the background import did not run for an enabled workspace")
	}
	lists := fake.requestedPath("/repos/acme/store/pulls")
	s.runReviewImportTick(t.Context(), time.Now().Add(time.Minute))
	if fake.requestedPath("/repos/acme/store/pulls") != lists {
		t.Fatal("ran again before the interval")
	}
	s.runReviewImportTick(t.Context(), time.Now().Add(11*time.Minute))
	if fake.requestedPath("/repos/acme/store/pulls") != lists+1 {
		t.Fatal("did not run after the interval")
	}
	status := requestJSON[reviewImportStatus](t, handler, http.MethodGet, "/api/reviews/import", nil, http.StatusOK)
	if status.Last == nil || status.Last.Trigger != "schedule" {
		t.Fatalf("status = %+v", status)
	}
}

func TestReviewImportIsOffByDefaultAndEnvOffIsHard(t *testing.T) {
	fake := newFakeImportHost(t, false)
	fake.open(1, "Change 1", "change-1")
	handler, s, _ := newReviewImportServer(t, fake, nil)

	// Allowed on the server, never turned on for the workspace.
	status := requestJSON[reviewImportStatus](t, handler, http.MethodGet, "/api/reviews/import", nil, http.StatusOK)
	if status.Enabled || status.Active || !status.Available || len(status.Repositories) != 0 || status.Last != nil {
		t.Fatalf("default status = %+v", status)
	}
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusConflict)

	// The workspace turned it on, the server is off: nothing runs.
	approveImport(t, handler, "github", "acme/store")
	for _, value := range []string{"", "off", "nonsense"} {
		t.Setenv("FLOW_REVIEW_IMPORT", value)
		requestJSON[map[string]any](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusConflict)
		s.runReviewImportTick(t.Context(), time.Now().Add(time.Hour))
		status = requestJSON[reviewImportStatus](t, handler, http.MethodGet, "/api/reviews/import", nil, http.StatusOK)
		if status.Available || status.Active || !status.Enabled {
			t.Fatalf("FLOW_REVIEW_IMPORT=%q status = %+v", value, status)
		}
	}
	t.Setenv("FLOW_REVIEW_IMPORT", "on")
	t.Setenv("FLOW_REVIEW_IMPORT_INTERVAL", "0")
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusConflict)
	s.runReviewImportTick(t.Context(), time.Now().Add(time.Hour))
	if fake.total() != 0 {
		t.Fatalf("an import ran while the server had it off: %v", fake.paths)
	}
}

func TestReviewImportSettingsFromEnv(t *testing.T) {
	t.Setenv("FLOW_REVIEW_IMPORT", "")
	t.Setenv("FLOW_REVIEW_IMPORT_INTERVAL", "")
	if settings := reviewImportSettingsFromEnv(); settings.Mode != "off" || settings.Interval != reviewImportDefaultInterval {
		t.Fatalf("default = %+v", settings)
	}
	for _, value := range []string{"on", "auto", "always"} {
		t.Setenv("FLOW_REVIEW_IMPORT", value)
		if settings := reviewImportSettingsFromEnv(); settings.Mode != "on" {
			t.Fatalf("%s = %+v", value, settings)
		}
	}
	t.Setenv("FLOW_REVIEW_IMPORT_INTERVAL", "30m")
	if settings := reviewImportSettingsFromEnv(); settings.Mode != "on" || settings.Interval != 30*time.Minute {
		t.Fatalf("configured = %+v", settings)
	}
	t.Setenv("FLOW_REVIEW_IMPORT_INTERVAL", "0")
	if settings := reviewImportSettingsFromEnv(); settings.Mode != "off" {
		t.Fatalf("interval 0 = %+v", settings)
	}
}

func TestReviewImportEndpointsForAdminsAndMembers(t *testing.T) {
	t.Setenv("FLOW_REVIEW_IMPORT", "on")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	api := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer api.Close()
	login := func(email string) *http.Client {
		jar, _ := cookiejar.New(nil)
		client := &http.Client{Jar: jar}
		authRequest[domain.AuthSession](t, client, http.MethodPost, api.URL+"/api/auth/login", map[string]string{"email": email, "password": "test-password"}, "", http.StatusOK)
		return client
	}
	admin, member := login("admin@example.test"), login("member@example.test")
	authRequest[domain.WorkspaceMember](t, admin, http.MethodPatch, api.URL+"/api/workspaces/test-workspace/members/usr_member", map[string]string{"role": "member"}, "", http.StatusOK)
	endpoint := api.URL + "/api/reviews/import"

	// Admins read, configure and run it (never "outside your teams").
	authRequest[reviewImportStatus](t, admin, http.MethodGet, endpoint, nil, "test-workspace", http.StatusOK)
	status := authRequest[reviewImportStatus](t, admin, http.MethodPatch, endpoint, map[string]any{"enabled": true}, "test-workspace", http.StatusOK)
	if !status.Active {
		t.Fatalf("status = %+v", status)
	}
	if result := authRequest[reviewImportResult](t, admin, http.MethodPost, endpoint, map[string]any{}, "test-workspace", http.StatusOK); result.Repositories != 0 {
		t.Fatalf("result = %+v", result)
	}
	// Members read the status; changing or running it is for admins.
	if status := authRequest[reviewImportStatus](t, member, http.MethodGet, endpoint, nil, "test-workspace", http.StatusOK); !status.Enabled {
		t.Fatalf("member status = %+v", status)
	}
	authRequest[map[string]any](t, member, http.MethodPatch, endpoint, map[string]any{"enabled": false}, "test-workspace", http.StatusForbidden)
	authRequest[map[string]any](t, member, http.MethodPost, endpoint, map[string]any{}, "test-workspace", http.StatusForbidden)
}

func TestReviewImportThroughGitHubAppInstallation(t *testing.T) {
	t.Setenv("FLOW_REVIEW_IMPORT", "on")
	f := newGitHubAppFixture(t)
	f.install()
	answer := func(status int, body any) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { writeJSON(w, status, body) }
	}
	f.routes.Store("POST /app/installations/42/access_tokens", answer(http.StatusCreated, map[string]any{"token": "ghs_test", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}))
	f.routes.Store("GET /installation/repositories", answer(http.StatusOK, map[string]any{"total_count": 1, "repositories": []map[string]any{{"id": 1, "full_name": "acme/store"}}}))
	f.routes.Store("GET /repos/acme/store/installation", answer(http.StatusOK, map[string]any{"id": 42}))
	pull := map[string]any{"id": 9921, "number": 21, "title": "Fix checkout", "state": "open", "html_url": "https://github.com/acme/store/pull/21", "user": map[string]any{"login": "alice"},
		"base": map[string]any{"ref": "main"}, "head": map[string]any{"ref": "flow/fix", "sha": "abc"}}
	f.routes.Store("GET /repos/acme/store/pulls", answer(http.StatusOK, []any{pull}))
	body, _ := json.Marshal(map[string]any{"enabled": true, "repositories": []map[string]string{{"provider": "github", "repository": "acme/store"}}})
	if rec := f.do(http.MethodPatch, "/api/reviews/import?workspace=test-workspace", body, http.Header{"Content-Type": {"application/json"}}); rec.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body.String())
	}
	rec := f.do(http.MethodPost, "/api/reviews/import?workspace=test-workspace", nil, nil)
	var result reviewImportResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || rec.Code != http.StatusOK || result.Imported != 1 || result.Repositories != 1 {
		t.Fatalf("app import: %d %s", rec.Code, rec.Body.String())
	}
	reviews := importedReviews(f.repo, "github")
	if len(reviews[21]) != 1 || reviews[21][0].ExternalID != "9921" {
		t.Fatalf("app review = %+v", reviews)
	}
	// The App's webhook for the same pull request updates that review.
	if rec := f.webhook("pull_request", "a1", fmt.Sprintf(appPullRequestPayload, "edited", "open", false, false, time.Now().UTC().Format(time.RFC3339))); rec.Code != http.StatusAccepted {
		t.Fatalf("webhook: %d %s", rec.Code, rec.Body.String())
	}
	if reviews := importedReviews(f.repo, "github"); len(reviews[21]) != 1 {
		t.Fatalf("webhook duplicated the imported review: %d", len(reviews[21]))
	}
}
