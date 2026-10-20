package main

import (
	"encoding/json"
	"fmt"
	"net/http"
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
	repos    []map[string]any
	notFound map[int]bool
}

func newFakeImportHost(t *testing.T, gitlab bool) *fakeImportHost {
	f := &fakeImportHost{t: t, gitlab: gitlab, pulls: map[int]map[string]any{}, headers: map[string]string{}, notFound: map[int]bool{}}
	if gitlab {
		f.repos = []map[string]any{{"path_with_namespace": "group/sub/app"}, {"path_with_namespace": "group/archived", "archived": true}}
	} else {
		f.repos = []map[string]any{{"full_name": "acme/store"}, {"full_name": "acme/old", "archived": true}, {"full_name": "other/tool"}}
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

func (f *fakeImportHost) serve(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("Authorization") == "Bearer local-test-token"
	if f.gitlab {
		token = r.Header.Get("PRIVATE-TOKEN") == "glpat-test"
	}
	if !token {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "Bad credentials"})
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.EscapedPath()
	if r.Method == http.MethodGet {
		f.paths = append(f.paths, r.URL.RequestURI())
	}
	for key, value := range f.headers {
		w.Header().Set(key, value)
	}
	if f.limited == r.Method+" "+path {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(30*time.Minute).Unix(), 10))
		writeJSON(w, http.StatusForbidden, map[string]string{"message": "API rate limit exceeded"})
		return
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
	repo, list, single := "/repos/acme/store", "/pulls", "/pulls/"
	if f.gitlab {
		repo, list, single = "/api/v4/projects/group%2Fsub%2Fapp", "/merge_requests", "/merge_requests/"
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

// newReviewImportServer is a workspace with a classic connection to fake.
func newReviewImportServer(t *testing.T, fake *fakeImportHost, config map[string]string) (http.Handler, *server, *store.SQLiteStore) {
	t.Helper()
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
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	return newHandler(s), s, repository
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

func TestReviewImportCreatesOpenPullRequestsWithoutDuplicates(t *testing.T) {
	fake := newFakeImportHost(t, false)
	for number := 1; number <= 105; number++ {
		fake.open(number, fmt.Sprintf("Change %d", number), fmt.Sprintf("change-%d", number))
	}
	fake.open(106, "TST-1 Fix the checkout", "tst-1-fix")
	handler, _, repository := newReviewImportServer(t, fake, nil)

	result := requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	if result.Repositories != 1 || result.Imported != 106 || result.Updated != 0 || len(result.Errors) != 0 {
		t.Fatalf("first import = %+v", result)
	}
	if fake.requested("/repos/acme/store/pulls?state=open") != 2 || fake.requested("other/tool") != 0 || fake.requested("acme/old") != 0 {
		t.Fatalf("listing must page through the organization's active repositories: %v", fake.paths)
	}
	reviews := importedReviews(repository, "github")
	if len(reviews) != 106 {
		t.Fatalf("imported reviews = %d", len(reviews))
	}
	linked := reviews[106][0]
	if linked.ExternalID != "9106" || linked.RepositoryOwner != "acme" || linked.RepositoryName != "store" || linked.Status != "open" || linked.HeadBranch != "tst-1-fix" ||
		linked.URL != "https://github.com/acme/store/pull/106" || linked.Author.ID != "github:alice" || linked.Description != "Body TST-1 Fix the checkout" {
		t.Fatalf("imported review = %+v", linked)
	}
	issue := repository.Bootstrap().Issues[slices.IndexFunc(repository.Bootstrap().Issues, func(issue domain.Issue) bool { return issue.Identifier == "TST-1" })]
	if !slices.Contains(linked.IssueIDs, issue.ID) {
		t.Fatalf("issue not linked from the title: %v", linked.IssueIDs)
	}

	again := requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	if again.Imported != 0 || again.Updated != 0 || again.Closed != 0 {
		t.Fatalf("re-sync changed reviews: %+v", again)
	}
	for number, items := range importedReviews(repository, "github") {
		if len(items) != 1 {
			t.Fatalf("PR #%d has %d reviews", number, len(items))
		}
	}
	status := requestJSON[reviewImportStatus](t, handler, http.MethodGet, "/api/reviews/import", nil, http.StatusOK)
	if status.Last == nil || status.Last.Repositories != 1 || status.Running || status.Background == "" {
		t.Fatalf("status = %+v", status)
	}
}

func TestReviewImportUpdatesClosedAndMergedPullRequests(t *testing.T) {
	fake := newFakeImportHost(t, false)
	for number := 1; number <= 4; number++ {
		fake.open(number, fmt.Sprintf("Change %d", number), fmt.Sprintf("change-%d", number))
	}
	handler, _, repository := newReviewImportServer(t, fake, nil)
	requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)

	fake.finish(1, true)
	fake.finish(2, false)
	fake.mu.Lock()
	fake.pulls[3]["title"] = "Change 3, renamed"
	fake.pulls[3]["draft"] = true
	fake.mu.Unlock()
	result := requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	if result.Imported != 0 || result.Closed != 2 || result.Updated != 3 {
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
	result = requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	if result.Updated != 1 || importedReviews(repository, "github")[2][0].Status != "open" || fake.requested("/repos/acme/store/pulls/1") != 1 {
		t.Fatalf("reopen = %+v; requests %v", result, fake.paths)
	}
}

func TestReviewImportAndWebhooksShareOneReview(t *testing.T) {
	fake := newFakeImportHost(t, false)
	fake.open(1, "Change 1", "change-1")
	handler, _, repository := newReviewImportServer(t, fake, map[string]string{"webhookSecret": "secret"})
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

func TestReviewImportQueuesFullSyncOfImportedReviews(t *testing.T) {
	previous := reviewSyncRunner
	reviewSyncRunner = func(job func()) { job() }
	t.Cleanup(func() { reviewSyncRunner = previous })
	fake := newFakeImportHost(t, false)
	fake.open(1, "Change 1", "change-1")
	handler, _, repository := newReviewImportServer(t, fake, nil)
	requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	review := importedReviews(repository, "github")[1][0]
	if review.SyncedAt == nil || review.SyncError != "" || review.HeadSHA != "sha-change-1" {
		t.Fatalf("imported review was not synced: %+v", review)
	}
	// Nothing changed on the code host: no second full sync.
	files := fake.requested("/pulls/1/files")
	requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	if fake.requested("/pulls/1/files") != files {
		t.Fatal("an unchanged pull request was synced again")
	}
}

func TestReviewImportGitLabSelfManagedMergeRequests(t *testing.T) {
	fake := newFakeImportHost(t, true)
	fake.open(3, "Rename math", "rename")
	fake.open(4, "Add docs", "docs")
	fake.open(5, "Experiment", "experiment")
	handler, _, repository := newReviewImportServer(t, fake, nil)
	result := requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	if result.Repositories != 1 || result.Imported != 3 || len(result.Errors) != 0 {
		t.Fatalf("gitlab import = %+v (%v)", result, fake.paths)
	}
	if fake.requested("min_access_level=20") != 1 || fake.requested("group%2Farchived") != 0 {
		t.Fatalf("project listing = %v", fake.paths)
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
}

func TestReviewImportStopsAtTheRateLimit(t *testing.T) {
	fake := newFakeImportHost(t, false)
	fake.open(1, "Change 1", "change-1")
	fake.headers["X-RateLimit-Remaining"] = "12"
	fake.headers["X-RateLimit-Reset"] = strconv.FormatInt(time.Now().Add(20*time.Minute).Unix(), 10)
	handler, _, repository := newReviewImportServer(t, fake, nil)
	result := requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	if result.RateLimitedUntil == nil || time.Until(*result.RateLimitedUntil) < 15*time.Minute || fake.requested("/pulls") != 0 || len(importedReviews(repository, "github")) != 0 {
		t.Fatalf("a nearly spent budget must stop the run before listing pulls: %+v %v", result, fake.paths)
	}
	// Sync now waits until the reset.
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusTooManyRequests)

	fake = newFakeImportHost(t, false)
	fake.open(1, "Change 1", "change-1")
	fake.limited = "GET /repos/acme/store/pulls"
	handler, _, _ = newReviewImportServer(t, fake, nil)
	result = requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
	if result.RateLimitedUntil == nil || result.Imported != 0 {
		t.Fatalf("a rate limited answer must stop the run: %+v", result)
	}
}

func TestReviewImportNeededWithoutWorkingWebhooks(t *testing.T) {
	fake := newFakeImportHost(t, false)
	_, s, repository := newReviewImportServer(t, fake, nil)
	data := repository.Bootstrap()
	if reason := s.reviewImportNeeded(t.Context(), data, "auto"); !strings.Contains(reason, "GitHub has no webhook") {
		t.Fatalf("no webhook secret: %q", reason)
	}
	index := slices.IndexFunc(data.IntegrationConnections, func(item domain.IntegrationConnection) bool { return item.ID == "github-import" })
	data.IntegrationConnections[index].Config["webhookSecret"] = "secret"
	if reason := s.reviewImportNeeded(t.Context(), data, "auto"); !strings.Contains(reason, "no webhook recently") {
		t.Fatalf("never delivered: %q", reason)
	}
	recent := time.Now().Add(-time.Hour)
	data.IntegrationConnections[index].LastWebhookAt = &recent
	if reason := s.reviewImportNeeded(t.Context(), data, "auto"); reason != "" {
		t.Fatalf("healthy webhook: %q", reason)
	}
	if reason := s.reviewImportNeeded(t.Context(), data, "always"); reason == "" {
		t.Fatal("always imports")
	}
	data.IntegrationConnections[index].LastWebhookAt = nil
	if reason := s.reviewImportNeeded(t.Context(), data, "off"); reason != "" {
		t.Fatalf("off: %q", reason)
	}
}

func TestReviewImportSchedulerRunsDueWorkspaces(t *testing.T) {
	fake := newFakeImportHost(t, false)
	fake.open(1, "Change 1", "change-1")
	_, s, repository := newReviewImportServer(t, fake, nil)
	settings := reviewImportSettings{Mode: "auto", Interval: 10 * time.Minute}
	now := time.Now()
	s.runReviewImportTick(t.Context(), settings, now)
	if len(importedReviews(repository, "github")) != 1 {
		t.Fatal("the background import did not run for a workspace without webhooks")
	}
	lists := fake.requested("/pulls?state=open")
	s.runReviewImportTick(t.Context(), settings, now.Add(time.Minute))
	if fake.requested("/pulls?state=open") != lists {
		t.Fatal("ran again before the interval")
	}
	s.runReviewImportTick(t.Context(), settings, now.Add(11*time.Minute))
	if fake.requested("/pulls?state=open") != lists+1 {
		t.Fatal("did not run after the interval")
	}
}

func TestReviewImportSettingsFromEnv(t *testing.T) {
	t.Setenv("FLOW_REVIEW_IMPORT", "")
	t.Setenv("FLOW_REVIEW_IMPORT_INTERVAL", "")
	if settings := reviewImportSettingsFromEnv(); settings.Mode != "auto" || settings.Interval != reviewImportDefaultInterval {
		t.Fatalf("default = %+v", settings)
	}
	t.Setenv("FLOW_REVIEW_IMPORT", "always")
	t.Setenv("FLOW_REVIEW_IMPORT_INTERVAL", "30m")
	if settings := reviewImportSettingsFromEnv(); settings.Mode != "always" || settings.Interval != 30*time.Minute {
		t.Fatalf("configured = %+v", settings)
	}
	t.Setenv("FLOW_REVIEW_IMPORT_INTERVAL", "0")
	if settings := reviewImportSettingsFromEnv(); settings.Mode != "off" {
		t.Fatalf("interval 0 = %+v", settings)
	}
}

func TestReviewImportThroughGitHubAppInstallation(t *testing.T) {
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
	data := f.repo.Bootstrap()
	if reason := f.server.reviewImportNeeded(t.Context(), data, "auto"); !strings.Contains(reason, "GitHub App") {
		t.Fatalf("an App without deliveries needs imports: %q", reason)
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
	if reason := f.server.reviewImportNeeded(t.Context(), f.repo.Bootstrap(), "auto"); reason != "" {
		t.Fatalf("an App delivering webhooks needs no background import: %q", reason)
	}
}
