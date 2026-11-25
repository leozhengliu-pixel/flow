package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (f *memberGitLab) putsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.puts)
}

// reviewDecisionStatus runs a review action and returns the status and body.
func reviewDecisionCall(f *memberAccountsFixture, id, action string) *httptest.ResponseRecorder {
	switch action {
	case "approve", "requestChanges":
		return f.do(http.MethodPost, "/api/reviews/"+id+"/submit?workspace=test-workspace", map[string]string{"decision": action})
	default:
		status := map[string]string{"merge": "merged", "close": "closed"}[action]
		return f.do(http.MethodPatch, "/api/reviews/"+id+"?workspace=test-workspace", map[string]string{"status": status})
	}
}

func requireAccountRequired(t *testing.T, rec *httptest.ResponseRecorder, label string) {
	t.Helper()
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusForbidden || body["code"] != reviewAccountRequiredCode || !strings.Contains(body["error"], "account") {
		t.Fatalf("%s: %d %s", label, rec.Code, rec.Body.String())
	}
}

// Approving, requesting changes, merging and closing run as the member's own
// GitLab account only. Without one, the workspace token never stands in and
// the host receives nothing.
func TestGitLabReviewDecisionsNeedTheMembersOwnAccount(t *testing.T) {
	f := newMemberAccountsFixture(t)
	review := f.addReview()
	before := len(f.gitlab.seenSnapshot())
	for _, action := range []string{"approve", "requestChanges", "merge", "close"} {
		requireAccountRequired(t, reviewDecisionCall(f, review.ID, action), action)
	}
	if after := len(f.gitlab.seenSnapshot()); after != before {
		t.Fatalf("the host was called without a linked account: %v", f.gitlab.seenSnapshot()[before:])
	}
	if posts, puts := f.gitlab.postsSnapshot(), f.gitlab.putsSnapshot(); len(posts)+len(puts) != 0 {
		t.Fatalf("posts %v puts %v", posts, puts)
	}
	// Comments keep the workspace fallback.
	updated := requestJSON[domain.CodeReview](t, f.handler, http.MethodPost, "/api/reviews/"+review.ID+"/comments?workspace=test-workspace", map[string]string{"body": "Still fine"}, http.StatusCreated)
	if last := lastReviewEvent(updated); last.PostedAs == nil || last.PostedAs.Via != domain.CodeHostAccountActorBot {
		t.Fatalf("comment fallback %+v", last.PostedAs)
	}
}

func TestGitLabReviewDecisionsRunAsTheLinkedMember(t *testing.T) {
	f := newMemberAccountsFixture(t)
	review := f.addReview()
	if rec := f.linkPAT("glpat-member"); rec.Code != http.StatusOK {
		t.Fatalf("link: %d %s", rec.Code, rec.Body.String())
	}
	if rec := reviewDecisionCall(f, review.ID, "approve"); rec.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body.String())
	}
	if rec := reviewDecisionCall(f, review.ID, "merge"); rec.Code != http.StatusOK {
		t.Fatalf("merge: %d %s", rec.Code, rec.Body.String())
	}
	if posts := f.gitlab.postsSnapshot(); !slices.Equal(posts, []string{"approve glpat-member"}) {
		t.Fatalf("posts %v", posts)
	}
	if puts := f.gitlab.putsSnapshot(); !slices.Equal(puts, []string{"/merge glpat-member"}) {
		t.Fatalf("puts %v", puts)
	}
}

// A token the host rejects (401) asks for a reconnect; the workspace token
// does not take over approvals or merges.
func TestGitLabReviewDecisionsDoNotFallBackOnARejectedToken(t *testing.T) {
	f := newMemberAccountsFixture(t)
	review := f.addReview()
	if rec := f.linkPAT("glpat-member"); rec.Code != http.StatusOK {
		t.Fatalf("link: %d %s", rec.Code, rec.Body.String())
	}
	f.gitlab.mu.Lock()
	delete(f.gitlab.scopes, "glpat-member")
	f.gitlab.mu.Unlock()
	for _, action := range []string{"approve", "merge"} {
		rec := reviewDecisionCall(f, review.ID, action)
		requireAccountRequired(t, rec, action)
		// The first rejection marks the account "reconnect"; afterwards it is
		// simply not usable and the member is asked to connect it again.
		if action == "approve" && !strings.Contains(rec.Body.String(), "Reconnect") {
			t.Fatalf("%s: no reconnect hint: %s", action, rec.Body.String())
		}
	}
	if posts, puts := f.gitlab.postsSnapshot(), f.gitlab.putsSnapshot(); len(posts)+len(puts) != 0 {
		t.Fatalf("workspace token used: posts %v puts %v", posts, puts)
	}
}

// The host's refusal of the member (403) comes back as a refusal.
func TestGitLabReviewDecisionsPassTheHostRefusalThrough(t *testing.T) {
	f := newMemberAccountsFixture(t)
	review := f.addReview()
	if rec := f.linkPAT("glpat-member"); rec.Code != http.StatusOK {
		t.Fatalf("link: %d %s", rec.Code, rec.Body.String())
	}
	f.gitlab.mu.Lock()
	f.gitlab.forbidden["glpat-member"] = true
	f.gitlab.mu.Unlock()
	for _, action := range []string{"approve", "merge"} {
		if rec := reviewDecisionCall(f, review.ID, action); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "can't make this change") {
			t.Fatalf("%s: %d %s", action, rec.Code, rec.Body.String())
		}
	}
	if posts, puts := f.gitlab.postsSnapshot(), f.gitlab.putsSnapshot(); len(posts)+len(puts) != 0 {
		t.Fatalf("posts %v puts %v", posts, puts)
	}
}

// Approval and requested changes are host decisions: a PATCH cannot set them.
func TestReviewPatchCannotSetDecisionStates(t *testing.T) {
	f := newMemberAccountsFixture(t)
	review := f.addReview()
	for _, status := range []string{"approved", "inReview"} {
		rec := f.do(http.MethodPatch, "/api/reviews/"+review.ID+"?workspace=test-workspace", map[string]string{"status": status})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", status, rec.Code, rec.Body.String())
		}
	}
	data, _ := f.repo.WorkspaceMetadata("test-workspace")
	if got := data.Reviews[len(data.Reviews)-1]; got.ID != review.ID || got.Status != "open" {
		t.Fatalf("review %s is %s", got.ID, got.Status)
	}
	// The status it already has, and plain metadata edits, still work.
	if rec := f.do(http.MethodPatch, "/api/reviews/"+review.ID+"?workspace=test-workspace", map[string]any{"title": "Renamed"}); rec.Code != http.StatusOK {
		t.Fatalf("metadata edit: %d %s", rec.Code, rec.Body.String())
	}
}

// fakeGitHubReviews is a GitHub API double for member review actions.
type fakeGitHubReviews struct {
	*httptest.Server
	mu      sync.Mutex
	calls   []string // "METHOD path token"
	invalid map[string]bool
	denied  map[string]bool
}

func (g *fakeGitHubReviews) snapshot() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.calls)
}

func newFakeGitHubReviews(t *testing.T) *fakeGitHubReviews {
	g := &fakeGitHubReviews{invalid: map[string]bool{}, denied: map[string]bool{}}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		g.calls = append(g.calls, r.Method+" "+r.URL.Path+" "+token)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case g.invalid[token]:
			w.WriteHeader(http.StatusUnauthorized)
		case g.denied[token]:
			w.WriteHeader(http.StatusForbidden)
		case r.Method == http.MethodPut && r.URL.Path == "/repos/owner/repo/pulls/5/merge":
			_, _ = w.Write([]byte(`{"merged":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/owner/repo/pulls/5/reviews":
			_, _ = w.Write([]byte(`{"id":11}`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/owner/repo/issues/5/comments":
			_, _ = w.Write([]byte(`{"id":12}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(g.Close)
	return g
}

type gitHubReviewFixture struct {
	t       *testing.T
	github  *fakeGitHubReviews
	handler http.Handler
	repo    *store.SQLiteStore
	review  domain.CodeReview
	viewer  domain.User
	server  *server
	host    string
}

func newGitHubReviewFixture(t *testing.T) *gitHubReviewFixture {
	t.Helper()
	enableConnectorSecrets(t)
	github := newFakeGitHubReviews(t)
	host := strings.TrimPrefix(github.URL, "http://")
	t.Setenv("FLOW_INTEGRATION_GITHUB_CLIENT_ID", "cid")
	t.Setenv("FLOW_INTEGRATION_GITHUB_CLIENT_SECRET", "csecret")
	t.Setenv("FLOW_INTEGRATION_GITHUB_AUTHORIZATION_URL", github.URL+"/login/oauth/authorize")
	t.Setenv("FLOW_INTEGRATION_GITHUB_TOKEN_URL", github.URL+"/login/oauth/access_token")
	t.Setenv("FLOW_INTEGRATION_GITHUB_API_URL", github.URL)
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	seedIntegrationOAuthTokens(t, repository, "test-workspace", "github", "workspace-token", "")
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	f := &gitHubReviewFixture{t: t, github: github, handler: newHandler(s), repo: repository, viewer: repository.Account().Viewer, server: s}
	if err := repository.MutateWorkspace(workspaceFixtureContext(t.Context()), "test-workspace", "test.review", "", nil, func(data *domain.Bootstrap) error {
		data.IntegrationConnections = []domain.IntegrationConnection{{ID: "github", Provider: "github", OAuthAuthorized: true, Status: "connected", Config: map[string]string{"apiUrl": github.URL, "organization": "owner"}}}
		review := data.Reviews[0]
		review.ID, review.SlugID, review.Provider, review.RepositoryOwner, review.RepositoryName, review.Number, review.Status = "review_gh", "github-5", "github", "owner", "repo", 5, "open"
		review.URL, review.Draft = github.URL+"/owner/repo/pull/5", false
		data.Reviews = append(data.Reviews, review)
		f.review = review
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.host = host
	return f
}

func (f *gitHubReviewFixture) link(token string) {
	f.t.Helper()
	account, err := f.repo.UpsertCodeHostAccount(f.t.Context(), domain.CodeHostAccount{ID: "cha_gh_member", UserID: f.viewer.ID, CodeHost: domain.CodeHostGitHub, HostName: f.host, BaseURL: f.github.URL, ExternalUserID: "7", Login: "alice", AuthType: domain.CodeHostAuthOAuth})
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.server.writeCodeHostTokens(f.t.Context(), account.ID, codeHostAccountTokens{AccessToken: token, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *gitHubReviewFixture) call(action string) *httptest.ResponseRecorder {
	f.t.Helper()
	m := &memberAccountsFixture{t: f.t, handler: f.handler}
	return reviewDecisionCall(m, f.review.ID, action)
}

func TestGitHubReviewDecisionsNeedTheMembersOwnAccount(t *testing.T) {
	f := newGitHubReviewFixture(t)
	for _, action := range []string{"approve", "requestChanges", "merge", "close"} {
		requireAccountRequired(t, f.call(action), action)
	}
	if calls := f.github.snapshot(); len(calls) != 0 {
		t.Fatalf("the host was called without a linked account: %v", calls)
	}
	// Comments keep the workspace fallback.
	m := &memberAccountsFixture{t: t, handler: f.handler}
	if rec := m.do(http.MethodPost, "/api/reviews/"+f.review.ID+"/submit?workspace=test-workspace", map[string]string{"decision": "comment", "body": "Looks fine"}); rec.Code != http.StatusOK {
		t.Fatalf("comment: %d %s", rec.Code, rec.Body.String())
	}
	if calls := f.github.snapshot(); !slices.Equal(calls, []string{"POST /repos/owner/repo/issues/5/comments workspace-token"}) {
		t.Fatalf("calls %v", calls)
	}
}

func TestGitHubReviewDecisionsRunAsTheLinkedMember(t *testing.T) {
	f := newGitHubReviewFixture(t)
	f.link("ghu_member")
	for _, action := range []string{"approve", "merge"} {
		if rec := f.call(action); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", action, rec.Code, rec.Body.String())
		}
	}
	want := []string{"POST /repos/owner/repo/pulls/5/reviews ghu_member", "PUT /repos/owner/repo/pulls/5/merge ghu_member"}
	if calls := f.github.snapshot(); !slices.Equal(calls, want) {
		t.Fatalf("calls %v, want %v", calls, want)
	}
}

func TestGitHubReviewDecisionsDoNotFallBackOnARejectedToken(t *testing.T) {
	f := newGitHubReviewFixture(t)
	f.link("ghu_member")
	f.github.invalid["ghu_member"] = true
	for _, action := range []string{"approve", "merge"} {
		requireAccountRequired(t, f.call(action), action)
	}
	for _, call := range f.github.snapshot() {
		if strings.HasSuffix(call, "workspace-token") {
			t.Fatalf("workspace token used: %v", f.github.snapshot())
		}
	}
}

func TestGitHubReviewDecisionsPassTheHostRefusalThrough(t *testing.T) {
	f := newGitHubReviewFixture(t)
	f.link("ghu_member")
	f.github.denied["ghu_member"] = true
	for _, action := range []string{"approve", "merge"} {
		if rec := f.call(action); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %d %s", action, rec.Code, rec.Body.String())
		}
	}
	for _, call := range f.github.snapshot() {
		if strings.HasSuffix(call, "workspace-token") {
			t.Fatalf("workspace token used: %v", f.github.snapshot())
		}
	}
}

func TestGitHubReviewPatchCannotSetDecisionStates(t *testing.T) {
	f := newGitHubReviewFixture(t)
	m := &memberAccountsFixture{t: t, handler: f.handler}
	for _, status := range []string{"approved", "inReview"} {
		if rec := m.do(http.MethodPatch, "/api/reviews/"+f.review.ID+"?workspace=test-workspace", map[string]string{"status": status}); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", status, rec.Code, rec.Body.String())
		}
	}
}
