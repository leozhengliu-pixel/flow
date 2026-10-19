package coding

import (
	"errors"
	"fmt"
	"time"

	"flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
)

// The session state machine (design §4). Step is a pure function of the
// session, what the reconciler observed and the configuration; it returns the
// next session document and the idempotent actions to run. It never calls a
// provider: sandboxes are driven by flipping coding_sandboxes.desired, and
// their observed phase is an input.

// StepConfig is the configuration Step reads.
type StepConfig struct {
	Policies          map[domain.CodingSessionPhase]PhasePolicy
	Timeouts          config.CodingTimeouts
	Retries           config.CodingRetries
	ReconcileInterval time.Duration
	MaxSessions       int
	WorkspaceSessions int
}

// NewStepConfig derives the Step configuration from the coding config.
func NewStepConfig(c config.CodingConfig) StepConfig {
	interval := c.ReconcileInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return StepConfig{Policies: PhasePolicies(c), Timeouts: c.Timeouts, Retries: c.Retries, ReconcileInterval: interval, MaxSessions: c.MaxSessions, WorkspaceSessions: c.WorkspaceSessions}
}

// Observation is what the reconciler saw before calling Step.
type Observation struct {
	Now time.Time
	// ProcessStart is when this Flow process started: heartbeat timeouts are
	// measured from max(last heartbeat, process start) so an outage never
	// marks healthy runners lost.
	ProcessStart    time.Time
	ProviderEnabled bool
	// ActiveInWorkspace / ActiveTotal count other sessions holding capacity.
	ActiveInWorkspace int
	ActiveTotal       int
	// SpendErr is the spend check result (queued only).
	SpendErr error
	// PendingTurns counts inbox turns not delivered to a runner yet.
	PendingTurns int
	Sandboxes    []sandbox.Record
}

// ActionKind is an idempotent side effect requested by Step.
type ActionKind string

const (
	// ActProvision resolves the environment, model, access and snapshot and
	// creates (or binds) the session's sandbox; its outcome goes to AfterProvision.
	ActProvision ActionKind = "provision"
	// ActPublish verifies the push and opens or updates the draft PR/MR; its outcome goes to AfterPublish.
	ActPublish ActionKind = "publish"
	// ActStopSandbox sets desired=stopped on a sandbox row.
	ActStopSandbox ActionKind = "stopSandbox"
	// ActRevokeTokens revokes a sandbox's runtime tokens.
	ActRevokeTokens ActionKind = "revokeTokens"
	// ActEnqueueStop puts a stop item in the runner's inbox.
	ActEnqueueStop ActionKind = "enqueueStop"
)

// Action is one side effect.
type Action struct {
	Kind      ActionKind
	SandboxID string
}

// Decision is Step's result.
type Decision struct {
	Session domain.CodingSession
	Actions []Action
	// NextIn is when the session should be reconciled again.
	NextIn time.Duration
}

func (d *Decision) add(kind ActionKind, sandboxID string) {
	for _, existing := range d.Actions {
		if existing.Kind == kind && existing.SandboxID == sandboxID {
			return
		}
	}
	d.Actions = append(d.Actions, Action{Kind: kind, SandboxID: sandboxID})
}

// Has reports whether the decision includes an action kind.
func (d Decision) Has(kind ActionKind) bool {
	for _, action := range d.Actions {
		if action.Kind == kind {
			return true
		}
	}
	return false
}

// liveSandbox reports whether a sandbox may still be running.
func liveSandbox(record sandbox.Record) bool {
	if record.EndedAt != nil {
		return false
	}
	switch record.Phase {
	case sandbox.PhaseSucceeded, sandbox.PhaseFailed, sandbox.PhaseMissing:
		return false
	}
	return true
}

// goneSandbox reports whether a sandbox the session wants running has died.
func goneSandbox(record sandbox.Record) bool {
	return record.Desired == sandbox.DesiredRunning && (record.Phase == sandbox.PhaseFailed || record.Phase == sandbox.PhaseMissing || record.Phase == sandbox.PhaseSucceeded)
}

func currentSandbox(s domain.CodingSession, sandboxes []sandbox.Record) (sandbox.Record, bool) {
	for _, record := range sandboxes {
		if record.ID == s.SandboxID && s.SandboxID != "" {
			return record, true
		}
	}
	return sandbox.Record{}, false
}

// Enter moves a session to phase at now: the state follows the phase policy,
// the phase deadline is set from its timeout, the retry counter resets and
// running time is accounted. Every phase change goes through it, so
// phase_started_at and phase_deadline are always written together with the
// phase by the same version CAS.
func (c StepConfig) Enter(s *domain.CodingSession, phase domain.CodingSessionPhase, now time.Time) {
	if s.Phase == domain.CodingPhaseRunning && phase != domain.CodingPhaseRunning && s.TurnStartedAt != nil {
		s.RunningMillis += max(0, now.Sub(*s.TurnStartedAt).Milliseconds())
		s.TurnStartedAt = nil
	}
	if phase == domain.CodingPhaseRunning && s.Phase != domain.CodingPhaseRunning {
		at := now
		s.TurnStartedAt = &at
		if s.StartedAt == nil {
			s.StartedAt = &at
		}
	}
	if phase != s.Phase {
		s.Attempts = 0
	}
	s.Phase = phase
	s.PhaseStartedAt = now
	s.PhaseDeadline = nil
	policy := c.Policies[phase]
	if policy.Timeout > 0 {
		deadline := now.Add(policy.Timeout)
		s.PhaseDeadline = &deadline
	}
	if phase == domain.CodingPhaseDone {
		s.State = TerminalState(s.ErrorCode, s.StopRequested || s.ApprovalState == "rejected")
		at := now
		s.EndedAt = &at
		return
	}
	s.State = policy.State
	s.EndedAt = nil
}

// Fail ends the session with an error code and stops its sandboxes.
func (c StepConfig) Fail(d *Decision, sandboxes []sandbox.Record, code domain.CodingErrorCode, message string, now time.Time) {
	s := &d.Session
	s.ErrorCode, s.Error = code, message
	c.finish(d, sandboxes, now)
}

// finish moves to done, stopping every sandbox that may still run.
func (c StepConfig) finish(d *Decision, sandboxes []sandbox.Record, now time.Time) {
	for _, record := range sandboxes {
		if record.Desired == sandbox.DesiredRunning || liveSandbox(record) {
			d.add(ActStopSandbox, record.ID)
		}
		d.add(ActRevokeTokens, record.ID)
	}
	d.Session.Pushed = false
	d.Session.CurrentTurnID = ""
	c.Enter(&d.Session, domain.CodingPhaseDone, now)
}

func (c StepConfig) timedOut(s domain.CodingSession, now time.Time) bool {
	return s.PhaseDeadline != nil && !now.Before(*s.PhaseDeadline)
}

// SandboxErrorCode maps a provider failure reason to a session error code
// for a sandbox that failed to start.
func SandboxErrorCode(reason sandbox.Reason) domain.CodingErrorCode {
	switch reason {
	case sandbox.ReasonImagePull:
		return domain.CodingErrImagePullFailed
	case sandbox.ReasonUnschedulable:
		return domain.CodingErrUnschedulable
	case sandbox.ReasonQuota:
		return domain.CodingErrCapacityUnavailable
	case sandbox.ReasonInitFailed:
		return domain.CodingErrSnapshotFailed
	default:
		return domain.CodingErrSandboxStartFailed
	}
}

func lostMessage(record sandbox.Record) string {
	switch record.Reason {
	case sandbox.ReasonOOMKilled:
		return "The sandbox ran out of memory. Use a larger sandbox size for this environment."
	case sandbox.ReasonEvicted:
		return "The sandbox was evicted from its node."
	case sandbox.ReasonDeadlineExceeded:
		return "The sandbox reached its maximum lifetime."
	}
	if record.Message != "" {
		return record.Message
	}
	if record.Phase == sandbox.PhaseMissing {
		return "The sandbox no longer exists."
	}
	return fmt.Sprintf("The sandbox stopped unexpectedly (%s).", record.Phase)
}

// Step computes the next state of a non-terminal session.
func (c StepConfig) Step(s domain.CodingSession, obs Observation) Decision {
	now := obs.Now
	d := Decision{Session: s, NextIn: c.ReconcileInterval}
	ns := &d.Session
	if s.Phase == domain.CodingPhaseDone {
		d.NextIn = 0
		return d
	}
	current, hasCurrent := currentSandbox(s, obs.Sandboxes)
	anyLive := false
	for _, record := range obs.Sandboxes {
		if liveSandbox(record) {
			anyLive = true
		}
	}

	// A stop request wins over everything else.
	if s.StopRequested && s.Phase != domain.CodingPhaseStopping {
		if !anyLive {
			c.finish(&d, obs.Sandboxes, now)
			return d
		}
		c.Enter(ns, domain.CodingPhaseStopping, now)
		d.add(ActEnqueueStop, "")
		d.NextIn = c.Timeouts.StopGrace
		return d
	}

	// Whole-session cap (cumulative running time).
	if limit := c.Timeouts.MaxSession; limit > 0 {
		running := s.RunningMillis
		if s.Phase == domain.CodingPhaseRunning && s.TurnStartedAt != nil {
			running += now.Sub(*s.TurnStartedAt).Milliseconds()
		}
		if time.Duration(running)*time.Millisecond >= limit {
			c.Fail(&d, obs.Sandboxes, domain.CodingErrSessionTimeout, "The session reached its maximum running time.", now)
			return d
		}
	}

	switch s.Phase {
	case domain.CodingPhaseSetup, domain.CodingPhaseRunning, domain.CodingPhaseAwaitingUser, domain.CodingPhasePublishing:
		if hasCurrent && goneSandbox(current) {
			c.Fail(&d, obs.Sandboxes, domain.CodingErrSandboxLost, lostMessage(current), now)
			return d
		}
		// awaitingUser may have no sandbox (repository selection before provisioning).
		if !hasCurrent && s.Phase != domain.CodingPhasePublishing && !(s.Phase == domain.CodingPhaseAwaitingUser && s.SandboxID == "") {
			c.Fail(&d, obs.Sandboxes, domain.CodingErrSandboxLost, "The session's sandbox record is missing.", now)
			return d
		}
	}

	// Heartbeats are expected while setup or a turn runs.
	if (s.Phase == domain.CodingPhaseSetup || s.Phase == domain.CodingPhaseRunning) && c.Timeouts.Heartbeat > 0 {
		last := s.PhaseStartedAt
		if current.HeartbeatAt != nil && current.HeartbeatAt.After(last) {
			last = *current.HeartbeatAt
		}
		if obs.ProcessStart.After(last) {
			last = obs.ProcessStart
		}
		if now.Sub(last) >= c.Timeouts.Heartbeat {
			c.Fail(&d, obs.Sandboxes, domain.CodingErrRunnerLost, "The runner stopped sending heartbeats.", now)
			return d
		}
	}

	policy := c.Policies[s.Phase]
	switch s.Phase {
	case domain.CodingPhaseAwaitingApproval:
		if c.timedOut(s, now) {
			c.Fail(&d, obs.Sandboxes, policy.OnTimeout, "Nobody approved the session in time.", now)
			return d
		}
		d.NextIn = time.Minute

	case domain.CodingPhaseQueued:
		switch {
		case !obs.ProviderEnabled:
			c.Fail(&d, obs.Sandboxes, domain.CodingErrSandboxDisabled, "Coding sandboxes are disabled on this server.", now)
		case errors.Is(obs.SpendErr, ErrQuotaExceeded):
			c.Fail(&d, obs.Sandboxes, domain.CodingErrQuotaExceeded, obs.SpendErr.Error(), now)
		case (c.WorkspaceSessions <= 0 || obs.ActiveInWorkspace < c.WorkspaceSessions) && (c.MaxSessions <= 0 || obs.ActiveTotal < c.MaxSessions):
			c.Enter(ns, domain.CodingPhaseProvisioning, now)
			d.add(ActProvision, "")
		case c.timedOut(s, now):
			c.Fail(&d, obs.Sandboxes, policy.OnTimeout, "No sandbox capacity became available in time.", now)
		}

	case domain.CodingPhaseProvisioning:
		if c.timedOut(s, now) {
			c.Fail(&d, obs.Sandboxes, policy.OnTimeout, "Provisioning the sandbox timed out.", now)
			return d
		}
		d.add(ActProvision, "")

	case domain.CodingPhaseStarting:
		switch {
		case !hasCurrent:
			c.Enter(ns, domain.CodingPhaseProvisioning, now)
			d.add(ActProvision, "")
		case goneSandbox(current):
			c.startFailed(&d, obs.Sandboxes, current, SandboxErrorCode(current.Reason), lostMessage(current), now)
		case c.timedOut(s, now):
			code := policy.OnTimeout
			if current.Reason == sandbox.ReasonImagePull || current.Reason == sandbox.ReasonUnschedulable || current.Reason == sandbox.ReasonQuota {
				code = SandboxErrorCode(current.Reason)
			}
			c.startFailed(&d, obs.Sandboxes, current, code, "The sandbox did not start in time.", now)
		}

	case domain.CodingPhaseSetup:
		if c.timedOut(s, now) {
			c.Fail(&d, obs.Sandboxes, policy.OnTimeout, "Environment setup timed out.", now)
		}

	case domain.CodingPhaseRunning:
		if c.timedOut(s, now) {
			// The runner gets the stop grace to commit and push a WIP before
			// the sandbox is drained; the session then ends with turnTimeout.
			ns.ErrorCode, ns.Error = policy.OnTimeout, "The turn exceeded its time limit."
			c.Enter(ns, domain.CodingPhaseStopping, now)
			d.add(ActEnqueueStop, "")
			d.NextIn = c.Timeouts.StopGrace
		}

	case domain.CodingPhaseAwaitingUser:
		switch {
		case c.timedOut(s, now):
			c.Fail(&d, obs.Sandboxes, policy.OnTimeout, "Nobody answered the agent's question in time.", now)
		case obs.PendingTurns > 0 && (!hasCurrent || !liveSandbox(current)):
			c.requeue(&d, obs.Sandboxes, now)
		default:
			d.NextIn = time.Minute
		}

	case domain.CodingPhasePublishing:
		if c.timedOut(s, now) {
			c.Fail(&d, obs.Sandboxes, policy.OnTimeout, "Opening the pull request timed out.", now)
			return d
		}
		d.add(ActPublish, "")

	case domain.CodingPhaseIdle:
		alive := hasCurrent && liveSandbox(current) && current.Desired == sandbox.DesiredRunning
		switch {
		case obs.PendingTurns > 0 && !alive:
			c.requeue(&d, obs.Sandboxes, now)
		case obs.PendingTurns > 0:
			// The warm runner long-polls the inbox and picks the turn up.
		case c.timedOut(s, now):
			// Not an error: the warm sandbox is reclaimed and the state stays complete.
			c.finish(&d, obs.Sandboxes, now)
		case hasCurrent && !alive:
			ns.SandboxID = ""
			d.NextIn = time.Minute
		default:
			d.NextIn = time.Minute
		}

	case domain.CodingPhaseStopping:
		switch {
		case !anyLive || c.timedOut(s, now):
			c.finish(&d, obs.Sandboxes, now)
		case now.Sub(s.PhaseStartedAt) >= c.Timeouts.StopGrace:
			// The runner had its grace period for a WIP commit; drain the sandbox.
			for _, record := range obs.Sandboxes {
				if record.Desired == sandbox.DesiredRunning {
					d.add(ActStopSandbox, record.ID)
				}
			}
		}
	}
	if ns.Phase != domain.CodingPhaseDone && ns.PhaseDeadline != nil {
		if until := ns.PhaseDeadline.Sub(now); until > 0 && until < d.NextIn {
			d.NextIn = until
		}
	}
	if d.Has(ActProvision) || d.Has(ActPublish) {
		d.NextIn = c.ReconcileInterval
	}
	return d
}

// startFailed retries a failed sandbox start with a new sandbox id while
// start retries remain; a failed snapshot restore falls back to inline setup.
func (c StepConfig) startFailed(d *Decision, sandboxes []sandbox.Record, current sandbox.Record, code domain.CodingErrorCode, message string, now time.Time) {
	s := &d.Session
	if s.SandboxAttempts >= c.Retries.Start {
		c.Fail(d, sandboxes, code, message, now)
		return
	}
	d.add(ActStopSandbox, current.ID)
	d.add(ActRevokeTokens, current.ID)
	s.SandboxAttempts++
	s.SandboxID = ""
	if code == domain.CodingErrSnapshotFailed {
		s.SnapshotID = ""
	}
	c.Enter(s, domain.CodingPhaseProvisioning, now)
	d.add(ActProvision, "")
}

// requeue sends a session whose sandbox is gone back to the queue for its next turn.
func (c StepConfig) requeue(d *Decision, sandboxes []sandbox.Record, now time.Time) {
	for _, record := range sandboxes {
		if record.Desired == sandbox.DesiredRunning {
			d.add(ActStopSandbox, record.ID)
		}
	}
	s := &d.Session
	s.SandboxID, s.SandboxAttempts, s.CurrentTurnID = "", 0, ""
	if s.HeadSHA != "" {
		s.ExistingBranch = true
	}
	c.Enter(s, domain.CodingPhaseQueued, now)
}

// ProvisionOutcome is the result of an ActProvision.
type ProvisionOutcome struct {
	// SandboxID is the created or bound sandbox (success).
	SandboxID string
	// Code is set on failure; Transient failures are retried with backoff.
	Code      domain.CodingErrorCode
	Message   string
	Transient bool
	// Selection lists environments to choose from when the repository is ambiguous.
	Selection []domain.CodingEnvironment
	// Apply copies resolved fields (environment, repository, model…) onto the session.
	Apply func(*domain.CodingSession)
}

// AfterProvision applies a provisioning outcome.
func (c StepConfig) AfterProvision(d *Decision, sandboxes []sandbox.Record, outcome ProvisionOutcome, now time.Time) {
	s := &d.Session
	if outcome.Apply != nil {
		outcome.Apply(s)
	}
	switch {
	case outcome.SandboxID != "":
		s.SandboxID = outcome.SandboxID
		c.Enter(s, domain.CodingPhaseStarting, now)
		d.NextIn = c.ReconcileInterval
	case len(outcome.Selection) > 0:
		c.Enter(s, domain.CodingPhaseAwaitingUser, now)
		d.NextIn = time.Minute
	case outcome.Transient && s.Attempts < c.Retries.Provision:
		s.Attempts++
		d.NextIn = Backoff(s.Attempts - 1)
	default:
		code := outcome.Code
		if code == "" {
			code = c.Policies[domain.CodingPhaseProvisioning].OnTimeout
		}
		c.Fail(d, sandboxes, code, outcome.Message, now)
	}
}

// PublishOutcome is the result of an ActPublish.
type PublishOutcome struct {
	PullRequest *domain.SessionPullRequest
	Code        domain.CodingErrorCode
	Message     string
	Transient   bool
}

// AfterPublish applies a publishing outcome.
func (c StepConfig) AfterPublish(d *Decision, sandboxes []sandbox.Record, outcome PublishOutcome, now time.Time) {
	s := &d.Session
	switch {
	case outcome.Code == "":
		if pr := outcome.PullRequest; pr != nil {
			mergePullRequest(s, *pr)
		}
		s.Pushed = false
		c.Enter(s, domain.CodingPhaseIdle, now)
		d.NextIn = time.Minute
	case outcome.Transient && s.Attempts < c.Retries.Publish:
		s.Attempts++
		d.NextIn = Backoff(s.Attempts - 1)
	default:
		code := outcome.Code
		if outcome.Transient {
			code = c.Policies[domain.CodingPhasePublishing].OnTimeout
		}
		c.Fail(d, sandboxes, code, outcome.Message, now)
	}
}

// mergePullRequest records a PR/MR on the session and its external URLs.
func mergePullRequest(s *domain.CodingSession, pr domain.SessionPullRequest) {
	replaced := false
	for index, existing := range s.PullRequests {
		if existing.CodeHost == pr.CodeHost && existing.HostName == pr.HostName && existing.Number == pr.Number {
			s.PullRequests[index], replaced = pr, true
		}
	}
	if !replaced {
		s.PullRequests = append(s.PullRequests, pr)
	}
	latest := pr
	s.LatestPullRequest = &latest
	label := pullRequestLabel(pr)
	for index, link := range s.ExternalURLs {
		if link.URL == pr.URL {
			s.ExternalURLs[index].Label = label
			return
		}
	}
	s.ExternalURLs = append(s.ExternalURLs, domain.AgentExternalURL{Label: label, URL: pr.URL})
}

func pullRequestLabel(pr domain.SessionPullRequest) string {
	if pr.CodeHost == domain.CodeHostGitLab {
		return fmt.Sprintf("Merge request !%d", pr.Number)
	}
	return fmt.Sprintf("Pull request #%d", pr.Number)
}
