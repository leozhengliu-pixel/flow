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
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	githubhost "flow/api/internal/codehost/github"
	"flow/api/internal/codehost/gitlab"
	"flow/api/internal/coding"
	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/objectstore"
	"flow/api/internal/sandbox"
	"flow/api/internal/store"
)

// End-to-end smoke test of coding sessions across every track, on the real
// server (routes, store, codingstore adapter, session core, sandbox
// reconciler) with the fake sandbox provider, an httptest GitHub API, an
// httptest GitLab API and an httptest LLM upstream. The runner protocol is
// played by hand over HTTP with the token the sandbox reconciler minted.

// fakeGitHubAPI is the GitHub REST API the App host calls.
type fakeGitHubAPI struct {
	*httptest.Server
	mu       sync.Mutex
	branches map[string]string // branch → head SHA
	pulls    map[int]map[string]any
	unknown  []string
	revoked  int
}

func newFakeGitHubAPI(t *testing.T) *fakeGitHubAPI {
	f := &fakeGitHubAPI{branches: map[string]string{}, pulls: map[int]map[string]any{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeGitHubAPI) setBranch(name, sha string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.branches[name] = sha
}

func (f *fakeGitHubAPI) pull(number int) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pulls[number]
}

func (f *fakeGitHubAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	write := func(status int, value any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(value)
	}
	var body map[string]any
	if r.Body != nil {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
	}
	const repo = "/repos/acme/web"
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/app/installations/42":
		write(200, map[string]any{"id": 42, "account": map[string]any{"login": "acme", "type": "Organization"}, "repository_selection": "selected"})
	case r.Method == http.MethodPost && path == "/app/installations/42/access_tokens":
		write(201, map[string]any{"token": "ghs_installation_token", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	case r.Method == http.MethodDelete && path == "/installation/token":
		f.revoked++
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && path == repo+"/installation":
		write(200, map[string]any{"id": 42})
	case r.Method == http.MethodGet && path == repo:
		write(200, map[string]any{"id": 1001, "full_name": "acme/web", "default_branch": "main", "private": true, "html_url": "https://github.com/acme/web", "permissions": map[string]any{"push": true}})
	case r.Method == http.MethodGet && strings.HasPrefix(path, repo+"/collaborators/") && strings.HasSuffix(path, "/permission"):
		write(200, map[string]any{"permission": "write", "role_name": "write"})
	case r.Method == http.MethodGet && strings.HasPrefix(path, repo+"/git/ref/heads/"):
		branch, _ := url.PathUnescape(strings.TrimPrefix(path, repo+"/git/ref/heads/"))
		if sha, ok := f.branches[branch]; ok {
			write(200, map[string]any{"ref": "refs/heads/" + branch, "object": map[string]any{"sha": sha, "type": "commit"}})
			return
		}
		write(404, map[string]any{"message": "Not Found"})
	case r.Method == http.MethodGet && path == repo+"/pulls":
		head := r.URL.Query().Get("head")
		list := []map[string]any{}
		for _, pull := range f.pulls {
			if ref := pull["head"].(map[string]any)["ref"]; "acme:"+fmt.Sprint(ref) == head || fmt.Sprint(ref) == head {
				list = append(list, pull)
			}
		}
		write(200, list)
	case r.Method == http.MethodPost && path == repo+"/pulls":
		number := len(f.pulls) + 7
		head := fmt.Sprint(body["head"])
		pull := map[string]any{
			"id": 9000 + number, "number": number, "html_url": "https://github.com/acme/web/pull/" + strconv.Itoa(number), "title": body["title"], "body": body["body"],
			"state": "open", "draft": body["draft"], "user": map[string]any{"login": "flow-dev[bot]"},
			"head": map[string]any{"ref": head, "sha": f.branches[head], "repo": map[string]any{"full_name": "acme/web"}}, "base": map[string]any{"ref": body["base"]},
		}
		f.pulls[number] = pull
		write(201, pull)
	case strings.HasPrefix(path, repo+"/pulls/"):
		number, _ := strconv.Atoi(strings.TrimPrefix(path, repo+"/pulls/"))
		pull, ok := f.pulls[number]
		if !ok {
			write(404, map[string]any{"message": "Not Found"})
			return
		}
		if r.Method == http.MethodPatch {
			pull["body"] = body["body"]
		}
		write(200, pull)
	case r.Method == http.MethodPost && strings.HasPrefix(path, repo+"/issues/"):
		write(201, map[string]any{"id": 1})
	case strings.Contains(path, "/check-runs") || strings.HasSuffix(path, "/status"):
		write(200, map[string]any{"total_count": 0, "check_runs": []any{}, "state": "pending", "statuses": []any{}})
	default:
		f.unknown = append(f.unknown, r.Method+" "+r.URL.String())
		write(404, map[string]any{"message": "Not Found"})
	}
}

// tokenCapturingProvider is the fake provider that remembers the runtime
// token each sandbox was started with (what a real runner reads from its
// token file); sandbox.Fake itself never retains tokens.
type tokenCapturingProvider struct {
	*sandbox.Fake
	mu     sync.Mutex
	tokens map[string]string
}

func (p *tokenCapturingProvider) StartSandbox(ctx context.Context, spec sandbox.SandboxSpec) (sandbox.SandboxStatus, error) {
	p.mu.Lock()
	p.tokens[spec.ID] = spec.Token
	p.mu.Unlock()
	return p.Fake.StartSandbox(ctx, spec)
}

func (p *tokenCapturingProvider) token(id string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tokens[id]
}

type codingE2E struct {
	provider *tokenCapturingProvider
	t        *testing.T
	s        *server
	handler  http.Handler
	repo     *store.SQLiteStore
	raw      *sql.DB
	github   *fakeGitHubAPI
	gitlab   *fakeGitLab
	llmCalls []string
	llmAuth  []string
	viewer   domain.User
	login    string
	mu       sync.Mutex
}

func newCodingE2E(t *testing.T) *codingE2E {
	t.Helper()
	enableConnectorSecrets(t)
	t.Setenv("FLOW_APP_URL", "https://flow.example")
	dbPath := filepath.Join(t.TempDir(), "flow.db")
	repository, err := store.OpenSQLiteTestFixture(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	raw, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { raw.Close() })
	e := &codingE2E{t: t, repo: repository, raw: raw, github: newFakeGitHubAPI(t), gitlab: newFakeGitLab(t)}
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		e.llmCalls = append(e.llmCalls, r.Method+" "+r.URL.Path)
		e.llmAuth = append(e.llmAuth, r.Header.Get("Authorization"))
		e.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","model":"gpt-5-codex","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Looking at the checkout handler."}]}],"usage":{"input_tokens":1200,"output_tokens":300,"total_tokens":1500}}`))
	}))
	t.Cleanup(llm.Close)
	objects, err := objectstore.Open(context.Background(), objectstore.Config{Driver: "local", LocalPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true, objectStore: objects}
	s.agent.Enabled = true
	for _, workspace := range repository.WorkspaceKeys() {
		s.ensureBuiltinApplication(context.Background(), workspace)
	}
	s.initCoding(appconfig.CodingConfig{
		Enabled: true, Provider: appconfig.CodingProviderFake, RuntimeURL: "http://flow.test", InstanceID: "e2e", Image: "flow-sandbox:e2e",
		MaxSessions: 5, WorkspaceSessions: 5, ReconcileInterval: time.Second, BranchPrefix: "flow/", PRMention: "@flow",
		Timeouts: appconfig.CodingTimeouts{Queue: time.Hour, Approval: time.Hour, Provision: time.Hour, Start: time.Hour, Setup: time.Hour, Turn: time.Hour, Heartbeat: time.Hour, Publish: time.Hour, Idle: time.Hour, StopGrace: time.Minute, MaxSession: 4 * time.Hour, TokenGrace: time.Minute},
		Retries:  appconfig.CodingRetries{Provision: 3, Start: 2, Publish: 5},
		Sizes:    map[string]appconfig.SandboxSize{"small": {CPU: "1", Memory: "8Gi"}, "medium": {CPU: "2", Memory: "16Gi"}, "large": {CPU: "4", Memory: "32Gi"}},
		LLM: appconfig.CodingLLMConfig{
			OpenAIBaseURL: llm.URL + "/v1", OpenAIAPIKey: "sk-upstream-secret", CodexModels: []string{"gpt-5-codex"},
			Prices: map[string]appconfig.ModelPrice{"gpt-5-codex": {Input: 1.25, Output: 10}},
		},
		RuntimePricesUSD: map[string]float64{"medium": 0.20},
		Snapshot:         appconfig.CodingSnapshotConfig{Strategy: sandbox.StrategyArchive, ArchivePrefix: "coding-snapshots", ArchiveMaxBytes: 1 << 20, Timeout: time.Hour},
		GitHubApp:        appconfig.GitHubAppConfig{AppID: "123", Slug: "flow-dev", PrivateKey: keyPEM, WebhookSecret: "whsec", Host: "github.com", APIURL: e.github.URL},
		GitLab:           appconfig.CodingGitLabConfig{CredentialMode: gitlab.ModeProjectToken, TokenScopes: []string{"api", "read_repository", "write_repository"}},
	})
	if !s.codingEnabled() {
		t.Fatal("coding sessions must be enabled")
	}
	e.provider = &tokenCapturingProvider{Fake: s.coding.sandbox.(*sandbox.Fake), tokens: map[string]string{}}
	s.coding.sandbox = e.provider
	e.s, e.handler = s, newHandler(s)
	if err := mutateTestWorkspace(t, repository, func(data *domain.Bootstrap) {
		if data.WorkspaceSettings.FeatureFlags == nil {
			data.WorkspaceSettings.FeatureFlags = map[string]bool{}
		}
		data.WorkspaceSettings.FeatureFlags[coding.FeatureFlag] = true
	}); err != nil {
		t.Fatal(err)
	}
	e.viewer = requestJSON[domain.Bootstrap](t, e.handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Viewer
	e.login, _, _ = strings.Cut(strings.ToLower(e.viewer.Email), "@")
	if e.login == "" {
		e.login = strings.ToLower(e.viewer.Name)
	}
	return e
}

// do sends a request to the server; token, when set, is the sandbox bearer token.
func (e *codingE2E) do(method, target string, body any, token string, header http.Header) *httptest.ResponseRecorder {
	var reader io.Reader
	switch value := body.(type) {
	case nil:
	case []byte:
		reader = bytes.NewReader(value)
	default:
		raw, _ := json.Marshal(value)
		reader = bytes.NewReader(raw)
	}
	request := httptest.NewRequest(method, target, reader)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for key, values := range header {
		request.Header[key] = values
	}
	recorder := httptest.NewRecorder()
	e.handler.ServeHTTP(recorder, request)
	return recorder
}

func (e *codingE2E) decode(recorder *httptest.ResponseRecorder, want int, into any) {
	e.t.Helper()
	if recorder.Code != want {
		e.t.Fatalf("status %d, want %d: %s", recorder.Code, want, recorder.Body.String())
	}
	if into != nil {
		if err := json.Unmarshal(recorder.Body.Bytes(), into); err != nil {
			e.t.Fatalf("decode %s: %v", recorder.Body.String(), err)
		}
	}
}

// installGitHubApp links installation 42 through the real install and setup
// handlers and links the viewer's GitHub account (personal access check).
func (e *codingE2E) installGitHubApp() {
	e.t.Helper()
	var start struct{ URL string }
	e.decode(e.do(http.MethodPost, "/api/integrations/github-app/install?workspace=test-workspace", nil, "", nil), http.StatusOK, &start)
	parsed, err := url.Parse(start.URL)
	if err != nil {
		e.t.Fatal(err)
	}
	setup := e.do(http.MethodGet, "/api/integrations/github-app/setup?installation_id=42&setup_action=install&state="+url.QueryEscape(parsed.Query().Get("state")), nil, "", nil)
	if setup.Code != http.StatusOK || !strings.Contains(setup.Body.String(), `"installed"`) {
		e.t.Fatalf("setup: %d %s", setup.Code, setup.Body.String())
	}
	link, _ := json.Marshal(githubhost.UserLink{Login: e.login, ID: 77, HostName: "github.com", LinkedAt: time.Now().UTC()})
	if err := e.repo.PutConnectorSecret(context.Background(), githubhost.UserLinkSecretID("github.com", e.viewer.ID), link, time.Now().AddDate(1, 0, 0)); err != nil {
		e.t.Fatal(err)
	}
}

func (e *codingE2E) githubWebhook(event, delivery, body string) *httptest.ResponseRecorder {
	mac := hmac.New(sha256.New, []byte("whsec"))
	mac.Write([]byte(body))
	header := http.Header{}
	header.Set("X-GitHub-Event", event)
	header.Set("X-GitHub-Delivery", delivery)
	header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	return e.do(http.MethodPost, "/api/integrations/github-app/webhook", []byte(body), "", header)
}

func (e *codingE2E) session(id string) domain.CodingSession {
	e.t.Helper()
	session, err := e.s.codingStore().CodingSession(context.Background(), "test-workspace", id)
	if err != nil {
		e.t.Fatal(err)
	}
	return session
}

// drive runs the session reconciler (track B) and the sandbox reconciler
// (track A) until the session reaches phase. Due times are reset between
// passes instead of waiting for them.
func (e *codingE2E) drive(id string, phase domain.CodingSessionPhase) domain.CodingSession {
	e.t.Helper()
	ctx := context.Background()
	sessions := &coding.Reconciler{Service: e.s.coding.service, Owner: "e2e-sessions", Meter: e.s.coding.meter, ProcessStart: time.Now().Add(-time.Hour)}
	sandboxes := &sandbox.Reconciler{Store: e.s.codingStore(), Provider: e.s.coding.sandbox, Config: e.s.coding.config, Owner: "e2e-sandboxes", Usage: codingMeterUsage{meter: e.s.coding.meter}}
	for range 40 {
		e.exec(`UPDATE coding_sessions SET next_reconcile_at=0 WHERE lease_until=0`)
		e.exec(`UPDATE coding_sandboxes SET lease_until=0 WHERE lease_owner=''`)
		sessions.RunOnce(ctx)
		if _, err := sandboxes.RunOnce(ctx); err != nil {
			e.t.Fatalf("sandbox reconciler: %v", err)
		}
		if session := e.session(id); session.Phase == phase {
			return session
		}
	}
	session := e.session(id)
	e.t.Fatalf("session stuck in %s (%s: %s), want %s; unknown GitHub calls %v", session.Phase, session.ErrorCode, session.Error, phase, e.github.unknown)
	return session
}

func (e *codingE2E) exec(query string, args ...any) {
	e.t.Helper()
	if _, err := e.raw.Exec(query, args...); err != nil {
		e.t.Fatalf("%s: %v", query, err)
	}
}

func (e *codingE2E) createIssue(title string) domain.Issue {
	e.t.Helper()
	data, _ := e.repo.WorkspaceMetadata("test-workspace")
	return requestJSON[domain.Issue](e.t, e.handler, http.MethodPost, "/api/issues", map[string]any{"title": title, "teamId": data.Teams[0].ID, "description": "Stack trace from acme/web"}, http.StatusCreated)
}

func TestCodingSessionEndToEnd(t *testing.T) {
	e := newCodingE2E(t)
	ctx := context.Background()
	e.installGitHubApp()
	environment, err := e.s.codingStore().CreateCodingEnvironment(ctx, domain.CodingEnvironment{
		WorkspaceKey: "test-workspace", Name: "Web", CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: "acme/web", DefaultBranch: "main", SandboxSize: domain.CodingSizeMedium,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Delegate an issue to the Flow agent, then start the session through
	// track L's issue trigger: the delegation task becomes the anchor.
	issue := e.createIssue("Checkout returns 500")
	agentUser := e.s.builtinAgentUserID(ctx, "test-workspace")
	issue = requestJSON[domain.Issue](t, e.handler, http.MethodPatch, "/api/issue-records/"+issue.ID, map[string]any{"delegateId": agentUser}, http.StatusOK)
	if issue.AgentSessionID == "" {
		t.Fatalf("delegation did not create an agent task: %+v", issue)
	}
	var started coding.TriggerResult
	e.decode(e.do(http.MethodPost, "/api/issues/"+issue.ID+"/coding-session", map[string]any{}, "", nil), http.StatusCreated, &started)
	if started.Session == nil || started.Session.AgentSessionID != issue.AgentSessionID || started.Session.Source != domain.CodingSourceIssue {
		t.Fatalf("started: %+v", started)
	}
	sessionID := started.Session.ID
	var again coding.TriggerResult
	e.decode(e.do(http.MethodPost, "/api/issues/"+issue.ID+"/coding-session", map[string]any{}, "", nil), http.StatusOK, &again)
	if again.Session == nil || again.Session.ID != sessionID {
		t.Fatalf("a second start for the same delegation must return the same session: %+v", again)
	}
	if anchor, err := e.repo.AgentTask(ctx, "test-workspace", issue.AgentSessionID); err != nil || anchor.CodingSessionID != sessionID {
		t.Fatalf("the delegation task is linked to the session: %+v %v", anchor.CodingSessionID, err)
	}

	// 2. Provisioning (B) creates the sandbox row; the sandbox reconciler (A)
	// starts it on the fake provider and mints its token.
	session := e.drive(sessionID, domain.CodingPhaseStarting)
	if session.EnvironmentID != environment.ID || session.Repository != "acme/web" || session.SandboxID == "" || session.Model != "gpt-5-codex" || session.ModelReason == "" {
		t.Fatalf("provisioned: %+v", session)
	}
	token := e.provider.token(session.SandboxID)
	if _, ok := e.provider.Spec(session.SandboxID); !ok || !strings.HasPrefix(token, coding.SandboxTokenPrefix) {
		t.Fatalf("the sandbox was started with a runtime token: %v %v", token != "", ok)
	}

	// 3. The runner: bootstrap, setup → running, a git credential, an LLM call
	// through the proxy, events, and the turn completion with the pushed head.
	var boot coding.BootstrapResponse
	e.decode(e.do(http.MethodGet, "/internal/coding-runtime/bootstrap", nil, token, nil), http.StatusOK, &boot)
	if boot.Turn == nil || !strings.Contains(boot.Turn.Prompt, "Checkout returns 500") || boot.Repo.FullName != "acme/web" || boot.Repo.BranchPrefix != "flow/" ||
		boot.LLM.OpenAIBaseURL != "http://flow.test/internal/llm-proxy/openai/v1" || boot.Guidance == "" {
		t.Fatalf("bootstrap: %+v", boot)
	}
	if session = e.session(sessionID); session.Phase != domain.CodingPhaseSetup {
		t.Fatalf("after bootstrap: %s", session.Phase)
	}
	e.decode(e.do(http.MethodPost, "/internal/coding-runtime/heartbeat", coding.HeartbeatRequest{Phase: "running"}, token, nil), http.StatusOK, nil)
	if session = e.session(sessionID); session.Phase != domain.CodingPhaseRunning {
		t.Fatalf("after the running heartbeat: %s", session.Phase)
	}
	var credential coding.CredentialResponse
	e.decode(e.do(http.MethodPost, "/internal/coding-runtime/credentials", coding.CredentialRequest{Protocol: "https", Host: "github.com", Path: "acme/web.git", Purpose: "git"}, token, nil), http.StatusOK, &credential)
	if credential.Password != "ghs_installation_token" {
		t.Fatalf("git credential: %+v", credential)
	}
	llmResponse := e.do(http.MethodPost, "/internal/llm-proxy/openai/v1/responses", map[string]any{"model": "gpt-5-codex", "input": "Find the bug"}, token, nil)
	if llmResponse.Code != http.StatusOK || !strings.Contains(llmResponse.Body.String(), "checkout handler") {
		t.Fatalf("LLM proxy: %d %s", llmResponse.Code, llmResponse.Body.String())
	}
	e.mu.Lock()
	if len(e.llmCalls) != 1 || !strings.HasSuffix(e.llmCalls[0], "/responses") || e.llmAuth[0] != "Bearer sk-upstream-secret" {
		t.Fatalf("upstream call %v auth %v: the provider key is added by Flow, never the sandbox token", e.llmCalls, e.llmAuth)
	}
	e.mu.Unlock()
	if e.do(http.MethodPost, "/internal/llm-proxy/openai/v1/responses", map[string]any{"model": "gpt-5-codex"}, "fcs_not-a-token", nil).Code != http.StatusUnauthorized {
		t.Fatal("the LLM proxy must refuse unknown sandbox tokens")
	}
	events := coding.EventsRequest{Events: []coding.RunnerEvent{
		{ID: "ev1", TurnID: boot.Turn.ID, Type: coding.EventThought, Body: "Reading the checkout handler"},
		{ID: "ev1b", TurnID: boot.Turn.ID, Type: coding.EventAction, Action: &coding.ActionDetail{Action: "Bash", Parameter: "go test ./checkout/...\necho done"}},
		{ID: "ev2", TurnID: boot.Turn.ID, Type: coding.EventResponse, Body: "Fixed the nil pointer in checkout."},
	}}
	e.decode(e.do(http.MethodPost, "/internal/coding-runtime/events", events, token, nil), http.StatusOK, nil)
	// The status row's live step: the latest tool step of the running turn.
	if step := e.session(sessionID).CurrentStep; step == nil || step.Action != "Bash" || step.Parameter != "go test ./checkout/..." || step.TurnID != boot.Turn.ID {
		t.Fatalf("current step: %+v", step)
	}
	e.github.setBranch(session.BranchName, "c0ffee0000000000000000000000000000000000")
	complete := coding.TurnCompleteRequest{Status: coding.TurnSuccess, Pushed: true, HeadSHA: "c0ffee0000000000000000000000000000000000", Commits: []domain.CodingCommit{{SHA: "c0ffee0000000000000000000000000000000000", Message: "Fix checkout nil pointer"}}}
	e.decode(e.do(http.MethodPost, "/internal/coding-runtime/turns/"+url.PathEscape(boot.Turn.ID)+"/complete", complete, token, nil), http.StatusOK, nil)

	// 4. Publishing (B + D) opens a draft PR; the session is complete with it linked.
	session = e.drive(sessionID, domain.CodingPhaseIdle)
	if session.State != domain.CodingSessionComplete || session.LatestPullRequest == nil || session.LatestPullRequest.Number != 7 {
		t.Fatalf("published: state=%s pr=%+v", session.State, session.LatestPullRequest)
	}
	pull := e.github.pull(7)
	if pull == nil || pull["draft"] != true || pull["base"].(map[string]any)["ref"] != "main" || pull["head"].(map[string]any)["ref"] != session.BranchName {
		t.Fatalf("draft PR: %+v", pull)
	}
	var full domain.CodingSession
	e.decode(e.do(http.MethodGet, "/api/coding/sessions/"+sessionID, nil, "", nil), http.StatusOK, &full)
	if len(full.PullRequests) != 1 || full.PullRequests[0].URL != "https://github.com/acme/web/pull/7" {
		t.Fatalf("session API pull requests: %+v", full.PullRequests)
	}
	var usage int
	if err := e.raw.QueryRow(`SELECT COUNT(*) FROM coding_usage_records WHERE session_id=? AND kind='llm'`, sessionID).Scan(&usage); err != nil || usage != 1 {
		t.Fatalf("LLM usage metered: %d %v", usage, err)
	}

	// 5. A "@flow" comment on the PR (GitHub App webhook → D → L → B) is a
	// follow-up turn the idle runner receives; a redelivery adds nothing.
	comment := fmt.Sprintf(`{"action":"created","installation":{"id":42},"repository":{"id":1001,"full_name":"acme/web"},"issue":{"number":7,"title":"Fix checkout","state":"open","html_url":"https://github.com/acme/web/pull/7","pull_request":{"url":"x"}},"comment":{"id":555,"body":"@flow please add a regression test","html_url":"https://github.com/acme/web/pull/7#issuecomment-555","user":{"login":%q,"type":"User"}},"sender":{"login":%q,"type":"User"}}`, e.login, e.login)
	if hook := e.githubWebhook("issue_comment", "delivery-555", comment); hook.Code >= 300 {
		t.Fatalf("webhook: %d %s", hook.Code, hook.Body.String())
	}
	var inbox coding.InboxResponse
	e.decode(e.do(http.MethodGet, "/internal/coding-runtime/inbox?after=0", nil, token, nil), http.StatusOK, &inbox)
	followUps := 0
	for _, item := range inbox.Items {
		if item.Turn != nil && strings.Contains(item.Turn.Prompt, "please add a regression test") {
			followUps++
		}
	}
	if followUps != 1 {
		t.Fatalf("the PR comment must be one follow-up turn: %+v", inbox.Items)
	}
	if hook := e.githubWebhook("issue_comment", "delivery-555", comment); hook.Code >= 300 {
		t.Fatalf("redelivery: %d", hook.Code)
	}
	items, _ := e.s.codingStore().CodingInbox(ctx, sessionID, 0)
	turns := 0
	for _, item := range items {
		if item.Kind == coding.InboxTurn {
			turns++
		}
	}
	if turns != 2 {
		t.Fatalf("a redelivered comment must not queue another turn: %d turns", turns)
	}
	if session = e.session(sessionID); session.Phase != domain.CodingPhaseRunning {
		t.Fatalf("the delivered follow-up starts a turn: %s", session.Phase)
	}

	// 6. The agent asks a question (turn ends awaitingInput); the person's
	// prompt from the session view is the answer the waiting runner gets.
	followUp := session.CurrentTurnID
	e.decode(e.do(http.MethodPost, "/internal/coding-runtime/turns/"+url.PathEscape(followUp)+"/complete", coding.TurnCompleteRequest{Status: coding.TurnAwaitingInput, Summary: "Which test framework should I use?"}, token, nil), http.StatusOK, nil)
	if session = e.session(sessionID); session.Phase != domain.CodingPhaseAwaitingUser || session.State != domain.CodingSessionAwaitingInput {
		t.Fatalf("waiting for an answer: %s %s", session.Phase, session.State)
	}
	e.decode(e.do(http.MethodPost, "/api/coding/sessions/"+sessionID+"/prompt", map[string]any{"prompt": "Use vitest"}, "", nil), http.StatusOK, nil)
	e.decode(e.do(http.MethodGet, "/internal/coding-runtime/inbox?after="+strconv.FormatInt(inbox.Items[len(inbox.Items)-1].Seq, 10), nil, token, nil), http.StatusOK, &inbox)
	if len(inbox.Items) != 1 || inbox.Items[0].Turn == nil || inbox.Items[0].Turn.Prompt != "Use vitest" {
		t.Fatalf("the answer is delivered as the next turn: %+v", inbox.Items)
	}
	if session = e.session(sessionID); session.Phase != domain.CodingPhaseRunning || session.CurrentTurnID != inbox.Items[0].Turn.ID {
		t.Fatalf("the answer resumes the session: %s", session.Phase)
	}

	// 7. Closing the PR without merging archives the session and unassigns
	// the agent from the issue, attributed to GitHub.
	closed := `{"action":"closed","number":7,"installation":{"id":42},"repository":{"id":1001,"full_name":"acme/web"},"sender":{"login":"alice","type":"User"},
"pull_request":{"id":707,"number":7,"title":"Fix checkout","html_url":"https://github.com/acme/web/pull/7","state":"closed","draft":true,"merged":false,"updated_at":"2026-10-19T18:30:00Z",
"user":{"login":"flow-dev[bot]"},"head":{"ref":"` + session.BranchName + `","sha":"c0ffee0000000000000000000000000000000000"},"base":{"ref":"main","repo":{"full_name":"acme/web"}}}}`
	if hook := e.githubWebhook("pull_request", "delivery-close-7", closed); hook.Code >= 300 {
		t.Fatalf("close webhook: %d %s", hook.Code, hook.Body.String())
	}
	if session = e.session(sessionID); session.ArchivedAt == nil || !session.StopRequested {
		t.Fatalf("closing the PR archives (and stops) the busy session: archived=%v stop=%v", session.ArchivedAt, session.StopRequested)
	}
	if closedIssue := requestJSON[domain.Issue](t, e.handler, http.MethodGet, "/api/issue-records/"+issue.ID, nil, http.StatusOK); closedIssue.Delegate != nil {
		t.Fatalf("the agent is unassigned: %+v", closedIssue.Delegate)
	}
	history := requestJSON[store.IssueHistory](t, e.handler, http.MethodGet, "/api/issue-records/"+issue.ID+"/history", nil, http.StatusOK)
	unassigned, archived := false, false
	for _, event := range history.Activities {
		unassigned = unassigned || event.Type == "issue.updated" && event.Metadata["previousDelegate"] == agentUser && event.BotActor != nil && event.BotActor.UserDisplayName == "GitHub"
		archived = archived || event.Type == activityCodingSessionArchived && event.Metadata["sessionId"] == sessionID
	}
	if !unassigned || !archived {
		t.Fatalf("activity: unassigned=%v archived=%v %+v", unassigned, archived, history.Activities)
	}
}

// TestCodingSessionGitLabFollowUpEndToEnd: a GitLab MR note mentioning
// @flow (GitLab connection webhook → J → L → B) queues a follow-up turn on
// the session that owns the merge request.
func TestCodingSessionGitLabFollowUpEndToEnd(t *testing.T) {
	e := newCodingE2E(t)
	ctx := context.Background()
	var connected gitlabConnectResponse
	e.decode(e.do(http.MethodPut, "/api/integrations/gitlab?workspace=test-workspace", map[string]any{"config": map[string]string{"apiToken": "glpat-fake-api", "host": e.gitlab.URL}}, "", nil), http.StatusOK, &connected)
	host := strings.TrimPrefix(strings.TrimPrefix(e.gitlab.URL, "https://"), "http://")
	environment, err := e.s.codingStore().CreateCodingEnvironment(ctx, domain.CodingEnvironment{
		WorkspaceKey: "test-workspace", Name: "API", CodeHost: domain.CodeHostGitLab, HostName: host, Repository: "acme/api", DefaultBranch: "main", SandboxSize: domain.CodingSizeMedium,
	})
	if err != nil {
		t.Fatal(err)
	}
	issue := e.createIssue("Add rate limiting")
	var started coding.TriggerResult
	e.decode(e.do(http.MethodPost, "/api/issues/"+issue.ID+"/coding-session", map[string]any{"environmentId": environment.ID}, "", nil), http.StatusCreated, &started)
	if started.Session == nil || started.Session.EnvironmentID != environment.ID {
		t.Fatalf("started: %+v", started)
	}
	sessionID := started.Session.ID
	if err := e.s.codingStore().UpsertSessionPullRequest(ctx, "test-workspace", domain.SessionPullRequest{
		SessionID: sessionID, CodeHost: domain.CodeHostGitLab, HostName: host, Repository: "acme/api", Number: 27, State: "open", Draft: true,
		HeadBranch: started.Session.BranchName, BaseBranch: "main", URL: e.gitlab.URL + "/acme/api/-/merge_requests/27", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	note := fmt.Sprintf(`{"object_kind":"note","user":{"username":%q},"project":{"id":7,"path_with_namespace":"acme/api","web_url":%q},"object_attributes":{"id":9001,"note":"@flow please add tests for the limiter","noteable_type":"MergeRequest","url":%q},"merge_request":{"id":44001,"iid":27,"title":"Draft: Add rate limiting","url":%q,"state":"opened","source_branch":%q,"target_branch":"main","last_commit":{"id":"abc"}}}`,
		e.login, e.gitlab.URL+"/acme/api", e.gitlab.URL+"/acme/api/-/merge_requests/27#note_9001", e.gitlab.URL+"/acme/api/-/merge_requests/27", started.Session.BranchName)
	header := http.Header{}
	header.Set("X-Gitlab-Event", gitlab.HookNote)
	header.Set("X-Gitlab-Token", connected.WebhookSecret)
	header.Set("X-Gitlab-Event-UUID", "note-e2e-1")
	if hook := e.do(http.MethodPost, "/api/integrations/gitlab/webhook?workspace=test-workspace", []byte(note), "", header); hook.Code >= 300 {
		t.Fatalf("GitLab webhook: %d %s", hook.Code, hook.Body.String())
	}
	items, err := e.s.codingStore().CodingInbox(ctx, sessionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item.Turn != nil && strings.Contains(item.Turn.Prompt, "please add tests for the limiter") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the MR note must queue a follow-up turn: %+v", items)
	}
}

// Track L's "approval expired" notice is posted by the session core's
// ApprovalExpired hook (the reconciler owns the expiry); it lands once on the
// anchor however often the hook runs.
func TestCodingApprovalExpiredNotice(t *testing.T) {
	e := newCodingE2E(t)
	ctx := context.Background()
	session, created, err := e.s.coding.service.Start(ctx, coding.StartInput{WorkspaceKey: "test-workspace", Source: domain.CodingSourceExternal, Prompt: "From email", CreatorID: e.viewer.ID, RequireApproval: true, IdempotencyKey: "approval-notice"})
	if err != nil || !created || session.Phase != domain.CodingPhaseAwaitingApproval {
		t.Fatalf("start: %+v %v %v", session, created, err)
	}
	session.Phase, session.ErrorCode = domain.CodingPhaseDone, domain.CodingErrApprovalExpired
	for range 2 {
		e.s.coding.service.ApprovalExpired(ctx, session)
	}
	activities, err := e.repo.AgentActivities(ctx, "test-workspace", session.AgentSessionID, "")
	if err != nil {
		t.Fatal(err)
	}
	notices := 0
	for _, activity := range activities {
		if activity.Metadata["codingKey"] == "approval:expired" {
			notices++
			if activity.Metadata["decision"] != "expired" || !strings.Contains(activity.Body, "in time") {
				t.Fatalf("notice: %+v", activity)
			}
		}
	}
	if notices != 1 {
		t.Fatalf("approval expired notices = %d, want 1", notices)
	}
}
