package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/codehost"
	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// memberGitLab is a fake GitLab instance for members' account links: OAuth
// (authorization code with PKCE, refresh), personal access tokens with
// scopes, the user endpoint and merge request notes and approvals.
type memberGitLab struct {
	*httptest.Server
	mu        sync.Mutex
	challenge string
	scopes    map[string][]string // personal access token → scopes
	oauth     map[string]bool     // valid OAuth access tokens
	refresh   map[string]bool     // valid refresh tokens
	forbidden map[string]bool     // tokens refused (403) on approvals
	readOnly  map[string]bool     // tokens refused (403) on every write
	expiresIn int64
	issued    int
	// refreshDelay holds refresh responses back, so concurrent refreshes
	// overlap at the host.
	refreshDelay time.Duration
	refreshes    int      // refresh_token grants answered (accepted or not)
	posts        []string // "path auth-token"
	seen         []string
}

const testWorkspaceToken = "glpat-workspace"

func newMemberGitLab(t *testing.T) *memberGitLab {
	t.Helper()
	fake := &memberGitLab{scopes: map[string][]string{testWorkspaceToken: {"api"}, "glpat-member": {"api", "read_user"}, "glpat-noscope": {"read_api"}}, oauth: map[string]bool{}, refresh: map[string]bool{}, forbidden: map[string]bool{}, readOnly: map[string]bool{}, expiresIn: 7200}
	fake.Server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.Close)
	return fake
}

func (f *memberGitLab) token(r *http.Request) (string, bool) {
	if token := r.Header.Get("PRIVATE-TOKEN"); token != "" {
		_, ok := f.scopes[token]
		return token, ok
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return token, f.oauth[token]
}

func (f *memberGitLab) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/oauth/token" && f.refreshDelay > 0 {
		time.Sleep(f.refreshDelay)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("PRIVATE-TOKEN"))
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/oauth/token" {
		_ = r.ParseForm()
		if r.Form.Get("client_id") != "app-1" || r.Form.Get("client_secret") != "app-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if r.Form.Get("code") != "good" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge || r.Form.Get("redirect_uri") != "https://flow.example/api/account/code-host-accounts/oauth/callback" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
		case "refresh_token":
			f.refreshes++
			if !f.refresh[r.Form.Get("refresh_token")] {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			delete(f.refresh, r.Form.Get("refresh_token"))
		default:
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.issued++
		access, refresh := "gloa_access_"+string(rune('0'+f.issued)), "glr_"+string(rune('0'+f.issued))
		f.oauth[access], f.refresh[refresh] = true, true
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": access, "refresh_token": refresh, "expires_in": f.expiresIn, "scope": "api read_user", "token_type": "Bearer"})
		return
	}
	token, ok := f.token(r)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"401 Unauthorized"}`))
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v4/personal_access_tokens/self":
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "scopes": f.scopes[token], "active": true})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v4/user":
		username := "alice-gl"
		if token == testWorkspaceToken {
			username = "flow-bot"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 42, "username": username, "commit_email": ""})
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v4/projects/acme/app/merge_requests/3/"):
		if (strings.HasSuffix(r.URL.Path, "/approve") && f.forbidden[token]) || f.readOnly[token] {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		f.posts = append(f.posts, strings.TrimPrefix(r.URL.Path, "/api/v4/projects/acme/app/merge_requests/3/")+" "+token)
		w.WriteHeader(http.StatusCreated)
		if strings.HasSuffix(r.URL.Path, "/discussions") {
			// Comments are written back as discussions (threaded on GitLab).
			_, _ = w.Write([]byte(fmt.Sprintf(`{"id":"d%d","notes":[{"id":%d}]}`, len(f.posts), len(f.posts))))
			return
		}
		_, _ = w.Write([]byte(fmt.Sprintf(`{"id":%d}`, len(f.posts))))
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v4/projects/acme/app/merge_requests/3/discussions/"):
		// Thread resolution.
		if f.readOnly[token] {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *memberGitLab) seenSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.seen)
}

func (f *memberGitLab) postsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.posts)
}

type memberAccountsFixture struct {
	t       *testing.T
	repo    *store.SQLiteStore
	server  *server
	handler http.Handler
	gitlab  *memberGitLab
	viewer  domain.User
	conn    string
	dbPath  string
}

func newMemberAccountsFixture(t *testing.T) *memberAccountsFixture {
	t.Helper()
	enableConnectorSecrets(t)
	t.Setenv("FLOW_APP_URL", "https://flow.example")
	dbPath := filepath.Join(t.TempDir(), "flow.db")
	repository, err := store.OpenSQLiteTestFixture(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	f := &memberAccountsFixture{t: t, repo: repository, server: s, handler: newHandler(s), gitlab: newMemberGitLab(t), viewer: repository.Account().Viewer, dbPath: dbPath}
	connection := requestJSON[domain.IntegrationConnection](t, f.handler, http.MethodPut, "/api/integrations/gitlab?workspace=test-workspace", map[string]any{"name": "GitLab", "config": map[string]string{"apiToken": testWorkspaceToken, "host": f.gitlab.URL}}, http.StatusOK)
	f.conn = connection.ID
	return f
}

func (f *memberAccountsFixture) do(method, target string, input any) *httptest.ResponseRecorder {
	var body *bytes.Reader
	if input != nil {
		raw, _ := json.Marshal(input)
		body = bytes.NewReader(raw)
	} else {
		body = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, target, body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	return recorder
}

func (f *memberAccountsFixture) accounts() codeHostAccountsResponse {
	f.t.Helper()
	rec := f.do(http.MethodGet, "/api/account/code-host-accounts?workspace=test-workspace", nil)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	for _, secret := range []string{"gloa_", "glr_", "glpat-member", "app-secret", "ghu_"} {
		if strings.Contains(rec.Body.String(), secret) {
			f.t.Fatalf("connected accounts response leaks %s: %s", secret, rec.Body.String())
		}
	}
	var response codeHostAccountsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &response)
	return response
}

func (f *memberAccountsFixture) auditActions() []string {
	data, _ := f.repo.WorkspaceMetadataFields("test-workspace", "auditLog")
	actions := []string{}
	for _, entry := range data.AuditLog {
		if entry.ResourceType == "code_host_account" {
			actions = append(actions, entry.Action)
		}
	}
	return actions
}

func (f *memberAccountsFixture) linkPAT(token string) *httptest.ResponseRecorder {
	return f.do(http.MethodPost, "/api/account/code-host-accounts/gitlab/token?workspace=test-workspace", map[string]string{"instanceId": f.conn, "token": token})
}

// The OAuth flow: the admin registers the application, the member is sent
// to the admin-configured instance with a one-time state and a PKCE
// challenge, and the callback stores the account with sealed tokens that
// refresh and turn into "reconnect" once revoked.
func TestGitLabMemberOAuthLinkFlow(t *testing.T) {
	f := newMemberAccountsFixture(t)
	view := requestJSON[gitlabMemberOAuthApplicationView](t, f.handler, http.MethodGet, "/api/integrations/gitlab/"+f.conn+"/oauth-application?workspace=test-workspace", nil, http.StatusOK)
	if view.Configured || view.RedirectURL != "https://flow.example/api/account/code-host-accounts/oauth/callback" || strings.Join(view.Scopes, " ") != "api read_user" {
		t.Fatalf("oauth application view %+v", view)
	}
	rec := f.do(http.MethodPut, "/api/integrations/gitlab/"+f.conn+"/oauth-application?workspace=test-workspace", map[string]string{"applicationId": "app-1", "secret": "app-secret"})
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "app-secret") {
		t.Fatalf("save oauth application: %d %s", rec.Code, rec.Body.String())
	}
	instances := f.accounts().GitLabInstances
	if len(instances) != 1 || !instances[0].OAuthAvailable || instances[0].BaseURL != f.gitlab.URL {
		t.Fatalf("instances %+v", instances)
	}
	start := requestJSON[map[string]string](t, f.handler, http.MethodPost, "/api/account/code-host-accounts/gitlab/authorize?workspace=test-workspace", map[string]string{"instanceId": f.conn}, http.StatusOK)
	authorize, err := url.Parse(start["url"])
	if err != nil || !strings.HasPrefix(start["url"], f.gitlab.URL+"/oauth/authorize?") {
		t.Fatalf("authorize URL %q", start["url"])
	}
	query := authorize.Query()
	if query.Get("client_id") != "app-1" || query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" || !strings.HasPrefix(query.Get("state"), codeHostLinkStatePrefix) || query.Get("scope") != "api read_user" {
		t.Fatalf("authorize query %v", query)
	}
	f.gitlab.challenge = query.Get("code_challenge")
	f.gitlab.expiresIn = 60 // expires inside the refresh margin
	state := url.QueryEscape(query.Get("state"))
	if rec := f.do(http.MethodGet, codeHostLinkCallbackPath+"?code=good&state="+state, nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"linked"`) {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(http.MethodGet, codeHostLinkCallbackPath+"?code=good&state="+state, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("a state must be usable once: %d %s", rec.Code, rec.Body.String())
	}
	accounts := f.accounts().Accounts
	if len(accounts) != 1 || accounts[0].Login != "alice-gl" || accounts[0].AuthType != domain.CodeHostAuthOAuth || !accounts[0].CanAct || accounts[0].ConnectionID != f.conn || accounts[0].ExternalUserID != "42" {
		t.Fatalf("accounts %+v", accounts)
	}
	if actions := f.auditActions(); !slices.Contains(actions, "code_host_account.linked") {
		t.Fatalf("audit %v", actions)
	}
	account, err := f.repo.CodeHostAccount(t.Context(), f.viewer.ID, accounts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	f.gitlab.expiresIn = 7200
	tokens, err := f.server.memberHostTokens(t.Context(), account)
	if err != nil || tokens.AccessToken != "gloa_access_2" || tokens.RefreshToken != "glr_2" {
		t.Fatalf("refresh %+v %v", tokens, err)
	}
	// A revoked grant: the refresh fails and the account asks to reconnect.
	_ = f.server.writeCodeHostTokens(t.Context(), account.ID, codeHostAccountTokens{AccessToken: "gloa_access_2", RefreshToken: "glr_revoked", ExpiresAt: time.Now()})
	if _, err := f.server.memberHostTokens(t.Context(), account); !errors.Is(err, codehost.ErrUserNotLinked) {
		t.Fatalf("revoked refresh → not linked, got %v", err)
	}
	if accounts := f.accounts().Accounts; accounts[0].Status != domain.CodeHostAccountReconnect || accounts[0].CanAct {
		t.Fatalf("revoked account %+v", accounts[0])
	}
}

// A state started by one member cannot be finished by another.
func TestCodeHostLinkStateBelongsToItsMember(t *testing.T) {
	f := newMemberAccountsFixture(t)
	state, _, err := f.server.newCodeHostLinkState(t.Context(), codeHostLinkState{UserID: "someone-else", Workspace: "test-workspace", CodeHost: domain.CodeHostGitLab, Method: domain.CodeHostAuthOAuth, ConnectionID: f.conn, BaseURL: f.gitlab.URL})
	if err != nil {
		t.Fatal(err)
	}
	rec := f.do(http.MethodGet, codeHostLinkCallbackPath+"?code=good&state="+url.QueryEscape(state), nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "another user") {
		t.Fatalf("foreign state: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(http.MethodGet, codeHostLinkCallbackPath+"?code=good&state=forged", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("forged state: %d", rec.Code)
	}
}

// Personal access tokens: validated against the instance, read_user and api
// required (missing ones listed), only instances the workspace connected.
func TestGitLabMemberPersonalAccessToken(t *testing.T) {
	f := newMemberAccountsFixture(t)
	if rec := f.do(http.MethodPost, "/api/account/code-host-accounts/gitlab/authorize?workspace=test-workspace", map[string]string{"instanceId": f.conn}); rec.Code != http.StatusConflict {
		t.Fatalf("OAuth without an application: %d", rec.Code)
	}
	rec := f.linkPAT("glpat-noscope")
	var missing struct {
		Error         string   `json:"error"`
		MissingScopes []string `json:"missingScopes"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &missing)
	if rec.Code != http.StatusUnprocessableEntity || strings.Join(missing.MissingScopes, ",") != "api,read_user" {
		t.Fatalf("missing scopes: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.linkPAT("glpat-unknown"); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid token: %d %s", rec.Code, rec.Body.String())
	}
	before := len(f.gitlab.seen)
	for _, body := range []map[string]string{{"instanceId": "integration_elsewhere", "token": "glpat-member"}, {"instanceId": "", "token": "glpat-member", "host": "https://evil.example"}} {
		if rec := f.do(http.MethodPost, "/api/account/code-host-accounts/gitlab/token?workspace=test-workspace", body); rec.Code != http.StatusNotFound {
			t.Fatalf("unconnected instance %v: %d", body, rec.Code)
		}
	}
	if len(f.gitlab.seen) != before {
		t.Fatal("a token was sent for an instance the workspace did not connect")
	}
	rec = f.linkPAT("glpat-member")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "glpat-member") {
		t.Fatalf("link: %d %s", rec.Code, rec.Body.String())
	}
	accounts := f.accounts().Accounts
	if len(accounts) != 1 || accounts[0].AuthType != domain.CodeHostAuthPAT || accounts[0].Login != "alice-gl" || !accounts[0].CanAct {
		t.Fatalf("accounts %+v", accounts)
	}
	host := strings.TrimPrefix(f.gitlab.URL, "http://")
	repo := codehost.RepoRef{CodeHost: domain.CodeHostGitLab, HostName: host, FullName: "acme/app"}
	members := codingMemberHosts{s: f.server}
	// The git relay (server side only) may push with the PAT; it is never
	// served to a sandbox (coding_git_relay_test.go).
	credential, err := members.MemberGitCredential(t.Context(), "test-workspace", f.viewer.ID, repo)
	if err != nil || credential.Password != "glpat-member" || credential.Username != "alice-gl" || !credential.ExpiresAt.IsZero() || credential.CLIEnv != nil {
		t.Fatalf("relay credential for a personal access token: %+v %v", credential, err)
	}
	if _, err := members.MemberGitCredential(t.Context(), "test-workspace", "another-member", repo); !errors.Is(err, codehost.ErrUserNotLinked) {
		t.Fatalf("another member got this member's PAT for git: %v", err)
	}
	actor, err := members.MemberActor(t.Context(), "test-workspace", f.viewer.ID, repo)
	if err != nil || actor == nil || actor.Token != "glpat-member" || actor.Bearer || actor.FlowUserID != f.viewer.ID {
		t.Fatalf("member actor %+v %v", actor, err)
	}
	if actor, _ := members.MemberActor(t.Context(), "test-workspace", "another-member", repo); actor != nil {
		t.Fatal("another member got this member's token")
	}
	identity, ok, _ := members.MemberAccount(t.Context(), "test-workspace", f.viewer.ID, domain.CodeHostGitLab, host)
	if !ok || identity.Email != "42-alice-gl@users.noreply.127.0.0.1" || identity.Name == "" {
		t.Fatalf("member identity %+v", identity)
	}
	// The admin points the connection at another host: the link no longer
	// applies there.
	if err := f.repo.MutateWorkspace(t.Context(), "test-workspace", "test.move_gitlab", f.conn, nil, func(data *domain.Bootstrap) error {
		for index := range data.IntegrationConnections {
			if data.IntegrationConnections[index].ID == f.conn {
				data.IntegrationConnections[index].Config["host"] = "https://gitlab.other.example"
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := f.server.workspaceMemberAccount(t.Context(), "test-workspace", f.viewer.ID, domain.CodeHostGitLab, "gitlab.other.example"); ok {
		t.Fatal("a link made for one instance was used on another host")
	}
	if rec := f.do(http.MethodDelete, "/api/account/code-host-accounts/"+accounts[0].ID+"?workspace=test-workspace", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("unlink: %d", rec.Code)
	}
	if accounts := f.accounts().Accounts; len(accounts) != 0 {
		t.Fatalf("after unlink %+v", accounts)
	}
	if _, err := f.server.readCodeHostTokens(t.Context(), accounts[0].ID); err == nil {
		t.Fatal("unlink must delete the tokens")
	}
	if actions := f.auditActions(); !slices.Contains(actions, "code_host_account.unlinked") || !slices.Contains(actions, "code_host_account.linked") {
		t.Fatalf("audit %v", actions)
	}
	if rec := f.do(http.MethodDelete, "/api/account/code-host-accounts/"+accounts[0].ID, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("second unlink: %d", rec.Code)
	}
}

func (f *memberAccountsFixture) addReview() domain.CodeReview {
	f.t.Helper()
	var review domain.CodeReview
	if err := f.repo.MutateWorkspace(f.t.Context(), "test-workspace", "test.review", "", nil, func(data *domain.Bootstrap) error {
		review = data.Reviews[0]
		review.ID, review.SlugID, review.Provider, review.RepositoryOwner, review.RepositoryName, review.Number, review.Status = "review_member", "gitlab-3", "gitlab", "acme", "app", 3, "open"
		review.URL, review.Draft = f.gitlab.URL+"/acme/app/-/merge_requests/3", false
		data.Reviews = append(data.Reviews, review)
		return nil
	}); err != nil {
		f.t.Fatal(err)
	}
	return review
}

// Reviews act as the member with their linked account, fall back to the
// workspace integration (and say so) without one or when the member token
// is revoked, and never escalate past a member's own permissions.
func TestReviewActionsActAsTheMember(t *testing.T) {
	f := newMemberAccountsFixture(t)
	review := f.addReview()
	if rec := f.linkPAT("glpat-member"); rec.Code != http.StatusOK {
		t.Fatalf("link: %d %s", rec.Code, rec.Body.String())
	}
	updated := requestJSON[domain.CodeReview](t, f.handler, http.MethodPost, "/api/reviews/"+review.ID+"/comments?workspace=test-workspace", map[string]string{"body": "Looks good"}, http.StatusCreated)
	last := updated.Events[len(updated.Events)-1]
	if last.PostedAs == nil || last.PostedAs.Via != domain.CodeHostAccountActorMember || last.PostedAs.Login != "alice-gl" || last.PostedAs.Provider != "gitlab" {
		t.Fatalf("posted as %+v", last.PostedAs)
	}
	if last.Sync != nil || last.Remote == nil || last.Remote.ThreadID == "" {
		t.Fatalf("comment not written back: sync %+v remote %+v", last.Sync, last.Remote)
	}
	if posts := f.gitlab.postsSnapshot(); len(posts) != 1 || posts[0] != "discussions glpat-member" {
		t.Fatalf("posts %v", posts)
	}
	// Another member without a link: the workspace integration posts.
	data, _ := f.repo.WorkspaceMetadata("test-workspace")
	other := domain.User{ID: "member-without-link", Name: "Other"}
	postedAs, err := f.server.postReviewActivity(t.Context(), data, review, other, "comment", "From the workspace")
	if err != nil || postedAs == nil || postedAs.Via != domain.CodeHostAccountActorBot || postedAs.Login != "flow-bot" {
		t.Fatalf("workspace fallback %+v %v", postedAs, err)
	}
	if posts := f.gitlab.postsSnapshot(); posts[len(posts)-1] != "notes "+testWorkspaceToken {
		t.Fatalf("posts %v", posts)
	}
	// The member lacks approval rights on GitLab: refused, not escalated.
	f.gitlab.mu.Lock()
	f.gitlab.forbidden["glpat-member"] = true
	f.gitlab.mu.Unlock()
	if rec := f.do(http.MethodPost, "/api/reviews/"+review.ID+"/submit?workspace=test-workspace", map[string]string{"decision": "approve"}); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "can't make this change") {
		t.Fatalf("forbidden approval: %d %s", rec.Code, rec.Body.String())
	}
	if posts := f.gitlab.postsSnapshot(); slices.Contains(posts, "approve "+testWorkspaceToken) {
		t.Fatal("a refused member action was retried with the workspace token")
	}
	// The member revokes the token on GitLab: Flow falls back to the
	// workspace integration and asks the member to reconnect.
	f.gitlab.mu.Lock()
	delete(f.gitlab.scopes, "glpat-member")
	f.gitlab.mu.Unlock()
	updated = requestJSON[domain.CodeReview](t, f.handler, http.MethodPost, "/api/reviews/"+review.ID+"/comments?workspace=test-workspace", map[string]string{"body": "Again"}, http.StatusCreated)
	if last := updated.Events[len(updated.Events)-1]; last.PostedAs == nil || last.PostedAs.Via != domain.CodeHostAccountActorBot {
		t.Fatalf("revoked member fallback %+v", last.PostedAs)
	}
	if accounts := f.accounts().Accounts; accounts[0].Status != domain.CodeHostAccountReconnect {
		t.Fatalf("revoked token must ask to reconnect: %+v", accounts[0])
	}
}

// Host activity of a member's linked login is attributed to that member
// (reviews, reviewers, coding session commenters); a login linked by
// someone outside the workspace is not.
func TestLinkedAccountsAttributeHostActivity(t *testing.T) {
	f := newMemberAccountsFixture(t)
	data, _ := f.repo.WorkspaceMetadata("test-workspace")
	var member domain.User
	for _, user := range data.Users {
		if user.ID != f.viewer.ID && !user.App {
			member = user
			break
		}
	}
	host := strings.TrimPrefix(f.gitlab.URL, "http://")
	for userID, login := range map[string]string{member.ID: "dana-gl", "outsider": "mallory-gl"} {
		if _, err := f.repo.UpsertCodeHostAccount(t.Context(), domain.CodeHostAccount{ID: "cha_" + login, UserID: userID, CodeHost: domain.CodeHostGitLab, HostName: host, BaseURL: f.gitlab.URL, ExternalUserID: "9", Login: login, AuthType: domain.CodeHostAuthPAT}); err != nil {
			t.Fatal(err)
		}
	}
	var event externalCodeReviewEvent
	event.Action, event.Number, event.Title = "open", 5, "Add feature"
	event.User.Username = "Dana-GL"
	event.ObjectAttributes.ID, event.ObjectAttributes.IID, event.ObjectAttributes.Title, event.ObjectAttributes.URL = 905, 5, "Add feature", f.gitlab.URL+"/acme/app/-/merge_requests/5"
	event.ReviewersGitlab = []struct {
		Username string `json:"username"`
	}{{Username: "dana-gl"}}
	review, err := f.server.applyCodeReviewEvent(t.Context(), "test-workspace", "gitlab", "delivery-1", event)
	if err != nil || review.Author.ID != member.ID || !slices.Contains(review.ReviewerIDs, member.ID) {
		t.Fatalf("attribution: author %s reviewers %v, want %s: %v", review.Author.ID, review.ReviewerIDs, member.ID, err)
	}
	if last := review.Events[len(review.Events)-1]; last.Actor.ID != member.ID {
		t.Fatalf("sender %s", last.Actor.ID)
	}
	commenter, err := codingCommenterResolver{s: f.server}.ResolveCommenter(t.Context(), "test-workspace", domain.CodeHostGitLab, host, "dana-gl", "")
	if err != nil || commenter.UserID != member.ID {
		t.Fatalf("commenter %+v %v", commenter, err)
	}
	if linked := f.server.codeHostMemberLogins(t.Context(), &data, domain.CodeHostGitLab, host, "mallory-gl"); len(linked) != 0 {
		t.Fatalf("a login linked outside the workspace was attributed: %v", linked)
	}
}

// The GitHub App's user-to-server flow: PKCE through the App's setup URL,
// an 8-hour token with refresh that Flow's git relay may push with for the
// member's own sessions only.
func TestGitHubAppMemberLink(t *testing.T) {
	enableConnectorSecrets(t)
	t.Setenv("FLOW_APP_URL", "https://flow.example")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	var challenge string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/login/oauth/access_token":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			sum := sha256.Sum256([]byte(body["code_verifier"]))
			if body["code"] != "good" || body["client_secret"] != "csecret" || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
				_, _ = w.Write([]byte(`{"error":"bad_verification_code"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"ghu_member","refresh_token":"ghr_member","expires_in":28800,"refresh_token_expires_in":15897600}`))
		case r.URL.Path == "/user" && r.Header.Get("Authorization") == "Bearer ghu_member":
			_, _ = w.Write([]byte(`{"login":"alice","id":7}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(api.Close)
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	s.initCoding(appconfig.CodingConfig{Provider: appconfig.CodingProviderDisabled, GitHubApp: appconfig.GitHubAppConfig{AppID: "123", Slug: "flow-dev", ClientID: "cid", ClientSecret: "csecret", PrivateKey: keyPEM, WebhookSecret: "whsec", Host: "github.com", APIURL: api.URL}})
	s.gitHubAppHost().SetWebURL(api.URL)
	handler := newHandler(s)
	start := requestJSON[map[string]string](t, handler, http.MethodPost, "/api/account/code-host-accounts/github/authorize?workspace=test-workspace", map[string]string{"returnTo": "integration"}, http.StatusOK)
	authorize, _ := url.Parse(start["url"])
	if authorize.Query().Get("redirect_uri") != "https://flow.example/api/integrations/github-app/setup" || authorize.Query().Get("code_challenge") == "" {
		t.Fatalf("authorize URL %s", start["url"])
	}
	challenge = authorize.Query().Get("code_challenge")
	request := httptest.NewRequest(http.MethodGet, "/api/integrations/github-app/setup?code=good&state="+url.QueryEscape(authorize.Query().Get("state")), nil)
	request.Header.Set("Accept", "text/html")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusFound || recorder.Header().Get("Location") != "https://flow.example/test-workspace/settings/integrations/github?codeHostLink=linked" {
		t.Fatalf("setup redirect %d %q", recorder.Code, recorder.Header().Get("Location"))
	}
	status := requestJSON[gitHubAppStatus](t, handler, http.MethodGet, "/api/integrations/github-app?workspace=test-workspace", nil, http.StatusOK)
	if status.LinkedAccount == nil || status.LinkedAccount.Login != "alice" {
		t.Fatalf("github app status %+v", status)
	}
	viewer := repository.Account().Viewer
	repo := codehost.RepoRef{CodeHost: domain.CodeHostGitHub, HostName: "github.com", FullName: "acme/app"}
	members := codingMemberHosts{s: s}
	credential, err := members.MemberGitCredential(t.Context(), "test-workspace", viewer.ID, repo)
	if err != nil || credential.Password != "ghu_member" || credential.Username != "x-access-token" || len(credential.CLIEnv) != 0 {
		t.Fatalf("member git credential %+v %v", credential, err)
	}
	if _, err := members.MemberGitCredential(t.Context(), "test-workspace", "someone-else", repo); !errors.Is(err, codehost.ErrUserNotLinked) {
		t.Fatalf("another member's session got a token: %v", err)
	}
	account, _, _ := members.MemberAccount(t.Context(), "test-workspace", viewer.ID, domain.CodeHostGitHub, "github.com")
	if account.Email != "7+alice@users.noreply.github.com" {
		t.Fatalf("member commit email %q", account.Email)
	}
}
