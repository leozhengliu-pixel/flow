package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Loop runs on the agent worker (FLOW_AGENT_RUNTIME=worker, docs/agent-runtime-design.md §6.7, §7.3). The loop run
// record is unchanged — the run page, its steps, tool calls, output, contract and acceptance are the in-process
// run's — and only the model loop moves to the worker:
//
//   - start: startLoopRun records the run as running (its lease held for an agent run, workerLoopLeaseOwner) and
//     queues a run of kind loop whose payload names the loop run and its trigger. The concurrency budget counts the
//     running loop runs in the database, so it holds across API servers.
//   - setup: every server builds the run's context the way executeLoopRun does (prepareLoopRun: the owner, scope,
//     team-restricted key, the loop's connectors as discovered for the run, extra tools, the output contract — resolved
//     once and kept on the run). The payload's system prompt is the in-process run's.
//   - tools: /mcp lists the loop's tools (finish_run and report_progress included) and runs every call through
//     loopRunContext.runToolCall, the in-process run's gates and recorder; finish_run records the report and the
//     calls after it are refused, as in-process. The worker runs the calls of a turn one at a time, as in-process.
//   - turns: the run guard gives the in-process turn rules — at FLOW_LOOP_MAX_TURNS the tools are withheld with the
//     summary prompt, a reply without tools goes through the completion check (sent back with its nudge, or the run
//     ends incomplete), an empty run fails as empty, and the output is the replies and the finish_run report.
//   - end: completing the agent run finishes the loop run with finishLoopRun (failure classification or the
//     acceptance check). Heartbeats keep the loop run's lease and carry its cancel; Cancel reaches the worker at once;
//     a dead worker's run is restarted from scratch when nothing was written, else interrupted.

// workerLoopRunInfo is what a loop run's payload says (agent_runs.payload "loop").
type workerLoopRunInfo struct {
	LoopRunID string      `json:"loopRunId"`
	LoopID    string      `json:"loopId"`
	Trigger   loopTrigger `json:"trigger"`
	MaxTurns  int         `json:"maxTurns"`
	// TimeoutMS is the run's wall-clock limit (FLOW_LOOP_RUN_TIMEOUT).
	TimeoutMS int64 `json:"timeoutMs"`
	// Started is when the loop run started (unix ms).
	Started int64 `json:"started"`
}

func workerLoopInfo(run store.AgentRun) (workerLoopRunInfo, bool) {
	if run.Kind != store.AgentRunKindLoop || len(run.Payload) == 0 {
		return workerLoopRunInfo{}, false
	}
	var payload struct {
		Loop *workerLoopRunInfo `json:"loop"`
	}
	if json.Unmarshal(run.Payload, &payload) != nil || payload.Loop == nil || payload.Loop.LoopRunID == "" {
		return workerLoopRunInfo{}, false
	}
	return *payload.Loop, true
}

// loopRunsOnWorker reports whether loop runs execute on the agent worker.
func (s *server) loopRunsOnWorker() bool {
	return s.agentRuntimeWorker() && s.agentRuntimeEnabled()
}

// workerLoopLeaseOwner holds a loop run's lease for its agent run.
func workerLoopLeaseOwner(agentRunID string) string { return "agent-run:" + agentRunID }

// workerLoopStateKey keeps what a loop run's model loop decided so far (agent_run_states).
const workerLoopStateKey = "loop:state"

// workerLoopState is a loop run's model-loop state between the worker's calls.
type workerLoopState struct {
	ToolCalls   int    `json:"toolCalls"`
	Corrections int    `json:"corrections"`
	Output      string `json:"output,omitempty"`
	Final       bool   `json:"final,omitempty"`
	Nudged      bool   `json:"nudged,omitempty"`
	Finished    bool   `json:"finished,omitempty"`
	// Ending is how the agent stopped without finishing (loopRunEnding).
	EndReason  string `json:"endReason,omitempty"`
	EndMessage string `json:"endMessage,omitempty"`
	// Failure is the run's failure Flow decided (a budget, an empty run, an unavailable setup).
	FailReason  string `json:"failReason,omitempty"`
	FailMessage string `json:"failMessage,omitempty"`
}

func (s *server) workerLoopStateOf(ctx context.Context, runID string) workerLoopState {
	var state workerLoopState
	if raw, _, found, err := s.agentRunStateOf(ctx, runID, workerLoopStateKey); err == nil && found {
		_ = json.Unmarshal(raw, &state)
	}
	return state
}

func (s *server) updateWorkerLoopState(ctx context.Context, run store.AgentRun, change func(*workerLoopState)) workerLoopState {
	var result workerLoopState
	err := s.updateAgentRunState(ctx, run, workerLoopStateKey, func(raw []byte, _ bool) ([]byte, error) {
		var state workerLoopState
		_ = json.Unmarshal(raw, &state)
		change(&state)
		result = state
		return json.Marshal(state)
	})
	if err != nil {
		log.Printf("agent run %s: loop state not saved: %v", run.ID, err)
	}
	return result
}

// reserveWorkerLoopRun is the loop concurrency budget for runs on the worker: the running loop runs in the database,
// in the workspace and in all.
func (s *server) reserveWorkerLoopRun(ctx context.Context, workspace string, limits loopRunLimits) error {
	running, err := s.store.RunningLoopRuns(ctx)
	if err != nil {
		return nil
	}
	inWorkspace := 0
	for _, item := range running {
		if item.Workspace == workspace {
			inWorkspace++
		}
	}
	if limits.MaxConcurrent > 0 && len(running) >= limits.MaxConcurrent {
		return loopBudgetError("Too many loop runs are in progress on this server (limit %d); try again when one finishes", limits.MaxConcurrent)
	}
	if limits.MaxConcurrentPerWorkspace > 0 && inWorkspace >= limits.MaxConcurrentPerWorkspace {
		return loopBudgetError("Too many loop runs are in progress in this workspace (limit %d); try again when one finishes", limits.MaxConcurrentPerWorkspace)
	}
	return nil
}

// workerLoopRunInFlight reports whether a run of the loop on this entity is still running (on any server).
func (s *server) workerLoopRunInFlight(ctx context.Context, workspace, loopID, entityID string) bool {
	running, err := s.store.RunningLoopRuns(ctx)
	if err != nil {
		return false
	}
	for _, item := range running {
		if item.Workspace != workspace || item.LoopID != loopID {
			continue
		}
		if run, err := s.store.LoopRun(ctx, workspace, item.ID); err == nil && run.Status == "running" && run.EntityID == entityID {
			return true
		}
	}
	return false
}

// queueLoopRunOnWorker stores the running loop run and queues its agent run.
func (s *server) queueLoopRunOnWorker(workspace string, loop domain.Loop, trigger loopTrigger, run domain.LoopRun, limits loopRunLimits) (domain.LoopRun, error) {
	ctx := context.Background()
	agentRunID := newWorkerChatRunID()
	now := time.Now().UTC()
	run.HeartbeatAt = &now
	if err := s.store.CreateLoopRun(ctx, workspace, run, store.LoopRunLease{Owner: workerLoopLeaseOwner(agentRunID), ExpiresAt: now.Add(limits.LeaseTTL)}); err != nil {
		return domain.LoopRun{}, err
	}
	principal, _ := json.Marshal(agentRunPrincipal{Mode: agentRunModeUser, UserID: loop.OwnerID, Connectors: slices.Clone(loop.ConnectorIDs)})
	payload, _ := json.Marshal(map[string]any{"loop": workerLoopRunInfo{LoopRunID: run.ID, LoopID: loop.ID, Trigger: trigger, MaxTurns: limits.MaxTurns, TimeoutMS: limits.Timeout.Milliseconds(), Started: now.UnixMilli()}})
	if _, _, err := s.store.EnqueueAgentRun(ctx, store.AgentRunInput{ID: agentRunID, WorkspaceKey: workspace, SessionID: run.ID, Kind: store.AgentRunKindLoop, IdempotencyKey: "loop-run:" + run.ID, Principal: principal, Payload: payload}); err != nil {
		rec := &loopRunRecorder{s: s, workspace: workspace, run: run, leaseOwner: workerLoopLeaseOwner(agentRunID)}
		s.finishLoopRun(ctx, workspace, loop, rec, &loopRunError{reason: "unavailable", message: "The run could not be queued for the agent worker", err: err})
		stored, _ := s.store.LoopRun(ctx, workspace, run.ID)
		return stored, nil
	}
	return run, nil
}

// workerLoopRecorder is the loop run's recorder as stored now: a worker run's calls may reach any server, one at a
// time, and each records on the run as it is stored.
func (s *server) workerLoopRecorder(ctx context.Context, run store.AgentRun, info workerLoopRunInfo) (*loopRunRecorder, error) {
	record, err := s.store.LoopRun(ctx, run.WorkspaceKey, info.LoopRunID)
	if err != nil {
		return nil, err
	}
	rec := &loopRunRecorder{s: s, workspace: run.WorkspaceKey, run: record, leaseOwner: workerLoopLeaseOwner(run.ID)}
	for _, step := range record.Steps {
		rec.order = max(rec.order, step.Order)
	}
	for _, call := range record.ToolCalls {
		rec.order = max(rec.order, call.Order)
	}
	state := s.workerLoopStateOf(ctx, run.ID)
	if state.EndReason != "" {
		rec.ending = &loopRunEnding{reason: state.EndReason, message: state.EndMessage}
	}
	return rec, nil
}

// workerLoopContext is the loop run's context for one of its worker run's requests.
func (s *server) workerLoopContext(ctx context.Context, run store.AgentRun, info workerLoopRunInfo) (*loopRunContext, error) {
	rec, err := s.workerLoopRecorder(ctx, run, info)
	if err != nil {
		return nil, err
	}
	if rec.run.Status != "running" {
		return nil, &loopRunError{reason: "unavailable", message: "The loop run is no longer running"}
	}
	data, ok := s.store.WorkspaceMetadataFields(run.WorkspaceKey, "loops")
	if !ok {
		return nil, errNotFound
	}
	loop := loopByID(&data, info.LoopID)
	if loop == nil {
		return nil, &loopRunError{reason: "unavailable", message: "The loop no longer exists"}
	}
	grant := s.agentRunGrantOf(run)
	connectors := func(request *http.Request, _ domain.Bootstrap, owner domain.User) (context.Context, error) {
		// The loop's selected connectors, discovered for the run once (a skipped one is noticed on the run).
		tools, err := s.agentRunConnectorTools(ctx, grant)
		if err != nil {
			return nil, &loopRunError{reason: "unavailable", message: "A selected connector is unavailable: " + err.Error(), err: err}
		}
		connectorCtx := context.WithValue(request.Context(), connectorContextKey{}, connectorRequestContext{Workspace: run.WorkspaceKey, UserID: owner.ID})
		return context.WithValue(connectorCtx, connectorToolsKey{}, tools), nil
	}
	lc, err := s.prepareLoopRun(ctx, run.WorkspaceKey, *loop, info.Trigger, rec, connectors)
	if err != nil {
		return nil, err
	}
	lc.request = lc.request.WithContext(withAgentRun(lc.request.Context(), run))
	return lc, nil
}

// workerLoopPayload is the context of a loop run's payload: the in-process run's system prompt and first message. A
// run whose setup fails is told so (failure) and ends with it.
func (s *server) workerLoopPayload(ctx context.Context, run store.AgentRun, info workerLoopRunInfo) (*agentRunPromptContext, string) {
	lc, err := s.workerLoopContext(ctx, run, info)
	if err != nil {
		reason, message := "error", err.Error()
		var runErr *loopRunError
		if errors.As(err, &runErr) {
			reason, message = runErr.reason, runErr.message
		}
		s.updateWorkerLoopState(ctx, run, func(state *workerLoopState) { state.FailReason, state.FailMessage = reason, message })
		return nil, message
	}
	return &agentRunPromptContext{System: lc.systemPrompt(ctx), Messages: []agentRunPromptMessage{{Role: "user", Content: "Run this loop now and follow its instructions."}}, WebAccess: lc.loop.WebSearch}, ""
}

// serveMCPLoopRun answers a loop run token's tools/list and tools/call: the loop's tools, through its gates.
func (s *server) serveMCPLoopRun(w http.ResponseWriter, r *http.Request, request mcpRPCRequest, grant *agentRunGrant, info workerLoopRunInfo) {
	ctx := r.Context()
	lc, err := s.workerLoopContext(ctx, grant.run, info)
	if err != nil {
		if request.Method == "tools/list" {
			writeJSON(w, http.StatusOK, mcpRPCResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{"tools": []any{}}})
			return
		}
		s.writeMCPToolResult(w, r, request.ID, nil, err)
		return
	}
	definitions := s.agentTurnTools(lc.request.Context())
	if request.Method == "tools/list" {
		listed := make([]map[string]any, 0, len(definitions))
		for _, tool := range definitions {
			name := strings.TrimPrefix(tool.Name, "mcp__flow.")
			listed = append(listed, map[string]any{"name": name, "description": tool.Description, "inputSchema": json.RawMessage(tool.Parameters), "annotations": map[string]any{"readOnlyHint": tool.Access == "read", "destructiveHint": strings.HasPrefix(name, "delete_")}})
		}
		writeJSON(w, http.StatusOK, mcpRPCResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{"tools": listed}})
		return
	}
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(request.Params, &params); err != nil {
		s.writeMCPError(w, request.ID, -32602, "Invalid params", nil)
		return
	}
	if params.Arguments == nil {
		params.Arguments = map[string]any{}
	}
	if !slices.ContainsFunc(definitions, func(tool agentProviderTool) bool { return strings.TrimPrefix(tool.Name, "mcp__flow.") == params.Name }) {
		s.writeMCPToolResult(w, r, request.ID, nil, fmt.Errorf("%s is not available to this loop run", params.Name))
		return
	}
	arguments, _ := json.Marshal(params.Arguments)
	call := domain.AgentToolCall{ID: "run_" + grant.run.ID, Name: params.Name, Arguments: arguments}
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" && len(key) <= 160 {
		call.ID = key
	}
	result := func(content string, failed bool) {
		var value any = content
		if json.Valid([]byte(content)) {
			value = json.RawMessage(content)
		}
		if failed {
			s.writeMCPToolResult(w, r, request.ID, nil, errors.New(content))
			return
		}
		s.writeMCPToolResult(w, r, request.ID, value, nil)
	}
	state := s.workerLoopStateOf(ctx, grant.run.ID)
	if lc.rec.summary() != nil {
		result("The run already finished with finish_run; this call was not executed.", true)
		return
	}
	switch params.Name {
	case agentProgressTool:
		lc.progressStep(call)
		result(`{"ok":true}`, false)
		return
	case loopFinishToolName:
		summary, err := parseLoopSummary(call.Arguments)
		if err != nil {
			result(err.Error(), true)
			return
		}
		lc.rec.setSummary(summary)
		lc.rec.save(true)
		result(`{"ok":true}`, false)
		return
	}
	limits := lc.limits
	state = s.updateWorkerLoopState(ctx, grant.run, func(state *workerLoopState) { state.ToolCalls++ })
	if limits.MaxToolCalls > 0 && state.ToolCalls > limits.MaxToolCalls {
		message := fmt.Sprintf("The run made more than %d tool calls, its budget. Narrow the instructions or raise FLOW_LOOP_MAX_TOOL_CALLS.", limits.MaxToolCalls)
		s.updateWorkerLoopState(ctx, grant.run, func(state *workerLoopState) { state.FailReason, state.FailMessage = "budget_exhausted", message })
		// The run ends here, as in-process: its worker stops at once.
		_, _ = s.store.CancelAgentRun(context.WithoutCancel(ctx), grant.run.ID)
		result(message, true)
		return
	}
	if !lc.readOnly[params.Name] {
		// A change (or a connector call) may land: a run that may have written is never restarted.
		_ = s.store.MarkAgentRunWrote(ctx, grant.run.ID)
	}
	content, failed := lc.runToolCall(ctx, call)
	result(content, failed)
}

// workerLoopGuardOp is a loop run's guard: the in-process model loop's turn rules (agent_runtime_guard.go).
func (s *server) workerLoopGuardOp(ctx context.Context, run store.AgentRun, info workerLoopRunInfo, input workerRunGuardRequest) (workerRunGuardResponse, error) {
	response := workerRunGuardResponse{Enabled: true, DecisionRound: -1}
	maxTurns := info.MaxTurns
	if maxTurns <= 0 {
		maxTurns = s.loopLimits().MaxTurns
	}
	switch input.Op {
	case "start", "before", "after", "coding":
		return response, nil
	case "turn":
		if input.Round < maxTurns {
			return response, nil
		}
		// The tool turns are used up: the last turn has no tools and writes the summary.
		s.updateWorkerLoopState(ctx, run, func(state *workerLoopState) {
			state.Final = true
			response.Withhold = true
			if !state.Nudged {
				response.Nudge, state.Nudged = "\n\n"+loopSummaryPrompt, true
			}
		})
		return response, nil
	case "finish":
	default:
		return response, store.ErrAgentRunInput
	}
	rec, err := s.workerLoopRecorder(ctx, run, info)
	if err != nil {
		return response, err
	}
	state := s.workerLoopStateOf(ctx, run.ID)
	final := state.Final || input.Phase == "turnLimit"
	text := strings.TrimSpace(input.Text)
	nudge := ""
	switch {
	case rec.summary() != nil:
		// finish_run reported the run: its report ends the output (the turn's own text is narration).
		state.Output = strings.TrimSpace(state.Output + "\n\n" + renderLoopSummary(rec.summary()))
		rec.setOutputNow(state.Output)
	case text == "" && strings.TrimSpace(state.Output) == "" && rec.toolCount() == 0:
		rec.setOutputNow("")
		state.FailReason, state.FailMessage = "empty_response", "Flow Agent returned an empty response"
		response.Error = state.FailMessage
	default:
		if text != "" {
			state.Output = strings.TrimSpace(strings.TrimSpace(state.Output) + "\n\n" + text)
		}
		rec.setOutputNow(state.Output)
		if final {
			// The tool turns ran out before the agent reported it was done.
			rec.endUnfinished("budget_exhausted", fmt.Sprintf("The run used all %d tool turns before it finished; the summary is what it had done by then", maxTurns), text)
			break
		}
		var ending *loopRunEnding
		nudge, ending = loopCompletionCheck(rec, text, state.Corrections)
		if nudge != "" {
			state.Corrections++
			break
		}
		if ending != nil {
			rec.endUnfinished(ending.reason, ending.message, text)
		}
	}
	if rec.ending != nil {
		state.EndReason, state.EndMessage = rec.ending.reason, rec.ending.message
	}
	state.Finished = nudge == ""
	rec.save(true)
	s.updateWorkerLoopState(ctx, run, func(stored *workerLoopState) {
		toolCalls := stored.ToolCalls
		*stored = state
		stored.ToolCalls = max(stored.ToolCalls, toolCalls)
	})
	if nudge != "" {
		response.Nudge = nudge
		return response, nil
	}
	if response.Error == "" {
		response.Reply = firstNonEmpty(state.Output, text)
	}
	return response, nil
}

// finishWorkerLoopRun finishes the loop run of a worker run that ended: with Flow's own failure (a budget, an empty
// run, an unavailable setup), as the run ended (cancelled, timed out, interrupted, the provider failed), or through the
// acceptance check of what it produced.
func (s *server) finishWorkerLoopRun(ctx context.Context, run store.AgentRun, info workerLoopRunInfo, status, failure string) {
	ctx = context.WithoutCancel(ctx)
	rec, err := s.workerLoopRecorder(ctx, run, info)
	if err != nil || rec.run.Status != "running" {
		return
	}
	data, ok := s.store.WorkspaceMetadataFields(run.WorkspaceKey, "loops")
	if !ok {
		return
	}
	loop := loopByID(&data, info.LoopID)
	if loop == nil {
		loop = &domain.Loop{ID: info.LoopID}
	}
	state := s.workerLoopStateOf(ctx, run.ID)
	runCtx := ctx
	var runErr error
	cancelWith := func(cause error) {
		canceled, cancel := context.WithCancelCause(ctx)
		cancel(cause)
		runCtx, runErr = canceled, context.Canceled
	}
	switch {
	case state.FailReason != "":
		runErr = &loopRunError{reason: state.FailReason, message: state.FailMessage}
	case status == store.AgentRunCanceled:
		cancelWith(errLoopRunCancelled)
	case status == store.AgentRunInterrupted:
		cancelWith(errLoopLeaseLost)
	case status == store.AgentRunFailed && info.TimeoutMS > 0 && time.Since(time.UnixMilli(info.Started)) >= time.Duration(info.TimeoutMS)*time.Millisecond:
		cancelWith(errLoopRunTimedOut)
	case status == store.AgentRunFailed:
		runErr = loopProviderFailure(errors.New(firstNonEmpty(strings.TrimSpace(failure), "the agent run failed")))
	case !state.Finished:
		// The worker ended without the run's last word: what it had is kept and the run is incomplete.
		rec.endUnfinished("incomplete", loopStoppedMessage, state.Output)
	}
	s.finishLoopRun(runCtx, run.WorkspaceKey, *loop, rec, runErr)
}

// workerLoopHeartbeat keeps a worker loop run's lease; cancel is true when the run should stop (a person cancelled
// it, or its lease is gone).
func (s *server) workerLoopHeartbeat(ctx context.Context, run store.AgentRun, info workerLoopRunInfo) (cancel bool) {
	now := time.Now().UTC()
	requested, err := s.store.HeartbeatLoopRun(ctx, run.WorkspaceKey, info.LoopRunID, workerLoopLeaseOwner(run.ID), now, now.Add(s.loopLimits().LeaseTTL))
	switch {
	case errors.Is(err, store.ErrLoopRunLeaseLost):
		return true
	case err != nil:
		log.Printf("Loop run heartbeat workspace=%s run=%s: %v", run.WorkspaceKey, info.LoopRunID, err)
		return false
	}
	return requested
}

// workerLoopRunOf is the agent run of a worker loop run, if any.
func (s *server) workerLoopRunOf(ctx context.Context, workspace, loopRunID string) (store.AgentRun, workerLoopRunInfo, bool) {
	runs, err := s.store.SessionAgentRuns(ctx, workspace, loopRunID, 5)
	if err != nil {
		return store.AgentRun{}, workerLoopRunInfo{}, false
	}
	for _, run := range runs {
		if info, ok := workerLoopInfo(run); ok {
			return run, info, true
		}
	}
	return store.AgentRun{}, workerLoopRunInfo{}, false
}

// cancelWorkerLoopRun stops a loop run on the worker at once: its agent run is cancelled (the worker learns it from
// the cancel long-poll), and a run no worker took yet ends here as cancelled. It reports whether the run is one.
func (s *server) cancelWorkerLoopRun(ctx context.Context, workspace, loopRunID string) bool {
	run, info, ok := s.workerLoopRunOf(ctx, workspace, loopRunID)
	if !ok {
		return false
	}
	canceled, err := s.store.CancelAgentRun(ctx, run.ID)
	if err == nil && !store.AgentRunHeld(canceled.Status) {
		s.finishWorkerLoopRun(ctx, canceled, info, store.AgentRunCanceled, "")
	}
	return true
}

// workerLoopRunPending reports whether a running loop run is held for its agent run, which is queued or held: the
// loop reconciler leaves it alone (its lease is renewed here while it waits for a worker). A queued run no worker
// takes within FLOW_AGENT_WORKER_CLAIM_TIMEOUT ends the loop run as unavailable.
func (s *server) workerLoopRunPending(ctx context.Context, item store.RunningLoopRun) bool {
	agentRunID, ok := strings.CutPrefix(item.LeaseOwner, "agent-run:")
	if !ok {
		return false
	}
	run, err := s.store.AgentRun(ctx, agentRunID)
	if err != nil {
		return false
	}
	info, ok := workerLoopInfo(run)
	if !ok {
		return false
	}
	switch {
	case run.Status == store.AgentRunQueued:
		if canceled, expired, err := s.store.CancelUnclaimedAgentRun(ctx, run.ID, workerChatNoWorker, time.Now().Add(-s.agentWorkerClaimTimeout())); err == nil && expired {
			s.updateWorkerLoopState(ctx, canceled, func(state *workerLoopState) { state.FailReason, state.FailMessage = "unavailable", workerChatNoWorker })
			s.finishWorkerLoopRun(ctx, canceled, info, store.AgentRunCanceled, "")
			return true
		}
		now := time.Now().UTC()
		_, _ = s.store.HeartbeatLoopRun(ctx, item.Workspace, item.ID, item.LeaseOwner, now, now.Add(s.loopLimits().LeaseTTL))
		return true
	case store.AgentRunHeld(run.Status):
		return time.Now().Before(item.LeaseExpiresAt)
	}
	// The agent run ended without finishing the loop run (its completion was lost): finish it now.
	s.finishWorkerLoopRun(ctx, run, info, run.Status, run.Error)
	return true
}

// recoverWorkerLoopRun handles a loop run's agent run the reconciler released: requeued (it wrote nothing) starts
// the loop run over; interrupted ends it as interrupted. It reports whether run is a loop run.
func (s *server) recoverWorkerLoopRun(ctx context.Context, run store.AgentRun, requeued bool) bool {
	info, ok := workerLoopInfo(run)
	if !ok {
		return false
	}
	if !requeued {
		s.finishWorkerLoopRun(ctx, run, info, store.AgentRunInterrupted, "")
		return true
	}
	s.resetAgentRunState(ctx, run, workerLoopStateKey)
	now := time.Now().UTC()
	_, err := s.store.UpdateLoopRun(ctx, run.WorkspaceKey, info.LoopRunID, []store.LoopRunEvent{loopRunEvent("restarted", map[string]any{"attempt": run.Attempts})}, func(record *domain.LoopRun) error {
		if record.Status != "running" {
			return store.ErrNoMutation
		}
		// The new attempt starts over (the first wrote nothing); the contract stays.
		record.Steps, record.ToolCalls, record.Output, record.Summary, record.Produced, record.HeartbeatAt = nil, nil, "", nil, nil, &now
		return nil
	})
	if err != nil && !errors.Is(err, store.ErrNoMutation) {
		log.Printf("restart worker loop run %s: %v", info.LoopRunID, err)
	}
	return true
}
