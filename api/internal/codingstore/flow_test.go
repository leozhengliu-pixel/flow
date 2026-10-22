package codingstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"flow/api/internal/codehost"
	"flow/api/internal/coding"
	"flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
	"flow/api/internal/store"
)

// Integration-style tests of the session core on a real SQLite store with
// the Fake sandbox provider. simulateSandboxes stands in for track A's
// sandbox reconciler: it starts and stops Fake sandboxes for the desired
// state of coding_sandboxes rows, writes the observation back and mints the
// sandbox token, exactly through the table contract.

const workspace = "acme"

type harness struct {
	t        *testing.T
	repo     *store.SQLiteStore
	raw      *sql.DB
	store    Store
	svc      *coding.Service
	fake     *sandbox.Fake
	host     *fakeHost
	cfg      config.CodingConfig
	tokens   map[string]string // sandbox id → token
	settings domain.WorkspaceSettings
	mu       sync.Mutex
}

type fakeEnvs struct{ envs []domain.CodingEnvironment }

func (f fakeEnvs) CodingEnvironment(_ context.Context, _ string, id string) (domain.CodingEnvironment, error) {
	for _, env := range f.envs {
		if env.ID == id {
			return env, nil
		}
	}
	return domain.CodingEnvironment{}, coding.ErrNotFound
}
func (f fakeEnvs) ListCodingEnvironments(context.Context, string, bool) ([]domain.CodingEnvironment, error) {
	return f.envs, nil
}
func (f fakeEnvs) CodingEnvironmentSecretValues(context.Context, string, string) ([]domain.CodingEnvVar, error) {
	return []domain.CodingEnvVar{{Name: "API_KEY", Value: "secret"}}, nil
}

type fakeHost struct {
	codehost.Stub
	mu        sync.Mutex
	head      string
	created   int
	failNext  error
	minted    int
	revoked   []string
	changeReq *codehost.ChangeRequest
	// branchHeads counts publish-stage head checks.
	branchHeads int
}

func (f *fakeHost) Connected(context.Context, string) (bool, error) { return true, nil }
func (f *fakeHost) CheckUserAccess(context.Context, string, codehost.RepoRef, codehost.UserIdentity) error {
	return nil
}
func (f *fakeHost) BranchHead(context.Context, string, codehost.RepoRef, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.branchHeads++
	if err := f.failNext; err != nil {
		f.failNext = nil
		return "", err
	}
	return f.head, nil
}
func (f *fakeHost) FindChangeRequest(context.Context, string, codehost.RepoRef, string) (*codehost.ChangeRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.changeReq, nil
}
func (f *fakeHost) CreateChangeRequest(_ context.Context, _ string, input codehost.CreateChangeRequestInput) (codehost.ChangeRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created++
	change := codehost.ChangeRequest{Repo: input.Repo, Number: 42, URL: "https://github.com/acme/app/pull/42", Title: input.Title, State: "open", Draft: input.Draft, HeadBranch: input.Head, BaseBranch: input.Base}
	f.changeReq = &change
	return change, nil
}
func (f *fakeHost) MintCredential(context.Context, codehost.CredentialRequest) (codehost.Credential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.minted++
	return codehost.Credential{Username: "x-access-token", Password: "ghs_test", ExpiresAt: time.Now().Add(time.Hour), CLIEnv: map[string]string{"GH_TOKEN": "ghs_test"}}, nil
}
func (f *fakeHost) RevokeSessionCredentials(_ context.Context, _ string, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, sessionID)
	return nil
}

type harnessWorkspaces struct{ h *harness }

func (w harnessWorkspaces) CodingWorkspace(context.Context, string) (coding.WorkspaceInfo, error) {
	w.h.mu.Lock()
	defer w.h.mu.Unlock()
	return coding.WorkspaceInfo{Settings: w.h.settings, CreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}, nil
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	path := filepath.Join(t.TempDir(), "coding.db")
	repo, err := store.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { raw.Close() })
	if _, err := raw.Exec(`INSERT INTO application_installations(id,workspace_key,client_id,user_id,data,webhook_secret) VALUES('app1',?,?,'flow-agent','{}','x')`, workspace, store.BuiltinApplicationClientID); err != nil {
		t.Fatal(err)
	}
	cfg := config.CodingConfig{
		Enabled: true, Provider: config.CodingProviderFake, MaxSessions: 10, WorkspaceSessions: 5, ReconcileInterval: 10 * time.Millisecond,
		Timeouts:   config.CodingTimeouts{Queue: time.Hour, Approval: time.Hour, Provision: time.Hour, Start: time.Hour, Setup: time.Hour, Turn: time.Hour, Heartbeat: time.Hour, Publish: time.Hour, Idle: time.Hour, StopGrace: time.Minute, MaxSession: 4 * time.Hour},
		Retries:    config.CodingRetries{Provision: 3, Start: 2, Publish: 5},
		Sizes:      map[string]config.SandboxSize{"medium": {CPU: "2", Memory: "16Gi"}},
		LLM:        config.CodingLLMConfig{CodexModels: []string{"gpt-5-codex", "gpt-5"}},
		RuntimeURL: "http://flow.test", BranchPrefix: "flow/", Image: "flow-sandbox:test", RuntimePricesUSD: map[string]float64{"medium": 3600},
	}
	h := &harness{t: t, repo: repo, raw: raw, store: New(repo), fake: sandbox.NewFake(nil), host: &fakeHost{Stub: codehost.Stub{HostKind: domain.CodeHostGitHub}, head: "abc123"}, cfg: cfg, tokens: map[string]string{}}
	h.settings = domain.WorkspaceSettings{FeatureFlags: map[string]bool{coding.FeatureFlag: true}}
	h.svc = h.newService(nil)
	return h
}

func (h *harness) newService(guard coding.SpendGuard) *coding.Service {
	envs := fakeEnvs{envs: []domain.CodingEnvironment{{ID: "env1", Name: "App", CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: "acme/app", DefaultBranch: "main", Secrets: []domain.CodingSecretRef{{Name: "API_KEY"}}}}}
	return coding.NewService(coding.Options{Config: h.cfg, Store: h.store, Anchors: h.store, Workspaces: harnessWorkspaces{h}, Environments: envs, Hosts: map[string]codehost.Host{domain.CodeHostGitHub: h.host}, Guard: guard})
}

func (h *harness) reconciler(owner string) *coding.Reconciler {
	return &coding.Reconciler{Service: h.svc, Owner: owner, ProcessStart: time.Now().Add(-time.Hour), Meter: &coding.Meter{Store: h.store, Config: h.cfg}}
}

// simulateSandboxes is the stand-in for track A's sandbox reconciler.
func (h *harness) simulateSandboxes() {
	h.t.Helper()
	rows, err := h.raw.Query(`SELECT id,session_id,desired,phase FROM coding_sandboxes`)
	if err != nil {
		h.t.Fatal(err)
	}
	type row struct{ id, session, desired, phase string }
	list := []row{}
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.id, &item.session, &item.desired, &item.phase); err != nil {
			h.t.Fatal(err)
		}
		list = append(list, item)
	}
	rows.Close()
	now := time.Now().UnixMilli()
	for _, item := range list {
		switch {
		case item.desired == "running" && item.phase == "pending":
			status, err := h.fake.StartSandbox(context.Background(), sandbox.SandboxSpec{ID: item.id, Kind: sandbox.KindSession, SessionID: item.session})
			if err != nil {
				h.exec(`UPDATE coding_sandboxes SET phase='failed',reason=? WHERE id=?`, string(sandbox.ReasonImagePull), item.id)
				continue
			}
			token := coding.SandboxTokenPrefix + item.id
			h.tokens[item.id] = token
			h.exec(`INSERT INTO coding_sandbox_tokens(token_hash,sandbox_id,session_id,workspace_key,expires_at,created_at) VALUES(?,?,?,?,?,?) ON CONFLICT(token_hash) DO NOTHING`, hash(token), item.id, item.session, workspace, now+3600_000, now)
			h.exec(`UPDATE coding_sandboxes SET phase=?,started_at=?,provider_ref=? WHERE id=?`, string(status.Phase), now, status.ProviderRef, item.id)
		case item.desired == "stopped" && (item.phase == "running" || item.phase == "pending"):
			_ = h.fake.StopSandbox(context.Background(), item.id, sandbox.StopOptions{})
			h.exec(`UPDATE coding_sandboxes SET phase='succeeded',ended_at=? WHERE id=?`, now, item.id)
		}
	}
}

func (h *harness) exec(query string, args ...any) {
	h.t.Helper()
	if _, err := h.raw.Exec(query, args...); err != nil {
		h.t.Fatalf("%s: %v", query, err)
	}
}

func hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (h *harness) session(id string) domain.CodingSession {
	h.t.Helper()
	session, err := h.store.CodingSession(context.Background(), workspace, id)
	if err != nil {
		h.t.Fatal(err)
	}
	return session
}

func (h *harness) activities(anchor string) []domain.AgentActivity {
	h.t.Helper()
	all := []domain.AgentActivity{}
	after := ""
	for {
		page, err := h.repo.AgentActivities(context.Background(), workspace, anchor, after)
		if err != nil {
			h.t.Fatal(err)
		}
		all = append(all, page...)
		if len(page) < 100 {
			return all
		}
		after = page[len(page)-1].ID
	}
}

func (h *harness) start(prompt string) domain.CodingSession {
	h.t.Helper()
	session, created, err := h.svc.Start(context.Background(), coding.StartInput{WorkspaceKey: workspace, Source: domain.CodingSourceIssue, Prompt: prompt, CreatorID: "user1", IdempotencyKey: "key-" + prompt})
	if err != nil || !created {
		h.t.Fatalf("start: %v %v", created, err)
	}
	return session
}

// drive reconciles and simulates sandboxes until the session reaches phase.
func (h *harness) drive(id string, phase domain.CodingSessionPhase) domain.CodingSession {
	h.t.Helper()
	reconciler := h.reconciler("test-owner")
	for range 50 {
		h.exec(`UPDATE coding_sessions SET next_reconcile_at=0 WHERE id=? AND lease_until=0`, id)
		reconciler.RunOnce(context.Background())
		h.simulateSandboxes()
		if session := h.session(id); session.Phase == phase {
			return session
		}
	}
	session := h.session(id)
	h.t.Fatalf("session stuck in %s (%s %s), want %s", session.Phase, session.ErrorCode, session.Error, phase)
	return session
}

func TestSessionLifecycleOnFakeProvider(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	session := h.start("Fix the login bug")
	if session.Phase != domain.CodingPhaseQueued || session.State != domain.CodingSessionPending || session.Harness != domain.CodingHarnessCodex || !strings.HasPrefix(session.BranchName, "flow/") {
		t.Fatalf("started: %+v", session)
	}
	if again, created, err := h.svc.Start(ctx, coding.StartInput{WorkspaceKey: workspace, Source: domain.CodingSourceIssue, Prompt: "Fix the login bug", CreatorID: "user1", IdempotencyKey: "key-Fix the login bug"}); err != nil || created || again.ID != session.ID {
		t.Fatalf("idempotent start: %v %v %s", created, err, again.ID)
	}
	if anchor, err := h.repo.AgentTask(ctx, workspace, session.AgentSessionID); err != nil || anchor.Trigger != coding.AnchorTrigger || anchor.AppUserID != "flow-agent" {
		t.Fatalf("anchor: %+v %v", anchor, err)
	}
	if !h.repo.IsCodingAnchor(ctx, workspace, session.AgentSessionID) {
		t.Fatal("the anchor must be recognised so the application worker skips it")
	}
	session = h.drive(session.ID, domain.CodingPhaseStarting)
	if session.SandboxID == "" || session.Model != "gpt-5-codex" || session.Repository != "acme/app" || session.BaseRef != "main" {
		t.Fatalf("provisioned: %+v", session)
	}
	h.simulateSandboxes()
	token := h.tokens[session.SandboxID]

	boot, err := h.svc.Bootstrap(ctx, token, 0)
	if err != nil {
		t.Fatal(err)
	}
	if boot.Turn == nil || boot.Turn.Prompt != "Fix the login bug" || boot.Repo.CloneURL != "https://github.com/acme/app.git" || boot.Harness.Model != "gpt-5-codex" || !boot.Environment.HasSecrets || boot.LLM.OpenAIBaseURL != "http://flow.test/internal/llm-proxy/openai/v1" || !strings.Contains(boot.Guidance, boot.Repo.BranchName) || boot.Repo.BranchPrefix != "flow/" {
		t.Fatalf("bootstrap: %+v", boot)
	}
	if again, err := h.svc.Bootstrap(ctx, token, 0); err != nil || again.Turn == nil || again.Turn.ID != boot.Turn.ID {
		t.Fatalf("a repeated bootstrap returns the same turn: %v", err)
	}
	if session = h.session(session.ID); session.Phase != domain.CodingPhaseSetup || session.CurrentTurnID != boot.Turn.ID {
		t.Fatalf("after bootstrap: %s %s", session.Phase, session.CurrentTurnID)
	}
	secrets, err := h.svc.SetupSecrets(ctx, token)
	if err != nil || len(secrets.Variables) != 1 {
		t.Fatalf("setup secrets: %v %+v", err, secrets)
	}
	if _, err := h.svc.SetupSecrets(ctx, token); !errors.Is(err, coding.ErrAlreadyUsed) {
		t.Fatalf("setup secrets are served once: %v", err)
	}
	if beat, err := h.svc.Heartbeat(ctx, token, coding.HeartbeatRequest{Phase: "running"}); err != nil || beat.Stop {
		t.Fatalf("heartbeat: %+v %v", beat, err)
	}
	if session = h.session(session.ID); session.Phase != domain.CodingPhaseRunning || session.TurnStartedAt == nil {
		t.Fatalf("setup must finish on a running heartbeat: %s", session.Phase)
	}
	if _, err := h.svc.SetupSecrets(ctx, token); !errors.Is(err, coding.ErrForbidden) {
		t.Fatalf("setup secrets only during setup: %v", err)
	}
	credential, err := h.svc.Credentials(ctx, token, coding.CredentialRequest{Protocol: "https", Host: "github.com", Path: "acme/app.git", Purpose: "cli"})
	if err != nil || credential.Password != "ghs_test" || credential.CLIEnv["GH_TOKEN"] == "" {
		t.Fatalf("credential: %+v %v", credential, err)
	}
	if _, err := h.svc.Credentials(ctx, token, coding.CredentialRequest{Protocol: "https", Host: "github.com", Path: "acme/other.git", Purpose: "git"}); !errors.Is(err, coding.ErrForbidden) {
		t.Fatalf("credentials are scoped to the session repository: %v", err)
	}

	exit := 0
	batch := coding.EventsRequest{Events: []coding.RunnerEvent{
		{ID: "e1", TurnID: boot.Turn.ID, Type: coding.EventThought, Body: "Looking at the login form"},
		{ID: "e2", TurnID: boot.Turn.ID, Type: coding.EventAction, Action: &coding.ActionDetail{Action: "Ran command", Parameter: "npm test", Result: "ok", ExitCode: &exit}},
		{ID: "e3", TurnID: boot.Turn.ID, Type: coding.EventPlan, Plan: []domain.AgentPlanStep{{Content: "Fix", Status: "in_progress"}, {Content: "Test", Status: "pending"}}},
		{ID: "e4", TurnID: boot.Turn.ID, Type: coding.EventDiff, Diff: []domain.CodingDiffSummary{{Path: "login.ts", Additions: 3, Deletions: 1, Status: "modified"}}},
		{ID: "e5", TurnID: boot.Turn.ID, Type: coding.EventResponse, Body: "Fixed the login bug."},
		{ID: "e6", TurnID: "old-turn", Type: coding.EventThought, Body: "stale"},
	}}
	result, err := h.svc.Events(ctx, token, batch)
	if err != nil || result.Accepted != 5 || result.Ignored != 1 {
		t.Fatalf("events: %+v %v", result, err)
	}
	result, err = h.svc.Events(ctx, token, batch)
	if err != nil || result.Duplicates != 5 || result.Accepted != 0 {
		t.Fatalf("a retried batch must be deduplicated: %+v %v", result, err)
	}
	activities := h.activities(session.AgentSessionID)
	if len(activities) != 2 || activities[0].Type != "thought" || activities[1].Type != "action" || activities[1].Metadata["action"] != "Ran command" || activities[1].Metadata["parameter"] != "npm test" {
		t.Fatalf("activities in order: %+v", activities)
	}
	session = h.session(session.ID)
	if len(session.Plan) != 2 || session.Plan[0].Status != "inProgress" || len(session.Diffs) != 1 || session.Summary != "Fixed the login bug." {
		t.Fatalf("session after events: %+v", session)
	}
	if anchor, _ := h.repo.AgentTask(ctx, workspace, session.AgentSessionID); len(anchor.Plan) != 2 {
		t.Fatalf("the plan is mirrored on the anchor: %+v", anchor.Plan)
	}

	if _, err := h.svc.CompleteTurn(ctx, token, "not-current", coding.TurnCompleteRequest{Status: coding.TurnSuccess}); !errors.Is(err, coding.ErrConflict) {
		t.Fatalf("only the current turn can complete: %v", err)
	}
	complete := coding.TurnCompleteRequest{Status: coding.TurnSuccess, Pushed: true, HeadSHA: "abc123", HarnessSessionID: "thread-1", Commits: []domain.CodingCommit{{SHA: "abc123", Message: "Fix login"}}}
	session, err = h.svc.CompleteTurn(ctx, token, boot.Turn.ID, complete)
	if err != nil || session.Phase != domain.CodingPhasePublishing {
		t.Fatalf("complete: %s %v", session.Phase, err)
	}
	if again, err := h.svc.CompleteTurn(ctx, token, boot.Turn.ID, complete); err != nil || again.Version != session.Version {
		t.Fatalf("turn completion is idempotent: %v", err)
	}
	session = h.drive(session.ID, domain.CodingPhaseIdle)
	if session.State != domain.CodingSessionComplete || session.LatestPullRequest == nil || session.LatestPullRequest.Number != 42 || h.host.created != 1 || session.HarnessResumeID != "thread-1" {
		t.Fatalf("published: %+v", session)
	}
	anchor, _ := h.repo.AgentTask(ctx, workspace, session.AgentSessionID)
	activities = h.activities(session.AgentSessionID)
	last := activities[len(activities)-1]
	if anchor.Status != "complete" || last.Type != "response" || !strings.Contains(last.Body, "Fixed the login bug.") || !strings.Contains(last.Body, "pull/42") || len(anchor.ExternalURLs) != 1 {
		t.Fatalf("response activity with the PR link: %s %+v %+v", anchor.Status, last, anchor.ExternalURLs)
	}
	full, err := h.svc.Get(ctx, workspace, session.ID)
	if err != nil || len(full.Sandboxes) != 1 || full.Sandboxes[0].Status != "running" || len(full.CommitsByTurnActivityID[boot.Turn.ID]) != 1 {
		t.Fatalf("get: %+v %v", full, err)
	}

	// A follow-up on the idle session is delivered to the warm runner.
	if _, err := h.svc.Prompt(ctx, workspace, session.ID, coding.PromptInput{Prompt: "Also fix the logout", AuthorID: "user1"}); err != nil {
		t.Fatal(err)
	}
	inbox, err := h.svc.Inbox(ctx, token, 0, 0)
	if err != nil || len(inbox.Items) != 1 || inbox.Items[0].Turn == nil || inbox.Items[0].Turn.Prompt != "Also fix the logout" {
		t.Fatalf("inbox: %+v %v", inbox, err)
	}
	session = h.session(session.ID)
	if session.Phase != domain.CodingPhaseRunning || session.CurrentTurnID != inbox.Items[0].Turn.ID {
		t.Fatalf("delivering the turn starts it: %s", session.Phase)
	}

	// Stop: the runner hears it, completes with stopped, the sandbox drains.
	if _, err := h.svc.Stop(ctx, workspace, session.ID, "user1", false); err != nil {
		t.Fatal(err)
	}
	session = h.drive(session.ID, domain.CodingPhaseStopping)
	if beat, _ := h.svc.Heartbeat(ctx, token, coding.HeartbeatRequest{Phase: "running"}); !beat.Stop {
		t.Fatal("the heartbeat must tell the runner to stop")
	}
	if _, err := h.svc.CompleteTurn(ctx, token, session.CurrentTurnID, coding.TurnCompleteRequest{Status: coding.TurnStopped}); err != nil {
		t.Fatal(err)
	}
	h.exec(`UPDATE coding_sandboxes SET desired='stopped' WHERE id=?`, session.SandboxID) // the grace period passed
	session = h.drive(session.ID, domain.CodingPhaseDone)
	if session.State != domain.CodingSessionStale {
		t.Fatalf("a stopped session is stale, got %s", session.State)
	}
	if _, err := h.svc.Authenticate(ctx, token); !errors.Is(err, coding.ErrTokenInvalid) {
		t.Fatalf("tokens are revoked when the session ends: %v", err)
	}
	if len(h.host.revoked) != 1 || h.host.revoked[0] != session.ID {
		t.Fatalf("host credentials revoked at session end: %v", h.host.revoked)
	}
	// Runtime is billed for the sandbox (5-minute increments while running, the rest at the end).
	var usage int
	if err := h.raw.QueryRow(`SELECT COUNT(*) FROM coding_usage_records WHERE session_id=? AND kind='runtime'`, session.ID).Scan(&usage); err != nil {
		t.Fatal(err)
	}

	// Restart with the default model re-queues the last turn.
	restarted, err := h.svc.Restart(ctx, workspace, session.ID, "user1", true)
	if err != nil || restarted.Phase != domain.CodingPhaseQueued || restarted.ModelSelection != domain.CodingModelAuto || !restarted.ExistingBranch {
		t.Fatalf("restart: %+v %v", restarted, err)
	}
	if pending, _ := h.store.PendingCodingTurns(ctx, session.ID); pending != 1 {
		t.Fatalf("restart enqueues the last turn again: %d", pending)
	}
}

func TestReconcilerRestartSafety(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	session := h.start("Refactor")

	// A reconciler leases the session and crashes before releasing it.
	crashed, err := h.store.LeaseCodingSessions(ctx, "crashed-owner", 50*time.Millisecond, 10)
	if err != nil || len(crashed) != 1 {
		t.Fatalf("lease: %d %v", len(crashed), err)
	}
	if other, _ := h.store.LeaseCodingSessions(ctx, "second-owner", time.Minute, 10); len(other) != 0 {
		t.Fatal("a leased session must not be leased twice")
	}
	time.Sleep(80 * time.Millisecond)
	// A fresh process (new owner, new process start) picks it up once the lease expired.
	fresh := h.reconciler("fresh-owner")
	if handled := fresh.RunOnce(ctx); handled != 1 {
		t.Fatalf("the expired lease must be re-leased, handled %d", handled)
	}
	if err := h.store.ReleaseCodingSession(ctx, "crashed-owner", session.ID, time.Now()); !errors.Is(err, coding.ErrLeaseLost) {
		t.Fatalf("the crashed owner's release must fail: %v", err)
	}
	session = h.session(session.ID)
	if session.Phase != domain.CodingPhaseStarting || session.SandboxID == "" {
		t.Fatalf("after the fresh pass: %s", session.Phase)
	}

	// The session update after provisioning is lost (crash between the
	// sandbox row and the session CAS): provisioning again reuses the row.
	sandboxID := session.SandboxID
	if _, err := h.store.UpdateCodingSession(ctx, workspace, session.ID, func(current *domain.CodingSession) error {
		current.SandboxID = ""
		h.svc.StepConfig().Enter(current, domain.CodingPhaseProvisioning, time.Now())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	session = h.drive(session.ID, domain.CodingPhaseStarting)
	records, _ := h.store.CodingSandboxes(ctx, workspace, session.ID)
	if len(records) != 1 || session.SandboxID != sandboxID {
		t.Fatalf("provisioning must be idempotent: %d sandboxes, %s vs %s", len(records), session.SandboxID, sandboxID)
	}

	// A start failure retries with a new sandbox; the old sandbox's token is refused.
	h.simulateSandboxes()
	oldToken := h.tokens[sandboxID]
	h.exec(`UPDATE coding_sandboxes SET phase='failed',reason='ImagePullFailed' WHERE id=?`, sandboxID)
	session = h.drive(session.ID, domain.CodingPhaseStarting)
	if session.SandboxID == sandboxID || session.SandboxAttempts != 1 {
		t.Fatalf("retry with a new sandbox id: %+v", session)
	}
	if _, err := h.svc.Bootstrap(ctx, oldToken, 0); !errors.Is(err, coding.ErrTokenInvalid) && !errors.Is(err, coding.ErrGone) {
		t.Fatalf("the failed sandbox's token must not bootstrap: %v", err)
	}
	h.simulateSandboxes()
	if _, err := h.svc.Bootstrap(ctx, h.tokens[session.SandboxID], 0); err != nil {
		t.Fatal(err)
	}

	// Concurrent reconcilers never lease the same session.
	h.exec(`UPDATE coding_sessions SET next_reconcile_at=0`)
	var wg sync.WaitGroup
	counts := make([]int, 4)
	for index := range counts {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			leased, err := h.store.LeaseCodingSessions(ctx, "owner-"+string(rune('a'+index)), time.Minute, 10)
			if err == nil {
				counts[index] = len(leased)
			}
		}(index)
	}
	wg.Wait()
	if total := counts[0] + counts[1] + counts[2] + counts[3]; total != 1 {
		t.Fatalf("the session was leased %d times", total)
	}
}

func TestRuntimeAuthentication(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for _, token := range []string{"", "not-a-token", coding.SandboxTokenPrefix + "unknown"} {
		if _, err := h.svc.Bootstrap(ctx, token, 0); !errors.Is(err, coding.ErrTokenInvalid) {
			t.Fatalf("token %q: %v", token, err)
		}
	}
	session := h.drive(h.start("Auth").ID, domain.CodingPhaseStarting)
	h.simulateSandboxes()
	token := h.tokens[session.SandboxID]
	if _, err := h.svc.Bootstrap(ctx, token, 0); err != nil {
		t.Fatal(err)
	}
	if grant, err := h.svc.Authenticate(ctx, token); err != nil || grant.EnvironmentID != "env1" || grant.SessionID != session.ID || grant.Kind != sandbox.KindSession {
		t.Fatalf("grant: %+v %v", grant, err)
	}
	// An expired token is refused.
	h.exec(`UPDATE coding_sandbox_tokens SET expires_at=1 WHERE sandbox_id=?`, session.SandboxID)
	if _, err := h.svc.Heartbeat(ctx, token, coding.HeartbeatRequest{}); !errors.Is(err, coding.ErrTokenInvalid) {
		t.Fatalf("expired: %v", err)
	}
	h.exec(`UPDATE coding_sandbox_tokens SET expires_at=? WHERE sandbox_id=?`, time.Now().Add(time.Hour).UnixMilli(), session.SandboxID)
	// The LLM proxy authenticator resolves the caller with the session's model.
	caller, err := h.svc.AuthenticateLLMCaller(ctx, token)
	if err != nil || caller.Model != "gpt-5-codex" || caller.UserID != "user1" || caller.SessionID != session.ID {
		t.Fatalf("llm caller: %+v %v", caller, err)
	}
	if err := h.store.RevokeSandboxTokens(ctx, session.SandboxID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.AuthenticateLLMCaller(ctx, token); err == nil {
		t.Fatal("a revoked token must not reach the LLM proxy")
	}
	// Event batches are bounded.
	events := make([]coding.RunnerEvent, coding.MaxEventsPerRequest+1)
	if _, err := h.svc.Events(ctx, token, coding.EventsRequest{Events: events}); !errors.Is(err, coding.ErrInvalidInput) {
		t.Fatalf("oversized batch: %v", err)
	}
}

func TestSpendLimitsEndSessions(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.settings.SpendLimits = domain.SpendLimitSettings{UserLimit: 1, UserReset: "daily"}
	guard := &coding.SpendLimiter{Usage: h.store, Workspaces: harnessWorkspaces{h}}
	h.svc = h.newService(guard)
	session := h.drive(h.start("Spend").ID, domain.CodingPhaseStarting)

	// LLM usage over the limit ends the running session with quotaExceeded.
	meter := &coding.Meter{Store: h.store, Guard: guard, Config: h.cfg, OnQuotaExceeded: func(ctx context.Context, ws, id string, err error) {
		if _, failErr := h.svc.FailSession(ctx, ws, id, domain.CodingErrQuotaExceeded, err.Error()); failErr != nil {
			t.Error(failErr)
		}
	}}
	llm := &coding.LLMMeter{Meter: meter, Guard: guard}
	caller := struct{ ws, user string }{workspace, "user1"}
	if err := llm.RecordUsage(ctx, domain.CodingUsageRecord{WorkspaceKey: caller.ws, SessionID: session.ID, UserID: caller.user, Model: "gpt-5-codex", CostMicros: 1_500_000}); err != nil {
		t.Fatal(err)
	}
	session = h.session(session.ID)
	if session.Phase != domain.CodingPhaseDone || session.ErrorCode != domain.CodingErrQuotaExceeded || session.CostMicros != 1_500_000 {
		t.Fatalf("over the limit: %s %s cost=%d", session.Phase, session.ErrorCode, session.CostMicros)
	}
	records, _ := h.store.CodingSandboxes(ctx, workspace, session.ID)
	if len(records) != 1 || records[0].Desired != sandbox.DesiredStopped {
		t.Fatalf("the sandbox is stopped: %+v", records)
	}
	// New sessions fail at once; follow-ups and restarts are refused.
	blocked, created, err := h.svc.Start(ctx, coding.StartInput{WorkspaceKey: workspace, Source: domain.CodingSourceIssue, Prompt: "More", CreatorID: "user1"})
	if err != nil || !created || blocked.Phase != domain.CodingPhaseDone || blocked.ErrorCode != domain.CodingErrQuotaExceeded {
		t.Fatalf("blocked start: %+v %v", blocked, err)
	}
	if _, err := h.svc.Restart(ctx, workspace, session.ID, "user1", false); !errors.Is(err, coding.ErrQuotaExceeded) {
		t.Fatalf("restart over the limit: %v", err)
	}
	// Another user is not affected by user1's limit; an override lifts user1's.
	if other, _, err := h.svc.Start(ctx, coding.StartInput{WorkspaceKey: workspace, Source: domain.CodingSourceIssue, Prompt: "Other", CreatorID: "user2"}); err != nil || other.Phase != domain.CodingPhaseQueued {
		t.Fatalf("other user: %+v %v", other, err)
	}
	h.settings.SpendLimits.UserOverrides = map[string]float64{"user1": 10}
	if lifted, _, err := h.svc.Start(ctx, coding.StartInput{WorkspaceKey: workspace, Source: domain.CodingSourceIssue, Prompt: "Lifted", CreatorID: "user1"}); err != nil || lifted.Phase != domain.CodingPhaseQueued {
		t.Fatalf("override: %+v %v", lifted, err)
	}
	// Usage records are idempotent by id.
	record := domain.CodingUsageRecord{ID: "u1", WorkspaceKey: workspace, Kind: coding.UsageLLM, UserID: "user3", CostMicros: 5}
	for range 2 {
		if err := h.store.RecordCodingUsage(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	if spent, _ := h.store.CodingSpendMicros(ctx, coding.SpendQuery{WorkspaceKey: workspace, UserID: "user3"}); spent != 5 {
		t.Fatalf("idempotent usage: %d", spent)
	}
}

func TestRuntimeBillingIsIdempotent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	session := h.drive(h.start("Bill").ID, domain.CodingPhaseStarting)
	h.simulateSandboxes()
	started := time.Now().Add(-12 * time.Minute)
	h.exec(`UPDATE coding_sandboxes SET started_at=? WHERE id=?`, started.UnixMilli(), session.SandboxID)
	meter := &coding.Meter{Store: h.store, Config: h.cfg}
	for range 3 {
		record, _ := h.store.CodingSandbox(ctx, session.SandboxID)
		if err := meter.BillSandbox(ctx, record, "user1", ""); err != nil {
			t.Fatal(err)
		}
	}
	if billed, _ := h.store.CodingSandboxBilledSeconds(ctx, session.SandboxID); billed != 600 {
		t.Fatalf("two whole 5-minute increments while running, got %d", billed)
	}
	h.exec(`UPDATE coding_sandboxes SET ended_at=? WHERE id=?`, started.Add(12*time.Minute+30*time.Second).UnixMilli(), session.SandboxID)
	record, _ := h.store.CodingSandbox(ctx, session.SandboxID)
	_ = meter.BillSandbox(ctx, record, "user1", "")
	_ = meter.BillSandbox(ctx, record, "user1", "")
	if billed, _ := h.store.CodingSandboxBilledSeconds(ctx, session.SandboxID); billed != 750 {
		t.Fatalf("the remainder is billed at the end, got %d", billed)
	}
	if spent, _ := h.store.CodingSpendMicros(ctx, coding.SpendQuery{WorkspaceKey: workspace, UserID: "user1"}); spent != 750_000_000 {
		t.Fatalf("runtime cost at $3600/h: %d", spent)
	}
}
