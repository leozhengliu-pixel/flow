package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/codehost"
	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type gitHubAppFixture struct {
	t       *testing.T
	repo    *store.SQLiteStore
	dbPath  string
	server  *server
	handler http.Handler
	api     *httptest.Server
}

func newGitHubAppFixture(t *testing.T) *gitHubAppFixture {
	t.Helper()
	enableConnectorSecrets(t)
	t.Setenv("FLOW_APP_URL", "https://flow.example")
	dbPath := filepath.Join(t.TempDir(), "flow.db")
	repository, err := store.OpenSQLiteTestFixture(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/app/installations/42":
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey") {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"id":42,"account":{"login":"acme","type":"Organization"},"repository_selection":"selected"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}
	}))
	t.Cleanup(api.Close)
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	s.initCoding(appconfig.CodingConfig{Provider: appconfig.CodingProviderDisabled, GitHubApp: appconfig.GitHubAppConfig{AppID: "123", Slug: "flow-dev", PrivateKey: keyPEM, WebhookSecret: "whsec", Host: "github.com", APIURL: api.URL}})
	return &gitHubAppFixture{t: t, repo: repository, dbPath: dbPath, server: s, handler: newHandler(s), api: api}
}

func (f *gitHubAppFixture) do(method, target string, body []byte, header http.Header) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	for key, values := range header {
		request.Header[key] = values
	}
	if request.Header.Get("Accept") == "" {
		request.Header.Set("Accept", "application/json")
	}
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	return recorder
}

func (f *gitHubAppFixture) webhook(event, delivery string, body string) *httptest.ResponseRecorder {
	mac := hmac.New(sha256.New, []byte("whsec"))
	mac.Write([]byte(body))
	header := http.Header{}
	header.Set("X-GitHub-Event", event)
	header.Set("X-GitHub-Delivery", delivery)
	header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	return f.do(http.MethodPost, "/api/integrations/github-app/webhook", []byte(body), header)
}

// install links installation 42 to test-workspace through the real install
// and setup handlers.
func (f *gitHubAppFixture) install() {
	f.t.Helper()
	rec := f.do(http.MethodPost, "/api/integrations/github-app/install?workspace=test-workspace", nil, nil)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("install: %d %s", rec.Code, rec.Body.String())
	}
	var start struct{ URL string }
	_ = json.Unmarshal(rec.Body.Bytes(), &start)
	parsed, err := url.Parse(start.URL)
	if err != nil || parsed.Host != "github.com" || parsed.Path != "/apps/flow-dev/installations/new" || parsed.Query().Get("state") == "" {
		f.t.Fatalf("install URL %q", start.URL)
	}
	state := parsed.Query().Get("state")
	rec = f.do(http.MethodGet, "/api/integrations/github-app/setup?installation_id=42&setup_action=install&state="+url.QueryEscape(state), nil, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"installed"`) {
		f.t.Fatalf("setup: %d %s", rec.Code, rec.Body.String())
	}
}

func TestGitHubAppInstallAndSetup(t *testing.T) {
	f := newGitHubAppFixture(t)
	rec := f.do(http.MethodGet, "/api/integrations/github-app?workspace=test-workspace", nil, nil)
	var status gitHubAppStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil || rec.Code != http.StatusOK || !status.Configured || status.HostName != "github.com" || status.Slug != "flow-dev" || len(status.Installations) != 0 || status.CanLinkAccount {
		t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
	}
	f.install()
	installations, err := f.repo.CodeHostInstallations(t.Context(), "test-workspace", "github")
	if err != nil || len(installations) != 1 || installations[0].AccountLogin != "acme" || installations[0].InstallationID != "42" {
		t.Fatalf("installation not stored: %+v %v", installations, err)
	}
	rec = f.do(http.MethodGet, "/api/integrations/github-app?workspace=test-workspace", nil, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &status)
	if len(status.Installations) != 1 || status.Installations[0].RepositorySelection != "selected" {
		t.Fatalf("status installations: %s", rec.Body.String())
	}
	// A forged or tampered state is refused, and browsers are redirected.
	rec = f.do(http.MethodGet, "/api/integrations/github-app/setup?installation_id=42&state=e30.AAAA", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("forged state: %d %s", rec.Code, rec.Body.String())
	}
	header := http.Header{}
	header.Set("Accept", "text/html")
	rec = f.do(http.MethodGet, "/api/integrations/github-app/setup?installation_id=42&state=bad", nil, header)
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), "https://flow.example/?") || !strings.Contains(rec.Header().Get("Location"), "githubApp=error") {
		t.Fatalf("browser redirect: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	expired, _ := f.server.signGitHubAppState(gitHubAppState{Workspace: "test-workspace", Purpose: "install"})
	payload, _, _ := strings.Cut(expired, ".")
	if _, err := f.server.verifyGitHubAppState(payload + ".x"); err == nil {
		t.Fatal("bad signature must fail")
	}
	// Account linking is unavailable without client credentials.
	if rec := f.do(http.MethodPost, "/api/integrations/github-app/authorize?workspace=test-workspace", nil, nil); rec.Code != http.StatusConflict {
		t.Fatalf("authorize without OAuth: %d", rec.Code)
	}
	// Unlink.
	rec = f.do(http.MethodDelete, "/api/integrations/github-app/installations/"+installations[0].ID+"?workspace=test-workspace", nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unlink: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(http.MethodDelete, "/api/integrations/github-app/installations/42?workspace=test-workspace", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("second unlink: %d", rec.Code)
	}
}

const appPullRequestPayload = `{"action":"%s","number":21,"installation":{"id":42},"repository":{"id":1,"full_name":"acme/store"},"sender":{"login":"alice","type":"User"},
"pull_request":{"id":9921,"number":21,"title":"Fix checkout","body":"Fixes things","html_url":"https://github.com/acme/store/pull/21","state":"%s","draft":%t,"merged":%t,"updated_at":"%s",
"user":{"login":"flow-dev[bot]"},"head":{"ref":"flow/fix","sha":"abc"},"base":{"ref":"main","repo":{"full_name":"acme/store"}}}}`

func TestGitHubAppWebhookSyncsReviewsAndSessionPullRequests(t *testing.T) {
	f := newGitHubAppFixture(t)
	f.install()
	// A session linked to PR #21.
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `INSERT INTO coding_sessions(id,workspace_key,agent_session_id,creator_id,source,state,phase,phase_started_at,data,created_at,updated_at) VALUES('s1','test-workspace','a1','u1','issue','complete','idle',1,'{}',1,1)`); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	if err := f.repo.UpsertSessionPullRequest(t.Context(), "test-workspace", domain.SessionPullRequest{SessionID: "s1", CodeHost: "github", HostName: "github.com", Repository: "acme/store", Number: 21, State: "open", Draft: true, UpdatedAt: created}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var dispatched []codehost.WebhookEvent
	previous := codingWebhookDispatch
	codingWebhookDispatch = func(_ *server, _ context.Context, workspace string, event codehost.WebhookEvent) error {
		mu.Lock()
		defer mu.Unlock()
		if workspace != "test-workspace" {
			t.Errorf("dispatched to %s", workspace)
		}
		dispatched = append(dispatched, event)
		return nil
	}
	t.Cleanup(func() { codingWebhookDispatch = previous })

	stamp := func(minutes int) string {
		return created.Add(time.Duration(minutes) * time.Minute).Format(time.RFC3339)
	}
	// Bad signature.
	header := http.Header{}
	header.Set("X-GitHub-Event", "pull_request")
	header.Set("X-Hub-Signature-256", "sha256=00")
	if rec := f.do(http.MethodPost, "/api/integrations/github-app/webhook", []byte(`{}`), header); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature: %d", rec.Code)
	}
	// Ready for review: CodeReview created, session link updated with the review id.
	if rec := f.webhook("pull_request", "d1", fmt.Sprintf(appPullRequestPayload, "ready_for_review", "open", false, false, stamp(1))); rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"workspaces":1`) {
		t.Fatalf("ready_for_review: %d %s", rec.Code, rec.Body.String())
	}
	reviews := f.repo.Bootstrap().Reviews
	if len(reviews) == 0 || reviews[0].ExternalID != "9921" {
		t.Fatalf("App PR events must feed the CodeReview sync: %+v", reviews)
	}
	links, _ := f.repo.SessionPullRequests(t.Context(), "test-workspace", "s1")
	if len(links) != 1 || links[0].Draft || links[0].ReviewID != reviews[0].ID || links[0].URL == "" {
		t.Fatalf("session link after ready_for_review: %+v", links)
	}
	reviewCount := len(reviews)
	// Redelivery of the same delivery is applied once.
	if rec := f.webhook("pull_request", "d1", fmt.Sprintf(appPullRequestPayload, "ready_for_review", "open", false, false, stamp(1))); rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"workspaces":0`) {
		t.Fatalf("redelivery: %d %s", rec.Code, rec.Body.String())
	}
	// Merged.
	if rec := f.webhook("pull_request", "d2", fmt.Sprintf(appPullRequestPayload, "closed", "closed", false, true, stamp(3))); rec.Code != http.StatusAccepted {
		t.Fatalf("merged: %d %s", rec.Code, rec.Body.String())
	}
	// A late, older "reopened" does not undo the merge.
	if rec := f.webhook("pull_request", "d3", fmt.Sprintf(appPullRequestPayload, "reopened", "open", false, false, stamp(2))); rec.Code != http.StatusAccepted {
		t.Fatalf("late reopen: %d %s", rec.Code, rec.Body.String())
	}
	links, _ = f.repo.SessionPullRequests(t.Context(), "test-workspace", "s1")
	if links[0].State != "merged" {
		t.Fatalf("merged state expected: %+v", links[0])
	}
	if got := f.repo.Bootstrap().Reviews; len(got) != reviewCount || got[0].Status != "merged" {
		t.Fatalf("review must be updated in place and merged: %d %+v", len(got), got[0])
	}
	mu.Lock()
	if len(dispatched) != 3 || dispatched[0].Type != codehost.EventChangeRequestUpdate || dispatched[1].Type != codehost.EventChangeRequestMerged {
		t.Fatalf("dispatched %+v", dispatched)
	}
	mu.Unlock()
	// Installation lifecycle: suspended, then deleted.
	if rec := f.webhook("installation", "d4", `{"action":"suspend","installation":{"id":42,"account":{"login":"acme","type":"Organization"},"repository_selection":"selected","suspended_at":"2026-03-02T00:00:00Z"}}`); rec.Code != http.StatusAccepted {
		t.Fatalf("suspend: %d %s", rec.Code, rec.Body.String())
	}
	installations, _ := f.repo.CodeHostInstallations(t.Context(), "test-workspace", "github")
	if len(installations) != 1 || installations[0].SuspendedAt == nil {
		t.Fatalf("suspend not applied: %+v", installations)
	}
	if connected, _ := f.server.gitHubAppHost().Connected(t.Context(), "test-workspace"); connected {
		t.Fatal("a suspended installation does not connect the workspace")
	}
	if rec := f.webhook("installation", "d5", `{"action":"deleted","installation":{"id":42,"account":{"login":"acme"}}}`); rec.Code != http.StatusAccepted {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if installations, _ := f.repo.CodeHostInstallations(t.Context(), "test-workspace", "github"); len(installations) != 0 {
		t.Fatalf("deleted installation must be removed: %+v", installations)
	}
	// Ignored events are acknowledged.
	if rec := f.webhook("ping", "d6", `{"zen":"x"}`); rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), "ignored") {
		t.Fatalf("ping: %d %s", rec.Code, rec.Body.String())
	}
}

func TestGitHubAppWebhookSkipsReviewSyncWithClassicConnection(t *testing.T) {
	f := newGitHubAppFixture(t)
	f.install()
	requestJSON[domain.IntegrationConnection](t, f.handler, http.MethodPut, "/api/integrations/github?workspace=test-workspace", map[string]any{"name": "acme", "config": map[string]string{"organization": "acme", "webhookSecret": "secret"}}, http.StatusOK)
	before := len(f.repo.Bootstrap().Reviews)
	if rec := f.webhook("pull_request", "c1", fmt.Sprintf(appPullRequestPayload, "opened", "open", true, false, time.Now().UTC().Format(time.RFC3339))); rec.Code != http.StatusAccepted {
		t.Fatalf("webhook: %d %s", rec.Code, rec.Body.String())
	}
	if after := len(f.repo.Bootstrap().Reviews); after != before {
		t.Fatalf("the classic connection already syncs reviews; the App must not add a second copy (%d → %d)", before, after)
	}
}
