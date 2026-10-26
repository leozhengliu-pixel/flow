package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"

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

// workerRunGuardKey is where a run's guard state is kept (agent_run_keys).
const workerRunGuardKey = "guard:state"

// workerRunGuardLocks serializes the guard operations of one run in this process (parallel tool calls of a turn).
var workerRunGuardLocks sync.Map

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
	// before: handled means the call is not run; content (failed: as an error) is its result.
	Handled bool   `json:"handled,omitempty"`
	Content string `json:"content,omitempty"`
	Failed  bool   `json:"failed,omitempty"`
	// finish: the reply to post (empty: keep the model's own), whether Flow started Coding, or the run's error.
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
	lock, _ := workerRunGuardLocks.LoadOrStore(run.ID, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()
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

func (s *server) workerRunGuardOp(r *http.Request, run store.AgentRun, input workerRunGuardRequest) (workerRunGuardResponse, error) {
	ctx := r.Context()
	disabled := workerRunGuardResponse{DecisionRound: -1}
	var state workerRunGuardState
	if input.Op == "start" {
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
		state = workerRunGuardState{Coding: coding, DecisionRound: -1, HadSession: task.CodingSessionID != ""}
		if len(data.Issues) == 1 {
			state.IssueTitle = data.Issues[0].Title
		}
	} else {
		raw, found, err := s.store.AgentRunKey(ctx, run.ID, workerRunGuardKey)
		if err != nil {
			return disabled, err
		}
		if !found || json.Unmarshal(raw, &state) != nil {
			return disabled, nil // the run never started its guard (a chat run)
		}
	}
	g := state.guard()
	response := workerRunGuardResponse{Enabled: true}
	call := domain.AgentToolCall{Name: input.Tool, Arguments: json.RawMessage(input.Arguments)}
	switch input.Op {
	case "start":
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
		if response.Handled {
			// Not run: logged so the thread shows it was skipped, as for an in-process run.
			if tool, ok := s.agentRunConnectorTool(ctx, run, input.Tool); ok {
				metadata := connectorActionMetadata(tool, call)
				metadata["result"] = connectorNotRunResult(response.Content)
				_ = s.agentRunActivityAppender(ctx, run)(domain.AgentActivity{Type: "action", Body: tool.RemoteName, Metadata: metadata})
			}
		}
	case "after":
		// A failed call or one that found nothing is a call without progress. A call that ran through /mcp failed
		// when Flow saw an error or an isError result (agent_runtime_connectors.go), whatever its text says.
		failed := input.Failed || s.agentRunConnectorFailed(ctx, run, input.Tool, input.Arguments)
		g.after(call, input.Content, failed || connectorFoundNothing([]byte(input.Content)))
	case "coding":
		task, err := s.store.AgentTask(ctx, run.WorkspaceKey, run.SessionID)
		if err != nil {
			return disabled, err
		}
		response.Content = s.recordCodingStart(ctx, task.WorkspaceKey, g, input.Content, input.Failed)
	case "finish":
		reply, transferred, failure, err := s.workerRunFinish(r, run, &state, g, input)
		if err != nil {
			return disabled, err
		}
		response.Reply, response.Transferred, response.Error = reply, transferred, failure
		state.Finished = true
	default:
		return disabled, store.ErrAgentRunInput
	}
	state.update(g)
	raw, err := json.Marshal(state)
	if err != nil {
		return disabled, err
	}
	if err := s.store.ReplaceAgentRunKey(ctx, run, workerRunGuardKey, raw); err != nil {
		return disabled, err
	}
	if input.Op == "finish" {
		workerRunGuardLocks.Delete(run.ID)
	}
	response.CodingDue, response.DecisionRound = g.codingDue(), state.DecisionRound
	return response, nil
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
	switch input.Phase {
	case "reply":
		if g.codingDue() {
			// A plan or a "done" claim instead of Coding: Flow starts it and reports its real state.
			reply, transferred = transfer("the agent replied without starting Coding", input.Text)
			return reply, transferred, "", nil
		}
		return rebase(s.builtinFinalReply(ctx, task, g, input.Text)), false, "", nil
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
	raw, found, err := s.store.AgentRunKey(ctx, run.ID, workerRunGuardKey)
	if err != nil || !found {
		return false
	}
	var state workerRunGuardState
	return json.Unmarshal(raw, &state) == nil && (state.Finished || state.Started != nil)
}
