package coding

import (
	"testing"
	"time"

	"flow/api/internal/config"
	"flow/api/internal/domain"
)

func defaultConfig(t *testing.T) config.CodingConfig {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Coding
}

func TestEveryPhaseHasAPolicy(t *testing.T) {
	policies := PhasePolicies(defaultConfig(t))
	for phase := range transitions {
		policy, ok := policies[phase]
		if !ok {
			t.Fatalf("phase %s has no policy", phase)
		}
		if phase != domain.CodingPhaseDone && policy.State == "" {
			t.Fatalf("phase %s has no state", phase)
		}
		if phase != domain.CodingPhaseDone && phase != domain.CodingPhaseIdle && phase != domain.CodingPhaseStopping && policy.OnTimeout == "" {
			t.Fatalf("phase %s has no timeout error code", phase)
		}
	}
	if policies[domain.CodingPhaseRunning].Timeout != time.Hour {
		t.Fatalf("turn timeout: %v", policies[domain.CodingPhaseRunning].Timeout)
	}
}

func TestTransitions(t *testing.T) {
	ok := [][2]domain.CodingSessionPhase{
		{domain.CodingPhaseQueued, domain.CodingPhaseProvisioning},
		{domain.CodingPhaseRunning, domain.CodingPhaseStopping},
		{domain.CodingPhaseIdle, domain.CodingPhaseRunning},
		{domain.CodingPhaseDone, domain.CodingPhaseQueued},
		{domain.CodingPhaseSetup, domain.CodingPhaseDone},
	}
	for _, pair := range ok {
		if !CanTransition(pair[0], pair[1]) {
			t.Errorf("%s → %s must be allowed", pair[0], pair[1])
		}
	}
	bad := [][2]domain.CodingSessionPhase{
		{domain.CodingPhaseQueued, domain.CodingPhaseRunning},
		{domain.CodingPhaseDone, domain.CodingPhaseStopping},
		{domain.CodingPhaseStopping, domain.CodingPhaseRunning},
	}
	for _, pair := range bad {
		if CanTransition(pair[0], pair[1]) {
			t.Errorf("%s → %s must be rejected", pair[0], pair[1])
		}
	}
}

func TestTerminalStateAndBackoff(t *testing.T) {
	if TerminalState(domain.CodingErrPushFailed, false) != domain.CodingSessionError || TerminalState("", true) != domain.CodingSessionStale || TerminalState("", false) != domain.CodingSessionComplete {
		t.Fatal("terminal states")
	}
	if Backoff(0) != 2*time.Second || Backoff(1) != 4*time.Second || Backoff(10) != time.Minute {
		t.Fatalf("backoff: %v %v %v", Backoff(0), Backoff(1), Backoff(10))
	}
}
