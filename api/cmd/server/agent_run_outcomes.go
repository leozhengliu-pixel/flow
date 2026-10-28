package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// A worker run (agent-worker) makes its tool calls through /mcp with its run token, so Flow sees every write the
// run makes and keeps the run's ledger itself (agent_action_outcomes.go), under agentRunLedgerKey: each Flow write
// call and each connector call that may write, with how it ended. The run's final answer is checked against it at
// two points:
//
//   - its last word (the guard's finish, agent_runtime_guard.go): an answer that reports change outcomes while the
//     run made no write is answered with the nudge, once (the worker sends it to the model and asks again); a second
//     such answer becomes Flow's statement that nothing was executed. Otherwise sentences contrary to the ledger are
//     rewritten and Flow's count follows the answer, as in a chat reply;
//   - the response activity it posts (POST /internal/agent-runtime/runs/{id}/activities), for a run that did not
//     ask for its last word: the same checks without the nudge.

// agentRunLedgerKey is where a worker run's ledger is kept (agent_run_keys).
const agentRunLedgerKey = "outcome:ledger"

// agentRunLedgerMax bounds the entries a run's ledger keeps (the tool budget stops a run well before).
const agentRunLedgerMax = 500

// agentRunLedgerLocks serializes the ledger updates of one run in this process (parallel tool calls of a turn).
var agentRunLedgerLocks sync.Map

// recordAgentRunWrite adds a write call's outcome to the run's ledger.
func (s *server) recordAgentRunWrite(ctx context.Context, run store.AgentRun, outcome agentWriteOutcome) {
	lock, _ := agentRunLedgerLocks.LoadOrStore(run.ID, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	ctx = context.WithoutCancel(ctx)
	ledger := s.agentRunLedger(ctx, run)
	if len(ledger.outcomes) >= agentRunLedgerMax {
		return
	}
	raw, err := json.Marshal(append(ledger.outcomes, outcome))
	if err == nil {
		err = s.store.ReplaceAgentRunKey(ctx, run, agentRunLedgerKey, raw)
	}
	if err != nil {
		log.Printf("agent run %s: write outcome not recorded: %v", run.ID, err)
	}
}

// recordAgentRunFlowWrite records a Flow tool call of a run when the tool writes.
func (s *server) recordAgentRunFlowWrite(ctx context.Context, run store.AgentRun, name string, arguments, result []byte, status, reason string) {
	if !agentIsFlowWriteTool(name) {
		return
	}
	s.recordAgentRunWrite(ctx, run, agentWriteOutcomeFor(agentToolBaseName(name), domain.AgentToolCall{Name: name, Arguments: arguments, Result: result}, status, reason))
}

// recordAgentRunConnectorWrite records a connector call of a run when the tool may write.
func (s *server) recordAgentRunConnectorWrite(ctx context.Context, run store.AgentRun, tool connectorTool, args map[string]any, status, reason string) {
	if tool.ReadOnly {
		return
	}
	arguments, _ := json.Marshal(args)
	s.recordAgentRunWrite(ctx, run, agentWriteOutcomeFor(agentConnectorLedgerName(tool), domain.AgentToolCall{Name: tool.Definition.Name, Arguments: arguments}, status, reason))
}

// agentRunLedger is the run's ledger as recorded so far.
func (s *server) agentRunLedger(ctx context.Context, run store.AgentRun) *agentWriteLedger {
	ledger := &agentWriteLedger{}
	if raw, found, err := s.store.AgentRunKey(ctx, run.ID, agentRunLedgerKey); err == nil && found {
		_ = json.Unmarshal(raw, &ledger.outcomes)
	}
	return ledger
}

// forgetAgentRunLedgerLock drops a finished run's lock.
func forgetAgentRunLedgerLock(runID string) {
	agentRunLedgerLocks.Delete(runID)
}

// builtinOutcomeRequest is what a thread run was asked to do, for the outcome check: the person's latest message
// (the mention's comment, or a reply in the thread), and for a delegation the issue's title and description.
func builtinOutcomeRequest(task domain.AgentTask, data domain.Bootstrap) string {
	request := firstNonEmpty(strings.TrimSpace(task.Prompt), strings.TrimSpace(task.InitialPrompt))
	if task.Trigger == "delegation" && len(data.Issues) == 1 {
		request += "\n" + data.Issues[0].Title + "\n" + data.Issues[0].Description
	}
	return request
}

// agentRunCheckedResponse is the response a worker run posts, checked against the run's ledger when the run did
// not ask for its last word through the guard (which checked it then).
func (s *server) agentRunCheckedResponse(r *http.Request, run store.AgentRun, task domain.AgentTask, body string) string {
	ctx := r.Context()
	started := false
	if raw, found, err := s.store.AgentRunKey(ctx, run.ID, workerRunGuardKey); err == nil && found {
		var state workerRunGuardState
		if json.Unmarshal(raw, &state) == nil {
			if state.Finished {
				return body
			}
			started = state.Started != nil
		}
	}
	if body == agentNothingExecutedText(true) || body == agentNothingExecutedText(false) {
		return body
	}
	data := domain.Bootstrap{}
	if _, loaded, _, _, err := s.builtinRunRequest(r, task); err == nil {
		data = loaded
	}
	request := builtinOutcomeRequest(task, data)
	chinese := containsHan(request)
	if len(data.Issues) == 1 {
		chinese = chinese || containsHan(data.Issues[0].Title+data.Issues[0].Description)
	}
	ledger := s.agentRunLedger(ctx, run)
	if !started && task.CodingSessionID == "" && s.agentUngroundedText(request, body, ledger) {
		return agentNothingExecutedText(chinese)
	}
	return ledger.report(body, chinese)
}
