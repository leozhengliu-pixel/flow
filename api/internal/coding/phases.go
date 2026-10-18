// Package coding is the coding-session orchestration layer (track B): the
// session state machine, the session reconciler, triggers and metering. It
// depends on interfaces only (Store, sandbox.Provider via sandbox records,
// codehost.Host) so it is unit-testable without HTTP or a database. See
// docs/coding-sessions-design.md §4-§5.
package coding

import (
	"time"

	"flow/api/internal/config"
	"flow/api/internal/domain"
)

// PhasePolicy is the timeout/retry contract of one phase.
type PhasePolicy struct {
	Phase domain.CodingSessionPhase
	// State is the user-facing state while in this phase.
	State domain.CodingSessionState
	// Timeout bounds the time spent in the phase (0 = unbounded, e.g. idle
	// is bounded by Idle TTL instead, done is terminal).
	Timeout time.Duration
	// Retries is how many times an idempotent phase is retried after a
	// transient failure (exponential backoff 2s·2^n, max 1 min) before
	// OnExhausted.
	Retries int
	// OnTimeout is the error code when Timeout passes.
	OnTimeout domain.CodingErrorCode
}

// PhasePolicies returns the per-phase policy for a configuration.
func PhasePolicies(c config.CodingConfig) map[domain.CodingSessionPhase]PhasePolicy {
	t, r := c.Timeouts, c.Retries
	list := []PhasePolicy{
		{domain.CodingPhaseAwaitingApproval, domain.CodingSessionAwaitingInput, t.Approval, 0, domain.CodingErrApprovalExpired},
		{domain.CodingPhaseQueued, domain.CodingSessionPending, t.Queue, 0, domain.CodingErrCapacityUnavailable},
		{domain.CodingPhaseProvisioning, domain.CodingSessionPending, t.Provision, r.Provision, domain.CodingErrSandboxStartFailed},
		{domain.CodingPhaseStarting, domain.CodingSessionPending, t.Start, r.Start, domain.CodingErrSandboxStartFailed},
		{domain.CodingPhaseSetup, domain.CodingSessionActive, t.Setup, 0, domain.CodingErrEnvironmentSetup},
		{domain.CodingPhaseRunning, domain.CodingSessionActive, t.Turn, 0, domain.CodingErrTurnTimeout},
		{domain.CodingPhaseAwaitingUser, domain.CodingSessionAwaitingInput, t.Approval, 0, domain.CodingErrApprovalExpired},
		{domain.CodingPhasePublishing, domain.CodingSessionActive, t.Publish, r.Publish, domain.CodingErrPullRequestFailed},
		{domain.CodingPhaseIdle, domain.CodingSessionComplete, t.Idle, 0, ""},
		{domain.CodingPhaseStopping, domain.CodingSessionStopping, t.StopGrace * 2, 0, ""},
		{domain.CodingPhaseDone, "", 0, 0, ""},
	}
	result := make(map[domain.CodingSessionPhase]PhasePolicy, len(list))
	for _, policy := range list {
		result[policy.Phase] = policy
	}
	return result
}

// transitions lists the allowed phase changes. Any non-terminal phase may
// also go to stopping (user stop) or done (error, max session duration).
var transitions = map[domain.CodingSessionPhase][]domain.CodingSessionPhase{
	domain.CodingPhaseAwaitingApproval: {domain.CodingPhaseQueued},
	domain.CodingPhaseQueued:           {domain.CodingPhaseProvisioning},
	domain.CodingPhaseProvisioning:     {domain.CodingPhaseStarting},
	domain.CodingPhaseStarting:         {domain.CodingPhaseSetup, domain.CodingPhaseRunning, domain.CodingPhaseProvisioning},
	domain.CodingPhaseSetup:            {domain.CodingPhaseRunning},
	domain.CodingPhaseRunning:          {domain.CodingPhaseAwaitingUser, domain.CodingPhasePublishing, domain.CodingPhaseIdle},
	domain.CodingPhaseAwaitingUser:     {domain.CodingPhaseRunning, domain.CodingPhaseQueued},
	domain.CodingPhasePublishing:       {domain.CodingPhaseIdle, domain.CodingPhaseRunning},
	// A follow-up on an idle session runs a new turn in the warm sandbox, or
	// re-queues when the sandbox was reclaimed (restore from the pushed branch).
	domain.CodingPhaseIdle:     {domain.CodingPhaseRunning, domain.CodingPhaseQueued},
	domain.CodingPhaseStopping: {domain.CodingPhaseDone},
	// done → queued is "continue"/"restart" (a new turn after completion, error or stop).
	domain.CodingPhaseDone: {domain.CodingPhaseQueued},
}

// CanTransition reports whether the reconciler or an API call may move a
// session from one phase to another.
func CanTransition(from, to domain.CodingSessionPhase) bool {
	if from != domain.CodingPhaseDone && from != domain.CodingPhaseStopping && (to == domain.CodingPhaseStopping || to == domain.CodingPhaseDone) {
		return true
	}
	for _, next := range transitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

// TerminalState is the state a session settles in when it reaches done.
func TerminalState(errorCode domain.CodingErrorCode, stoppedByUser bool) domain.CodingSessionState {
	switch {
	case errorCode != "":
		return domain.CodingSessionError
	case stoppedByUser:
		return domain.CodingSessionStale
	default:
		return domain.CodingSessionComplete
	}
}

// Backoff is the retry delay after attempt n (0-based).
func Backoff(attempt int) time.Duration {
	delay := 2 * time.Second
	for i := 0; i < attempt && delay < time.Minute; i++ {
		delay *= 2
	}
	return min(delay, time.Minute)
}
