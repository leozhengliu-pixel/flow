package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/coding"
	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// fakeCodingStore is an in-memory coding store for the trigger routes (the
// SQL implementations belong to tracks B, D and K).
type fakeCodingStore struct {
	mu           sync.Mutex
	sessions     map[string]domain.CodingSession
	anchors      map[string]domain.AgentTask
	keys         map[string]string
	inbox        map[string][]coding.InboxItem
	environments []domain.CodingEnvironment
	links        []domain.SessionPullRequest
	spend        int64
}

func newFakeCodingStore(environments ...domain.CodingEnvironment) *fakeCodingStore {
	return &fakeCodingStore{sessions: map[string]domain.CodingSession{}, anchors: map[string]domain.AgentTask{}, keys: map[string]string{}, inbox: map[string][]coding.InboxItem{}, environments: environments}
}

func (f *fakeCodingStore) CreateCodingSession(_ context.Context, session domain.CodingSession, anchor domain.AgentTask, key string) (domain.CodingSession, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.keys[key]; ok {
		return f.sessions[id], false, nil
	}
	f.keys[key] = session.ID
	f.sessions[session.ID] = session
	f.anchors[anchor.ID] = anchor
	return session, true, nil
}

func (f *fakeCodingStore) CodingSession(_ context.Context, workspace, id string) (domain.CodingSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.sessions[id]
	if !ok || session.WorkspaceKey != workspace {
		return domain.CodingSession{}, coding.ErrNotFound
	}
	return session, nil
}

func (f *fakeCodingStore) ListCodingSessions(_ context.Context, workspace string, filter coding.SessionFilter) ([]domain.CodingSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []domain.CodingSession
	for _, session := range f.sessions {
		if session.WorkspaceKey == workspace && (filter.IssueID == "" || session.IssueID == filter.IssueID) {
			result = append(result, session)
		}
	}
	return result, nil
}

func (f *fakeCodingStore) UpdateCodingSession(_ context.Context, workspace, id string, mutate func(*domain.CodingSession) error) (domain.CodingSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.sessions[id]
	if !ok || session.WorkspaceKey != workspace {
		return domain.CodingSession{}, coding.ErrNotFound
	}
	if err := mutate(&session); err != nil {
		return domain.CodingSession{}, err
	}
	f.sessions[id] = session
	return session, nil
}

func (f *fakeCodingStore) EnqueueCodingInbox(_ context.Context, _ string, sessionID string, item coding.InboxItem) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.inbox[sessionID] {
		if existing.Turn != nil && item.Turn != nil && existing.Turn.ID == item.Turn.ID {
			return existing.Seq, nil
		}
	}
	item.Seq = int64(len(f.inbox[sessionID]) + 1)
	f.inbox[sessionID] = append(f.inbox[sessionID], item)
	return item.Seq, nil
}

func (f *fakeCodingStore) ListCodingEnvironments(context.Context, string, bool) ([]domain.CodingEnvironment, error) {
	return f.environments, nil
}

func (f *fakeCodingStore) CodingEnvironmentForRepository(_ context.Context, _ string, codeHost, hostName, repository string) (domain.CodingEnvironment, error) {
	for _, environment := range f.environments {
		if environment.CodeHost == codeHost && environment.HostName == hostName && strings.EqualFold(environment.Repository, repository) {
			return environment, nil
		}
	}
	return domain.CodingEnvironment{}, coding.ErrNotFound
}

func (f *fakeCodingStore) UpsertSessionPullRequest(_ context.Context, _ string, pr domain.SessionPullRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for index, link := range f.links {
		if link.SessionID == pr.SessionID && link.Number == pr.Number && link.Repository == pr.Repository {
			f.links[index] = pr
			return nil
		}
	}
	f.links = append(f.links, pr)
	return nil
}

func (f *fakeCodingStore) SessionsForChangeRequest(_ context.Context, codeHost, hostName, repository string, number int) ([]domain.CodingSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []domain.CodingSession
	for _, link := range f.links {
		if link.CodeHost == codeHost && link.HostName == hostName && strings.EqualFold(link.Repository, repository) && link.Number == number {
			result = append(result, f.sessions[link.SessionID])
		}
	}
	return result, nil
}

func (f *fakeCodingStore) CodingSpendMicros(context.Context, coding.SpendQuery) (int64, error) {
	return f.spend, nil
}

func (f *fakeCodingStore) turns(sessionID string) []coding.Turn {
	f.mu.Lock()
	defer f.mu.Unlock()
	var turns []coding.Turn
	for _, item := range f.inbox[sessionID] {
		turns = append(turns, *item.Turn)
	}
	return turns
}

type codingTriggerFixture struct {
	s        *server
	handler  http.Handler
	repo     *store.SQLiteStore
	fake     *fakeCodingStore
	issue    domain.Issue
	settings func(*domain.CodingAgentSettings)
}

func newCodingTriggerFixture(t *testing.T, environments ...domain.CodingEnvironment) *codingTriggerFixture {
	t.Helper()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	s.coding = &codingServices{config: appconfig.CodingConfig{Enabled: true, PRMention: "@flow", BranchPrefix: "flow/"}, hosts: map[codehost.Kind]codehost.Host{}}
	fake := newFakeCodingStore(environments...)
	previous := codingTriggerStore
	codingTriggerStore = func(*server) any { return fake }
	t.Cleanup(func() { codingTriggerStore = previous })
	fixture := &codingTriggerFixture{s: s, handler: newHandler(s), repo: repository, fake: fake}
	data, _ := repository.WorkspaceMetadata("test-workspace")
	team := data.Teams[0]
	created := requestJSON[domain.Issue](t, fixture.handler, http.MethodPost, "/api/issues", map[string]any{"teamId": team.ID, "title": "Checkout returns 500", "description": "Stack trace from acme/payments-api"}, http.StatusCreated)
	fixture.issue = created
	return fixture
}

func (f *codingTriggerFixture) post(t *testing.T, path string, body any, headers map[string]string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(mustJSON(t, body)))
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func githubEnvironment(id, repository string) domain.CodingEnvironment {
	return domain.CodingEnvironment{ID: id, Name: repository, CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: repository}
}

func TestIssueCodingSessionRoute(t *testing.T) {
	f := newCodingTriggerFixture(t, githubEnvironment("e-web", "acme/web"), githubEnvironment("e-pay", "acme/payments-api"))
	path := "/api/issues/" + f.issue.ID + "/coding-session"

	code, body := f.post(t, path, map[string]any{"prompt": "Fix the 500"}, map[string]string{"Idempotency-Key": "k1"})
	if code != http.StatusCreated || !strings.Contains(body, `"outcome":"started"`) {
		t.Fatalf("status = %d body = %s", code, body)
	}
	if len(f.fake.sessions) != 1 {
		t.Fatalf("sessions = %+v", f.fake.sessions)
	}
	var session domain.CodingSession
	for _, item := range f.fake.sessions {
		session = item
	}
	if session.EnvironmentID != "e-pay" || session.IssueID != f.issue.ID || session.Phase != domain.CodingPhaseQueued || session.Source != domain.CodingSourceIssue || session.Harness != domain.CodingHarnessCodex {
		t.Fatalf("session = %+v", session)
	}
	if turns := f.fake.turns(session.ID); len(turns) != 1 || !strings.Contains(turns[0].Prompt, "Fix the 500") || !strings.Contains(turns[0].Prompt, f.issue.Identifier) {
		t.Fatalf("turns = %+v", turns)
	}
	// A second request continues the open session.
	code, body = f.post(t, path, map[string]any{"prompt": "Also add a test"}, nil)
	if code != http.StatusOK || !strings.Contains(body, `"outcome":"continued"`) || len(f.fake.turns(session.ID)) != 2 {
		t.Fatalf("status = %d body = %s", code, body)
	}
	if code, _ := f.post(t, "/api/issues/missing/coding-session", map[string]any{}, nil); code != http.StatusNotFound {
		t.Fatalf("missing issue: %d", code)
	}
}

func TestIssueCodingSessionRouteGuards(t *testing.T) {
	f := newCodingTriggerFixture(t)
	path := "/api/issues/" + f.issue.ID + "/coding-session"
	if code, body := f.post(t, path, map[string]any{}, nil); code != http.StatusConflict || !strings.Contains(body, `"code":"noPolicyRepos"`) {
		t.Fatalf("no environment: %d %s", code, body)
	}
	f.s.coding.config.Enabled = false
	if code, body := f.post(t, path, map[string]any{}, nil); code != http.StatusServiceUnavailable || !strings.Contains(body, "sandboxDisabled") {
		t.Fatalf("disabled: %d %s", code, body)
	}
	f.s.coding.config.Enabled = true
	if err := mutateTestWorkspace(t, f.repo, func(data *domain.Bootstrap) {
		if data.WorkspaceSettings.FeatureFlags == nil {
			data.WorkspaceSettings.FeatureFlags = map[string]bool{}
		}
		data.WorkspaceSettings.FeatureFlags["coding-sessions"] = false
	}); err != nil {
		t.Fatal(err)
	}
	if code, _ := f.post(t, path, map[string]any{}, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("feature off: %d", code)
	}
}

func TestIssueCodingSessionExternalApproval(t *testing.T) {
	f := newCodingTriggerFixture(t, githubEnvironment("e-web", "acme/web"))
	if err := mutateTestWorkspace(t, f.repo, func(data *domain.Bootstrap) {
		data.Asks = append(data.Asks, domain.Ask{ID: "ask1", Source: "email", IssueID: f.issue.ID, Title: "From email"})
	}); err != nil {
		t.Fatal(err)
	}
	code, body := f.post(t, "/api/issues/"+f.issue.ID+"/coding-session", map[string]any{"prompt": "fix it"}, nil)
	if code != http.StatusCreated || !strings.Contains(body, `"approvalRequired":true`) {
		t.Fatalf("status = %d body = %s", code, body)
	}
	var session domain.CodingSession
	for _, item := range f.fake.sessions {
		session = item
	}
	if session.Phase != domain.CodingPhaseAwaitingApproval {
		t.Fatalf("session = %+v", session)
	}
	data, _ := f.repo.WorkspaceMetadataFields("test-workspace", "teamMembers")
	_ = data
	// Approve through the elicitation answer hook.
	handled, err := f.s.handleCodingElicitationAnswer(context.Background(), "test-workspace", "usr_admin", "admin", "coding:approve:"+session.ID)
	if !handled || err != nil {
		t.Fatalf("approve: %t %v", handled, err)
	}
	if got := f.fake.sessions[session.ID]; got.Phase != domain.CodingPhaseQueued || got.ApprovalState != coding.ApprovalApproved {
		t.Fatalf("approved = %+v", got)
	}
	if handled, _ := f.s.handleCodingElicitationAnswer(context.Background(), "test-workspace", "u", "member", "something else"); handled {
		t.Fatal("other answers are not coding answers")
	}
	// Trusting email lets the next email issue start directly.
	if err := mutateTestWorkspace(t, f.repo, func(data *domain.Bootstrap) {
		data.WorkspaceSettings.CodingAgentSettings.TrustedExternalSources = []string{"email"}
	}); err != nil {
		t.Fatal(err)
	}
	f.fake.sessions[session.ID] = func() domain.CodingSession {
		s := f.fake.sessions[session.ID]
		s.Phase = domain.CodingPhaseDone
		return s
	}()
	code, body = f.post(t, "/api/issues/"+f.issue.ID+"/coding-session", map[string]any{"prompt": "fix it again"}, nil)
	if code != http.StatusCreated || strings.Contains(body, `"approvalRequired":true`) {
		t.Fatalf("trusted: %d %s", code, body)
	}
}

func TestReviewCodingSessionRoute(t *testing.T) {
	f := newCodingTriggerFixture(t, githubEnvironment("e-web", "acme/web"))
	if err := mutateTestWorkspace(t, f.repo, func(data *domain.Bootstrap) {
		data.Reviews = append(data.Reviews, domain.CodeReview{
			ID: "rev1", Provider: "github", Number: 12, Title: "Speed up checkout", Status: "open", RepositoryOwner: "acme", RepositoryName: "web",
			URL: "https://github.com/acme/web/pull/12", HeadBranch: "feature/checkout", BaseBranch: "main", IssueIDs: []string{f.issue.ID},
		})
	}); err != nil {
		t.Fatal(err)
	}
	path := "/api/reviews/rev1/coding-session"
	if code, body := f.post(t, path, map[string]any{"kind": "rebase"}, nil); code != http.StatusForbidden || !strings.Contains(body, "Confirm the force push") {
		t.Fatalf("rebase guard: %d %s", code, body)
	}
	if code, _ := f.post(t, path, map[string]any{"kind": "explode"}, nil); code != http.StatusBadRequest {
		t.Fatalf("bad kind: %d", code)
	}
	code, body := f.post(t, path, map[string]any{"kind": "fix"}, nil)
	if code != http.StatusCreated {
		t.Fatalf("fix: %d %s", code, body)
	}
	var session domain.CodingSession
	for _, item := range f.fake.sessions {
		session = item
	}
	if session.Source != domain.CodingSourcePullRequest || session.BranchName != "feature/checkout" || session.IssueID != f.issue.ID || len(f.fake.links) != 1 || f.fake.links[0].ReviewID != "rev1" {
		t.Fatalf("session = %+v links = %+v", session, f.fake.links)
	}
	if turns := f.fake.turns(session.ID); turns[0].Kind != coding.TurnKindFixPR {
		t.Fatalf("turns = %+v", turns)
	}
	// Quick actions continue the linked session.
	code, body = f.post(t, path, map[string]any{"kind": "lint"}, nil)
	if code != http.StatusOK || !strings.Contains(body, `"outcome":"continued"`) {
		t.Fatalf("lint: %d %s", code, body)
	}
	code, _ = f.post(t, path, map[string]any{"kind": "rebase", "confirmForcePush": true}, nil)
	turns := f.fake.turns(session.ID)
	if code != http.StatusOK || turns[len(turns)-1].Kind != coding.TurnKindRebase {
		t.Fatalf("confirmed rebase: %d %+v", code, turns)
	}
}

func TestStartCodingSessionTool(t *testing.T) {
	f := newCodingTriggerFixture(t, githubEnvironment("e-web", "acme/web"))
	data, _ := f.repo.WorkspaceMetadata("test-workspace")
	actor := mcpActor{WorkspaceKey: "test-workspace", User: data.Viewer}
	call := func(ctx context.Context, args map[string]any) map[string]any {
		t.Helper()
		result, err := f.s.startCodingSessionFromTool(ctx, actor, args)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(result)
		out := map[string]any{}
		_ = json.Unmarshal(raw, &out)
		return out
	}
	// The reference rule: an investigation does not start coding.
	out := call(context.Background(), map[string]any{"prompt": "Investigate the slow export", "userRequest": "investigate why export is slow", "issueId": f.issue.Identifier})
	if out["started"] != false || out["intent"] != "noCode" || len(f.fake.sessions) != 0 {
		t.Fatalf("investigate: %+v", out)
	}
	out = call(context.Background(), map[string]any{"prompt": "Fix the 500 in checkout", "userRequest": "fix this bug", "issueId": f.issue.Identifier})
	if out["started"] != true || out["outcome"] != "started" {
		t.Fatalf("fix: %+v", out)
	}

	// The Loop action honours "Repository access for Loops" and code access.
	loop := domain.Loop{ID: "loop1", OwnerID: data.Viewer.ID}
	ctx := withCodingLoopRun(context.Background(), loop, "run1", loopTrigger{EntityType: "issue", EntityID: f.issue.ID}, "read")
	out = call(ctx, map[string]any{"prompt": "Fix the bug"})
	if out["code"] != string(domain.CodingErrPermissionDenied) {
		t.Fatalf("read-only loop: %+v", out)
	}
	ctx = withCodingLoopRun(context.Background(), loop, "run1", loopTrigger{EntityType: "issue", EntityID: f.issue.ID}, "readWrite")
	f.fake.sessions = map[string]domain.CodingSession{}
	out = call(ctx, map[string]any{"prompt": "Fix the bug"})
	if out["code"] != string(domain.CodingErrRepoNotInPolicy) {
		t.Fatalf("loop without repository access: %+v", out)
	}
	if err := mutateTestWorkspace(t, f.repo, func(data *domain.Bootstrap) {
		data.WorkspaceSettings.CodingAgentSettings.LoopRepositoryAccess = "selected"
		data.WorkspaceSettings.CodingAgentSettings.LoopRepositories = []string{"e-web"}
		data.WorkspaceSettings.SpendLimits.LoopOverrides = map[string]float64{"loop1": 2}
	}); err != nil {
		t.Fatal(err)
	}
	f.fake.spend = 2_000_000
	if out = call(ctx, map[string]any{"prompt": "Fix the bug"}); out["code"] != string(domain.CodingErrQuotaExceeded) {
		t.Fatalf("loop over its limit: %+v", out)
	}
	f.fake.spend = 0
	out = call(ctx, map[string]any{"prompt": "Fix the bug"})
	if out["started"] != true {
		t.Fatalf("loop: %+v", out)
	}
	for _, session := range f.fake.sessions {
		if session.Source != domain.CodingSourceLoop || session.IssueID != f.issue.ID || session.EnvironmentID != "e-web" || session.CreatorID != data.Viewer.ID {
			t.Fatalf("loop session = %+v", session)
		}
	}
}

// codingFakeHost verifies a shared secret and returns a canned event.
type codingFakeHost struct {
	codehost.Stub
	event codehost.WebhookEvent
}

func (h *codingFakeHost) ParseWebhook(_ context.Context, header http.Header, _ []byte) (codehost.WebhookEvent, error) {
	if header.Get("X-Test-Token") != "secret" {
		return codehost.WebhookEvent{}, codehost.ErrBadSignature
	}
	return h.event, nil
}

func TestDispatchCodingWebhookFollowUp(t *testing.T) {
	f := newCodingTriggerFixture(t, githubEnvironment("e-web", "acme/web"))
	host := &codingFakeHost{Stub: codehost.Stub{HostKind: domain.CodeHostGitHub}}
	f.s.coding.hosts[domain.CodeHostGitHub] = host
	data, _ := f.repo.WorkspaceMetadata("test-workspace")
	login := data.Viewer.Name
	pr := &codehost.ChangeRequest{Repo: codehost.RepoRef{CodeHost: "github", HostName: "github.com", FullName: "acme/web"}, Number: 5, HeadBranch: "feature/x", BaseBranch: "main", State: "open"}
	host.event = codehost.WebhookEvent{DeliveryID: "d1", Type: codehost.EventComment, CodeHost: "github", HostName: "github.com", Repo: pr.Repo, ChangeRequest: pr, CommentID: "c1", CommentBody: "@flow add tests", AuthorLogin: login}
	header := http.Header{}
	header.Set("X-Test-Token", "secret")
	event, err := host.ParseWebhook(context.Background(), header, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.dispatchCodingWebhook(context.Background(), "test-workspace", event); err != nil {
		t.Fatal(err)
	}
	if len(f.fake.sessions) != 1 {
		t.Fatalf("sessions = %+v", f.fake.sessions)
	}
	var session domain.CodingSession
	for _, item := range f.fake.sessions {
		session = item
	}
	if session.Source != domain.CodingSourcePullRequest || session.Phase != domain.CodingPhaseQueued || session.CreatorID != data.Viewer.ID {
		t.Fatalf("session = %+v (viewer %s)", session, fmt.Sprint(data.Viewer.ID))
	}
	// A comment from someone who is not a member waits for approval.
	host.event.CommentID, host.event.AuthorLogin, host.event.CommentBody = "c2", "stranger", "@flow drop the tests"
	event, _ = host.ParseWebhook(context.Background(), header, nil)
	if err := f.s.dispatchCodingWebhook(context.Background(), "test-workspace", event); err != nil {
		t.Fatal(err)
	}
	gated := 0
	for _, item := range f.fake.sessions {
		if item.Phase == domain.CodingPhaseAwaitingApproval {
			gated++
		}
	}
	if gated != 1 {
		t.Fatalf("sessions = %+v", f.fake.sessions)
	}
	_ = time.Now
}

// Tracks D and J reach the dispatcher through these exact shapes.
func TestCodingWebhookDispatchEntryPoints(t *testing.T) {
	var s any = &server{}
	if _, ok := s.(interface {
		dispatchCodingWebhook(ctx context.Context, workspaceKey string, event codehost.WebhookEvent) error
	}); !ok {
		t.Fatal("dispatchCodingWebhook signature changed (track J)")
	}
	if codingWebhookDispatch == nil {
		t.Fatal("codingWebhookDispatch is not wired (track D)")
	}
	// Coding off: dispatch is a no-op.
	if err := codingWebhookDispatch(&server{coding: &codingServices{}}, context.Background(), "ws", codehost.WebhookEvent{Type: codehost.EventComment}); err != nil {
		t.Fatal(err)
	}
}

// The UI's explicit start, the agent's tool and retries on a delegated issue
// converge on one session per delegation, even when they race.
func TestDelegatedIssueStartIsIdempotentPerAnchor(t *testing.T) {
	f := newCodingTriggerFixture(t, githubEnvironment("e-web", "acme/web"))
	triggers, ok := f.s.codingTriggers("test-workspace")
	if !ok {
		t.Fatal("triggers unavailable")
	}
	data, _ := f.repo.WorkspaceMetadata("test-workspace")
	issue := f.issue
	issue.Delegate = &domain.User{ID: "app_flow", App: true, BuiltinAgent: true}
	issue.AgentSessionID = "task-delegation-1"

	start := func(header string, viewer domain.User) coding.TriggerResult {
		t.Helper()
		trigger := f.s.issueCodingTrigger(context.Background(), "test-workspace", data, issue, viewer)
		trigger.Prompt = coding.IssuePrompt(*trigger.Issue, "")
		trigger.IdempotencyKey = issueCodingIdempotencyKey(trigger, issue.ID, header)
		result, err := triggers.StartForIssue(context.Background(), trigger)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	// Two racing starts (different client keys) before either sees the other.
	var wg sync.WaitGroup
	results := make([]coding.TriggerResult, 4)
	for index := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[index] = start(fmt.Sprintf("client-%d", index), data.Viewer)
		}()
	}
	wg.Wait()
	again := start("", domain.User{ID: "app_flow", App: true, BuiltinAgent: true})
	if len(f.fake.sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(f.fake.sessions))
	}
	var session domain.CodingSession
	for _, item := range f.fake.sessions {
		session = item
	}
	if session.AgentSessionID != "task-delegation-1" {
		t.Fatalf("anchor = %q", session.AgentSessionID)
	}
	for _, result := range append(results, again) {
		if result.Session == nil || result.Session.ID != session.ID {
			t.Fatalf("result = %+v", result)
		}
	}
	if turns := f.fake.turns(session.ID); len(turns) != 1 {
		t.Fatalf("a repeated start must not add turns: %+v", turns)
	}
	if key := issueCodingIdempotencyKey(coding.IssueTrigger{}, "i1", "k"); key != "issue:i1:k" {
		t.Fatalf("undelegated key = %q", key)
	}
}

// TestCodingTriggerErrorStatus: a trigger that fails with a known service
// error (here: the built-in Flow agent is not enabled) answers like the
// sessions API with its code, not a bare 500.
func TestCodingTriggerErrorStatus(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeCodingTriggerError(recorder, coding.ErrNoAgent)
	var body map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &body)
	if recorder.Code != http.StatusConflict || body["code"] != "agentUnavailable" || !strings.Contains(fmt.Sprint(body["error"]), "built-in Flow agent") {
		t.Fatalf("no agent: %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	writeCodingTriggerError(recorder, fmt.Errorf("%w: model %q is not in the pi catalog", coding.ErrInvalidInput, "x"))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "not in the pi catalog") {
		t.Fatalf("invalid input: %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	writeCodingTriggerError(recorder, fmt.Errorf("database is locked"))
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "database") {
		t.Fatalf("an unknown error stays an opaque 500: %d %s", recorder.Code, recorder.Body.String())
	}
}
