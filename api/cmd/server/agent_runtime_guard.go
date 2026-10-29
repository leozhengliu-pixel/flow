package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// A worker run (agent-worker) runs its own model loop, so the in-process run guard (builtin_run_guard.go) and the
// transfer to Coding (builtin_coding_transfer.go) cannot wrap it. Instead the worker asks Flow at the same points
// the in-process loop decides, and Flow answers with the same builtinRunGuard, kept per run:
//
//   - start: the run's coding context (Coding available, development intent, the coding environment) is read once,
//     from the run's session and issue, exactly as for an in-process run.
//   - turn: before each model turn, whether connector tools are withdrawn (budget spent, or the last
//     builtinDecisionTurns turns of the worker's own cap) and, once, the decision nudge for the last tool result.
//   - before / after: around each connector call — an identical repeat is answered from the earlier result with
//     the nudge, a call over budget is refused, a failed or empty result counts as no progress.
//   - coding: a start_coding_session result is read into the run's delivery state and annotated for the model.
//   - finish: the run's last word — a reply without Coding, a forced-decision turn that kept searching, or the turn
//     limit — moves a development run into Coding through the same tool path and permission checks, or rewrites a
//     "done" claim into the session's real state.
//
// The worker enforces what Flow answers (it removes the tools, short-circuits the call, appends the nudge, posts
// the reply); every rule and every text lives here, so both runtimes behave the same.

// workerRunGuardKey is where a run's guard state is kept (agent_run_states, versioned: workerRunGuardOp).
const workerRunGuardKey = "guard:state"

// workerRunGuardState is a builtinRunGuard as stored between the worker's calls.
type workerRunGuardState struct {
	Coding        builtinCodingContext  `json:"coding"`
	IssueTitle    string                `json:"issueTitle,omitempty"`
	Calls         int                   `json:"calls"`
	Bytes         int                   `json:"bytes"`
	NoProgress    int                   `json:"noProgress"`
	Results       map[string]string     `json:"results,omitempty"`
	Decided       bool                  `json:"decided,omitempty"`
	DecisionRound int                   `json:"decisionRound"`
	HadSession    bool                  `json:"hadSession,omitempty"`
	Started       *workerRunCodingStart `json:"started,omitempty"`
	// Finished is set once the worker asked for the run's last word: the completion check leaves the run alone.
	Finished bool `json:"finished,omitempty"`
	// OutcomeNudged is set once a last word that reported change outcomes no write produced was answered with the
	// nudge (agent_run_outcomes.go).
	OutcomeNudged bool `json:"outcomeNudged,omitempty"`
	// EmptyNudged and TextApprovalNudged are set once a built-in session run's empty reply, or a reply that asked
	// for approval in text, was sent back (as executeBuiltinApplication retries each once).
	EmptyNudged        bool `json:"emptyNudged,omitempty"`
	TextApprovalNudged bool `json:"textApprovalNudged,omitempty"`
}

// workerRunCodingStart is a stored codingStartOutcome.
type workerRunCodingStart struct {
	Delivery  codingDelivery `json:"delivery"`
	SessionID string         `json:"sessionId,omitempty"`
	Blocker   string         `json:"blocker,omitempty"`
}

func (state workerRunGuardState) guard() *builtinRunGuard {
	g := newBuiltinRunGuard(state.Coding)
	g.calls, g.bytes, g.noProgress, g.decided, g.hadSession = state.Calls, state.Bytes, state.NoProgress, state.Decided, state.HadSession
	for key, value := range state.Results {
		g.results[key] = value
	}
	if state.Started != nil {
		g.started = &codingStartOutcome{delivery: state.Started.Delivery, sessionID: state.Started.SessionID, blocker: state.Started.Blocker}
	}
	return g
}

func (state *workerRunGuardState) update(g *builtinRunGuard) {
	state.Coding, state.Calls, state.Bytes, state.NoProgress, state.Decided, state.HadSession = g.coding, g.calls, g.bytes, g.noProgress, g.decided, g.hadSession
	state.Results = g.results
	state.Started = nil
	if g.started != nil {
		state.Started = &workerRunCodingStart{g.started.delivery, g.started.sessionID, g.started.blocker}
	}
}

// workerRunGuardRequest is one guard operation of a worker run.
type workerRunGuardRequest struct {
	Op string `json:"op"`
	// turn
	Round      int  `json:"round"`
	MaxTurns   int  `json:"maxTurns"`
	Connectors bool `json:"connectors"`
	// before / after: the connector call (arguments as the model sent them)
	Tool      string `json:"tool"`
	Arguments string `json:"arguments"`
	// after / coding: the call's result
	Content string `json:"content"`
	Failed  bool   `json:"failed"`
	// finish: reply (the model replied), decision (the forced-decision turn did not start Coding) or turnLimit
	Phase string `json:"phase"`
	Text  string `json:"text"`
}

// workerRunGuardResponse is Flow's answer; the fields beyond the run's guard view are set by their operation.
type workerRunGuardResponse struct {
	Enabled bool `json:"enabled"`
	// CodingDue: the run must still enter Coding (it asks for code changes, Coding is available, not started yet).
	CodingDue bool `json:"codingDue"`
	// DecisionRound is the run's forced-decision turn (-1 until connector tools are withdrawn).
	DecisionRound int `json:"decisionRound"`
	// turn
	MustDecide bool   `json:"mustDecide,omitempty"`
	Nudge      string `json:"nudge,omitempty"`
	// Withhold (a chat reply whose tool budget is spent): the turn is offered no tools, so it gives the last word.
	Withhold bool `json:"withhold,omitempty"`
	// before: handled means the call is not run; content (failed: as an error) is its result.
	Handled bool   `json:"handled,omitempty"`
	Content string `json:"content,omitempty"`
	Failed  bool   `json:"failed,omitempty"`
	// finish: the reply to post (empty: keep the model's own), whether Flow started Coding, or the run's error. A
	// finish answered with Nudge (and nothing else) is not the last word: the worker sends the nudge to the model as
	// the person's message, once, and asks for the last word again with the new answer.
	Reply       string `json:"reply,omitempty"`
	Transferred bool   `json:"transferred,omitempty"`
	Error       string `json:"error,omitempty"`
}

func (s *server) agentRunGuard(w http.ResponseWriter, r *http.Request, _ agentRuntimeCaller, run store.AgentRun) {
	var input workerRunGuardRequest
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	if !decodeJSON(w, r, &input) {
		return
	}
	response, err := s.workerRunGuardOp(r, run, input)
	switch {
	case errors.Is(err, store.ErrAgentRunInput):
		writeError(w, http.StatusBadRequest, "op must be start, turn, before, after, coding or finish (finish: phase reply, decision or turnLimit)")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "the run guard could not be updated")
	default:
		writeJSON(w, http.StatusOK, response)
	}
}

// workerRunGuardStateOf is a run's stored guard; found is false when the run never started one.
func (s *server) workerRunGuardStateOf(ctx context.Context, runID string) (state workerRunGuardState, found bool) {
	raw, _, found, err := s.agentRunStateOf(ctx, runID, workerRunGuardKey)
	if err != nil || !found || json.Unmarshal(raw, &state) != nil {
		return workerRunGuardState{}, false
	}
	return state, true
}

// workerRunGuardOp runs one guard operation. The guard is a versioned state (agent_run_states): the parallel
// connector calls of a turn send their before and after at once, possibly to different API servers, so each
// operation reads the stored guard, applies itself and stores the result only if nobody stored first, and applies
// itself again on the newer guard otherwise. What an operation does besides changing the guard (a skipped call's
// activity and ledger entry) is done once, after its guard was stored; the last word (finish), which may move the
// run into Coding, runs once and merges its outcome onto a guard changed meanwhile.
func (s *server) workerRunGuardOp(r *http.Request, run store.AgentRun, input workerRunGuardRequest) (workerRunGuardResponse, error) {
	ctx := r.Context()
	disabled := workerRunGuardResponse{DecisionRound: -1}
	if info, chat := workerChatInfo(run); chat {
		// A chat reply's guard: its tool budget and outcome check (agent_worker_chat.go).
		return s.workerChatGuardOp(ctx, run, info, input)
	}
	if info, loop := workerLoopInfo(run); loop {
		// A loop run's guard: the in-process model loop's turn rules (agent_worker_loop.go).
		return s.workerLoopGuardOp(ctx, run, info, input)
	}
	switch input.Op {
	case "start":
		task, err := s.store.AgentTask(ctx, run.WorkspaceKey, run.SessionID)
		if err != nil {
			return disabled, nil // a chat run: no thread, no guard
		}
		request, data, _, _, err := s.builtinRunRequest(r, task)
		if err != nil {
			return disabled, nil
		}
		coding := s.builtinCodingContextFor(request.Context(), task, data)
		environments := make([]domain.CodingEnvironment, 0, len(coding.Environments))
		for _, environment := range coding.Environments {
			environments = append(environments, domain.CodingEnvironment{ID: environment.ID, Name: environment.Name, Repository: environment.Repository})
		}
		coding.Environments = environments
		state := workerRunGuardState{Coding: coding, DecisionRound: -1, HadSession: task.CodingSessionID != ""}
		if len(data.Issues) == 1 {
			state.IssueTitle = data.Issues[0].Title
		}
		raw, err := json.Marshal(state)
		if err != nil {
			return disabled, err
		}
		if err := s.store.ReplaceAgentRunState(ctx, run, workerRunGuardKey, raw); err != nil {
			return disabled, err
		}
		g := state.guard()
		return workerRunGuardResponse{Enabled: true, CodingDue: g.codingDue(), DecisionRound: state.DecisionRound}, nil
	case "finish":
		return s.workerRunGuardFinishOp(r, run, input)
	case "turn", "before", "after", "coding":
	default:
		return disabled, store.ErrAgentRunInput
	}
	// What the step reads besides the guard, read once.
	failed := false
	if input.Op == "after" {
		// A failed call or one that found nothing is a call without progress. A call that ran through /mcp failed
		// when Flow saw an error or an isError result (agent_runtime_connectors.go), whatever its text says.
		failed = input.Failed || s.agentRunConnectorFailed(ctx, run, input.Tool, input.Arguments)
	}
	var task domain.AgentTask
	if input.Op == "coding" {
		var err error
		if task, err = s.store.AgentTask(ctx, run.WorkspaceKey, run.SessionID); err != nil {
			return disabled, err
		}
	}
	call := domain.AgentToolCall{Name: input.Tool, Arguments: json.RawMessage(input.Arguments)}
	// A built-in session run queued by Flow has the in-process run's turn budget and tool budget too: its turn cap is
	// Flow's, and a spent budget withholds the tools for the run's last word (agent_worker_thread.go).
	thread, isThread := workerThreadInfo(run)
	withhold, budgetNudge := false, ""
	if input.Op == "turn" && isThread {
		if thread.MaxTurns > 0 {
			input.MaxTurns = thread.MaxTurns
		}
		withhold, budgetNudge = s.workerThreadBudgetTurn(ctx, run, thread, input.Round)
	}
	for attempt := range agentRunStateAttempts {
		raw, version, found, err := s.agentRunStateOf(ctx, run.ID, workerRunGuardKey)
		if err != nil {
			return disabled, err
		}
		var state workerRunGuardState
		if !found || json.Unmarshal(raw, &state) != nil {
			return disabled, nil // the run never started its guard (a chat run)
		}
		g := state.guard()
		response := workerRunGuardResponse{Enabled: true}
		switch input.Op {
		case "turn":
			maxTurns := input.MaxTurns
			if maxTurns <= 0 {
				maxTurns = s.agentMaxToolTurns()
			}
			if input.Connectors && g.mustDecideWithin(input.Round, maxTurns) {
				response.MustDecide = true
				if !g.decided {
					g.decided, state.DecisionRound = true, input.Round
					response.Nudge = g.decisionNudge()
				}
			}
		case "before":
			response.Content, response.Failed, response.Handled = g.before(call)
		case "after":
			g.after(call, input.Content, failed || connectorFoundNothing([]byte(input.Content)))
		case "coding":
			response.Content = s.recordCodingStart(ctx, task.WorkspaceKey, g, input.Content, input.Failed)
		}
		state.update(g)
		next, err := json.Marshal(state)
		if err != nil {
			return disabled, err
		}
		swapped, err := s.store.SwapAgentRunState(ctx, run, workerRunGuardKey, version, next)
		if err != nil {
			return disabled, err
		}
		if !swapped {
			agentRunStateBackoff(attempt)
			continue
		}
		if input.Op == "before" && response.Handled {
			// Not run: logged so the thread shows it was skipped, as for an in-process run.
			if tool, ok := s.agentRunConnectorTool(ctx, run, input.Tool); ok {
				metadata := connectorActionMetadata(tool, call)
				metadata["result"] = connectorNotRunResult(response.Content)
				_ = s.agentRunActivityAppender(ctx, run)(domain.AgentActivity{Type: "action", Body: tool.RemoteName, Metadata: metadata})
				var args map[string]any
				_ = json.Unmarshal([]byte(input.Arguments), &args)
				s.recordAgentRunConnectorWrite(ctx, run, tool, args, agentWriteNotRun, response.Content)
			}
		}
		response.CodingDue, response.DecisionRound = g.codingDue(), state.DecisionRound
		if input.Op == "turn" {
			response.Withhold, response.Nudge = withhold, response.Nudge+budgetNudge
		}
		return response, nil
	}
	return disabled, errAgentRunStateBusy
}

// workerRunGuardFinishOp is the run's last word (workerRunFinish), run once on the stored guard. The outcome (the
// run finished, was nudged, or entered Coding) is then stored; when another server changed the guard meanwhile,
// the outcome is merged onto the newer guard, whose call counts and results it keeps.
func (s *server) workerRunGuardFinishOp(r *http.Request, run store.AgentRun, input workerRunGuardRequest) (workerRunGuardResponse, error) {
	ctx := r.Context()
	disabled := workerRunGuardResponse{DecisionRound: -1}
	raw, version, found, err := s.agentRunStateOf(ctx, run.ID, workerRunGuardKey)
	if err != nil {
		return disabled, err
	}
	var state workerRunGuardState
	if !found || json.Unmarshal(raw, &state) != nil {
		return disabled, nil
	}
	g := state.guard()
	response := workerRunGuardResponse{Enabled: true}
	reply, transferred, failure, err := s.workerRunFinish(r, run, &state, g, input)
	if err != nil {
		return disabled, err
	}
	switch reply {
	case agentUngroundedOutcomeNudge:
		// Not the last word yet: the model is told once that nothing ran.
		response.Nudge, state.OutcomeNudged = reply, true
	case agentEmptyTurnNudge:
		// An empty reply is asked for again once.
		response.Nudge, state.EmptyNudged = reply, true
	case agentTextApprovalNudge:
		// A reply that asked for approval in text is told once to call the tool instead.
		response.Nudge, state.TextApprovalNudged = reply, true
	default:
		response.Reply, response.Transferred, response.Error = reply, transferred, failure
		state.Finished = true
	}
	state.update(g)
	outcome := state
	for attempt := range agentRunStateAttempts {
		next, err := json.Marshal(state)
		if err != nil {
			return disabled, err
		}
		swapped, err := s.store.SwapAgentRunState(ctx, run, workerRunGuardKey, version, next)
		if err != nil {
			return disabled, err
		}
		if swapped {
			g = state.guard()
			response.CodingDue, response.DecisionRound = g.codingDue(), state.DecisionRound
			return response, nil
		}
		agentRunStateBackoff(attempt)
		raw, version, _, err = s.agentRunStateOf(ctx, run.ID, workerRunGuardKey)
		if err != nil {
			return disabled, err
		}
		var latest workerRunGuardState
		if json.Unmarshal(raw, &latest) != nil {
			return disabled, errAgentRunStateBusy
		}
		latest.Finished = latest.Finished || outcome.Finished
		latest.OutcomeNudged = latest.OutcomeNudged || outcome.OutcomeNudged
		latest.EmptyNudged = latest.EmptyNudged || outcome.EmptyNudged
		latest.TextApprovalNudged = latest.TextApprovalNudged || outcome.TextApprovalNudged
		latest.Decided = latest.Decided || outcome.Decided
		if outcome.Started != nil {
			latest.Started = outcome.Started
		}
		state = latest
	}
	return disabled, errAgentRunStateBusy
}

// workerRunFinish is the last word of a worker run, as executeBuiltinApplication ends a run: a development run
// that has not entered Coding is moved into it (or ends with the blocker), and a "done" claim is replaced with
// the session's real state.
func (s *server) workerRunFinish(r *http.Request, run store.AgentRun, state *workerRunGuardState, g *builtinRunGuard, input workerRunGuardRequest) (reply string, transferred bool, failure string, err error) {
	task, err := s.store.AgentTask(r.Context(), run.WorkspaceKey, run.SessionID)
	if err != nil {
		return "", false, "", err
	}
	request, data, _, _, err := s.builtinRunRequest(r, task)
	if err != nil {
		return "", false, "", nil // the application lost access: nothing is started, the worker keeps its reply
	}
	ctx := request.Context()
	base := s.loopAppURL()
	if base == "" {
		base = agentLinkBaseURL(r)
	}
	rebase := func(text string) string { return rebaseAgentWorkspaceLinks(text, data.Workspace.URLKey, base) }
	transfer := func(reason, text string) (string, bool) {
		appendActivity := func(activity domain.AgentActivity) error {
			activity.ActorID = task.AppUserID
			next, err := appendBuiltinActivity(ctx, s.store, task, activity)
			if err == nil {
				task = next
			}
			return err
		}
		execute := func(call domain.AgentToolCall) ([]byte, error) { return s.executeAgentTool(request, data, call) }
		coding, ok := s.builtinCodingTransfer(ctx, task, g, state.IssueTitle, reason, execute, appendActivity)
		if !ok {
			return "", true
		}
		return rebase(s.builtinReplyWithCoding(ctx, task, g, text, coding)), true
	}
	// A built-in session run queued by Flow (agent_worker_thread.go) ends as executeBuiltinApplication ends a run:
	// an empty reply and a reply asking for approval in text are each sent back once and then fail the run, and once
	// the tool budget is spent the last word is the reply (or Flow's summary) without the outcome check.
	thread, isThread := workerThreadInfo(run)
	stopped := ""
	if isThread {
		stopped = s.workerChatBudget(ctx, run).stop
	}
	lastWord := func(text string) string {
		if g.started != nil && g.started.delivery != codingNotStarted {
			// Coding was requested: the thread reports the session's state.
			return codingDeliveryReply(g.started.delivery, g.started.blocker, g.coding)
		}
		reply := workerThreadLastWord(firstNonEmpty(stopped, agentBudgetTurns), text, thread.MaxTurns, thread.MaxCalls, g.coding.Chinese)
		return rebase(s.agentRunLedger(ctx, run).report(s.builtinFinalReply(ctx, task, g, reply), g.coding.Chinese))
	}
	switch input.Phase {
	case "reply":
		if isThread && stopped == "" {
			if strings.TrimSpace(input.Text) == "" {
				switch {
				case !state.EmptyNudged:
					return agentEmptyTurnNudge, false, "", nil
				case g.codingDue():
					reply, transferred = transfer("the agent replied without starting Coding", "")
					return reply, transferred, "", nil
				}
				return "", false, agentEmptyTurnError(""), nil
			}
			if s.workerRunTextApproval(ctx, run, input.Text) {
				if !state.TextApprovalNudged {
					return agentTextApprovalNudge, false, "", nil
				}
				return "", false, agentTextApprovalError, nil
			}
		}
		if g.codingDue() {
			// A plan or a "done" claim instead of Coding: Flow starts it and reports its real state.
			reply, transferred = transfer("the agent replied without starting Coding", input.Text)
			return reply, transferred, "", nil
		}
		if isThread && stopped != "" {
			return lastWord(input.Text), false, "", nil
		}
		// Outcomes are reported from the run's tool results (agent_run_outcomes.go): an answer that reports
		// outcomes no write produced is sent back once (the nudge), then replaced by Flow's statement.
		ledger := s.agentRunLedger(ctx, run)
		if g.started == nil && task.CodingSessionID == "" && s.agentUngroundedText(builtinOutcomeRequest(task, data), input.Text, ledger) {
			if !state.OutcomeNudged {
				return agentUngroundedOutcomeNudge, false, "", nil
			}
			return agentNothingExecutedText(g.coding.Chinese), false, "", nil
		}
		return rebase(ledger.report(s.builtinFinalReply(ctx, task, g, input.Text), g.coding.Chinese)), false, "", nil
	case "decision":
		if g.codingDue() {
			// The forced-decision turn passed without Coding: Flow starts it instead of running the turn's calls.
			reply, transferred = transfer("the read-only budget was spent without starting Coding", "")
			return reply, transferred, "", nil
		}
		return "", false, "", nil
	case "turnLimit":
		if g.codingDue() {
			reply, transferred = transfer("the run reached its turn limit without starting Coding", "")
			return reply, transferred, "", nil
		}
		if g.started != nil && g.started.delivery != codingNotStarted {
			// Coding was requested: the thread reports the session's state rather than a failed run.
			return codingDeliveryReply(g.started.delivery, g.started.blocker, g.coding), false, "", nil
		}
		if isThread {
			// Past the turn budget without a last word: Flow's summary, as in-process.
			return lastWord(""), false, "", nil
		}
		return "", false, "Agent reached its tool execution limit without starting Coding or reporting a blocker.", nil
	}
	return "", false, "", store.ErrAgentRunInput
}

// workerRunGuardFinished reports whether a worker run already had its last word through the guard (the completion
// check then leaves it alone).
func (s *server) workerRunGuardFinished(ctx context.Context, run store.AgentRun) bool {
	if run.ID == "" {
		return false
	}
	state, found := s.workerRunGuardStateOf(ctx, run.ID)
	return found && (state.Finished || state.Started != nil)
}
