package coding

import (
	"testing"
	"time"

	"flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
)

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func testConfig() config.CodingConfig {
	return config.CodingConfig{
		Enabled: true, Provider: config.CodingProviderFake, MaxSessions: 2, WorkspaceSessions: 1, ReconcileInterval: 5 * time.Second,
		Timeouts:   config.CodingTimeouts{Queue: 30 * time.Minute, Approval: 7 * 24 * time.Hour, Provision: 5 * time.Minute, Start: 10 * time.Minute, Setup: 20 * time.Minute, Turn: time.Hour, Heartbeat: 2 * time.Minute, Publish: 5 * time.Minute, Idle: 15 * time.Minute, StopGrace: time.Minute, MaxSession: 4 * time.Hour},
		Retries:    config.CodingRetries{Provision: 3, Start: 2, Publish: 5},
		Sizes:      map[string]config.SandboxSize{"small": {CPU: "1", Memory: "8Gi"}, "medium": {CPU: "2", Memory: "16Gi"}},
		LLM:        config.CodingLLMConfig{CodexModels: []string{"gpt-5-codex", "gpt-5"}, ClaudeCodeModels: []string{"claude-sonnet-4-5", "claude-opus-4-1"}},
		RuntimeURL: "http://flow.test", BranchPrefix: "flow/", RuntimePricesUSD: map[string]float64{"medium": 0.20},
	}
}

// sessionIn builds a session that entered phase at `entered`.
func sessionIn(c StepConfig, phase domain.CodingSessionPhase, entered time.Time) domain.CodingSession {
	s := domain.CodingSession{ID: "s1", WorkspaceKey: "w", SandboxID: "sb1", CurrentTurnID: "turn1"}
	c.Enter(&s, phase, entered)
	return s
}

func box(phase sandbox.Phase, reason sandbox.Reason, heartbeat *time.Time) sandbox.Record {
	return sandbox.Record{ID: "sb1", SessionID: "s1", Kind: sandbox.KindSession, Desired: sandbox.DesiredRunning, Phase: phase, Reason: reason, HeartbeatAt: heartbeat}
}

func at(d time.Duration) *time.Time { value := t0.Add(d); return &value }

// TestStepTable covers every phase's timeout, retry and failure path. Each
// case checks the next phase, the error code, the attempts and that the
// transition is allowed by CanTransition.
func TestStepTable(t *testing.T) {
	c := NewStepConfig(testConfig())
	running := []sandbox.Record{box(sandbox.PhaseRunning, "", nil)}
	type tc struct {
		name     string
		session  func() domain.CodingSession
		obs      Observation
		phase    domain.CodingSessionPhase
		code     domain.CodingErrorCode
		state    domain.CodingSessionState
		actions  []ActionKind
		attempts int
	}
	cases := []tc{
		{name: "approval waits", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseAwaitingApproval, t0) }, obs: Observation{Now: t0.Add(time.Hour)}, phase: domain.CodingPhaseAwaitingApproval, state: domain.CodingSessionAwaitingInput},
		{name: "approval timeout", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseAwaitingApproval, t0) }, obs: Observation{Now: t0.Add(7 * 24 * time.Hour)}, phase: domain.CodingPhaseDone, code: domain.CodingErrApprovalExpired, state: domain.CodingSessionError},
		{name: "queued provisions with capacity", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseQueued, t0) }, obs: Observation{Now: t0, ProviderEnabled: true}, phase: domain.CodingPhaseProvisioning, state: domain.CodingSessionPending, actions: []ActionKind{ActProvision}},
		{name: "queued waits on workspace capacity", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseQueued, t0) }, obs: Observation{Now: t0.Add(time.Minute), ProviderEnabled: true, ActiveInWorkspace: 1, ActiveTotal: 1}, phase: domain.CodingPhaseQueued, state: domain.CodingSessionPending},
		{name: "queued waits on global capacity", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseQueued, t0) }, obs: Observation{Now: t0.Add(time.Minute), ProviderEnabled: true, ActiveTotal: 2}, phase: domain.CodingPhaseQueued},
		{name: "queue timeout", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseQueued, t0) }, obs: Observation{Now: t0.Add(30 * time.Minute), ProviderEnabled: true, ActiveTotal: 2}, phase: domain.CodingPhaseDone, code: domain.CodingErrCapacityUnavailable},
		{name: "queued over spend limit", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseQueued, t0) }, obs: Observation{Now: t0, ProviderEnabled: true, SpendErr: &QuotaError{Scope: SpendScopeUser}}, phase: domain.CodingPhaseDone, code: domain.CodingErrQuotaExceeded},
		{name: "queued without provider", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseQueued, t0) }, obs: Observation{Now: t0}, phase: domain.CodingPhaseDone, code: domain.CodingErrSandboxDisabled},
		{name: "provisioning retries", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseProvisioning, t0) }, obs: Observation{Now: t0.Add(time.Minute)}, phase: domain.CodingPhaseProvisioning, actions: []ActionKind{ActProvision}},
		{name: "provision timeout", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseProvisioning, t0) }, obs: Observation{Now: t0.Add(5 * time.Minute)}, phase: domain.CodingPhaseDone, code: domain.CodingErrSandboxStartFailed},
		{name: "starting waits", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseStarting, t0) }, obs: Observation{Now: t0.Add(time.Minute), Sandboxes: []sandbox.Record{box(sandbox.PhaseStarting, "", nil)}}, phase: domain.CodingPhaseStarting},
		{name: "start failure retries with a new sandbox", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseStarting, t0) }, obs: Observation{Now: t0.Add(time.Minute), Sandboxes: []sandbox.Record{box(sandbox.PhaseFailed, sandbox.ReasonImagePull, nil)}}, phase: domain.CodingPhaseProvisioning, actions: []ActionKind{ActStopSandbox, ActRevokeTokens, ActProvision}},
		{name: "start failure exhausted: image pull", session: func() domain.CodingSession {
			s := sessionIn(c, domain.CodingPhaseStarting, t0)
			s.SandboxAttempts = 2
			return s
		}, obs: Observation{Now: t0.Add(time.Minute), Sandboxes: []sandbox.Record{box(sandbox.PhaseFailed, sandbox.ReasonImagePull, nil)}}, phase: domain.CodingPhaseDone, code: domain.CodingErrImagePullFailed},
		{name: "start timeout exhausted: unschedulable", session: func() domain.CodingSession {
			s := sessionIn(c, domain.CodingPhaseStarting, t0)
			s.SandboxAttempts = 2
			return s
		}, obs: Observation{Now: t0.Add(10 * time.Minute), Sandboxes: []sandbox.Record{box(sandbox.PhasePending, sandbox.ReasonUnschedulable, nil)}}, phase: domain.CodingPhaseDone, code: domain.CodingErrUnschedulable},
		{name: "start timeout exhausted: generic", session: func() domain.CodingSession {
			s := sessionIn(c, domain.CodingPhaseStarting, t0)
			s.SandboxAttempts = 2
			return s
		}, obs: Observation{Now: t0.Add(10 * time.Minute), Sandboxes: []sandbox.Record{box(sandbox.PhaseRunning, "", nil)}}, phase: domain.CodingPhaseDone, code: domain.CodingErrSandboxStartFailed},
		{name: "start timeout retries", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseStarting, t0) }, obs: Observation{Now: t0.Add(10 * time.Minute), Sandboxes: []sandbox.Record{box(sandbox.PhasePending, "", nil)}}, phase: domain.CodingPhaseProvisioning, actions: []ActionKind{ActStopSandbox, ActRevokeTokens, ActProvision}},
		{name: "setup timeout", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseSetup, t0) }, obs: Observation{Now: t0.Add(20 * time.Minute), ProcessStart: t0.Add(19 * time.Minute), Sandboxes: running}, phase: domain.CodingPhaseDone, code: domain.CodingErrEnvironmentSetup},
		{name: "running healthy", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseRunning, t0) }, obs: Observation{Now: t0.Add(10 * time.Minute), Sandboxes: []sandbox.Record{box(sandbox.PhaseRunning, "", at(9*time.Minute))}}, phase: domain.CodingPhaseRunning, state: domain.CodingSessionActive},
		{name: "turn timeout", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseRunning, t0) }, obs: Observation{Now: t0.Add(time.Hour), Sandboxes: []sandbox.Record{box(sandbox.PhaseRunning, "", at(59*time.Minute))}}, phase: domain.CodingPhaseStopping, code: domain.CodingErrTurnTimeout, state: domain.CodingSessionStopping, actions: []ActionKind{ActEnqueueStop}},
		{name: "turn timeout ends in error after the WIP grace", session: func() domain.CodingSession {
			s := sessionIn(c, domain.CodingPhaseStopping, t0)
			s.ErrorCode = domain.CodingErrTurnTimeout
			return s
		}, obs: Observation{Now: t0.Add(30 * time.Second), Sandboxes: []sandbox.Record{box(sandbox.PhaseSucceeded, "", nil)}}, phase: domain.CodingPhaseDone, code: domain.CodingErrTurnTimeout, state: domain.CodingSessionError},
		{name: "runner lost", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseRunning, t0) }, obs: Observation{Now: t0.Add(5 * time.Minute), Sandboxes: []sandbox.Record{box(sandbox.PhaseRunning, "", at(2*time.Minute))}}, phase: domain.CodingPhaseDone, code: domain.CodingErrRunnerLost},
		{name: "heartbeat timeout counts from process start", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseRunning, t0) }, obs: Observation{Now: t0.Add(5 * time.Minute), ProcessStart: t0.Add(4 * time.Minute), Sandboxes: []sandbox.Record{box(sandbox.PhaseRunning, "", at(time.Minute))}}, phase: domain.CodingPhaseRunning},
		{name: "sandbox lost while running", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseRunning, t0) }, obs: Observation{Now: t0.Add(time.Minute), Sandboxes: []sandbox.Record{box(sandbox.PhaseFailed, sandbox.ReasonOOMKilled, nil)}}, phase: domain.CodingPhaseDone, code: domain.CodingErrSandboxLost},
		{name: "session timeout", session: func() domain.CodingSession {
			s := sessionIn(c, domain.CodingPhaseRunning, t0)
			s.RunningMillis = (4*time.Hour - time.Minute).Milliseconds()
			return s
		}, obs: Observation{Now: t0.Add(time.Minute), Sandboxes: []sandbox.Record{box(sandbox.PhaseRunning, "", at(time.Minute))}}, phase: domain.CodingPhaseDone, code: domain.CodingErrSessionTimeout},
		{name: "awaiting user timeout", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseAwaitingUser, t0) }, obs: Observation{Now: t0.Add(7 * 24 * time.Hour), Sandboxes: running}, phase: domain.CodingPhaseDone, code: domain.CodingErrApprovalExpired},
		{name: "awaiting user answer without sandbox requeues", session: func() domain.CodingSession {
			s := sessionIn(c, domain.CodingPhaseAwaitingUser, t0)
			s.SandboxID = ""
			return s
		}, obs: Observation{Now: t0.Add(time.Minute), PendingTurns: 1}, phase: domain.CodingPhaseQueued},
		{name: "publishing publishes", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhasePublishing, t0) }, obs: Observation{Now: t0.Add(time.Minute), Sandboxes: running}, phase: domain.CodingPhasePublishing, actions: []ActionKind{ActPublish}},
		{name: "publish timeout", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhasePublishing, t0) }, obs: Observation{Now: t0.Add(5 * time.Minute), Sandboxes: running}, phase: domain.CodingPhaseDone, code: domain.CodingErrPullRequestFailed},
		{name: "idle keeps warm sandbox", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseIdle, t0) }, obs: Observation{Now: t0.Add(time.Minute), Sandboxes: running}, phase: domain.CodingPhaseIdle, state: domain.CodingSessionComplete},
		{name: "idle TTL reclaims the sandbox without error", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseIdle, t0) }, obs: Observation{Now: t0.Add(15 * time.Minute), Sandboxes: running}, phase: domain.CodingPhaseDone, state: domain.CodingSessionComplete, actions: []ActionKind{ActStopSandbox, ActRevokeTokens}},
		{name: "idle follow-up with warm sandbox waits for the runner", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseIdle, t0) }, obs: Observation{Now: t0.Add(time.Minute), PendingTurns: 1, Sandboxes: running}, phase: domain.CodingPhaseIdle},
		{name: "idle follow-up after sandbox loss requeues", session: func() domain.CodingSession { return sessionIn(c, domain.CodingPhaseIdle, t0) }, obs: Observation{Now: t0.Add(time.Minute), PendingTurns: 1, Sandboxes: []sandbox.Record{box(sandbox.PhaseMissing, "", nil)}}, phase: domain.CodingPhaseQueued, actions: []ActionKind{ActStopSandbox}},
		{name: "stop with live sandbox", session: func() domain.CodingSession {
			s := sessionIn(c, domain.CodingPhaseRunning, t0)
			s.StopRequested = true
			return s
		}, obs: Observation{Now: t0.Add(time.Minute), Sandboxes: running}, phase: domain.CodingPhaseStopping, state: domain.CodingSessionStopping, actions: []ActionKind{ActEnqueueStop}},
		{name: "stop without sandbox", session: func() domain.CodingSession {
			s := sessionIn(c, domain.CodingPhaseQueued, t0)
			s.StopRequested = true
			return s
		}, obs: Observation{Now: t0}, phase: domain.CodingPhaseDone, state: domain.CodingSessionStale},
		{name: "stopping drains after grace", session: func() domain.CodingSession {
			s := sessionIn(c, domain.CodingPhaseStopping, t0)
			s.StopRequested = true
			return s
		}, obs: Observation{Now: t0.Add(time.Minute), Sandboxes: running}, phase: domain.CodingPhaseStopping, actions: []ActionKind{ActStopSandbox}},
		{name: "stopping finishes when drained", session: func() domain.CodingSession {
			s := sessionIn(c, domain.CodingPhaseStopping, t0)
			s.StopRequested = true
			return s
		}, obs: Observation{Now: t0.Add(30 * time.Second), Sandboxes: []sandbox.Record{box(sandbox.PhaseSucceeded, "", nil)}}, phase: domain.CodingPhaseDone, state: domain.CodingSessionStale},
		{name: "stopping force-finishes at 2x grace", session: func() domain.CodingSession {
			s := sessionIn(c, domain.CodingPhaseStopping, t0)
			s.StopRequested = true
			return s
		}, obs: Observation{Now: t0.Add(2 * time.Minute), Sandboxes: running}, phase: domain.CodingPhaseDone, state: domain.CodingSessionStale, actions: []ActionKind{ActStopSandbox, ActRevokeTokens}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			before := test.session()
			d := c.Step(before, test.obs)
			if d.Session.Phase != test.phase || d.Session.ErrorCode != test.code {
				t.Fatalf("phase %s code %q, want %s %q (%s)", d.Session.Phase, d.Session.ErrorCode, test.phase, test.code, d.Session.Error)
			}
			if test.state != "" && d.Session.State != test.state {
				t.Fatalf("state %s, want %s", d.Session.State, test.state)
			}
			if test.code != "" && d.Session.Phase == domain.CodingPhaseDone && d.Session.State != domain.CodingSessionError {
				t.Fatalf("an error ends in state error, got %s", d.Session.State)
			}
			if before.Phase != d.Session.Phase && !CanTransition(before.Phase, d.Session.Phase) {
				t.Fatalf("Step produced a forbidden transition %s → %s", before.Phase, d.Session.Phase)
			}
			for _, kind := range test.actions {
				if !d.Has(kind) {
					t.Fatalf("missing action %s in %+v", kind, d.Actions)
				}
			}
			if d.Session.Phase != domain.CodingPhaseDone && d.NextIn <= 0 {
				t.Fatalf("a live session needs a next reconcile time")
			}
			if d.Session.Phase != before.Phase && d.Session.Phase != domain.CodingPhaseDone {
				if policy := c.Policies[d.Session.Phase]; policy.Timeout > 0 && (d.Session.PhaseDeadline == nil || !d.Session.PhaseDeadline.Equal(test.obs.Now.Add(policy.Timeout))) {
					t.Fatalf("phase deadline %v not set with the phase", d.Session.PhaseDeadline)
				}
			}
		})
	}
}

func TestAfterProvisionRetriesTransientFailuresWithBackoff(t *testing.T) {
	c := NewStepConfig(testConfig())
	d := Decision{Session: sessionIn(c, domain.CodingPhaseProvisioning, t0)}
	for attempt := 1; attempt <= 3; attempt++ {
		c.AfterProvision(&d, nil, ProvisionOutcome{Code: domain.CodingErrGitHubTransientError, Transient: true}, t0)
		if d.Session.Phase != domain.CodingPhaseProvisioning || d.Session.Attempts != attempt || d.NextIn != Backoff(attempt-1) {
			t.Fatalf("attempt %d: %s attempts=%d next=%s", attempt, d.Session.Phase, d.Session.Attempts, d.NextIn)
		}
	}
	c.AfterProvision(&d, nil, ProvisionOutcome{Code: domain.CodingErrGitHubTransientError, Transient: true}, t0)
	if d.Session.Phase != domain.CodingPhaseDone || d.Session.ErrorCode != domain.CodingErrGitHubTransientError {
		t.Fatalf("exhausted: %s %s", d.Session.Phase, d.Session.ErrorCode)
	}
	// Non-transient errors fail at once with their precise code.
	d = Decision{Session: sessionIn(c, domain.CodingPhaseProvisioning, t0)}
	c.AfterProvision(&d, nil, ProvisionOutcome{Code: domain.CodingErrRepoNotInPolicy}, t0)
	if d.Session.ErrorCode != domain.CodingErrRepoNotInPolicy || d.Session.Attempts != 0 {
		t.Fatalf("non-transient: %+v", d.Session)
	}
	// Success moves to starting; a selection asks the user.
	d = Decision{Session: sessionIn(c, domain.CodingPhaseProvisioning, t0)}
	c.AfterProvision(&d, nil, ProvisionOutcome{SandboxID: "sb9", Apply: func(s *domain.CodingSession) { s.Model = "m" }}, t0)
	if d.Session.Phase != domain.CodingPhaseStarting || d.Session.SandboxID != "sb9" || d.Session.Model != "m" {
		t.Fatalf("success: %+v", d.Session)
	}
	d = Decision{Session: sessionIn(c, domain.CodingPhaseProvisioning, t0)}
	c.AfterProvision(&d, nil, ProvisionOutcome{Selection: []domain.CodingEnvironment{{ID: "a"}, {ID: "b"}}}, t0)
	if d.Session.Phase != domain.CodingPhaseAwaitingUser || !CanTransition(domain.CodingPhaseProvisioning, domain.CodingPhaseAwaitingUser) {
		t.Fatalf("selection: %s", d.Session.Phase)
	}
}

func TestAfterPublishRetriesThenFails(t *testing.T) {
	c := NewStepConfig(testConfig())
	d := Decision{Session: sessionIn(c, domain.CodingPhasePublishing, t0)}
	d.Session.Pushed = true
	for attempt := 1; attempt <= 5; attempt++ {
		c.AfterPublish(&d, nil, PublishOutcome{Code: domain.CodingErrGitHubTransientError, Transient: true}, t0)
		if d.Session.Phase != domain.CodingPhasePublishing || d.Session.Attempts != attempt {
			t.Fatalf("attempt %d: %s", attempt, d.Session.Phase)
		}
	}
	c.AfterPublish(&d, nil, PublishOutcome{Code: domain.CodingErrGitHubTransientError, Transient: true}, t0)
	if d.Session.ErrorCode != domain.CodingErrPullRequestFailed {
		t.Fatalf("exhausted publish must be pullRequestFailed, got %s", d.Session.ErrorCode)
	}
	d = Decision{Session: sessionIn(c, domain.CodingPhasePublishing, t0)}
	c.AfterPublish(&d, nil, PublishOutcome{Code: domain.CodingErrPushFailed, Message: "head mismatch"}, t0)
	if d.Session.ErrorCode != domain.CodingErrPushFailed {
		t.Fatalf("push failure: %s", d.Session.ErrorCode)
	}
	d = Decision{Session: sessionIn(c, domain.CodingPhasePublishing, t0)}
	d.Session.Pushed = true
	c.AfterPublish(&d, nil, PublishOutcome{PullRequest: &domain.SessionPullRequest{CodeHost: "github", Number: 7, URL: "https://github.com/a/b/pull/7"}}, t0)
	if d.Session.Phase != domain.CodingPhaseIdle || d.Session.Pushed || len(d.Session.ExternalURLs) != 1 || d.Session.ExternalURLs[0].Label != "Pull request #7" {
		t.Fatalf("success: %+v", d.Session)
	}
}

func TestSnapshotRestoreFailureFallsBackToInlineSetup(t *testing.T) {
	c := NewStepConfig(testConfig())
	s := sessionIn(c, domain.CodingPhaseStarting, t0)
	s.SnapshotID = "snap1"
	d := c.Step(s, Observation{Now: t0.Add(time.Minute), Sandboxes: []sandbox.Record{box(sandbox.PhaseFailed, sandbox.ReasonInitFailed, nil)}})
	if d.Session.Phase != domain.CodingPhaseProvisioning || d.Session.SnapshotID != "" || d.Session.SandboxAttempts != 1 || d.Session.SandboxID != "" {
		t.Fatalf("restore failure must retry without the snapshot: %+v", d.Session)
	}
}

func TestEnterAccountsRunningTime(t *testing.T) {
	c := NewStepConfig(testConfig())
	s := domain.CodingSession{}
	c.Enter(&s, domain.CodingPhaseRunning, t0)
	c.Enter(&s, domain.CodingPhaseIdle, t0.Add(10*time.Minute))
	c.Enter(&s, domain.CodingPhaseRunning, t0.Add(20*time.Minute))
	c.Enter(&s, domain.CodingPhaseDone, t0.Add(25*time.Minute))
	if s.RunningMillis != (15*time.Minute).Milliseconds() || s.State != domain.CodingSessionComplete || s.EndedAt == nil {
		t.Fatalf("running %d state %s", s.RunningMillis, s.State)
	}
}

func TestNewIDIsSortableLowercaseULID(t *testing.T) {
	a, b := newIDAt(t0), newIDAt(t0.Add(time.Millisecond))
	if len(a) != 26 || a >= b {
		t.Fatalf("ids %s %s", a, b)
	}
	for _, r := range a {
		if (r < '0' || r > '9') && (r < 'a' || r > 'z') {
			t.Fatalf("id %s is not lowercase alphanumeric", a)
		}
	}
}

func TestModelRouting(t *testing.T) {
	llm := testConfig().LLM
	if model, ok := ResolveModel(llm, "codex", "auto"); !ok || model != "gpt-5-codex" {
		t.Fatalf("auto: %s", model)
	}
	if model, ok := ResolveModel(llm, "claude", ""); !ok || model != "claude-sonnet-4-5" {
		t.Fatalf("legacy claude harness: %s", model)
	}
	if _, ok := ResolveModel(llm, "codex", "claude-opus-4-1"); ok {
		t.Fatal("a model outside the harness catalog must be refused")
	}
	if _, ok := ResolveModel(llm, "openSource", "auto"); ok {
		t.Fatal("auto with an empty catalog has nothing to pick")
	}
	if next, ok := NextAutoModel(llm, "codex", "gpt-5-codex"); !ok || next != "gpt-5" {
		t.Fatalf("fallback: %s", next)
	}
	if _, ok := NextAutoModel(llm, "codex", "gpt-5"); ok {
		t.Fatal("no fallback after the last model")
	}
	if ClaudeThinkingTokens(EffortHigh) != 32000 || ClaudeThinkingTokens("") != 0 {
		t.Fatal("thinking tokens")
	}
	if NormalizeHarness("auto") != domain.CodingHarnessCodex || NormalizeHarness("claude") != domain.CodingHarnessClaudeCode {
		t.Fatal("legacy harness values")
	}
	options := ModelOptions(llm)
	if len(options) != 6 || !options[0].Auto || options[0].Harness != domain.CodingHarnessCodex {
		t.Fatalf("options: %+v", options)
	}
}

func TestAutoRoutesByTask(t *testing.T) {
	llm := testConfig().LLM
	llm.CodexModels = []string{"gpt-5-codex", "gpt-5", "gpt-5-mini"}
	small, ok := RouteAutoModel(llm, "codex", "Add a farewell() function next to greet(). Keep it minimal.")
	if !ok || small.Model != "gpt-5-mini" || small.Reason != routeReasonSmall || small.Escalation != "gpt-5-codex" {
		t.Fatalf("small task: %+v", small)
	}
	large, _ := RouteAutoModel(llm, "codex", "Refactor the billing module and migrate every caller to the new API.")
	if large.Model != "gpt-5" || large.Reason != routeReasonLarge || large.Escalation != "" {
		t.Fatalf("large task: %+v", large)
	}
	standard, _ := RouteAutoModel(llm, "codex", "The settings page shows the wrong timezone after saving.")
	if standard.Model != "gpt-5-codex" || standard.Reason != routeReasonStandard || standard.Escalation != "gpt-5" {
		t.Fatalf("standard task: %+v", standard)
	}
	// Without a model of the tier, the nearest tier is used (a more capable one first).
	llm.CodexModels = []string{"gpt-5-codex", "gpt-5"}
	if fallback, _ := RouteAutoModel(llm, "codex", "Fix a typo in the README."); fallback.Model != "gpt-5-codex" || fallback.Reason != routeReasonSmall {
		t.Fatalf("fallback tier: %+v", fallback)
	}
	// Configured tiers override the built-ins; unknown models are standard.
	llm.CodexModels = []string{"local-a", "local-b"}
	llm.ModelInfo = map[string]config.ModelInfo{"local-b": {Tier: ModelTierSmall}}
	if routed, _ := RouteAutoModel(llm, "codex", "Rename the helper."); routed.Model != "local-b" || routed.Escalation != "local-a" {
		t.Fatalf("configured tier: %+v", routed)
	}
	if _, ok := RouteAutoModel(llm, "openSource", "anything"); ok {
		t.Fatal("an empty catalog routes nothing")
	}
}

func TestBranchName(t *testing.T) {
	if got := BranchName("flow/", "ENG-12", "Fix the login page!", "01jabcdefghjkmnpqrstvwxyz0"); got != "flow/eng-12-fix-the-login-page-vwxyz0" {
		t.Fatalf("branch %s", got)
	}
}
