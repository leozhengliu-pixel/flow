package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestMain(m *testing.M) {
	// Background review syncs never run in tests unless a test asks for them.
	reviewSyncRunner = func(func()) {}
	os.Exit(m.Run())
}

type recordedReviewRequest struct {
	Method, Path string
	Body         map[string]any
}

// fakeReviewGitHub serves the recorded jiaozongben/flow-coding-test PRs #7
// and #8 (testdata/review_sync) and records writes.
type fakeReviewGitHub struct {
	t        *testing.T
	server   *httptest.Server
	mu       sync.Mutex
	requests []recordedReviewRequest
	deny     map[string]bool
	created  map[string][]map[string]any // "comments" / "issue_comments" by PR
	resolved map[string]bool
	nextID   int64
	files    map[string][]map[string]any // generated file lists by PR number
}

func newFakeReviewGitHub(t *testing.T) *fakeReviewGitHub {
	f := &fakeReviewGitHub{t: t, deny: map[string]bool{}, created: map[string][]map[string]any{}, resolved: map[string]bool{}, nextID: 9000, files: map[string][]map[string]any{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func readReviewFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "review_sync", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (f *fakeReviewGitHub) list(name, number, kind string) []map[string]any {
	var items []map[string]any
	if raw, err := os.ReadFile(filepath.Join("testdata", "review_sync", "github_pr"+number+"_"+name+".json")); err == nil {
		_ = json.Unmarshal(raw, &items)
	}
	return append(items, f.created[number+"/"+kind]...)
}

func (f *fakeReviewGitHub) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer local-test-token" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "Bad credentials"})
		return
	}
	var body map[string]any
	if r.Body != nil {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/repos/jiaozongben/flow-coding-test")
	if query, _ := body["query"].(string); r.Method != http.MethodGet && !strings.Contains(query, "reviewThreads") {
		f.requests = append(f.requests, recordedReviewRequest{Method: r.Method, Path: path, Body: body})
	}
	if f.deny[r.Method+" "+path] {
		writeJSON(w, http.StatusForbidden, map[string]string{"message": "Resource not accessible by integration"})
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	paged := func(items []map[string]any) {
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		if perPage == 0 {
			perPage = 30
		}
		start := min(len(items), max(page-1, 0)*perPage)
		writeJSON(w, http.StatusOK, items[start:min(len(items), start+perPage)])
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	now := time.Now().UTC().Format(time.RFC3339)
	newComment := func(kind, number string, extra map[string]any) map[string]any {
		f.nextID++
		comment := map[string]any{"id": f.nextID, "body": body["body"], "html_url": fmt.Sprintf("https://github.com/jiaozongben/flow-coding-test/pull/%s#c%d", number, f.nextID),
			"user": map[string]any{"login": "flow-app[bot]"}, "created_at": now, "updated_at": now}
		for key, value := range extra {
			comment[key] = value
		}
		f.created[number+"/"+kind] = append(f.created[number+"/"+kind], comment)
		return comment
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/graphql":
		query, _ := body["query"].(string)
		variables, _ := body["variables"].(map[string]any)
		if strings.Contains(query, "reviewThreads") {
			var threads map[string]any
			_ = json.Unmarshal(readReviewFixture(f.t, "github_pr7_threads.json"), &threads)
			nodes := threads["data"].(map[string]any)["repository"].(map[string]any)["pullRequest"].(map[string]any)["reviewThreads"].(map[string]any)["nodes"].([]any)
			for _, node := range nodes {
				thread := node.(map[string]any)
				if value, ok := f.resolved[thread["id"].(string)]; ok {
					thread["isResolved"] = value
				}
			}
			writeJSON(w, http.StatusOK, threads)
			return
		}
		id, _ := variables["id"].(string)
		if f.deny["graphql"] {
			writeJSON(w, http.StatusOK, map[string]any{"data": nil, "errors": []map[string]string{{"type": "FORBIDDEN", "message": "Resource not accessible by integration"}}})
			return
		}
		f.resolved[id] = strings.Contains(query, "mutation($id: ID!) { resolveReviewThread")
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"thread": map[string]any{"id": id}}})
	case len(parts) == 2 && parts[0] == "pulls" && r.Method == http.MethodGet:
		w.Write(readReviewFixture(f.t, "github_pr"+parts[1]+".json"))
	case len(parts) == 3 && parts[0] == "pulls" && parts[2] == "files":
		if generated, ok := f.files[parts[1]]; ok {
			paged(generated)
			return
		}
		paged(f.list("files", parts[1], "files"))
	case len(parts) == 3 && parts[0] == "pulls" && parts[2] == "comments" && r.Method == http.MethodGet:
		paged(f.list("comments", parts[1], "comments"))
	case len(parts) == 3 && parts[0] == "pulls" && parts[2] == "reviews":
		paged(f.list("reviews", parts[1], "reviews"))
	case len(parts) == 3 && parts[0] == "issues" && parts[2] == "comments" && r.Method == http.MethodGet:
		paged(f.list("issue_comments", parts[1], "issue_comments"))
	case len(parts) == 3 && parts[0] == "pulls" && parts[2] == "comments" && r.Method == http.MethodPost:
		writeJSON(w, http.StatusCreated, newComment("comments", parts[1], map[string]any{"path": body["path"], "line": body["line"], "side": body["side"], "commit_id": body["commit_id"]}))
	case len(parts) == 5 && parts[0] == "pulls" && parts[4] == "replies":
		var inReplyTo int64
		var root map[string]any
		for _, comment := range f.list("comments", parts[1], "comments") {
			if strconv.FormatFloat(reviewFixtureNumber(comment["id"]), 'f', -1, 64) == parts[3] {
				root = comment
			}
		}
		inReplyTo, _ = strconv.ParseInt(parts[3], 10, 64)
		writeJSON(w, http.StatusCreated, newComment("comments", parts[1], map[string]any{"in_reply_to_id": inReplyTo, "path": root["path"], "line": root["line"], "side": root["side"]}))
	case len(parts) == 3 && parts[0] == "issues" && parts[2] == "comments" && r.Method == http.MethodPost:
		writeJSON(w, http.StatusCreated, newComment("issue_comments", parts[1], nil))
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found " + r.Method + " " + r.URL.Path})
	}
}

func reviewFixtureNumber(value any) float64 {
	switch number := value.(type) {
	case float64:
		return number
	case int64:
		return float64(number)
	}
	return 0
}

func (f *fakeReviewGitHub) writes() []recordedReviewRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

// newReviewSyncServer points the seeded review at PR number on the fake.
func newReviewSyncServer(t *testing.T, apiURL string, number int) (http.Handler, *store.SQLiteStore) {
	t.Helper()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	err = repository.MutateWorkspace(t.Context(), "test-workspace", "review.sync_fixture", "", nil, func(data *domain.Bootstrap) error {
		data.Reviews[0].Provider, data.Reviews[0].RepositoryOwner, data.Reviews[0].RepositoryName, data.Reviews[0].Number = "github", "jiaozongben", "flow-coding-test", number
		data.Reviews[0].ExternalID = "1"
		data.IntegrationConnections = append(data.IntegrationConnections, domain.IntegrationConnection{ID: "github-review-sync", Provider: "github", Name: "GitHub", Status: "connected", OAuthAccessToken: "local-test-token",
			Config: map[string]string{"apiUrl": apiURL, "organization": "jiaozongben", "webhookSecret": "secret"}, Scopes: []string{}, Channels: []string{}})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true}), repository
}

func findReviewEvent(t *testing.T, review domain.CodeReview, id string) domain.ReviewEvent {
	t.Helper()
	event, ok := reviewEventByID(review, id)
	if !ok {
		t.Fatalf("event %s missing from %#v", id, review.Events)
	}
	return event
}

func syncedEvents(review domain.CodeReview) []domain.ReviewEvent {
	return slices.DeleteFunc(slices.Clone(review.Events), func(event domain.ReviewEvent) bool { return event.Remote == nil })
}

func TestReviewSyncFromRecordedGitHubPullRequest(t *testing.T) {
	fake := newFakeReviewGitHub(t)
	handler, _ := newReviewSyncServer(t, fake.server.URL, 7)
	review := requestJSON[domain.CodeReview](t, handler, http.MethodPost, "/api/reviews/review-1/sync", map[string]bool{"force": true}, http.StatusOK)
	if review.SyncError != "" || review.SyncedAt == nil {
		t.Fatalf("sync failed: %q", review.SyncError)
	}
	if len(review.Files) != 3 || review.HeadSHA == "" || review.BaseSHA == "" {
		t.Fatalf("files/refs not synced: %#v", review.Files)
	}
	if file := review.Files[slices.IndexFunc(review.Files, func(file domain.ReviewFile) bool { return file.Path == "src/geometry.js" })]; file.Status != "added" || file.Additions != 13 || !strings.HasPrefix(file.Patch, "@@") {
		t.Fatalf("added file = %#v", file)
	}
	// 4 inline comments, 1 conversation comment, 1 review ("Review notes";
	// the empty review that only holds a reply is not a card).
	if got := syncedEvents(review); len(got) != 6 {
		t.Fatalf("synced events = %d: %#v", len(got), got)
	}
	root := findReviewEvent(t, review, "github-review_comment-4320909403")
	reply := findReviewEvent(t, review, "github-review_comment-4320909537")
	if root.Path != "src/math.js" || root.Line != 11 || root.Side != "RIGHT" || root.Remote.ThreadID != "PRRT_kwDOVPJZYM6uLbWF" || root.Resolved || root.Actor.ID != "github:jiaozongben" {
		t.Fatalf("thread root = %#v", root)
	}
	if reply.ParentID != root.ID || reply.Line != 11 || !strings.Contains(reply.Body, "RangeError") {
		t.Fatalf("reply not threaded: %#v", reply)
	}
	if resolved := findReviewEvent(t, review, "github-review_comment-4320909416"); !resolved.Resolved || resolved.Path != "src/geometry.js" || !strings.Contains(resolved.Body, "```suggestion") {
		t.Fatalf("resolved suggestion thread = %#v", resolved)
	}
	if summary := findReviewEvent(t, review, "github-review-5580132882"); summary.Type != "review_commented" || summary.Body != "Review notes" {
		t.Fatalf("review summary = %#v", summary)
	}
	if conversation := findReviewEvent(t, review, "github-issue_comment-6282025869"); conversation.Type != "commented" || conversation.Path != "" {
		t.Fatalf("conversation comment = %#v", conversation)
	}
	// Idempotent: a second sync changes nothing.
	again := requestJSON[domain.CodeReview](t, handler, http.MethodPost, "/api/reviews/review-1/sync", map[string]bool{"force": true}, http.StatusOK)
	if len(again.Events) != len(review.Events) || len(again.Files) != 3 {
		t.Fatalf("second sync duplicated data: %d → %d events", len(review.Events), len(again.Events))
	}
	// A non-forced sync right after is skipped.
	requestJSON[domain.CodeReview](t, handler, http.MethodPost, "/api/reviews/review-1/sync", map[string]bool{"force": false}, http.StatusOK)
	if len(fake.writes()) != 0 {
		t.Fatalf("sync wrote to GitHub: %#v", fake.writes())
	}
}

func TestReviewSyncRenamesDeletedAndLargeFiles(t *testing.T) {
	fake := newFakeReviewGitHub(t)
	handler, _ := newReviewSyncServer(t, fake.server.URL, 8)
	review := requestJSON[domain.CodeReview](t, handler, http.MethodPost, "/api/reviews/review-1/sync", nil, http.StatusOK)
	byPath := map[string]domain.ReviewFile{}
	for _, file := range review.Files {
		byPath[file.Path] = file
	}
	if renamed := byPath["lib/arithmetic.js"]; renamed.Status != "renamed" || renamed.PreviousPath != "src/math.js" || renamed.Patch != "" || renamed.PatchUnavailable {
		t.Fatalf("pure rename = %#v", renamed)
	}
	if deleted := byPath["src/math.test.js"]; deleted.Status != "deleted" || deleted.Deletions != 11 {
		t.Fatalf("deleted file = %#v", deleted)
	}
	if large := byPath["lib/squares.js"]; large.Additions != 403 || strings.Count(large.Patch, "\n+") < 400 {
		t.Fatalf("large file = %d additions", large.Additions)
	}
	if review.Additions != 410 || review.Deletions != 12 {
		t.Fatalf("totals = +%d -%d", review.Additions, review.Deletions)
	}
	if summary := findReviewEvent(t, review, "github-review-5580133207"); !strings.Contains(summary.Body, "Large rename") {
		t.Fatalf("review = %#v", summary)
	}
}

func TestReviewSyncPaginatesFilesAndFlagsMissingPatches(t *testing.T) {
	fake := newFakeReviewGitHub(t)
	files := []map[string]any{}
	for index := range 150 {
		files = append(files, map[string]any{"filename": fmt.Sprintf("gen/file%03d.txt", index), "status": "modified", "additions": 1, "deletions": 1, "changes": 2, "patch": "@@ -1 +1 @@\n-a\n+b"})
	}
	files = append(files, map[string]any{"filename": "assets/logo.png", "status": "added", "additions": 0, "deletions": 0, "changes": 0},
		map[string]any{"filename": "data/huge.json", "status": "modified", "additions": 50000, "deletions": 2, "changes": 50002})
	fake.files["8"] = files
	handler, _ := newReviewSyncServer(t, fake.server.URL, 8)
	review := requestJSON[domain.CodeReview](t, handler, http.MethodPost, "/api/reviews/review-1/sync", nil, http.StatusOK)
	if len(review.Files) != 152 {
		t.Fatalf("paginated files = %d", len(review.Files))
	}
	last := review.Files[len(review.Files)-1]
	if last.Path != "data/huge.json" || !last.PatchUnavailable || last.Patch != "" {
		t.Fatalf("large file without patch = %#v", last)
	}
	if binary := review.Files[len(review.Files)-2]; binary.PatchUnavailable || binary.Status != "added" {
		t.Fatalf("empty binary file = %#v", binary)
	}
}

func TestReviewWriteBackToGitHub(t *testing.T) {
	fake := newFakeReviewGitHub(t)
	handler, _ := newReviewSyncServer(t, fake.server.URL, 7)
	requestJSON[domain.CodeReview](t, handler, http.MethodPost, "/api/reviews/review-1/sync", nil, http.StatusOK)

	// An inline comment on a removed line (left side).
	review := requestJSON[domain.CodeReview](t, handler, http.MethodPost, "/api/reviews/review-1/comments", map[string]any{"body": "Why remove this?", "path": "src/math.js", "line": 3, "side": "LEFT"}, http.StatusCreated)
	created := review.Events[len(review.Events)-1]
	if created.Side != "LEFT" || created.Sync != nil || created.Remote == nil || created.Remote.Kind != reviewRemoteReviewComment {
		t.Fatalf("inline comment not written back: %#v", created)
	}
	writes := fake.writes()
	if last := writes[len(writes)-1]; last.Path != "/pulls/7/comments" || last.Body["side"] != "LEFT" || last.Body["line"] != float64(3) || last.Body["commit_id"] != review.HeadSHA || review.HeadSHA == "" {
		t.Fatalf("create request = %#v", last)
	}
	// A multi-line comment sends its start.
	requestJSON[domain.CodeReview](t, handler, http.MethodPost, "/api/reviews/review-1/comments", map[string]any{"body": "Range", "path": "src/math.js", "line": 14, "startLine": 11, "side": "RIGHT"}, http.StatusCreated)
	if last := fake.writes()[len(fake.writes())-1]; last.Body["start_line"] != float64(11) || last.Body["start_side"] != "RIGHT" {
		t.Fatalf("multi-line request = %#v", last.Body)
	}

	// A reply joins the GitHub thread.
	review = requestJSON[domain.CodeReview](t, handler, http.MethodPost, "/api/reviews/review-1/comments", map[string]any{"body": "Agreed", "parentId": "github-review_comment-4320909403"}, http.StatusCreated)
	reply := review.Events[len(review.Events)-1]
	if reply.Remote == nil || reply.Remote.ThreadID != "PRRT_kwDOVPJZYM6uLbWF" || reply.Sync != nil {
		t.Fatalf("reply = %#v", reply)
	}
	if last := fake.writes()[len(fake.writes())-1]; last.Path != "/pulls/7/comments/4320909403/replies" {
		t.Fatalf("reply request = %#v", last)
	}

	// Resolving the thread resolves it on GitHub.
	review = requestJSON[domain.CodeReview](t, handler, http.MethodPatch, "/api/reviews/review-1/comments/github-review_comment-4320909403", map[string]bool{"resolved": true}, http.StatusOK)
	if root := findReviewEvent(t, review, "github-review_comment-4320909403"); !root.Resolved || root.Sync != nil {
		t.Fatalf("resolved root = %#v", root)
	}
	if !fake.resolved["PRRT_kwDOVPJZYM6uLbWF"] {
		t.Fatal("thread was not resolved on GitHub")
	}

	// A conversation comment.
	requestJSON[domain.CodeReview](t, handler, http.MethodPost, "/api/reviews/review-1/comments", map[string]any{"body": "Thanks!"}, http.StatusCreated)
	if last := fake.writes()[len(fake.writes())-1]; last.Path != "/issues/7/comments" {
		t.Fatalf("conversation request = %#v", last)
	}

	// The next sync sees Flow's comments on GitHub without duplicating them.
	before := requestJSON[domain.CodeReview](t, handler, http.MethodGet, "/api/reviews/review-1", nil, http.StatusOK)
	after := requestJSON[domain.CodeReview](t, handler, http.MethodPost, "/api/reviews/review-1/sync", nil, http.StatusOK)
	if len(after.Events) != len(before.Events) {
		t.Fatalf("sync duplicated written-back comments: %d → %d", len(before.Events), len(after.Events))
	}
	if root := findReviewEvent(t, after, "github-review_comment-4320909403"); !root.Resolved {
		t.Fatal("resolution lost on sync")
	}
	if mine := findReviewEvent(t, after, created.ID); mine.Actor.ID == "github:flow-app[bot]" || mine.Side != "LEFT" {
		t.Fatalf("Flow comment lost its author or side: %#v", mine)
	}
}

func TestReviewWriteBackPermissionErrorKeepsLocalChange(t *testing.T) {
	fake := newFakeReviewGitHub(t)
	handler, _ := newReviewSyncServer(t, fake.server.URL, 7)
	requestJSON[domain.CodeReview](t, handler, http.MethodPost, "/api/reviews/review-1/sync", nil, http.StatusOK)
	fake.mu.Lock()
	fake.deny["POST /pulls/7/comments/4320909403/replies"], fake.deny["graphql"] = true, true
	fake.mu.Unlock()

	review := requestJSON[domain.CodeReview](t, handler, http.MethodPost, "/api/reviews/review-1/comments", map[string]any{"body": "Blocked reply", "parentId": "github-review_comment-4320909403"}, http.StatusCreated)
	reply := review.Events[len(review.Events)-1]
	if reply.Body != "Blocked reply" || reply.Remote != nil || reply.Sync == nil || reply.Sync.State != "failed" || reply.Sync.Op != "create" || !strings.Contains(reply.Sync.Error, "denied") {
		t.Fatalf("failed reply = %#v", reply)
	}
	review = requestJSON[domain.CodeReview](t, handler, http.MethodPatch, "/api/reviews/review-1/comments/github-review_comment-4320909410", map[string]bool{"resolved": true}, http.StatusOK)
	root := findReviewEvent(t, review, "github-review_comment-4320909410")
	if !root.Resolved || root.Sync == nil || root.Sync.Op != "resolve" || root.Sync.State != "failed" {
		t.Fatalf("failed resolution = %#v", root)
	}
	// A sync keeps the local resolution while it is not on GitHub.
	review = requestJSON[domain.CodeReview](t, handler, http.MethodPost, "/api/reviews/review-1/sync", nil, http.StatusOK)
	if root := findReviewEvent(t, review, "github-review_comment-4320909410"); !root.Resolved || root.Sync == nil {
		t.Fatalf("sync dropped the pending resolution: %#v", root)
	}

	// Access granted: the sync retries both.
	fake.mu.Lock()
	fake.deny = map[string]bool{}
	fake.mu.Unlock()
	review = requestJSON[domain.CodeReview](t, handler, http.MethodPost, "/api/reviews/review-1/sync", nil, http.StatusOK)
	retried := findReviewEvent(t, review, reply.ID)
	if retried.Sync != nil || retried.Remote == nil || retried.ParentID != "github-review_comment-4320909403" {
		t.Fatalf("retried reply = %#v", retried)
	}
	if root := findReviewEvent(t, review, "github-review_comment-4320909410"); !root.Resolved || root.Sync != nil || !fake.resolved["PRRT_kwDOVPJZYM6uLbWJ"] {
		t.Fatalf("retried resolution = %#v", root)
	}
	if count := len(slices.DeleteFunc(slices.Clone(review.Events), func(event domain.ReviewEvent) bool { return event.Body != "Blocked reply" })); count != 1 {
		t.Fatalf("retry duplicated the reply: %d", count)
	}
}

func TestReviewWriteBackWithoutCredentialsFailsVisibly(t *testing.T) {
	handler, repository := newReviewSyncServer(t, "http://127.0.0.1:1", 7)
	if err := repository.MutateWorkspace(t.Context(), "test-workspace", "review.sync_fixture", "", nil, func(data *domain.Bootstrap) error {
		for index := range data.IntegrationConnections {
			data.IntegrationConnections[index].OAuthAccessToken = ""
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOW_INTEGRATION_GITHUB_ACCESS_TOKEN", "")
	review := requestJSON[domain.CodeReview](t, handler, http.MethodPost, "/api/reviews/review-1/comments", map[string]any{"body": "Local only"}, http.StatusCreated)
	if event := review.Events[len(review.Events)-1]; event.Sync == nil || event.Sync.State != "failed" || !strings.Contains(event.Sync.Error, "credentials") {
		t.Fatalf("comment without credentials = %#v", event)
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/reviews/review-1/comments", map[string]any{"body": "x", "path": "a.go", "line": 1, "side": "MIDDLE"}, http.StatusBadRequest)
}

func signedGitHubWebhook(t *testing.T, handler http.Handler, event, delivery string, payload any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(payload)
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write(raw)
	req := httptest.NewRequest(http.MethodPost, "/api/integrations/github/webhook?workspace=test-workspace", bytes.NewReader(raw))
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", delivery)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("%s webhook status=%d body=%s", event, rec.Code, rec.Body.String())
	}
	var result map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &result)
	return result
}

func TestGitHubReviewWebhooksUpdateCommentsIncrementally(t *testing.T) {
	fake := newFakeReviewGitHub(t)
	handler, _ := newReviewSyncServer(t, fake.server.URL, 7)
	requestJSON[domain.CodeReview](t, handler, http.MethodPost, "/api/reviews/review-1/sync", nil, http.StatusOK)
	var comments []map[string]any
	_ = json.Unmarshal(readReviewFixture(t, "github_pr7_comments.json"), &comments)
	geometry := comments[slices.IndexFunc(comments, func(item map[string]any) bool { return item["id"] == float64(4320909416) })]
	repository := map[string]any{"full_name": "jiaozongben/flow-coding-test"}
	pull := map[string]any{"id": 1, "number": 7, "title": "Add geometry helpers", "head": map[string]any{"sha": "abc"}}

	result := signedGitHubWebhook(t, handler, "pull_request_review_thread", "d-thread", map[string]any{"action": "unresolved", "repository": repository, "pull_request": pull,
		"thread": map[string]any{"node_id": "PRRT_kwDOVPJZYM6uLbWN", "comments": []any{geometry}}})
	if result["reviewId"] != "review-1" {
		t.Fatalf("thread webhook result = %#v", result)
	}
	review := requestJSON[domain.CodeReview](t, handler, http.MethodGet, "/api/reviews/review-1", nil, http.StatusOK)
	if event := findReviewEvent(t, review, "github-review_comment-4320909416"); event.Resolved {
		t.Fatal("unresolved webhook did not reopen the thread")
	}
	eventsBefore := len(review.Events)

	signedGitHubWebhook(t, handler, "pull_request_review_comment", "d-new", map[string]any{"action": "created", "repository": repository, "pull_request": pull,
		"comment": map[string]any{"id": 555, "in_reply_to_id": int64(4320909416), "path": "src/geometry.js", "line": 12, "side": "RIGHT", "body": "Done", "user": map[string]any{"login": "octocat"}, "created_at": time.Now().UTC().Format(time.RFC3339)}})
	review = requestJSON[domain.CodeReview](t, handler, http.MethodGet, "/api/reviews/review-1", nil, http.StatusOK)
	added := findReviewEvent(t, review, "github-review_comment-555")
	if added.ParentID != "github-review_comment-4320909416" || added.Body != "Done" {
		t.Fatalf("webhook reply = %#v", added)
	}
	// The created delivery also adds its delivery event; comments are not duplicated.
	if len(syncedEvents(review)) != 7 || len(review.Events) < eventsBefore+1 {
		t.Fatalf("events after created webhook = %d", len(review.Events))
	}

	signedGitHubWebhook(t, handler, "pull_request_review_comment", "d-edit", map[string]any{"action": "edited", "repository": repository, "pull_request": pull,
		"comment": map[string]any{"id": 555, "in_reply_to_id": int64(4320909416), "path": "src/geometry.js", "line": 12, "side": "RIGHT", "body": "Done, thanks", "user": map[string]any{"login": "octocat"}}})
	signedGitHubWebhook(t, handler, "pull_request_review_comment", "d-delete", map[string]any{"action": "deleted", "repository": repository, "pull_request": pull, "comment": map[string]any{"id": int64(4320909410)}})
	signedGitHubWebhook(t, handler, "issue_comment", "d-issue", map[string]any{"action": "created", "repository": repository, "issue": map[string]any{"number": 7, "pull_request": map[string]any{"url": "x"}},
		"comment": map[string]any{"id": 777, "body": "Ship it", "user": map[string]any{"login": "octocat"}, "created_at": time.Now().UTC().Format(time.RFC3339)}})
	review = requestJSON[domain.CodeReview](t, handler, http.MethodGet, "/api/reviews/review-1", nil, http.StatusOK)
	if edited := findReviewEvent(t, review, "github-review_comment-555"); edited.Body != "Done, thanks" {
		t.Fatalf("edit not applied: %#v", edited)
	}
	if _, ok := reviewEventByID(review, "github-review_comment-4320909410"); ok {
		t.Fatal("deleted comment is still listed")
	}
	if conversation := findReviewEvent(t, review, "github-issue_comment-777"); conversation.Body != "Ship it" {
		t.Fatalf("issue comment = %#v", conversation)
	}
}

func TestReviewSyncFromGitLabMergeRequest(t *testing.T) {
	var mu sync.Mutex
	writes := []recordedReviewRequest{}
	resolved := map[string]bool{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "glpat-test" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "401 Unauthorized"})
			return
		}
		path := r.URL.Path
		base := "/api/v4/projects/group/app/merge_requests/3"
		mu.Lock()
		defer mu.Unlock()
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.Method != http.MethodGet {
			writes = append(writes, recordedReviewRequest{Method: r.Method, Path: strings.TrimPrefix(r.URL.RequestURI(), "/api/v4/projects/group%2Fapp/merge_requests/3"), Body: body})
		}
		page := r.URL.Query().Get("page")
		switch {
		case path == base && r.Method == http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"title": "Rename math", "description": "", "state": "opened", "web_url": "https://gitlab.example/group/app/-/merge_requests/3", "source_branch": "rename", "target_branch": "main", "sha": "head1",
				"diff_refs": map[string]string{"base_sha": "base1", "start_sha": "start1", "head_sha": "head1"}})
		case path == base+"/diffs":
			if page != "1" {
				writeJSON(w, http.StatusOK, []any{})
				return
			}
			writeJSON(w, http.StatusOK, []map[string]any{
				{"old_path": "src/math.js", "new_path": "lib/arithmetic.js", "renamed_file": true, "diff": ""},
				{"old_path": "src/math.test.js", "new_path": "src/math.test.js", "deleted_file": true, "diff": "@@ -1,2 +0,0 @@\n-a\n-b\n"},
				{"old_path": "lib/new.js", "new_path": "lib/new.js", "new_file": true, "diff": "@@ -0,0 +1 @@\n+x\n"},
				{"old_path": "src/keep.js", "new_path": "src/keep.js", "diff": "@@ -1,3 +1,3 @@\n a\n-b\n+c\n d\n"},
				{"old_path": "big.min.js", "new_path": "big.min.js", "diff": "", "too_large": true},
			})
		case path == base+"/discussions" && r.Method == http.MethodGet:
			if page != "1" {
				writeJSON(w, http.StatusOK, []any{})
				return
			}
			writeJSON(w, http.StatusOK, []map[string]any{
				{"id": "disc-left", "notes": []map[string]any{
					{"id": 11, "type": "DiffNote", "body": "Why delete b?", "author": map[string]any{"username": "alice", "name": "Alice"}, "created_at": "2026-10-19T10:00:00Z", "resolvable": true, "resolved": resolved["disc-left"],
						"position": map[string]any{"new_path": "src/keep.js", "old_path": "src/keep.js", "old_line": 2, "new_line": nil}},
					{"id": 12, "type": "DiffNote", "body": "It moved", "author": map[string]any{"username": "bob"}, "created_at": "2026-10-19T10:05:00Z", "resolvable": true, "resolved": resolved["disc-left"]},
					{"id": 13, "body": "changed the description", "system": true, "author": map[string]any{"username": "bob"}, "created_at": "2026-10-19T10:06:00Z"},
				}},
				{"id": "disc-plain", "individual_note": true, "notes": []map[string]any{{"id": 21, "body": "LGTM", "author": map[string]any{"username": "carol"}, "created_at": "2026-10-19T11:00:00Z"}}},
			})
		case path == base+"/discussions" && r.Method == http.MethodPost:
			writeJSON(w, http.StatusCreated, map[string]any{"id": "disc-new", "notes": []map[string]any{{"id": 31, "body": body["body"]}}})
		case strings.HasPrefix(path, base+"/discussions/") && strings.HasSuffix(path, "/notes"):
			writeJSON(w, http.StatusCreated, map[string]any{"id": 41, "body": body["body"]})
		case strings.HasPrefix(path, base+"/discussions/") && r.Method == http.MethodPut:
			resolved[strings.TrimPrefix(path, base+"/discussions/")] = r.URL.Query().Get("resolved") == "true"
			writeJSON(w, http.StatusOK, map[string]any{"id": "disc-left"})
		default:
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "404 Not Found " + path})
		}
	}))
	defer upstream.Close()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err := repository.MutateWorkspace(t.Context(), "test-workspace", "review.sync_fixture", "", nil, func(data *domain.Bootstrap) error {
		data.Reviews[0].Provider, data.Reviews[0].RepositoryOwner, data.Reviews[0].RepositoryName, data.Reviews[0].Number = "gitlab", "group", "app", 3
		data.IntegrationConnections = append(data.IntegrationConnections, domain.IntegrationConnection{ID: "gitlab-review-sync", Provider: "gitlab", Name: "GitLab", Status: "connected", OAuthAccessToken: "glpat-test",
			Config: map[string]string{"apiUrl": upstream.URL + "/api/v4"}, Scopes: []string{}, Channels: []string{}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	review := requestJSON[domain.CodeReview](t, handler, http.MethodPost, "/api/reviews/review-1/sync", nil, http.StatusOK)
	if review.SyncError != "" || len(review.Files) != 5 || review.BaseSHA != "base1" || review.StartSHA != "start1" {
		t.Fatalf("gitlab sync = %q files=%#v", review.SyncError, review.Files)
	}
	byPath := map[string]domain.ReviewFile{}
	for _, file := range review.Files {
		byPath[file.Path] = file
	}
	if file := byPath["lib/arithmetic.js"]; file.Status != "renamed" || file.PreviousPath != "src/math.js" {
		t.Fatalf("rename = %#v", file)
	}
	if file := byPath["src/math.test.js"]; file.Status != "deleted" || file.Deletions != 2 {
		t.Fatalf("delete = %#v", file)
	}
	if file := byPath["src/keep.js"]; file.Additions != 1 || file.Deletions != 1 || byPath["lib/new.js"].Status != "added" || !byPath["big.min.js"].PatchUnavailable {
		t.Fatalf("diff counts = %#v", byPath)
	}
	root := findReviewEvent(t, review, "gitlab-note-11")
	if root.Side != "LEFT" || root.Line != 2 || root.Path != "src/keep.js" || root.Remote.ThreadID != "disc-left" || root.Resolved {
		t.Fatalf("diff note = %#v", root)
	}
	if reply := findReviewEvent(t, review, "gitlab-note-12"); reply.ParentID != root.ID {
		t.Fatalf("gitlab reply = %#v", reply)
	}
	if _, ok := reviewEventByID(review, "gitlab-note-13"); ok {
		t.Fatal("system notes must not sync")
	}

	review = requestJSON[domain.CodeReview](t, handler, http.MethodPost, "/api/reviews/review-1/comments", map[string]any{"body": "Ack", "parentId": root.ID}, http.StatusCreated)
	if reply := review.Events[len(review.Events)-1]; reply.Remote == nil || reply.Remote.ThreadID != "disc-left" || reply.Sync != nil {
		t.Fatalf("gitlab reply write-back = %#v", reply)
	}
	review = requestJSON[domain.CodeReview](t, handler, http.MethodPatch, "/api/reviews/review-1/comments/"+root.ID, map[string]bool{"resolved": true}, http.StatusOK)
	if !resolved["disc-left"] || findReviewEvent(t, review, root.ID).Sync != nil {
		t.Fatal("gitlab discussion was not resolved")
	}
	// An inline comment on an unchanged line carries both line numbers.
	review = requestJSON[domain.CodeReview](t, handler, http.MethodPost, "/api/reviews/review-1/comments", map[string]any{"body": "Context", "path": "src/keep.js", "line": 3, "side": "RIGHT"}, http.StatusCreated)
	mu.Lock()
	last := writes[len(writes)-1]
	mu.Unlock()
	position, _ := last.Body["position"].(map[string]any)
	if position["new_line"] != float64(3) || position["old_line"] != float64(3) || position["base_sha"] != "base1" || position["head_sha"] != "head1" {
		t.Fatalf("gitlab position = %#v", position)
	}
	if inline := review.Events[len(review.Events)-1]; inline.Remote == nil || inline.Remote.ThreadID != "disc-new" || inline.Remote.ID != "31" {
		t.Fatalf("gitlab inline write-back = %#v", inline)
	}
}

func TestPatchCounterpartLine(t *testing.T) {
	patch := "@@ -10,4 +10,5 @@ func\n a\n-b\n+c\n+d\n e\n"
	for _, tc := range []struct {
		side       string
		line, want int
	}{{"RIGHT", 10, 10}, {"RIGHT", 11, 0}, {"RIGHT", 13, 12}, {"LEFT", 12, 13}, {"LEFT", 11, 0}, {"RIGHT", 99, 0}} {
		if got := patchCounterpartLine(patch, tc.side, tc.line); got != tc.want {
			t.Errorf("%s %d = %d, want %d", tc.side, tc.line, got, tc.want)
		}
	}
}

func TestTrimReviewDeliveryEventsKeepsComments(t *testing.T) {
	events := []domain.ReviewEvent{}
	for index := range 5 {
		events = append(events, domain.ReviewEvent{ID: fmt.Sprint("delivery", index), Type: "synchronize"})
	}
	events = append(events[:2], append([]domain.ReviewEvent{{ID: "comment", Type: "commented", Remote: &domain.ReviewEventRemote{Provider: "github", Kind: "issue_comment", ID: "1"}}}, events[2:]...)...)
	trimmed := trimReviewDeliveryEvents(events, 2)
	ids := []string{}
	for _, event := range trimmed {
		ids = append(ids, event.ID)
	}
	if strings.Join(ids, ",") != "comment,delivery3,delivery4" {
		t.Fatalf("trimmed = %v", ids)
	}
}

func TestMergeReviewSnapshotAdoptsPendingWriteBack(t *testing.T) {
	now := time.Now().UTC()
	flowUser := domain.User{ID: "u1", Name: "Ada"}
	review := domain.CodeReview{Events: []domain.ReviewEvent{{ID: "local", Type: "commented", Body: "On it", Path: "a.go", Line: 4, Side: "RIGHT", Actor: flowUser, CreatedAt: now,
		Sync: &domain.ReviewEventSync{State: "pending", Op: "create"}}}}
	actor := func(author remoteReviewAuthor) domain.User { return domain.User{ID: "github:" + author.Login} }
	// The webhook for Flow's own comment arrives before the write-back records its id.
	mergeReviewSnapshot(&review, reviewSnapshot{Provider: "github", Comments: []remoteReviewComment{{Kind: reviewRemoteReviewComment, ID: "42", Type: "commented", Body: "On it", Path: "a.go", Line: 4, Side: "RIGHT", Author: remoteReviewAuthor{Login: "flow[bot]"}, CreatedAt: now}}}, actor, now)
	if len(review.Events) != 1 || review.Events[0].ID != "local" || review.Events[0].Remote.ID != "42" || review.Events[0].Sync != nil || review.Events[0].Actor.ID != "u1" {
		t.Fatalf("adopted = %#v", review.Events)
	}
	// The write-back result for an event a sync already confirmed changes nothing.
	applyReviewPushResults(&review, "github", map[string]reviewPushResult{"local": {op: "create", remote: &domain.ReviewEventRemote{Provider: "github", Kind: reviewRemoteReviewComment, ID: "42"}}}, now)
	if len(review.Events) != 1 {
		t.Fatalf("events = %#v", review.Events)
	}
	// A synced duplicate is dropped when the write-back records the same id.
	review = domain.CodeReview{Events: []domain.ReviewEvent{
		{ID: "local", Body: "x", CreatedAt: now, Sync: &domain.ReviewEventSync{State: "pending", Op: "create"}},
		{ID: "github-review_comment-7", Body: "x", CreatedAt: now, Remote: &domain.ReviewEventRemote{Provider: "github", Kind: reviewRemoteReviewComment, ID: "7"}},
		{ID: "reply", ParentID: "github-review_comment-7", CreatedAt: now},
	}}
	applyReviewPushResults(&review, "github", map[string]reviewPushResult{"local": {op: "create", remote: &domain.ReviewEventRemote{Provider: "github", Kind: reviewRemoteReviewComment, ID: "7"}}}, now)
	if len(review.Events) != 2 || review.Events[1].ParentID != "local" || review.Events[0].Remote == nil {
		t.Fatalf("dedupe = %#v", review.Events)
	}
}

func TestGitHubAppWebhookSyncsReviewCommentsAndThreads(t *testing.T) {
	f := newGitHubAppFixture(t)
	f.install()
	var queued []string
	previous := reviewSyncRunner
	reviewSyncRunner = func(func()) { queued = append(queued, "sync") }
	t.Cleanup(func() { reviewSyncRunner = previous })
	if rec := f.webhook("pull_request", "a1", fmt.Sprintf(appPullRequestPayload, "opened", "open", false, false, time.Now().UTC().Format(time.RFC3339))); rec.Code != http.StatusAccepted {
		t.Fatalf("opened: %d %s", rec.Code, rec.Body.String())
	}
	if len(queued) != 1 {
		t.Fatalf("an opened pull request queues a full sync: %v", queued)
	}
	comment := `{"id":501,"path":"cart.go","line":4,"side":"LEFT","body":"Removed on purpose?","user":{"login":"bob"},"created_at":"2026-10-19T10:00:00Z"}`
	payload := `{"action":"%s","installation":{"id":42},"repository":{"id":1,"full_name":"acme/store"},"sender":{"login":"bob","type":"User"},` +
		`"pull_request":{"id":9921,"number":21,"title":"Fix checkout","html_url":"https://github.com/acme/store/pull/21","state":"open","head":{"ref":"flow/fix","sha":"abc"},"base":{"ref":"main","repo":{"full_name":"acme/store"}}},%s}`
	if rec := f.webhook("pull_request_review_comment", "a2", fmt.Sprintf(payload, "created", `"comment":`+comment)); rec.Code != http.StatusAccepted {
		t.Fatalf("comment: %d %s", rec.Code, rec.Body.String())
	}
	// Thread resolution is not an event the App parser acts on; it still syncs.
	if rec := f.webhook("pull_request_review_thread", "a3", fmt.Sprintf(payload, "resolved", `"thread":{"node_id":"PRRT_1","comments":[`+comment+`]}`)); rec.Code != http.StatusAccepted {
		t.Fatalf("thread: %d %s", rec.Code, rec.Body.String())
	}
	review := f.repo.Bootstrap().Reviews[0]
	event, ok := reviewEventByID(review, "github-review_comment-501")
	if !ok || event.Side != "LEFT" || event.Line != 4 || !event.Resolved || event.Remote.ThreadID != "PRRT_1" {
		t.Fatalf("app comment sync = %#v (ok=%v)", event, ok)
	}
	if slices.ContainsFunc(review.Events, func(item domain.ReviewEvent) bool { return item.Type == "resolved" }) {
		t.Fatal("thread resolution must not add a delivery event")
	}
}

type scriptedReviewRemote struct {
	name  string
	err   error
	calls *[]string
}

func (r scriptedReviewRemote) Provider() string { return "github" }
func (r scriptedReviewRemote) REST(context.Context, string, string, any, any) error {
	*r.calls = append(*r.calls, r.name)
	return r.err
}
func (r scriptedReviewRemote) GraphQL(context.Context, string, map[string]any, any) error {
	*r.calls = append(*r.calls, r.name)
	return r.err
}

func TestFallbackReviewRemoteUsesTheFirstAllowedCredential(t *testing.T) {
	calls := []string{}
	remote := &fallbackRemote{remotes: []reviewRemote{
		scriptedReviewRemote{name: "app", err: &reviewRemoteError{Provider: "github", Status: http.StatusNotFound}, calls: &calls},
		scriptedReviewRemote{name: "classic", calls: &calls},
	}}
	if err := remote.REST(t.Context(), http.MethodGet, "/pulls/1", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := remote.GraphQL(t.Context(), "query", nil, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "app,classic,classic" {
		t.Fatalf("calls = %v", calls)
	}
	failing := &fallbackRemote{remotes: []reviewRemote{scriptedReviewRemote{name: "app", err: &reviewRemoteError{Provider: "github", Status: http.StatusBadGateway}, calls: &calls}, scriptedReviewRemote{name: "classic", calls: &calls}}}
	if err := failing.REST(t.Context(), http.MethodPost, "/x", nil, nil); reviewRemoteStatus(err) != http.StatusBadGateway {
		t.Fatalf("a non-permission failure must not fall back (a write may have happened): %v", err)
	}
}
