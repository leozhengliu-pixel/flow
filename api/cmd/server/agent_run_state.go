package main

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"flow/api/internal/store"
)

// A worker run's calls reach Flow through any of its API servers: its /mcp calls (several at once in a turn), its
// guard operations and its activity posts may each be served by a different process. So no per-run state is kept
// in a process's memory or behind a process's lock:
//
//   - the run's ledger is one row per call (agent_run_tool_calls, agent_run_outcomes.go);
//   - its tool budget, guard and notices are versioned states (agent_run_states): read, update, compare-and-swap,
//     retried when another server stored first (updateAgentRunState);
//   - a chat reply's activities are rows numbered by the worker, applied to the saved reply in their order
//     (agent_worker_chat.go).
//
// What stays in a process is only what cannot move: the requests following a reply (SSE), a connector call in
// flight with its question to the person (agent_runtime_elicitations.go), and the run's discovered connector
// tools (a cache: another process discovers them again).

// errAgentRunStateBusy is returned when a state kept changing under an update (other servers kept storing first).
var errAgentRunStateBusy = errors.New("agent run state changed concurrently too often")

// agentRunStateAttempts bounds the compare-and-swap rounds of one update.
const agentRunStateAttempts = 40

// agentRunStateOf reads a run's state under key: the versioned state, or the value an earlier version of Flow kept
// in agent_run_keys (version 0, so the first update stores it as a versioned state).
func (s *server) agentRunStateOf(ctx context.Context, runID, key string) (raw []byte, version int64, found bool, err error) {
	raw, version, found, err = s.store.AgentRunState(ctx, runID, key)
	if err != nil || found {
		return raw, version, found, err
	}
	raw, found, err = s.store.AgentRunKey(ctx, runID, key)
	return raw, 0, found, err
}

// updateAgentRunState applies update to a run's state under key atomically across API servers. update gets the
// stored value (found false: none) and returns the next one; it runs again on the newer value when another server
// stored first, so it must have no side effects (callers act on its outcome once updateAgentRunState returned).
func (s *server) updateAgentRunState(ctx context.Context, run store.AgentRun, key string, update func(raw []byte, found bool) ([]byte, error)) error {
	ctx = context.WithoutCancel(ctx)
	for attempt := range agentRunStateAttempts {
		raw, version, found, err := s.agentRunStateOf(ctx, run.ID, key)
		if err != nil {
			return err
		}
		next, err := update(raw, found)
		if err != nil {
			return err
		}
		swapped, err := s.store.SwapAgentRunState(ctx, run, key, version, next)
		if err != nil || swapped {
			return err
		}
		agentRunStateBackoff(attempt)
	}
	return errAgentRunStateBusy
}

// agentRunStateBackoff waits a little (more after each lost round) before another compare-and-swap round.
func agentRunStateBackoff(attempt int) {
	if attempt < 2 {
		return
	}
	time.Sleep(time.Duration(1+rand.IntN(min(attempt, 10)*2)) * time.Millisecond)
}

// resetAgentRunState starts a run's states under keys over (stored as null) and forgets its ledger.
func (s *server) resetAgentRunState(ctx context.Context, run store.AgentRun, keys ...string) {
	ctx = context.WithoutCancel(ctx)
	_ = s.store.DeleteAgentRunToolCalls(ctx, run.ID)
	for _, key := range keys {
		_ = s.store.ReplaceAgentRunState(ctx, run, key, []byte(`null`))
		if _, found, err := s.store.AgentRunKey(ctx, run.ID, key); err == nil && found {
			_ = s.store.ReplaceAgentRunKey(ctx, run, key, []byte(`null`))
		}
	}
}

// copyAgentRunState gives run to the ledger and the states under keys of run from (a run that continues it).
func (s *server) copyAgentRunState(ctx context.Context, from string, to store.AgentRun, keys ...string) {
	ctx = context.WithoutCancel(ctx)
	_ = s.store.CopyAgentRunToolCalls(ctx, from, to)
	for _, key := range keys {
		if raw, _, found, err := s.agentRunStateOf(ctx, from, key); err == nil && found {
			_ = s.store.ReplaceAgentRunState(ctx, to, key, raw)
		}
	}
	if raw, found, err := s.store.AgentRunKey(ctx, from, agentRunLedgerKey); err == nil && found {
		_ = s.store.ReplaceAgentRunKey(ctx, to, agentRunLedgerKey, raw)
	}
}

// agentRunCallKey is the key of the /mcp call a request serves (its Idempotency-Key), under which the call's
// ledger entry is recorded.
type agentRunCallKey struct{}

func withAgentRunCallKey(ctx context.Context, key string) context.Context {
	if key == "" {
		return ctx
	}
	return context.WithValue(ctx, agentRunCallKey{}, key)
}

func agentRunCallKeyFrom(ctx context.Context) string {
	key, _ := ctx.Value(agentRunCallKey{}).(string)
	return key
}
