package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// A worker run (agent-worker) makes its tool calls through /mcp with its run token, so Flow sees every write the
// run makes and keeps the run's ledger itself (agent_action_outcomes.go): each Flow write call and each connector
// call that may write, with how it ended, one row per call (agent_run_tool_calls). The run's final answer is
// checked against the stored entries at two points:
//
//   - its last word (the guard's finish, agent_runtime_guard.go): an answer that reports change outcomes while the
//     run made no write is answered with the nudge, once (the worker sends it to the model and asks again); a second
//     such answer becomes Flow's statement that nothing was executed. Otherwise sentences contrary to the ledger are
//     rewritten and Flow's count follows the answer, as in a chat reply;
//   - the response activity it posts (POST /internal/agent-runtime/runs/{id}/activities), for a run that did not
//     ask for its last word: the same checks without the nudge.

// agentRunLedgerKey is where an earlier version of Flow kept a worker run's whole ledger (agent_run_keys); it is
// still read for a run that started then.
const agentRunLedgerKey = "outcome:ledger"

// agentRunLedgerMax bounds the entries a run's ledger keeps (the tool budget stops a run well before).
const agentRunLedgerMax = store.AgentRunToolCallsMax

// recordAgentRunWrite adds a write call's outcome to the run's ledger: its own row (agent_run_tool_calls), keyed by
// the request's Idempotency-Key with the entry's tool and target when the request has one (a retried call updates
// its entries; a bulk call keeps one per target) and by a new id otherwise. Parallel calls of a turn may be served
// by different API servers at once; each adds only its own rows, so no entry is lost and the counts are exact.
func (s *server) recordAgentRunWrite(ctx context.Context, run store.AgentRun, outcome agentWriteOutcome) {
	ctx = context.WithoutCancel(ctx)
	key := idgen.New("call_")
	if call := agentRunCallKeyFrom(ctx); call != "" {
		sum := sha256.Sum256([]byte(call + "\x00" + outcome.Tool + "\x00" + outcome.Target))
		key = "mcp:" + hex.EncodeToString(sum[:20])
	}
	raw, err := json.Marshal(outcome)
	if err == nil {
		err = s.store.RecordAgentRunToolCall(ctx, run, key, time.Now().UnixNano(), raw)
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

// agentRunLedger is the run's ledger as recorded so far, read from the stored entries when the decision is made
// (whichever servers recorded them).
func (s *server) agentRunLedger(ctx context.Context, run store.AgentRun) *agentWriteLedger {
	ledger := &agentWriteLedger{}
	if raw, found, err := s.store.AgentRunKey(ctx, run.ID, agentRunLedgerKey); err == nil && found {
		_ = json.Unmarshal(raw, &ledger.outcomes)
	}
	rows, err := s.store.AgentRunToolCalls(ctx, run.ID)
	if err != nil {
		log.Printf("agent run %s: ledger not read: %v", run.ID, err)
	}
	for _, raw := range rows {
		if len(ledger.outcomes) >= agentRunLedgerMax {
			break
		}
		var outcome agentWriteOutcome
		if json.Unmarshal(raw, &outcome) == nil {
			ledger.outcomes = append(ledger.outcomes, outcome)
		}
	}
	return ledger
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
	if state, found := s.workerRunGuardStateOf(ctx, run.ID); found {
		if state.Finished {
			return body
		}
		started = state.Started != nil
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
